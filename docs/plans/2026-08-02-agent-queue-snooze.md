# Plan: the agent queue — snooze

## Why / Alignment

Implements the *Deferral* rock of [the agent queue vision](../vision/agent-queue.md):
snooze with real durations, waking to the tail of the queue, broken early only by
errors and states the daemon cannot explain.

**Why.** Settle answers *I dealt with this*. Nothing today answers *not now*. The
gap shows up twice a day: an agent you cannot act on until the build finishes, a
finished run you want to read after lunch, a long job you kicked off and do not
want handed back for an hour. Settling those is a lie — the turn comes straight
back — so they sit in the band and the queue stops meaning "these are the things
you can do something about". Done for this chunk: any agent can be deferred to a
named time, it is out of the queue and visibly parked until then, and it comes
back at the tail when the time arrives.

**Aligned on** (Victor, 2026-08-02):

- **Snooze only.** Indefinite backgrounding — the rock's other half — is a
  follow-up. It is the same suppression stamp with no deadline and no
  break-through, and it is a distinct verb (deferral is honoured, muting is
  absolute), so it does not belong inside this one.
- **Any agent can be snoozed, not just one that owes a turn.** Pin and mute
  already apply regardless of turn state, and the useful case — "this run has
  40 minutes left, do not hand it back before then" — is precisely the one where
  no turn is open yet. Settled rows carry the affordance too.
- **Break-through: `unknown`, and `idle` with reason `process_exited`.**
  `unknown` (reasons `stuck` / `no_evidence`) is the daemon admitting it cannot
  tell; `process_exited` is the agent's process actually gone. A normal end of
  run resolves `idle` with `prompt_idle` / `classifier_verdict`, so ordinary
  finishing does not break through — only dying does. A break-through consumes
  the snooze: the deferral was interrupted by something the user could not have
  anticipated, and they are back in the loop with that agent.
- **Snoozed agents get their own collapsible section at the bottom of the
  sidebar, above muted workspaces.** They are in neither band. This mirrors the
  muted section exactly, which is the shape the sidebar already has for "quiet
  but reachable", and it keeps waking one early findable — the settled band is
  long, and a deferral you cannot find is one you cannot undo.
- **Steering a snoozed agent does not wake it.** The vision's wording is that a
  considered act is not undone by business as usual, and typing into an agent is
  business as usual. Waking early is its own gesture.
- **Looking at a snoozed agent does not wake it,** by the vision's standing rule.

**In scope.** The snooze stamp and its wake timers, the duration menu (30m, 1h,
8h, tomorrow, Saturday, Monday), break-through, the sidebar's snoozed section
with wake times and a wake-now action, a `session.snooze` shortcut, ⌘K entries,
and handover on snooze the way settle already hands over.

**Deferred.** Indefinite backgrounding. A custom date/time picker (the vision's
seventh duration) — it needs a real input surface and the six fixed choices are
what get used. Any snooze presence on the home dashboard.

**Vision.** Advances the *Deferral* big rock. The rocks already shipped: state
detection, the queue, settle, the standing order, move-on, and automatic move-on.

## The shape

Snooze is **settle plus a suppression window**. It closes the open turn the way
settle does, and it stops the next one from opening until the deadline.

```text
  you snooze for 1h          ->  turn_settled_at = now
                                 turn_snoozed_until = now+1h
  agent stops, waiting_input ->  suppressed: no turn opens        [quiet]
  agent goes unknown         ->  break-through: snooze cleared,
                                 turn opens now                   [loud]
  1h elapses                 ->  snooze cleared; if the agent is in a
                                 turn-opening state, a turn opens stamped
                                 at the deadline                  -> tail
```

