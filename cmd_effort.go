package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/pitwall/internal/burn"
	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/coach"
	"github.com/sur1cat/pitwall/internal/quota"
	"github.com/sur1cat/pitwall/internal/ui"
)

const effortUsage = `pitwall effort — stop choosing an effort level in every project

Usage:
  pitwall effort              what each project launches at, and what its history suggests
  pitwall effort --apply      write every suggestion to .claude/settings.local.json
  pitwall effort --set LEVEL [PROJECT]
                              pin one project (or the current directory) by hand
  pitwall effort --clear [PROJECT]
                              remove the pin and fall back to your user default
  pitwall effort --guard      lower your user default while today's burn is over budget

Levels: low, medium, high, xhigh, max.

Effort is a normal settings key, so a value in a project's
.claude/settings.local.json applies to every session you start there. Nothing
can change effort inside a running session except you, with /effort.
`

var effortLevels = map[string]int{"low": 0, "medium": 1, "high": 2, "xhigh": 3, "max": 4}

func cmdEffort(args []string) error {
	var apply, asJSON, guard bool
	var set, clear, limitFloor string
	var limit float64
	var threshold float64
	fs := flag.NewFlagSet("effort", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(effortUsage) }
	fs.BoolVar(&apply, "apply", false, "write suggestions to project settings")
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.StringVar(&set, "set", "", "pin a level for one project")
	fs.StringVar(&clear, "clear", "", "remove a project's pin")
	fs.BoolVar(&guard, "guard", false, "lower the user default while over budget")
	fs.Float64Var(&limit, "limit", envLimitValue(), "spend budget for the 5-hour window, in USD")
	fs.Float64Var(&threshold, "threshold", 0.85, "fraction of the budget that triggers the guard")
	fs.StringVar(&limitFloor, "floor", "medium", "level the guard drops to")
	if err := fs.Parse(hoistFlags(fs, args)); err != nil {
		return err
	}

	switch {
	case guard:
		return effortGuard(limit, threshold, limitFloor)
	case clear != "" || (clear == "" && hasFlag(fs, "clear")):
		return effortPin(clear, "", fs.Args())
	case set != "":
		if _, ok := effortLevels[set]; !ok {
			return fmt.Errorf("unknown level %q — use low, medium, high, xhigh or max", set)
		}
		return effortPin("", set, fs.Args())
	}
	return effortReport(apply, asJSON)
}

