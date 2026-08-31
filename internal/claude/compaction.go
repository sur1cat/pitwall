package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Compaction is one time a conversation was summarised to fit. It is the most
// expensive thing that happens to a session and the least visible: the tokens
// are re-read from scratch afterwards, the wait is dead time, and what was
// dropped is gone with no way to ask what it was.
type Compaction struct {
	At      time.Time
	Session string
	Project string
	// Dropped is how many tokens went away in this event.
	Dropped int64
	// Before and After bracket the summarisation.
	Before, After int64
	// Stall is how long the session waited for it.
	Stall time.Duration
	// Trigger is what caused it — the automatic threshold or a manual /compact.
	Trigger string
}

// Compactions reads every compaction recorded in the retained transcripts.
func Compactions() []Compaction {
	root := filepath.Join(Dir(), "projects")
	var out []Compaction
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			return nil
		}
		out = append(out, compactionsIn(path)...)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// compactionsIn scans one transcript. Only lines mentioning a compaction are
// unmarshalled, because a transcript can be tens of megabytes.
func compactionsIn(path string) []Compaction {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	session := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	project := projectName(filepath.Base(filepath.Dir(path)))
	var out []Compaction

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), "compact") {
			continue
		}
		var rec struct {
			Timestamp string `json:"timestamp"`
			Subtype   string `json:"subtype"`
			Meta      struct {
				PreTokens               int64  `json:"preTokens"`
				PostTokens              int64  `json:"postTokens"`
				CumulativeDroppedTokens int64  `json:"cumulativeDroppedTokens"`
				DurationMs              int64  `json:"durationMs"`
				Trigger                 string `json:"trigger"`
			} `json:"compactMetadata"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		m := rec.Meta
		if m.PreTokens == 0 && m.CumulativeDroppedTokens == 0 {
			continue
		}
		dropped := m.CumulativeDroppedTokens
		if dropped == 0 && m.PreTokens > m.PostTokens {
			dropped = m.PreTokens - m.PostTokens
		}
		at, _ := time.Parse(time.RFC3339, rec.Timestamp)
		trigger := m.Trigger
		if trigger == "" {
			trigger = "auto"
		}
		out = append(out, Compaction{
			At: at, Session: session, Project: project,
			Dropped: dropped, Before: m.PreTokens, After: m.PostTokens,
			Stall: time.Duration(m.DurationMs) * time.Millisecond, Trigger: trigger,
		})
	}
	return out
}

// projectName turns Claude Code's encoded directory name into something a
// person recognises. The encoding replaces every path separator with a dash
// and cannot be reversed — a dash in a directory name is indistinguishable
// from a separator — so the last segment is the best available answer.
func projectName(encoded string) string {
	parts := strings.Split(strings.Trim(encoded, "-"), "-")
	if len(parts) == 0 {
		return encoded
	}
	return parts[len(parts)-1]
}
