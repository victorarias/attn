# The worktree sweep

attn reclaims worktrees whose work has landed, in the background, and shows all of
it. This is the reasoning and the measurements behind the numbers in the code;
the code carries only the number and a pointer here.

Everything below was measured on 2026-09-04 against two real repositories,
147 worktrees in total (`attn`: 141, `mrpebbles`: 6).

## Two halves that never mix

A **refresh** runs git and writes observed state onto registry rows. A **sweep**
reads those rows and decides. They are deliberately separate:

- git never runs on a request path. `attn worktree list` and the Worktrees panel
  read the registry, so a slow or network-bound repository can never make the
  surface wait.
- every decision is explainable from the row the surface is showing. If the panel
  says "kept: 3 uncommitted files", that is the same field the gate read.

Every git call in a refresh is a tracked `GitOperation`, so the app can show a
per-row "refreshing…" while a repository is slow.

## What counts as merged

The ladder, first hit wins, recorded on the row as the merged signal:

1. `pull_request` — a merged pull request for the branch. Read from the
   repository-scoped record the refresh writes, plus any merged PR a live session
   opened. This is the only rung that catches a squash merge whose commits never
   appear on the integration branch.
2. `ancestor` — the branch tip is an ancestor of the integration branch.
3. `tree` — the branch's exact tree hash already appears somewhere on the
   integration branch's history. This is how a rebase or squash merge reads once
   the pull request record has aged out, and it is content-identical by
   definition, not a heuristic.

There is deliberately **no patch-id probe**. It found 9 extra worktrees in the
spike and writes a loose object per branch into the user's repository to do it.

### The integration branch

Resolved from where a repository's merged pull requests actually targeted (the
modal base ref), not from `origin/HEAD`, and re-resolved every 24 hours.

Receipt: 118 of 152 merged pull requests in `attn` targeted `next` while
`origin/HEAD` says `main`. Ancestry against `origin/main` found 0 of 31 merged
worktree branches; against `origin/next` it found them.

## The gates, in order

The first gate that holds names the kept reason, and the row carries that text so
a person or an agent can act on it.

| Gate | Kept reason |
| --- | --- |
| git lists it but the directory is gone | `kept_stale` — prune it |
| keep pin | `pinned` |
| a live session is in it | `kept_live_session` |
| an open seed points at it | `kept_open_seed` |
| never refreshed, or the last refresh failed | `unknown` |
| uncommitted or untracked files | `kept_dirty` |
| stash entries on its branch | `kept_dirty` |
| detached HEAD that is not an ancestor | `kept_detached` |
| no merged signal | `kept_unmerged` |
| commits the merge does not account for | `kept_unpushed` |
| merged and clean, but not idle long enough | `scheduled`, with the date |
| everything passed | removed |

Detached HEAD is its own gate because 22 of the 23 detached worktrees in the
sample held commits on no branch at all: deleting one destroys work with nothing
to recover it from.

"Commits the merge does not account for" is subtler than "unpushed". A naive
unpushed gate blocks exactly the squash-merge case the pull request record exists
to catch, so an `ancestor` or `tree` signal accounts for everything, and a
`pull_request` signal accounts for the branch up to the head SHA GitHub recorded
at merge. Only commits past that count.

## Idle

Idle is `max(newest tree mtime excluding .git and build directories, last commit
date, last session activity)`. The tree mtime is what makes idle honest for a
worktree attn never ran a session in.

**N = 14 days.** Receipt: idle days of the worktrees that pass every other gate
top out at 7 for anything being worked with, then one at 10, then nothing until
19. 14 sits inside that gap, so the youngest worktree the sweep reclaims is 18.9
days idle. Generosity is nearly free here: N=7 reclaims 39 worktrees and N=14
reclaims 38 — one worktree of difference. A worktree you are actually using that
ever reaches 14 days idle means this number is wrong and gets remeasured.

Override with `ATTN_WORKTREE_SWEEP_IDLE_DAYS` while testing.

## Cost

A full pass is about 12 s of git for 147 worktrees, 3 s of which is network. One
merged-pull-request API call per repository, measured at 899 ms. The cron runs
hourly, which is far off any request path and far under the window it watches.

Override with `ATTN_WORKTREE_SWEEP_INTERVAL`.

## Reversal and inspection

- **Keep pin**: `attn worktree keep <path>` / `unkeep`, or the button on the row.
  It outranks every gate and survives refreshes.
- **Sweep log**: the durable record of every removal and its reason. The row is
  gone by then, so this is the only place a removal can be inspected afterwards.
  `attn worktree log`, or the panel.
- **Seed notes**: every removal lands as a note on each seed whose last execution
  ran in that worktree, naming the path, the branch and the reason.
- **Delete**: `attn worktree delete <path>` and the row's own two-step delete
  destroy exactly what the sweep would, so they leave the same trail — a log
  entry reading `deleted … at your request`, and the same notes on the same
  seeds. The only difference is who decided. On a stale row — git still lists it
  but the directory is gone — delete is the prune: `git worktree remove` on a
  missing directory drops the record and nothing else.
- **Switch**: on by default. `worktree_sweep_enabled=false` (Settings › Files and
  locations › Worktree sweep) stops removals; refreshes continue, so the surface
  stays accurate and an eligible row says it is eligible.

## Verifying it before trusting it

`internal/daemon/worktree_sweep_receipt_test.go` runs the shipped gates read-only
against real repositories and prints what the sweep would do:

```
ATTN_SWEEP_RECEIPT_REPOS=/path/a,/path/b go test ./internal/daemon \
  -run TestWorktreeSweepReceipt -v
```

It never prunes, never writes an object, and never opens a database. Run it
before changing a gate, and check the diff in the verdicts rather than trusting
the reasoning.

The receipt taken before this shipped: 29 candidates across two repositories, 29
confirmed merged — 26 by byte-identical tree or ancestry on the integration
branch, 3 by a pull request whose recorded merge head matched the worktree's own
HEAD exactly.
