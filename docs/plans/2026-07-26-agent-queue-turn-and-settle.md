# Plan: the agent queue — turn ownership and settle

## Why / Alignment

Implements the first two big rocks of [the agent queue vision](../vision/agent-queue.md):
*the queue itself* and *settle*, plus the standing order they have to be rendered
into.

**A turn opens on a state and closes only when the user settles it.** No state
transition ever removes a row. An agent you prompt goes back to work still on
your list, because it went back to work at your instruction — whether that
discharged what you owed it is a judgement only the user can make. This is the
decision the whole design hangs from, and it means settle is not a convenience on
top of the queue: it is the queue's only exit, so the two cannot ship apart.

**In this chunk.** Whose turn it is becomes daemon-owned and lands on every
broadcast session. The sidebar gains a queue arrangement behind a setting: the
chief anchored at the top, a *Your turn* band of flat agent rows below it, and
today's workspace tree unchanged under that. Settle is one keystroke on any
agent. The long-run review flag and the dead `internal/attention` aggregator come
out.

**Not in this chunk.** Snooze, the move-on shortcut, dragging an agent between the
queue and a pinned workspace, the born-pinned toggle in the ⌘T flow, pinned
workspaces as their own band, a designed empty state, and default-on.

## The shape

Queue mode is **the vision's standing order**: chief, then the turns you owe,
then pinned workspaces, then the settled rest, then muted. An agent appears in
exactly one place. The daemon decides who owes a turn and stamps it on every
session it broadcasts; the app renders and sorts.

```text
Sidebar, queue mode on               Sidebar, queue mode off (unchanged)
  chief (anchored, never queues)       workspace group
  ── Your turn ──                        session row
    api-refactor  working    12m         session row
    docs-pass     waiting     4m       workspace group
    flaky-tests   idle        1m         ...
  ── Settled ──                        ── Muted ──
    exo-main      idle                   workspace group
    md-focus      working
  ── Pinned ──
    workspace group
      session row
  ── Muted ──
    workspace group
```

A queued row shows its live state, because being in the queue no longer implies
being stopped. `api-refactor` above is running: you steered it and have not yet
said you are done with it. So does a settled row: settling says you are finished
with an agent, not that it stopped.

*Your turn* and *Settled* are flat — one agent is one row, never a workspace and
never a group with a count. Pinned and muted workspaces keep the tree's grouped
rendering, because those are places you go and get work rather than a list handed
to you.

Slices 1 and 2 shipped a narrower shape — a band on top of the untouched
workspace tree — which drew every queued agent twice, gave settled agents no band
of their own, and made a row look like it moved when only one of its two copies
did. Slice 3 is what makes the sidebar the standing order. See Decisions.

## Data model

### Two stamps, and the rule between them

```sql
-- migration 81 (confirmed: 80 is MAX(version) across every local profile DB)
ALTER TABLE sessions ADD COLUMN turn_opened_at  TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN turn_settled_at TEXT NOT NULL DEFAULT '';
```

A session owes a turn iff `turn_opened_at > turn_settled_at`. Opening is
conditional and settling is not:

```sql
-- OpenTurnIfClosed(id, now): stamped from applyState after a state commit.
-- The WHERE clause is the whole state machine: a turn already open is left
-- alone, so its position in the queue never moves while you work with it.
UPDATE sessions SET turn_opened_at = ?
 WHERE id = ? AND (turn_opened_at = '' OR turn_opened_at <= turn_settled_at)

-- SettleTurn(id, now)
UPDATE sessions SET turn_settled_at = ? WHERE id = ?
```

Walked through:

| event | opened | settled | in queue |
|---|---|---|---|
| spawns, `launching` | — | — | no |
| boots to prompt, `waiting_input` | T1 | — | **yes** |
| you prompt it, `working` | T1 | — | **yes** — it is still yours |
| it stops, `waiting_input` | T1 | — | **yes**, same position |
| you settle | T1 | T2 | no |
| it finishes later, `idle` | T3 | T2 | **yes**, at the bottom |

Settling an agent that is still running is the ordinary move, not an edge case:
it means *I am done with this for now*, and it comes back the next time it wants
you. That is what keeps an empty queue reachable while eight agents are running.

