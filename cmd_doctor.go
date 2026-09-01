package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/ui"
)

// check is one thing that either works or explains why it does not.
type check struct {
	Name   string `json:"name"`
	State  string `json:"state"` // ok, warn, fail
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// cmdDoctor answers "why is it not showing me anything". Every number pitwall
// prints comes from a file it has to find and read, and when one of those is
// missing the failure looks like an empty screen rather than an error. This
// walks the whole chain and names the first link that is broken.
func cmdDoctor(args []string) error {
	var asJSON, noColor bool
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	if err := fs.Parse(hoistFlags(args)); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	checks := []check{
		checkConfigDir(),
		checkTranscripts(),
		checkHistory(),
		checkStats(),
		checkGates(),
		checkDisk(),
		checkSessions(),
		checkSettings(),
		checkCache(),
		checkGit(),
		checkCredential(),
	}

	if asJSON {
		return emitJSON(map[string]any{"checks": checks})
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall doctor"),
		ui.Gray("what pitwall can and cannot read on this machine"))
	worst := "ok"
	for _, c := range checks {
		fmt.Printf("  %s %-22s %s\n", mark(c.State), c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Printf("    %s %s\n", ui.Gray("→"), ui.Gray(c.Fix))
		}
		if c.State == "fail" || (c.State == "warn" && worst == "ok") {
			worst = c.State
		}
	}
	fmt.Println()
	switch worst {
	case "fail":
		fmt.Printf("  %s\n", ui.Red("something pitwall needs is missing — the lines marked ✗ say what"))
	case "warn":
		fmt.Printf("  %s\n", ui.Yellow("pitwall works, but some numbers will be narrower than you expect"))
	default:
		fmt.Printf("  %s\n", ui.Green("everything pitwall reads is where it expects it"))
	}
	return nil
}

func mark(state string) string {
	switch state {
	case "fail":
		return ui.Red("✗")
	case "warn":
		return ui.Yellow("!")
	default:
		return ui.Green("✓")
	}
}

func checkConfigDir() check {
	dir := claude.Dir()
	st, err := os.Stat(dir)
	if err != nil {
		return check{"config directory", "fail",
			fmt.Sprintf("%s does not exist", shortPath(dir)),
			"Claude Code has not run on this machine, or CLAUDE_CONFIG_DIR points elsewhere"}
	}
	if !st.IsDir() {
		return check{"config directory", "fail", shortPath(dir) + " is not a directory", ""}
	}
	if f, err := os.Open(dir); err != nil {
		return check{"config directory", "fail",
			fmt.Sprintf("%s cannot be read: %v", shortPath(dir), err),
			"check the permissions on the directory"}
	} else {
		f.Close()
	}
	note := shortPath(dir)
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		note += "  (from CLAUDE_CONFIG_DIR)"
	}
	return check{"config directory", "ok", note, ""}
}

func checkTranscripts() check {
	r := claude.Retain()
	if r.Files == 0 {
		return check{"transcripts", "fail",
			"none found under projects/",
			"burn, coach and perms read these; they appear after Claude Code runs a session"}
	}
	detail := fmt.Sprintf("%d files, reaching back %.0f days", r.Files, r.Days())
	if r.Trimming() {
		return check{"transcripts", "warn", detail,
			fmt.Sprintf("Claude Code deletes them past %d days — raise cleanupPeriodDays in settings.json", r.EffectiveLimit())}
	}
	return check{"transcripts", "ok", detail, ""}
}

func checkHistory() check {
	p := filepath.Join(claude.Dir(), "history.jsonl")
	st, err := os.Stat(p)
	if err != nil {
		return check{"prompt history", "warn", "history.jsonl not found",
			"perms and primer find your projects through it; without it they see recent ones only"}
	}
	return check{"prompt history", "ok",
		fmt.Sprintf("%s, last written %s ago", ui.Bytes(st.Size()),
			ui.Duration(time.Since(st.ModTime()))), ""}
}

// checkStats reports on Claude Code's own usage summary, which outlives the
// transcripts and is the only record of what was spent before they were
// pruned.
func checkStats() check {
	st, ok := claude.ReadStats()
	if !ok {
		return check{"usage summary", "warn", "stats-cache.json is absent or in an unfamiliar shape",
			"only the all-time figure needs it; everything else reads transcripts"}
	}
	return check{"usage summary", "ok",
		fmt.Sprintf("%d sessions over %.0f days, outliving the transcripts",
			st.TotalSessions, st.Span().Hours()/24), ""}
}

