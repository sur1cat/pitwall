package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/ui"
	"github.com/sur1cat/pitwall/internal/worktree"
)

type treeGlobals struct {
	paths   multiFlag
	asJSON  bool
	noSize  bool
	noCache bool
	noColor bool
}

func (g *treeGlobals) bind(fs *flag.FlagSet) {
	fs.Var(&g.paths, "path", "limit to this repository (repeatable)")
	fs.Var(&g.paths, "p", "shorthand for --path")
	fs.BoolVar(&g.asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&g.noSize, "no-size", false, "skip the on-disk size walk")
	fs.BoolVar(&g.noCache, "no-cache", false, "ignore the transcript cache")
	fs.BoolVar(&g.noColor, "no-color", false, "disable ANSI color")
}

func (g *treeGlobals) options() worktree.Options {
	if g.noColor {
		ui.SetColor(false)
	}
	return worktree.Options{Roots: g.paths, WithSize: !g.noSize, NoCache: g.noCache}
}

func treeFlags(name string, g *treeGlobals) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(treeUsage) }
	g.bind(fs)
	return fs
}

func treeStatus(args []string) error {
	var g treeGlobals
	fs := treeFlags("status", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := worktree.Run(g.options())
	if err != nil {
		return err
	}
	if g.asJSON {
		return emitJSON(map[string]any{"repos": res.Repos, "totals": worktree.Summarize(res.Repos)})
	}
	treeRender(res)
	return nil
}

func treeRender(res worktree.Result) {
	totals := worktree.Summarize(res.Repos)
	if totals.Worktrees == 0 {
		fmt.Println("No git worktrees found. Pass --path to point pitwall at a repository.")
		return
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall tree"),
		ui.Gray(fmt.Sprintf("%d repositories · %d worktrees · %s on disk",
			len(res.Repos), totals.Worktrees, ui.Bytes(totals.Bytes))))

	for _, repo := range res.Repos {
		base := repo.Base
		if base == "" {
			base = ui.Yellow("unknown")
		}
		fmt.Printf("%s  %s\n", ui.Bold(filepath.Base(repo.Root)),
			ui.Gray(short(repo.Root)+"  base: "+base))

		var t ui.Table
		for _, wt := range repo.Worktrees {
			t.Row(treeGlyph(wt.State), treeStateLabel(wt.State), treeBranchCell(wt), treeLineageCell(wt), treeDetailCell(wt))
		}
		fmt.Print(t.Render("  "))
		fmt.Println()
	}

	order := []worktree.State{worktree.StatePrimary, worktree.StateActive, worktree.StateStranded, worktree.StateAhead, worktree.StateDead, worktree.StateOrphan}
	var parts []string
	for _, s := range order {
		if n := totals.ByState[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", treeStateLabel(s), n))
		}
	}
	fmt.Printf("%s  %s\n", ui.Bold("Summary"), strings.Join(parts, ui.Gray(" · ")))

	var reclaimable int64
	var removable, stranded, strandedFiles int
	for _, repo := range res.Repos {
		for _, wt := range repo.Worktrees {
			if wt.State.Removable() {
				removable++
				reclaimable += wt.SizeBytes
			}
			if wt.State == worktree.StateStranded {
				stranded++
				strandedFiles += wt.Dirty()
			}
		}
	}
	if removable > 0 {
		line := fmt.Sprintf("  %d worktree(s) removable", removable)
		if reclaimable > 0 {
			line += fmt.Sprintf(", %s to reclaim", ui.Bytes(reclaimable))
		}
		fmt.Println(line)
	}
	if stranded > 0 {
		fmt.Println("  " + ui.Yellow(fmt.Sprintf("%d worktree(s) hold %d uncommitted file(s) with no live session",
			stranded, strandedFiles)))
	}
	if removable > 0 {
		fmt.Println(ui.Gray("\nnext: pitwall tree gc --dry-run"))
	}
}

func treeGlyph(s worktree.State) string {
	switch s {
	case worktree.StatePrimary:
		return ui.Gray("•")
	case worktree.StateActive:
		return ui.Green("●")
	case worktree.StateStranded:
		return ui.Yellow("⚑")
	case worktree.StateAhead:
		return ui.Blue("↑")
	case worktree.StateOrphan:
		return ui.Gray("?")
	default:
		return ui.Gray("✗")
	}
}

func treeStateLabel(s worktree.State) string {
	switch s {
	case worktree.StatePrimary:
		return ui.Gray(string(s))
	case worktree.StateActive:
		return ui.Green(string(s))
	case worktree.StateStranded:
		return ui.Yellow(string(s))
	case worktree.StateAhead:
		return ui.Blue(string(s))
	default:
		return ui.Gray(string(s))
	}
}

func treeBranchCell(wt *worktree.Worktree) string {
	name := wt.Branch
	if name == "" {
		name = ui.Gray("(detached)")
	}
	return ui.Truncate(name, 46)
}

func treeLineageCell(wt *worktree.Worktree) string {
	switch {
	case wt.Prunable:
		return ui.Gray("directory gone")
	case wt.Primary:
		return ui.Gray("main checkout")
	case !wt.HasCounts:
		return ui.Gray("lineage unknown")
	case wt.Ahead > 0:
		return ui.Blue(fmt.Sprintf("+%d unmerged", wt.Ahead))
	default:
		return ui.Gray("merged")
	}
}

func treeDetailCell(wt *worktree.Worktree) string {
	var parts []string
	if n := wt.Dirty(); n > 0 {
		parts = append(parts, ui.Yellow(fmt.Sprintf("%d uncommitted", n)))
	}
	if wt.Session != nil {
		parts = append(parts, ui.Green(fmt.Sprintf("%s %s", wt.Session.Name, wt.Session.Status)))
	} else if wt.Owner != nil && !wt.Owner.Alive {
		parts = append(parts, ui.Gray("owner session ended"))
	}
	if wt.SizeBytes > 0 {
		parts = append(parts, ui.Gray(ui.Bytes(wt.SizeBytes)))
	}
	if !wt.LastCommit.IsZero() {
		parts = append(parts, ui.Gray(ui.Ago(wt.LastCommit)))
	}
	return strings.Join(parts, ui.Gray(" · "))
}

func treeGC(args []string) error {
	var g treeGlobals
	var opt worktree.GCOptions
	var yes, noSalvage bool
	fs := treeFlags("gc", &g)
	fs.BoolVar(&opt.DryRun, "dry-run", false, "show the plan without changing anything")
	fs.BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	fs.BoolVar(&yes, "y", false, "shorthand for --yes")
	fs.BoolVar(&noSalvage, "no-salvage", false, "do not commit stranded work")
	fs.BoolVar(&opt.Force, "force", false, "allow discarding uncommitted work")
	fs.BoolVar(&opt.PruneBranches, "prune-branches", false, "delete branches merged into the base")
	fs.BoolVar(&opt.IncludeAhead, "include-ahead", false, "also remove worktrees with unmerged commits")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opt.Salvage = !noSalvage

	res, err := worktree.Run(g.options())
	if err != nil {
		return err
	}
	plan := worktree.Plan(res.Repos, opt)
	if len(plan) == 0 {
		fmt.Println("Nothing to clean — every worktree is active, primary, or holds unmerged work.")
		return nil
	}

	if !g.asJSON {
		treePrintPlan(plan, opt)
	}
	if opt.DryRun {
		if g.asJSON {
			// The panel needs a summary it can put on a button, not just the
			// per-worktree list.
			var bytes int64
			var salvage int
			for _, wt := range plan {
				bytes += wt.SizeBytes
				if wt.State == worktree.StateStranded {
					salvage++
				}
			}
			return emitJSON(map[string]any{
				"plan": worktree.GC(res.Repos, opt), "count": len(plan),
				"bytes": bytes, "salvage": salvage,
			})
		}
		fmt.Println(ui.Gray("\ndry run — nothing was changed"))
		return nil
	}
	if !yes {
		ok, err := confirm(fmt.Sprintf("Remove %d worktree(s)?", len(plan)))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("aborted")
			return nil
		}
	}

	actions := worktree.GC(res.Repos, opt)
	if g.asJSON {
		return emitJSON(map[string]any{"actions": actions})
	}
	treePrintActions(actions)
	return nil
}

