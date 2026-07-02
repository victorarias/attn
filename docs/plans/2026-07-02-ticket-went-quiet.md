# Plan: The went-quiet floor — projection + stopped-without-report event

## Goal

Close the last open mechanism gap in
[docs/vision/chief-delegation-awareness.md](../vision/chief-delegation-awareness.md)
("Still open: **the went-quiet floor**"): a ticket sitting in **Working** whose
assignee session simply stops emitting — no terminal report, no crash — is the one
silence the model doesn't catch today. `captureTicketCrashState` only fires when the
*process* dies mid-flight; a session that settles idle and says nothing leaves a
stale Working column forever. Two composable pieces: **(a)** a pure-frontend board
projection — working cards join the assignee session's runtime state (the ticket
assignee IS the session id, per `createDelegatedTicket`) plus time since last ticket
activity into a chip like "agent idle 14m, no report"; **(b)** a daemon-authored
**activity** event ("session stopped without a report — worth a look") appended when
a ticket-bound session settles into idle/waiting_input while its ticket is still
working, fanned out through the existing `notifyTicketObservers` path so the chief's
watch/nudge picks it up like any other move. Spine (vision, verbatim): "**The board
informs; it never gates**"; attn authors exactly ONE status itself — **crashed** —
so this is an activity/notification event, never a status change; "**The daemon
delivers a doorbell, never content.**"

## Architecture Map

```text
Current — silence is invisible:
  delegate() -> createDelegatedTicket (ticket opens Working, assignee = session id)
  agent settles without `attn ticket status ...`
    -> state write (one of 4 paths, all marked by cancelNudgeOnLeaveIdle):
       handlePTYState / handleState / updateAndBroadcastState / ...WithTimestamp
    -> notifyTicketSessionWentIdle only FLUSHES deferred doorbells (no-op when nothing unread)
    -> board card: Working + pulse, forever. Chief never signaled.
  (only a dead process is caught: captureTicketCrashState -> crashed)

Target (a) — frontend floor (continuous, pure join, NO protocol):
  useDaemonStore(): tickets + daemonSessions
    -> App.tsx passes daemonSessions -> TicketBoardSurface -> TicketBoardPanel
    -> BoardCard renders ticketQuietChip(ticket, sessions, now)      [utils/ticketQuiet.ts]
       working ticket + assignee idle/waiting/gone -> "agent idle 14m, no report"

Target (b) — daemon floor (edge-triggered, once per quiet episode):
  state write (all 4 paths) ---------> trackTicketQuietOnState(sessionID, state)
    isIdleForNudge(state)?  arm : cancel
    -> armTicketQuietIfEligible: session live + ActiveTicketForSession != nil
                                 + ticket.Status == working  => timer (grace, default 2m)
    -> ticketQuietFire(sessionID, self)      [identity check, mirror ticketBackstopFire]
      -> ticketQuietRecheck: still idle? ticket still working?
        -> store.AddTicketWentQuiet(...)     [event kind went_quiet, author "attn",
                                              activity row kind comment — NON-status]
        -> ticketEventLanded(ticketID, TicketAuthorAttn)   [attn author: no self re-arm]
             -> notifyTicketObservers(ticketID)  -> chief watch / nudge countdown
             -> broadcastTicketsUpdated()
  every producer (comment/status/attach/take/app edits/crash)
    -> ticketEventLanded(ticketID, author)   [single choke point]
         author != "attn" -> re-arm/cancel the assignee's quiet clock, then notify
  dropSessionRecord / Daemon.Stop -> cancelTicketQuiet / stopTicketQuietTimers

Tests:
  NewForTesting + fakeSpawnBackend harness (delegateForNotify, boundTicketID,
  commentOnTicket, callSetTicketStatus, callTicketInbox, fireNudgeNow)
    -> d.ticketQuietGrace override (mirror d.ticketBackstopGrace)
    -> hand-fire timers via ticketQuietFire (mirror TestTicketBackstopStaleTimerDoesNotDoorbell)
```

## Data Model / Interfaces

Store (`internal/store`):

```go
// ticket_events.go — seventh domain event
TicketEventWentQuiet TicketEventKind = "went_quiet"

// tickets.go — the one write. NEVER touches tickets.status; guards atomically.
// Returns appended=false (no rows at all) when the ticket is gone or no longer
// status=working — "suppressed when a terminal status arrives first", made atomic.
func (s *Store) AddTicketWentQuiet(id, note string, settledAt, now time.Time) (appended bool, err error)
//   tx: SELECT status FROM tickets WHERE id=? ; bail unless == working
//       appendTicketEventTx(TicketEvent{Kind: went_quiet, Author: TicketAuthorAttn,
//                                       Comment: note, Detail: settledAt.Format(RFC3339)})
//       if event appended: INSERT ticket_activity (kind='comment', author='attn', comment=note)
//   NO updated_at bump (mirror SetTicketResumeSessionID's "no board churn" precedent)
```

