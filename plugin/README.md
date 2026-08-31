# pitwall plugin for Claude Code

Wires [pitwall](https://github.com/sur1cat/pitwall) into Claude Code.

## What it adds

- **`/pitwall`** — one command that reports what the fleet is doing, what it
  costs, and what it left behind.
- **A `SessionStart` hook** — when a session opens in a git repository that has
  no `CLAUDE.md`, pitwall injects a compact summary of what earlier sessions
  already learned there: the commands that get run, the directories that get
  touched, the files that keep being opened.

  On the machine this was built for, the first three prompts of a session cost
  **$7.84 each and made 53 tool calls** in repositories with no primer, against
  **$1.55 and 18 tool calls** in ones that had something to start from. The hook
  exists to close that gap without asking you to write documentation first.

  It stays silent when a `CLAUDE.md` already exists, when the repository has
  fewer than two recorded sessions, and whenever anything goes wrong.

## Install

The plugin drives the `pitwall` binary, so install that first:

```sh
go install github.com/sur1cat/pitwall@latest
# or: curl -fsSL https://raw.githubusercontent.com/sur1cat/pitwall/main/install.sh | sh
```

Then:

```sh
claude plugin marketplace add sur1cat/pitwall
claude plugin install pitwall
```

For the status line — plugins cannot provide one, so it goes in your own
settings:

```sh
pitwall install
```

## Removing it

```sh
claude plugin uninstall pitwall
pitwall install --remove
```