### The predicate — `internal/attention`, rebuilt down to it

```go
// OpensTurn is the state vocabulary: which states start a turn you owe.
// waiting_input, pending_approval, unknown (+ idle from slice 2).
// Never: launching, working, scheduled, recoverable.
func OpensTurn(state protocol.SessionState) bool

// Owed decides membership. It does not consult state at all — state matters
// only at the moment a turn opens.
type Input struct {
    OpenedAt        time.Time
    SettledAt       time.Time
    IsShell         bool
    ChiefOfStaff    bool
    WorkspacePinned bool
    WorkspaceMuted  bool
}

func Owed(in Input) bool {
    if in.IsShell || in.ChiefOfStaff || in.WorkspacePinned || in.WorkspaceMuted {
        return false
    }
    return in.OpenedAt.After(in.SettledAt)
}
```

`recoverable` never opens a turn: the daemon revives it unattended.

**Shells are excluded by agent, and it is load-bearing.** A shell pane is a real
store session, registered `idle` at birth (`internal/daemon/ws_pty.go:218-220`),
where it stays forever. Without the exclusion, slice 2 would put every ⌘\`
terminal in the queue permanently with nothing to settle them. The exclusion goes
in from slice 1 even though it only bites in slice 2, so the slice-2 flip really
is one line.

**The exclusions filter at read, not at open.** A pinned or muted agent still
accumulates `turn_opened_at`; it is simply not shown. Unpinning surfaces whatever
was outstanding, at its true age, rather than starting it from nothing — the
queue does not quietly forget what happened while you were not looking.

### On the wire

```tsp
model Session {
  // ...
  turn_owed?: boolean;       // derived at broadcast; never stored
  turn_opened_at?: string;   // ISO; the queue's sort key
}

model SettleTurnMessage {
  cmd: "settle_turn";
  session_id: string;
}
```

The queue sorts by `turn_opened_at` ascending, not by `state_since`: a turn's age
is how long you have owed it, and it must not move when the agent changes state
under you. `needs_review_after_long_run` is removed from `Session`.

## The chief

The chief never queues, and it gets its anchored slot in slice 1 rather than in a
later rock. Excluding it without the slot would make a blocked chief a turn that
never enters the queue — the one failure the vision calls unrecoverable — so the
two halves have to land together.

In queue mode the chief renders as a single row pinned above the *Your turn*
band, carrying its own state indicator, so a blocked chief says so where it
stands instead of being promoted into competition with the work it dispatched.
It is still in the tree below as well, like every other promoted row. ⌘J follows
the queue and so never lands on the chief; that is acceptable precisely because
its slot is always on screen.

## The mode, and moving between the two arrangements

`SettingQueueModeEnabled = "queue_mode_enabled"`
(`internal/daemon/ws_settings.go`), default `false`, read and written through the
existing `get_settings` / `set_setting` / `settings_updated` path. It lives in
the daemon because the vision makes the arrangement in effect policy rather than
a rendering preference, and because later rocks (snooze, move-on) make it change
what a turn is. In this chunk only rendering consumes it.

**Turn stamps are kept and `turn_owed` is broadcast whether the mode is on or
off.** The mode gates the band, not the daemon. That is what makes both
transitions free, and it means a hub renders a remote agent's turn correctly
regardless of what the remote daemon's own setting says.

### The UI

A switch in the sidebar's display popover (`Sidebar.tsx`,
`.sidebar-settings-popover`, `data-testid="toggle-queue-mode"`), labelled **Agent
queue** and first in the popover — it is the only choice there that changes what
the sidebar contains rather than how the tree below is drawn. It shipped as a
`settings-block` in Settings → General and moved here afterwards: the queue is a
sidebar arrangement, and every other control that changes how the sidebar looks
was already one click away in that popover.

Unlike its neighbours the setting is daemon-owned, so `App.tsx` reads it with
`isQueueModeEnabled` and hands `Sidebar` the resolved boolean plus the writer;
the sidebar never sees the key.

The toggle is also a ⌘K action (`actionMenuItems` in `App.tsx`), titled by the
state it moves to and calling the same writer, so the two entries can never
disagree.

The settle shortcut is registered only while the mode is on: with the band gone
the keystroke has nothing visible to do, and an invisible verb that silently
stamps state is worse than no verb. The daemon accepts `settle_turn` either way —
it is just a stamp.

### Turning it on

Nothing is pre-settled. The migration backfills `turn_opened_at` from
`state_since` for sessions currently in a turn-opening state, so the first queue
is the honest outstanding board with sensible ages. If that is twenty rows, that
is the honest state of the board.

The tempting alternative — stamp `turn_settled_at = now` on everything at flip
time so the queue starts empty and fills as agents stop — is rejected. It hides
live turns at the exact moment the user is least equipped to notice they are
missing, which is the one failure mode the vision says is unrecoverable. It is
also not a first-run problem worth special-casing: a full queue on open is what
every morning looks like, and settle is one keystroke per row.

### Turning it off

A pure rendering revert. The band and the chief slot disappear; the tree below
was never modified, so there is nothing to restore, re-sort, or re-home. Stamps
keep accruing, so flipping back on lands on a true queue rather than one that
starts from the moment you flipped — no resync, no recompute, no migration in
either direction.

Selection survives both flips: it is addressed by session id and the tree is
unchanged, so the agent you were in stays the agent you are in, focus included.
⌘1–9 and ⌘↑/⌘↓ keep addressing the workspace tree in both modes.

## Architecture

```text
Daemon — target

  applyState  (session_state.go — the only persisted-state door)
    -> commit
    -> if attention.OpensTurn(newState): store.OpenTurnIfClosed(id, now)   [new]
    -> broadcastSessionStateChanged(id)

  ws settle_turn
    -> store.SettleTurn(id, now)
      -> statetrace entry {state, state_reason, "settled"}   [free detection labels]
      -> broadcastSessionStateChanged(id)

  sessionForBroadcastWithChiefOfStaff   (daemon.go:3044)
    decorateSessionWithWorkspace / ...WorkspaceMute   (existing)
    decorateSessionWithTurn                            [new]
      -> attention.Owed(...) -> clone.TurnOwed, clone.TurnOpenedAt
    (delete the NeedsReviewAfterLongRun branch)