`Detail` = the settle timestamp is the episode identity: two distinct quiet episodes
(re-armed by a state blip, zero intervening ticket events) get different signatures,
so `appendTicketEventTx`'s consecutive-dedup can't swallow the second episode's
notification; a pathological double-fire within one episode still dedups.

Daemon (`internal/daemon/ticket_quiet.go`, new file):

```go
const ticketWentQuietNote = "session stopped without a report — worth a look" // neutral, never accuses
const defaultTicketQuietGrace = 2 * time.Minute

// Daemon fields (daemon.go, next to ticketBackstopMu/Timers/Grace):
ticketQuietMu     sync.Mutex
ticketQuietTimers map[string]*time.Timer  // keyed by session id; presence == armed episode
ticketQuietGrace  time.Duration           // 0 => default; test override

func (d *Daemon) trackTicketQuietOnState(sessionID, state string)   // 4 state paths
func (d *Daemon) armTicketQuietIfEligible(sessionID string)         // predicate + (re)arm
func (d *Daemon) cancelTicketQuiet(sessionID string)
func (d *Daemon) ticketQuietFire(sessionID string, self *time.Timer)
func (d *Daemon) ticketQuietRecheck(sessionID string)               // predicates + append + fan-out
func (d *Daemon) stopTicketQuietTimers()                            // Daemon.Stop teardown
func (d *Daemon) ticketEventLanded(ticketID, author string)         // re-arm hook + notifyTicketObservers
```

