package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sur1cat/pitwall/internal/burn"
	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/quota"
	"github.com/sur1cat/pitwall/internal/ui"
)

type burnGlobals struct {
	since   time.Duration
	project string
	asJSON  bool
	noCache bool
	noColor bool
	limit   float64
}

func (g *burnGlobals) bind(fs *flag.FlagSet) {
	fs.Var(sinceFlag{&g.since}, "since", "only count the last span (30d, 2w, 12h)")
	fs.StringVar(&g.project, "project", "", "only count this project")
	fs.BoolVar(&g.asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&g.noCache, "no-cache", false, "re-read every transcript")
	fs.BoolVar(&g.noColor, "no-color", false, "disable ANSI color")
	fs.Float64Var(&g.limit, "limit", burnEnvLimit(), "5-hour spend budget in USD")
}

func burnEnvLimit() float64 {
	for _, key := range []string{"PITWALL_LIMIT", "CBURN_LIMIT"} {
		if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
			return v
		}
	}
	return 0
}

func (g *burnGlobals) load() ([]burn.Record, burn.Report) {
	if g.noColor {
		ui.SetColor(false)
	}
	_ = burn.Load(filepath.Join(claude.Dir(), "pitwall", "pricing.json"))
	rep := burn.Scan(!g.noCache)

	records := rep.Records
	if g.since > 0 {
		records = burnAfter(records, time.Now().Add(-g.since))
	}
	if g.project != "" {
		var kept []burn.Record
		for _, r := range records {
			if strings.EqualFold(r.Project, g.project) {
				kept = append(kept, r)
			}
		}
		records = kept
	}
	return records, rep
}

func burnFlags(name string, g *burnGlobals) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(burnUsage) }
	g.bind(fs)
	return fs
}

// burnTotal is a usage and cost rollup.
type burnTotal struct {
	Usage    burn.Usage `json:"usage"`
	Cost     burn.Cost  `json:"cost"`
	Messages int        `json:"messages"`
	Total    float64    `json:"total"`
}

// Dollars is the burnTotal spend of the rollup.
func (t burnTotal) Dollars() float64 { return t.Cost.Total() }

func burnSum(records []burn.Record) burnTotal {
	var t burnTotal
	for _, r := range records {
		t.Usage.Add(r.Usage)
		t.Messages += r.Messages
		if c, ok := burn.Compute(r.Model, r.Hour, r.Usage); ok {
			t.Cost.Add(c)
		}
	}
	t.Total = t.Cost.Total()
	return t
}

func burnAfter(records []burn.Record, cutoff time.Time) []burn.Record {
	var out []burn.Record
	for _, r := range records {
		if !r.Hour.Before(cutoff.Truncate(time.Hour)) {
			out = append(out, r)
		}
	}
	return out
}

