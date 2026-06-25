# The communication gateway

A foundational, attn-owned substrate for **agent-to-agent communication** — one
delivery-tracked queue that every chief↔agent and agent↔agent message rides on. This
doc is the design for the **first PR**: the gateway built **standalone, unhooked from
attn, proven by a simulation harness**. Integration into attn's surfaces is a
*later* PR.

> The north star this serves is `docs/vision/chief-delegation-awareness.md` — read
> that for the *why*. This doc is the *what*: a self-contained spec for the gateway.

## Why / Alignment

**Why.** Today a delegated agent's outcome reaches the chief through one mechanism (a
live `dispatch watch` event) and is *separately* persisted through another (the
raw-tier journal), with different trigger conditions — so review/blocker outcomes get
lost on compaction, mail to a still-working agent rots until it next stops, and the
non-Claude (codex) path has no delivery at all. There is no single thing that knows
"this message was delivered / seen." The gateway is that single thing: every
message/outcome is a **delivery** with a known state, written once, consumed reliably
by whoever it's addressed to.

**The spine it serves:** *awareness, not autonomy.* The gateway only ever moves
information so a recipient can *consume* it and decide for itself. It never acts, and
it never streams message content into a PTY — the most it does to a live agent is
poke it to go read its own queue.

**The design calls (settled):**

- **One foundation we trust, built in isolation first.** The gateway ships as a
  single PR, **unhooked from attn**, exercised end-to-end by a **simulation harness**.
  Replacing existing parts later (the durable dispatch mailbox, the watch/journal
  internals) is fine — the point is a substrate we trust, not preserving old seams.
- **Delivery kind is explicit; no `Classify`.** The producer states the kind at
  enqueue. The snapshot→event classifier (`dispatch.Classify()`) was inference from
  the daemon-state-trigger era and **retires** when the gateway integrates. Noise
  suppression becomes "only meaningful things get enqueued."
- **Ack = output.** There is no explicit ack call. A delivery is acked the moment its
  content is output to the recipient. No agent is ever forced to confirm.
- **Hardcoded chief id.** One chief per profile, so the chief's recipient id is a
  literal constant baked into the API — a delegated agent addresses the chief with no
  lookup.
- **Flat 30-day TTL.** A single hard expiry for every kind; pure garbage collection,
  not the seam for any reliability feature.

## Scope

**In scope (this PR):** the gateway library + its storage + the `enqueue` / `consume`
operations + the two-consumer contract, all **standalone** and covered by the
simulation harness. No attn daemon wiring, no PTY, no real agents.

**Out of scope (later PRs / deferred):**

- *Integration* into attn — producers (agent self-report → `outcome`; daemon →
  `crashed`; chief→agent → `mail`), the `watch` consumer + delegation-time arming, the
  idle-nudge scheduler + `HasSelfMonitor`, orient-read + re-arm, and retiring the old
  mailbox/journal paths. That is the next PR.
