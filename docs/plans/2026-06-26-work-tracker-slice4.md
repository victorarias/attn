# Work Tracker — Slice 4 execution sub-plan (ticket view + resume + attachments)

Slice 4 of `docs/plans/2026-06-26-work-tracker.md`: the **ticket detail view** plus the
chief actions, resume, and attachments that let the chief drive work from the UI instead
of `dispatch`. Slices 1–3 built the durable model, the agent forward channel, the crash
seam, and the event-driven notification core (all backend/CLI).

**Scope guard:** this is the *detail view*, not the board. The board (status columns +
backlog + filters) is **slice 5** — out of scope here. Slice 4 navigation is the detail
panel opened from a session and the **⌘K** assign-/open-ticket actions; the visual board
comes later. ("ultracode after the 4th slice" → after this detail-view slice, before the
board.)

This is the half of the dispatch→ticket cutover that lets the chief **read and act on**
tickets. Once it lands, slice 3's gated **3e** (agent-prompt rewire + `dispatch` removal)
can proceed, because the chief no longer depends on the dispatch dashboard. Codex nudge
(slice 6) was largely built in 3d-2; the board is slice 5; both stay separate.

> Stacked, additive sub-PRs on `feat/tickets`. Frontend sub-PRs follow `app/CLAUDE.md`
> (vitest with `createMockDaemon` + call-count assertions, the request/result async
> pattern `sendPRAction`, the App/AppContent two-component rule).

## Binding decisions

1. **A full protocol `Ticket` type lands in 4a.** Slices 1–3 kept tickets store-local;
   the UI needs them on the wire. 4a defines `Ticket` + `TicketActivity` +
   `TicketAttachment` in `main.tsp` and value-mappers (the `SessionsToValues` idiom).
2. **Read API + live updates mirror dispatches.** `list_tickets` (the chief's *involved*
   set — `InvolvedTicketIDs`, not a global table; identity-uniform, free for a future
   per-agent board) and `get_ticket` (full record + activity + attachments), plus a
   `tickets_updated` WS broadcast on every mutation — the exact shape of
   `chief_of_staff_dispatches_updated` (`internal/daemon/chief_of_staff_dispatch.go:295`,
   frontend `app/src/hooks/useDaemonSocket.ts:1391`, store `app/src/store/daemonSessions.ts`).
   Non-archived only.
3. **Navigation is ⌘K + session binding, not a board.** Open a ticket's detail from its
   bound session, and ⌘K to assign-ticket-to-session / open-ticket-from-session. The
   detail panel is the slice-4 surface.
4. **Chief-side producers (comment / edit description / status / assign) land here** and
   call `notifyTicketObservers` — completing the board→agent steer with real content (the
   piece 3d-2 deferred). `AddTicketComment` / `EditTicketDescription` / `AssignTicket`
   already exist in the store.
5. **Resume reuses the existing session-resume path** (`set_session_resume_id`,
   `internal/daemon/daemon.go:2118`) + the ticket's stored `Cwd` / `LastAgentID`. No new
   resume engine.
6. **Attachments copy into `.attn/tickets/<id>/`**, mirroring the dispatch-handoff
   notebook-write pattern (`internal/daemon/notebook_dispatch_journal.go:74`); store has
   `TicketAttachment` + `AddTicketAttachment`. `dispatch handoff` folds into `ticket attach`.

## Sub-PRs (stacked on `feat/tickets`)

| # | branch | additive? | scope |
|---|---|---|---|
| 4a | `tickets/slice-4a-read-api` | ✅ additive | protocol `Ticket`/`TicketActivity`/`TicketAttachment` types; `list_tickets` (chief's involved set) + `get_ticket` (full record) commands + handlers; `tickets_updated` broadcast wired into the existing producers (create/status/crash); daemon list/value mappers. The data source. **(includes this plan doc.)** |
| 4b | `tickets/slice-4b-detail` | ✅ additive | frontend: zustand `tickets[]` + `setTickets`, WS `onTicketsUpdate` wiring, and the **read-only detail panel** (title / description / status / activity thread / attachments) fetched via `get_ticket` (request/result). Entry: open-from-session. vitest with `createMockDaemon`. |
| 4c | `tickets/slice-4c-actions` | ✅ additive | chief **actions** in the detail view — change status, **add comment** (new producer → `AddTicketComment` + `notifyTicketObservers`), **edit description**, **assign** (⌘K assign-/open-ticket-from-session). New protocol commands + async request/result actions. The chief→agent steer with real content. |
| 4d | `tickets/slice-4d-resume` | ✅ additive | **resume** a ticket's session — reopen using the ticket's `Cwd`/`LastAgentID` + `set_session_resume_id`; a Resume button on a closed/idle ticket that reloads the bound agent and closes the view. |
| 4e | `tickets/slice-4e-attach` | ✅ additive | **attachments** — `attn ticket attach --file <p>` CLI + daemon copy into `.attn/tickets/<id>/`, surfaced in the detail panel; `dispatch handoff` folds in here. |

CI is main-only → each PR verified green locally + figgyster-reviewed. After 4e the detail
surface is usable → run the **real-Claude e2e** (slice 3d-3's deferred check) via `make dev`,
then the **ultracode** multi-agent review. The board (slice 5) and the gated `dispatch`
removal (slice 3 / 7) follow.

## Test posture

- **4a** (backend): store + daemon handler tests — involved-set scoping, activity/attachment
  population, broadcast fired. Same harness as slices 1–3.
- **4b–4e** (frontend): vitest unit with `createMockDaemon` (call-count assertions to catch
  loops/races per `app/CLAUDE.md`); the detail panel is plain layout → vitest + happy-dom,
  not the Playwright harness. An e2e flow (open ticket → change status → live update) once
  actions exist.
- **Real-Claude e2e** after 4e via the dev install: delegate a real task, open its ticket,
  comment (nudges the agent), watch status move, resume a closed one.
