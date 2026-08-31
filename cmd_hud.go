package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sur1cat/pitwall/internal/burn"
	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/fleet"
	"github.com/sur1cat/pitwall/internal/quota"
	"github.com/sur1cat/pitwall/internal/ui"
	"github.com/sur1cat/pitwall/internal/worktree"
)

// cmdHUD is the default screen: everything worth a glance, nothing that takes
// longer than a couple of seconds to compute.
func cmdHUD(args []string) error {
	var noColor, asJSON, noTree bool
	fs := flag.NewFlagSet("hud", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(rootUsage) }
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&noTree, "no-tree", false, "skip the worktree scan")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	agents := fleet.Snapshot(fleet.Options{})
	records := burn.Scan(true).Records
	now := time.Now()
	today := burnSum(burnAfter(records, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())))
	window := burnSum(burnAfter(records, now.Add(-5*time.Hour)))

	// Subagent fan-out is close to half of all messages and is invisible
	// everywhere else, so the glance view carries its share of the spend.
	var subRecords []burn.Record
	for _, r := range records {
		if r.Sub {
			subRecords = append(subRecords, r)
		}
	}
	todaySub := burnSum(burnAfter(subRecords, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())))
	windowSub := burnSum(burnAfter(subRecords, now.Add(-5*time.Hour)))

	var totals worktree.Totals
	var reclaim int64
	var stranded int
	if !noTree {
		if res, err := worktree.Run(worktree.Options{}); err == nil {
			totals = worktree.Summarize(res.Repos)
			for _, repo := range res.Repos {
				for _, wt := range repo.Worktrees {
					if wt.State.Removable() {
						reclaim += wt.SizeBytes
					}
					if wt.State == worktree.StateStranded {
						stranded++
					}
				}
			}
		}
	}

	quotaReading, quotaErr := quota.Get(quota.Options{
		Dir:       filepath.Join(claude.Dir(), "pitwall"),
		CacheOnly: noTree, // the slow path may refresh; the fast one never waits
	})

	if asJSON {
		out := map[string]any{
			"agents": agents, "waiting": len(fleet.NeedYou(agents)),
			"today": today, "window_5h": window,
			"today_subagent": todaySub, "window_5h_subagent": windowSub,
		}
		if !noTree {
			out["worktrees"] = totals
			out["stranded"] = stranded
			out["reclaim_bytes"] = reclaim
		}
		if quotaErr == nil || !quotaReading.FetchedAt.IsZero() {
			out["quota"] = quotaReading
		}
		if r := claude.Retain(); r.Trimming() {
			out["retention"] = map[string]any{
				"days": r.Days(), "limit": r.EffectiveLimit(), "configured": r.Set,
			}
		}
		return emitJSON(out)
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall"), ui.Gray(now.Format("Mon 15:04")))

	counts := map[fleet.State]int{}
	for _, a := range agents {
		counts[a.State]++
	}
	if len(agents) == 0 {
		fmt.Printf("  %s %s\n", ui.Gray("agents "), ui.Gray("none running"))
	} else {
		fmt.Printf("  %s %s\n", ui.Gray("agents "), agentSummary(counts))
		for _, a := range agents {
			if a.State.NeedsYou() {
				fmt.Printf("          %s %s %s %s\n", fleetGlyph(a.State), fleetLabel(a.State),
					ui.Bold(a.Name), fleetDetail(a))
			}
		}
	}

	if quotaErr == nil || !quotaReading.FetchedAt.IsZero() {
		fmt.Printf("  %s %s\n", ui.Gray("plan   "), quotaLine(quotaReading))
	}

	rate := window.Dollars() / 5
	fmt.Printf("  %s %s today   %s in the last 5h   %s\n", ui.Gray("spend  "),
		ui.Bold(money(today.Dollars())), money(window.Dollars()),
		ui.Gray(fmt.Sprintf("burn %s/h", money(rate))))

	if sub := todaySub.Dollars(); sub > 0 && today.Dollars() > 0 {
		fmt.Printf("          %s\n", ui.Gray(fmt.Sprintf(
			"%s of that went to subagents (%.0f%%)", money(sub), sub/today.Dollars()*100)))
	}

	if !noTree && totals.Worktrees > 0 {
		line := fmt.Sprintf("%d worktrees", totals.Worktrees)
		if n := totals.ByState[worktree.StateDead] + totals.ByState[worktree.StateOrphan]; n > 0 {
			line += fmt.Sprintf("   %d removable", n)
			if reclaim > 0 {
				line += " (" + ui.Bytes(reclaim) + ")"
			}
		}
		if stranded > 0 {
			line += "   " + ui.Yellow(fmt.Sprintf("%d holding unsaved work", stranded))
		}
		fmt.Printf("  %s %s\n", ui.Gray("git    "), line)
	}

	fmt.Printf("\n%s\n", ui.Gray("pitwall fleet · burn · tree · coach"))
	return nil
}

func agentSummary(counts map[fleet.State]int) string {
	parts := []string{}
	add := func(s fleet.State, render func(string) string) {
		if n := counts[s]; n > 0 {
			parts = append(parts, render(fmt.Sprintf("%d %s", n, s)))
		}
	}
	add(fleet.StateWaiting, ui.Yellow)
	add(fleet.StateDone, ui.Green)
	add(fleet.StateWorking, ui.Cyan)
	add(fleet.StateIdle, ui.Gray)
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ui.Gray(" · ")
		}
		out += p
	}
	return out
}

// quotaLine renders both windows compactly, with a warning when the current
// pace runs one of them out before it resets.
// quotaLine is the glance version of what "pitwall quota" prints, and must
// agree with it. Both lead with the rate that rests on more evidence: for the
// weekly window that is its own elapsed time, which covers days, rather than
// pitwall's readings, which may cover minutes.
func quotaLine(u quota.Usage) string {
	part := func(name string, w quota.Window, p quota.Pace) string {
		s := fmt.Sprintf("%s %.0f%%", name, w.Utilization)
		switch {
		case w.Utilization >= 90:
			s = ui.Red(s)
		case w.Utilization >= 70:
			s = ui.Yellow(s)
		}
		if !p.Trustworthy() {
			return s
		}
		if d, ok := w.ExhaustedIn(p); ok {
			if d >= 6*time.Hour {
				d = d.Round(time.Hour)
			}
			s += ui.Yellow(" full in " + ui.Duration(d))
		}
		return s
	}
	week := u.SevenDayPace
	if avg, ok := u.SevenDay.Average(quota.WeekLength); ok {
		week = avg
	}
	return part("5h", u.FiveHour, u.FiveHourPace) + "   " +
		part("7d", u.SevenDay, week) +
		ui.Gray("   resets in "+ui.Duration(u.FiveHour.Until()))
}