func burnSummary(args []string) error {
	var g burnGlobals
	fs := burnFlags("summary", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	records, rep := g.load()
	if len(records) == 0 {
		fmt.Println("No usage found. Is Claude Code installed and has it been used?")
		return nil
	}

	now := time.Now()
	windows := []struct {
		label string
		since time.Time
	}{
		{"last 5 hours", now.Add(-5 * time.Hour)},
		{"today", time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())},
		{"last 7 days", now.AddDate(0, 0, -7)},
		{"all time", time.Time{}},
	}

	if g.asJSON {
		out := map[string]any{"duplicates_skipped": rep.Duplicates, "unknown_models": rep.Unknown}
		for _, w := range windows {
			out[w.label] = burnSum(burnAfter(records, w.since))
		}
		out["by_model"] = burnGroups(records, func(r burn.Record) string { return r.Model })
		out["by_effort"] = burnGroups(records, burnEffortOf)
		out["by_agent"] = burnGroups(records, burnAgentOf)
		return emitJSON(out)
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall burn"),
		ui.Gray(fmt.Sprintf("%d transcripts · %d served from cache", rep.Files, rep.Cached)))

	var t ui.Table
	t.Row(ui.Gray("window"), ui.Gray("tokens"), ui.Gray("cost"), "")
	for _, w := range windows {
		s := burnSum(burnAfter(records, w.since))
		extra := ""
		if w.label == "last 5 hours" {
			extra = burnRate(s, 5*time.Hour, g.limit)
		}
		t.Row(w.label, tokens(s.Usage.Total()), money(s.Dollars()), extra)
	}
	fmt.Print(t.Render("  "))

	all := burnSum(records)
	burnPrintGroup("by model", burnGroups(records, func(r burn.Record) string { return r.Model }), all, 6)
	burnPrintGroup("by effort", burnGroups(records, burnEffortOf), all, 6)
	burnPrintGroup("by agent", burnGroups(records, burnAgentOf), all, 2)

	if all.Usage.CacheRead > 0 {
		var billed, uncached float64
		for _, r := range records {
			if c, ok := burn.Compute(r.Model, r.Hour, burn.Usage{Input: r.Usage.CacheRead}); ok {
				uncached += c.Input
				billed += c.Input * burn.CacheRead
			}
		}
		share := float64(all.Usage.CacheRead) / float64(max64(all.Usage.Total(), 1)) * 100
		fmt.Printf("\n  %s %s read from cache (%.0f%% of all tokens) — billed %s instead of %s\n",
			ui.Green("cache:"), tokens(all.Usage.CacheRead), share,
			ui.Green(money(billed)), ui.Gray(money(uncached)))
	}
	fmt.Printf("  %s\n", ui.Gray("costs are Anthropic API list rates — on a subscription this is value consumed, not an invoice"))
	if len(rep.Unknown) > 0 {
		fmt.Printf("  %s tokens counted, cost unknown: %s (add rates in ~/.claude/pitwall/pricing.json)\n",
			ui.Yellow("note:"), strings.Join(rep.Unknown, ", "))
	}
	if rep.Duplicates > 0 {
		fmt.Printf("  %s\n", ui.Gray(fmt.Sprintf("%d replayed messages skipped (session forks and resumes)", rep.Duplicates)))
	}
	printCompactionNote()
	printRetentionNote(claude.Retain())
	return nil
}

// printCompactionNote reports what summarising the conversation has cost. It
// is the most expensive thing that happens to a session and the least visible:
// the tokens are re-read from scratch afterwards, the wait is dead time, and
// what was dropped is gone with no way to ask what it was.
func printCompactionNote() {
	events := claude.Compactions()
	if len(events) == 0 {
		return
	}
	var dropped int64
	var stall time.Duration
	sessions := map[string]bool{}
	worst := claude.Compaction{}
	for _, e := range events {
		dropped += e.Dropped
		stall += e.Stall
		sessions[e.Session] = true
		if e.Dropped > worst.Dropped {
			worst = e
		}
	}
	fmt.Printf("  %s %d compactions across %d sessions dropped %s and waited %s\n",
		ui.Yellow("context:"), len(events), len(sessions), tokens(dropped), ui.Duration(stall))
	if worst.Dropped > 0 {
		fmt.Printf("           %s\n", ui.Gray(fmt.Sprintf(
			"the worst dropped %s in %s", tokens(worst.Dropped), worst.Project)))
	}
}

// burnRate renders the current spend rate and, when a budget is set, a burnMeter.
func burnRate(s burnTotal, window time.Duration, limit float64) string {
	rate := s.Dollars() / window.Hours()
	out := ui.Gray(fmt.Sprintf("burn %s/h", money(rate)))
	if limit > 0 {
		frac := s.Dollars() / limit
		out = burnMeter(frac) + " " + out
	}
	return out
}

func burnMeter(frac float64) string {
	const width = 10
	if frac < 0 {
		frac = 0
	}
	filled := int(frac * width)
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("▓", filled) + strings.Repeat("░", width-filled)
	switch {
	case frac >= 0.9:
		return ui.Red(bar)
	case frac >= 0.65:
		return ui.Yellow(bar)
	default:
		return ui.Green(bar)
	}
}

// burnAgentOf splits the main conversation from the subagents it spawns.
// Subagent transcripts live in a subagents/ directory and carry isSidechain,
// and they are close to half of all messages — worth a line of its own rather
// than a column in a CSV nobody exports.
func burnAgentOf(r burn.Record) string {
	if r.Sub {
		return "subagents"
	}
	return "main agent"
}

func burnEffortOf(r burn.Record) string {
	if r.Effort == "" {
		return "(unset)"
	}
	return r.Effort
}

