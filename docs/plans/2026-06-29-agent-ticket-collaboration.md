# Plan: Agent ticket collaboration (comment / subscribe / take)

## Goal

Let any agent interact with **any** ticket, not just the one bound to its session:

- **Comment** on any ticket — a one-shot note that informs the ticket's participants
  but does NOT subscribe the commenter.
- **Subscribe** to a ticket — opt in to its future notifications.
- **Take** a ticket — claim it (become the assignee); `--confirm` required if it is
  already assigned to someone else.

Plus a guidance change already shipped separately (PR #439): a non-chief agent asks the
user before reporting `completed`.

## Participation model (the core)

Today an identity participates in a ticket iff it is the **assignee** OR has **authored
any event** on it. Notifications (`UnreadTicketEvents`, `TicketParticipants`) derive
from that. Two changes:

1. **Commenting no longer confers participation.** Authoring a `commented` event is
   excluded from the participation rule, so a one-shot commenter informs participants
   without joining. (Decided with Victor.)
2. **Explicit subscription confers participation.** A new `ticket_subscriptions` table
   is a third participation source.

Resulting rule:

```
participant(ticket) = assignee
                    ∪ authors of NON-comment events     (chief via `created`, assignee via status)
                    ∪ subscribers                        (new, opt-in)
```

Keeping non-comment authorship as a source means the chief stays aware of tickets it
delegated (it authored `created`) with **no delegation rewiring** — the change is purely
additive plus the comment exclusion.

## Architecture Map

```text
Agent CLI (cmd/attn) ── unix socket ──> daemon handler ──> store
  attn ticket comment <id> <text>   -> handleTicketComment    -> AddTicketComment(author=session)        -> notify participants
  attn ticket subscribe <id>        -> handleTicketSubscribe  -> AddTicketSubscription(session, id)       (cursor stays 0: delivers history)
  attn ticket unsubscribe <id>      -> handleTicketUnsubscribe-> RemoveTicketSubscription(session, id)
  attn ticket take <id> [--confirm] -> handleTicketTake       -> AssignTicket(id, session, --confirm)     -> `assigned` event, notify

All four are agent commands: resolve identity from source_session_id, author as the
session, reply via protocol.Response over the unix socket (mirrors handleSetTicketStatus,
NOT the app's ws ticket_action_result path).
```

Participation queries change in `internal/store/ticket_events.go`:

```sql
-- UnreadTicketEvents scope (and TicketParticipants inverse):
assignee = ?
UNION  ticket_id WHERE author = ? AND kind != 'commented'   -- was: author = ?
UNION  ticket_id FROM ticket_subscriptions WHERE identity = ?
```

## Data Model

```sql
-- migration 58 (latest is 57). Mirror of ticket_event_cursors.
CREATE TABLE ticket_subscriptions (
    identity   TEXT NOT NULL,
    ticket_id  TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (identity, ticket_id),
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
```

Also add `ticket_subscriptions` to the explicit purge cleanup alongside
`ticket_event_cursors` (tickets.go PurgeExpired).

Protocol (one ProtocolVersion bump for the whole set): new agent commands
`ticket_comment`, `ticket_subscribe`, `ticket_unsubscribe`, `ticket_take`. Each carries
`source_session_id` + `ticket_id`; comment adds `comment`; take adds `confirm` (bool).

## Boundaries

- The store owns participation. Handlers never recompute it; they call the mutators and
  `notifyTicketObservers`, which already fans out to participants minus the author.
- `take` is reassignment: it sets `tickets.assignee` and appends an `assigned` event.
  It does NOT advance the taker's cursor — the taker is picking up work it hasn't seen,
  so its first `attn ticket inbox` should deliver the ticket's history (the same
  "freshly-assigned ⇒ deliver from start" rule the delegation cursor-advance is the
  deliberate exception to).
- `subscribe` does NOT advance the subscriber's cursor either — it leaves the cursor at
  0 so the first `attn ticket inbox` delivers the ticket's history, same as `take`. A
  nudge is a single fixed doorbell regardless of unread count (#438), so there is no
  "flood" to avoid by going future-only — and advancing to a per-ticket latest seq would
  reintroduce the global-max cursor hazard #438 was about. One rule: delegation's
  spawn-prompt is the ONLY cursor-advance exception; take and subscribe both deliver
  history.

## Implementation Steps

- [x] **Slice 1 — comment (#2):** `ticket_comment` protocol cmd + `handleTicketComment` +
      `attn ticket comment <id> -m <text>` CLI + `CommentTicket` client method. Participation
      queries exclude `commented` authorship (and dead `InvolvedTicketIDs` removed). Tests:
      store-level comment-exclusion (both queries), daemon linchpin (commenter not subscribed
      to later events), CLI parse, protocol decode. ProtocolVersion 133→134. CHANGELOG.
      CLI uses `--message`/`-m` (not a trailing positional) so flags compose around the id —
      a trailing-positional comment swallowed `--session` written after it.
- [ ] **Slice 2 — subscribe/unsubscribe:** branch AFTER slice 1 merges (same participation
      queries collide). Migration 58 + `ticket_subscriptions` store methods + participation
      UNION + purge cleanup + protocol cmds + CLI + client. Subscribe does NOT advance the
      cursor (delivers history). Tests: subscribe → events nudge + inbox delivers prior
      history; unsubscribe → they stop.
- [ ] **Slice 3 — take:** `AssignTicket` store method (+ `assigned` event) + `ticket_take`
      protocol cmd + `handleTicketTake` with `--confirm` guard + CLI + client. Tests:
      take unassigned → assigned; take already-taken without --confirm → error; with
      --confirm → reassigned + previous assignee notified.

## Decisions

- **Comment ≠ participation; subscribe/assignment = participation.** A one-shot comment
  shouldn't reintroduce the cross-ticket nudge-noise just fixed in #438 (Victor's call).
- **Keep non-comment authorship as a participation source** rather than moving fully to
  explicit subscriptions — the chief stays aware via `created` with zero delegation
  rewiring; the change stays additive.
- **Both `take` and `subscribe` deliver history (no cursor advance).** A nudge is one
  fixed doorbell regardless of unread count, so future-only buys nothing and would
  reintroduce the global-max cursor hazard from #438. Delegation's spawn-prompt stays the
  single deliberate cursor-advance exception.
- **Taking an assigned ticket needs `--confirm`.** Guards against an agent silently
  stealing another's active work.

## Open Questions

- Should `take`-over notify the previous assignee specially, or is the `assigned` event
  (which they receive as a still-participant via prior status authorship) enough? Leaning
  on the latter.

## Follow-ups

- A board/UI affordance to see subscribers, and an app-side "watch" toggle, are out of
  scope here (agent/CLI first).
- Reconcile dead `InvolvedTicketIDs` with the new participation rule (or delete it).
