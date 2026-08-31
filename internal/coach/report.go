package coach

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// correction matches a prompt whose job is to undo or redirect the previous turn.
// Go's \b is ASCII-only, so it never fires next to Cyrillic — these
// alternatives are matched unanchored on purpose.
var correction = regexp.MustCompile(`(?i)(стоп |остановись|не то |не так|неправильно|зачем ты|` +
	`я не просил|кто тебя просил|сломал|испортил|откати|верни как было|убери это|` +
	`я же говорил|я же просил|я же сказал|опять не|снова не|не нужно было|` +
	`\bwrong\b|revert that|\bundo\b|you broke|that's not what|not what i asked)`)

// continuation matches a prompt that only nudges the agent onward.
var continuation = regexp.MustCompile(`(?i)^\s*(да|нет|ок|окей|давай( дальше| делай| продолжай)?|` +
	`продолжай|продолжи|дальше|начинай|приступай|делай|go|yes|no|ok|continue|proceed)[\s.!,]*$`)

var slashCmd = regexp.MustCompile(`^\s*/\w`)

// ClassStat is the rollup for one outcome class.
type ClassStat struct {
	Prompts int     `json:"prompts"`
	Spend   float64 `json:"spend"`
}

// ProjectStat is per-repository spend, used to target advice.
type ProjectStat struct {
	Repo         string  `json:"repo"`
	Name         string  `json:"name"`
	Primed       bool    `json:"primed"`
	Sessions     int     `json:"sessions"`
	Prompts      int     `json:"prompts"`
	Spend        float64 `json:"spend"`
	OpeningTurns int     `json:"opening_turns"`
	OpeningSpend float64 `json:"opening_spend"`
}

// OpeningCost is what the first prompts of a session cost in this project.
func (p ProjectStat) OpeningCost() float64 {
	if p.OpeningTurns == 0 {
		return 0
	}
	return p.OpeningSpend / float64(p.OpeningTurns)
}

// Finding is one measured habit with a number and something to do about it.
type Finding struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Amount float64  `json:"amount"`
	Share  float64  `json:"share"`
	Detail []string `json:"detail"`
	Action string   `json:"action"`
	// Correlated marks a finding that is a strong association rather than a
	// controlled measurement.
	Correlated bool `json:"correlated"`
}

// Report is everything the coach concluded.
type Report struct {
	From     time.Time           `json:"from"`
	To       time.Time           `json:"to"`
	Prompts  int                 `json:"prompts"`
	Spend    float64             `json:"spend"`
	ByClass  map[Class]ClassStat `json:"by_class"`
	Projects []ProjectStat       `json:"projects"`
	Findings []Finding           `json:"findings"`
}

// openingTurns is how many prompts count as a session's cold start.
const openingTurns = 3

// Analyse turns raw exchanges into findings.
func Analyse(ex []*Exchange) Report {
	r := Report{Prompts: len(ex), ByClass: map[Class]ClassStat{}}
	if len(ex) == 0 {
		return r
	}
	r.From, r.To = ex[0].Time, ex[len(ex)-1].Time

	sessions := map[string][]*Exchange{}
	repos := map[string]*ProjectStat{}
	repoOf := map[string]string{}

	for _, e := range ex {
		r.Spend += e.Cost
		c := r.ByClass[e.Class()]
		c.Prompts++
		c.Spend += e.Cost
		r.ByClass[e.Class()] = c
		sessions[e.Session] = append(sessions[e.Session], e)

		root, ok := repoOf[e.CWD]
		if !ok {
			root = RepoOf(e.CWD)
			repoOf[e.CWD] = root
		}
		e.Repo = root
		p := repos[root]
		if p == nil {
			p = &ProjectStat{Repo: root, Name: filepath.Base(root), Primed: Primed(root)}
			repos[root] = p
		}
		p.Prompts++
		p.Spend += e.Cost
	}

	for _, l := range sessions {
		sort.Slice(l, func(i, j int) bool { return l[i].Time.Before(l[j].Time) })
		if len(l) == 0 {
			continue
		}
		if p := repos[l[0].Repo]; p != nil {
			p.Sessions++
		}
		for i, e := range l {
			if i >= openingTurns {
				break
			}
			if p := repos[e.Repo]; p != nil {
				p.OpeningTurns++
				p.OpeningSpend += e.Cost
			}
		}
	}

	for _, p := range repos {
		r.Projects = append(r.Projects, *p)
	}
	sort.Slice(r.Projects, func(i, j int) bool { return r.Projects[i].Spend > r.Projects[j].Spend })

	r.Findings = append(r.Findings,
		coldStart(r, sessions),
		roundTrips(r, sessions),
		effortValue(r, ex),
		rework(r, sessions))
	kept := r.Findings[:0]
	for _, f := range r.Findings {
		if f.ID != "" {
			kept = append(kept, f)
		}
	}
	r.Findings = kept
	sort.SliceStable(r.Findings, func(i, j int) bool { return r.Findings[i].Amount > r.Findings[j].Amount })
	return r
}

