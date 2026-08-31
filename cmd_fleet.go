package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/sur1cat/pitwall/internal/fleet"
	"github.com/sur1cat/pitwall/internal/ui"
)

type fleetGlobals struct {
	asJSON  bool
	all     bool
	noGit   bool
	noColor bool
}

func (g *fleetGlobals) bind(fs *flag.FlagSet) {
	fs.BoolVar(&g.asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&g.all, "all", false, "include sessions whose process has exited")
	fs.BoolVar(&g.noGit, "no-git", false, "skip git branch lookup")
	fs.BoolVar(&g.noColor, "no-color", false, "disable ANSI color")
}

func (g *fleetGlobals) options() fleet.Options {
	if g.noColor {
		ui.SetColor(false)
	}
	return fleet.Options{NoGit: g.noGit, IncludeStale: g.all}
}

func fleetFlags(name string, g *fleetGlobals) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(fleetUsage) }
	g.bind(fs)
	return fs
}

func fleetStatus(args []string) error {
	var g fleetGlobals
	fs := fleetFlags("status", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	agents := fleet.Snapshot(g.options())
	if g.asJSON {
		return emitJSON(map[string]any{"agents": agents, "waiting": len(fleet.NeedYou(agents))})
	}
	fmt.Print(fleetRender(agents))
	return nil
}

func fleetRender(agents []fleet.Agent) string {
	var b strings.Builder
	if len(agents) == 0 {
		return "No Claude Code sessions are running.\n"
	}
	need := len(fleet.NeedYou(agents))
	headline := fmt.Sprintf("%d agents", len(agents))
	if need > 0 {
		headline += ui.Gray(" · ") + ui.Yellow(fmt.Sprintf("%d need you", need))
	} else {
		headline += ui.Gray(" · nothing waiting on you")
	}
	b.WriteString(fmt.Sprintf("%s  %s\n\n", ui.Bold("pitwall fleet"), headline))

	var t ui.Table
	for _, a := range agents {
		t.Row(fleetGlyph(a.State), fleetLabel(a.State), ui.Bold(a.Name), fleetWhere(a), fleetDetail(a), ui.Gray(ui.Duration(a.Idle)))
	}
	b.WriteString(t.Render("  "))
	return b.String()
}

func fleetGlyph(s fleet.State) string {
	switch s {
	case fleet.StateWaiting:
		return ui.Yellow("▲")
	case fleet.StateDone:
		return ui.Green("✔")
	case fleet.StateWorking:
		return ui.Cyan("▸")
	case fleet.StateStale:
		return ui.Gray("×")
	default:
		return ui.Gray("·")
	}
}

func fleetLabel(s fleet.State) string {
	switch s {
	case fleet.StateWaiting:
		return ui.Yellow(string(s))
	case fleet.StateDone:
		return ui.Green(string(s))
	case fleet.StateWorking:
		return ui.Cyan(string(s))
	default:
		return ui.Gray(string(s))
	}
}

func fleetWhere(a fleet.Agent) string {
	s := a.Project
	if a.Branch != "" && a.Branch != "HEAD" {
		s += ui.Gray("@" + ui.Truncate(a.Branch, 24))
	}
	return s
}

func fleetDetail(a fleet.Agent) string {
	switch a.State {
	case fleet.StateWaiting:
		if a.Question != "" {
			return ui.Yellow("asks: ") + ui.Truncate(oneLine(a.Question), 58)
		}
		return ui.Yellow("blocked on " + a.Pending)
	case fleet.StateWorking:
		return ui.Gray("running")
	case fleet.StateDone:
		s := ui.Truncate(oneLine(a.LastText), 58)
		if a.LastTurn > 0 {
			s += ui.Gray("  (" + ui.Duration(a.LastTurn) + " turn)")
		}
		return s
	case fleet.StateStale:
		return ui.Gray("process gone")
	default:
		return ui.Gray(ui.Truncate(oneLine(a.LastText), 58))
	}
}

// oneLine flattens an assistant message into a single readable line. The raw
// text is markdown and often ends in a fenced block of git output, which turned
// a summary into "Committed and pushed. ``` 25b7f73 ← main = origin/main".
// Headings, fences, bullets and emphasis are dropped rather than shown, because
// a line truncated at 58 characters has no room to render them anyway.
func oneLine(s string) string {
	var kept []string
	inFence := false
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || line == "" {
			continue
		}
		line = strings.TrimLeft(line, "#")
		for _, bullet := range []string{"- ", "* ", "> "} {
			line = strings.TrimPrefix(strings.TrimSpace(line), bullet)
		}
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "`", "")
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	}
	return strings.Join(strings.Fields(strings.Join(kept, " · ")), " ")
}

