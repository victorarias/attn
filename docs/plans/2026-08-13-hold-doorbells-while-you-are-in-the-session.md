# Hold doorbells while the user is in the session

Status: proposed

## The problem

An agent message lands in the pane the user is typing in. It has happened
more than once: `attn agent msg` types its payload plus Enter into the
target's composer, splicing a half-written prompt and submitting it.

The second half of the problem sets the shape of the fix. The user does
not stop owning a composer when he looks at another window: a prompt he
is drafting in session A is still sitting in A's composer while he
glances at B or at Chrome. A doorbell that fires the instant he switches
away splices that draft just as badly as one that fires while he types.

## The rule

A doorbell to session S is **held** when S is the session the UI is
showing, and stays held for **2 minutes** after the user switches away.

- While S is selected: held, with no deadline. Nothing lands in the pane
  the user is in, however long he stays there.
- On switching away at time T: held until `T + 2min`, then released.
- Switching back to S before `T + 2min` returns it to held-with-no-
  deadline.
- A session that was never selected is never held.

The 2 minutes is not a measured budget — it is the width of the gap the
user needs to hop to another window and come back to an unsent draft. It
is deliberately imperfect and is stated as such: a draft left for three
minutes still gets spliced. It buys the common case cheaply.

### The hole this leaves, and how it closes

Selection is sticky. `setSelectedSession` never clears, so quitting the
app leaves the daemon believing that session is still selected — and
under "held while selected" that queues its messages forever.

The hold therefore also requires a connected UI websocket client
(`d.wsHub.ClientCount() > 0`). No app running means nothing is selected
means messages flow. This needs no invented timeout: a closed app is not
"active in an agent". Either way the sender is told which wait it is in.

## Who waits and who does not

The split is by who asked. An automated delivery waits; a delivery the
user just clicked is what he is doing right now.

| Doorbell | Origin | Behavior |
| --- | --- | --- |
| Agent message (`agent_msg.go:136`) | automated | held; row stays queued, sender told why |
| Ticket nudge (`nudge_countdown.go:313`) | automated | deadline floored at the release instant |
| Chief-of-staff nudge (`chief_of_staff.go:118`) | automated | held; the existing inbox fallback carries it |
| Present handback (`present.go:616`) | user-initiated | unchanged — reached from `handlePresentSubmitRound` |
| Markdown annotation submit, `trigger_nudge` | user-initiated | unchanged |

Only agent messages need a retry rail, and they already have one: the
state-change drain. Nothing new is invented for the others — the chief
nudge falls back to the inbox as it does today, and Present handback is
exempt because the user is the one who just submitted the round.

### The sender's receipt

The sender learns which wait it is in, because the two end differently:

> queued (your user is working in that session — lands about 2 minutes
> after they move to another one)

## Why this does not slide in today

Four places where adding the hold the obvious way would make the
codebase worse. Each is a refactor that has to come first.

### 1. The doorbell's gate is checked by everybody and by the doorbell

`isNudgeDeliveryAllowed` has eleven call sites. Two are inside the
primitive (`typeDoorbellRoute:177`, `submitDoorbell:207`); the other
nine are callers pre-checking the same thing immediately before calling
it — `runNudgeDelivery:318`, `nudgeChiefOfStaff:130`, `present.go:612`,
`ws_markdown_annotations_submit.go:76`,
`notifyUnreadTicketSessionLocked:168`, `syncNudgeForState:142`,
`updateNudgeSelection:362`, `handleTriggerNudge:418`,
`drainAgentMessagesAfterStateChange:397`.

Adding the hold as a second gate the same way smears it over the same
nine sites, each of which then has to get both right, and the gate after
that costs nine more.

