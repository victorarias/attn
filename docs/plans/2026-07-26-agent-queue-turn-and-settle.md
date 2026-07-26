# Plan: the agent queue — turn ownership and settle

## Why / Alignment

Implements the first two big rocks of [the agent queue vision](../vision/agent-queue.md):
*the queue itself* and *settle*, plus the static band arrangement they have to be
rendered into.

The two rocks ship together because a queue without settle cannot be drained. The
vision makes a finished run (`idle`) a turn you owe, and most turns settle
themselves — the moment an agent starts working the turn is no longer yours. An
agent you are *done with* has no such exit. Without the settle verb, every agent
that ever finished piles up in the queue permanently, and the first day of using
it is also the last.

**In this chunk.** Whose turn it is becomes daemon-owned and lands on every
broadcast session. The sidebar gains a queue arrangement behind a setting: a
*Your turn* band of flat agent rows, pinned workspaces lifted into their own
band, and today's workspace tree unchanged below. Settle is one keystroke on any
agent. The long-run review flag and the dead `internal/attention` aggregator come
out.

**Not in this chunk.** Snooze, the move-on shortcut, dragging an agent between
the queue and a pinned workspace, the born-pinned toggle in the ⌘T flow, the
chief announcing it is blocked without leaving its slot, a designed empty state,
and default-on.

## The shape

Queue mode is **today's sidebar plus a band on top**. The workspace tree below is
untouched and complete, so an agent the daemon fails to promote is still exactly
where it has always been — that is the vision's "reorders, never hides", taken
literally. The daemon decides who owes a turn and stamps it on every session it
broadcasts; the app renders and sorts. A turn is settled iff its settle stamp is
not older than the state it settles, so nothing ever has to be un-settled.

```text
Sidebar, queue mode on          Sidebar, queue mode off (unchanged)
  chief (pinned to top)           workspace group
  ── Your turn ──                   session row
    agent  · workspace title        session row
    agent  · workspace title      workspace group
    agent  · workspace title        ...
  ── Pinned ──                    ── Muted ──
    workspace group                 workspace group
      session row
  ── Settled ──
    workspace group  (the whole tree, promoted rows included)
      session row
  ── Muted ──
    workspace group
```

## Data model

### The predicate — `internal/attention`, rebuilt down to it

```go
// Input is everything that decides whether a session owes the user a turn.
type Input struct {
    State           protocol.SessionState
    StateSince      time.Time
    SettledAt       time.Time // zero => never settled
    WorkspacePinned bool
    WorkspaceMuted  bool
    ChiefOfStaff    bool
}

func Owed(in Input) bool {
    if in.WorkspacePinned || in.WorkspaceMuted || in.ChiefOfStaff {
        return false // outside the queue by explicit intent, or by standing order
    }
    if !turnState(in.State) {
        return false
    }
    return in.SettledAt.Before(in.StateSince)
}

// turnState: waiting_input, pending_approval, unknown, idle.
// Not a turn:  launching, working, scheduled, recoverable.
```

`idle` is a turn — the vision's largest deliberate consequence, and the last
thing the slices below turn on. `recoverable` is not: the daemon revives it
unattended.

### Settle — one column, no lifecycle

```sql
-- migration 81 (80 is the current max)
ALTER TABLE sessions ADD COLUMN turn_settled_at TEXT NOT NULL DEFAULT '';
```

`SettleTurn(id)` stamps `now`. Nothing ever clears it: the next turn is a new
`state_since`, which is later than the stamp, so the row re-enters the queue by
itself.

That invariant rests on `state_since` meaning *when this state began*. Today it
does not: `store.UpdateState` rewrites `state_since` on every write including a
same-state one (`internal/store/store.go:471-496`), and only caller discipline
keeps it honest — the evidence resolver drops no-ops
(`internal/daemon/session_evidence.go:543`) but the plugin-driver path
(`internal/daemon/plugin_driver.go:353`) and the PTY live-signal path
(`internal/daemon/daemon.go:1817`) do not. Under this design a same-state rewrite
silently un-settles a turn the user already dismissed. The guard moves into the
store, where the column's meaning lives.

### On the wire

```tsp
model Session {
  // ...
  turn_owed?: boolean;   // derived at broadcast; never stored
}

model SettleTurnMessage {
  cmd: "settle_turn";
  session_id: string;
}
```

No `turn_since`: it *is* `state_since`, which is already on the wire and is what
the queue sorts by, ascending. `needs_review_after_long_run` is removed from
`Session`.

## The mode, and moving between the two arrangements

