---
description: Show what the agent fleet is doing, what it is costing, and what it has left behind
---

Run these and summarise the result for me in a few lines. Do not paste raw
output unless something needs attention.

```
pitwall --no-color
```

If anything looks wrong — an agent waiting on a question, spend running hotter
than usual, worktrees holding unsaved work — say so first and tell me the one
command that addresses it. Otherwise a single line confirming things are fine
is enough.

Useful follow-ups, only if I ask:

- `pitwall coach --no-color` — where my spend actually goes and what would change it
- `pitwall tree gc --dry-run --no-color` — what cleanup would remove
- `pitwall primer <path>` — draft a CLAUDE.md for a repository that has none
