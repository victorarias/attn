# Projection-side coalescing of snapshot pushes

Status: proposal, for decision. Recommendation is **do not build it**.

## The question

Every bus fact that matches a wire projection pushes to every WebSocket client
immediately (`wireProjections`, `internal/daemon/bus.go`). Bulk operations
already collapse their whole-list pushes through `coalesceSnapshots`, but
per-fact traffic does not coalesce at all. Under legitimate load — many working
sessions each honestly changing a few times a minute — does that per-fact ×
per-client multiplication need a per-(projection, subject) debounce, and what
would it cost in latency?

The answer is no, by three orders of magnitude, and the measuring turned up two
producers that are worth fixing instead.

## The short version

After the evidence-reason flap is fixed, the entire projection layer pushes
**357 messages per hour** — one every ten seconds — into a buffer that holds 256
and is shared with PTY output that can legitimately produce **200 messages per
second for a single session**. A debounce there is a mechanism with nothing to
do.

What the measuring did find:

- `tickets_updated` is a **217 KB** message that bypasses the coalescing
  machinery entirely, and **193 KB of it is ticket descriptions no client
  renders**. One observed second in production pushed 1.09 MB of it.
- `plugin.health.changed` publishes unconditionally every 15 seconds per
  connected plugin — 5,760 durable rows and 5,760 disk-scanning pushes a day,
  with attn idle. Once the flap is fixed it is the loudest wire message attn
  produces.

Both are the same defect class as the flap: a producer that publishes when
nothing changed. Neither is fixed by coalescing downstream, and both are
cheaper to fix than the mechanism this document was asked to design.

## Receipts

### How the numbers were taken

Two independent sources, both read-only against production:

1. **The durable bus log.** `~/.attn/attn.db` copied out with a read-only
   `.backup` (never opened read-write). 315,073 facts spanning
   2026-08-02T10:24:55Z → 2026-08-10T19:26:58Z — 201 hours, 110 sessions,
   1,461 session-hours. A model script maps every fact through
   `wireProjections` to the wire message it produces, applies snapshot
   coalescing where the producer wraps its publishes in `coalesceSnapshots`,
   and reports the resulting push timeline.
2. **The live socket.** A throwaway WebSocket observer connected to the running
   production daemon as an ordinary client (`client_hello`, no capabilities,
   never attached to a PTY) and recorded the arrival time and byte length of
   every message. Two runs, 5m09s and a shorter second run, agreeing to within
   0.4% on rate.

The bus log gives rates over a realistic week; the socket gives byte sizes the
log cannot know, because a fact's payload is not its projection's payload.

One known inaccuracy, in the conservative direction: the model assumed
`ticket.*` collapses inside `coalesceSnapshots` like `pr.*` does. It does not
(see below), so the true push count is 13 higher than the table reports across
201 hours. Nothing else in the model depends on it.

### Post-flap-fix is the world this is designed for

A sibling change fixes the evidence-reason flap: `runEvidenceResolveLoop`
re-resolves every session each second and broadcasts whenever the resolver
*reason* changes, and on a busy session two co-true clauses alternate at 1 Hz
without the state ever moving. In the log, **81.5%** of `session.state.changed`
facts land within 1 second of the previous fact on the same subject. The model
below reports both worlds; the flap-fixed column drops exactly those.

|                          | today       | post flap-fix |
| ------------------------ | ----------- | ------------- |
| pushes over 201 h        | 260,273     | 71,716        |
| average rate             | 1,295 /h    | **357 /h**    |
| seconds with any push    | 23.1 % of wall clock | 8.6 % |
| pushes/sec p50 / p99 / max | 1 / 5 / 36 | 1 / 3 / 36    |
| worst 10 s window        | 62 (6.2 /s) | 42 (4.2 /s)   |
| worst 60 s window        | 255 (4.2 /s) | **104 (1.7 /s)** |
| per session-hour         | 158.8       | **29.8**      |

Concurrency over the same window, measured as distinct sessions publishing per
5-minute bucket: median 2, p99 9, max 19.

Post-fix, one working session costs a push every two minutes.

### What a push actually weighs

Measured on the wire, production daemon:

| message                   | bytes (p50) | rate observed |
| ------------------------- | ----------- | ------------- |
| `tasks_changed`           | 25          | 233 /h        |
| `notifications_updated`   | 50          | rare          |
| `workspace_state_changed` | 265         | 35 /h         |
| `session_state_changed`   | **720** (703–820) | 6,944 /h (flap running) |
| `plugins_updated`         | 1,017       | 233–253 /h    |
| `sessions_updated`        | 6,009       | 127 /h        |
| `prs_updated`             | 7,037       | 47 /h         |
| `tickets_updated`         | **217,391** | 1.1 /h        |
| `initial_state`           | 239,486     | once per connect |

Whole-socket throughput during observation: **3.0 KB/s**, or 2.3 KB/s
discounting the one-off `initial_state` — with the flap still running. Post-fix
the same traffic is roughly 100 B/s average.

The per-session push is 720 bytes. The daemon marshals it once and enqueues the
same byte slice per client (`SendRawTextToMatchingClients`), so client fan-out
costs a pointer send each, not a re-serialization.