`SettingQueueModeEnabled = "queue_mode_enabled"`
(`internal/daemon/ws_settings.go`), default `false`, read and written through the
existing `get_settings` / `set_setting` / `settings_updated` path. It lives in
the daemon because the vision makes the arrangement in effect policy rather than
a rendering preference, and because later rocks (snooze, move-on) make it change
what a turn is. In this chunk only rendering consumes it.

**`turn_owed` is computed and broadcast whether the mode is on or off.** The mode
gates the band, not the daemon. That is what makes both transitions free, and it
means a hub renders a remote agent's turn correctly regardless of what the remote
daemon's own setting says.

### The UI

A `settings-block` in **General**, following the `workflows_enabled` block
(`SettingsModal.tsx:1954-1982`) exactly: intro copy, a `settings-row-card`, and
an Enable/Disable `settings-action` button, `data-testid="settings-queue-toggle"`.
Heading: **Agent queue**. The copy has to say the one thing a user cannot infer —
that agents are handed to you oldest-first and leave when they no longer want
you, and that everything stays exactly where it is in the sidebar below.

The settle shortcut is registered only while the mode is on: with the band gone
the keystroke has nothing visible to do, and an invisible verb that silently
stamps state is worse than no verb. The daemon accepts `settle_turn` either way —
it is just a stamp.

### Turning it on

Nothing is backfilled and nothing is pre-settled. The queue shows the true
present: every agent currently in a turn state, oldest `state_since` first. If
that is twenty rows, that is the honest state of the board.

The tempting alternative — stamp `turn_settled_at = now` on everything at flip
time so the queue starts empty and fills as agents actually stop — is rejected.
It hides live turns at the exact moment the user is least equipped to notice
they are missing, which is the one failure mode the vision says is unrecoverable.
It is also not a first-run problem worth special-casing: once slice 3 lands, a
full queue on open is what every morning looks like, and settle is one keystroke
per row.

### Turning it off

A pure rendering revert. The band disappears and the tree below it was never
modified, so there is nothing to restore, re-sort, or re-home. `turn_settled_at`
stamps persist untouched and the daemon keeps computing `turn_owed`, so flipping
back on is instant and lands on the same queue — no resync, no recompute, no
migration in either direction.

Selection survives both flips: it is addressed by session id and the tree is
unchanged, so the agent you were in stays the agent you are in, focus included.
⌘1–9 and ⌘↑/⌘↓ keep addressing the workspace tree in both modes.

## Architecture

```text
Daemon — target

  store.UpdateState / ApplyAgentDriverState
    -> state_since moves only on a real state change   [new guard]
  ws settle_turn
    -> store.SettleTurn(id)
      -> statetrace entry {state, state_reason, "settled"}   [free detection labels]
      -> broadcastSessionStateChanged(id)

  sessionForBroadcastWithChiefOfStaff   (daemon.go:3044)
    decorateSessionWithWorkspace / ...WorkspaceMute   (existing)
    decorateSessionWithTurn                            [new]
      -> attention.Owed(...) -> clone.TurnOwed
    (delete the NeedsReviewAfterLongRun branch)

Frontend — target

  App.tsx
    sessions (with turn_owed) + workspaces
      -> buildWorkspaceViewModels(...)          (unchanged, still the whole tree)
      -> buildQueueBands(workspaces, sessions)  [new, pure, unit-tested]
           -> { chief, turns[], pinned[], settled[], muted[] }
      -> <Sidebar queue={bands} />              (renders bands when queue mode on)
    'session.settle' shortcut -> sendSettleTurn(activeSessionId)
```

`buildQueueBands` sorts `turns` by `state_since` ascending and computes nothing
about who owes a turn — it reads `turn_owed`. Clicking a turn row calls the
existing `handleSelectSession`, which already bumps `utilityFocusRequestToken`,
so "handed an agent means handed the keyboard" needs no new machinery.

## Boundaries

- `internal/attention` owns the predicate and nothing else. Pure; no store or
  daemon imports.
- `internal/daemon` owns the settle write, the decoration, and the broadcast. It
  must not re-implement the predicate inline.
- `internal/store` owns `turn_settled_at` and the `state_since` invariant.
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

The one true idea in that package survives as `Input.StateSince`: attention-since
was always `state_since`, and the old aggregator already sorted oldest-first.

## Implementation steps

Three slices, each one usable end to end the day it lands. The order is chosen so
that each slice puts exactly one unproven design decision into daily use, and so
that a slice that turns out to be wrong can be reverted or rethought without
stranding the ones before it. Every slice touches the store, the daemon, the
protocol, and the sidebar.

Each slice also names what is **deliberately still wrong** when it ships, so that
living with it does not get mistaken for a design failure and quietly patched in
the wrong layer.