// coldStart measures what starting a session in an unprepared repository costs.
func coldStart(r Report, sessions map[string][]*Exchange) Finding {
	var coldTurns, warmTurns int
	var coldSpend, warmSpend, coldTools, warmTools float64
	for _, l := range sessions {
		for i, e := range l {
			if i >= openingTurns {
				break
			}
			if Primed(e.Repo) {
				warmTurns++
				warmSpend += e.Cost
				warmTools += float64(e.Tools)
			} else {
				coldTurns++
				coldSpend += e.Cost
				coldTools += float64(e.Tools)
			}
		}
	}
	if coldTurns == 0 || warmTurns == 0 || r.Spend == 0 {
		return Finding{}
	}
	coldPer, warmPer := coldSpend/float64(coldTurns), warmSpend/float64(warmTurns)
	if coldPer <= warmPer {
		return Finding{}
	}
	recoverable := (coldPer - warmPer) * float64(coldTurns)

	f := Finding{
		ID:         "cold-start",
		Title:      "Sessions restart from zero in repositories with no primer",
		Amount:     recoverable,
		Share:      recoverable / r.Spend,
		Correlated: true,
		Detail: []string{
			fmt.Sprintf("the first %d prompts of a session are %s of all spend",
				openingTurns, pctStr((coldSpend+warmSpend)/r.Spend)),
			fmt.Sprintf("without a primer: $%.2f per opening prompt, %.0f tool calls each",
				coldPer, coldTools/float64(coldTurns)),
			fmt.Sprintf("with one:        $%.2f per opening prompt, %.0f tool calls each",
				warmPer, warmTools/float64(warmTurns)),
		},
	}
	var worst []string
	for _, p := range r.Projects {
		if !p.Primed && p.OpeningTurns >= 3 && len(worst) < 3 {
			worst = append(worst, fmt.Sprintf("%s ($%.2f per opening prompt)", p.Name, p.OpeningCost()))
		}
	}
	if len(worst) > 0 {
		f.Action = "pitwall primer " + firstWord(worst[0]) + "   — drafts a CLAUDE.md from what past sessions already discovered"
		f.Detail = append(f.Detail, "worst offenders: "+join(worst))
	}
	return f
}

// roundTrips measures question-then-one-line-answer cycles.
func roundTrips(r Report, sessions map[string][]*Exchange) Finding {
	var n int
	var spend float64
	for _, l := range sessions {
		for i := 0; i+1 < len(l); i++ {
			a, b := l[i], l[i+1]
			asked := a.EndedOnQuestion || a.Asks > 0
			short := b.Length() < 120 || continuation.MatchString(b.Prompt)
			if asked && short {
				n++
				spend += a.Cost + b.Cost
			}
		}
	}
	if n == 0 || r.Spend == 0 {
		return Finding{}
	}
	return Finding{
		ID:     "round-trips",
		Title:  "Answers that end in a question, followed by a one-line reply",
		Amount: spend,
		Share:  spend / r.Spend,
		Detail: []string{
			fmt.Sprintf("%d round trips; each one pays for a full turn to ask and another to resume", n),
			"the agent had to stop because the prompt left a decision open",
		},
		Action: "state the decision up front: which files, what done looks like, and what not to touch",
	}
}

