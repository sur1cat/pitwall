package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
	"github.com/sur1cat/pitwall/internal/ui"
)

// cmdRecall brings back what a compaction threw away.
//
// When Claude Code summarises a conversation to fit, the detail is gone from
// the model and there is no way to ask what it was. But it never left the
// disk: the boundary record lists exactly which messages survived, so
// everything before it that is not on that list is recoverable by subtraction.
// The result is written to a file, because a file can be pulled back into any
// session with @ — no hook, nothing to install, and nothing that can go wrong
// silently.
func cmdRecall(args []string) error {
	var out, project, session string
	var asJSON, noColor, mine bool
	var limit int
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(recallUsage) }
	fs.StringVar(&out, "out", "", "write the recovered text to this file")
	fs.StringVar(&project, "project", "", "only compactions in this project")
	fs.StringVar(&session, "session", "", "only this session")
	fs.BoolVar(&mine, "mine", false, "only the prompts you typed")
	fs.IntVar(&limit, "n", 12, "how many events to list")
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color")
	if err := fs.Parse(hoistFlags(args)); err != nil {
		return err
	}
	if noColor {
		ui.SetColor(false)
	}
	query := strings.ToLower(strings.Join(fs.Args(), " "))

	events := claude.Compactions()
	var kept []claude.Compaction
	for _, e := range events {
		if project != "" && !strings.Contains(e.Project, project) {
			continue
		}
		if session != "" && !strings.HasPrefix(e.Session, session) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		fmt.Println("No compactions found in the retained transcripts.")
		return nil
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].At.After(kept[j].At) })

	if query != "" || out != "" || mine {
		return recallContent(kept, query, out, mine, asJSON)
	}
	return recallList(kept, limit, asJSON)
}

// recallList shows what has been compacted away and how to get it back.
func recallList(events []claude.Compaction, limit int, asJSON bool) error {
	if asJSON {
		rows := make([]map[string]any, 0, len(events))
		for _, e := range events {
			rows = append(rows, map[string]any{
				"at": e.At, "session": e.Session, "project": e.Project,
				"dropped": e.Dropped, "stall_seconds": e.Stall.Seconds(), "trigger": e.Trigger,
			})
		}
		return emitJSON(map[string]any{"compactions": rows})
	}

	var dropped int64
	var stall time.Duration
	for _, e := range events {
		dropped += e.Dropped
		stall += e.Stall
	}
	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall recall"),
		ui.Gray(fmt.Sprintf("%d compactions dropped %s and waited %s",
			len(events), tokens(dropped), ui.Duration(stall))))

	var t ui.Table
	t.Row(ui.Gray("when"), ui.Gray("project"), ui.Gray("dropped"), ui.Gray("waited"), ui.Gray("session"))
	for i, e := range events {
		if i >= limit {
			fmt.Print(t.Render("  "))
			fmt.Printf("  %s\n", ui.Gray(fmt.Sprintf("… and %d more", len(events)-limit)))
			t = ui.Table{}
			break
		}
		t.Row(e.At.Format("Jan 2 15:04"), e.Project, tokens(e.Dropped),
			ui.Duration(e.Stall), ui.Gray(shortID(e.Session)))
	}
	fmt.Print(t.Render("  "))

	fmt.Printf("\n%s\n", ui.Bold("getting it back"))
	fmt.Printf("  %s   %s\n", "pitwall recall WORD", ui.Gray("search what was thrown away"))
	fmt.Printf("  %s   %s\n", "pitwall recall --session ID --out recovered.md", ui.Gray("write it to a file"))
	fmt.Printf("  %s\n", ui.Gray("then pull the file back into a session with @recovered.md"))
	return nil
}