func treePrintPlan(plan []*worktree.Worktree, opt worktree.GCOptions) {
	fmt.Printf("%s\n\n", ui.Bold("Plan"))
	var t ui.Table
	var freed int64
	for _, wt := range plan {
		action := "remove"
		if wt.Dirty() > 0 && opt.Salvage {
			action = ui.Yellow(fmt.Sprintf("salvage %d file(s), then remove", wt.Dirty()))
		} else if wt.Dirty() > 0 {
			action = ui.Red(fmt.Sprintf("DISCARD %d file(s), then remove", wt.Dirty()))
		}
		t.Row(treeStateLabel(wt.State), ui.Truncate(short(wt.Path), 60), action, ui.Gray(ui.Bytes(wt.SizeBytes)))
		freed += wt.SizeBytes
	}
	fmt.Print(t.Render("  "))
	fmt.Printf("\n  %d worktree(s), %s to reclaim\n", len(plan), ui.Bytes(freed))
}

func treePrintActions(actions []worktree.Action) {
	var freed int64
	var removed, salvaged, failed int
	fmt.Printf("\n%s\n", ui.Bold("Result"))
	for _, a := range actions {
		switch {
		case a.Error != "":
			failed++
			fmt.Printf("  %s %s — %s\n", ui.Red("✗"), short(a.Path), a.Error)
		case a.Skipped != "":
			fmt.Printf("  %s %s — %s\n", ui.Gray("–"), short(a.Path), ui.Gray(a.Skipped))
		default:
			removed++
			freed += a.FreedBytes
			line := fmt.Sprintf("  %s %s", ui.Green("✓"), short(a.Path))
			if a.Salvaged {
				salvaged++
				line += ui.Yellow(fmt.Sprintf("  salvaged %d file(s)", a.SalvagedFiles))
			}
			if a.BranchDeleted {
				line += ui.Gray("  branch deleted")
			}
			fmt.Println(line)
			if a.PatchPath != "" {
				fmt.Printf("      %s\n", ui.Gray("patch: "+short(a.PatchPath)))
			}
		}
	}
	fmt.Printf("\n  removed %d · salvaged %d · failed %d · reclaimed %s\n",
		removed, salvaged, failed, ui.Bytes(freed))
	if salvaged > 0 {
		fmt.Println(ui.Gray("  restore any salvage with: git am <patch>   (or: git log refs/pitwall/salvage/*)"))
	}
}