- *Deferred (do not design around):* deep awareness-survives-compaction /
  reconstitute-across-days (compaction is fine; crashes resume from transcript); and
  the "agent went quiet without crashing" silence floor (a live prototype to be
  spec'd later). The gateway must not try to solve either.

## The model

### Delivery

A **delivery** is one unit of communication:

| Field | Meaning |
|---|---|
| `id` | unique delivery id |
| `from` | sender recipient-id |
| `to` | recipient-id (see Addressing) |
| `kind` | `outcome` \| `mail` |
| `payload` | the content (structured report for `outcome`, message for `mail`) |
| `state` | `pending` → `acked` (or `expired`) |
| `created_at` | enqueue time |
| `acked_at` | when its content was output to the recipient (nullable) |

`outcome` = something an agent reports about its own work (done / failed / review /
blocked / crashed). `mail` = a steering message from one party to another. The kind is
set by the producer at enqueue; the gateway never infers it.

### Addressing — per recipient

A **recipient id** identifies who a delivery is *for*:

- a delegated **agent** → its stable recipient id (stable across the agent's
  lifetime, independent of its current session);
- the **chief** → a **hardcoded literal constant** (e.g. `chief`). There is one chief
  per profile, so the constant is unambiguous and any agent can address the chief
  without resolving its session.

The chief **reads all** deliveries addressed to the chief id. A `consume` is scoped to
the caller's recipient id.

### Dedup — by content

Two deliveries collapse to one when they match on **`(from, to, kind, payload)`** —
i.e. dedup considers the *content*, not just the addressing pair. Re-sending the same
message is idempotent. Distinct payloads never collapse: a `needs_input` interim and a
later `done` are different content and both persist, so a consumer sees the real
sequence of a thread (no magic "latest-wins supersede").

### Bundling — by sender

One `consume` returns the recipient's pending deliveries **grouped by sender**, so the
chief gets *one coherent pull* — "from agent A: these; from agent B: these" — never N
separate pings. This is where multi-delegation synthesis is solved: at the substrate,
once.

### TTL — flat 30 days

Every delivery hard-expires 30 days after `created_at`, regardless of kind or state.
This is garbage collection only. 30 days comfortably spans normal compaction/restart,
so the ledger is durable-*enough* without being long-term memory (that remains the
keeper/chief journal, untouched and unrelated).

### Ack — output is the ack

`consume` returns a recipient's pending deliveries **and marks them `acked` in the same
step** (`acked_at` = now). There is **no separate ack call** and no `delivered`
middle-state requiring follow-up. Consequences, by design:

- The gateway still records *when* each delivery was acked, so a sender can see its
  message was seen — delivery tracking is preserved, the *ceremony* is not.
- Already-acked deliveries are **not re-output** on a later `consume`.
- Therefore `consume` returns **only `pending`** deliveries — a recipient catches up on
  what landed *since it last looked*, not a replay of what it already saw. (A restarted
  chief re-acquires only un-consumed threads; replaying already-seen-then-compacted
  threads is an intentionally deferred problem (see Non-goals), not solved here.)

## Operations (the API surface)

The standalone gateway exposes two core operations (exact transport — function calls
vs CLI vs IPC — settled in build, but the contract is):

- **`enqueue(from, to, kind, payload) → delivery_id`** — idempotent under content
  dedup; sets `state = pending`. Producers: an agent reporting an outcome, the daemon
  reporting a crash, a party sending mail. (All wired in the *integration* PR.)
- **`consume(recipient_id) → [{ sender, deliveries[] }]`** — returns pending deliveries
  for `recipient_id`, **bundled by sender**, and atomically marks them `acked`. Empty
  when nothing is pending.

Plus housekeeping: TTL expiry (a sweep that moves `created_at + 30d` deliveries to
`expired` / removes them). No other verbs — deliberately small.

## The two consumers (one queue)

The gateway is **consumer-agnostic**. Integration adds exactly two ways to call
`consume`:

1. **Event-driven** — a Claude agent arms a Monitor whose `watch` blocks on the
   gateway and emits + acks new deliveries as they land. No injection; best
   experience.
2. **Injected nudge** — for an agent with no self-monitor (codex), or a Claude that
   didn't arm one: when a recipient (**chief or agent**) has pending deliveries *and*
   is idle, attn pty-injects a *fixed* "consume your queue" poke. The recipient runs
   `consume` itself; **content never enters the PTY**.

Which consumer a session uses is picked by a **`HasSelfMonitor`** capability flag
(true for claude, false for codex) — *not* part of the gateway; noted here only so the
gateway stays clean of it.

## What it replaces (at integration, not now)

The durable, ack-tracked dispatch mailbox becomes `mail`-kind deliveries; the live
`dispatch watch` becomes the event-driven consumer; the raw-tier dispatch-outcome
journal becomes `outcome`-kind deliveries (the delivery ledger). The integration PR
removes those parallel paths. `dispatch.Classify()` retires.

## Build discipline — this PR

- **Unhooked.** No daemon, no PTY, no real agents, no protocol fan-out. A
  self-contained package with its own storage.
- **Simulation harness.** The async, multi-surface back-and-forth is the risk, so the
  PR's confidence comes from a harness that drives producers and consumers against the
  gateway directly. It **includes the nudge consumer path** (model it as "an idle
  recipient is poked, then calls `consume`"); it **may skip real Claude-Monitor
  arming** (drive the event-driven `consume` directly). Time/TTL is injectable so
  expiry is testable without waiting.

### Simulation-harness scenarios (the acceptance bar)

1. **Enqueue → consume → ack.** A delivery is `pending`; one `consume` returns it and
   leaves it `acked`; a second `consume` returns nothing.
2. **Content dedup.** Two identical `enqueue`s yield one delivery; two same-pair
   different-payload enqueues yield two.
3. **Bundle by sender.** Deliveries from A and B to one recipient come back grouped,
   one batch per sender.
4. **Hardcoded chief id.** Multiple agents enqueue to the chief constant; the chief's
   single `consume` reads them all, bundled by sender.
5. **Pending-only / no replay.** After ack, the same delivery never re-appears; a fresh
   consumer (simulating restart) sees only what arrived since.
6. **TTL.** With injected time, a delivery past 30 days is `expired` and not returned.
7. **Two consumers, same queue.** The event-driven path and the nudge path produce the
   same observable acks against identical input.
8. **Interleaving.** Interim then terminal outcomes from one sender both survive dedup
   and bundle correctly; mail and outcome kinds coexist for one recipient.

## Open questions (small)

- **Transport of the API** (in-process calls vs a CLI/IPC surface) — decide in build;
  the contract above is transport-independent.
- **Sender id for an agent** — its stable recipient id; confirm the exact identifier
  used so bundle-by-sender is stable across the agent's lifetime.

## Non-goals (restating, so they don't creep in)

- Not a general pub/sub for arbitrary sessions beyond chief↔agent / agent↔agent
  delivery.
- Not long-term memory — the 30-day TTL is GC, the keeper/chief journal is memory.
- Not awareness-across-compaction, and not the "agent went quiet without crashing"
  silence floor.
- Not autonomy: the gateway moves information for a recipient to consume; it never
  acts and never streams content into a PTY.