**Suppressing at open, not filtering at read, is the load-bearing call.** The
four existing exclusions (shell, chief, pinned, muted) filter at read: the stamp
keeps accruing and unpinning surfaces the turn at its *original* age. That is
right for pinning — the queue should not forget what happened while you were not
looking. It is wrong for snooze, which the vision says wakes to the *tail*: you
deferred it, so the clock on what you owe starts when you said you would come
back, not when the agent first asked. Suppressing at open is what makes the wake
stamp `now` rather than resurrecting an hour-old one straight to the head of the
band.

Because a snooze always settles first, `attention.Owed` needs no new input: a
snoozed session simply has no turn open. The stamp rides the wire only so the
sidebar can put the row in its section and say when it comes back.

## Data model

```sql
-- migration 85 (MAX(version) across every local profile DB is 84)
ALTER TABLE sessions ADD COLUMN turn_snoozed_until TEXT NOT NULL DEFAULT '';
```

```go
// store
func (s *Store) SnoozeTurn(id string, until, now time.Time) bool // settles + stamps
func (s *Store) WakeTurn(id string) bool                         // clears the stamp
func (s *Store) SnoozedSessions() map[string]time.Time           // boot rescheduling

// TurnStamps carries the deadline alongside the two turn stamps, so the one
// read the daemon already does for a session answers "is it deferred, and
// until when" as well.
type TurnStamps struct{ OpenedAt, SettledAt, SnoozedUntil time.Time }
```

`SnoozeTurn` writes both stamps in one statement so a turn can never be
suppressed while still open.

### On the wire

```tsp
model Session {
  turn_snoozed_until?: string;   // ISO; present only while a snooze is live
}

model SnoozeTurnMessage {
  cmd: "snooze_turn";
  session_id: string;
  until: string;                 // ISO instant, computed by the client
}

model WakeTurnMessage {
  cmd: "wake_turn";
  session_id: string;
}
```

**The client computes the wake instant.** "Tomorrow", "Saturday", and "Monday"
are calendar questions and need the *user's* timezone and locale; a remote
endpoint's daemon may be in neither. The daemon takes an absolute instant and
schedules against it, which keeps it out of the calendar business entirely.

Two commands rather than one with an empty `until`: waking early is a distinct
user act and reads as one at every layer.

## Architecture

```text
Daemon

  ws snooze_turn
    -> store.SnoozeTurn(id, until)     (settles and stamps in one write)
    -> cancelAutoSettle                (a countdown would promise a second settle)
    -> traceSettle                     (a snooze is a settle with a reason)
    -> scheduleSnoozeWake(id, until)
    -> broadcastSessionStateChanged

  ws wake_turn / timer fires / break-through
    -> store.WakeTurn(id)
    -> if attention.OpensTurn(current state): store.OpenTurnIfClosed(id, at)
    -> broadcastSessionStateChanged

  applyState  (session_state.go — the one place a turn opens)
    -> if snoozed && attention.BreaksSnooze(state, reason): wake, then open
    -> else if snoozed: skip the open            [the suppression]
    -> else if attention.OpensTurn(state): open  (unchanged)

  start-up: rescheduleSnoozeWakes() from the store; a deadline already past
            fires immediately and stamps the turn at the *deadline*, not at
            boot — a snooze that lapsed while the daemon was down has been
            owed since it lapsed, and the queue should say so.

Frontend

  buildQueueBands -> { chief, turns, settled, snoozed }
      a live turn_snoozed_until routes the row to `snoozed`, out of both bands
  Sidebar: collapsible "Snoozed" section, below Settled, above Muted Workspaces
  SnoozeMenu: the six durations; opened from a row control, the ⌘⇧S shortcut,
              and ⌘K
  advanceAfterTurnClosed: arriving in `snoozed` closes a turn the same way
              arriving in `settled` does, so snoozing hands over the next agent
```

## Boundaries

- `internal/attention` gains `BreaksSnooze(state, reason)` and nothing else. It
  stays pure — the break-through set is vocabulary, exactly like `OpensTurn`.
- `internal/daemon` owns the timers, the suppression branch in `applyState`, and
  the wake write. It must not re-implement the predicate inline.
