package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/coach"
	"github.com/sur1cat/pitwall/internal/ui"
	"github.com/sur1cat/pitwall/perms"
)

// cmdPerms routes the permission subcommands.
func cmdPerms(args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "audit", "check":
		return permsAudit(args)
	case "fix", "clean":
		return permsFix(args)
	case "help", "--help", "-h":
		fmt.Print(permsUsage)
		return nil
	default:
		return fmt.Errorf("unknown perms subcommand %q", sub)
	}
}

// permsLoad discovers every settings file with permission rules and reads them.
// Directories come from history.jsonl as well as the transcripts, because
// transcripts are pruned after a month and a project that has been quiet since
// then still has a settings file full of rules.
func permsLoad(repoFilter string) ([]perms.Rule, []perms.Source, map[string]int) {
	dirs := map[string]bool{}
	for _, cwd := range append(claude.Workdirs(), claude.HistoryDirs()...) {
		dirs[cwd] = true
		if r := coach.RepoOf(cwd); r != "" {
			dirs[r] = true
		}
	}
	list := make([]string, 0, len(dirs))
	for r := range dirs {
		list = append(list, r)
	}
	sort.Strings(list)

	var rules []perms.Rule
	var used []perms.Source
	kinds := map[string]int{}
	for _, src := range perms.Discover(claude.Dir(), list) {
		if repoFilter != "" && !strings.Contains(src.Path, repoFilter) {
			continue
		}
		got := perms.Read(src)
		if len(got) == 0 {
			continue
		}
		used = append(used, src)
		for _, r := range got {
			rules = append(rules, r)
			kinds[r.Kind]++
		}
	}
	return rules, used, kinds
}

// permsFix removes the rules that cannot do anything and repairs the ones that
// can be repaired without granting anything new. It writes nothing unless
// asked, and copies every file it touches first.
func permsFix(args []string) error {
	var write, dropOneOffs, noColor, asJSON bool
	var repoFilter string
	fs := flag.NewFlagSet("perms fix", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(permsFixUsage) }
	fs.BoolVar(&write, "write", false, "apply the changes (default is a dry run)")
	fs.BoolVar(&dropOneOffs, "drop-one-offs", false, "also remove literal rules that only match one command")
	fs.StringVar(&repoFilter, "project", "", "only settings files under this project")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	if err := fs.Parse(hoistFlags(fs, args)); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	rules, sources, _ := permsLoad(repoFilter)
	if len(rules) == 0 {
		fmt.Println("No permission rules found.")
		return nil
	}
	plans := perms.PlanFixes(perms.Lint(rules), perms.Options{DropOneOffs: dropOneOffs})

	var totalRemove, totalRewrite int
	for _, p := range plans {
		totalRemove += len(p.Remove)
		totalRewrite += len(p.Rewrite)
	}

	if asJSON {
		byCategory := map[string]int{}
		for _, p := range plans {
			for _, f := range p.Remove {
				byCategory[f.Category]++
			}
			for _, f := range p.Report {
				byCategory[f.Category]++
			}
			for range p.Rewrite {
				byCategory["repairable"]++
			}
		}
		out := map[string]any{
			"files": len(sources), "rules": len(rules), "written": write,
			"remove": totalRemove, "rewrite": totalRewrite,
			"by_category": byCategory, "plans": permsPlanJSON(plans),
			"secrets": permsSecretCount(plans),
		}
		if write {
			applied, backups := permsApplyAll(plans, filepath.Join(claude.Dir(), "pitwall", "perms-backups"))
			out["applied"] = applied
			out["backups"] = backups
		}
		return emitJSON(out)
	}

	head := ui.Gray("dry run — nothing is written")
	if write {
		head = ui.Yellow("writing")
	}
	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall perms fix"), head)

	if totalRemove == 0 && totalRewrite == 0 {
		fmt.Println("  Nothing to clean.")
		return nil
	}

	backupDir := filepath.Join(claude.Dir(), "pitwall", "perms-backups")
	var touched int
	for _, p := range plans {
		if p.Empty() {
			continue
		}
		touched++
		fmt.Printf("  %s\n", ui.Bold(p.Source.Short()))
		if n := len(p.Remove); n > 0 {
			fmt.Printf("    %s %-4d %s\n", ui.Red("remove "), n, ui.Gray(permsBreakdown(p.Remove)))
		}
		if n := len(p.Rewrite); n > 0 {
			for _, w := range p.Rewrite {
				fmt.Printf("    %s %s %s %s\n", ui.Green("rewrite"), w.Rule.Raw, ui.Gray("→"), ui.Green(w.To))
			}
		}
		if n := len(p.Report); n > 0 {
			fmt.Printf("    %s %-4d %s\n", ui.Gray("keep   "), n, ui.Gray(permsBreakdown(p.Report)))
		}
		if write {
			backup, err := perms.Apply(p, backupDir)
			if err != nil {
				fmt.Printf("    %s %v\n", ui.Red("failed:"), err)
				continue
			}
			fmt.Printf("    %s %s\n", ui.Gray("backup"), ui.Gray(backup))
		}
		fmt.Println()
	}

	fmt.Printf("  %s %d rules removed, %d rewritten, across %d files\n",
		ui.Bold("total:"), totalRemove, totalRewrite, touched)
	if write {
		fmt.Printf("  %s %s\n", ui.Gray("originals copied to"), ui.Gray(permsShort(backupDir)))
		if n := permsSecretCount(plans); n > 0 {
			fmt.Printf("\n  %s %d rules held a credential. They are gone from the settings files,\n",
				ui.Yellow("rotate:"), n)
			fmt.Printf("           but the values still exist in the backups above, in your shell history\n")
			fmt.Printf("           and in old transcripts. Rotate them and keep them in the environment.\n")
		}
	} else {
		fmt.Printf("  %s\n", ui.Gray("nothing was written — add --write to apply, each file is copied first"))
	}
	return nil
}