### Slice 1 — a queue you can look at

Ships the whole arrangement for turns that settle themselves: an agent enters the
queue when it stops for you and leaves the moment it starts working again.

*Puts into use:* whether a flat, cross-workspace, oldest-first list is a better
thing to live in than the tree. That is the vision's own unanswered question, and
this is the cheapest possible way to ask it.

*Deliberately still wrong:* a finished run leaves the queue by itself, which is
the behaviour the vision rejects. Do not fix that here — it is slice 3, and it is
only survivable once settle exists.

- [ ] Rebuild `internal/attention` to `Input`/`Owed`, with `turnState` =
      `waiting_input`, `pending_approval`, `unknown`. `SettledAt` is in the
      struct and always zero. Table test over states × pinned/muted/chief.
- [ ] Delete the aggregator, its adapters, `daemon.aggregateAttention`, and
      `recomputeWorkflowAttention`.
- [ ] `store.UpdateState`: move `state_since` only when `state` actually
      changes; `state_updated_at` always moves. Same for the in-memory branch
      and `ApplyAgentDriverState`. The queue sorts by `state_since`, so without
      this a plugin driver re-reporting the state it is already in keeps
      resetting its own age and sinks to the bottom of the list.
- [ ] Regression test: a same-state write leaves `state_since` untouched, from
      both the resolver-shaped and plugin-report-shaped paths.
- [ ] Protocol: `Session.turn_owed`; regenerate; bump `ProtocolVersion`
      (constants.go **and** `useDaemonSocket.ts`).
- [ ] `decorateSessionWithTurn` in `sessionForBroadcastWithChiefOfStaff`,
      unconditional on the mode.
- [ ] `SettingQueueModeEnabled`, defaulting off.
- [ ] `buildQueueBands` + unit tests: oldest `state_since` first, a new arrival
      lands at the bottom, a row leaving moves only the rows below it, and the
      workspace tree the builder returns is identical to queue-mode-off.
- [ ] Sidebar: the *Your turn* band above the unchanged tree, rows carrying
      agent label + workspace title. Pinned workspaces stay where they are in the
      tree; their own band is the standing-order rock.
- [ ] Settings block in General + a test that flipping it on and back off leaves
      the tree, the selected session, and terminal focus untouched.
- [ ] Live verification: full `make install PROFILE=<throwaway>`; agents across
      two workspaces into `waiting_input`, band ordered oldest-first, clicking a
      row lands in the agent with the keyboard already in its terminal, steering
      one removes it without moving the others, pinning a workspace keeps its
      agents out of the band, and toggling the mode off and on mid-session
      returns the same queue with the same agent still focused.

### Slice 2 — settle

Ships the verb, on the states that already queue. Now a turn the queue was wrong
about can be dismissed, and the dismissal survives a daemon restart and a state
re-report.

*Puts into use:* the settle stamp itself — whether "settled iff the stamp is not
older than `state_since`" holds up against real transitions, and whether one
keystroke is genuinely enough to make being wrong cheap.

*Deliberately still wrong:* nothing new. Slice 1's gap stands.

- [ ] Migration 81 + `store.SettleTurn(id)` and `turn_settled_at` on the read
      path; `Owed` starts consulting `SettledAt`.
- [ ] Protocol: `settle_turn`; regenerate; bump.
- [ ] Handler → `SettleTurn` → statetrace entry (state + `state_reason`) →
      broadcast.
- [ ] `session.settle` shortcut (registry + cheatsheet + `Menu::default`
      accelerator check), registered only while the mode is on, and a row
      affordance.
- [ ] Tests: settle clears `turn_owed`; a following real state change restores
      it; a same-state re-report does not; settle on a not-owed session is a
      no-op.
- [ ] Live verification: settle a waiting agent, it leaves the band; restart the
      daemon, it stays gone; steer it and let it stop again, it comes back.

### Slice 3 — a finished run is a turn

Flips `idle` into the queue and retires the machinery that existed only because a
finished run was invisible.

*Puts into use:* the vision's largest consequence — nothing that ever ran leaves
your plate by itself, so the queue at the start of a day holds every agent you
did not settle the day before. This is the change most likely to feel heavy, and
it is last on purpose: by the time it lands, settle is already muscle memory, and
if it still feels wrong the slice is one predicate entry to revert.

- [ ] Add `idle` to `turnState`.
- [ ] Delete the long-run deferral, `handleSessionVisualized`, the
      `session_visualized` command, the app's 5s dwell timer, the `longRun`
      tracking and its call sites, and `longRunReviewThreshold`.
- [ ] Delete `sessionstate.IdleStale`, `Policy.IdleStaleAfter`,
      `defaultIdleStaleAfter`, and their tests.