- `internal/store` owns the column and the paired settle+stamp write.
- The app renders, orders, and computes wake instants. It must not decide
  membership: `turn_owed` remains the daemon's answer.

## Implementation steps

One slice. Snooze without a way back out is not shippable, and the wake path is
most of the work, so there is no honest cut here either.

- [x] `attention.BreaksSnooze`, with table tests over the state/reason pairs.
- [x] Migration 85, guarded on the column already existing.
- [x] `store.SnoozeTurn` / `WakeTurn` / `SnoozedSessions`, plus the deadline on
      `TurnStamps`, both branches (SQLite and in-memory), with tests: snoozing settles an open turn
      in the same write; waking clears; a wake with no snooze is a no-op.
- [x] The suppression branch in `applyState`, and `decorateSessionWithSnooze`
      stamping `turn_snoozed_until` (only while live).
- [x] Wake timers mirroring `auto_settle.go`: schedule, cancel, replace, stop on
      teardown, reschedule from the store at start-up.
- [x] Protocol: the field and the two commands; regenerate; bump
      `ProtocolVersion` in `constants.go` **and** `useDaemonSocket.ts`.
- [x] Daemon tests: a snoozed agent that stops opens no turn; it wakes to a turn
      stamped at the deadline; `unknown` and `process_exited` break through and
      clear the snooze; a normal `idle` finish does not; a snooze survives a
      daemon restart and a lapsed one fires on boot.
- [x] `snoozeInstant(choice, now)` in the app — pure, unit-tested across a
      month/year boundary and each weekday, with the day choices waking at 09:00
      local.
- [x] `buildQueueBands` gains `snoozed`; the routing and its tests.
- [x] The sidebar section, the row controls, wake-now, the duration menu, the
      `session.snooze` shortcut (⌘⇧S — free in the registry; check
      `Menu::default`), cheatsheet, and the ⌘K entries.
- [x] `advanceAfterTurnClosed` treats arrival in `snoozed` as a close.
- [x] Live verification on a throwaway profile, as
      `real-app:scenario-agent-queue-snooze` (catalogued as `agent-queue-snooze`):
      two real Claude agents, snoozed from the row menu for 30 minutes, watched
      out of the band into the section with its wake time, driven through a
      second run under the deferral with no turn opening, woken early to the
      tail, woken again by the timer, carried across a daemon restart, and
      SIGKILLed to see the break-through. `queue_get_state` gained the snoozed
      section so the assertions read the rendered DOM.

## Decisions

- **Snooze suppresses turn opening; it does not hide an open turn.** See The
  shape. The read-filter alternative wakes to the head of the queue, which the
  vision rejects.
- **A break-through consumes the snooze rather than pausing it.** The user is
  back in the loop with that agent; resuming a deferral they have since dealt
  with would be the system re-asserting an intent the user has moved past.
- **A lapsed snooze stamps the turn at its deadline, not at boot.** The turn has
  genuinely been owed since the deadline, and the queue is ordered by how long
  you have owed something.
- **Steering does not wake.** "A considered act that business as usual does not
  undo." The risk — forgetting a snooze and wondering why an agent never queues
  — is what the visible section with wake times is for.
- **Snoozing hands over the next agent, like settle.** Snooze is a turn-closing
  act performed on the agent you are looking at; leaving the user parked in an
  agent they just deferred would be the exact bookkeeping move-on removes.
  Pinning still must not hand over — the distinction is arrival in a band, not
  departure from one.

## Open questions

- Whether the snoozed section should be collapsed or expanded by default. Muted
  starts collapsed because it is *not ever*; snoozed is *not yet* and comes back
  on its own. Shipping collapsed with a count, since the wake is what surfaces
  it.
- Whether a snooze should show anywhere on the agent's own terminal tile. Left
  out: the row says it, and the tile is already carrying the auto-settle
  countdown.
