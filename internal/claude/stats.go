package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Stats is Claude Code's own rolling summary of your usage, kept in
// stats-cache.json.
//
// It matters because it outlives the transcripts. Claude Code deletes those
// after cleanupPeriodDays, taking every number derived from them with it,
// while this file keeps lifetime totals per model and a daily series going
// back to the first session. On the machine this was built against it holds
// 50.8B cache-read tokens against the 32.8B still readable from transcripts.
//
// It is undocumented, so it is read defensively: a missing file, an unknown
// version or a changed shape yields no reading rather than a wrong one, and
// anything derived from it says where it came from.
type Stats struct {
	Version       int
	FirstSession  time.Time
	LastComputed  time.Time
	TotalSessions int
	TotalMessages int
	// ByModel is lifetime token counts keyed by model id.
	ByModel map[string]ModelTotals
	// Daily is the per-day series, oldest first.
	Daily []DayTotals
}

// ModelTotals is what one model has consumed over the file's whole span.
type ModelTotals struct {
	Input       int64 `json:"inputTokens"`
	Output      int64 `json:"outputTokens"`
	CacheRead   int64 `json:"cacheReadInputTokens"`
	CacheCreate int64 `json:"cacheCreationInputTokens"`
}

// Total is every token the model consumed.
func (m ModelTotals) Total() int64 { return m.Input + m.Output + m.CacheRead + m.CacheCreate }

// DayTotals is one day of activity.
type DayTotals struct {
	Date      string
	Messages  int
	Sessions  int
	ToolCalls int
}

// statsVersionsRead are the file layouts this code understands. A newer one is
// declined rather than guessed at.
var statsVersionsRead = map[int]bool{5: true}

// ReadStats loads Claude Code's usage summary, reporting false when it is
// absent or in a shape this code does not know.
func ReadStats() (Stats, bool) {
	raw, err := os.ReadFile(filepath.Join(Dir(), "stats-cache.json"))
	if err != nil {
		return Stats{}, false
	}
	var doc struct {
		Version          int                    `json:"version"`
		FirstSessionDate string                 `json:"firstSessionDate"`
		LastComputedDate string                 `json:"lastComputedDate"`
		TotalSessions    int                    `json:"totalSessions"`
		TotalMessages    int                    `json:"totalMessages"`
		ModelUsage       map[string]ModelTotals `json:"modelUsage"`
		DailyActivity    []struct {
			Date          string `json:"date"`
			MessageCount  int    `json:"messageCount"`
			SessionCount  int    `json:"sessionCount"`
			ToolCallCount int    `json:"toolCallCount"`
		} `json:"dailyActivity"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return Stats{}, false
	}
	if !statsVersionsRead[doc.Version] || len(doc.ModelUsage) == 0 {
		return Stats{}, false
	}

	s := Stats{
		Version:       doc.Version,
		TotalSessions: doc.TotalSessions,
		TotalMessages: doc.TotalMessages,
		ByModel:       doc.ModelUsage,
	}
	s.FirstSession, _ = time.Parse(time.RFC3339, doc.FirstSessionDate)
	s.LastComputed, _ = time.Parse("2006-01-02", doc.LastComputedDate)
	for _, d := range doc.DailyActivity {
		s.Daily = append(s.Daily, DayTotals{
			Date: d.Date, Messages: d.MessageCount,
			Sessions: d.SessionCount, ToolCalls: d.ToolCallCount,
		})
	}
	sort.Slice(s.Daily, func(i, j int) bool { return s.Daily[i].Date < s.Daily[j].Date })
	return s, true
}

// Span is how long this summary covers.
func (s Stats) Span() time.Duration {
	if s.FirstSession.IsZero() {
		return 0
	}
	return time.Since(s.FirstSession)
}
