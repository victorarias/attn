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

- [ ] Leg 1: convert 2–3 jobs runner/cron tests to `synctest.Test`; remove
      their real sleeps/deadline polls; note speed and determinism delta
- [ ] Leg 2: add rapid; write stateful `Between` property test in
      `internal/rankkey`; confirm shrinking output quality with a seeded
      forced failure (then remove the forced failure)
- [ ] Leg 3: add embedded toxiproxy helper + slow-client eviction test in
      `internal/daemon`; healthy-client control and post-toxic recovery
      included
- [ ] Run the full Go suite (`make test`) — legs must not slow it noticeably;
      note per-leg wall-clock cost
- [ ] Append a Findings section to this plan: per leg, verdict
      (adopt/drop), evidence, and — for adopts — the one-line rule for when
      future work should reach for the tool
- [ ] Changelog fragment (internal/testing entry) per repo policy

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
