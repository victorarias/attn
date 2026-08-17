# Superdraft: agent ledger, agent close, timed worktree sweep

> Motivation: a stalled delegate session sat as a husk in `pending_approval`
> with nothing left to do, and only a human could close it. Before agents get
> close authority, closes must be auditable and mistakes recoverable. Ordering
> is deliberate: ledger → close authority → sweep; each is the safety
> net for the next.

## The ledger

- Mostly a provenance + durability problem, not a new-data problem: the daemon
  already holds id, agent, member, workspace, state. Missing:
  - **Origin**: manually launched vs delegated; who delegated (session id, or
    the role when the chief-of-staff seat did).
  - **Bindings**: ticket id, brief, workspace, worktree path + repo.
  - **Lifecycle timestamps**: created, first turn, last activity, closed.
  - **Close attribution**: closed_by (human via app / agent session id),
    and the worktree decision made at close.
  - **Branch head at close** — recorded so any later sweep is provably
    recoverable (branch pushed? head hash?).
- Append-only; records **survive closure**. Today a closed session vanishes
  from `agent list`, which is what makes "closed by mistake"
  unrecoverable-by-inspection.

## Agent close authority

- New verb, e.g. `attn agent close <id>`, allowed from agent sessions.
- Close is **soft**: session marked closed in the ledger, transcript kept,
  worktree marked for sweeping (next section). No data destroyed at close
  time.
- **Decided — closing someone else's agent needs `--confirm`.** Any session
  may close any agent, but when the target wasn't created by the caller the
  verb refuses without `--confirm` and says why:
  "are you sure? this agent wasn't created by you. This is not necessarily a
  problem, but you should be aware of this fact." The ledger's origin field
  is what makes this check possible.
- Closing a session in `working` state also requires `--confirm` (it may be
  mid-turn; the ledger records the interruption).
- Refuse-or-flag when the worktree has uncommitted or unpushed work; that is
  the actual data-loss case, the timer only helps if someone notices.
- **Decided — reopen means re-launch.** Closed is a state, not an erasure:
  reopening launches a fresh delegate resuming from the preserved worktree
  and branch (`attn delegate --ticket <id>` on the recorded checkout). The
  old transcript stays closed; the ledger links the old and new sessions.

## The sweep

The one vocabulary word: a worktree is **marked for sweeping**, the daemon's
**sweeper** deletes it when the window passes and the guards clear. Sweeping
touches worktrees only — the main repo folder is never a candidate, ever.

- **Human close (app):** the prompt becomes keep / delete / **sweep**.
  Delete stays immediate and final — Victor is the trust boundary. Sweep
  hands it to the same machinery agents use.
- **Agent close: never immediate.** The worktree is marked for sweeping:
  **3 days, configurable** (config key, daemon-owned settings). 3d is a
  proposal sized to "noticed after a night's sleep or a weekend", not a
  measurement — remeasure if a real mistake ever races it.
