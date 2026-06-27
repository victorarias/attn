# Slice 3 execution plan — Delegation ⇄ tickets (live wiring)

> Detail plan for slice 3 of `2026-06-26-work-tracker.md`. The parent plan is the
> spec; this records the **sub-split and the implementation decisions**, because
> slice 3 is large and **removes live production paths** (the dispatch mailbox +
> report), so the destructive work is gated behind the additive replacements.

## Why split

Slice 3 spans the daemon, hooks, the `dispatch` CLI surface, the protocol (version
bump + `make generate-types`), and the store, and it **deletes** the mailbox/report
paths. That is too big for one reviewable PR and the deletions are irreversible. So
it lands as **stacked, additive-first sub-PRs**, with the removal **last and gated**.

## Decisions (resolved from the parent plan)

- **Binding = `ticket.assignee = the delegated session id`.** The agent's observer
  identity in `ticketnotify` is its session id (`AgentObserver(sessionID, agent)`), so
  assignee == identity *is* the binding — no new sessions column or protocol field.
  A `session → ticket` lookup is `ActiveTicketForSession` (the non-terminal ticket
  assigned to that session), used by crash detection and the report path. Parent plan:
  "the ticket binds to it" and "delegated sessions always have a ticket."
- **Ticket id at delegate time = a slug derived from the label/brief.** The agent
  isn't running yet, so attn derives the slug and resolves collisions with a numeric
  suffix (`ErrTicketIDTaken` → retry). (The agent-names-it path applies to
  agent-created tickets, a later surface.)
- **Initial status = Working.** Delegation starts the agent immediately, so the
  ticket is in flight from t0. (Todo is for an un-started backlog item.)
- **Assignee = the delegated session id.** This is the observer identity in
  `ticketnotify` (`AgentObserver(sessionID, agent)`), so assignee == identity is what
  makes the agent observe its own ticket. Display name resolution is a board concern.
- **Forward report = a ticket-native verb.** Agent reports via
  `ticket status <id> <status> [--comment]` → `SetTicketStatus`. The dispatch outcome
  verbs (`update/block/review/complete/fail`) are removed in the gated removal sub-PR.
  `handoff` is NOT removed here — it becomes `ticket attach` in slice 4.
- **DispatchWorkState → TicketStatus**: `in_progress→Working`, `needs_input→Blocked`,
  `ready_for_review→In Review`, `completed→Done`, `failed→Failed`.
- **Crash is attn-authored.** `handlePTYExit` → `captureDispatchCloseState` →
  `closedMidFlight`: when a bound ticket is still non-terminal on a mid-flight close,
  attn writes `SetTicketStatus(Crashed, author="attn")`. The one transition a dead
  worker can't make itself.
- **Reverse = ticket events + ticketnotify.** Chief edits a ticket → events →
  `ticketnotify.Notify` → Claude watch-consume / codex nudge. The
  `chief_of_staff_dispatch_messages` mailbox is removed in the gated removal sub-PR.

## Sub-PRs (stacked on `tickets/slice-2-events`)

| # | branch | additive? | scope |
|---|---|---|---|
| 3a | `tickets/slice-3a-binding` | ✅ additive | delegate creates + binds a ticket (slug, Working, assignee=session) and emits the created event; `ActiveTicketForSession` lookup. No migration/protocol change. |
| 3b | `tickets/slice-3b-report` | ✅ additive | `ticket status` verb + daemon handler → `SetTicketStatus` on the bound ticket; new protocol command (version bump). |
| 3c | `tickets/slice-3c-crash` | ✅ additive | wire the crash seam to author `Crashed` on a bound, non-terminal ticket. |
| 3d | `tickets/slice-3d-observe` | ✅ additive | chief + agent observers on live sessions; ticket events drive `Notify` (Claude watch / codex-nudge stub); end-to-end with a real Claude agent. |
| 3e | `tickets/slice-3e-remove-dispatch` | ⚠️ **destructive — gated on Victor's confirm** | delete the dispatch mailbox (`message/inbox/messages/read/ack`, store file, handlers, protocol) and the outcome-report verbs, now that tickets cover them. |

CI is main-only, so each PR is verified green locally + figgyster-reviewed. The
removal sub-PR (3e) is **not** started until the additive replacements are proven and
Victor signs off on deleting the live paths.
