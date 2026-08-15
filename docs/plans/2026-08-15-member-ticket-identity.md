# Plan: A member's ticket thread survives its sleep

## Goal

A crew member, not one day's session, owns its ticket participation. Subscriptions,
authorship, cursors, interruption attention, and delivery follow `member:<id>` across
day turnover. Any unread ticket activity may wake a sleeping member; the existing
autonomous wake limit is the governor.

The ticket assignee remains the concrete session doing the work. Taking a ticket
also attaches the member identity beside that assignee, so the thread survives when
the day ends without changing ticket/session ownership semantics.

## Architecture Map

```text
member-bound ticket command
  ticketIdentityForSession(session)
    -> member:trellis
  store mutation / subscription / cursor
    -> durable member identity

ticket event lands
  notifyTicketObservers(ticket)
    TicketParticipants(ticket)
      ordinary session -> existing countdown delivery
      role:chief_of_staff -> current chief session
      member:trellis
        awake -> current bound session -> existing countdown delivery
        asleep + unread
          -> crewWakeWithDelivery(autonomous=true)
            charge existing wake ledger
            claim one serialized day
            inject charter + predecessor letter
            submit the ordinary wake greeting after priming
            wait for its prompt-submit receipt
            arm the ticket doorbell from durable unread state
          -> refresh the new session's existing unread indicator
        wake refused
          -> existing notification feed names the limit and manual wake action
          -> member cursor remains unchanged
```

Day transitions adopt legacy session state before the binding moves or clears:

```text
ticket participant/subscription/cursor keyed by old session
  -> ensure member subscription for every involved ticket
  -> re-attribute historical ticket authorship to the member
  -> merge cursor into member cursor with max(old, member)
  -> delete old explicit subscriptions and cursors
```

On upgrade, startup applies the same migration to each member's current binding
and recorded `LetterSession`, which covers the live day and immediate predecessor.
Older session-only rows cannot be attributed to a member from the remaining data;
they stay as ordinary historical session participation rather than being guessed or
silently deleted.

## Data Model / Interfaces

No wire shape changes.

```go
TicketMemberIdentity("trellis") == "member:trellis"

MigrateTicketIdentity(fromSession, memberIdentity, now)
  // one SQLite transaction; idempotent; max cursor wins
```

The existing `ticket_participants` view already accepts arbitrary identities from
subscriptions and event authors, so `member:<id>` needs no new participation table.
Historical event and activity authors are re-attributed from the disposable session
to the durable member. Without that rewrite the member's new self-author exclusion
would replay its predecessor's own events as unread.

## Boundaries

- `internal/store` owns identity string helpers and the atomic participation/cursor
  adoption.
- `internal/daemon/ticket_identity.go` is the only session ↔ durable ticket identity
  map. It resolves member identities through the crew binding.
- Crew binding claim, transfer, and release call the store adoption before losing
  the predecessor session id.
- Ticket notification reuses `crewWakeWithDelivery`; it does not create another wake
  path or advance a cursor to acknowledge a failed delivery.
- Existing notifications make a wake-limit refusal visible without adding UI or
  protocol surface.

## Implementation Steps

- [x] Add member identity parsing/formatting and atomic legacy-session adoption.
- [x] Make member-bound subscribe, unsubscribe, inbox, attention, mutation authorship,
      and take participation use the member identity.
- [x] Resolve awake members and wake sleeping members from the notifier, with the
      ticket doorbell after crew priming and a durable warning on refusal.
- [x] Cover awake/asleep resolution, day-turnover cursor continuity, legacy migration,
      wake refusal/unread preservation, unread indicator, and watch behavior.
- [x] Add the changelog fragment; run targeted Go tests and `make test-quick`.
- [x] Install and exercise a throwaway profile for successful wake and
      `crew.wake_limit=0`, then clean it.
- [ ] Rebase, publish a ready PR, and wait for green CI, figgyster approval, and zero
      unresolved threads without merging.

## Decisions

- Every unread ticket event may wake a sleeping member. Importance filtering would
  create a second policy beside the wake ledger; the wake limit already bounds the
  unattended cost.
- The assignee remains a concrete session. `member:<id>` is the durable participant
  beside it, preserving existing reconciliation, resume, and active-ticket behavior.
- A wake refusal creates a warning notification and leaves ticket cursors untouched.
  A daemon log alone is not loud enough for an unattended refusal.
- Existing session-authored ticket history is re-attributed to the member while its
  participation and maximum cursor are adopted transactionally. This preserves both
  durable attribution and the observable read position.

## Follow-ups

- Garden/seed identities deliberately remain session/member-name behavior from their
  current design; this change establishes the ticket-only pattern requested here.
