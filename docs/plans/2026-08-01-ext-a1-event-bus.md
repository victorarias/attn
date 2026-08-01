# A1 — Event bus core

Stage A1 of the [extension platform roadmap](2026-08-01-extension-platform-roadmap.md).
North star: [docs/vision/extension-platform.md](../vision/extension-platform.md).

Status: gate approved by Victor 2026-08-01. Implemented and live-verified
2026-08-01.

## Gate answers

The roadmap requires these to be settled with Victor before implementation.
They are.

### 1. Bus events are domain facts; wsHub projects them onto the wire

The log carries **facts** — a name, an indexed subject, a small JSON payload —
not the WebSocket event that a fact happens to produce. The WebSocket hub
becomes a consumer whose *projection* decides what the wire needs, which is
frequently a whole-list snapshot re-push.

This matters because most of attn's ~27 `broadcastXxx` methods are snapshot
pushes, not events: `broadcastSessionsUpdated`, `broadcastTicketsUpdated`, and
`broadcastPRs` re-serialize entire lists. Putting those on a durable log would
store a fat stream of UI invalidations, and an extension subscribing to it
would learn only that "the ticket list changed again" — it would have to diff
the list to recover the fact. Facts on the log keep it small and make
subscription meaningful.

The cost, accepted: A2 is a real per-broadcaster migration (fact + projection),
not a mechanical rename.

### 2. Ordered delivery, one event in flight, managed cursors

Consumers register durably (name, cursor, filter). Delivery is strict global
`seq` order with one event in flight per consumer; the cursor advances after
the handler returns, giving at-least-once. New consumers start **from now** by
default. Lag is `head - cursor` and is visible to the operator.

Parallel per-subject delivery was rejected for now: it turns the cursor into a
watermark plus an in-flight set, makes post-crash replay fuzzy, and forces
every handler author — including agents writing extensions — to reason about
concurrency from day one. It can be added later per-subject without changing
the published contract.

### 3. Retention: age window, cursor-aware, enabled consumers only

Trim events older than a window (default 30 days), never past the slowest
**enabled** consumer's cursor. A disabled or dead consumer does not pin the log
forever: when it comes back with a cursor below the trim point, it resumes at
head and the gap is logged. Bounded growth, no silent loss for live consumers.

### 4. Streams stay off the bus

The durable bus is for facts at human/agent scale. PTY output, PTY desync,
attach results, workspace tile content, and fs change bursts keep their
existing direct paths. `broadcastRawWSMessage` already performs per-client
predicate routing (`SendRawTextToMatchingClients` with a client predicate for
attach traffic), which pub/sub cannot express, and this is the hottest path in
the product.

A2's exit criterion is therefore: **every state-change broadcast goes through
the bus; byte streams stay direct, by an enumerated and documented list of
exceptions, and nothing else does.**

### Assumptions accepted alongside the gate

- Event names are dotted `domain.verb` with prefix-wildcard subscription
  (`session.*`). `ext.<name>.*` is reserved for extension-published events.
- The existing `ticket_events` log (migration 56) stays as-is in A1. Its
  per-`(identity, ticket)` unread cursors are a domain feature, not bus
  mechanics. Ticket mutators additionally publish facts to the bus; subsuming
  the two logs is a later cleanup, not part of this stage.

## Design

### Package boundary

`internal/bus`, following the `internal/tasks` precedent: it defines the
persistence interface it needs, accepts an implementation plus a `LogFunc` at
construction, and MUST NOT import `internal/daemon` (the daemon imports it).

### Schema

Migration 84 (latest existing is 83).

```sql
CREATE TABLE bus_events (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    subject    TEXT NOT NULL DEFAULT '',
    payload    TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_bus_events_name ON bus_events(name, seq);
CREATE INDEX idx_bus_events_subject ON bus_events(subject, seq);

CREATE TABLE bus_consumers (
    name       TEXT PRIMARY KEY,
    cursor     INTEGER NOT NULL DEFAULT 0,
    filter     TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL
);
```

`seq` is the cursor space, exactly as `ticket_events.seq` is today.

### Delivery

One dispatch goroutine per registered durable consumer: read the next batch
after `cursor` matching `filter`, invoke the handler per event in `seq` order,
persist the cursor after each success. A handler error retries with backoff and
does not advance the cursor (at-least-once, ordered, stalls loudly rather than
skipping). Lag is exposed for operator inspection.

The hub is an **ephemeral** consumer: it holds no row, starts at head, and
receives live fan-out only. Clients already refetch on reconnect, so per-client
replay is out of scope here.

### Projection

`internal/daemon` owns the fact → wire mapping. A publish call replaces the
body of a `broadcastXxx` method; the hub consumer's projection re-derives what
clients get. Wire behavior must not change.

