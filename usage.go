package main

const fleetUsage = `pitwall fleet — which of your agents needs you right now

Usage:
  pitwall fleet [status]   one-shot view of every session
  pitwall fleet watch      live view, refreshed on an interval
  pitwall fleet wait       block until an agent needs you, then exit 0
  pitwall fleet recap      what each agent did while you were away

Flags:
      --json / --all / --no-git / --no-color
watch:  -n, --interval D    --exec CMD   (env: PITWALL_NAME, PITWALL_STATE, PITWALL_CWD, PITWALL_QUESTION)
wait:   --for waiting|done|any   --timeout D   -n, --interval D
`

const burnUsage = `pitwall burn — what your usage costs, and which knob spends it

Usage:
  pitwall burn [summary]   spend by window, model and effort level
  pitwall burn top         heaviest project | session | model | effort | day
  pitwall burn watch       the same summary on an interval
  pitwall burn export      raw hourly records as CSV or JSON
  pitwall burn models      the pricing table in use

Flags:
      --since 30d  --project NAME  --json  --no-cache  --no-color  --limit USD
top:  --by DIM   --n N
export: --format csv|json
`

const treeUsage = `pitwall tree — the git worktrees your agents left behind

Usage:
  pitwall tree [status]    inventory every worktree, with its owning session
  pitwall tree gc          salvage stranded work, then remove dead worktrees
  pitwall tree salvage     commit and archive stranded work, remove nothing
  pitwall tree collisions  branches whose changes will collide on merge
  pitwall tree prep [DIR]  copy the local config a fresh worktree is missing

Flags:
  -p, --path DIR  --json  --no-size  --no-cache  --no-color
gc:   --dry-run  -y, --yes  --no-salvage  --force  --prune-branches  --include-ahead
`

const coachUsage = `pitwall coach — how you actually spend, and what would change it

Usage:
  pitwall coach            findings ranked by what they cost you
  pitwall coach --project NAME   narrow to one repository

Flags:
      --since SPAN  only analyse the last span (30d, 2w, 12h)
      --projects   list spend and priming per repository
      --json       machine-readable findings
      --no-color
`

const primerUsage = `pitwall primer — draft a CLAUDE.md from what past sessions learned

Usage:
  pitwall primer [PATH]        print a draft for the repository at PATH (default: cwd)
  pitwall primer PATH --write  write it to PATH/CLAUDE.md (refuses to overwrite)
  pitwall primer --all         every repository with no primer, ranked by cost
  pitwall primer --all --write draft one in each of them

The draft is observed behaviour — the commands agents actually run, the files
they keep opening, where the code lives. It is a starting point to edit, not
documentation.
`
