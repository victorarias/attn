# Plan: Ticket notification buffering and read-before-write

## Goal

Reduce ticket-notification interruptions while keeping updates immediate for the agent
currently doing the work.

- Current assignee: use the existing short safety countdown; never apply the 30-minute
  buffer.
- Every non-assignee observer—including the chief, subscribers, and former
  participants—uses one fixed 30-minute interruption budget across all observed
  tickets.
- Immediate assignee delivery piggybacks that observer's buffered activity.
- Explicit `ticket inbox` catches up immediately. Automated `ticket inbox --watch`
  respects delivery eligibility.
- A mutation attempted with unread target-ticket activity consumes and returns that
  activity, rejects the write, and requires a deliberate retry.

The first release has a fixed 30-minute window with test-only override. It does not
change Automations, delay traffic to the current assignee, or add an urgency bypass for
`needs_input`.

## Simplified Architecture

```text
Current:
ticket mutation
  -> durable ticket event
  -> participant identities
  -> per-session in-memory countdown
  -> fixed doorbell

ticket inbox / --watch
  -> ConsumeAll
  -> advance durable per-ticket cursors
  -> return ticket bundles

Target:
ticket mutation
  -> durable ticket event
  -> classify each live target against the ticket's current assignee
       assignee     -> desired deadline = normal safety countdown
       non-assignee -> desired deadline = max(normal countdown,
                                               attention_anchor + 30m)
                      where attention_anchor is last_attention_at when present,
                      otherwise the oldest unread event time
  -> existing countdown keeps the earliest requested deadline
  -> fixed doorbell

explicit inbox
  -> existing ConsumeAll immediately
  -> non-empty result updates last_attention_at

watch poll
  -> refresh short live-watch lease
  -> if immediate activity or buffered deadline is due:
       existing ConsumeAll (all unread activity piggybacks)
  -> otherwise return empty

guarded mutation
  -> one store transaction:
       target has updater-unread events -> consume target events; reject mutation
       otherwise                         -> apply mutation
```

The event log remains the batch and the existing cursors remain acknowledgement. There
is no materialized notification batch, batch identifier, prepare/ack protocol, or
second scheduler.

## Delivery Rules

### One clock per observer

Persist one value per delivery-budget identity:

```go
type TicketDeliveryAttention struct {
    ObserverKey    string
    LastAttentionAt time.Time
}
```

- Active chief: use durable `role:chief_of_staff` so the clock survives chief-session
  transfer, including activity from that chief session's personal subscriptions.
- Every other observer: use its ordinary observer identity.
- If the observer is the ticket's current assignee, immediate delivery wins regardless
  of any subscription or role participation.

`last_attention_at` moves forward after:

- a successful ticket doorbell;
- a non-empty explicit inbox consume;
- a non-empty watch consume;
- an immediate-assignee consume with piggybacked activity;
- a mutation-conflict catch-up.

An empty inbox read does not move it. Updating the clock never delays assignee traffic;
it only affects the observer's future non-assignee delivery.

Only `observer_key` and `last_attention_at` are durable. The daemon derives the next
buffered deadline and counts from current unread events. On restart or session recovery,
it recomputes the desired deadline and rearms the existing countdown.

### Earliest-deadline countdown

Extend `nudgeCountdown` to accept an absolute desired deadline:

```text
assignee deadline     = now + normalNudgeWindow
buffered deadline     = max(now + normalNudgeWindow,
                            attentionAnchor + 30m)
armed deadline        = min(existingDeadline, desiredDeadline)
```

For a first delivery, `attentionAnchor` is the oldest unread event timestamp. After
the observer has received or consumed activity, it is the durable
`last_attention_at` value.

This single rule supplies:

- non-sliding burst coalescing;
- one observer-wide timer across tickets;
- immediate assignee precedence over an already-buffered timer;
- piggybacking, because the resulting inbox consume already returns all unread events.