Protocol: add `went_quiet` to the `TicketEventKind` enum in
`internal/protocol/schema/main.tsp` (critical pattern #1). `ticketEventToProtocol`
is a blind cast and `fprintTicketInbox` (cmd/attn) prints `e.Kind` generically, so
neither needs code changes — the inbox renders `[time] went_quiet by attn` plus the
note. `TicketActivityKind` is untouched (the thread entry is a `comment`).

Frontend (`app/src/utils/ticketQuiet.ts`, new file — the exported seam):

```ts
export type TicketQuietChip = { state: 'idle' | 'waiting' | 'gone'; label: string };

// null unless ticket.status === 'working' && assignee set && the assignee session
// is idle/waiting_input (normalizeSessionState) or missing entirely ("gone").
// Age = relativeTime(ticket.updated_at, now) — time since last ticket activity.
export function ticketQuietChip(t: Ticket, sessions: readonly DaemonSession[], now?: Date): TicketQuietChip | null;
// The raw join, for the dashboard-state-card plan's sessionStateForTicket seam:
export function assigneeSessionState(t: Ticket, sessions: readonly DaemonSession[]): UISessionState | undefined;
// relativeTime MOVES here (from TicketBoardPanel.tsx) to avoid a component<->util
// import cycle; TicketBoardPanel re-exports it so existing imports/tests hold.
```

## Boundaries

- **The store owns the append + its atomic status guard.** `AddTicketWentQuiet` can
  never change `tickets.status` — crashed (ticket_crash.go) stays the ONE
  attn-authored status. The daemon pre-checks for cheapness; the mutator re-guards
  inside the tx so a report racing the fire wins.
- **ticket_quiet.go owns episode state** (the timer map IS the episode: arm on
  settle, cancel on leave-idle/removal, re-arm on non-attn ticket activity, delete
  on fire). Nothing else reads or writes it.
- **`notifyTicketObservers` is unchanged** and still owns fan-out; `ticketEventLanded`
  wraps it as the "an event landed" front door so future producers can't forget the
  re-arm (the compiler demands an author).
- **The doorbell stays `ticketNudgePrompt`** — a fixed "go read your inbox" trigger.
  The went-quiet note travels only in the durable event; the daemon never types it
  into a PTY.
- **The chip is a pure join** — no daemon or protocol involvement; the frontend must
  not try to detect "quiet" beyond joining state it already has.

## Implementation Steps

Slice 1 — store + protocol kind:

- [ ] `TicketEventWentQuiet` in the `TicketEventKind` const block
      (`internal/store/ticket_events.go`); update the "all six domain events" doc
      comment to seven.
- [ ] `AddTicketWentQuiet` in `internal/store/tickets.go` — mirror
      `AddTicketComment`'s tx shape, but: status guard first, event append before
      the activity insert (skip the activity row when the event deduped, so the two
      layers never diverge), and no `touchTicketTx`.
- [ ] Store tests in `internal/store/tickets_test.go` (mirror
      `TestTicketCommentsAndEdits`): `TestAddTicketWentQuiet` — appends event
      (kind/author/Comment/Detail) + activity row (kind comment, author attn);
      returns false and writes nothing for a non-working or unknown ticket; same
      `settledAt` twice dedups both layers; a different `settledAt` appends again;
      `updated_at` unchanged.
- [ ] TypeSpec: add `went_quiet` to `TicketEventKind` in
      `internal/protocol/schema/main.tsp`; `rm -rf tsp-output` (orphaned models glob
      back in); `make generate-types`; bump `ProtocolVersion` in
      `internal/protocol/constants.go` (never pin the number); then tsc-check —
      quicktype MERGES value-identical enums in generated.ts and silently drops a TS
      export, so confirm `TicketEventKind` and `TicketActivityKind` are still
      distinct exports and `pnpm --dir app exec tsc --noEmit` passes.

Slice 2 — daemon episode machinery:

- [ ] `internal/daemon/ticket_quiet.go`: constants + the functions listed above.
      Copy `scheduleTicketBackstop`'s ready-channel timer-identity pattern and
      `ticketBackstopFire`'s superseded-timer bail verbatim (internal/daemon/
      ticket_notify.go); `ticketQuietRecheck` mirrors `ticketBackstopRecheck`'s
      predicate-then-act shape but appends + `ticketEventLanded(ticketID,
      store.TicketAuthorAttn)` + `broadcastTicketsUpdated()` (mirror
      `captureTicketCrashState`'s tail) instead of arming a countdown.
- [ ] Daemon fields next to the ticketBackstop trio in the `Daemon` struct
      (daemon.go); `stopTicketQuietTimers()` in `Daemon.Stop()` next to
      `stopTicketBackstops()`.
- [ ] Hook the four state-mutation paths: add `d.trackTicketQuietOnState(id, state)`
      immediately beside each `cancelNudgeOnLeaveIdle` call — `handlePTYState`,
      `handleState`, `updateAndBroadcastState`,
      `updateAndBroadcastStateWithTimestamp` (all in daemon.go). Arm on
      idle/waiting_input (restart the clock if already armed — same "always grace
      after the latest signal" rule as the backstop), cancel otherwise.
- [ ] `cancelTicketQuiet(sessionID)` in `dropSessionRecord` next to
      `clearNudgeState` (a removed session must not fire; the fire-time re-check is
      the belt, this is the suspenders).
- [ ] Producers route through `ticketEventLanded(ticketID, author)`:
      `afterTicketMutation` (ticket_actions.go — gains an author param, all three
      app handlers pass `store.TicketAuthorYou`), `handleTicketComment`
      (ticket_comment.go), the agent status handler (ticket_status.go),
      `handleTicketAttach` (ticket_attach.go), `handleTicketTake` (ticket_take.go),
      `captureTicketCrashState` (ticket_crash.go, author `store.TicketAuthorAttn`).
      Inside: author == attn → skip straight to notify (the quiet fire and crash
      never re-arm their own clock); otherwise re-arm/cancel via
      `armTicketQuietIfEligible` on the ticket's assignee, then notify.
- [ ] Daemon tests `internal/daemon/ticket_quiet_test.go`, on the
      ticket_notify_test.go harness (`delegateForNotify`, `boundTicketID`,
      `commentOnTicket`, `callSetTicketStatus`, `fireNudgeNow`, `wasNudged`):
      - `TestTicketQuietFiresAfterGraceWhenSettledAndStillWorking` — tiny
        `d.ticketQuietGrace`; settle the delegate via `trackTicketQuietOnState`;
        poll `TicketEventsSince(0)` for exactly one `went_quiet` authored `attn`;
        ticket status still `working`; the idle chief gets its doorbell
        (`fireNudgeNow` + `wasNudged`, mirroring
        `TestTicketBackstopDoorbellsIdleChiefAfterAgentReports`).
      - `TestTicketQuietSuppressedWhenStatusLandsFirst` — hour grace; agent reports
        `ready_for_review` (`callSetTicketStatus`); hand-fire the captured timer;
        no `went_quiet` event.
      - `TestTicketQuietFiresOncePerEpisode` — after a fire, no timer remains armed
        (attn author didn't re-arm) and a second hand-fire of the stale handle
        appends nothing (superseded-timer bail, mirror
        `TestTicketBackstopStaleTimerDoesNotDoorbell`).
      - `TestTicketQuietRearmsOnNewActivity` — after a fire, `commentOnTicket`
        (author "you") re-arms; hand-fire; a second `went_quiet` event exists (the
        intervening comment broke signature adjacency).
      - `TestTicketQuietCanceledWhenAgentResumes` — `trackTicketQuietOnState(id,
        working)` clears the armed timer.
      - `TestTicketQuietCrashCaptureUnaffected` — `dropSessionRecord` on a
        `working`-state delegate still authors `crashed` and appends no
        `went_quiet`.
- [ ] Docs: one line in `internal/agent/attn_skill/references/tickets.md` (the
      inbox section) explaining the `went_quiet` event kind so a chief reading its
      inbox knows attn wrote it and what it does/doesn't mean; CHANGELOG entry
      (user-visible: the board and the chief now notice a delegate that stops
      without reporting); tick the vision doc's "went quiet floor" checkbox with a
      pointer to this plan.

Slice 3 — frontend projection:

- [ ] `app/src/utils/ticketQuiet.ts`: move `relativeTime` here from
      `TicketBoardPanel.tsx` (re-export from the panel so
      TicketBoardPanel.test.tsx's import keeps working); add `ticketQuietChip` +
      `assigneeSessionState` (use `normalizeSessionState` from
      `app/src/types/sessionState.ts`; `DaemonSession.state` is a plain string on
      the wire).
- [ ] `TicketBoardPanel.tsx`: `sessions?: DaemonSession[]` prop (default `[]`);
      `BoardCard` gains the chip — render
      `<span className="tb-quiet-chip" data-testid="ticket-quiet-chip">` when
      `ticketQuietChip(...)` is non-null; style in `TicketBoardPanel.css` (muted
      warning tone; define any color with a literal fallback — bare `var()` refs to
      undefined tokens are silent no-ops in the real app).
- [ ] `TicketBoardSurface.tsx`: accept + forward `sessions`; `App.tsx`: pass
      `daemonSessions` (already destructured in `AppContent`) at the
      `<TicketBoardSurface>` call site. **This plan also owns the dashboard
      wiring**: if the dashboard-state-card plan's `StateOfTheWorldCard` has
      landed, pass
      `sessionStateForTicket={(t) => assigneeSessionState(t, daemonSessions)}`
      at the `<Dashboard>` call site (one line; the card renders its In-flight
      chip only when the prop is present, so nothing breaks if it hasn't landed —
      skip and see Open Questions).
- [ ] Tests: `app/src/utils/ticketQuiet.test.ts` — chip for idle / waiting_input /
      missing session on a working ticket (label text pinned, injectable `now`);
      null for a working assignee, a non-working ticket, an unassigned ticket; the
      moved `relativeTime` suite comes along. `TicketBoardPanel.test.tsx` — a
      working card with an idle assignee shows `ticket-quiet-chip`; a working card
      with a working assignee and a done card do not (extend `makeTicket` with a
      small `makeSession` fixture).

## Verification

```bash
go build ./...
go test ./internal/store -run 'TestAddTicketWentQuiet'
go test ./internal/daemon -run 'TicketQuiet'      # scope -run: TestGitStatusScheduler has a pre-existing race
go test ./internal/daemon -run 'TicketBackstop|Notify'   # existing notify suite still green
rm -rf tsp-output && make generate-types && pnpm --dir app exec tsc --noEmit
pnpm --dir app test ticketQuiet
pnpm --dir app test TicketBoardPanel
```

Manual (dev profile, standing authorization): `make dev`, delegate a trivial task
from a chief, let the delegate settle without reporting, and confirm — the board
card shows the chip; after the grace the ticket's activity thread carries the
attn-authored note; the chief's `attn ticket inbox` delivers a `went_quiet` event;
the ticket is still in Working.

## Decisions

Settled with Victor (the brief) — not up for re-litigation:

- **Activity event, never a status.** attn authors exactly one status — `crashed`
  (`captureTicketCrashState`). Went-quiet is a NON-status activity/notification
  event; the ticket stays in Working. The board informs; it never gates.
- **Neutral copy: "session stopped without a report — worth a look."** Never
  "stalled"/"failed" — idle-with-working is an EXPECTED state:
  `delegatedTicketPrompt` (internal/daemon/delegate.go) tells delegates to ask the
  user before reporting `completed`, so a delegate quietly waiting on Victor is
  routine. The event is still correct then (Victor is the bottleneck; worth a
  look), but the copy must not accuse.
- **Author is `attn`** (`store.TicketAuthorAttn`), mirroring how crashed events are
  authored — an authoring identity, never an observer, so it accrues no cursors.
- **Once per quiet episode; re-arm on new ticket activity or a session state
  change.** The timer map IS the episode; the fire deletes it, and only a non-attn
  event or a fresh settle re-creates it.
- **Grace: 2 minutes** (`defaultTicketQuietGrace`), distinctly longer than the 8s
  self-monitor backstop. Justification: a settle is often the *start* of a normal
  notify handshake — backstop grace (8s) + visible nudge countdown (30s,
  `defaultNudgeCountdownWindow`) + the agent reading its inbox and reporting — so
  the quiet floor must sit well above that ~40–60s cycle or it fires mid-handshake
  on healthy sessions; 2m is ~3× that worst case yet still tells the chief within
  minutes. It also dwarfs idle blips between turns.
- **Suppressed when a terminal (or any non-working) status lands first** — eager
  cancel via `ticketEventLanded`, fire-time re-check, and the mutator's atomic
  `status == working` guard. Crash capture is untouched and wins: a crashed ticket
  is terminal, so the quiet path no-ops.
- **Frontend projection is a pure join, no protocol**, and the helper is exported
  from `app/src/utils/ticketQuiet.ts` — this plan owns the seam the
  dashboard-state-card plan consumes (`sessionStateForTicket`, left unwired there).

Implementation decisions made here:

- **New event kind `went_quiet`; the activity-thread row stays kind `comment`.**
  A distinct event kind keeps the chief's inbox (and `--json` consumers)
  machine-distinguishable and lets `Detail` carry the settle timestamp for
  episode-unique dedup — `AddTicketComment` has no Detail, and reusing `commented`
  would let the consecutive-event dedup swallow a repeat episode. The human-facing
  thread reuses `comment` (author "attn"), so `TicketActivityKind`, the detail
  panel, and the ticket UI need zero changes. `ticketEventToProtocol` and
  `fprintTicketInbox` are kind-agnostic — only the TypeSpec enum widens (hence the
  ProtocolVersion bump).
- **No `updated_at` bump.** The chip's "no report for 14m" age is
  `ticket.updated_at`; if the quiet event bumped it, the floor would reset its own
  clock and read fresh. Precedent: `SetTicketResumeSessionID` ("purely internal
  bookkeeping... never churns the board"). Consequence: an open TicketDetailPanel
  won't live-refresh for this entry (its refreshKey is `updated_at`) — visible on
  reopen; acceptable, the chip and the chief's inbox are the load-bearing surfaces.
- **`ticketEventLanded(ticketID, author)` is the single producer choke point** —
  re-arm and fan-out in one call, so a future producer can't land an event without
  the quiet clock hearing about it; attn-authored events skip re-arm so the fire
  can't feed itself.
- **The assignee is notified too, by design.** `notifyTicketObservers`
  blanket-notifies participants minus the author; the quiet session is the
  assignee, so it gets the standard countdown → doorbell → inbox nudge — a gentle
  "you went quiet" that often prompts the missing self-report. No special-casing.
- **The chip's "gone" variant** (working ticket, assignee session no longer
  exists) rides along: `ActiveTicketForSession` semantics make a cleanly-closed
  session with an unreported working ticket exactly the resume case, and the join
  already has the data. Crash is not "gone" — crashed tickets leave Working.

## Open Questions / Follow-ups

- Wiring `assigneeSessionState` into the dashboard state card's
  `sessionStateForTicket` prop is **this plan's job** (slice 3, App.tsx step —
  the card ships the prop unwired and its doc points back here). If the card has
  not landed when slice 3 executes, the one-line wiring moves mechanically to
  whichever PR lands second.
- Observed while grounding, out of scope: `notifyTicketSessionWentIdle` (the
  deferred-doorbell flush) is hooked only in `handlePTYState`, not the hook or
  classifier state paths — a busy non-self-monitor whose settle arrives via
  `handleState` may never get its deferred nudge. The quiet floor hooks all four
  paths and doesn't depend on the flush; file separately.
- `scheduled` sessions are deliberately outside `isIdleForNudge`, so a delegate
  parked on a cron never fires the quiet event — revisit if delegates start using
  crons in anger.
- The vision's remaining open items — push-vs-hold thresholds and multi-delegation
  synthesis — are untouched by this plan.