// permsApplyAll writes every non-empty plan and returns what happened, so the
// JSON path and the text path cannot drift apart in what they actually do.
func permsApplyAll(plans []perms.Plan, backupDir string) (applied int, backups []string) {
	for _, p := range plans {
		if p.Empty() {
			continue
		}
		backup, err := perms.Apply(p, backupDir)
		if err != nil {
			continue
		}
		applied++
		if backup != "" {
			backups = append(backups, backup)
		}
	}
	return applied, backups
}

// permsBreakdown summarises a set of findings as "category n · category n",
// heaviest first, so a line of counts explains itself.
func permsBreakdown(fs []perms.Finding) string {
	counts := map[string]int{}
	for _, f := range fs {
		counts[f.Category]++
	}
	type pair struct {
		name string
		n    int
	}
	list := make([]pair, 0, len(counts))
	for k, v := range counts {
		list = append(list, pair{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].name < list[j].name
	})
	parts := make([]string, 0, len(list))
	for _, p := range list {
		parts = append(parts, fmt.Sprintf("%s %d", p.name, p.n))
	}
	return strings.Join(parts, " · ")
}

// permsSecretCount is how many removed rules carried a credential.
func permsSecretCount(plans []perms.Plan) int {
	n := 0
	for _, p := range plans {
		for _, f := range p.Remove {
			if f.Category == "secret" {
				n++
			}
		}
	}
	return n
}

func permsPlanJSON(plans []perms.Plan) []map[string]any {
	out := make([]map[string]any, 0, len(plans))
	for _, p := range plans {
		if p.Empty() {
			continue
		}
		rw := make([]map[string]string, 0, len(p.Rewrite))
		for _, w := range p.Rewrite {
			rw = append(rw, map[string]string{"from": w.Rule.Raw, "to": w.To})
		}
		out = append(out, map[string]any{
			"file": p.Source.Short(), "scope": p.Source.Scope.String(),
			"remove": len(p.Remove), "remove_by": permsBreakdown(p.Remove),
			"rewrite": rw, "kept": len(p.Report),
		})
	}
	return out
}