// recallContent searches or exports the discarded messages themselves.
// recallContent searches or exports the discarded messages themselves. The
// mine filter earns its place from a measurement: across 37 compactions here,
// 302 records were preserved verbatim and 3 of them were prose the human had
// typed. Everything else kept was the agent's own output and tool results, so
// what a compaction reliably loses is precisely what you asked for.
func recallContent(events []claude.Compaction, query, out string, mine, asJSON bool) error {
	type hit struct {
		Event claude.Compaction
		Msg   claude.Dropped
	}
	var hits []hit
	for _, e := range events {
		for _, d := range claude.DroppedFrom(e.Path, e.UUID) {
			if mine && !d.Typed {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(d.Text), query) {
				continue
			}
			hits = append(hits, hit{e, d})
		}
	}
	if len(hits) == 0 {
		if query != "" {
			fmt.Printf("Nothing matching %q was thrown away.\n", query)
		} else {
			fmt.Println("Nothing recoverable — the compaction kept everything it had.")
		}
		return nil
	}

	if asJSON {
		rows := make([]map[string]any, 0, len(hits))
		for _, h := range hits {
			rows = append(rows, map[string]any{
				"at": h.Msg.At, "role": h.Msg.Role, "text": h.Msg.Text,
				"tools": h.Msg.Tools, "session": h.Event.Session, "project": h.Event.Project,
			})
		}
		return emitJSON(map[string]any{"recovered": rows})
	}

	if out != "" {
		return writeRecovered(out, hits[0].Event, func(yield func(claude.Dropped)) {
			for _, h := range hits {
				yield(h.Msg)
			}
		}, len(hits))
	}

	what := fmt.Sprintf("%d discarded messages mention %q", len(hits), query)
	if query == "" {
		what = fmt.Sprintf("%d prompts you typed were discarded", len(hits))
	} else if mine {
		what = fmt.Sprintf("%d prompts you typed mention %q", len(hits), query)
	}
	fmt.Printf("%s  %s\n\n", ui.Bold("pitwall recall"), ui.Gray(what))
	for i, h := range hits {
		if i >= 8 {
			fmt.Printf("  %s\n", ui.Gray(fmt.Sprintf("… and %d more — use --out to write them all to a file", len(hits)-8)))
			break
		}
		fmt.Printf("  %s %s %s\n", ui.Gray(h.Msg.At.Format("Jan 2 15:04")),
			ui.Bold(h.Msg.Role), ui.Gray(h.Event.Project))
		fmt.Printf("    %s\n", ui.Truncate(oneLine(h.Msg.Text), 90))
	}
	fmt.Printf("\n  %s\n", ui.Gray("add --out recovered.md to write them out in full"))
	return nil
}

// writeRecovered saves the messages as markdown, which is the form a session
// can take back through @.
func writeRecovered(path string, e claude.Compaction, each func(func(claude.Dropped)), n int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Recovered from a compaction\n\n")
	fmt.Fprintf(&b, "%d messages that %s discarded on %s in %s.\n",
		n, e.Trigger, e.At.Format("2 Jan 2006 15:04"), e.Project)
	fmt.Fprintf(&b, "They were never deleted from the transcript — only from the model's context.\n\n---\n\n")
	each(func(d claude.Dropped) {
		fmt.Fprintf(&b, "### %s · %s\n\n", d.Role, d.At.Format("15:04:05"))
		if d.Text != "" {
			fmt.Fprintf(&b, "%s\n\n", d.Text)
		}
		if len(d.Tools) > 0 {
			fmt.Fprintf(&b, "*tools: %s*\n\n", strings.Join(d.Tools, ", "))
		}
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s %d messages, %s\n", ui.Green("wrote"), n, path)
	fmt.Printf("  %s\n", ui.Gray("pull it back into a session with @"+filepath.Base(path)))
	return nil
}

// shortID trims a session identifier to something that still identifies it in
// a table but does not take the whole width.
func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

const recallUsage = `pitwall recall — bring back what a compaction threw away

When Claude Code summarises a conversation to fit, the detail is gone from the
model and there is no way to ask what it was. It never left the disk: the
boundary record lists exactly which messages survived, so everything before it
that is not on that list is recoverable by subtraction.

Usage:
  pitwall recall                 what has been compacted away, and when
  pitwall recall WORD            search the discarded messages
  pitwall recall --mine          only the prompts you typed
  pitwall recall WORD --out F    write the matches to a file

Flags:
      --out FILE       write the recovered text as markdown
      --session ID     only this session
      --project NAME   only this project
      --n N            how many events to list
      --json           machine-readable output

The file is the point: pull it back into any session with @file.md. No hook,
nothing to install, and nothing that can fail silently.
`
