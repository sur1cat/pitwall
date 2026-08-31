# Subsystem design notes

pitwall began as three separate tools — `ctree`, `cfleet` and `cburn` — which
were folded into it and then developed further. Their design notes are kept
here verbatim, because they record why each subsystem works the way it does,
and that reasoning outlived the binaries.

Where these notes and the code disagree, the code is right: `internal/worktree`,
`internal/fleet` and `internal/burn` have all moved on since. What has not
changed is the reasoning about *what to measure* and *what not to claim*.

---

# worktree — from ctree


## Why this exists

Parallel agents are now the normal way to use Claude Code: `--worktree` is
built in, and tools like dmux, FleetCode and Crystal all help you *launch*
more of them. Nothing helps you afterwards.

The measurements that motivated `ctree` came from one developer's machine
after a month of parallel-agent work (git worktree state at the time of the
survey; Claude Code prunes transcripts after roughly a month, so anything
derived from them describes that window):

- 30 worktrees across 3 repositories, 2.7 GB
- 84 local branches
- **30 of 30** worktrees had zero unique commits — all fully merged, all dead
- one of those "safe to delete" worktrees held 13 uncommitted files, two weeks
  old, including a new database migration
- and earlier, a real production incident: two parallel worktrees had burned
  the same migration sequence numbers, leaving the deployed schema ahead of
  every branch in the repository

Every existing cleanup tool would have deleted that thirteenth file, and none
of them would have caught the migration clash.

## The three design decisions

**1. Classification before action.** The tool's job is not "delete merged
worktrees" — it is to answer "what is this, and what would I lose". Every
command shares the same six-state classifier, so `status`, `gc` and `salvage`
can never disagree about what is safe.

**2. Unknown means keep.** Any worktree whose lineage cannot be established
(no base branch, detached HEAD, missing counts) is classified `AHEAD`, the
"has unique work" state. Cleanup tools fail dangerously when they guess
optimistically; this one guesses pessimistically.

**3. Salvage is not optional.** Rescue is on by default and gates removal: if
the commit, the ref or the patch write fails, the worktree stays. `--force` is
the only way to lose work, and it has to be typed.

## The Claude join

Claude Code writes a `worktree-state` record into the session transcript when a
session enters a worktree:

```json
{"type":"worktree-state","worktreeSession":{
  "worktreePath":"/repo/.claude/worktrees/e5-e7",
  "worktreeName":"e5-e7",
  "worktreeBranch":"worktree-e5-e7",
  "originalBranch":"main",
  "originalHeadCommit":"0ff78cb…",
  "sessionId":"beb4983e-…"}}
```

and maintains a registry of running processes in `~/.claude/sessions/*.json`
(pid, cwd, `busy`/`idle`, derived name). Joining the two gives the ownership
graph git has no way to know:

- worktree → creating session → is that process still alive?
- live session cwd → which worktree is it *currently* inside (deepest match,
  so a nested `.claude/worktrees/x` wins over the repository root)?

`ACTIVE` is the union of both: a session working inside the worktree now, or an
owner session still running.

## Performance

The transcript corpus grows without bound — 2.1 GB and ~2000 files on the
machine above. Three things keep the scan fast:

- a byte-level `bytes.Contains` prefilter before any JSON parsing, so 99.9% of
  lines are rejected without allocation;
- a `cwd` extractor that slices the raw line instead of unmarshalling records
  that can each be megabytes;
- a per-file cache keyed on size and mtime, so only changed transcripts are
  re-read.

Cold: ~5s. Warm: ~1.7s. Both are dominated by git, not by the scan.

## Deliberate non-goals

- **Not a launcher.** Creating worktrees is solved; `ctree` never creates one.
- **Not a dashboard.** Session monitoring is a crowded space (cctop,
  claude-dashboard, ClaudeTUI). `ctree sessions` exists only to explain who is
  holding a worktree.
- **Not automatic.** No daemon, no background deletion. Cleanup is destructive,
  so it stays an explicit command with a dry run.

## Open questions

- Should `AHEAD` worktrees older than N days be surfaced as "probably
  abandoned"? Age alone is weak evidence; a stale long-lived branch is normal.
- Migration detection is currently heuristic (numeric prefix under a
  `migrations/ | migrate/ | versions/` path). A per-framework mode (Alembic,
  golang-migrate, Rails, Django) could be exact.
- Worth detecting version-bump collisions (`package.json`, `go.mod`) the same
  way as migrations?

---

# fleet — from cfleet


## The measurement that motivated this

From one month of one developer's retained Claude Code transcripts (2,905
measured turns, 2026-07-30 to 2026-08-31 — Claude Code prunes transcripts after
roughly a month, so this is the whole window, not a sample of it):

- 494 hours of wall-clock time spent waiting on agent turns
- 42% of turns took longer than 5 minutes; 362 took longer than 20
- 48% of all agent-busy time had **two or more turns running simultaneously**
- **430 hours** of "the agent had finished, the human had not come back yet" —
  25% of those gaps were longer than 15 minutes
- 28.7 hours of agents sitting blocked on an unanswered question; the worst
  single case waited 22 hours