func burnGroups(records []burn.Record, key func(burn.Record) string) map[string]burnTotal {
	out := map[string]burnTotal{}
	for _, r := range records {
		k := key(r)
		t := out[k]
		t.Usage.Add(r.Usage)
		t.Messages += r.Messages
		if c, ok := burn.Compute(r.Model, r.Hour, r.Usage); ok {
			t.Cost.Add(c)
		}
		out[k] = t
	}
	return out
}

func burnPrintGroup(title string, groups map[string]burnTotal, all burnTotal, limit int) {
	if len(groups) == 0 {
		return
	}
	type row struct {
		name string
		t    burnTotal
	}
	rows := make([]row, 0, len(groups))
	for k, v := range groups {
		rows = append(rows, row{k, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].t.Dollars() > rows[j].t.Dollars() })
	if len(rows) > limit {
		rows = rows[:limit]
	}

	fmt.Printf("\n%s\n", ui.Bold(title))
	var t ui.Table
	for _, r := range rows {
		share := ""
		if all.Dollars() > 0 {
			share = ui.Gray(fmt.Sprintf("%4.1f%%", r.t.Dollars()/all.Dollars()*100))
		}
		per := ""
		if r.t.Messages > 0 {
			per = ui.Gray(fmt.Sprintf("%s / message", money(r.t.Dollars()/float64(r.t.Messages))))
		}
		t.Row(r.name, money(r.t.Dollars()), share, tokens(r.t.Usage.Total()), per)
	}
	fmt.Print(t.Render("  "))
}

