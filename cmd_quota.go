package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/quota"
	"github.com/sur1cat/pitwall/internal/ui"
)

const quotaUsage = `pitwall quota — how much of your plan is left, from Anthropic

Usage:
  pitwall quota          the 5-hour and rolling windows, with a projection
  pitwall quota --force  ignore the 180-second cache

This is the one pitwall command that uses the network. It calls the same usage
endpoint Claude Code calls, with the credential Claude Code already stored, and
sends nothing anywhere else. macOS will ask once for permission to read that
credential from the keychain.
`

func cmdQuota(args []string) error {
	var asJSON, force, noColor bool
	fs := flag.NewFlagSet("quota", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(quotaUsage) }
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&force, "force", false, "ignore the cache")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	if err := fs.Parse(hoistFlags(fs, args)); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}

	usage, err := quota.Get(quota.Options{
		Dir:   filepath.Join(claude.Dir(), "pitwall"),
		Force: force,
	})
	if err != nil && usage.FetchedAt.IsZero() {
		return err
	}
	if asJSON {
		return emitJSON(usage)
	}

	fmt.Printf("%s  %s\n\n", ui.Bold("plan usage"), ui.Gray(sourceNote(usage)))
	var t ui.Table
	t.Row(ui.Gray("window"), ui.Gray("used"), ui.Gray("resets in"), ui.Gray("projection"))
	t.Row("5 hours", meterFor(usage.FiveHour), until(usage.FiveHour),
		projection(usage.FiveHour, usage.FiveHourPace, quota.Pace{}))
	avg, _ := usage.SevenDay.Average(quota.WeekLength)
	t.Row("7 days", meterFor(usage.SevenDay), until(usage.SevenDay),
		projection(usage.SevenDay, avg, usage.SevenDayPace))
	// The plan stacks several limits and Anthropic's own dashboard shows one.
	// A weekly allowance for a single model runs out on its own schedule, and
	// hitting it is indistinguishable from hitting the overall one unless
	// both are on screen.
	for _, w := range []struct {
		label string
		win   *quota.Window
	}{
		{"7d opus", usage.SevenDayOpus},
		{"7d sonnet", usage.SevenDaySonnet},
	} {
		if w.win == nil {
			continue
		}
		a, _ := w.win.Average(quota.WeekLength)
		t.Row(w.label, meterFor(*w.win), until(*w.win), projection(*w.win, a, quota.Pace{}))
	}
	if e := usage.ExtraUsage; e != nil && e.IsEnabled && e.Utilization != nil {
		t.Row("extra", meterFor(quota.Window{Utilization: *e.Utilization}), "",
			ui.Gray(extraNote(e.UsedCredits, e.MonthlyLimit)))
	}
	fmt.Print(t.Render("  "))

	if avg.OK {
		fmt.Printf("\n  %s\n", ui.Gray(fmt.Sprintf(
			"the 7-day figure is your average of %.1f%%/h over the %s this window has been open",
			avg.PerHour, ui.Duration(avg.Span))))
		if recent := usage.SevenDayPace; recent.Trustworthy() && recent.PerHour > avg.PerHour*1.5 {
			d, ok := usage.SevenDay.ExhaustedIn(recent)
			line := fmt.Sprintf("you are going %.1f%%/h right now, well above that", recent.PerHour)
			if ok {
				line += " — at that rate, full in " + ui.Duration(d)
			}
			fmt.Printf("  %s\n", ui.Yellow(line))
		}
	}

	if err != nil {
		fmt.Printf("\n  %s %v\n", ui.Yellow("note:"), err)
	}
	if usage.SevenDayOpus == nil && usage.SevenDaySonnet == nil {
		fmt.Printf("\n  %s\n", ui.Gray("this plan reports no per-model weekly windows; on plans that have them\n"+
			"  they run out on their own schedule and are shown here too"))
	}

	fmt.Printf("\n%s\n", ui.Gray("the 5-hour window is rolling, so pitwall measures its pace from its own\n"+
		"readings; the 7-day window is weekly and resets as a whole, so its rate is\n"+
		"read from how much of it is gone and how long it has been open"))
	return nil
}

// extraNote describes pay-as-you-go usage beyond the plan.
func extraNote(used, limit *float64) string {
	if used == nil {
		return "extra usage is on"
	}
	if limit == nil {
		return fmt.Sprintf("$%.2f of extra usage, no monthly cap set", *used)
	}
	return fmt.Sprintf("$%.2f of $%.2f monthly extra usage", *used, *limit)
}

func sourceNote(u quota.Usage) string {
	if u.FetchedAt.IsZero() {
		return "no reading yet"
	}
	if u.Cached {
		return "cached, read at " + u.FetchedAt.Format("15:04")
	}
	return "read at " + u.FetchedAt.Format("15:04")
}

func meterFor(w quota.Window) string {
	const width = 12
	filled := int(w.Utilization / 100 * width)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "▮"
		} else {
			bar += "▯"
		}
	}
	label := fmt.Sprintf("%s %3.0f%%", bar, w.Utilization)
	switch {
	case w.Utilization >= 90:
		return ui.Red(label)
	case w.Utilization >= 70:
		return ui.Yellow(label)
	default:
		return ui.Green(label)
	}
}

func until(w quota.Window) string {
	d := w.Until()
	if d == 0 {
		return ui.Gray("—")
	}
	return ui.Duration(d)
}

// projection renders when a window fills. It leads with whichever rate rests
// on more evidence: the average since the window opened when that is known,
// and otherwise pitwall's own readings — but only once they have seen enough
// movement to survive the rounding in a whole-percent reading.
func projection(w quota.Window, primary, fallback quota.Pace) string {
	if w.Utilization >= 100 {
		return ui.Red("exhausted")
	}
	p := primary
	if !p.OK {
		p = fallback
	}
	if !p.OK {
		return ui.Gray("measuring — needs a few more readings")
	}
	if !p.Trustworthy() {
		return ui.Gray("too little movement to project yet")
	}
	if p.PerHour <= 0 {
		return ui.Green("idle")
	}
	d, ok := w.ExhaustedIn(p)
	if !ok {
		return ui.Green("lasts the window")
	}
	// Minutes imply a precision a whole-percent reading cannot support.
	if d >= 6*time.Hour {
		d = d.Round(time.Hour)
	}
	if d < time.Hour {
		return ui.Red("full in " + ui.Duration(d))
	}
	return ui.Yellow("full in " + ui.Duration(d))
}