Push notifications already existed and were enabled. The gap they leave is
plurality: a notification tells you *something* happened, not *which of your
four agents* is blocked, on what, and whether the other three are fine.

## Scope discipline

The session-monitoring space is crowded — cctop, claude-dashboard,
claude-code-monitor, ClaudeTUI all render sessions, and several also chart
token spend. Competing on surface area is a losing game, so `cfleet` competes
on a single question and on being scriptable:

- one screen, sorted by whether it blocks you
- `wait` as a blocking primitive, with meaningful exit codes (0 hit, 2 timeout,
  130 interrupt), so it composes into shell pipelines rather than replacing them
- `--exec` instead of a built-in notifier, so the notification channel is the
  user's choice and not a dependency
- no token accounting at all — that is `cburn`'s job

## Deriving WAITING

Claude Code's registry reports `busy` or `idle`. Neither says "blocked on a
human", which is the state that matters most. `cfleet` derives it from the
transcript:

> a `tool_use` block with no matching `tool_result`, while the session is not
> busy, means the agent is parked on a person

That covers both shapes of blocking with one rule: an `AskUserQuestion` (whose
question text is then surfaced) and a tool sitting on a permission prompt.
Anything answered clears itself, because the matching `tool_result` deletes the
pending entry.

`DONE` is the complement: idle, with the transcript ending on the agent's side,
meaning the human has not typed since. After 12 hours it decays to `IDLE` —
technically still unanswered, but no longer something to interrupt you about.

## Reading the tail, not the file

Transcripts grow without bound (some in the sample corpus exceed 60 MB), and a
watch loop re-reads them every few seconds. `cfleet` seeks to the last 1 MB,
discards the partial first line, and parses forward. That window always spans
the current turn, and the cost of a refresh is constant regardless of how long
a session has been running.

## Non-goals

- **No history or analytics.** A snapshot of now, not a time series.
- **No control.** It never sends input to a session; you switch to the terminal
  yourself.
- **No notification transport.** No Telegram bot, no SMTP, no daemon — just
  `--exec` and exit codes.

---

# burn — from cburn


## Why another usage tool

Token accounting for Claude Code is a crowded space (ccusage, ccflare, the
Claude Code Usage Monitor). They answer "how many tokens and how much money".
`cburn` exists because that is not the question a heavy user actually asks.

From nine months of one developer's prompt history (`history.jsonl` is retained
far longer than transcripts), the meta-commands they typed most were not about
code at all:

| Command | Times typed |
|---|---:|
| `/effort` | 264 |
| `/usage` | 173 |
| `/rate-limit-options` | 22 |

459 prompts — **5.8% of everything typed in nine months** — spent managing how
hard the model thinks and how much budget is left. `/usage` was re-checked
within 30 minutes in 42 of 172 cases.

The `/effort` number is the interesting one. Nobody re-picks a setting 264
times because they enjoy it; they do it because they have no idea what it
costs. So the headline feature is not a token counter, it is the answer to
"what is each effort level actually worth" — measured over the retained
transcript window, about a month:

```
by effort
  max      $18,196.58  82.6%  $0.18 / message
  high     $3,018.27   13.7%  $0.22 / message
  xhigh    $801.98      3.6%  $0.11 / message
```

## Correctness decisions

**Cache multipliers, not input rates.** With 98% of tokens coming from cache
reads, pricing cache at the input rate would overstate spend by roughly 10×.
Claude Code records `cache_creation.ephemeral_5m_input_tokens` and
`ephemeral_1h_input_tokens` separately, so the 1.25× and 2× write premiums are
applied exactly rather than averaged.

**Deduplication that survives caching.** Session forks and resumes replay
earlier assistant messages into new transcripts. Counting them twice inflates
totals silently. The approach:

1. every transcript is summarised into hourly buckets *and* a base64 blob of
   64-bit hashes of the API response ids it counted;
2. at merge time, files are processed oldest-first and their id sets are tested
   against everything already counted;
3. only files with an actual overlap are re-parsed, skipping the duplicates.

Cost: a 2.2 MB cache file. Benefit: correct totals with a 0.07-second warm run.

**Hourly buckets.** Fine enough to derive a trailing five-hour window and a
burn rate; coarse enough that the cache stays small no matter how long a
session ran.

**Dated pricing.** Promotional launch rates expire. A record is priced at the
rate that applied on its own timestamp, so historical totals stay stable
instead of silently re-pricing when a promotion ends.

## Honesty constraints

Two things this tool deliberately refuses to do:

- **It does not claim to know your plan limits.** Anthropic does not publish
  subscription limits in a machine-readable form and they change. The meter
  only appears when you declare a budget yourself with `--limit`.
- **It does not present API rates as a bill.** On a subscription these numbers
  are the value you consumed, not an invoice, and every screen says so. A tool
  that shows a subscriber "$22,016" without that caveat is lying.

## Non-goals

- No proxy, no interception, no wrapper around the CLI — read-only, after the
  fact, zero risk to a running session.
- No network access of any kind.
- No fleet monitoring or session control; that is `cfleet`.

---

