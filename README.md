# pitwall

**The instrument panel for a fleet of coding agents.**

When you run four or five Claude Code agents at once, the hard part stops being
the code. It becomes: which one is blocked on me, what is this costing, what did
they leave behind, and which of my habits is burning the budget.

`pitwall` answers all four from data already on your disk. One binary, no
dependencies, nothing leaves the machine.

Two things it shows that other tools do not:

- **How full each agent's context window is.** This is the resource that runs
  out first and least visibly. Money is recoverable and quota resets on a
  schedule; a conversation forced into a compaction loses what it knew, and
  nothing warns you before it happens.
- **What your subagents cost.** They live in a `subagents/` directory that
  usage trackers skip, and on the machine this was built on they are 23% of
  the spend across nearly as many messages as the main conversation.

```
$ pitwall

pitwall  Mon 12:05

  agents  1 WAITING · 2 DONE · 1 WORKING
          ▲ WAITING mds-0e     asks: Drop the legacy index before backfill?
          ✔ DONE    fleety-c8  Shipped to prod. 63 drivers imported, tests…
  spend   $254.74 today   $254.74 in the last 5h   burn $50.95/h
  git     44 worktrees   30 removable (2.5 GB)   1 holding unsaved work

pitwall fleet · burn · tree · coach
```

## The part that is actually new

Everything above is telemetry. `pitwall coach` is the opinion:

```
$ pitwall coach

pitwall coach  $17,015.77 across 3373 prompts · 2026-07-30 – 2026-08-31

  what a prompt bought  prompts  spend      share
  execute               889      $8,763.63  52%   wrote or changed code
  investigate           1883     $8,094.75  48%   read, searched or ran things — changed nothing
  talk                  601      $157.39     1%   no tool calls at all

1. "max" carries your spend but "xhigh" delivers more per dollar
   $3,125.98 · 18% of spend · association, not a controlled test
     max      $14111   2320 prompts  $2.42 per code change
     high      $2603    598 prompts  $3.07 per code change
     xhigh      $301     83 prompts  $1.13 per code change
     full gap would be $7532; scaled to $3126 because "xhigh" has only 83 prompts behind it
     → run a week at xhigh and compare — pitwall records the answer either way

2. Sessions restart from zero in repositories with no primer
   $3,121.83 · 18% of spend · association, not a controlled test
     the first 3 prompts of a session are 23% of all spend
     without a primer: $7.84 per opening prompt, 53 tool calls each
     with one:        $1.55 per opening prompt, 18 tool calls each
     → pitwall primer fleety   — drafts a CLAUDE.md from what past sessions discovered

3. Answers that end in a question, followed by a one-line reply
   $2,365.85 · 14% of spend
     171 round trips; each one pays for a full turn to ask and another to resume
     → state the decision up front: which files, what done looks like, what not to touch
```

Findings are ranked by what they cost, and anything that is a correlation
rather than a controlled measurement says so on its own line.

## Requirements

| | |
|---|---|
| Claude Code | Any recent version. pitwall reads what it writes to `~/.claude`, so there must be some history there to read. |
| OS | macOS, Linux or Windows. The menu bar app is macOS only. |
| `git` | Only for `pitwall tree`. Everything else works without it. |
| Go 1.23+ | Only if you build from source. |
| Xcode command line tools | Only for the menu bar app. |

Nothing else. The binary is statically linked and carries no dependencies.

## Install

### Homebrew (macOS and Linux)

```sh
brew install sur1cat/tap/pitwall
```

`sur1cat/tap` is this project's own tap rather than homebrew-core, so the
prefix is part of the command. One `brew tap sur1cat/tap` up front lets you drop
it afterwards.

### Go

```sh
go install github.com/sur1cat/pitwall@latest
```

Installs into `$(go env GOPATH)/bin`, which is `~/go/bin` by default. Add it to
your `PATH` if it is not there already.

### Script

```sh
curl -fsSL https://raw.githubusercontent.com/sur1cat/pitwall/main/install.sh | sh
```

Downloads the release build for your platform into `~/.local/bin` and tells you
if that is not on your `PATH`. Set `PREFIX` to put it elsewhere:

```sh
curl -fsSL .../install.sh | PREFIX=/usr/local/bin sh
```

