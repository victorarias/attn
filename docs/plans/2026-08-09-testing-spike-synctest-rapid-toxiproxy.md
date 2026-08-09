# Plan: testing spike — synctest, rapid, Toxiproxy

## Goal

Evaluate three off-the-shelf testing tools against real attn seams, each in its
smallest useful form. This is Antithesis-inspired ("deterministic time,
property exploration, fault injection") but strictly shopping, not building:
every leg uses a mature library and targets code that exists today.

Each leg ends with a verdict: **adopt** (the tests merge and the pattern is
recommended for future work) or **drop** (delete the leg, record why in
Decisions). A leg that needs heavy plumbing to fit is a drop — the point of the
spike is to learn the fit, not to force it.

One PR carries all three legs plus the findings appended to this plan.

## Leg 1 — `testing/synctest` on the jobs runner

Go 1.25 stdlib (we are on 1.25.3), no new dependency. `synctest.Test` runs the
test's goroutines in a bubble with a fake clock that advances only when every
goroutine is durably blocked — sleeps and tickers complete instantly and
deterministically.

Target: `internal/jobs/runner_test.go` and `cron_test.go`. They currently pace
on real time — `deadline := time.Now().Add(3 * time.Second)` poll loops,
`time.Sleep(20 * testPoll)`, `<-time.After(2 * time.Second)` — which is
exactly the shape synctest deletes. The runner itself already injects `now`
(`runner.go:229`) and paces on `time.NewTicker(r.pollInterval)`, so it should
run inside a bubble unmodified.

Work: convert 2–3 of the sleep/poll-heavy tests (e.g. the retry/backoff and
cron-tick ones), keeping the originals' assertions. Do not convert the whole
file; the spike question is fit, not coverage.

Learning questions:
- Does the SQLite-backed store cooperate with the bubble (queries run to
  completion; only time/channel blocking must be durable)?
- Do the converted tests get faster and stop needing `testPoll` at all?
- Any daemon-side helper (fake clock, `jobstesting`) made redundant?

## Leg 2 — `pgregory.net/rapid` on rankkey

One small, mature dependency. Property-based testing with generation and
automatic shrinking; also does stateful/model-based sequences.

Target: `internal/rankkey` — `Between(a, b)` has crisp documented invariants:

```go
// rapid sketch, not literal
rapid.Check(t, func(t *rapid.T) {
    // generate a sorted list of valid keys via repeated Between inserts
    // at rapid-chosen positions (stateful exploration), then assert after
    // every insert:
    //   a < K < b (byte order)          — strict betweenness
    //   !strings.HasSuffix(K, "0")      — canonical, no trailing min digit
    //   whole list still strictly sorted
})
```

The stateful sequence is the interesting part: hundreds of random
insert-front/insert-back/insert-between operations, checking the whole-list
invariant each step — the kind of exploration the existing example tests in
`rankkey_test.go` don't do. Key-length growth under adversarial insertion
patterns is worth asserting loosely (it may not be boundable in general —
if a tight bound doesn't hold, report the observed growth rather than forcing
a property).

Learning questions:
- Does shrinking produce readable minimal counterexamples (its main selling
  point over hand-rolled fuzzing)?
- Is the rapid style compatible with our table/corpus test conventions?

## Leg 3 — Toxiproxy on the daemon WebSocket

`github.com/Shopify/toxiproxy/v2` — embed the proxy server in-process in the
test (no external binary, CI-friendly); drive it with the toxiproxy Go client.

Target: the slow-WebSocket-client eviction path, which exists but has no test
that exercises it through a real degraded network:

```text
test WS client (nhooyr.io/websocket, already a dep)
  -> toxiproxy listener :A            — bandwidth/latency toxics applied here
    -> daemon WS listener :B          — real daemon via useFreeWSPort(t)
         wsHub: send chan cap 256, maxSlowCount = 3
         -> disconnect StatusPolicyViolation "client too slow"
             (internal/daemon/websocket.go:365,422-437)
```

Scenario: connect one healthy client and one via toxiproxy; apply a bandwidth
toxic (or stop reading) on the proxied leg; generate sustained broadcast
traffic; assert the throttled client is evicted with the "client too slow"
close status while the healthy client keeps receiving; then remove the toxic,
reconnect, and assert recovery. Follow the existing daemon_test.go pattern
(`useFreeWSPort(t)`, harness builder) for wiring.

Learning questions:
- Is the embedded-server setup light enough to be a reusable helper
  (`internal/daemon/toxitest` or similar) for future remote-leg tests?
- Does the eviction actually fire on a throttled-but-open TCP connection, or
  only on a closed one? (If the answer is surprising, that alone justifies
  the leg.)

## Boundaries

- Each leg is independent — a drop verdict on one does not block the others.
- No production code changes except trivially exposing an existing seam
  (e.g. a constructor option that already half-exists). If a leg wants real
  refactoring, that is a drop verdict with a note, not a refactor.
- New dependencies: `pgregory.net/rapid`, `github.com/Shopify/toxiproxy/v2`
  (test-only). Nothing else.
- Test safety rules apply as everywhere: `config.ScopeTestEnvironment`, never
  production `~/.attn`.

## Implementation Steps

- [x] Leg 1: convert 2–3 jobs runner/cron tests to `synctest.Test`; remove
      their real sleeps/deadline polls; note speed and determinism delta
- [x] Leg 2: add rapid; write stateful `Between` property test in
      `internal/rankkey`; confirm shrinking output quality with a seeded
      forced failure (then remove the forced failure)
- [x] Leg 3: add embedded toxiproxy helper + slow-client eviction test in
      `internal/daemon`; healthy-client control and post-toxic recovery
      included
- [x] Run the full Go suite (`make test`) — legs must not slow it noticeably;
      note per-leg wall-clock cost
- [x] Append a Findings section to this plan: per leg, verdict
      (adopt/drop), evidence, and — for adopts — the one-line rule for when
      future work should reach for the tool
- [x] Changelog fragment (internal/testing entry) per repo policy

## Decisions

- Off-the-shelf only: hand-rolled simulation harnesses were explicitly
  rejected as too much work for the gain (Victor, 2026-08-09).
- Toxiproxy targets the local WS listener, not the OrbStack remote leg — the
  remote leg is the eventual prize but too heavy for a spike; the helper
  should not preclude it.
- Legs merge individually on adopt; a spike branch that only produces
  knowledge (all drops) still updates this plan and closes.

## Open Questions

- Does synctest's bubble tolerate the store's occasional non-time blocking
  (mutexes, SQLite I/O)? Expected yes (transient running is fine; only
  durable blocking must be on time/channels), but this is the leg's main
  risk.

## Follow-ups

- If Leg 3 adopts: a Toxiproxy leg for hub transport / remote relay
  (currently zero CI coverage of hub→remote command flows).
- If Leg 1 adopts: convert remaining timing-paced tests opportunistically
  when they flake, not as a sweep.
- Deferred from the wider Antithesis discussion: sometimes-assertions
  catalog, continuous invariant checking, state-corpus seeding — all
  parked, revisit only if the spike changes the cost picture.

---

# Findings

All three legs **adopt**. Every measurement below is from an M-series Mac,
Go 1.25.3, and is reproducible from the commands quoted.

## Leg 1 — `testing/synctest` on the jobs runner: **adopt**

**Rule:** reach for `synctest.Test` when a test asserts something about
elapsed time — a backoff, a debounce, a recurrence — or asserts that
something did *not* happen. Do not reach for it when the code under test
talks to a real socket or opens its own `database/sql` handle.

Three tests converted: `TestFailuresBackOffThenGoDeadOnce`,
`TestAKindIsSerializedWithItselfButNotWithOthers` (both
`internal/jobs/runner_test.go`) and `TestACronEntryFiresOnItsIntervalAndRearms`
(`cron_test.go`). Assertions unchanged.

**Evidence**

- `go test -count=3 ./internal/jobs`: **0.735–0.893s → 0.449–0.470s**
  (baseline measured against the pre-change files via `go test -overlay`).
  Three of roughly thirty tests converted cut the package's wall-clock
  roughly in half, because what they spent was almost entirely fixed sleeps.
- Per-test: the three summed to ~95ms (0.005 + 0.05 + 0.04) and now sum to
  ~15ms.
- `go test -race -count=20` on the three: clean.

**What the conversion actually removed.** Two things, and the second matters
more than the speed:

1. The `fakeClock` injected through `Options.Now` and the shortened
   `PollInterval`. Inside a bubble `time.Now` *is* the fake clock, so a
   bubbled test runs the runner on its production 1s poll interval and
   advances an hour of it instantly. `newBubbleRunner` therefore configures
   neither.
2. Every `waitFor` poll loop, and with them the difference between a claim
   and a guess. `TestAKindIsSerializedWithItselfButNotWithOthers` asserts
   that the second serial job never starts; it used to believe that after
   sleeping 40ms. `synctest.Wait` returns when every goroutine in the bubble
   is durably blocked, so the same assertion is now about a system with
   nothing left to do. Same for "a dead job is never re-selected", which now
   really does let a fake hour of dispatch passes run first.

**The open question, answered — with a caveat worth knowing.** A throwaway
probe ran 200 real cgo SQLite writes (`mattn/go-sqlite3`) from a bubbled
goroutine interleaved with fake-clock sleeps: all 200 rows landed and fake
time advanced exactly 200s. SQLite I/O is transient blocking and does not
stall a bubble.

But opening the store *inside* the bubble fails:

```
panic: deadlock: main bubble goroutine has exited but blocked goroutines remain
goroutine 40 [select (durable), synctest bubble 1]:
database/sql.(*DB).connectionOpener(...)
```

`database/sql.OpenDB` starts a `connectionOpener` goroutine that lives as
long as the DB. Opened in the bubble it joins the bubble and never exits, so
`synctest.Test` reports a deadlock even though the test itself passed. Moving
the `store.New()` call above `synctest.Test` fixes it completely (3/3 runs,
exact fake time). **Open long-lived resources outside the bubble; put only
the code under test inside it.**

The generalisation: the bubble is the boundary. `internal/jobs` over its
in-memory store qualifies. A `internal/daemon` test with real sockets does
not — a goroutine blocked on a real network read is not durably blocked, so
time never advances. That is why Leg 3 is not a synctest test.

## Leg 2 — `pgregory.net/rapid` on rankkey: **adopt**

**Rule:** reach for rapid when a unit has a stated invariant and a large
input space — especially when the interesting failures need a *sequence* of
operations rather than one bad input. Keep the example tests; rapid explores,
it does not document.

Two properties in `internal/rankkey/rankkey_property_test.go`: a stateful one
(`t.Repeat` over insert-front / insert-back / insert-between / append, with
whole-list order and canonical form re-checked before and after every action)
and one bounding key growth to at most one digit past the longer bound.

**Evidence**

- Cost: 100 checks per property, **3.4ms + 1.0ms** of check time; package
  `-count=3` goes **0.383–0.422s → 0.486–0.534s**, so ~35ms a run.
- Confidence available on demand: `-rapid.checks=200000` passes in 23s and
  found nothing. `Between` looks genuinely correct.
- **Shrinking is as advertised.** Against a `Between` mutated to forget its
  bump (`digits[(da+base)/2]` → `digits[da]`), rapid failed after 2 tests and
  shrank to the minimal reaching sequence — five `append_new`s to walk the
  key up to `"z"`, then one `insert_back` — and named the call:

  ```
  [rapid] failed after 2 tests: Between("z", "") = "z0" ends in the minimum digit
  [rapid] draw action: "append_new"   (×5)
  [rapid] draw action: "insert_back"
  ```

  The hand-rolled LCG stress test already in `rankkey_test.go` catches that
  mutation too, but reports only `key "90" ends in the minimum digit` — the
  symptom, with no call and no path to it. Rapid also prints a replayable
  seed and writes a `.fail` file.

**The finding that justifies the leg on its own.** The growth property
catches a mutation that **every pre-existing test passes**: a `Between` that
writes one extra digit it does not need. Brute force, the LCG stress loop,
and all the example tests go green; rapid fails and shrinks the
counterexample to `Between("", "")` — the simplest input there is. Nothing in
the suite was watching key length, and key length is the whole cost model of
fractional ranking.

Honest limit: the growth property is loose by construction. A mutation that
merely stops taking the single-digit shortcut (`db-da >= 2` → `>= 3`) stays
inside "at most one digit past the longer bound" and is caught by nothing,
including this. A tight bound does not hold in general — repeatedly halving
one gap must grow keys — so this asserts the shape, not the size.

Operational note: on failure rapid writes `internal/<pkg>/testdata/rapid/…`.
Those files are meant to be committed as regression seeds; a developer who
does not want them must delete them.

## Leg 3 — Toxiproxy on the daemon WebSocket: **adopt**

**Rule:** reach for the embedded Toxiproxy when the behavior under test *is*
the network being bad — backpressure, eviction, reconnect, partition. Not for
anything a fake or a direct channel write can express; it costs a real
dependency and real seconds.

`internal/daemon/toxiproxy_slow_client_test.go` runs a proxy in-process (no
external binary, no HTTP control API — the `ApiServer` exists only to satisfy
`NewProxy`'s logger and metrics registry) between one test client and the
daemon's real listener, with a healthy control client on the direct path.

**Evidence**

- The eviction fires over a genuinely throttled TCP link, in under a second,
  through exactly the intended path:
  ```
  hub: WebSocket client slow (1/3 missed)
  hub: WebSocket client slow (2/3 missed)
  hub: WebSocket client too slow (3 missed), disconnecting
  ```
- Stability: **5/5 passes** at ~3.6–4.2s, **3/3 under `-race`** at ~5.1–6.7s
  (a decent stand-in for a loaded CI box).
- Cost: ~4s added to one `internal/daemon` shard of ~70s.

**The surprise, which is the leg's real return.** The eviction is prompt but
*silent to the client that needs it*. `StatusPolicyViolation "client too
slow"` is written to a socket already holding everything the hub queued
before giving up, on a link slow enough to have caused the eviction — so the
close frame arrives behind all of it. The first version of this test waited
45 seconds and saw nothing but its own read timeout, while the daemon had
dropped the client after one second. The test now heals the link and *then*
reads the close status, because that is the only way the status is
observable. An app on a degraded link therefore cannot distinguish "evicted
for being slow" from "the network died" at the moment it matters. Recording
it, not fixing it — a spike does not change production code.

**Second finding: the flood rate is the experiment.** An unpaced hub fan-out
outruns *every* client, degraded or not; the first attempt evicted the
healthy control client too. Reproducing "one bad client, everyone else fine"
requires offering traffic between the two rates, and the constants in the
test carry that arithmetic (10 KB/s link, 4 KB messages, 200 messages/s
offered, 256-slot buffer full in ~1.3s, single write 0.4s against
`wsWritePump`'s 10s allowance).

**The cost to weigh.** `github.com/Shopify/toxiproxy/v2` is not small. It
brings 15 transitive modules — the Prometheus client, `gorilla/mux`,
`zerolog`, `protobuf` — and forced `golang.org/x/sys` and `golang.org/x/text`
upgrades for the whole module. All test-only in use, but they are in `go.mod`
and in every build's module graph. Worth it for a fault-injection capability
we have nowhere else; not worth it for a second copy of something a fake
already does.

## Suite impact

`make test`, whole suite: baseline commit `414dd441` **4:43.79**, this branch
**3:49.91** cold and **2:11.64** warm. The legs do not slow the suite; Leg 1
speeds its package up and Leg 3 adds ~4s to one of five daemon shards.

Two daemon tests failed on the first branch run and neither is ours:

- `TestCodexResumeMappingEndToEnd` — **fails identically on the baseline
  commit**, cold cache. Pre-existing load flake.
- `TestStartJobQueueArmsThePeriodicTicks` — failed once under full-suite
  load, passes 3/3 in isolation and on the re-run. A cron-arming poll,
  load-sensitive. Note that adding a test to `internal/daemon` reshuffles
  every shard (`scripts/test-go.sh` partitions round-robin by index), so a
  new test changes which tests contend with each other.

## Follow-ups earned

- Leg 3's helper is deliberately shaped for reuse: `newToxiProxy(t,
  upstream)` takes any upstream address, so the hub-transport / remote-relay
  leg (zero CI coverage today) is a matter of pointing it at a different
  port.
- Leg 1: convert remaining timing-paced tests opportunistically when they
  flake, not as a sweep. `waitFor` and `fakeClock` stay for the unconverted
  ones.
- Worth its own ticket: whether the app can be told *why* it was disconnected
  on a slow link, given the close frame cannot outrun the backlog.
  **Answered — the eviction now hangs up and explains itself on the client's
  next connection.** Two measurements settled the design. Between two kernels,
  aborting the socket with SO_LINGER 0 reaches the peer immediately: a probe
  wrote 1 MB into a stalled connection, aborted, and the client read the
  400,368 bytes its receive buffer already held and then got ECONNRESET, 0 ms
  after the abort. Through the toxiproxy hop it reaches nobody: the bytes are
  inside the proxy rather than in either kernel, a userspace hop forwards no
  reset, and the client read on for another 65 s and saw a plain EOF. So
  nothing the daemon does can outrun a buffering middlebox — which is why the
  fix is two-part: hang up within a second (`evictionCloseGrace`, after
  offering the close frame), and carry the reason on the next `client_hello`
  as `client_eviction_notice`, keyed on a client id the app repeats across
  reconnects. The leg's test keeps the eviction, the healthy control, and the
  recovery, and now pins the notice arriving over the same degraded link;
  `TestEvictedClientIsNotFedItsBacklogFirst` covers the kernel-to-kernel case
  the proxy cannot express.

  A third measurement, from the live app rather than a test, moved where the
  fix had to go. The hub's slow-count rule is not how a real client dies. With
  the app's socket frozen and 400 sessions in the store, one snapshot was
  enough: the write pump sat on its 10 s deadline with 409,117 bytes stuck in
  the client's receive queue and 145,951 more queued on the daemon's side, and
  the hub's slow-count never reached 2. The connection then ended through the
  write pump, which knew nothing about evictions and filed nothing — so the app
  reconnected to a daemon with no answer for it. Both exits now file the same
  record. The transport needs no extra help on that path: the library tears the
  connection down when its own write deadline expires, confirmed by mutation
  (`TestWriteStallEndsTheConnectionAndIsRemembered` still sees the connection
  end with the abort removed).

  The keepalive turned out to be the exit that fires in practice. With the
  app's socket frozen, the unanswered ping beat both the slow-count and the
  write deadline to it in every live run — and that path did two wrong things.
  It filed nothing, so a client that reconnected got no answer; and it closed
  through the WebSocket close handshake, which waits five seconds for a peer
  that has already stopped answering (live: ping failed 20:22:32, socket gone
  20:22:37; in a test with the keepalive shrunk, 5.4s versus 1.4s once it hangs
  up instead). It now hangs up like the hub does, and files the disconnect —
  but only when the daemon is still holding messages for that client. An
  unanswered ping with nothing owed is a connection that died, not a client
  that fell behind, and a laptop coming out of sleep must not be told it was
  too slow.