func treeSalvage(args []string) error {
	var g treeGlobals
	var yes bool
	fs := treeFlags("salvage", &g)
	fs.BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	fs.BoolVar(&yes, "y", false, "shorthand for --yes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := worktree.Run(g.options())
	if err != nil {
		return err
	}

	var stranded []*worktree.Worktree
	for _, repo := range res.Repos {
		for _, wt := range repo.Worktrees {
			if wt.State == worktree.StateStranded {
				stranded = append(stranded, wt)
			}
		}
	}
	if len(stranded) == 0 {
		fmt.Println("Nothing stranded — no uncommitted work without a live session.")
		return nil
	}

	if !g.asJSON {
		fmt.Printf("%s\n\n", ui.Bold("Stranded work"))
		var t ui.Table
		for _, wt := range stranded {
			t.Row(ui.Truncate(short(wt.Path), 58), ui.Yellow(fmt.Sprintf("%d file(s)", wt.Dirty())), ui.Gray(ui.Ago(wt.LastCommit)))
			for _, f := range treePreview(wt) {
				t.Row("", ui.Gray("  "+f), "")
			}
		}
		fmt.Print(t.Render("  "))
		if !yes {
			ok, err := confirm(fmt.Sprintf("\nCommit and archive %d worktree(s)?", len(stranded)))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Println("aborted")
				return nil
			}
		}
	}

	// Salvage only: keep every worktree in place.
	repos := []*worktree.Repo{{Root: "", Worktrees: stranded}}
	for _, wt := range stranded {
		wt.State = worktree.StateStranded
	}
	actions := worktree.GC(repos, worktree.GCOptions{Salvage: true, DryRun: false, SalvageOnly: true})
	if g.asJSON {
		return emitJSON(map[string]any{"actions": actions})
	}
	treePrintActions(actions)
	return nil
}

func treePreview(wt *worktree.Worktree) []string {
	all := append(append([]string{}, wt.Modified...), wt.Untracked...)
	sort.Strings(all)
	if len(all) > 6 {
		return append(all[:6], fmt.Sprintf("… and %d more", len(all)-6))
	}
	return all
}

