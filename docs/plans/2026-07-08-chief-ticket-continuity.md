# Plan: Chief ticket continuity

## Why / Alignment

The chief-of-staff role is durable even though the session filling it can change. Chief-delegated tickets must therefore keep their unread history and notification ownership across a role transfer. Event authorship remains session-scoped for audit; it must not double as durable participation.

This chunk covers chief-delegated ticket ownership, inbox consumption, event routing, role-transfer nudge cleanup, and upgrade backfill. Ordinary assignment and explicit subscription behavior stay session-scoped. Runtime-specific waiting remains unchanged: Claude may drain `ticket inbox --watch`; Codex relies on nudges and one-shot inbox reads.

## Architecture Map

```text
Current:
chief session creates ticket
  -> created-event authorship implies participation
    -> session cursor owns unread state
      -> role transfer loses ticket scope and delivery

Target:
chief session creates ticket (session remains event author)
  -> durable ticket role ownership: chief_of_staff
    -> durable role cursor owns chief unread state
      -> current profile-role holder resolves as delivery target

Active chief inbox:
  -> consume personal session identity (assignment / explicit subscription)
  -> consume durable chief role identity (chief-owned tickets)
  -> merge and deduplicate events by sequence
```

## Data Model / Interfaces

```text
ticket_role_owners(role, ticket_id, created_at)
  primary key: (role, ticket_id)
  role: chief_of_staff

ticket_event_cursors(identity, ticket_id, cursor, updated_at)
  durable chief cursor identity: role:chief_of_staff
  session identities remain unchanged for ordinary participation
```

## Boundaries

- The store owns durable role participation and cursor scope.
- The daemon resolves a durable role observer to the current live session and its runtime delivery capability.
- Ticket events retain the concrete author session; role-owned creation does not permanently subscribe that session.
- Role transfer changes delivery only. It never copies or advances a cursor.

## Implementation Steps

- [x] Add durable role ownership storage and migration/backfill for active pre-fix delegated tickets.
- [x] Create chief-delegated tickets with durable chief ownership.
- [x] Resolve chief inbox/unread checks across personal and role identities without duplicates.
- [x] Route role notifications to only the active chief and clean up stale timers on transfer.
- [x] Add coverage for transfer, cursor continuity, retired-chief silence, ordinary participants, migration, and runtime guidance.
- [x] Run focused and full relevant tests, live-profile verification, changelog update, review, and PR packaging.

## Decisions

- Do not copy session cursors during transfer: the durable role cursor is the single chief bookmark.
- Keep role ownership separate from explicit subscriptions so `ticket unsubscribe` cannot remove product-owned chief awareness.
- Backfill ownership without creating or advancing cursors, preserving every unread event.

## Verification

- Focused continuity and guidance tests pass under the race detector.
- `go vet` passes for the store, notifier, and daemon packages.
- The uncached full Go suite passes serially. Parallel full-suite runs exposed an unrelated load-sensitive timeout in the Codex resume mapping test; that test and the full daemon package both pass independently.
- The signed `attn-dev.app` package passes the real-app ticket lifecycle scenario, and the migrated dev database records the ticket under durable chief role ownership without pre-seeding its role cursor.