// checkGates reports on the server-side switches, which decide part of how
// Claude Code behaves and are the only local evidence that something changed
// under you.
func checkGates() check {
	cfg, ok := claude.ReadConfig()
	if !ok {
		return check{"feature gates", "warn", "~/.claude.json is absent or in an unfamiliar shape",
			"only pitwall drift needs it"}
	}
	on := 0
	for _, v := range cfg.Gates {
		if v {
			on++
		}
	}
	return check{"feature gates", "ok",
		fmt.Sprintf("%d of %d on, %d skills counted", on, len(cfg.Gates), len(cfg.Skills)), ""}
}

// checkDisk reports what Claude Code keeps in its own directory, which nothing
// else accounts for. The worktree scan covers what agents leave in
// repositories and says nothing about this, and here the two are the same
// order of magnitude.
func checkDisk() check {
	parts := claude.Disk()
	if len(parts) == 0 {
		return check{"disk", "ok", "nothing measurable yet", ""}
	}
	total, stale := claude.TotalDisk(parts)
	detail := fmt.Sprintf("%s in %s, largest is %s at %s",
		ui.Bytes(total), shortPath(claude.Dir()), parts[0].Name, ui.Bytes(parts[0].Bytes))
	if stale == 0 {
		return check{"disk", "ok", detail, ""}
	}
	for _, p := range parts {
		if p.Stale > 0 {
			return check{"disk", "warn", detail,
				fmt.Sprintf("%s of it is %s — %s", ui.Bytes(stale), p.Name, p.Why)}
		}
	}
	return check{"disk", "ok", detail, ""}
}

func checkSessions() check {
	live := claude.Sessions()
	if len(live) == 0 {
		return check{"live sessions", "ok", "none running", ""}
	}
	return check{"live sessions", "ok", fmt.Sprintf("%d running", len(live)), ""}
}

func checkSettings() check {
	var bad []string
	var seen int
	for _, p := range settingsFiles() {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue // absent is not the same as broken
		}
		seen++
		var doc map[string]any
		if json.Unmarshal(raw, &doc) != nil {
			bad = append(bad, shortPath(p))
		}
	}
	if len(bad) > 0 {
		return check{"settings files", "fail",
			fmt.Sprintf("%d of %d do not parse: %s", len(bad), seen, strings.Join(bad, ", ")),
			"Claude Code refuses a settings file it cannot read; fix the JSON"}
	}
	if seen == 0 {
		return check{"settings files", "ok", "none written yet", ""}
	}
	return check{"settings files", "ok", fmt.Sprintf("%d found, all parse", seen), ""}
}

// settingsFiles lists the settings files pitwall reads, user and per project.
func settingsFiles() []string {
	out := []string{filepath.Join(claude.Dir(), "settings.json")}
	dirs := map[string]bool{}
	for _, d := range append(claude.Workdirs(), claude.HistoryDirs()...) {
		dirs[d] = true
	}
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Strings(list)
	for _, d := range list {
		for _, n := range []string{"settings.json", "settings.local.json"} {
			p := filepath.Join(d, ".claude", n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				out = append(out, p)
			}
		}
	}
	return out
}

func checkCache() check {
	dir := filepath.Join(claude.Dir(), "pitwall")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return check{"cache", "warn", "cannot create " + shortPath(dir),
			"pitwall still works, but every scan re-reads everything"}
	}
	probe := filepath.Join(dir, ".doctor-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		return check{"cache", "warn", "cannot write to " + shortPath(dir),
			"pitwall still works, but every scan re-reads everything"}
	}
	os.Remove(probe)
	return check{"cache", "ok", "writable at " + shortPath(dir), ""}
}

func checkGit() check {
	if _, err := exec.LookPath("git"); err != nil {
		return check{"git", "warn", "not on PATH",
			"only pitwall tree needs it; everything else works without"}
	}
	return check{"git", "ok", "available", ""}
}

func checkCredential() check {
	dir := filepath.Join(claude.Dir(), "pitwall")
	if _, err := os.Stat(filepath.Join(dir, "quota-cache.json")); err == nil {
		return check{"plan credential", "ok", "a reading has been fetched before", ""}
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
		return check{"plan credential", "ok", "CLAUDE_CODE_OAUTH_TOKEN is set", ""}
	}
	if _, err := os.Stat(filepath.Join(claude.Dir(), ".credentials.json")); err == nil {
		return check{"plan credential", "ok", "stored in .credentials.json", ""}
	}
	return check{"plan credential", "warn", "not found yet",
		"only pitwall quota needs it; on macOS it is in the Keychain and is asked for once"}
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