// effortValue compares what each effort level bought per unit of work.
func effortValue(r Report, ex []*Exchange) Finding {
	type agg struct {
		n     int
		spend float64
		edits int
	}
	by := map[string]*agg{}
	for _, e := range ex {
		k := e.Effort
		if k == "" {
			continue
		}
		a := by[k]
		if a == nil {
			a = &agg{}
			by[k] = a
		}
		a.n++
		a.spend += e.Cost
		a.edits += e.Edits
	}
	if len(by) < 2 {
		return Finding{}
	}
	type row struct {
		name    string
		spend   float64
		perEdit float64
		n       int
	}
	var rows []row
	for k, a := range by {
		if a.edits == 0 {
			continue
		}
		rows = append(rows, row{k, a.spend, a.spend / float64(a.edits), a.n})
	}
	if len(rows) < 2 {
		return Finding{}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].spend > rows[j].spend })
	dominant := rows[0]
	best := rows[0]
	for _, x := range rows {
		if x.n >= 20 && x.perEdit < best.perEdit {
			best = x
		}
	}
	if best.name == dominant.name || dominant.perEdit <= best.perEdit {
		return Finding{}
	}
	// The cheaper level is usually the less-used one, so scale the claim to
	// how much evidence there is for it rather than extrapolating from a
	// handful of prompts.
	gap := dominant.spend * (1 - best.perEdit/dominant.perEdit)
	confidence := float64(best.n) / 200
	if confidence > 1 {
		confidence = 1
	}
	saving := gap * confidence
	f := Finding{
		ID:         "effort-mix",
		Title:      fmt.Sprintf("%q carries your spend but %q delivers more per dollar", dominant.name, best.name),
		Amount:     saving,
		Share:      saving / r.Spend,
		Correlated: true,
	}
	for _, x := range rows {
		f.Detail = append(f.Detail, fmt.Sprintf("%-6s %8s  %5d prompts  $%.2f per code change",
			x.name, money(x.spend), x.n, x.perEdit))
	}
	f.Detail = append(f.Detail,
		fmt.Sprintf("full gap would be %s; scaled to %s because %q has only %d prompts behind it",
			money(gap), money(saving), best.name, best.n),
		"tasks are not identical across levels, so treat this as a hypothesis to test, not a verdict")
	f.Action = fmt.Sprintf("run a week at %s and compare — pitwall records the answer either way", best.name)
	return f
}

// rework measures spend on turns the next prompt had to correct.
func rework(r Report, sessions map[string][]*Exchange) Finding {
	var n int
	var spend float64
	for _, l := range sessions {
		for i := 1; i < len(l); i++ {
			p := l[i].Prompt
			if slashCmd.MatchString(p) || !correction.MatchString(p) {
				continue
			}
			n++
			spend += l[i-1].Cost
		}
	}
	if n == 0 || r.Spend == 0 {
		return Finding{}
	}
	return Finding{
		ID:     "rework",
		Title:  "Work you paid for and then told the agent to undo",
		Amount: spend,
		Share:  spend / r.Spend,
		Detail: []string{
			fmt.Sprintf("%d prompts were corrections; this is what the turn before them cost", n),
		},
		Action: "name the constraint before the work starts, not after you see the diff",
	}
}

func pctStr(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func money(v float64) string { return fmt.Sprintf("$%.0f", v) }

func firstWord(s string) string {
	for i, r := range s {
		if r == ' ' {
			return s[:i]
		}
	}
	return s
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