Frontend — target

  App.tsx
    sessions (with turn_owed + turn_opened_at) + workspaces
      -> buildWorkspaceViewModels(...)          (unchanged, still the whole tree)
      -> buildQueueBands(workspaces, sessions)  [new, pure, unit-tested]
           -> { chief, turns[], tree[], muted[] }
      -> <Sidebar queue={bands} />              (renders bands when queue mode on)
    'session.settle' shortcut -> sendSettleTurn(activeSessionId)
```

`buildQueueBands` sorts `turns` by `turn_opened_at` ascending and computes nothing
about who owes a turn — it reads `turn_owed`. Clicking a turn row calls the
existing `handleSelectSession`, which already bumps `utilityFocusRequestToken`,
so "handed an agent means handed the keyboard" needs no new machinery.

## Boundaries

- `internal/attention` owns `OpensTurn` and `Owed` and nothing else. Pure; no
  store or daemon imports.
- `internal/daemon` owns when to stamp, the settle write, the decoration, and the
  broadcast. It must not re-implement either predicate inline.
- `internal/store` owns the two columns and the conditional open. The condition
  lives in SQL so opening is atomic rather than read-modify-write.
- The app renders and orders. It must not derive turn ownership from state, and
  must not filter the workspace tree.
- Remote sessions arrive with `turn_owed` already computed by their own daemon
  (`internal/hub/manager.go`); the hub passes it through and does not recompute.

## What this removes

The long-run review flag, whole:

- `needs_review_after_long_run` from the schema, `Session`, and
  `sessionForBroadcast`; the hub's equality check (`internal/hub/manager.go:1332`).
- `classifyOrDeferAfterStop`'s deferral branch, `handleSessionVisualized`, the
  `session_visualized` command, and the app's 5s dwell timer
  (`app/src/App.tsx:1595-1634`, `useDaemonSocket.ts:1204,5246`).
- `longRun` tracking: `markRunStartedIfNeeded`, `clearLongRunTracking` and its
  nine call sites, `longRunReviewThreshold`.
- `sessionstate.IdleStale`, `Policy.IdleStaleAfter`, `defaultIdleStaleAfter` and
  their tests. The epic left `IdleStale` with no caller on purpose; this is the
  caller arriving and saying no.

The dead attention aggregator:

- `attention.Source`, `Item`, `FromSource`, `Aggregator`, `Result`,
  `SessionAdapter`, `PRAdapter`, `WorkflowRunAdapter`, `aggregator_test.go`.
- `daemon.aggregateAttention` (`internal/daemon/attention.go`) and
  `recomputeWorkflowAttention` (`internal/daemon/workflow_broadcast.go:78-90`),
  whose only production effect is a log line — nothing broadcasts the result.

Its one true idea — that attention has an age and the oldest goes first — is what
`turn_opened_at` becomes. The old package read that age off `state_since`, which
is exactly the mistake this design does not repeat: a turn's age has to survive
the agent changing state.

## Implementation steps

Two slices, each usable end to end the day it lands, each crossing store, daemon,
protocol, and sidebar. Slice 1 is large because settle is the queue's only exit
and a queue nothing can leave is not a shippable half — that is the honest atom,
not a failure to slice finely.

Each slice names what is **deliberately still wrong** when it ships, so living
with it does not get mistaken for a design failure and quietly patched in the
wrong layer.

### Slice 1 — the queue, and settle

*Puts into use:* both halves of the core bet at once, because they are one
mechanism — a flat cross-workspace list ordered by how long you have owed it, and
a list that only your own keystroke shortens.

*Deliberately still wrong:* an agent that reaches `idle` with no turn open —
because you settled it while it was working, or because it was spawned straight
into `working` with an initial prompt — finishes without asking for you. Agents
you prompted from the queue are unaffected: their turn was already open and stays
open through the run. That gap is slice 2.

- [x] Rebuild `internal/attention` to `OpensTurn`/`Owed`/`Input`. Table tests:
      the state vocabulary; the stamp comparison; every exclusion, including a
      shell sitting in `idle` — the case slice 2 turns live.
- [x] Delete the aggregator, its adapters, `daemon.aggregateAttention`, and
      `recomputeWorkflowAttention`.
- [x] Migration 81: both columns, plus the `turn_opened_at` backfill from
      `state_since` for sessions currently in a turn-opening state.
- [x] `store.OpenTurnIfClosed` and `store.SettleTurn`, both branches (SQLite and
      in-memory). Store tests: opening twice does not move the stamp; settling
      then re-opening does.
- [x] Stamp from `applyState` after a successful commit.
- [x] Protocol: `Session.turn_owed`, `Session.turn_opened_at`, `settle_turn`;
      regenerate; bump `ProtocolVersion` (constants.go **and**
      `useDaemonSocket.ts`).
- [x] `decorateSessionWithTurn`, unconditional on the mode; `settle_turn` handler
      → `SettleTurn` → statetrace entry → broadcast.
- [x] `SettingQueueModeEnabled`, defaulting off.
- [x] Daemon tests: a prompted agent stays owed across `working`; settle removes
      it; a later turn-opening state re-adds it at a new age; shell, pinned,
      muted, and chief sessions are never owed.
- [x] Carry `turn_owed` and `turn_opened_at` into the app's enriched session
      model. The frontend has never read `state_since` — zero consumers today —
      so do not assume timestamps survive into the local model.
- [x] `buildQueueBands` + unit tests: oldest `turn_opened_at` first; a new
      arrival lands at the bottom; a row whose state changes does not move; a row
      leaving moves only the rows below it; the workspace tree the builder
      returns is identical to queue-mode-off.
- [x] Sidebar: the anchored chief row, the *Your turn* band with live state
      indicators, agent label + workspace title, above the unchanged tree.
- [x] `session.settle` shortcut (registry + cheatsheet + `Menu::default`
      accelerator check), registered only while the mode is on, and a row
      affordance.
- [x] Point every session-attention surface at `turn_owed` while the mode is on,
      leaving each on `isAttentionSessionState` while it is off. There are four,
      not one: ⌘J (`handleJumpToWaiting`, `App.tsx:2890`), the collapsed-rail
      per-workspace dot (`Sidebar.tsx:785`), the grid tile's attention glow
      (`App.tsx:2820` → `GridCompositor`), and the attention drawer's session
      section plus its badge count (`waitingLocalSessions`, `App.tsx:2884`). The
      drawer keeps its PR sections either way — PRs are out of scope for the
      queue and are the drawer's reason to exist.
- [x] Settings block in General, plus the ⌘K action, plus a test that flipping it
      on and back off leaves the tree, the selected session, and terminal focus
      untouched.
- [x] Live verification: full `make install PROFILE=<throwaway>`. Agents across
      two workspaces; band ordered oldest-first; clicking a row lands in the agent
      with the keyboard already in its terminal; **prompting an agent leaves it in
      place, in the same position, with its indicator turning green**; settle
      removes it; restart the daemon and it stays gone; let it finish and it
      returns at the bottom; a blocked chief shows in its own slot and never in
      the band; toggling the mode off and on mid-session returns the same queue
      with the same agent still focused.
      Automated as `real-app:scenario-agent-queue`, which drives two live Claude
      agents through all of it and asserts against the rendered band via a new
      `queue_get_state` bridge verb.

### Slice 2 — a finished run opens a turn

*Puts into use:* the vision's largest consequence — nothing that ever ran leaves
your plate by itself. It is last on purpose: by then settle is muscle memory, and
if it still feels heavy the flip is one predicate entry to revert.

- [x] Add `idle` to `OpensTurn`. A session launched and never spoken to reaches
      the same state and queues too (see Decisions).
- [x] Delete the long-run deferral, `handleSessionVisualized`, the
      `session_visualized` command, the app's 5s dwell timer, the `longRun`
      tracking and its call sites, and `longRunReviewThreshold`.
- [x] Delete `sessionstate.IdleStale`, `Policy.IdleStaleAfter`,
      `defaultIdleStaleAfter`, and their tests.
- [x] Protocol: drop `needs_review_after_long_run` and `session_visualized`;
      regenerate; bump. Drop the hub's equality check for the flag.
- [x] Confirm a 5m+ run publishes its verdict immediately rather than on view.
- [x] Live verification: an agent settled while working reappears when it
      finishes; a shell pane never appears; a day's worth of finished agents is a
      list you can actually drain.
      Folded into `real-app:scenario-agent-queue` as two steps: a settled agent
      whose run ends `idle` returns at the bottom, and a split shell pane —
      registered `idle`, the same state — changes nothing in the band. The first
      step also asserts an agent queues from the moment it boots to its prompt.

### Slice 3 — the standing order

*Puts into use:* the vision's sidebar, rather than a band bolted onto the old
one. Slices 1 and 2 rendered queue mode as *Your turn* over the untouched
workspace tree, so a queued agent was drawn twice, settled agents had no band,
and a row appeared to move when only one of its copies did. This makes an agent
appear exactly once, in the band that says what it is.

Nothing here is daemon work: `turn_owed` and the exclusions already say
everything the app needs. It is entirely a rendering and reachability change.

```text
buildQueueBands(workspaces) -> { chief, turns, settled }
  turns    = turnOwed, oldest turnOpenedAt first          (flat, as today)
  settled  = the rest, from workspaces that are neither pinned nor muted
             (flat; excludes the chief, which has its own slot)
  pinned + muted workspaces are not in the bands at all — the tree renders them