func fleetWatch(args []string) error {
	var g fleetGlobals
	var interval time.Duration
	var execCmd string
	fs := fleetFlags("watch", &g)
	fs.DurationVar(&interval, "interval", 3*time.Second, "refresh interval")
	fs.DurationVar(&interval, "n", 3*time.Second, "shorthand for --interval")
	fs.StringVar(&execCmd, "exec", "", "command to run when an agent needs you")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if interval < time.Second {
		interval = time.Second
	}
	opt := g.options()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer func() { fmt.Print("\033[?25h") }() // restore the cursor
	fmt.Print("\033[?25l")

	previous := map[string]fleet.State{}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		agents := fleet.Snapshot(opt)
		fmt.Print("\033[H\033[2J")
		fmt.Print(fleetRender(agents))
		fmt.Printf("\n%s\n", ui.Gray(fmt.Sprintf("refreshing every %s · ctrl-c to stop · %s",
			interval, time.Now().Format("15:04:05"))))

		if execCmd != "" {
			for _, a := range agents {
				if a.State.NeedsYou() && previous[a.SessionID] != a.State {
					fleetNotify(execCmd, a)
				}
			}
		}
		previous = map[string]fleet.State{}
		for _, a := range agents {
			previous[a.SessionID] = a.State
		}

		select {
		case <-stop:
			fmt.Println()
			return nil
		case <-tick.C:
		}
	}
}

// fleetNotify runs the user's command with the agent's details in the environment.
func fleetNotify(command string, a fleet.Agent) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Env = append(os.Environ(),
		"PITWALL_NAME="+a.Name,
		"PITWALL_STATE="+string(a.State),
		"PITWALL_CWD="+a.CWD,
		"PITWALL_QUESTION="+oneLine(a.Question),
	)
	_ = cmd.Start()
	go func() { _ = cmd.Wait() }()
}

func fleetWait(args []string) error {
	var g fleetGlobals
	var target string
	var timeout, interval time.Duration
	fs := fleetFlags("wait", &g)
	fs.StringVar(&target, "for", "any", "waiting | done | any")
	fs.DurationVar(&timeout, "timeout", 0, "give up after this long")
	fs.DurationVar(&interval, "interval", 3*time.Second, "poll interval")
	fs.DurationVar(&interval, "n", 3*time.Second, "shorthand for --interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if interval < time.Second {
		interval = time.Second
	}
	opt := g.options()

	match := func(a fleet.Agent) bool {
		switch strings.ToLower(target) {
		case "waiting":
			return a.State == fleet.StateWaiting
		case "done":
			return a.State == fleet.StateDone
		default:
			return a.State.NeedsYou()
		}
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	for {
		var hits []fleet.Agent
		for _, a := range fleet.Snapshot(opt) {
			if match(a) {
				hits = append(hits, a)
			}
		}
		if len(hits) > 0 {
			if g.asJSON {
				return emitJSON(map[string]any{"agents": hits})
			}
			for _, a := range hits {
				fmt.Printf("%s %s %s %s\n", fleetGlyph(a.State), fleetLabel(a.State), ui.Bold(a.Name), fleetDetail(a))
			}
			return nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "timed out waiting for an agent")
			os.Exit(2)
		}
		select {
		case <-stop:
			os.Exit(130)
		case <-time.After(interval):
		}
	}
}

func fleetRecap(args []string) error {
	var g fleetGlobals
	fs := fleetFlags("recap", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	agents := fleet.Snapshot(g.options())
	if g.asJSON {
		return emitJSON(map[string]any{"agents": agents})
	}

	var any bool
	fmt.Printf("%s\n\n", ui.Bold("While you were away"))
	for _, a := range agents {
		text := a.Recap
		if text == "" {
			text = a.LastText
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		any = true
		fmt.Printf("  %s %s %s\n", fleetGlyph(a.State), ui.Bold(a.Name), ui.Gray(fleetWhere(a)+" · "+ui.Ago(time.Now().Add(-a.Idle))))
		for _, line := range wrapText(oneLine(text), 92) {
			fmt.Printf("      %s\n", line)
		}
		fmt.Println()
	}
	if !any {
		fmt.Println("  Nothing to report — no agent has written a summary yet.")
	}
	return nil
}

// wrapText breaks a single line into width-limited lines on word boundaries,
// counting characters rather than bytes so non-Latin text wraps correctly.
func wrapText(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line, n := words[0], utf8.RuneCountInString(words[0])
	for _, w := range words[1:] {
		lw := utf8.RuneCountInString(w)
		if n+1+lw > width {
			lines = append(lines, line)
			line, n = w, lw
			continue
		}
		line += " " + w
		n += 1 + lw
	}
	return append(lines, line)
}