Approval wait, active-session pause, and recent-user-input protection remain final PTY
safety checks. They may postpone the doorbell without clearing unread state or moving
the 30-minute clock.

### Watch versus nudge

`--watch` is an automated delivery channel, not an explicit catch-up. Add a watch mode
to the existing inbox command rather than a separate batch interface.

Each watch poll refreshes a short in-memory lease for that session. Watch eligibility
and countdown firing share one daemon delivery lock:

- a live watch consumes eligible activity and the countdown sees no unread events;
- if the countdown reaches its deadline while the watch lease is live, it defers to the
  next watch poll and keeps a short lease-expiry backstop;
- if the watch disappears, the lease expires and the backstop doorbells the session;
- an explicit inbox ignores both the lease and buffer deadline.

The lease is intentionally ephemeral. After daemon restart, a real watch recreates it
on its next poll; otherwise the durable unread state rearms the nudge path. A crash may
repeat a fixed doorbell, but cannot lose ticket events.

## Read-before-write

### Agent commands

For `ticket status`, `ticket comment`, and `ticket attach`, use one store-local
transaction seam:

```text
attemptTicketMutation(updater identities, ticket, mutation):
  pending = updater-unread events on this ticket only

  if pending:
    advance only this ticket's applicable cursors
    commit
    return pending without running mutation

  run mutation in the same transaction
  commit
```

The CLI renders returned events using the current inbox formatter, explains that the
requested mutation did not run, and exits non-zero. The updater reviews the events and
retries. A new event before the retry rejects it again. Unrelated ticket cursors never
move.

This deliberately keeps today's consume-before-render reliability model. If the CLI
dies after the daemon response but before printing, the returned events are already
read—the same crash window as `ticket inbox` today. Avoiding it would require the
prepare/render/ack subsystem intentionally removed by this simplification.

`ticket attach` may stage/install files before its store commit, as it does today; a
catch-up conflict follows the existing rollback path and leaves no installed artifact.

### App actions and exceptions

The app already reads a ticket detail before status/comment/description/attach actions.
Return `latest_event_seq` with that detail and send it back as `expected_event_seq`.
The store rejects a stale value; the app refreshes the detail and requires retry.

| Mutation | Behavior |
|---|---|
| CLI status, comment, attach | Atomic target-ticket consume-or-mutate |
| App status, comment, description, attach | Optimistic `expected_event_seq` check |
| Take/assign | Allow; report that ticket history is unread; next content mutation is guarded |
| Subscribe | Allow; report newly visible unread history without consuming it |
| Unsubscribe | Always allow |
| New/delegated create | No prior activity to guard |
| attn crash, revival, reconciliation | System mutation bypasses the updater guard |

## Minimal Interfaces and State

### Store

Add:

- one `ticket_delivery_attention(observer_key, last_attention_at)` table;
- an unread summary that reports whether a session has assigned-ticket unread activity,
  buffered unread activity, and counts needed for logs;
- a target-ticket consume-or-mutate transaction helper;
- `latest_event_seq` / expected-sequence checks for app mutations.

Keep `ticket_events` and `ticket_event_cursors` unchanged.

### Daemon

Keep the existing `nudgeCountdowns` and `unreadCache`. Add only:

```go
deliveryMu     sync.Mutex
watchLeaseUntil map[string]time.Time
```

`notifyTicketObservers` loads the ticket's current assignee once, maps role identities
to live sessions as today, deduplicates delivery targets, and requests an immediate or
buffered deadline. It does not need a general observer-relation type.

### Protocol and CLI

Change only the existing surfaces:

- `TicketInboxMessage` gains `mode: explicit | watch`;
- ticket mutation results may carry one catch-up `TicketEventBundle`;
- ticket detail/actions carry `latest_event_seq` / `expected_event_seq`;
- bump `ProtocolVersion` and regenerate Go/TypeScript types.