func permsShort(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// permsAudit reports the permission rules Claude Code has accumulated. It reads
// and reports; it never edits a settings file.
func permsAudit(args []string) error {
	var asJSON, noColor bool
	var debugPerms bool
	var only, repoFilter string
	var limit int
	fs := flag.NewFlagSet("perms", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(permsUsage) }
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	fs.StringVar(&only, "category", "", "show only this category")
	fs.StringVar(&repoFilter, "project", "", "only rules from this project")
	fs.IntVar(&limit, "n", 5, "examples to show per category")
	fs.BoolVar(&debugPerms, "debug", false, "report what the scan looked at")
	if err := fs.Parse(hoistFlags(fs, args)); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	// Two directories matter per session, and they are not always the same:
	// settings.local.json loads from the git repository root, resolved through
	// worktrees to the main checkout, while settings.json loads from the
	// working directory itself with no parent fallback. A worktree session
	// therefore reads its own shared file but the main checkout's local one.
	dirs := map[string]bool{}
	cwds := append(claude.Workdirs(), claude.HistoryDirs()...)
	for _, cwd := range cwds {
		dirs[cwd] = true
		if r := coach.RepoOf(cwd); r != "" {
			dirs[r] = true
		}
	}
	list := make([]string, 0, len(dirs))
	for r := range dirs {
		list = append(list, r)
	}
	sort.Strings(list)
	if debugPerms {
		fmt.Printf("%s %d working directories, %d candidate settings roots\n",
			ui.Gray("scan:"), len(cwds), len(list))
	}

	sources := perms.Discover(claude.Dir(), list)
	var rules []perms.Rule
	kinds := map[string]int{}
	for _, s := range sources {
		if repoFilter != "" && !strings.Contains(s.Path, repoFilter) {
			continue
		}
		for _, r := range perms.Read(s) {
			rules = append(rules, r)
			kinds[r.Kind]++
		}
	}
	if len(rules) == 0 {
		fmt.Println("No permission rules found.")
		return nil
	}

	findings := perms.Lint(rules)
	if only != "" {
		var kept []perms.Finding
		for _, f := range findings {
			if f.Category == only {
				kept = append(kept, f)
			}
		}
		findings = kept
	}

	if asJSON {
		return emitJSON(map[string]any{
			"files": len(sources), "rules": len(rules), "by_kind": kinds,
			"findings": permsJSON(findings), "prefixes": perms.Prefixes(findings, 3),
		})
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall perms"),
		ui.Gray(fmt.Sprintf("%d rules across %d files · %d allow · %d deny · %d ask",
			len(rules), len(sources), kinds["allow"], kinds["deny"], kinds["ask"])))

	if len(findings) == 0 {
		fmt.Println("  Nothing to report — every rule can match something.")
		return nil
	}

	byCat := map[string][]perms.Finding{}
	for _, f := range findings {
		byCat[f.Category] = append(byCat[f.Category], f)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool {
		return perms.CategoryRank(cats[i]) < perms.CategoryRank(cats[j])
	})

	var t ui.Table
	t.Row(ui.Gray("finding"), ui.Gray("rules"), ui.Gray("what it means"))
	for _, c := range cats {
		t.Row(permsTint(c), fmt.Sprintf("%d", len(byCat[c])), ui.Gray(permsBlurb[c]))
	}
	fmt.Print(t.Render("  "))

	for _, c := range cats {
		fs := byCat[c]
		fmt.Printf("\n%s %s\n", ui.Bold(c), ui.Gray(fmt.Sprintf("(%d)", len(fs))))
		for i, f := range fs {
			if i >= limit {
				fmt.Printf("  %s\n", ui.Gray(fmt.Sprintf("… and %d more", len(fs)-limit)))
				break
			}
			fmt.Printf("  %s\n", permsRuleText(f))
			fmt.Printf("      %s %s\n", ui.Gray("·"), ui.Gray(f.Why))
			if f.Fix != "" {
				fmt.Printf("      %s %s\n", ui.Gray("→"), ui.Green(f.Fix))
			}
			fmt.Printf("      %s %s\n", ui.Gray("in"), ui.Gray(f.Rule.Source.Short()))
		}
	}

	if pfx := perms.Prefixes(findings, 3); len(pfx) > 0 {
		fmt.Printf("\n%s\n", ui.Bold("one rule would replace many"))
		fmt.Printf("  %s\n", ui.Gray("widening a permission is your call — pitwall only shows the arithmetic"))
		var t2 ui.Table
		for i, p := range pfx {
			if i >= 8 {
				break
			}
			t2.Row("Bash("+p.Pattern+")", ui.Gray(fmt.Sprintf("replaces %d one-off rules", p.Covers)))
		}
		fmt.Print(t2.Render("  "))
	}
	fmt.Printf("\n  %s\n", ui.Gray("pitwall reads these files and never edits them"))
	return nil
}

var permsBlurb = map[string]string{
	"secret":          "a credential is stored in the rule text",
	"ignored":         "Claude Code skips the rule when it loads settings",
	"never-consulted": "the rule loads but file access is never checked against it",
	"fragment":        "not a command — a comment or a piece of a multi-line block",
	"unmatchable":     "contains a shell separator, so it can never match",
	"wildcard-inside": "a * before the end matches far more than it looks like",
	"duplicate":       "the same rule is already present",
	"shadowed":        "a broader rule already covers it",
	"one-off":         "no wildcard, so it matches one exact command forever",
}

func permsTint(c string) string {
	switch c {
	case "secret", "wildcard-inside":
		return ui.Yellow(c)
	case "ignored", "never-consulted", "unmatchable", "fragment":
		return ui.Red(c)
	default:
		return c
	}
}

// permsRuleText renders a rule, holding back the text of one that carries a
// credential — printing it would copy the secret into a terminal, a scrollback
// buffer and very likely another transcript.
func permsRuleText(f perms.Finding) string {
	if f.Redacted {
		return f.Rule.Kind + " " + f.Rule.Tool + "(" + ui.Yellow("«redacted — contains a credential»") + ")"
	}
	return f.Rule.Kind + " " + f.Rule.Raw
}

func permsJSON(fs []perms.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		m := map[string]any{
			"category": f.Category, "why": f.Why, "kind": f.Rule.Kind,
			"tool": f.Rule.Tool, "source": f.Rule.Source.Short(),
			"scope": f.Rule.Source.Scope.String(),
		}
		if f.Redacted {
			m["rule"] = "«redacted»"
		} else {
			m["rule"] = f.Rule.Raw
		}
		if f.Fix != "" {
			m["fix"] = f.Fix
		}
		out = append(out, m)
	}
	return out
}