### The 256-message buffer is not the projection layer's buffer

Three facts settle this.

**PTY output shares the same channel.** `sendOutboundBlocking` on `c.send`
(cap 256) carries PTY frames and kitty blobs (`ws_pty.go:590`,
`ws_kitty.go:119`), the same channel `trySend` uses for broadcasts. PTY output
is already coalesced at the source — `internal/pty/session.go:238`, a 5 ms
window bounded at 256 KB, written because "MESSAGE COUNT, not byte volume,
balloons the WebKit frontend". Under flood that is up to **200 messages per
second for one session**. Against 1.7/s from every projection in the daemon
combined, coalescing projections to protect this buffer is not a rounding
error; it is below the noise floor of the thing that fills it.

**Eviction needs three consecutive misses.** `wsHub.run` increments
`slowCount` only when the buffer is full at send time and resets it on any
successful send; disconnect happens at `maxSlowCount = 3`. A client has to be
wedged, not merely slow.

**It has never happened.** Zero `client too slow` and zero `client slow` lines
in 21.5 hours of today's `daemon.log`.

At the worst post-fix minute (1.7/s), filling 256 slots takes a client stalled
for **2.5 minutes**. One busy session's PTY fills it in 1.3 seconds.

### Where the wire load actually is

**`tickets_updated` — 217 KB, uncoalesced, 89% dead weight.**

`projectTicketsUpdated` (`internal/daemon/ticket_board.go:97`) does not go
through `projectSnapshot`. Eleven whole-list projections do; this one broadcasts
directly, and its key `snapshotTickets` sits declared and unreferenced in the
list of snapshot keys (`internal/daemon/bus.go:561`) — the machinery was built
with a slot for it and never wired. So `coalesceSnapshots` cannot collapse the
board even where a producer wraps its publishes. Observed burst,
2026-08-09T18:51:15Z: five `ticket.status_changed` facts in one second, which at
today's board size is **1.09 MB** to every client.

Of the 217 KB, `SELECT SUM(LENGTH(description)) FROM tickets` is **193,435
bytes** across 45 tickets. The hook's own contract already says these are
"bare rows … The detail view fetches full records itself"
(`useDaemonSocket.ts:580`), and the detail panel reads `fullTicket` from a
`get_ticket` request, falling back to the board row only for the header. No
frontend code reads `description` off a board row. The board is shipping every
delegation brief in the workspace to every client on every ticket event, and on
every connect — `initial_state` is 239 KB, of which the board is ~217 KB.

**`plugin.health.changed` — 5,760 unconditional publishes a day, per plugin.**

`checkPluginHealth` (`internal/daemon/plugin_rpc.go:653`) publishes the fact on
every poll, healthy or not. `pluginHealthInterval` is 15 s; the gap histogram
confirms it — 14,350 of 14,919 gaps are exactly 14 s, and every one of the
14,919 belongs to a single plugin. Each publish writes a durable bus row and
runs `projectPluginsUpdated`, which rebuilds the catalog by scanning **two
plugin directories from disk** (`pluginCatalog` → `discoverPluginManifests`)
before pushing 1 KB.

This runs with attn idle, no session working, nothing to report. Post-flap-fix
it is the loudest wire message in the log (74/h over the week, 233–253/h live),
and the only one that is loud while the machine should be asleep. AGENTS.md
calls CPU burned while idle a defect, not a footnote.

Neither of these is a coalescing problem. Coalescing a 217 KB message that
fires 1.1 times an hour saves nothing; the message is wrong. Coalescing a
15-second heartbeat that never changes anything saves nothing; the publish is
wrong.

## Recommendation

**Do not build projection-side coalescing.** Fix the two producers.

The mechanism would be correct, general, and idle. It would add a timer per
(projection, subject), a flush goroutine that must never publish, an ordering
rule for removals, and a window constant that no measurement supports — to
smooth traffic that already runs at one message per ten seconds. The best
outcome of this investigation is discovering we do not need it.

The condition under which this document should be re-opened is stated below, as
a number, so nobody has to re-derive it.

### What to do instead

1. **Land the flap fix** (sibling work, ticket `a4-slice5-rt`). It removes 73%
   of the wire pushes and 66% of the durable bus log.
2. **Give `plugin.health.changed` a delta gate.** Publish only when
   status or message actually moves. Same shape as the flap fix, ~5 lines. This
   is the one that costs battery while idle.
3. **Drop `description` from board rows, and route `projectTicketsUpdated`
   through `projectSnapshot`.** The first takes the board push from 217 KB to
   ~24 KB and `initial_state` with it; the second puts the last whole-list
   projection inside the machinery every other one already uses, so a bulk
   ticket operation collapses like a bulk PR refresh does. Both need a check
   that no other client — the SDK, plugins — reads `description` off a list row,
   and a protocol bump if the field leaves the wire type.

Items 2 and 3 are small, independently shippable, and each has a measured
before/after. None of them is this document's mechanism.

### When to reconsider

