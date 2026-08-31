// Command pitwall is the instrument panel for a fleet of coding agents: what
// they are doing, what they cost, what they left behind, and which of your
// habits is spending the most.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sur1cat/pitwall/internal/ui"
)

var version = "dev"

const rootUsage = `pitwall — the instrument panel for a fleet of coding agents

Usage:
  pitwall                one screen: who is working, what today cost, what needs cleaning
  pitwall fleet          which agents are running, waiting, or done          (fleet --help)
  pitwall burn           what your usage costs, by model and effort level    (burn --help)
  pitwall tree           git worktrees your agents left behind               (tree --help)
  pitwall coach          how you actually spend, and what would change it    (coach --help)
  pitwall primer PATH    draft a CLAUDE.md from what past sessions learned
  pitwall effort         set each project's effort level once, from your own history
  pitwall lint "PROMPT"  check a prompt before you send it
  pitwall perms          which of your permission rules can never match       (perms --help)
  pitwall perms fix      remove the dead ones — dry run until --write
  pitwall quota          how much of your plan is left, from Anthropic
  pitwall focus NAME     bring that agent's terminal tab to the front
  pitwall statusline     one line for Claude Code's statusLine hook
  pitwall install        wire pitwall into Claude Code's settings
  pitwall hook EVENT     answer a Claude Code hook (used by the plugin)
  pitwall version        print the version

Everything is read-only apart from pitwall's own cache, the salvage archives
written by "tree gc", and the files "install" and "primer --write" create.
`

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, ui.Red("error: ")+err.Error())
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "", "hud", "overview":
		return cmdHUD(args)

	case "fleet":
		return fleetDispatch(args)
	case "burn":
		return burnDispatch(args)
	case "tree":
		return treeDispatch(args)

	case "coach":
		return cmdCoach(args)
	case "primer":
		return cmdPrimer(args)
	case "effort":
		return cmdEffort(args)
	case "lint":
		return cmdLint(args)
	case "perms", "permissions":
		return cmdPerms(args)
	case "quota":
		return cmdQuota(args)
	case "statusline":
		return burnStatusline(args)
	case "install":
		return cmdInstall(args)
	case "hook":
		return cmdHook(args)
	case "focus":
		return cmdFocus(args)

	case "version", "--version", "-v":
		fmt.Println("pitwall " + version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(rootUsage)
		return nil
	default:
		fmt.Print(rootUsage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func sub(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func fleetDispatch(args []string) error {
	c, rest := sub(args)
	switch c {
	case "", "status", "ls":
		return fleetStatus(rest)
	case "watch", "top":
		return fleetWatch(rest)
	case "wait":
		return fleetWait(rest)
	case "recap":
		return fleetRecap(rest)
	case "help", "--help", "-h":
		fmt.Print(fleetUsage)
		return nil
	default:
		fmt.Print(fleetUsage)
		return fmt.Errorf("unknown fleet command %q", c)
	}
}

func burnDispatch(args []string) error {
	c, rest := sub(args)
	switch c {
	case "", "summary", "status":
		return burnSummary(rest)
	case "top":
		return burnTop(rest)
	case "watch":
		return burnWatch(rest)
	case "export":
		return burnExport(rest)
	case "models":
		return burnModels(rest)
	case "help", "--help", "-h":
		fmt.Print(burnUsage)
		return nil
	default:
		fmt.Print(burnUsage)
		return fmt.Errorf("unknown burn command %q", c)
	}
}

func treeDispatch(args []string) error {
	c, rest := sub(args)
	switch c {
	case "", "status", "ls", "list":
		return treeStatus(rest)
	case "gc", "clean":
		return treeGC(rest)
	case "salvage":
		return treeSalvage(rest)
	case "collisions", "collide":
		return treeCollisions(rest)
	case "prep":
		return treePrep(rest)
	case "help", "--help", "-h":
		fmt.Print(treeUsage)
		return nil
	default:
		fmt.Print(treeUsage)
		return fmt.Errorf("unknown tree command %q", c)
	}
}

// hoistFlags moves flag arguments ahead of positional ones. Go's flag package
// stops parsing at the first non-flag argument, so "primer PATH --write"
// silently ignored --write. Anything after a bare "--" is left alone.
func hoistFlags(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			// A flag written as "--name value" takes the next argument with it.
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") &&
				flagTakesValue(strings.TrimLeft(a, "-")) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// flagTakesValue lists the flags that consume the next argument. Boolean flags
// must not, or they would swallow a path.
func flagTakesValue(name string) bool {
	switch name {
	case "path", "p", "since", "project", "limit", "for", "timeout", "interval", "n",
		"by", "format", "exec", "set", "clear", "floor", "threshold", "category":
		return true
	}
	return false
}

// ---- helpers shared by every command ----

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// short replaces the home directory with ~ for readable paths.
func short(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

func confirm(question string) (bool, error) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false, fmt.Errorf("not a terminal: re-run with --yes to confirm, or --dry-run to preview")
	}
	fmt.Printf("%s [y/N] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, nil
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

// money formats dollars with thousands separators.
func money(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := strconv.FormatFloat(v, 'f', 2, 64)
	dot := strings.IndexByte(s, '.')
	intPart, frac := s[:dot], s[dot:]
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := "$" + b.String() + frac
	if neg {
		return "-" + out
	}
	return out
}

// tokens formats a token count in compact units.
func tokens(n int64) string {
	switch {
	case n < 1_000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	default:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