func hasFlag(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func envLimitValue() float64 { return burnEnvLimit() }

// effortStat is what one effort level bought inside one project.
type effortStat struct {
	Level   string  `json:"level"`
	Prompts int     `json:"prompts"`
	Spend   float64 `json:"spend"`
	Edits   int     `json:"edits"`
}

// PerEdit is the dollars this level spent for each code change it produced.
func (s effortStat) PerEdit() float64 {
	if s.Edits == 0 {
		return 0
	}
	return s.Spend / float64(s.Edits)
}

type effortProject struct {
	Name    string       `json:"name"`
	Repo    string       `json:"repo"`
	Current string       `json:"current"`
	Source  string       `json:"source"`
	Suggest string       `json:"suggest,omitempty"`
	Reason  string       `json:"reason,omitempty"`
	Spend   float64      `json:"spend"`
	Levels  []effortStat `json:"levels"`
	// Primed and OpeningCost describe the other lever on the same project:
	// what a session costs in its first prompts, and whether it has anything
	// to start from.
	Primed      bool    `json:"primed"`
	Sessions    int     `json:"sessions"`
	OpeningCost float64 `json:"opening_cost"`
}

// minSamplesPerLevel is how many prompts a level needs inside one project
// before its cost per code change means anything.
const minSamplesPerLevel = 25

func effortReport(apply, asJSON bool) error {
	ex, err := coach.CollectWithProgress(progressBar(asJSON))
	if err != nil {
		return err
	}

	rep := coach.Analyse(ex)
	byRepo := map[string]coach.ProjectStat{}
	for _, p := range rep.Projects {
		byRepo[p.Repo] = p
	}

	type key struct{ repo, level string }
	agg := map[key]*effortStat{}
	spend := map[string]float64{}
	repos := map[string]bool{}
	for _, e := range ex {
		repo := coach.RepoOf(e.CWD)
		if repo == "" || e.Effort == "" {
			continue
		}
		repos[repo] = true
		spend[repo] += e.Cost
		k := key{repo, e.Effort}
		s := agg[k]
		if s == nil {
			s = &effortStat{Level: e.Effort}
			agg[k] = s
		}
		s.Prompts++
		s.Spend += e.Cost
		s.Edits += e.Edits
	}

	var projects []effortProject
	for repo := range repos {
		p := effortProject{Name: filepath.Base(repo), Repo: repo, Spend: spend[repo]}
		p.Current, p.Source = effectiveEffort(repo)
		for level := range effortLevels {
			if s, ok := agg[key{repo, level}]; ok {
				p.Levels = append(p.Levels, *s)
			}
		}
		sort.Slice(p.Levels, func(i, j int) bool {
			return effortLevels[p.Levels[i].Level] < effortLevels[p.Levels[j].Level]
		})
		p.Suggest, p.Reason = suggestLevel(p.Levels)
		if stat, ok := byRepo[repo]; ok {
			p.Primed, p.Sessions, p.OpeningCost = stat.Primed, stat.Sessions, stat.OpeningCost()
		}
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Spend > projects[j].Spend })

	if asJSON {
		return emitJSON(map[string]any{"projects": projects})
	}
	if len(projects) == 0 {
		fmt.Println("No history yet — pitwall needs a few sessions before it can suggest anything.")
		return nil
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("effort per project"),
		ui.Gray("what a new session starts at, and what your own history says"))
	var t ui.Table
	t.Row(ui.Gray("project"), ui.Gray("starts at"), ui.Gray("suggests"), ui.Gray("why"))
	shown, hidden := 0, 0
	for _, p := range projects {
		if p.Suggest == "" && p.Spend < 100 {
			hidden++
			continue
		}
		shown++
		suggest := ui.Gray("—")
		if p.Suggest != "" && p.Suggest != p.Current {
			suggest = ui.Yellow(p.Suggest)
		} else if p.Suggest != "" {
			suggest = ui.Green("already " + p.Suggest)
		}
		reason := p.Reason
		if reason == "" {
			reason = ui.Gray("not enough prompts at another level to compare")
		}
		t.Row(p.Name, p.Current+ui.Gray(" ("+p.Source+")"), suggest, reason)
	}
	fmt.Print(t.Render("  "))
	if shown == 0 {
		fmt.Println("  Not enough history yet — pitwall needs a few sessions per project.")
	}
	if hidden > 0 {
		fmt.Printf("  %s\n", ui.Gray(fmt.Sprintf("%d more projects have too little history to compare levels", hidden)))
	}

	if !apply {
		fmt.Printf("\n%s\n", ui.Gray("pitwall effort --apply            writes the suggestions into each project"))
		fmt.Printf("%s\n", ui.Gray("pitwall effort --set high mds     pins one project by hand"))
		return nil
	}

	fmt.Println()
	for _, p := range projects {
		if p.Suggest == "" || p.Suggest == p.Current {
			continue
		}
		if err := writeProjectEffort(p.Repo, p.Suggest); err != nil {
			fmt.Printf("  %s %s — %v\n", ui.Red("✗"), p.Name, err)
			continue
		}
		fmt.Printf("  %s %s now starts at %s\n", ui.Green("✓"), p.Name, ui.Bold(p.Suggest))
	}
	fmt.Println(ui.Gray("\nwritten to .claude/settings.local.json, which git ignores — revert with: pitwall effort --clear PROJECT"))
	return nil
}

// suggestLevel picks the level with the lowest cost per code change, among
// those with enough prompts behind them to mean anything.
func suggestLevel(levels []effortStat) (string, string) {
	var usable []effortStat
	for _, l := range levels {
		if l.Prompts >= minSamplesPerLevel && l.Edits > 0 {
			usable = append(usable, l)
		}
	}
	if len(usable) < 2 {
		return "", ""
	}
	best, worst := usable[0], usable[0]
	for _, l := range usable {
		if l.PerEdit() < best.PerEdit() {
			best = l
		}
		if l.Spend > worst.Spend {
			worst = l
		}
	}
	if best.Level == worst.Level {
		return best.Level, fmt.Sprintf("cheapest per change at %s (%s, n=%d)",
			best.Level, money(best.PerEdit()), best.Prompts)
	}
	return best.Level, fmt.Sprintf("%s per change vs %s at %s (n=%d)",
		money(best.PerEdit()), money(worst.PerEdit()), worst.Level, best.Prompts)
}

// effectiveEffort reports the level a new session in this repository starts
// at, and which file decides it.
func effectiveEffort(repo string) (string, string) {
	for _, candidate := range []struct{ path, source string }{
		{filepath.Join(repo, ".claude", "settings.local.json"), "project"},
		{filepath.Join(repo, ".claude", "settings.json"), "shared"},
		{filepath.Join(claude.Dir(), "settings.json"), "user"},
	} {
		if level := readEffort(candidate.path); level != "" {
			return level, candidate.source
		}
	}
	return "high", "default"
}

func readEffort(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var settings map[string]any
	if json.Unmarshal(raw, &settings) != nil {
		return ""
	}
	if level, ok := settings["effortLevel"].(string); ok {
		return level
	}
	return ""
}

