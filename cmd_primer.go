package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sur1cat/pitwall/internal/coach"
	"github.com/sur1cat/pitwall/internal/primer"
	"github.com/sur1cat/pitwall/internal/ui"
)

func cmdPrimer(args []string) error {
	var write, asJSON, force, all bool
	fs := flag.NewFlagSet("primer", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(primerUsage) }
	fs.BoolVar(&write, "write", false, "write the draft to PATH/CLAUDE.md")
	fs.BoolVar(&force, "force", false, "overwrite an existing CLAUDE.md")
	fs.BoolVar(&asJSON, "json", false, "machine-readable draft material")
	fs.BoolVar(&all, "all", false, "every repository that has no primer, ranked by cost")
	if err := fs.Parse(hoistFlags(args)); err != nil {
		return err
	}
	if all {
		return primerAll(write, force, asJSON)
	}

	path := "."
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	repo := coach.RepoOf(abs)

	d, err := primer.Gather(repo)
	if err != nil {
		return err
	}
	if d.ToolCalls == 0 {
		return fmt.Errorf("no Claude Code history found for %s", short(repo))
	}
	if asJSON {
		return emitJSON(d)
	}

	body := d.Markdown()
	if !write {
		fmt.Print(body)
		fmt.Fprintf(os.Stderr, "\n%s\n",
			ui.Gray(fmt.Sprintf("drafted from %d session(s) · write it with: pitwall primer %s --write",
				d.Sessions, short(repo))))
		return nil
	}

	target := filepath.Join(repo, "CLAUDE.md")
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("%s already exists — pass --force to overwrite", short(target))
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s %s  %s\n", ui.Green("wrote"), short(target),
		ui.Gray(fmt.Sprintf("from %d session(s), %d tool calls", d.Sessions, d.ToolCalls)))
	fmt.Println(ui.Gray("edit it — the sections it left blank are the ones that save the most time"))
	return nil
}

// primerAll ranks every repository with no primer by what its cold starts
// cost, so the biggest saving is the first thing on the list.
func primerAll(write, force, asJSON bool) error {
	ex, err := coach.CollectWithProgress(progressBar(asJSON))
	if err != nil {
		return err
	}
	rep := coach.Analyse(ex)

	// What an opening prompt costs where a primer already exists — the target
	// the unprimed repositories are measured against.
	var primedSpend float64
	var primedTurns int
	for _, p := range rep.Projects {
		if p.Primed {
			primedSpend += p.OpeningSpend
			primedTurns += p.OpeningTurns
		}
	}
	baseline := 0.0
	if primedTurns > 0 {
		baseline = primedSpend / float64(primedTurns)
	}

	type candidate struct {
		Name     string  `json:"name"`
		Repo     string  `json:"repo"`
		Sessions int     `json:"sessions"`
		Opening  float64 `json:"opening_cost"`
		AtStake  float64 `json:"at_stake"`
	}
	var list []candidate
	for _, p := range rep.Projects {
		if p.Primed || p.OpeningTurns < 3 || p.Sessions < 2 {
			continue
		}
		stake := 0.0
		if baseline > 0 && p.OpeningCost() > baseline {
			stake = (p.OpeningCost() - baseline) * float64(p.OpeningTurns)
		}
		list = append(list, candidate{p.Name, p.Repo, p.Sessions, p.OpeningCost(), stake})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].AtStake > list[j].AtStake })

	if asJSON {
		return emitJSON(map[string]any{"baseline_opening_cost": baseline, "candidates": list})
	}
	if len(list) == 0 {
		fmt.Println("Every repository with enough history already has a primer.")
		return nil
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("repositories with no primer"),
		ui.Gray(fmt.Sprintf("ranked by what their cold starts cost above %s per opening prompt",
			money(baseline))))
	var t ui.Table
	t.Row(ui.Gray("project"), ui.Gray("sessions"), ui.Gray("$/opening prompt"), ui.Gray("at stake"))
	var total float64
	for _, c := range list {
		total += c.AtStake
		t.Row(c.Name, fmt.Sprintf("%d", c.Sessions), money(c.Opening), ui.Yellow(money(c.AtStake)))
	}
	fmt.Print(t.Render("  "))
	fmt.Printf("\n  %s across %d repositories\n", ui.Bold(money(total)), len(list))

	if !write {
		fmt.Printf("\n%s\n", ui.Gray("pitwall primer --all --write   drafts a CLAUDE.md in each of them"))
		return nil
	}

	fmt.Println()
	for _, c := range list {
		d, err := primer.Gather(c.Repo)
		if err != nil || d.ToolCalls == 0 {
			fmt.Printf("  %s %s — no usable history\n", ui.Gray("–"), c.Name)
			continue
		}
		target := filepath.Join(c.Repo, "CLAUDE.md")
		if _, err := os.Stat(target); err == nil && !force {
			fmt.Printf("  %s %s — CLAUDE.md already exists\n", ui.Gray("–"), c.Name)
			continue
		}
		if err := os.WriteFile(target, []byte(d.Markdown()), 0o644); err != nil {
			fmt.Printf("  %s %s — %v\n", ui.Red("✗"), c.Name, err)
			continue
		}
		fmt.Printf("  %s %s  %s\n", ui.Green("✓"), short(target),
			ui.Gray(fmt.Sprintf("from %d sessions", d.Sessions)))
	}
	fmt.Println(ui.Gray("\nread them before you trust them — they are drafts, not documentation"))
	return nil
}
