package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/coach"
	"github.com/sur1cat/pitwall/internal/ui"
)

func cmdCoach(args []string) error {
	var since time.Duration
	var project string
	var asJSON, listProjects, noColor bool
	fs := flag.NewFlagSet("coach", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(coachUsage) }
	fs.Var(sinceFlag{&since}, "since", "only analyse the last span (30d, 2w, 12h)")
	fs.StringVar(&project, "project", "", "narrow to one repository")
	fs.BoolVar(&asJSON, "json", false, "machine-readable findings")
	fs.BoolVar(&listProjects, "projects", false, "list spend and priming per repository")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	ex, err := coach.CollectWithProgress(progressBar(asJSON))
	if err != nil {
		return err
	}
	if since > 0 {
		cutoff := time.Now().Add(-since)
		var kept []*coach.Exchange
		for _, e := range ex {
			if e.Time.After(cutoff) {
				kept = append(kept, e)
			}
		}
		ex = kept
	}
	if project != "" {
		var kept []*coach.Exchange
		for _, e := range ex {
			if strings.EqualFold(baseName(coach.RepoOf(e.CWD)), project) {
				kept = append(kept, e)
			}
		}
		ex = kept
	}
	if len(ex) == 0 {
		fmt.Println("No prompts found to analyse.")
		return nil
	}

	rep := coach.Analyse(ex)
	if asJSON {
		return emitJSON(rep)
	}
	if listProjects {
		printProjects(rep)
		return nil
	}
	printCoach(rep)
	printRetentionNote(claude.Retain())
	return nil
}

func printCoach(r coach.Report) {
	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall coach"),
		ui.Gray(fmt.Sprintf("%s across %d prompts · %s – %s",
			money(r.Spend), r.Prompts,
			r.From.Format("2006-01-02"), r.To.Format("2006-01-02"))))

	var t ui.Table
	t.Row(ui.Gray("what a prompt bought"), ui.Gray("prompts"), ui.Gray("spend"), ui.Gray("share"), "")
	meaning := map[coach.Class]string{
		coach.ClassExecute:     "wrote or changed code",
		coach.ClassInvestigate: "read, searched or ran things — changed nothing",
		coach.ClassTalk:        "no tool calls at all",
	}
	for _, c := range []coach.Class{coach.ClassExecute, coach.ClassInvestigate, coach.ClassTalk} {
		s := r.ByClass[c]
		if s.Prompts == 0 {
			continue
		}
		t.Row(string(c), fmt.Sprintf("%d", s.Prompts), money(s.Spend),
			pct(s.Spend, r.Spend), ui.Gray(meaning[c]))
	}
	fmt.Print(t.Render("  "))

	if len(r.Findings) == 0 {
		fmt.Println("\n  Nothing stands out — no habit is costing you enough to name.")
		return
	}
	for i, f := range r.Findings {
		fmt.Printf("\n%s %s\n", ui.Bold(fmt.Sprintf("%d.", i+1)), ui.Bold(f.Title))
		tag := ui.Yellow(money(f.Amount)) + ui.Gray(" · "+pct(f.Amount, r.Spend)+" of spend")
		if f.Correlated {
			tag += ui.Gray(" · association, not a controlled test")
		}
		fmt.Printf("   %s\n", tag)
		for _, d := range f.Detail {
			fmt.Printf("     %s\n", ui.Gray(d))
		}
		if f.Action != "" {
			fmt.Printf("     %s %s\n", ui.Green("→"), f.Action)
		}
	}
	fmt.Printf("\n%s\n", ui.Gray("pitwall coach --projects   for the per-repository breakdown"))
}

func printProjects(r coach.Report) {
	fmt.Printf("%s\n\n", ui.Bold("spend by repository"))
	var t ui.Table
	t.Row(ui.Gray("project"), ui.Gray("primer"), ui.Gray("sessions"), ui.Gray("spend"), ui.Gray("$/opening prompt"))
	for _, p := range r.Projects {
		if p.Prompts < 3 {
			continue
		}
		primed := ui.Red("none")
		if p.Primed {
			primed = ui.Green("yes")
		}
		t.Row(p.Name, primed, fmt.Sprintf("%d", p.Sessions), money(p.Spend), money(p.OpeningCost()))
	}
	fmt.Print(t.Render("  "))
	fmt.Printf("\n%s\n", ui.Gray("pitwall primer <project path>   drafts the missing primer"))
}

func pct(a, b float64) string {
	if b == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", a/b*100)
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

var _ = sort.Strings

// progressBar writes a single rewriting line to stderr while transcripts are
// read, so an empty terminal never looks like a broken command. It stays quiet
// when stderr is redirected or the caller wants machine-readable output.
func progressBar(quiet bool) coach.Progress {
	info, err := os.Stderr.Stat()
	if quiet || err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	last := -1
	return func(done, total int) {
		if total == 0 {
			return
		}
		pct := done * 100 / total
		if pct == last && done != total {
			return
		}
		last = pct
		fmt.Fprintf(os.Stderr, "\r\033[Kreading %d of %d transcripts… %d%%", done, total, pct)
		if done == total {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
	}
}
