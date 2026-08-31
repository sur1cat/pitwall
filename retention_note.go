package main

import (
	"fmt"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/ui"
)

// printRetentionNote warns when the transcript archive has reached its deletion
// boundary. Every history-derived number pitwall prints is computed over that
// window, so a silently sliding window makes the analysis quietly narrower than
// the user believes it to be.
func printRetentionNote(r claude.Retention) {
	if !r.Trimming() {
		return
	}
	how := fmt.Sprintf("the default %d days", claude.DefaultCleanupDays)
	if r.Set {
		how = fmt.Sprintf("cleanupPeriodDays = %d", r.Limit)
	}
	fmt.Printf("  %s transcripts only reach back %.0f days — Claude Code deletes older ones (%s).\n",
		ui.Yellow("window:"), r.Days(), how)
	fmt.Printf("           %s\n", ui.Gray(
		`raise it with '"cleanupPeriodDays": 365' in ~/.claude/settings.json; prompts in history.jsonl outlive the answers`))
}