// writeProjectEffort sets effortLevel in a project's local settings, leaving
// every other key it finds there untouched.
func writeProjectEffort(repo, level string) error {
	path := filepath.Join(repo, ".claude", "settings.local.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s is not valid JSON", short(path))
		}
	}
	if level == "" {
		delete(settings, "effortLevel")
	} else {
		settings["effortLevel"] = level
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func effortPin(clearProject, level string, rest []string) error {
	target := ""
	if level != "" && len(rest) > 0 {
		target = rest[0]
	} else if clearProject != "" {
		target = clearProject
	} else if len(rest) > 0 {
		target = rest[0]
	}
	repo, err := resolveRepo(target)
	if err != nil {
		return err
	}
	if err := writeProjectEffort(repo, level); err != nil {
		return err
	}
	if level == "" {
		fmt.Printf("%s %s falls back to your user default\n", ui.Green("✓"), filepath.Base(repo))
		return nil
	}
	fmt.Printf("%s %s now starts at %s\n", ui.Green("✓"), filepath.Base(repo), ui.Bold(level))
	return nil
}

// resolveRepo turns a name or path into a repository root.
func resolveRepo(target string) (string, error) {
	if target == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return coach.RepoOf(wd), nil
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		abs, _ := filepath.Abs(target)
		return coach.RepoOf(abs), nil
	}
	// A bare name: match it against the projects pitwall already knows.
	ex, err := coach.Collect()
	if err != nil {
		return "", err
	}
	for _, e := range ex {
		repo := coach.RepoOf(e.CWD)
		if strings.EqualFold(filepath.Base(repo), target) {
			return repo, nil
		}
	}
	return "", fmt.Errorf("no project called %q — see: pitwall effort", target)
}

// guardState remembers the level to restore once spending falls back.
type guardState struct {
	Original  string    `json:"original"`
	LoweredAt time.Time `json:"lowered_at"`
}

// effortGuard lowers your user-level default while the trailing five hours are
// over budget, and puts it back when they are not. It only affects sessions
// started afterwards — nothing can retune a session that is already running.
func effortGuard(limit, threshold float64, floor string) error {
	if _, ok := effortLevels[floor]; !ok {
		return fmt.Errorf("unknown floor %q", floor)
	}

	// Anthropic's own number when it is available, a declared budget otherwise.
	var fraction float64
	var basis string
	usage, qerr := quota.Get(quota.Options{Dir: filepath.Join(claude.Dir(), "pitwall")})
	switch {
	case qerr == nil || !usage.FetchedAt.IsZero():
		fraction = usage.FiveHour.Utilization / 100
		basis = fmt.Sprintf("%.0f%% of the 5-hour window used", usage.FiveHour.Utilization)
		if usage.SevenDay.Utilization/100 > fraction {
			fraction = usage.SevenDay.Utilization / 100
			basis = fmt.Sprintf("%.0f%% of the rolling window used", usage.SevenDay.Utilization)
		}
	case limit > 0:
		records := burn.Scan(true).Records
		window := burnSum(burnAfter(records, time.Now().Add(-5*time.Hour)))
		fraction = window.Dollars() / limit
		basis = fmt.Sprintf("%s of %s in the last 5h", money(window.Dollars()), money(limit))
	default:
		return fmt.Errorf("no usage reading available (%v) and no budget set — "+
			"try: pitwall quota, or pitwall effort --guard --limit 40", qerr)
	}

	statePath := filepath.Join(claude.Dir(), "pitwall", "effort-guard.json")
	var state guardState
	if raw, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(raw, &state)
	}
	userSettings := filepath.Join(claude.Dir(), "settings.json")
	current := readEffort(userSettings)
	if current == "" {
		current = "high"
	}

	switch {
	case fraction >= threshold && state.Original == "":
		if effortLevels[current] <= effortLevels[floor] {
			fmt.Printf("%s already at %s or lower — nothing to do\n", ui.Gray("guard:"), floor)
			return nil
		}
		if err := writeUserEffort(floor); err != nil {
			return err
		}
		state = guardState{Original: current, LoweredAt: time.Now()}
		_ = os.MkdirAll(filepath.Dir(statePath), 0o755)
		if raw, err := json.Marshal(state); err == nil {
			_ = os.WriteFile(statePath, raw, 0o644)
		}
		fmt.Printf("%s %s — new sessions now start at %s (was %s)\n",
			ui.Yellow("guard:"), basis, ui.Bold(floor), current)
	case fraction < threshold && state.Original != "":
		if err := writeUserEffort(state.Original); err != nil {
			return err
		}
		_ = os.Remove(statePath)
		fmt.Printf("%s back under the threshold (%s) — new sessions start at %s again\n",
			ui.Green("guard:"), basis, ui.Bold(state.Original))
	default:
		fmt.Printf("%s %s (%.0f%% of the threshold) — no change\n",
			ui.Gray("guard:"), basis, fraction/threshold*100)
	}
	return nil
}

func writeUserEffort(level string) error {
	path := filepath.Join(claude.Dir(), "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s is not valid JSON", short(path))
		}
	}
	settings["effortLevel"] = level
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}
