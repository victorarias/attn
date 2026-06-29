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
  attn ticket list [--status X] [--all] -> handleTicketList    -> ListTickets(filter)                     (global board READ)
  attn ticket comment <id> -m <text>    -> handleTicketComment  -> AddTicketComment(author=session)        -> notify participants
  attn ticket subscribe <id>            -> handleTicketSubscribe-> AddTicketSubscription(session, id)       (cursor stays 0: delivers history)
  attn ticket unsubscribe <id>          -> handleTicketUnsubscribe-> RemoveTicketSubscription(session, id)
  attn ticket take <id> [--confirm]     -> handleTicketTake     -> AssignTicket(id, session, --confirm)     -> `assigned` event, notify

The write verbs (comment/subscribe/take) are identity-scoped: resolve identity from
source_session_id, author as the session, reply via protocol.Response over the unix
socket (mirrors handleSetTicketStatus, NOT the app's ws ticket_action_result path).
`list` is the read foundation — a GLOBAL board read (reuses the app's ticketToProtocol +
ListTickets), so it does NOT require a session (mirrors `attn list` for sessions);
source_session_id is optional and unused, kept only for command-shape uniformity.
```

## Discoverability (the read+guidance foundation)

A write verb is useless without a way to obtain a ticket-id, and agents had **no board
read at all**: `ListTickets` was exposed only to the app UI via the `tickets_updated`
broadcast. So the foundation slice ships `attn ticket list` (the read) *and* the guidance
that tells agents the capability exists — folded in so the feature only hits main when an
agent can actually discover, find, and act on a ticket (Victor's call: "ship as one
capability").

- **`attn ticket list`** is available to every agent (no code restriction), but guidance
  frames board-listing as a **chief/coordinator** read — a worker mostly acts on its own
  ticket plus ids handed to it (Victor's call: "chief-oriented, but unrestricted").
- **Guidance lives in three tiers, split by the "unrestricted comment / chief-oriented
  list" decision** (per the prompting principle — thin always-on, depth in the reference):
  - always-on `TicketAwarenessGuidance` (hooks.go, **all agents**): the **unrestricted**
    verb only — any agent can post a one-shot note to a ticket it's been handed with
    `attn ticket comment <id> -m …`. NOT `list` — pushing every worker to scan the board
    contradicts the chief-oriented steer.
  - the chief block (hooks.go, **chief only**): board-listing (`attn ticket list`) as the
    coordinator's read, alongside the existing `attn ticket inbox --watch` Monitor guidance.
  - the skill's `tickets.md` reference: the how (list filters; comment = one-shot, no
    subscribe). `list` stays available to every agent (no code restriction) — the reference
    documents it for any agent that reaches for it; only the always-on *push* is chief-framed.
    Extended when subscribe/take land — not deferred.
- **`attn ticket list --json` carries each ticket's `description`** (the brief), not just
  id/status/assignee — `ListTickets` selects it. So an agent commenting on a found ticket
  is not blind to its content. A `ticket show` to read a ticket's *activity thread* (prior
  comments / status history, which list leaves empty) is a clean follow-up, out of scope here.

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

- [x] **Slice 1a — comment (#440):** `ticket_comment` protocol cmd + `handleTicketComment` +
      `attn ticket comment <id> -m <text>` CLI + `CommentTicket` client method. Participation
      queries exclude `commented` authorship (and dead `InvolvedTicketIDs` removed). Tests:
      store-level comment-exclusion (both queries), daemon linchpin (commenter not subscribed
      to later events), CLI parse, protocol decode. ProtocolVersion 133→134. CHANGELOG.
      CLI uses `--message`/`-m` (not a trailing positional) so flags compose around the id —
      a trailing-positional comment swallowed `--session` written after it.
- [ ] **Slice 1b — board-read + guidance (folded into #440, held to ship as one capability):**
      `ticket_list` protocol cmd + `handleTicketList` (reuse `ticketToProtocol`) +
      `attn ticket list [--status X] [--all] [--json]` CLI + `TicketList` client method (global
      read, optional session). Guidance: lean clause in always-on `TicketAwarenessGuidance`
      + coordinator framing in the chief block + the how in `tickets.md`. Tests: handler
      returns the board (filter honored), CLI parse, protocol decode. ProtocolVersion 134→135
      (one bump). CHANGELOG covers the whole capability.
- [x] **Slice 2 — subscribe/unsubscribe:** migration 58 `ticket_subscriptions` (mirror of
      ticket_event_cursors, CASCADE) + `AddTicketSubscription`/`RemoveTicketSubscription`/
      `IsTicketSubscribed` + participation UNION in both queries + purge cleanup in
      SweepExpiredTickets + `ticket_subscribe`/`ticket_unsubscribe` protocol cmds (silent — no
      event, no broadcast) + `attn ticket subscribe`/`unsubscribe <id>` CLI (shared
      `parseTicketIDArgs`) + client methods. Subscribe validates the ticket + is idempotent +
      does NOT advance the cursor (delivers history); unsubscribe is a tolerant idempotent
      removal. Tests: store participation both ways + idempotency + bad-id; daemon lifecycle
      (subscribe → chief comment nudges subscriber + inbox delivers; unsubscribe → stops) —
      trigger via synchronous `commentOnTicket`, NOT the net.Pipe `callSetTicketStatus` (returns
      before the async doorbell). ProtocolVersion 135→136. CHANGELOG + tickets.md reference.
- [x] **Slice 3 — take:** reused the existing `AssignTicket` store method (+ `assigned`
      event) + `ticket_take` protocol cmd + `handleTicketTake` with `--confirm` guard +
      `attn ticket take <id> [--confirm]` CLI (`parseTicketTakeArgs`) + `TakeTicket` client.
      Take does not advance the cursor (delivers history); a self-take short-circuits with no
      redundant event; the result echoes `previous_assignee`. Notify is left to
      `notifyTicketObservers` (no special-casing the previous assignee — see Open Questions).
      ProtocolVersion 136→137. CHANGELOG + tickets.md reference. Tests: daemon take-over
      (refused without --confirm, reassigns + nudges the displaced assignee + delivers history
      with --confirm), unassigned/self/unknown, CLI parse, protocol decode.

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

- ~~Should `take`-over notify the previous assignee specially, or is the `assigned` event
  (which they receive as a still-participant via prior status authorship) enough?~~
  **Resolved: the `assigned` event is enough.** The handler stays behind the participation
  boundary — it calls `AssignTicket` + `notifyTicketObservers`, which fans out to the
  ticket's participants. An actively-working previous assignee authored status events, so it
  remains a participant and is nudged. The only gap is a previous assignee that was assigned
  but never reported anything: it authored no event, so it is no longer a participant once it
  loses the assignee slot and is not nudged — acceptable, since it was never visibly working.
  Special-casing the previous assignee would mean a handler reaching past the store's
  participation rule, which the Boundaries section forbids.

## Follow-ups

- A board/UI affordance to see subscribers, and an app-side "watch" toggle, are out of
  scope here (agent/CLI first).
- Reconcile dead `InvolvedTicketIDs` with the new participation rule (or delete it).
  (Done in slice 1 — deleted.)
- **Discoverability prose for cross-ticket collaboration.** Slice 1 ships `comment` with
  passive discovery via `attn ticket` help (which spells out "does not subscribe you").
  The skill-reference prose belongs to the *whole* collaboration surface — comment +
  subscribe + take read as one capability ("acting on another agent's ticket"), so a
  dedicated reference section is deferred until slice 3 lands rather than half-documented
  per slice. The own-ticket self-report references (`delegated-agent.md`,
  `delegatedTicketPrompt`) are deliberately untouched: they cover `ticket status`, a
  different surface.