### By hand

Take the archive for your platform from
[releases](https://github.com/sur1cat/pitwall/releases), unpack it, and move
`pitwall` onto your `PATH`. This is the route for Windows, where the archive is
a `.zip`.

### From source

```sh
git clone https://github.com/sur1cat/pitwall
cd pitwall
make build            # ./bin/pitwall
make install          # into $GOBIN, or ~/go/bin
```

### Check it worked

```sh
pitwall version
pitwall doctor         # what pitwall can and cannot read here
```

`doctor` walks the whole chain pitwall depends on — the config directory, the
transcripts, the prompt history, the settings files, its own cache, git, the
plan credential — and names the first link that is broken. An empty screen and
a broken one look identical, and this is how to tell them apart.

## First run

In this order:

```sh
pitwall                # what is happening right now
pitwall burn           # what it has been costing, by model and effort level
pitwall perms          # which of your permission rules can never match
pitwall tree           # what your agents left behind on disk
pitwall coach          # which of your habits costs the most
```

`coach` reads every transcript on the first run, which takes a few seconds on a
large corpus; after that it is served from a cache and is instant. Everything
above only reads. The two commands that change anything — `pitwall perms fix`
and `pitwall tree gc` — say so, are a dry run by default, and back up whatever
they touch.

## Wire it into Claude Code

Three optional pieces, each independent of the others.

**A status line**, so every session shows what it is spending:

```sh
pitwall install          # --print to see the change first, --remove to undo
```

This adds a `statusLine` entry to `~/.claude/settings.json` and backs up the
previous file next to it.

**The plugin**, which adds a `/pitwall` command and a hook that offers a primer
when you open an unprimed repository:

```sh
claude plugin marketplace add sur1cat/pitwall
claude plugin install pitwall
```

**The menu bar app**, so none of this needs a terminal open:

```sh
brew install sur1cat/tap/pitwall-bar
pitwall-bar
```

or from a checkout:

```sh
make bar && open bar/build/PitwallBar.app
```

The app is compiled on the machine that installs it rather than shipped as a
notarised bundle. That is deliberate: an app downloaded from the internet
carries a quarantine attribute that an ad-hoc signature will not clear, while
one built locally starts without argument. The cost is that Xcode's command line
tools are needed.

To have it come back after a reboot, add `PitwallBar.app` to
**System Settings → General → Login Items**.

## In the menu bar

Six tabs behind the click — **Status** (who needs you, today's spend, plan left,
worktrees, with a button that cleans them up), **Coach** (the findings, rendered
natively), **Rules** (the permission rules that can never match, and a button
that removes them), **Projects** (per-project primer and effort level),
**Prompts**, and **Setup** (what the menu bar shows, notifications, language).
Clicking an agent brings its terminal tab to the front. Notifications fire once
when an agent asks a question or finishes, and never while it is working.
Details in [bar/README.md](bar/README.md).

## Update and uninstall

```sh
brew upgrade pitwall                     # Homebrew
go install github.com/sur1cat/pitwall@latest   # Go

brew uninstall pitwall pitwall-bar
rm ~/.local/bin/pitwall                  # if installed by the script
pitwall install --remove                 # take the status line back out
rm -rf ~/.claude/pitwall                 # pitwall's cache and quota readings
```

pitwall keeps nothing else. Removing `~/.claude/pitwall` costs you the scan
cache and the quota history behind the pace measurement; nothing else changes.

## If something looks wrong

**An empty screen, or an analysis that finds nothing** — run `pitwall doctor`.
It reports every file pitwall needs and which of them is missing. The usual
answer is that Claude Code has written nothing to `~/.claude/projects` yet, or
that `CLAUDE_CONFIG_DIR` points somewhere else.

**Numbers cover less time than you expect** — Claude Code deletes transcripts
after `cleanupPeriodDays`, 30 by default. `pitwall burn` says so once the
archive has reached that boundary. Raise it in `~/.claude/settings.json`.

**`pitwall quota` says it cannot read your plan** — it needs the OAuth token
Claude Code stored. On macOS that is in the Keychain and the system asks for
permission once. `CLAUDE_CODE_OAUTH_TOKEN` also works.

**The menu bar app will not open** — if it was downloaded rather than built
locally, macOS quarantines it. Build it from source, which is what the formula
does.

**A worktree command finds nothing** — `pitwall tree` needs `git` on the `PATH`.

## Surfaces

| Command | What it answers |
|---|---|
| `pitwall` | Everything at a glance, in under two seconds |
| `pitwall fleet` | Which agent is waiting, working, or done — and `wait` blocks until one needs you |
| `pitwall burn` | What usage costs, by model, effort level, project, session and day |
| `pitwall perms` | Which permission rules can never match, and `perms fix` removes them |
| `pitwall burn top --by branch` | What a feature cost, which no other tool answers |
| `pitwall recall` | Bring back what a compaction threw away |
| `pitwall doctor` | What pitwall can and cannot read here, and why a screen is empty |

The context reading is derived from each session's newest message — input plus
both kinds of cache, which is what occupies the window — against the model's
published size. A model whose window pitwall does not know gets no reading
rather than a guessed one: a bar that is wrong is worse than one that is
absent, because a wrong one gets acted on. In a status line the figure comes
from Claude Code itself, which hands it over already computed.
| `pitwall tree` | Which git worktrees your agents left behind, and which hold unsaved work |
| `pitwall quota` | How much of your plan is left, from Anthropic, with a projection |
| `pitwall coach` | Where your spend actually goes and what would change it |
| `pitwall primer` | A draft `CLAUDE.md` built from what past sessions learned about a repo |
| `pitwall focus` | Brings an agent's terminal tab to the front |
| `pitwall lint` | Checks a prompt against the shapes that needed fewest follow-ups |
| `pitwall tree prep` | Copies the local config a fresh worktree is missing |

Each has `--help`. Everything takes `--json`.

### fleet — who needs you

`WAITING` is derived rather than reported: a tool call with no result while the
agent is not running means it is parked on a human — a question, or a permission
prompt. `pitwall fleet wait` turns that into a shell primitive:

```sh
pitwall fleet wait && say "an agent needs you"
pitwall fleet watch --exec 'terminal-notifier -message "$PITWALL_NAME: $PITWALL_STATE"'
```

### quota — how much is actually left

The most common complaint about Claude Code is hitting a limit with no warning.
Claude Code itself knows the answer: it calls a usage endpoint that returns the
utilisation of both windows. `pitwall` calls the same one, with the credential
Claude Code already stored.

```
$ pitwall quota

  window   used               resets in  at this pace
  5 hours  ▮▮▮▯▯▯▯▯▯▯▯▯  33%  1h03m      lasts the window
  rolling  ▮▮▮▮▮▮▮▯▯▯▯▯  65%  70h23m     runs out in 52m  (pace of the last 1h37m)
```

The projection extrapolates the pace used so far inside the window, and says
what it extrapolated from — a burst right after a reset produces a short
projection, and you should be able to see that rather than be alarmed by it. It
stays silent when the window would reset before the pace ran it out.

The rolling window is labelled weekly by Anthropic but is observed to reset
every 72 hours. That is not documented, so pitwall says so instead of implying
otherwise.

`pitwall effort --guard` uses this number: when either window passes a
threshold, it lowers the effort level new sessions start at, and restores it
when the pressure drops.

### burn — what it costs, priced properly

Cache writes are not input tokens: a 5-minute-TTL write costs 1.25× the input
rate, a 1-hour write 2×, a cache read 0.1×. Claude Code records the TTL split
per request, and `pitwall` uses it — which matters when 98% of your tokens are
cache reads. Messages replayed by session forks are counted once. Models with
promotional launch pricing are billed at the rate that applied on the day.

Three numbers appear here that nothing else reports. **What a session cost
before anyone typed** — the system prompt, tool definitions, skills, plugins,
MCP servers and CLAUDE.md all arrive in the first request and are paid for,
and the counter you see starts afterwards; the median here is 34.1K tokens,
which matches an independent community measurement of ~34,433. **What a
compaction cost** in tokens dropped and time waited. And **what a branch cost**,
via `burn top --by branch`, which is the closest thing on disk to "what did
this feature cost".

Subagents are counted and shown separately. Their transcripts live in a
`subagents/` directory, they carry `isSidechain`, and on this machine they are
23% of the spend across nearly as many messages as the main conversation — a
fan-out cost that most trackers skip entirely and that nobody notices until it
has its own line.

On a subscription these numbers are the value you consumed, not an invoice, and
every screen says so.

Spans are written the way people write them: `--since 30d`, `--since 2w`,
`--since 12h`, or a bare `--since 30` meaning days.

### perms — the rules that can never match

Claude Code's "Yes, and don't ask again" writes a rule into
`.claude/settings.local.json`. Over a year that accumulates, and a large share
of what accumulates cannot do anything. `pitwall perms` reads every settings
file that carries permission rules and sorts them by why they fail, following
Claude Code's documented matching behaviour rather than guesswork:

- **secret** — a credential is written into the rule text. Reported as a count
  and a file, never echoed: printing it would copy the secret into a terminal,
  a scrollback buffer and very likely another transcript.
- **fragment** — a comment line, or a piece of a multi-line block saved with
  its newlines encoded. Not a command, so it never matches one.
- **unmatchable** — the rule text contains a shell separator. Claude Code
  matches each subcommand independently, so such a rule can never match.
- **ignored** — Claude Code skips it at load: an `mcp__` rule with parentheses,
  a rule on a tool's primary content field such as `Bash(command:rm *)`, or an
  allow glob not anchored to a literal server.
- **never-consulted** — a path rule on `Write`, `NotebookEdit`, `Glob` or
  `MultiEdit`. File access is only checked against `Read` and `Edit` rules.
- **wildcard-inside** — a `*` before the end. In `Bash(git * main)` the `*`
  stands in for the subcommand, which includes `-c`, and `git -c` runs a
  program you name.
- **shadowed** and **duplicate** — a broader or identical rule already covers it.
- **one-off** — no wildcard at all, so it matches that one command forever.

It also shows which single prefix rule would replace how many one-offs, and
stops there: widening a permission is a decision for the person who owns the
repository, so pitwall prints the arithmetic rather than acting on it.

`pitwall perms fix` cleans up what cannot work. It is a dry run until you add
`--write`, it copies every file into `~/.claude/pitwall/perms-backups` before
changing it, and it never touches managed settings. Two limits are deliberate:

- **It never widens a permission.** Repairing `Write(src/**)` to `Edit(src/**)`
  would grant access that the broken rule does not grant today, so an allow
  rule is reported and a deny or ask rule is corrected. The same holds for
  `Bash(command:rm *)` and every other ignored form.
- **Working rules stay.** A literal rule such as `Bash(npm run build)` is
  clutter, not a fault; removing it means Claude Code asks again. `--drop-one-offs`
  removes them if that is what you want.

Everything else in the file survives byte for byte, in its original key order,
so the diff you review is the change and nothing else.

### recall — what the compaction threw away

When Claude Code summarises a conversation to fit, the detail is gone from the
model and there is no way to ask what it was. This is the most-repeated
complaint about the tool, and the usual phrasing is "hours of context, no
warning, no way to get it back".

The second half of that is not true. The boundary record lists exactly which
message UUIDs survived, so everything written before it that is not on that
list is recoverable by **subtraction, not inference** — it never left the disk,
only the model's context.

```
$ pitwall recall
37 compactions dropped 41.5M and waited 1h26m

  when          project   dropped  waited  session
  Aug 31 09:04  starts    983.2K   3m03s   8e0d2771
  Aug 18 05:19  fleety    3.0M     2m59s   bcb957a6

$ pitwall recall worktree
141 discarded messages mention "worktree"

$ pitwall recall --session 8e0d2771 --out recovered.md
wrote 342 messages, recovered.md
  pull it back into a session with @recovered.md
```

`--mine` narrows it to the prompts you typed, and that flag exists because of a
measurement. Across 37 compactions here, 302 records were preserved verbatim
and **three of them were prose a human had written**. Everything else kept was
the agent's own output and tool results. What a compaction reliably discards is
precisely what you asked for, which is why it feels like the agent forgot the
task rather than the details.

The file is the point. Injecting context through a hook would be cleverer and
would fail silently when it went wrong; a markdown file you pull back with `@`
works in any session, needs nothing installed, and you can read it first.

Two things worth knowing before reaching for a cleverer fix. A `PreCompact`
hook *can* stop a compaction by exiting 2 — and that is cancellation, not
postponement: it never resumes, so you trade a summarised conversation for a
hard context wall. And the automatic trigger is not a mystery: across the
automatic compactions measured here it fired at a median of 1,000,333 tokens
with 1.7% spread, essentially exactly at the window. That is why a warning at
85% gives real notice rather than a guess.

### tree — the worktrees left behind

Six states, and `gc` only removes what it can prove is safe. Anything holding
uncommitted work is salvaged first — committed to its own branch, pinned behind
`refs/pitwall/salvage/*`, and archived as a patch — before the worktree is
removed. If the rescue fails, the worktree stays.

### primer — a starting point for repos that have none

Start with the list, which ranks every unprimed repository by what its cold
starts cost above the rate your primed ones manage:

```
$ pitwall primer --all

  project      sessions  $/opening prompt  at stake
  tenderai     26        $18.48            $1,261.71
  fleety       87        $5.00               $886.27
  mds          30        $8.79               $607.25

  $3,104.29 across 10 repositories
```

Then draft one, or all of them with `--all --write`:

```sh
pitwall primer ~/src/fleety
```

reads every past session in that repository and drafts a `CLAUDE.md` from what
agents actually did: the commands that get run (`go test`, `golangci-lint run`,
`npx vitest`, `psql`), the directories that get touched, the files that keep
being opened. It is observed behaviour, not documentation — the draft leaves
blanks for the intent no transcript can tell you.

The plugin does this automatically: when a session opens in a git repository
with no `CLAUDE.md`, a `SessionStart` hook injects a compact version of the same
summary. It stays quiet when a `CLAUDE.md` exists or the repository has too
little history to be worth the tokens.

## What pitwall deliberately does not do

**It does not switch your effort level for you.** Claude Code hooks receive the
current `effort` as read-only input; there is no field in any hook's output that
can change the model or the reasoning level, and `PreModelSwitch` can only
observe a switch you already asked for. Anything claiming to auto-tune effort is
either editing your settings behind your back or not doing it at all.

What pitwall does instead is make those 264 manual `/effort` decisions
*informed*: it shows what each level has actually cost you per unit of work, so
the choice is one keystroke made on evidence rather than on feel.

It also never touches a running session, never proxies your traffic, and never
sends anything anywhere.

## Where the data comes from

```
~/.claude/sessions/*.json      live sessions: pid, cwd, busy/idle, name
~/.claude/projects/**/*.jsonl  transcripts: usage, effort, tools, prompts, worktree records
git worktree / status / rev-list
```

Everything is read-only apart from pitwall's own caches, the salvage archives
`tree gc` writes, the local configuration `tree prep` copies into a worktree,
and the files `install`, `primer --write` and `effort --apply` create.

**One command uses the network.** `pitwall quota` calls
`https://api.anthropic.com/api/oauth/usage` — the endpoint Claude Code calls
for the same purpose — with the OAuth credential Claude Code already stored
(macOS keychain, or `~/.claude/.credentials.json`, or `CLAUDE_CODE_OAUTH_TOKEN`).
macOS asks once for permission to read it. Nothing is sent anywhere else, and
no other command touches the network. Readings are cached for 180 seconds
because that endpoint rate limits hard, with exponential backoff when it does.

**Transcripts are pruned by Claude Code after about a month**, so `burn` and
`coach` describe the retained window, not all time. Both print the range they
measured, and both warn once the archive has actually reached the boundary and
is being trimmed — the default is `cleanupPeriodDays: 30`, and raising it in
`~/.claude/settings.json` is the only way to keep a longer history. Prompts in
`~/.claude/history.jsonl` are not pruned on the same schedule, so questions
outlive the answers they produced.

Scans are cached per file on size and modification time: a first pass over a
couple of gigabytes takes a few seconds, and every pass after that is instant.

## Other agent tools

pitwall reads Claude Code's format. The internals are split so that a second
adapter (Cursor, Codex, Aider, Gemini CLI) would only need to produce the same
`Session`, `Record` and `Exchange` types — but no such adapter exists yet, and
one will not be written speculatively. If you want your tool supported, open an
issue with a sample of what it writes to disk.

## License

MIT
