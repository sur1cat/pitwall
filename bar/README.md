# PitwallBar

A macOS menu bar front end for `pitwall`. It shells out to the same binary the
CLI uses, so there is one source of truth and the bar can never disagree with
`pitwall --json`.

## Build and run

```sh
./build.sh && open build/PitwallBar.app
```

Requires Xcode command line tools and macOS 14+. The build copies the `pitwall`
binary into the bundle, so the app works before `pitwall` is on your `PATH`.

Quit it from the panel — there is no dock icon (`LSUIElement`).

## The five styles

The always-visible part is switchable; the panel behind the click is the same
for all of them. Every style previews live in the panel, so you compare them
against your real numbers rather than a mockup.

| Style | Menu bar | For |
|---|---|---|
| **Signal** | one dot, amber when an agent needs you | people who want zero noise |
| **Plan left** | `rolling 65%` — the window closest to its limit | watching the quota |
| **Spend** | `$275 · 5h $275` | watching the budget |
| **Burn rate** | `$55/h ▮▮▯▯▯` | watching the rate, with a meter |
| **Fleet** | `⚠1 ↻2 ✓1` | watching the agents |
| **Everything** | `⚠2 · $275 · $55/h` | the default |

The burn meter fills against `PITWALL_LIMIT` when you set it, and against a
$100/h reference otherwise. It is a scale, never a claim about your plan's
limits — pitwall does not know them.

## Three tabs

**Status** — how much of your plan is left, in both windows, with a warning
when the current pace runs one out before it resets. Then agents grouped under
headings that say why they are on screen
(Needs you / Finished / Running), with the actual question when one is waiting.
Then today's spend and burn rate, then the worktree count with reclaimable
space and a warning when one holds unsaved work.

Clicking an agent **brings its terminal tab to the front**. It matches the
session's controlling tty against the tabs Terminal.app and iTerm2 report, so it
lands on the right one rather than opening a new window.

**Coach** — the same findings as `pitwall coach`, rendered natively: what each
class of prompt bought, then each finding with what it costs and what to do
about it. The first scan reads every transcript and takes a few seconds; after
that it is served from pitwall's cache and opens instantly.

**Rules** — the permission rules Claude Code has saved for you, and which of
them cannot work. Every "Yes, and don't ask again" writes a rule into a
settings file; over a year most of what accumulates is comment lines, pieces of
multi-line commands, and rules with a credential in the text. The tab shows
what is wrong, which files it would change and by how much, and a button that
cleans it up.

The button writes nothing until it is pressed, and pressing it asks once more
first. Every file is copied into `~/.claude/pitwall/perms-backups` before it is
touched, working rules are left alone, and no permission is ever widened — a
broken `allow` rule is reported rather than repaired, because repairing it
would grant access that is not granted today.

**Settings** — what the menu bar shows, and whether to notify.

## Notifications

One notification when an agent starts waiting on a question, and one when it
finishes a turn. Nothing while it is working, and nothing on launch — the first
snapshot only records state, so starting the app never produces a burst about
things you already knew.

## Refresh

Every 8 seconds for agents and spend, every 5 minutes for the git scan — the
worktree walk is the only slow part, and stale-by-five-minutes is fine for
something that changes when you merge.
