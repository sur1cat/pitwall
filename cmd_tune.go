package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/ui"
)

// tuning is one setting worth considering, with the measurement behind it.
type tuning struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Because  string `json:"because"`
	Tradeoff string `json:"tradeoff"`
	Set      bool   `json:"already_set"`
}

// cmdTune recommends settings from what your own usage measures.
//
// Claude Code has knobs for the two things people complain about most — what a
// session loads before you type, and when a conversation is compacted — and
// they are documented in a reference nobody reads with numbers nobody has.
// pitwall has the numbers, so it can say which knob is worth turning and what
// turning it costs.
func cmdTune(args []string) error {
	var asJSON, noColor, write bool
	fs := flag.NewFlagSet("tune", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(tuneUsage) }
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	fs.BoolVar(&write, "write", false, "apply these to ~/.claude/settings.json")
	if err := fs.Parse(hoistFlags(args)); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	current := currentSettings()
	var out []tuning

	if starts := claude.Startups(); len(starts) >= 5 {
		median := claude.MedianStartup(starts)
		// Below about 20k there is nothing worth trimming; the floor is the
		// system prompt and the tool definitions.
		if median > 20_000 {
			for _, t := range []tuning{
				{Key: "disableBundledSkills", Value: true,
					Because:  fmt.Sprintf("sessions open at a median of %s before you type", tokens(median)),
					Tradeoff: "the skills that ship with Claude Code stop loading; your own still work"},
				{Key: "autoMemoryEnabled", Value: false,
					Because:  "auto-memory is read into every session opening",
					Tradeoff: "Claude stops recalling notes it wrote itself between sessions"},
				{Key: "includeGitInstructions", Value: false,
					Because:  "the built-in commit and PR instructions are in every system prompt",
					Tradeoff: "you explain your own commit conventions instead"},
			} {
				t.Set = has(current, t.Key)
				out = append(out, t)
			}
		}
	}

	if events := claude.Compactions(); len(events) > 0 {
		var dropped int64
		for _, e := range events {
			dropped += e.Dropped
		}
		out = append(out, tuning{
			Key: "autoCompactWindow", Value: 600000,
			Because: fmt.Sprintf("%d compactions dropped %s, each firing only at the very edge of the window",
				len(events), tokens(dropped)),
			Tradeoff: "compaction happens sooner and more often, losing less each time and at a point you chose",
			Set:      has(current, "autoCompactWindow"),
		})
	}

	if r := claude.Retain(); r.Trimming() && !r.Set {
		out = append(out, tuning{
			Key: "cleanupPeriodDays", Value: 365,
			Because:  fmt.Sprintf("transcripts reach back only %.0f days and are being deleted", r.Days()),
			Tradeoff: "the transcript directory keeps growing; it is text and compresses well",
			Set:      false,
		})
	}

	if asJSON {
		return emitJSON(map[string]any{"suggestions": out, "settings": shortPath(userSettingsPath())})
	}
	if len(out) == 0 {
		fmt.Println("Nothing worth changing — what you load and what you keep are already in hand.")
		return nil
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall tune"),
		ui.Gray("settings worth considering, from what your own usage measures"))
	for _, t := range out {
		mark := ui.Green("+")
		if t.Set {
			mark = ui.Gray("·")
		}
		fmt.Printf("  %s %s\n", mark, ui.Bold(fmt.Sprintf("%s: %v", t.Key, t.Value)))
		fmt.Printf("      %s\n", ui.Gray(t.Because))
		fmt.Printf("      %s %s\n", ui.Gray("costs you:"), ui.Gray(t.Tradeoff))
		if t.Set {
			fmt.Printf("      %s\n", ui.Gray("already set in your settings — left alone"))
		}
		fmt.Println()
	}
	fmt.Printf("  %s\n", ui.Gray("none of these is free; each trades something for what it saves"))
	if !write {
		fmt.Printf("  %s\n", ui.Gray("pitwall tune --write applies the ones you do not already have set"))
		return nil
	}
	return applyTunings(out, current)
}

// applyTunings writes the suggestions that are not already set, leaving every
// other key alone. A settings file that fails to parse stops Claude Code from
// starting, so the original is copied first and the result is checked before
// it is written.
func applyTunings(all []tuning, current map[string]any) error {
	var added []string
	for _, t := range all {
		if t.Set {
			continue
		}
		current[t.Key] = t.Value
		added = append(added, t.Key)
	}
	if len(added) == 0 {
		fmt.Printf("\n  %s\n", ui.Gray("everything suggested is already set — nothing written"))
		return nil
	}

	out, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	var probe map[string]any
	if json.Unmarshal(out, &probe) != nil {
		return fmt.Errorf("refusing to write settings that do not parse")
	}

	path := userSettingsPath()
	if raw, err := os.ReadFile(path); err == nil {
		backup := path + ".pitwall-backup-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return err
		}
		fmt.Printf("\n  %s %s\n", ui.Gray("backed up to"), ui.Gray(shortPath(backup)))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	fmt.Printf("  %s %d settings written to %s\n", ui.Green("done:"), len(added), shortPath(path))
	fmt.Printf("  %s\n", ui.Gray("they take effect in sessions started from now on"))
	return nil
}

func userSettingsPath() string { return filepath.Join(claude.Dir(), "settings.json") }

// currentSettings reads what is already configured, so advice is not given
// about a knob the user has already turned.
func currentSettings() map[string]any {
	raw, err := os.ReadFile(userSettingsPath())
	if err != nil {
		return map[string]any{}
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return map[string]any{}
	}
	return doc
}

func has(doc map[string]any, key string) bool { _, ok := doc[key]; return ok }

const tuneUsage = `pitwall tune — settings worth considering, from your own usage

Claude Code has knobs for the two things people complain about most: what a
session loads before you type, and when a conversation is compacted. They are
documented in a reference nobody reads, with numbers nobody has. pitwall has
the numbers.

Usage:
  pitwall tune           what is worth changing, and what each change costs
  pitwall tune --write   apply the ones not already set

Flags:
      --write     write them to ~/.claude/settings.json, backing it up first
      --json      machine-readable output
      --no-color  disable ANSI color

Nothing here is free. Every suggestion says what it trades away, because a
setting that only saves money is one you would already have found.
`