- [ ] Protocol: drop `needs_review_after_long_run` and `session_visualized`;
      regenerate; bump. Drop the hub's equality check for the flag.
- [ ] Confirm a 5m+ run publishes its verdict immediately rather than on view.
- [ ] Live verification: a finished agent stays in the band until settled, and a
      day's worth of finished agents is a list you can actually drain.

## Decisions

- **Settle ships with the queue rather than after it.** The vision's own
  consequence — nothing that ever ran leaves your plate by itself — makes a
  settle-less queue un-drainable within one day of use. Inside the chunk the
  order is settle first, `idle`-as-a-turn second, so the exit exists before the
  thing that needs it.
- **The chunk is sliced vertically, and `idle` goes last.** Every slice crosses
  store, daemon, protocol, and sidebar, and each is usable the day it lands. The
  alternative — a store PR, a daemon PR, a protocol PR, a UI PR — is faster to
  write and cannot tell you anything until the last one merges, which is the
  worst possible time to learn the arrangement reads badly. The slice order is
  chosen so each one puts a single unproven decision into daily use: the flat
  list (1), the settle stamp (2), then the consequence most likely to feel heavy
  (3), which by then is one predicate entry to revert.
- **`turn_owed` is derived at broadcast, never stored.** It depends on session
  state, workspace pin, workspace mute, and the chief role; storing it would
  create an invalidation graph over four inputs. Deriving it inside the existing
  decoration seam means every path that already broadcasts a session is correct
  for free.
- **Settle is keyed on `state_since`, not on a boolean.** A flag would need
  clearing at every transition — one missed clear and a live turn is invisible,
  the failure the vision says the user cannot recover from. A stamp compared
  against `state_since` has no clear path at all. It buys that by depending on
  `state_since` being truthful, which the store does not currently guarantee —
  hence the guard, which slice 1 already needs for ordering.
- **The long-run flag is deleted in both arrangements, not made conditional.**
  It renders nothing in the sidebar today; its only effect is holding a finished
  run's classification back until someone looks at the session. Removing it
  publishes true colors sooner in the scan arrangement too, so there is no
  version of it worth keeping behind the toggle.
- **The queue is additive to the workspace tree, not a filter over it.** A
  promoted agent appears in both the band and its workspace group. The
  duplication is the point: the tree stays complete and stable, which is the only
  defence against an agent that needs you and never enters the queue.
- **Pinned, muted, and chief are excluded daemon-side.** They are policy about
  queue membership, not rendering preferences, and a second client must see the
  same queue.
- **Enabling the mode settles nothing.** A flip that pre-settled the board would
  start the queue empty and fill it as agents stop, which reads better on the
  first screen and hides live turns at the moment the user is least able to
  notice. The queue is a live picture of the present or it is not trustworthy.
- **The mode gates the band, not the daemon.** `turn_owed` is computed and
  broadcast either way. It costs nothing, it makes both transitions a pure
  rendering change with no state to migrate, and it is what lets a hub queue a
  remote agent whose own daemon has the mode off.

## Open questions

- **The settle keybinding.** Recommend `session.settle` = ⌘E — unbound, not a
  `Menu::default` accelerator, and easy enough to press constantly. ⌘⇧J is the
  alternative, pairing with ⌘J (jump to waiting) as a queue family, at the cost
  of a two-modifier chord for the most-pressed verb in the product.
- **What a promoted row looks like in the tree below.** Dimmed, marked, or
  untouched. Untouched is the safe default and is what slice 1 does; living with
  it is what decides.
- **Ordering across endpoints.** Remote sessions sort by their own daemon's
  clock. Skew is small in practice and the queue is not a ledger, so this plan
  ignores it; revisit if a remote agent visibly lands in the wrong place.

- **What the settings block is called.** "Agent queue" is a placeholder: the
  vision leaves the feature unnamed and says so explicitly. The band headers
  (*Your turn* / *Settled*) are settled vocabulary; the name of the thing as a
  whole is not.

## Follow-ups

- If flipping the mode turns out to be frequent while it is being evaluated, put
  it in the ⌘K action menu (`ActionMenu.tsx`). Settings-only is the right home
  for a setting; it is the wrong home for something toggled ten times a day.

- Settle events are labelled detection failures when the settled state was one we
  could not explain (`state_reason` "stuck"/unknown). This chunk writes them to
  the state trace; nothing reads them yet.
- ⌘1–9 and ⌘↑/⌘↓ still address the workspace tree in queue mode. The vision
  accepts that they stop addressing queue positions; whether they should address
  the bands instead is a question for the standing-order rock.