```

- [x] `buildQueueBands` gains `settled`, ordered by workspace then label so it
      does not shuffle as states change. Pinned and muted workspaces contribute
      to neither band.
- [x] `QueueBands` renders a *Settled* header and its rows. A settled row shows
      live state and has no settle button; it is already settled.
- [x] `Sidebar` renders only pinned workspaces as groups while queue mode is on.
      The muted section is unchanged — it is already its own collapsible section
      below, not part of the tree.
- [x] Reachability, because the group header is the only place `onPinWorkspace`
      and `onMuteWorkspace` exist today and pinning is the vision's way *out* of
      the queue: a right-click menu on any flat row pins or mutes its workspace,
      plus a ⌘K command for both. Without this, turning the queue on is a
      one-way door.
- [x] Tile-only workspaces have no agent and so no row. They are already hidden
      unless pinned or `showSessionless` is on; when it is on, they render in the
      pinned-style tree so the toggle keeps meaning something.
- [x] Live verification: extend `real-app:scenario-agent-queue` — a queued agent
      appears exactly once (in the band, not in the tree), settling moves it to
      *Settled* rather than into a workspace group, a pinned workspace keeps its
      group and its agent stays out of both bands, and pin from the row menu
      moves an agent out of the queue.

## Decisions

- **A turn closes only when the user settles it.** Victor's call, 2026-07-26. No
  state transition removes a row, so prompting an agent leaves it on your list
  until you say you are finished with it. The rejected alternative — treating
  `working` as self-settling — is cheaper and reads fine on paper, but it decides
  on the user's behalf that sending a message discharged what was owed, and
  sometimes it plainly did not. It also made the queue empty itself under the
  cursor at the moment of steering.
- **Two stamps, and only opening is conditional.** `turn_opened_at >
  turn_settled_at` is the whole membership rule. Opening is guarded by a SQL
  `WHERE` so a turn already open keeps its original age — a row must not move
  while you work with it — and so re-reported states cannot disturb it. That
  guard also removes any dependence on `state_since` being truthful, which it
  currently is not.
- **The queue sorts by turn age, not state age.** `state_since` moves whenever
  the agent does; the queue's clock must not, or steering an agent would reshuffle
  the list around it.
- **Settle ships with the queue, in one slice.** With settle as the only exit, a
  queue without it is a list nothing can ever leave. There is no honest way to cut
  slice 1 smaller; cutting it anyway would produce a first slice no one could use
  for a day, which defeats the point of slicing vertically at all.
- **The chief's anchored slot is pulled forward out of the standing-order rock.**
  It never queues, so it needs somewhere to be blocked in view, and that has to
  arrive in the same slice as the exclusion.
- **`turn_owed` is derived at broadcast, never stored.** It depends on two stamps
  plus four exclusions; storing it would create an invalidation graph over six
  inputs. Deriving it inside the existing decoration seam means every path that
  already broadcasts a session is correct for free.
- **The long-run flag is deleted in both arrangements, not made conditional.** It
  renders nothing in the sidebar today; its only effect is holding a finished
  run's classification back until someone looks at the session. Removing it
  publishes true colors sooner in the scan arrangement too.
- **The queue is additive to the workspace tree, not a filter over it.** A
  promoted agent appears in both the band and its workspace group. The
  duplication is the point: the tree stays complete and stable, which is the only
  defence against an agent that needs you and never enters the queue.
- **Shell, chief, pinned, and muted are excluded daemon-side, at read.** They are
  policy about queue membership, not rendering preferences; a second client must
  see the same queue; and filtering at read rather than suppressing the stamp
  means un-pinning surfaces what was outstanding instead of losing it.
- **Each arrangement has one notion of what wants you, and the mode selects it.**
  `isAttentionSessionState` has four live consumers today (⌘J, the collapsed-rail
  dot, the grid tile glow, the attention drawer), and `Dashboard.tsx:77` runs a
  fifth, narrower rule of its own — so the codebase already carries competing
  notions, and the queue must not add a sixth. In queue mode all four follow
  `turn_owed`; with the mode off they keep following the predicate. The epic's
  standing
  constraint was that phase 4 must not introduce a second competing notion of
  "needs attention", and this honours it: the two never disagree inside one
  window, because only one arrangement is in effect at a time. Collapsing
  everything onto `turn_owed` would instead drop pinned agents out of the badge in
  the arrangement where pinning means kept in view.
- **`turn_settled_at` stays off the wire.** Only `turn_owed` and
  `turn_opened_at` are broadcast; the daemon reads both stamps through
  `store.TurnStamps` in the decoration seam. Publishing the settle stamp would
  hand a client everything it needs to recompute membership itself, and any
  client that did would get a different answer — it cannot see the shell, chief,
  pinned, or muted exclusions.
- **A session sitting at its prompt opens a turn like any other.** Victor's call,
  2026-07-27, reversing the guard slice 2 shipped with. `sessionstate` resolves a
  never-run session at its prompt to `idle` with `ReasonAtPrompt`,
  indistinguishable at the state level from a finished run, and slice 2 carried
  that distinction through the state door on `sessionStateChange.atPrompt` so the
  launch would not queue — reading the queue as results waiting to be read. The
  queue is turns you owe, and an agent you launched and have not spoken to is the
  purest one there is: nothing will ever happen in it until you type. The guard
  is gone, so `idle` opening a turn needs no exception. Its removal also retires a
  sharper edge: the guard read `everTookATurn`, which is in-memory evidence scoped
  to the daemon's lifetime rather than the session's, so after a daemon restart
  every quiet session resolved `at_prompt` and a run finishing inside the restart
  gap could be swallowed.
- **Enabling the mode settles nothing.** A flip that pre-settled the board would
  start the queue empty and fill it as agents stop, which reads better on the
  first screen and hides live turns at the moment the user is least able to
  notice. The queue is a live picture of the present or it is not trustworthy.

## Open questions

- **The settle keybinding.** Settled on `session.settle` = ⌘⇧E. ⌘E was the first
  choice and is wrong: plain ⌘E toggles inline code in the Notebook editor, and
  the shortcut editor's chord tests record it as an exclusive leader. ⌘⇧J is the
  alternative, pairing with ⌘J (jump to waiting) as a queue family.
- **What a promoted row looks like in the tree below.** Dimmed, marked, or
  untouched. Untouched is the safe default and is what slice 1 does; living with
  it is what decides.
- **A newly created agent enters the queue while you are typing into it.** ⌘T
  spawns an agent, it boots to its prompt, and it is a turn you owe — correctly,
  since it wants your first message. It stays there until you settle it like
  anything else. Expected, not a bug; named so it does not get "fixed" mid-slice
  with a special case for the focused agent, which "looking is never acting"
  forbids.
- **Ordering across endpoints.** Remote sessions stamp on their own daemon's
  clock. Skew is small in practice and the queue is not a ledger, so this plan
  ignores it; revisit if a remote agent visibly lands in the wrong place.
- **What the settings block is called.** "Agent queue" is a placeholder: the
  vision leaves the feature unnamed and says so explicitly. The band headers
  (*Your turn* / *Settled*) are settled vocabulary; the name of the thing as a
  whole is not.

## Follow-ups

- `sessionsMatch` in `internal/hub/manager.go` gained both turn fields. Without
  them a remote agent settled on its own daemon changed nothing the hub compared,
  so the hub would suppress the re-broadcast and the row would stay in the queue.

- `Dashboard.tsx:77` filters on `state === 'waiting_input'` alone, so its "waiting"
  list silently omits `pending_approval` and `unknown`. Out of scope here, but it
  is the same bug class the queue exists to end.
- `store.UpdateState` restamps `state_since` on same-state writes
  (`internal/store/store.go:471-496`), so the column does not mean what its name
  says. The queue no longer depends on it, but the state trace and
  `attn state explain` read it. Worth a guard on its own merits.
- Settle events are labelled detection failures when the settled state was one we
  could not explain (`state_reason` "stuck"/unknown). This chunk writes them to
  the state trace; nothing reads them yet.
- Settle-and-move-on as one keystroke, once the move-on rock exists. With settle
  as the only exit, the two verbs are almost always pressed together.
- ⌘1–9 and ⌘↑/⌘↓ still address the workspace tree in queue mode. Whether they
  should address the bands instead is a question for the standing-order rock.

### Simplification opportunities found while implementing

- [x] `classifySessionState` and `runClassifier` moved out of `daemon.go` into
      `classify_decision.go`, whose own header already described the first as the
      IO shell around the rules in that file. Pure code move; `daemon.go` is 3650
      lines rather than 3807.
- Deleting the deferral collapsed `classifyOrDeferAfterStop` entirely: both
  callers (`handleStop`, the transcript watcher) now call `classifySessionState`
  directly, so there is nothing further to narrow there.
- ~~`sessionStateChange.atPrompt` is a second, narrower channel for something the
  resolver already knows.~~ Moot: the field is gone with the guard it carried.
- `stateEffectProfile` does *not* collapse to a single flag, despite looking like
  it should: `touch` is what separates `resolverObservation` (a re-reading of
  evidence, which is not itself proof the session is alive) from every other
  cause. Recorded so the next reader does not try.
