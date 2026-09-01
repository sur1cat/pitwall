package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sur1cat/pitwall/internal/lint"
	"github.com/sur1cat/pitwall/internal/ui"
)

const lintUsage = `pitwall lint — check a prompt before you send it

Usage:
  pitwall lint "your prompt here"
  echo "your prompt" | pitwall lint
  pitwall lint --rules        every rule, with the measurement behind it

Every rule comes from your own history: how often prompts shaped like this one
ended in a clarifying question instead of work.
`

func cmdLint(args []string) error {
	var asJSON, rules bool
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(lintUsage) }
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&rules, "rules", false, "list every rule")
	if err := fs.Parse(hoistFlags(fs, args)); err != nil {
		return err
	}

	if rules {
		return listRules(asJSON)
	}

	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		info, err := os.Stdin.Stat()
		if err == nil && info.Mode()&os.ModeCharDevice == 0 {
			raw, _ := io.ReadAll(bufio.NewReader(os.Stdin))
			prompt = string(raw)
		}
	}
	if strings.TrimSpace(prompt) == "" {
		fmt.Print(lintUsage)
		return fmt.Errorf("nothing to check")
	}

	findings := lint.Check(prompt)
	if asJSON {
		return emitJSON(map[string]any{"findings": findings})
	}
	if len(findings) == 0 {
		fmt.Printf("%s this prompt has the shape that needed the fewest follow-ups\n", ui.Green("✓"))
		return nil
	}
	for _, f := range findings {
		fmt.Printf("\n%s %s\n", ui.Yellow("•"), ui.Bold(f.Title))
		fmt.Printf("   %s\n", ui.Gray(f.Detail))
		fmt.Printf("   %s %s\n", ui.Green("→"), f.Fix)
		fmt.Printf("   %s\n", ui.Gray(f.Evidence))
	}
	return nil
}

func listRules(asJSON bool) error {
	samples := []string{
		"почини баг с оплатой",
		"посмотри что там с очередью",
		"почини логин / добавь тест / обнови доки / задеплой",
		"сделай код ревью",
	}
	seen := map[string]lint.Rule{}
	for _, s := range samples {
		for _, f := range lint.Check(s) {
			seen[f.ID] = f.Rule
		}
	}
	if asJSON {
		var out []lint.Rule
		for _, r := range seen {
			out = append(out, r)
		}
		return emitJSON(map[string]any{"rules": out})
	}
	fmt.Printf("%s  %s\n", ui.Bold("prompt rules"), ui.Gray("measured on your own history"))
	for _, r := range seen {
		fmt.Printf("\n  %s\n", ui.Bold(r.Title))
		fmt.Printf("    %s %s\n", ui.Green("→"), r.Fix)
		fmt.Printf("    %s\n", ui.Gray(r.Evidence))
	}
	return nil
}
