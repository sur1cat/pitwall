package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/ui"
)

const installUsage = `pitwall install — wire pitwall into Claude Code

Adds a statusLine entry to ~/.claude/settings.json so every session shows what
it is spending. The previous file is backed up next to it.

Flags:
      --print    show the change without writing anything
      --remove   take the statusLine back out
`

func cmdInstall(args []string) error {
	var printOnly, remove bool
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(installUsage) }
	fs.BoolVar(&printOnly, "print", false, "show the change without writing")
	fs.BoolVar(&remove, "remove", false, "remove the statusLine entry")
	if err := fs.Parse(args); err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil || self == "" {
		self = "pitwall"
	}
	path := filepath.Join(claude.Dir(), "settings.json")

	settings := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", short(path), err)
		}
	case !os.IsNotExist(err):
		return err
	}

	if remove {
		delete(settings, "statusLine")
	} else {
		settings["statusLine"] = map[string]any{
			"type":    "command",
			"command": self + " statusline",
		}
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	if printOnly {
		fmt.Printf("%s %s\n\n%s\n", ui.Gray("would write"), short(path), string(out))
		return nil
	}
	if len(raw) > 0 {
		backup := path + ".pitwall-backup"
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return fmt.Errorf("could not back up %s: %w", short(path), err)
		}
		fmt.Printf("%s %s\n", ui.Gray("backed up"), short(backup))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}

	if remove {
		fmt.Printf("%s statusLine removed from %s\n", ui.Green("✓"), short(path))
		return nil
	}
	fmt.Printf("%s statusLine installed in %s\n", ui.Green("✓"), short(path))
	fmt.Println(ui.Gray("  new Claude Code sessions will show: model · $ today · 5h spend · burn rate"))
	fmt.Println(ui.Gray("  set a budget with CBURN_LIMIT or PITWALL_LIMIT to get a meter"))
	return nil
}