Re-open this when the projection layer's **worst 60-second window exceeds
25 pushes/second sustained** at a client — 15% of one flooding session's PTY
budget, and the point at which projections stop being noise beside the traffic
they share a buffer with.

From the measured 29.8 pushes per session-hour, that is **3,000 concurrently
working sessions**. Production peaks at 19. If a future feature makes a *single*
session louder — a per-keystroke fact, a progress meter projected to the wire —
that changes the divisor, and the check is the same one: model the fact rate
against the 25/s line before shipping the producer.

## If we ever do build it

Recorded so the next person starts from the analysis rather than the idea.

### Mechanism

Per-(projection key, subject) debounce with **leading-edge immediate,
trailing-edge flush**. The first push for a quiet subject goes out at once; only
a second push arriving inside the window is deferred, and the flush re-reads
current state. This is exactly the shape `nextCoalescedRead` already uses for
PTY output (`internal/pty/session.go:254`), for the same reason: it costs zero
latency on the isolated change, which is the overwhelmingly common case, and
bounds only the flood.

A trailing-only debounce is the wrong shape here and should be rejected
outright. It adds its full window to *every* change, and at 29.8 pushes per
session-hour essentially every change is isolated — pure latency regression,
zero traffic saved.

### The window, and why no number is defensible today

A window is a tripwire: set past where any healthy case goes, so only broken
things feel it. To set one you measure the inter-arrival gap of pushes on the
same subject and place the window below the smallest legitimate gap. Post-fix,
the median gap on a subject is minutes. Any window short enough to be invisible
(≤50 ms) catches nothing; any window long enough to catch something is a
visible delay on a state pill. **There is no number here with a receipt**, which
is itself the finding.

If the rates ever justify it, the window comes from re-running the model script
against the bus log of that day, not from this one.

### Latency

The reaction a user would notice first is a hook-driven `applyState` — an
approval prompt appearing, a turn opening — which today reaches the wire within
one broadcast of the hook. That is the budget a debounce spends. Note the
resolver-driven transitions already carry up to 1 s (`evidenceTickInterval`),
so the *asymmetry* matters: a window that is invisible on the evidence path is
plainly visible on the hook path, and the hook path is the one attn is judged
on.

### Ordering and correctness

Dropping intermediate pushes for a subject is safe because a projection is a
pure function of current store state keyed by the subject —
`projectSessionStateChanged` re-reads `d.store.Get(sessionID)` rather than
carrying a payload, and the client's model is last-writer-wins per id. A
trailing flush that re-reads therefore delivers exactly the state a client
would have converged to. Three constraints hold that argument up:

- **Re-read at flush, never capture.** A deferred closure holding a stale
  decorated session defeats the whole argument. `projectSnapshot` already gets
  this right by deferring the `push` func, not its result.
- **Removals never coalesce with updates.** Facts whose entity is gone carry a
  payload precisely because the store cannot be re-read
  (`session.unregistered`, `workspace.unregistered`, `pr.disappeared`). An
  update and a removal for the same subject must flush in publish order, or a
  delete/re-register pair lands inverted. Simplest safe rule: a removal flushes
  its subject's pending entry immediately and bypasses the window.
- **The flusher must not publish, and must not run inline.** The bus holds its
  publish lock across the inline fan-out, so a projection that publishes
  deadlocks (`internal/daemon/bus.go:20`). A timer-driven flush is a separate
  goroutine that only writes to the hub — which also means it needs its own
  shutdown path and a test that no fact is left pending at daemon stop.

### What must never be coalesced

- **Request/result traffic.** A `*_result` settles a parked promise keyed by
  request id (`daemonPendingRequests.ts`). Dropping one does not lose a
  redundant repaint, it hangs a UI action until its timeout.
- **PTY output, desync, attach, tile content, fs bursts.** Off the bus by
  design, and PTY is already coalesced at the source with a better-placed
  window.
- **Markdown annotation events.** Already last-writer-wins under their own
  keyed scheme (`daemonMarkdownAnnotationEvents.ts`); a second scheme over the
  top would fight it.
- **The remote relay.** `broadcastRawWSMessage` forwards bytes a remote daemon
  already projected. Coalescing them would mean the hub parsing and re-keying
  another daemon's wire traffic; the correct place is that daemon's own
  projection layer, which gets whatever we build here for free.

## Reproducing the measurements

The two scripts live in the session scratchpad, not the repo — they are
instrumentation, not product. What matters is the recipe:

```bash
# 1. copy production out, read-only, never in place
sqlite3 "file:$HOME/.attn/attn.db?mode=ro" ".backup '/tmp/scratch/prod.db'"

# 2. fact rates and the push model
#    map bus_events through wireProjections, collapse coalesced snapshot keys
#    per (key, second), report pushes/sec and worst rolling windows, with and
#    without facts whose gap to the previous fact on the same subject is <= 1s

# 3. payload sizes: connect an observer to the live daemon as an ordinary
#    client, send client_hello, never attach a PTY, log (arrival, event, bytes)
```

Step 3 is the one that cannot be skipped. Fact counts say nothing about bytes:
`tickets_updated` is 1.1 events an hour and the single largest thing attn puts
on the wire.