Do not add batch IDs, cursor-advance messages, ack commands, delivery-reason enums, or a
new notification result hierarchy.

### Observability

Reuse `ticket_unread` and `nudge_fires_at` for the existing unread marker/countdown.
Add structured daemon logs containing observer key, immediate/buffered classification,
pending counts, desired deadline, channel, and outcome. Do not log comment bodies.

No new settings or notification UI are required in the first release.

## Implementation Steps

- [x] Store: add `last_attention_at` persistence, assignee-aware unread summary, atomic
  target-ticket consume-or-mutate helpers, and optimistic app sequence checks.
- [x] Daemon: make the existing countdown earliest-deadline-aware; classify targets by
  current assignee; update/recover observer attention; preserve chief-role continuity.
- [x] Inbox/watch: add watch mode, ephemeral watch leases, lease-expiry backstop, and
  shared delivery serialization; keep explicit inbox immediate and consume all unread
  for piggybacking.
- [x] Mutations: guard CLI status/comment/attach, add app expected-sequence conflicts,
  render caught-up events with the inbox formatter, and preserve mutation exceptions.
- [x] Protocol/guidance/verification: regenerate types, bump `ProtocolVersion`, update
  runtime guidance and `CHANGELOG.md`, then complete focused and packaged-app proof.

## Verification

### Core and store

- Hundreds of events across several non-assigned tickets retain one deadline and one
  observer interruption; provenance and ticket grouping survive.
- Two observers have independent clocks/cursors.
- Assignee plus subscriber/role overlap resolves to immediate.
- An assigned-ticket event moves a long buffered countdown earlier and piggybacks all
  unread activity.
- Chief transfer retains the role clock/cursor and retargets only the session.
- Restart before and after eligibility reconstructs the correct timer from unread
  events plus `last_attention_at`.

### Watch and nudge

- A live watch consumes an eligible burst once and suppresses its nudge.
- A dead watch's lease expires and the nudge backstop fires.
- Watch polling and timer firing at the same boundary do not produce two wake-ups.
- Explicit inbox consumes immediately during a buffer window.
- Approval, selection pause, and recent-input rearm preserve unread state and deadlines.

### Mutation guard

- Target-ticket unread rejects status/comment/attach, returns and consumes only those
  events, and writes no mutation.
- Retry succeeds with no new event and rejects again if one arrived.
- Concurrent event append cannot land between the unread check and mutation.
- Attach conflict rolls staged files back.
- App stale sequence refreshes without applying the action.
- Take, subscribe, unsubscribe, create, and system mutations follow the exception table.

### Live app

Extend `scenario-chief-ticket-watch.mjs` or add one serial packaged-app scenario with
short test overrides:

1. a chief comment reaches the assigned worker through the normal short countdown;
2. a worker burst remains buffered for the chief/subscriber and arrives once;
3. immediate worker activity piggybacks that observer's buffered subscription activity;
4. explicit inbox drains before the deadline;
5. a stale mutation renders catch-up, rejects, and succeeds on retry;
6. daemon restart preserves unread activity and reconstructs delivery.

Run focused store, `ticketnotify`, daemon, CLI, and frontend tests; generated-type
checks; and the signed non-production packaged-app scenario. Packaged scenarios remain
strictly serial.

## Decisions

- Reuse the event log as the batch and current cursor consumption as acknowledgement.
- Persist only `last_attention_at`; derive deadlines and counts from unread events.
- Deepen the existing countdown rather than add a second delivery scheduler.
- Prefer a live watch by lease; keep the nudge as its expiring backstop.
- Accept today's consume-before-render CLI crash window to avoid a two-phase ack system.
- Keep the 30-minute window fixed and preserve immediate current-assignee delivery.

## Out of Scope

- Automations.
- Delaying activity to the current assignee.
- Priority/urgent event semantics or a `needs_input` bypass.
- New notification channels, settings, or first-release UI.
