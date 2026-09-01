package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/ui"
)

// gateSnapshot is one recorded reading of the server-side switches.
type gateSnapshot struct {
	At    time.Time       `json:"at"`
	Build string          `json:"build,omitempty"`
	Gates map[string]bool `json:"gates"`
}

// cmdDrift answers "did something change under me".
//
// Claude Code's behaviour is partly decided by switches the server turns on
// per account, and when one moves the effect is felt as the model getting
// better or worse for no visible reason — the sentiment behind the April 2026
// postmortem, where effort had silently moved from high to medium for a month.
// The community concluded this could not be checked locally. It can: the
// switches are cached in ~/.claude.json, so recording them turns a feeling
// into a diff.
//
// What the names mean is not knowable from here. Some are legible
// (tengu_compact_cache_prefix) and most are codenames, so this reports that
// something moved and when, not what it does.
func cmdDrift(args []string) error {
	var asJSON, noColor bool
	fs := flag.NewFlagSet("drift", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(driftUsage) }
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	if err := fs.Parse(hoistFlags(fs, args)); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	cfg, ok := claude.ReadConfig()
	if !ok {
		fmt.Println("No feature gates found — ~/.claude.json is absent or in an unfamiliar shape.")
		return nil
	}
	history := loadGateHistory()
	now := gateSnapshot{At: time.Now(), Gates: cfg.Gates}

	var diffs []struct {
		From, To time.Time
		Diff     claude.GateDiff
	}
	for i := 1; i < len(history); i++ {
		d := claude.CompareGates(history[i-1].Gates, history[i].Gates)
		if d.Any() {
			diffs = append(diffs, struct {
				From, To time.Time
				Diff     claude.GateDiff
			}{history[i-1].At, history[i].At, d})
		}
	}
	var latest claude.GateDiff
	if len(history) > 0 {
		latest = claude.CompareGates(history[len(history)-1].Gates, now.Gates)
	}

	if err := saveGateSnapshot(history, now); err != nil && !asJSON {
		fmt.Printf("  %s %v\n", ui.Gray("note: could not record this reading —"), err)
	}

	if asJSON {
		return emitJSON(map[string]any{
			"gates_on": countOn(cfg.Gates), "gates_total": len(cfg.Gates),
			"readings": len(history) + 1, "since_last": latest, "changes": diffs,
		})
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall drift"),
		ui.Gray(fmt.Sprintf("%d of %d server-side switches are on for your account",
			countOn(cfg.Gates), len(cfg.Gates))))

	if len(history) == 0 {
		fmt.Printf("  %s\n", ui.Gray("first reading recorded — run this again after something feels different"))
		fmt.Printf("  %s\n", ui.Gray("and it will say what moved in between"))
		return nil
	}
	if latest.Any() {
		fmt.Printf("%s %s\n", ui.Yellow("since your last reading"),
			ui.Gray(ui.Duration(time.Since(history[len(history)-1].At))+" ago"))
		printGateDiff(latest)
		fmt.Println()
	}
	if len(diffs) == 0 && !latest.Any() {
		fmt.Printf("  %s\n", ui.Green("nothing has moved across "+fmt.Sprintf("%d readings", len(history))))
		return nil
	}
	for i := len(diffs) - 1; i >= 0 && i > len(diffs)-6; i-- {
		d := diffs[i]
		fmt.Printf("%s\n", ui.Gray(d.To.Format("2 Jan 15:04")))
		printGateDiff(d.Diff)
		fmt.Println()
	}
	fmt.Printf("  %s\n", ui.Gray("what a switch does is not knowable from here — this says that one moved, and when"))
	return nil
}

func printGateDiff(d claude.GateDiff) {
	for _, g := range d.TurnedOn {
		fmt.Printf("  %s %s\n", ui.Green("on  "), g)
	}
	for _, g := range d.TurnedOff {
		fmt.Printf("  %s %s\n", ui.Red("off "), g)
	}
	for _, g := range d.Appeared {
		fmt.Printf("  %s %s\n", ui.Gray("new "), g)
	}
	for _, g := range d.Vanished {
		fmt.Printf("  %s %s\n", ui.Gray("gone"), g)
	}
}

func countOn(g map[string]bool) int {
	n := 0
	for _, v := range g {
		if v {
			n++
		}
	}
	return n
}

func gateHistoryPath() string {
	return filepath.Join(claude.Dir(), "pitwall", "gates.jsonl")
}

// loadGateHistory reads the readings recorded so far, oldest first.
func loadGateHistory() []gateSnapshot {
	f, err := os.Open(gateHistoryPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []gateSnapshot
	dec := json.NewDecoder(f)
	for {
		var s gateSnapshot
		if dec.Decode(&s) != nil {
			break
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// saveGateSnapshot appends a reading, but only when something actually moved
// or enough time has passed — a file with one line per invocation would bury
// the changes it exists to show.
func saveGateSnapshot(history []gateSnapshot, now gateSnapshot) error {
	if n := len(history); n > 0 {
		last := history[n-1]
		if !claude.CompareGates(last.Gates, now.Gates).Any() && time.Since(last.At) < 24*time.Hour {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(gateHistoryPath()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(gateHistoryPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(now)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

const driftUsage = `pitwall drift — what changed under you

Claude Code's behaviour is partly decided by switches the server turns on per
account. When one moves, the effect is felt as the model getting better or
worse for no visible reason: the April 2026 postmortem found effort had
silently moved from high to medium for a month. The switches are cached in
~/.claude.json, so recording them turns that feeling into a diff.

Usage:
  pitwall drift        what has moved since the readings before

Flags:
      --json      machine-readable output
      --no-color  disable ANSI color

Run it now to record a baseline, and again when something feels different.
What a switch does is not knowable from here — most are codenames — so this
says that one moved and when, not what it means.
`
