# Design notes

## What this is trying to fix

Running agents in parallel is now easy. Running them *well* is not, and the
failure modes are invisible: money leaks in places no dashboard shows, agents
sit blocked while you look at a different terminal, and git fills with
worktrees nobody will ever open again.

pitwall exists because all three are measurable from data already on disk, and
because measuring them changes what you do next.

## What the measurements said

From one developer's retained transcript window (2026-07-30 to 2026-08-31 —
Claude Code prunes transcripts after roughly a month), 3,373 human prompts and
$17,015 of attributable spend:

| | |
|---|---|
| Spend on prompts that changed no code | **48%** |
| Spend on prompts with no tool calls at all | 1% |
| Spend in the first 3 prompts of a session | **23%** |
| Opening prompt in a repo with no primer | **$7.84, 53 tool calls** |
| Opening prompt in a repo with one | **$1.55, 18 tool calls** |
| Question-then-one-line-answer round trips | 171, **14% of spend** |
| Work paid for and then corrected | 105 prompts, 2% of spend |

Three of these overturned the hypothesis that produced them:

- **"Tokens go to clarification, not execution."** Mostly false. Pure
  conversation with zero tool calls is 1% of spend. But the *round trips* — an
  answer ending in a question, then a one-line reply — really are 14%.
- **"Long sessions get expensive as context grows."** False. Turn cost is flat
  to slightly falling with position in a session (a late prompt costs 0.8× an
  early one); caching and compaction already handle it. Session hygiene is not
  the lever.
- **"Better prompts would cost less."** Not by length, at least: cost per prompt
  is flat at $4–7 from under 30 characters to over 600, and longer prompts are
  followed by a correction slightly *more* often.

What did survive is the cold start. The first three prompts of a session are a
quarter of all spend, and in repositories with no `CLAUDE.md` they burn 53 tool
calls each re-deriving a layout that earlier sessions already worked out. That
one is a correlation, not a controlled test — primed repositories in this sample
were also smaller — but the mechanism is visible in the tool calls, and the fix
is cheap enough to just try.

## Why the findings carry a confidence label

`coach` ranks by dollars, which makes it very easy to lie by extrapolation. The
effort finding is the clearest case: `xhigh` looked 2× more efficient per code
change than `max`, but on 83 prompts against 2,320. Reporting the raw gap would
have claimed $7,532 of recoverable spend from a rounding error's worth of
evidence.

So findings that rest on association rather than measurement say so on their own
line, and the effort estimate is scaled by how much evidence stands behind the
cheaper option. The number shrinks to $3,126 and the report shows both figures
and the reason.

A tool that tells you where your money goes has to be harder on itself than the
thing it is measuring.

## Why effort is not automatic

The obvious feature is auto-tuning: detect a mechanical task, drop to `medium`;
detect a hard one, raise to `max`. It cannot be built.

Claude Code hooks receive `effort` as **read-only input**. The full set of
fields a hook may return is `permissionDecision`, `permissionDecisionReason`,
`updatedInput` (tool arguments only), `updatedPrompt` (prompt text only),
`additionalContext`, `systemMessage`, `terminalSequence` and `retry`. None of
them touches the model or the reasoning level. `PreModelSwitch` observes a
switch you already requested and can block it, but cannot start one.

Writing `effortLevel` into `settings.json` behind the user's back would change
the default for *future* sessions while looking like it changed the current one.
That is worse than not shipping the feature.

So pitwall makes the manual decision informed instead of automating it away.
`/effort` was typed 264 times in nine months of prompt history — the problem was
never the keystroke, it was choosing blind.

## Why the primer hook is the one hook

`SessionStart` + `additionalContext` is the only place where a hook can spend a
few hundred tokens to avoid tens of thousands. It fires only when a repository
has no `CLAUDE.md`, has at least two recorded sessions and 50 tool calls of
history, and produces something to say. Every failure path prints `{}` and
exits 0, because a hook that breaks a session is worse than no hook.

The content is deliberately observational — "these commands were run, these
directories were touched, these files kept being opened" — with an explicit
instruction to verify anything load-bearing. Inventing architecture claims from
tool-call frequency would be worse than silence.

## Structure

```
internal/claude    sessions, transcript tails, worktree-state records
internal/burn      pricing and hourly usage aggregation
internal/fleet     agent state derivation
internal/worktree  git inventory, classification, salvage, collisions
internal/coach     prompt-to-outcome exchanges and findings
internal/primer    project drafts from observed behaviour
```

`coach` and `primer` are the new layer; the other four started life as three
standalone tools (`ctree`, `cfleet`, `cburn`) and were merged once it was clear
they were three views of the same dataset.

The split exists partly so a second agent tool could be supported by producing
the same `Session`, `Record` and `Exchange` types. That adapter is not written,
because nothing else with local transcripts is installed on the machine this was
built for, and speculative adapters rot.

## Non-goals

- **No proxy.** pitwall never sits in the request path. Reading after the fact
  cannot break a running session.
- **No network.** Not for pricing, not for telemetry, not for updates.
- **No automation of destructive things.** `tree gc` has a dry run and a
  confirmation, and salvages before it removes.
- **No plan-limit guessing.** Anthropic does not publish subscription limits in
  machine-readable form; the spend meter only appears when you declare a budget.