func burnTop(args []string) error {
	var g burnGlobals
	var by string
	var n int
	fs := burnFlags("top", &g)
	fs.StringVar(&by, "by", "project", "project | session | model | effort | day")
	fs.IntVar(&n, "n", 10, "how many rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	records, _ := g.load()

	var key func(burn.Record) string
	switch by {
	case "project":
		key = func(r burn.Record) string { return orDash(r.Project) }
	case "session":
		key = func(r burn.Record) string { return orDash(r.Session) }
	case "model":
		key = func(r burn.Record) string { return r.Model }
	case "effort":
		key = burnEffortOf
	case "day":
		key = func(r burn.Record) string { return r.Hour.Local().Format("2006-01-02") }
	default:
		return fmt.Errorf("unknown --by %q", by)
	}

	groups := burnGroups(records, key)
	if g.asJSON {
		return emitJSON(map[string]any{"by": by, "groups": groups})
	}
	fmt.Printf("%s  %s\n", ui.Bold("pitwall burn top"), ui.Gray("by "+by))
	burnPrintGroup("", groups, burnSum(records), n)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func burnWatch(args []string) error {
	var g burnGlobals
	var interval time.Duration
	fs := burnFlags("watch", &g)
	fs.DurationVar(&interval, "interval", 30*time.Second, "refresh interval")
	fs.DurationVar(&interval, "n", 30*time.Second, "shorthand for --interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer fmt.Print("\033[?25h")
	fmt.Print("\033[?25l")

	for {
		fmt.Print("\033[H\033[2J")
		if err := burnSummary(nil); err != nil {
			return err
		}
		fmt.Printf("\n%s\n", ui.Gray(fmt.Sprintf("refreshing every %s · ctrl-c to stop · %s",
			interval, time.Now().Format("15:04:05"))))
		select {
		case <-stop:
			fmt.Println()
			return nil
		case <-time.After(interval):
		}
	}
}

// burnStatusline prints a single line for Claude Code's statusLine hook, which
// passes session context as JSON on stdin.
func burnStatusline(args []string) error {
	var g burnGlobals
	fs := burnFlags("statusline", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ui.SetColor(false)

	var ctx struct {
		Model struct {
			DisplayName string `json:"display_name"`
			ID          string `json:"id"`
		} `json:"model"`
		// Claude Code hands the status line the context figure already
		// computed, which beats deriving it: it is the same number the
		// session itself is working against.
		ContextWindow struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"context_window"`
	}
	_ = json.NewDecoder(os.Stdin).Decode(&ctx)

	_ = burn.Load(filepath.Join(claude.Dir(), "pitwall", "pricing.json"))
	records := burn.Scan(!g.noCache).Records
	now := time.Now()
	today := burnSum(burnAfter(records, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())))
	window := burnSum(burnAfter(records, now.Add(-5*time.Hour)))

	parts := []string{}
	if name := ctx.Model.DisplayName; name != "" {
		parts = append(parts, name)
	}
	// The context window leads, because it is the constraint that bites first
	// and the one a status line is uniquely placed to show: it changes within
	// a single conversation, while spend and quota change over a day.
	if p := ctx.ContextWindow.UsedPercentage; p != nil {
		parts = append(parts, fmt.Sprintf("ctx %.0f%%", *p))
	}
	parts = append(parts,
		money(today.Dollars())+" today",
		"5h "+money(window.Dollars()))
	if u, err := quota.Get(quota.Options{
		Dir:       filepath.Join(claude.Dir(), "pitwall"),
		CacheOnly: true,
	}); err == nil {
		tightest, label := u.FiveHour, "5h"
		if u.SevenDay.Utilization > tightest.Utilization {
			tightest, label = u.SevenDay, "wk"
		}
		parts = append(parts, fmt.Sprintf("%s %.0f%%", label, tightest.Utilization))
	}
	if g.limit > 0 {
		parts = append(parts, fmt.Sprintf("%.0f%% of %s", window.Dollars()/g.limit*100, money(g.limit)))
	} else {
		parts = append(parts, money(window.Dollars()/5)+"/h")
	}
	fmt.Println(strings.Join(parts, " · "))
	return nil
}

func burnExport(args []string) error {
	var g burnGlobals
	var format string
	fs := burnFlags("export", &g)
	fs.StringVar(&format, "format", "csv", "csv | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	records, _ := g.load()
	sort.Slice(records, func(i, j int) bool { return records[i].Hour.Before(records[j].Hour) })

	if format == "json" {
		return emitJSON(records)
	}
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()
	_ = w.Write([]string{"hour", "project", "session", "model", "effort", "subagent",
		"messages", "input", "output", "cache_write_5m", "cache_write_1h", "cache_read", "cost_usd"})
	for _, r := range records {
		c, _ := burn.Compute(r.Model, r.Hour, r.Usage)
		_ = w.Write([]string{
			r.Hour.Local().Format(time.RFC3339), r.Project, r.Session, r.Model, r.Effort,
			strconv.FormatBool(r.Sub), strconv.Itoa(r.Messages),
			strconv.FormatInt(r.Usage.Input, 10), strconv.FormatInt(r.Usage.Output, 10),
			strconv.FormatInt(r.Usage.CacheWrite5m, 10), strconv.FormatInt(r.Usage.CacheWrite1h, 10),
			strconv.FormatInt(r.Usage.CacheRead, 10),
			strconv.FormatFloat(c.Total(), 'f', 6, 64),
		})
	}
	return nil
}

func burnModels(args []string) error {
	var g burnGlobals
	fs := burnFlags("models", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := filepath.Join(claude.Dir(), "pitwall", "pricing.json")
	_ = burn.Load(path)
	models := burn.Models()
	sort.Strings(models)
	if g.asJSON {
		return emitJSON(models)
	}
	fmt.Printf("%s  %s\n\n", ui.Bold("priced models"), ui.Gray("USD per million tokens"))
	var t ui.Table
	t.Row(ui.Gray("model"), ui.Gray("input"), ui.Gray("output"))
	for _, m := range models {
		in, _ := burn.Compute(m, time.Now(), burn.Usage{Input: 1_000_000})
		out, _ := burn.Compute(m, time.Now(), burn.Usage{Output: 1_000_000})
		t.Row(m, money(in.Input), money(out.Output))
	}
	fmt.Print(t.Render("  "))
	fmt.Printf("\n  %s\n", ui.Gray("cache write ×"+fmt.Sprintf("%.2f", burn.CacheWrite5m)+
		" (5m) or ×"+fmt.Sprintf("%.0f", burn.CacheWrite1h)+" (1h) of input; cache read ×"+
		fmt.Sprintf("%.1f", burn.CacheRead)))
	fmt.Printf("  %s\n", ui.Gray("override or extend: "+path))
	return nil
}
