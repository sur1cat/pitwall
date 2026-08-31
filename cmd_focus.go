package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/sur1cat/pitwall/internal/fleet"
	"github.com/sur1cat/pitwall/internal/ui"
)

const focusUsage = `pitwall focus — bring an agent's terminal to the front

Usage:
  pitwall focus NAME     the session name shown by "pitwall fleet"
  pitwall focus PID      the process id

Finds the terminal tab that session is running in and selects it. Supported in
Terminal.app and iTerm2; falls back to opening the agent's directory.
`

func cmdFocus(args []string) error {
	fs := flag.NewFlagSet("focus", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(focusUsage) }
	if err := fs.Parse(hoistFlags(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fmt.Print(focusUsage)
		return fmt.Errorf("name or pid required")
	}
	target := fs.Arg(0)

	agent, err := findAgent(target)
	if err != nil {
		return err
	}
	if tty := ttyOf(agent.PID); tty != "" {
		if focusTerminalTab(tty) {
			return nil
		}
	}
	// No matching tab: open the agent's directory so you at least land there.
	if agent.CWD != "" {
		return exec.Command("open", "-a", "Terminal", agent.CWD).Run()
	}
	return fmt.Errorf("could not find a terminal for %s (pid %d)", agent.Name, agent.PID)
}

func findAgent(target string) (fleet.Agent, error) {
	agents := fleet.Snapshot(fleet.Options{NoGit: true, IncludeStale: true})
	if pid, err := strconv.Atoi(target); err == nil {
		for _, a := range agents {
			if a.PID == pid {
				return a, nil
			}
		}
		return fleet.Agent{}, fmt.Errorf("no session with pid %d", pid)
	}
	for _, a := range agents {
		if strings.EqualFold(a.Name, target) || a.SessionID == target {
			return a, nil
		}
	}
	return fleet.Agent{}, fmt.Errorf("no session called %q — see: pitwall fleet", target)
}

// ttyOf returns the controlling terminal of a process, as /dev/ttysNNN.
func ttyOf(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	t := strings.TrimSpace(string(out))
	if t == "" || t == "??" || t == "-" {
		return ""
	}
	if !strings.HasPrefix(t, "/dev/") {
		t = "/dev/" + t
	}
	return t
}

// focusTerminalTab selects the tab bound to tty in whichever terminal owns it.
func focusTerminalTab(tty string) bool {
	scripts := []string{
		// Terminal.app
		`tell application "Terminal"
			repeat with w in windows
				repeat with t in tabs of w
					if tty of t is "` + tty + `" then
						set selected of t to true
						set index of w to 1
						activate
						return "ok"
					end if
				end repeat
			end repeat
		end tell
		return "no"`,
		// iTerm2
		`tell application "iTerm2"
			repeat with w in windows
				repeat with t in tabs of w
					repeat with s in sessions of t
						if tty of s is "` + tty + `" then
							select w
							select t
							select s
							activate
							return "ok"
						end if
					end repeat
				end repeat
			end repeat
		end tell
		return "no"`,
	}
	for _, script := range scripts {
		out, err := exec.Command("osascript", "-e", script).Output()
		if err == nil && strings.TrimSpace(string(out)) == "ok" {
			return true
		}
	}
	return false
}

var _ = ui.Bold