Under it is a real seam: some of those checks are **schedule-time**
("should I arm a countdown?") and some are **fire-time** ("may I write
bytes now?"). They share one predicate name today, which is why the
predicate is everywhere.

**R1 — the doorbell owns fire-time policy.**

```go
// origin:  automated | userInitiated
// outcome: delivered | blockedByApproval | heldForUser(until)
func (d *Daemon) ringDoorbell(
    sessionID, prompt string, origin doorbellOrigin,
) (doorbellOutcome, error)
```

Callers stop pre-checking and read an outcome. The hold is implemented
once. It also retires a smaller wart: `agentMessageQueuedDetail`
reverse-engineers its sentence with `errors.Is` over sentinel errors,
so every new reason is a new sentinel; an outcome makes it a map.

### 2. Three copies of a subtle timer handshake

`nudgeCountdowns`/`nudgeMu`, `autoSettleTimers`/`autoSettleMu`, and
`snoozeTimers`/`snoozeMu` are each: a map of per-session timers, a
`firesAt` stored beside the timer because `time.Timer` has no deadline
accessor, a ready-channel publish handshake so a zero-length window does
not fire against a half-written value, an identity check on fire, and
stop-and-replace. The copying is on the record in comments, in a chain:
`auto_settle.go:180` cites `nudge_countdown.go`, and `snooze.go:162`
cites `auto_settle.go`.

The release timer would be copy four, beside a fifth per-session map for
the deselect stamps.

**R2 — one `sessionTimers`**: `Arm(id, at, payload)`, `Stop(id)`,
`FiresAt(id)`, identity-safe fire. Phases and payloads stay with each
feature — auto-settle's arm/counting/held/resume, snooze's deadline
recheck against `WakeTurnAt`, the nudge's visible deadline. Only the
mechanism is shared.

This is the widest blast radius in the plan: three shipped,
behavior-critical timers. The test mass is what makes it safe —
1041/563/387 lines of tests against 586/567/266 lines of code.

### 3. User presence lives in three places under three locks

- `selectedSessionID` under `selectedSessionMu`, in **`tilecontent.go`**
  — a file about markdown tile content. Wrong home.
- `lastUserInputAt` and `lastAutoSettleActivityAt` under `lastInputMu`,
  in `nudge_countdown.go`.
- `lastUserActivityAtNano`, an atomic, in `presence.go`.

The hold predicate needs all three, and the obvious change bolts a
fourth map next to selection in the tile file. `nudge_countdown.go`
already carries two lock-ordering warnings ("nudgeMu must never be held
across a store read"; "the approval store read precedes nudgeMu: lock
order is one-way") — that is what the spread costs.

**R3 — a `userPresence` owner in `presence.go`**: selection, deselect
stamps, per-session keystroke recency, global activity. One lock, and
the derived predicates as methods (`InSession`, `HeldUntil`,
`QuietRemaining`). Mostly moves, and it retires a lock-order landmine
instead of adding to it.

### 4. The third countdown costs six edits

The wire already carries this shape twice: `nudge_fires_at` with
`ticket_unread` as its held companion, and `auto_settle_fires_at` with
`auto_settle_held`. Rendering has already been written twice:
`NudgeIndicator.tsx` exports `HeaderNudgeIndicator` and
`SidebarNudgeBar`, and `SettlingIndicator.tsx` exports
`HeaderSettlingIndicator` and `SidebarSettlingBar` — the same pair of
shapes, over the same `CountdownFill`, for the same two surfaces. A third
countdown writes them a third time, and joins the `||` chain at
`App.tsx:3097` and the by-name cancel list in `cancel_countdown.go:42`.

`cancel_countdown.go`'s header already names the situation: *"unrelated
mechanisms with unrelated re-arm rules, but from the user's side they are
one thing."* The cancel verb was unified; the shape and the rendering
were not.

**R4 — extract the shared bar/chip pair** used by all three, plus one
derived "is anything pending on this session" helper for `App.tsx`.

Deliberately **not** unified: the wire fields. Folding them into a
polymorphic `pending_actions` list would erase differences that are real
— `auto_settle_dismiss_armed` is not a countdown, and `ticket_unread`
outlives one — and that is a bigger change than this feature earns.

## Where the hold lives, after the refactors

One predicate on `userPresence`, one branch in `ringDoorbell`, one timer
from `sessionTimers`.

`setSelectedSession` stamps `deselectedAt[oldID]` on a real switch,
clears it when a session becomes selected again, and arms one release
timer at `now + 2min` — only when that session actually owes a delivery,
so a quiet switch does no work.

`releaseDoorbellHold` is the single fan-out: drain the queued agent
messages, re-arm the ticket-nudge countdown at its floored deadline.

## Reviewing the other countdowns

Four session-scoped countdowns can fire inside the grace window. Only one
of them can splice a draft.

1. **Ticket nudge** (`nudge_countdown.go`) — types into the composer.
   Resumes on switch-away and fires 30s later, squarely inside the two
   minutes. **Takes the floor.**
2. **Auto-settle** (`auto_settle.go`) — closes a turn; writes no bytes to
   the PTY, and is already frozen by any keystroke via `holdAutoSettle`.
   It can fire inside the grace, and that is harmless. **Left alone**,
   named here so the decision is on the record.
3. **Snooze wake** (`snooze.go`) — reopens a turn at an instant the user
   picked. No injection. **Left alone.**
4. **Agent message delivery** — has no countdown at all today. This plan
   gives it one.

## The indicator

A held delivery has to be visible, or the hold is just messages going
missing. Two states, because there are two:

- **In the session** — no deadline exists yet, so there is nothing to
  count down. A slowly pulsing dot and a count: "2 messages waiting —
  land when you leave".
- **After switching away** — a `CountdownFill` in `drain` direction to
  the landing instant: "lands in 1:47".

Clicking either delivers now. That is the way out: the hold is something
the daemon does on the user's behalf, so one gesture overrules it, the
same way `trigger_nudge` overrules a nudge countdown.

On the pulse: `AGENTS.md` bans continuously repainting animations, and
this is a deliberate exception kept to the cheapest form that reads as
flashing — a CSS opacity animation on a single small dot. Opacity is
compositor-only: no layout, no paint, no React re-render, and nothing at
all while the tile is offscreen. It runs only while a delivery is
actually held, which is rare and short.

### Protocol

Beside `ticket_unread` and `nudge_fires_at`, following the shape
`auto_settle_fires_at`/`auto_settle_held` already set:

- `held_delivery_count?: int32` — how many deliveries are waiting.
- `held_delivery_lands_at?: string` — the release instant; absent while
  the user is still in the session.
- a `deliver_held_now` command for the click.

`ProtocolVersion` increments; `generated.ts` moves with `generated.go`.

## Known gap: remote sessions

`session_selected` is routed to the endpoint owning the *newly* selected
session. The daemon the user just left never hears that it lost
selection, so a remote session stays "selected" on its own daemon
forever — and `attn agent msg` to a remote session is handled *by* that
daemon, so it would read a stale selection and hold indefinitely.

Fix: announce selection to every connected endpoint rather than routing
it to one; a daemon that does not own the announced session clears its
own selection. It belongs in this change rather than after it, because
the connected-client escape above does not cover it — the remote daemon
does have a client, the hub.

## Slices

An `epic/doorbell-hold` branch. Every refactor here changes paths that
already work — turn settling, snooze wake, ticket nudges, every doorbell
— so the question "can this PR damage what already works?" answers
itself.

1. **R3 — `userPresence`.** Pure move: three locks to one, out of
   `tilecontent.go` and `nudge_countdown.go` into `presence.go`.
2. **R2 — `sessionTimers`.** Adopted by the nudge countdown,
   auto-settle, and snooze; three copies of the handshake deleted.
3. **R1 — `ringDoorbell`.** Origin and outcome; the nine pre-checks go
   away; `agentMessageQueuedDetail` becomes a mapping.
4. **The hold.** Predicate, branch, release timer, the nudge deadline
   floor, the sender's receipt, and selection announced to all
   endpoints.
5. **R4 — shared countdown rendering.** No behavior change, two existing
   users adopt it.
6. **The indicator.** Protocol fields, `deliver_held_now`, and the third
   user of the shared component.

Slices 1–3 are behavior-preserving and carry no changelog-visible
change. Slices 4 and 6 ship as a pair: the first alone would hold
messages with nothing on screen to say so.

## Verification

Unit and integration in `internal/daemon`, driven through the real
`handlePtyInput` and `session_selected` handlers rather than hand-placed
stamps — `nudge_countdown_test.go` establishes that pattern.
`synctest` for the two-minute window and for the "nothing fires while
selected" assertion, since both assert elapsed time and a negative.

For slices 1–3, the existing test mass is the proof the refactor is
behavior-preserving; no test is rewritten to match a new shape without
saying why in the PR.

Live: a throwaway profile, two sessions, `attn agent msg` from one to the
other while a long prompt sits half-typed in the target. The draft must
survive the switch away and the return. Recorded for the PR body.