func treeCollisions(args []string) error {
	var g treeGlobals
	fs := treeFlags("collisions", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := worktree.Run(g.options())
	if err != nil {
		return err
	}

	var reports []worktree.RepoCollisions
	for _, repo := range res.Repos {
		if c := worktree.Collisions(repo); c.Any() {
			reports = append(reports, c)
		}
	}
	if g.asJSON {
		return emitJSON(map[string]any{"collisions": reports})
	}
	if len(reports) == 0 {
		fmt.Println("No collisions — no two in-flight branches touch the same files.")
		return nil
	}

	for _, r := range reports {
		fmt.Printf("%s  %s\n", ui.Bold(filepath.Base(r.Repo)),
			ui.Gray(fmt.Sprintf("%d in-flight branches vs %s", len(r.Branches), r.Base)))
		for _, m := range r.Migrations {
			fmt.Printf("  %s migration %s used twice\n", ui.Red("!!"), ui.Bold(m.Number))
			fmt.Printf("     %s  %s\n", ui.Gray(m.BranchA), m.FileA)
			fmt.Printf("     %s  %s\n", ui.Gray(m.BranchB), m.FileB)
		}
		for _, o := range r.Overlaps {
			fmt.Printf("  %s %s × %s — %d shared file(s)\n",
				ui.Yellow("⚠"), o.BranchA, o.BranchB, len(o.Files))
			for _, f := range o.Files[:min(3, len(o.Files))] {
				fmt.Printf("     %s\n", ui.Gray(f))
			}
		}
		if len(r.Hot) > 0 {
			fmt.Printf("  %s\n", ui.Gray("most contested files:"))
			for _, h := range r.Hot[:min(5, len(r.Hot))] {
				fmt.Printf("     %s %s\n", ui.Gray(fmt.Sprintf("%d branches", len(h.Branches))), h.Path)
			}
		}
		fmt.Println()
	}
	return nil
}

func treeSessions(args []string) error {
	var g treeGlobals
	fs := treeFlags("sessions", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if g.noColor {
		ui.SetColor(false)
	}
	sessions := claude.Sessions()
	if g.asJSON {
		return emitJSON(map[string]any{"sessions": sessions})
	}
	if len(sessions) == 0 {
		fmt.Println("No Claude Code sessions registered.")
		return nil
	}
	var t ui.Table
	for _, s := range sessions {
		state := ui.Gray("stale")
		if s.Alive {
			state = ui.Green(s.Status)
		}
		t.Row(ui.Bold(s.Name), state, ui.Truncate(short(s.CWD), 58), ui.Gray(ui.Ago(s.Updated())))
	}
	fmt.Printf("%s\n\n", ui.Bold("Claude Code sessions"))
	fmt.Print(t.Render("  "))
	return nil
}

// treePrep copies the local configuration a fresh worktree is missing, which
// is the difference between an agent's first command working and failing.
func treePrep(args []string) error {
	var g treeGlobals
	var dryRun bool
	fs := treeFlags("prep", &g)
	fs.BoolVar(&dryRun, "dry-run", false, "show what would be copied")
	if err := fs.Parse(hoistFlags(args)); err != nil {
		return err
	}
	target := "."
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}
	items, err := worktree.Prep(target, dryRun)
	if err != nil {
		return err
	}
	if g.asJSON {
		return emitJSON(map[string]any{"items": items})
	}
	if len(items) == 0 {
		fmt.Println("Nothing to copy — the main checkout has no local config this worktree lacks.")
		return nil
	}
	var copied int
	for _, i := range items {
		switch {
		case i.Copied:
			copied++
			fmt.Printf("  %s %s\n", ui.Green("✓"), i.Path)
		case i.Skipped != "":
			fmt.Printf("  %s %s %s\n", ui.Gray("–"), i.Path, ui.Gray("("+i.Skipped+")"))
		default:
			fmt.Printf("  %s %s %s\n", ui.Gray("·"), i.Path, ui.Gray("(would copy)"))
		}
	}
	if dryRun {
		fmt.Println(ui.Gray("\ndry run — nothing was copied"))
	} else if copied > 0 {
		fmt.Printf("\n  copied %d file(s) from the main checkout\n", copied)
	}
	return nil
}