const permsFixUsage = `pitwall perms fix — remove the rules that can never match

Removes rules that cannot do anything: comment lines and fragments of
multi-line blocks, rules containing a shell separator, exact duplicates, rules
a broader rule already covers, and rules with a credential in the text.
Repairs a rule only when the repair does not grant anything new, which means
deny and ask rules are corrected and a broken allow rule is reported instead.
Managed settings are never touched.

Usage:
  pitwall perms fix [--write]

Flags:
      --write          apply the changes; without it nothing is written
      --drop-one-offs  also remove literal rules that match one command forever
      --project NAME   only settings files under this project
      --json           machine-readable output
      --no-color       disable ANSI color

Every file is copied to ~/.claude/pitwall/perms-backups before it is changed.
`

const permsUsage = `pitwall perms — what your permission rules actually do

Reads every settings file that carries permission rules and reports the ones
that cannot do what they look like they do: rules Claude Code skips on load,
rules that can never match, rules a broader rule already covers, and rules
with a credential baked into the text.

Usage:
  pitwall perms [flags]

Flags:
      --category NAME  show only one finding category
      --project NAME   only rules from settings files under this project
      --n N            examples per category (default 5)
      --json           machine-readable output
      --no-color       disable ANSI color

pitwall reads these files and never edits them.
`