- **Preserve, then delete.** Classifying untracked files (authored work vs
  regenerable noise) is a lost game: every classifier is a manual list or a
  heuristic that eventually eats someone's work, and a strict clean guard
  silently never sweeps in a normal repo — dependency stores and build junk
  are the usual state, not a hygiene failure. So the sweeper doesn't
  classify. Before deleting, it **bags** what git can't recover — a tar of
  the untracked and modified files — into the ledger's keep beside the
  session record, then deletes. Recovery is untar + checkout. The guard
  question becomes "did we preserve everything?" (mechanical), not "is it
  safe to delete?" (unanswerable).
  - **Where**: `$ATTN_DATA_DIR/sweepings/<session-id>/` — daemon-owned,
    beside the ledger it belongs to, one dir per swept worktree holding
    `untracked.tar.gz` (+ `branch.bundle` for GC'd orphans) and a
    manifest naming every file and the recorded head.
  - **Found via the ledger, never by digging**: the ledger entry records
    the bag path; `attn agent show <id>` prints it (and `--json` for
    agents); `attn agent list --all` shows `swept (bag kept)` in the
    WORKTREE column. Restore is a documented two-liner in the manifest
    itself: re-worktree the branch, untar over it. When the branch is
    gone (squash-merge prunes it — the spike's common case), worktree
    the recorded head hash instead, or main where the head was merged.
  - **For how long: forever, by default.** Bags are KB-scale by receipt —
    a safety net that deletes itself isn't one. Cleanup is a human verb
    (`attn sweep clear [--older-than 30d]`), and `agent list --all`
    shows the sweepings total so growth is visible, never silent.
  - **Bag cap: 50MB, a tripwire that aborts.** Receipt below: the largest
    untracked item observed across 35 real worktrees is 68KB, ~700×
    headroom — nothing healthy touches this. If any entry exceeds it, the
    sweeper does NOT silently drop it and does NOT delete the worktree:
    that sweep aborts with **one notification per worktree**, listing
    every offending path with its size (hypothetical, e.g.
    ".pnpm-store/ 1.8GB, target/ 900MB — over the 50MB bag cap"; the
    spike observed no such case), never one per entry. Resolution is
    one human-or-agent decision: remove the junk, or consent to not
    preserve it (`attn agent sweep <id> --skip <path>`, repeatable), and
    the sweep proceeds.
    If a legitimate case ever hits the cap, the cap is wrong — remeasure.
  - Bags are small by construction; v1 ships no automatic bag retention.
- **Blocking guards — any one aborts the sweep:**
  1. **Unpushed commits**: verified **at mark time**, recorded in the
     ledger (head + remote witness). The spike shows why not at sweep time:
     after squash-merge + branch prune, ancestry can't prove merged work
     was ever pushed.
  2. **Ledger**: no session — live or created after the mark — bound to
     that path.
  3. **Changed since mark**: new or modified files after the mark abort
     (mtime + status vs the mark's snapshot). What was dirty *at* mark was
     shown to the closer — informed consent — and is bagged anyway; this
     guard exists for the reuse hazard, not file safety.
- An aborted sweep is loud: ledger note + notification saying which guard
  tripped, never silent. Marked worktrees and their countdowns are visible
  (`attn agent list --all`) and cancelable.

## Spike findings (2026-08-17, 35 live worktrees on this machine)

- **"Pushed" is not re-checkable later.** 28/35 worktrees have lost their
  upstream (branch deleted after squash-merge); their merged commits are
  unreachable from `origin/main` by ancestry. Verify-and-record at mark
  time is the only reliable shape; only 6/35 heads are provably on main.
- **Dirty state is rare and real.** 6/35 worktrees have untracked files,
  ~180KB total, largest item 68KB. Four carry authored work (an entire
  uncommitted feature, a harness scenario, plan docs, prototypes) — the
  bag saves actual content, not junk. The two "dependency stores" are
  phantoms on pnpm's layout: an 8KB index db and 0B of symlinks.
- One worktree sits on a detached HEAD, one tracks `origin/main` directly
  with real uncommitted work — both shapes the sweeper must handle
  (record the hash; guards refuse the dirty one until bagged).

## Worktree GC (same sweeper, wider net)

- The sweeper generalizes: orphan worktrees — `attn--*` siblings whose
  sessions are long closed, or that no ledger entry claims — get marked and
  **swept for real**. `git worktree list` is already the inventory;
  a propose-only GC would just create human work, which defeats the point.
- Deletion authority comes from bag + guards, not from provenance. Orphans
  predating the ledger have no mark-time pushed record and ancestry can't
  supply one (spike above) — for those, the bag also carries a
  `git bundle` of the branch, so deletion is recoverable even when
  "pushed" is unprovable. Notification names what went and its recorded
  head.
- Same window, same config key, own slice after the sweeper exists.

## Surface: `attn agent list` (decided)

`attn list` today is the JSON/app surface; the human table is `attn agent
list`. **Decided: one verb, two lenses** — A's front door, B's body:

    $ attn agent list          # live: who is here now, + ORIGIN column
    ID        NAME           AGENT   ORIGIN             STATE     TURN
    019f8de5  Keel           claude  crew               working   -

    $ attn agent list --all    # ledger lens: closed included, history columns
    ID        NAME           AGENT   ORIGIN             STATE               WORKTREE
    52f3e866  reconcile-s3b  codex   delegated (Keel)   closed 08-17 11:32  kept
    a1b2c3d4  oc-fix         claude  delegated (chief)  closed 08-15 09:04  sweeping in 2d 4h

Full per-session record via `attn agent show <id>`. One store underneath;
the flag picks the lens. History filters (`--since`, `--closed-by`,
`--origin`) hang off `--all` and never clutter the live view.

## Non-goals

- No per-agent permission system beyond the `--confirm` rule above.
- No transcript retention policy changes; close keeps what exists today.
- The main repo folder is out of scope for any deletion path, always.
