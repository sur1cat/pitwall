package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultCleanupDays is what Claude Code applies when cleanupPeriodDays is not
// set: transcripts older than this are deleted.
const DefaultCleanupDays = 30

// Retention describes how far back transcripts still reach. Everything pitwall
// learns about past work comes from them, so once the archive hits the deletion
// boundary the analysis quietly narrows to a sliding window without saying so.
// history.jsonl is not pruned on the same schedule, which is why prompts can
// outlive the answers they produced.
type Retention struct {
	Oldest time.Time
	Span   time.Duration
	Files  int
	Limit  int  // cleanupPeriodDays as configured
	Set    bool // whether the user configured it at all
}

// Days is the span of retained transcripts.
func (r Retention) Days() float64 { return r.Span.Hours() / 24 }

// EffectiveLimit is the cleanup period actually in force.
func (r Retention) EffectiveLimit() int {
	if r.Set && r.Limit > 0 {
		return r.Limit
	}
	return DefaultCleanupDays
}

// Trimming reports whether the archive has reached its deletion boundary, so
// transcripts are being dropped now rather than merely being old. The two-day
// margin keeps it from flapping on the day the boundary is crossed.
func (r Retention) Trimming() bool {
	return r.Files > 0 && r.Days() >= float64(r.EffectiveLimit())-2
}

// Retain measures the retained transcript window and reads the configured
// cleanup period.
func Retain() Retention {
	var out Retention
	out.Limit = cleanupPeriodDays(&out.Set)
	root := filepath.Join(Dir(), "projects")
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out.Files++
		if out.Oldest.IsZero() || info.ModTime().Before(out.Oldest) {
			out.Oldest = info.ModTime()
		}
		return nil
	})
	if !out.Oldest.IsZero() {
		out.Span = time.Since(out.Oldest)
	}
	return out
}

// cleanupPeriodDays reads the setting from the user settings file, reporting
// through set whether it was present at all — an explicit 30 and an absent key
// behave the same but read very differently to the person being warned.
func cleanupPeriodDays(set *bool) int {
	raw, err := os.ReadFile(filepath.Join(Dir(), "settings.json"))
	if err != nil {
		return 0
	}
	var doc struct {
		CleanupPeriodDays *int `json:"cleanupPeriodDays"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.CleanupPeriodDays == nil {
		return 0
	}
	*set = true
	return *doc.CleanupPeriodDays
}