## As built

1. **Migration 84** — `bus_events` (seq/name/subject/payload/source/created_at,
   indexed by name and subject) and `bus_consumers` (name/cursor/filter/enabled/
   updated_at). Store methods in `internal/store/bus.go`. `SaveBusConsumer`
   preserves an existing row's cursor and enabled bit, so daemon restart
   re-registers consumers without rewinding one or resurrecting a killed one.
2. **`internal/bus`** — `Event`/`Consumer`/`Store`/`Handler`, `Filter` (`*`,
   `prefix.*`, exact), publish with append + ordered ephemeral fan-out under one
   lock, one delivery loop per durable consumer, retention loop, `Status`.
3. **Daemon wiring** — `internal/daemon/bus_store.go` adapts the SQLite store to
   the seam (the `sqlTaskStore` arrangement). `ensureEventBus` runs at
   construction so a published fact projects even in a daemon that never
   started; `startEventBus` runs early in `Start`, before startup reconciliation
   can publish; `Stop` drops the subscription and drains.
4. **Migrated** — `session.state.changed` (the method already took the entity
   id, so no call site changed) and every ticket producer (16 call sites now
   publish `ticket.created` / `status_changed` / `commented` / `assigned` /
   `attached`, or `ticket.changed` where the site cannot name a sharper fact).
5. **Operator surface** — `attn bus status [--json]`, `attn bus enable|disable
   <consumer>`, reading and writing the profile database directly. No protocol
   version bump: status is a diagnostic, and the enabled bit is database-only by
   design so the kill switch does not depend on the daemon it kills.
6. **Tests** — `internal/store/bus_test.go` (log ordering, bounds,
   registration preservation, and the three trim cases: a lagging enabled
   consumer holds the line, the age window holds inside it, a disabled consumer
   does not pin), `internal/bus/{bus,filter}_test.go` (from-now default,
   catch-up, at-least-once redelivery on handler failure, filtered skip still
   advancing, ephemeral live-only, kill switch stop/resume, trimmed-past resume
   at head, status lag), `internal/daemon/bus_test.go` (the same guarantees over
   the real SQLite adapter, plus one fact producing exactly one board push).
7. **Pattern for A2** — documented at the top of `internal/daemon/bus.go` and
   summarized in AGENTS.md under "Event bus".

Two defects in the delivery loop were found by review and fixed before the stage
closed. Both only affected durable consumers, so neither reached the wire.

- **Backoff measured the wrong thing.** The failure streak was cleared only when
  a drain reached an empty batch, so a consumer that never caught up accumulated
  attempts across *different*, already-succeeded events and ratcheted to the
  retry cap on occasional transient failures. The streak now ends on every
  successful delivery: backoff escalates for an event the handler cannot get
  past, which is what it is for.
- **The kill switch could not reach a saturated consumer.** `drain` read the
  enabled bit once and then looped until the log was exhausted, so a consumer
  behind a busy producer stayed unreachable for the length of the burst — making
  `DefaultPollInterval`'s documented bound false in exactly the situation an
  operator would reach for the switch. The bit is now re-read on the poll
  interval from inside the batch loop.

Known and accepted: production registers no durable consumers yet — the hub is
ephemeral and extensions arrive in A4 — so the durable delivery loop is proved
by tests (including against the real adapter) rather than by production traffic.
The retention loop does run in production.

## Exit criteria

- Log and cursors survive daemon restart.
- A consumer that was down catches up from its cursor, in order.
- Trim honors enabled cursors; a long-disabled consumer resumes at head with a
  logged gap rather than pinning the log.
- Migrated broadcasters produce identical wire behavior.
- The A2 migration pattern is written down.

## Live verification

Throwaway profile (packaged app + daemon on this branch), 2026-08-01. Preflight
passed with no failures or warnings; protocol 200 agreed across CLI, app, and
daemon.

- Migration 84 applied; `bus_events` and `bus_consumers` present.
- Three ticket mutations produced exactly the expected facts, in order:
  `ticket.created`, `ticket.created`, `ticket.commented`, `ticket.created`,
  each carrying its ticket id as the subject.
- A WebSocket client observing those mutations received exactly one
  `tickets_updated` per fact — one board push per fact, wire behavior unchanged.
- Spawning a shell session produced one `session_state_changed` on the wire and
  exactly one `session.state.changed` fact.
- Daemon restart preserved the log (`seq 1..6`, 6 events retained).
- Operator surface end to end: `attn bus status` (table and `--json`),
  `disable`/`enable` against a lagging consumer row, and a non-zero exit for an
  unknown consumer name.
