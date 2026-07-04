# attn performance-test strategy — a proposed baseline (proposal, no implementation)

Status: **proposal for discussion.** No tests are built here. This doc argues
*which* performance tests attn should keep, *why*, and *where each can actually
run* — then names a minimal starter set and a fuller tier. Implementation is a
separate decision after we agree on the shape.

Scope: **performance = CPU (latency/throughput) AND memory** (resident
footprint, allocation churn, long-session growth/leaks). Memory is first-class,
not an afterthought — for attn it is arguably the *bigger* axis, because the app
is meant to run for days with many sessions open.

Companion doc: [`2026-06-08-performance-memory-cpu.md`](2026-06-08-performance-memory-cpu.md)
is the *optimization* roadmap (what to make faster). This doc is the *test*
strategy (how we keep it from silently regressing). They share the same map of
hot surfaces and the same measurement tooling.

---

## TL;DR — the recommendation

1. **Start with 3 Go micro-benchmarks that gate on _allocations_, not time.**
   Allocations are deterministic even on noisy shared CI runners; wall-clock is
   not. These are the only perf tests that can safely fail a PR in GitHub CI.
2. **Adopt the existing real-app harness as a _tracked trend_, not a gate.**
   `scenario-perf-baseline.mjs` already measures RSS, the warm-set per-pane cost,
   and the streaming memory balloon. It cannot run in GitHub CI (no GPU, no
   packaged app) — so run it locally/nightly on a Mac and eyeball the numbers.
3. **Add one leak-soak on top of the soak driver (#470)** to catch long-session
   growth — the failure mode users actually hit — measuring *retained-after-
   teardown*, not peak.

Everything else (store benchmarks, classifier-slice benchmark, frontend JS-heap
sampling, spawn-latency timing) is the fuller tier: real but lower
value-per-maintenance-cost. Details and ranking below.

The single most important idea in this doc: **gate on the deterministic thing,
trend on the noisy thing.** Get that split wrong and you either ship a flaky CI
that everyone learns to ignore, or a green suite that never catches a real
regression.

---

## The mental model

Two things vary independently and need different tools:

**Axis 1 — what you measure**

| | CPU | Memory |
| --- | --- | --- |
| **What breaks** | latency spikes, throughput cliffs, CPU burned while idle | per-session footprint × N sessions, allocation churn (GC pressure), one-way growth / leaks over days |
| **Go tooling** | `testing.B` ns/op, `pprof` CPU profile | `-benchmem` (B/op, allocs/op), `pprof` heap profile, `runtime.MemStats` via the `/debug/vars` endpoint |
| **Frontend tooling** | `performance.now()` timing; CPU pprof is Go-only | JS heap sampling (CDP / `performance.memory`), but the real signal is process RSS |
| **Macro tooling** | wall-clock of a scenario | `ps`/`vmmap` process-tree RSS (already in the harness) |

**Axis 2 — the altitude you test at** (this is the classic micro-vs-macro split)

- **Micro (Go `testing.B`)** — one function in isolation. Precise, cheap, fast,
  and *allocation counts are near-deterministic*. Weakness: measures a function,
  not a user experience; easy to benchmark something that doesn't matter.
- **Macro (real-app harness)** — the whole packaged app driven like a user.
  Realistic, catches emergent/cross-layer costs (the WASM balloon, per-pane
  frontend RSS). Weakness: noisy, slow, single-tenant (**never parallelize** —
  see the harness policy), and **cannot run in GitHub CI**.

You want both, at the right altitude for each risk, gated or trended per its
noise profile. A perf suite that is all-micro misses the memory story (which
lives in the frontend/macro layer); all-macro is too noisy to gate and too slow
to run per-PR.

---

## The craft (read this before the test list)

Victor asked for the honest craft, not a list. Here's what actually bites.

### 1. attn's CI reality decides everything

CI (`.github/workflows/ci.yml`) is **GitHub-hosted `ubuntu-latest` shared
runners** — plus one `macos-14` only in release-preflight. Consequences:

- **No GPU, no WindowServer, no packaged/signed macOS `.app`.** The real-app
  harness (Ghostty WebGL, WKWebView, native window capture) **physically cannot
  run in GitHub CI.** Any macro RSS/memory-balloon test is a **local / nightly /
  self-hosted-Mac** artifact, full stop. Pretending otherwise is how you get a
  perf "gate" that's actually disabled.
- **Shared runners are CPU-noisy.** ns/op swings run-to-run (neighbours on the
  box, frequency scaling). A wall-clock threshold on a shared runner is a
  coin-flip → flaky red → ignored suite. This is *the* classic perf-CI trap.
- **`go test ./...` compiles benchmarks but does not run them** (needs
  `-bench`). So Go benchmarks already build in CI for free (a compile check),
  and turning them into a gate is a small, contained workflow addition — not new
  infrastructure.

**Design rule that falls out of this:** the only perf assertion that belongs in
GitHub CI is one whose measured quantity is *deterministic on a noisy box*. That
is **allocations/op** (and, weakly, B/op) — not ns/op, not RSS.

### 2. Gates vs trends — pick per-metric, not per-test

- A **gate** fails the build. It must be deterministic and have a *generous*
  threshold (catch a 2× regression, not a 5% wobble). Only `allocs/op` qualifies
  cleanly for attn's CI.
- A **trend** is recorded and looked at by a human (or a nightly alert on a big
  delta). ns/op, throughput MiB/s, and RSS are trends. They're *diagnostic and
  valuable* — they just must not block a PR on a shared runner.

The mistake is treating these as one knob. "Add a perf test" almost always means
"add a trend + gate the one deterministic sub-metric of it."

### 3. Allocations are the deterministic CPU-and-memory proxy

`-benchmem` reports `B/op` and `allocs/op`. `allocs/op` is essentially
input-deterministic — it doesn't care how busy the runner is. It's a great proxy
for *both* axes: fewer allocations = less GC pressure (memory) and less
per-operation work (CPU). The WS-4 regression that this repo already fought (an
un-gated `logf` doing a `string(data)` + preview + disk write on every PTY
chunk) shows up cleanly as `allocs/op` on the datapath and is invisible to a
noisy ns/op number. **Gate `allocs/op`; benchstat the ns/op as a trend.**

`benchstat` (the standard Go tool) takes N runs of old vs new and reports the
delta with a confidence interval, so it separates signal from noise even on a
shared runner — but it needs multiple runs and a baseline, which is more suited
to a nightly/manual "did this PR change anything" check than a hard gate.

### 4. Fixture realism or don't bother

A transcript-parser benchmark over a 3-line file proves nothing — the whole cost
is the O(file-size) scan of real transcripts (~23 MiB Claude / ~6.8 MiB Codex
per the roadmap). A benchmark's fixture must be *representative in the dimension
that drives cost*: file size for the parser, chunk size + client count for the
PTY path, session count for macro RSS. Commit a realistic (possibly synthetic-
but-large) fixture, or the benchmark is theater. Corollary: keep the fixture
*stable* — regenerating it every run reintroduces noise and destroys
comparability.

### 5. Memory tests measure the RIGHT number, and it's usually "retained"

The roadmap already learned this the hard way: streaming output balloons
WebContent 250 MB → 1148 MB **and does not recover** because WASM linear memory
is one-way; only pane teardown reclaims it. So:

- **Peak RSS** is a weak signal (transient, allocator-dependent, recovers).
- **Retained RSS after the workload ends and panes tear down** is the real
  leak/growth signal. The harness's `--reclaim-hold-ms` decay sampler exists
  exactly to tell "soft-but-delayed reclaim" from "hard retention."
- On macOS the OS scavenges lazily; a single post-workload snapshot can read a
  false leak. Sample a decay window before concluding.

Leak detection = **run the same operation many times and watch the slope of
retained memory**, not a single before/after. That's what the soak driver
(#470) enables at the macro layer, and what a Go heap-growth test does at the
micro layer.

### 6. What is NOT worth measuring (so we don't waste maintenance)

- **ns/op as a CI gate.** Noise. Trend only.
- **Daemon steady-state heap.** Measured at 27 MB RSS / ~6 MB heap — there's
  nothing there to protect. (A benchmark of daemon *allocation churn* on a hot
  path is still worth it; the steady-state floor is not.)
- **PTY worker scrollback RSS.** Established as the Go-runtime floor (~14 MB),
  not scrollback. Don't write a test guarding a number that's already bottomed
  out.
- **Trivial pure functions** the compiler already covers, or micro-benchmarks of
  code that isn't on a hot path. Value-per-maintenance ≈ 0.
- **Frontend render timing in jsdom/happy-dom or headless Chromium.** It does not
  model Ghostty WebGL/WASM; a green number there is meaningless for the real app.
  Frontend perf that matters is macro-RSS only.
- **Micro-benchmarking anything whose cost is dominated by a subprocess or the
  model** (e.g. classifier end-to-end — cost is `claude -p`, not our code). Test
  the deterministic slice we own (the transcript extraction), not the LLM call.

---

## Proposed tests — ranked by value-per-maintenance-cost

Rank = (regression risk it covers) ÷ (cost to build + flakiness + upkeep).
"Where" is the hard constraint from §1.

| # | Test | Layer | Axis | Where it runs | Gate or trend | Rank |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | PTY output datapath allocations | Go micro | CPU+mem | **GitHub CI** | **gate** allocs/op | ★★★★★ |
| 2 | Transcript parser over a real-size fixture | Go micro | CPU+mem | **GitHub CI** | **gate** allocs/op + trend ns/op | ★★★★★ |
| 3 | Real-app RSS baseline (idle @ N sessions + per-pane) | Macro | Memory | Local / nightly Mac | trend | ★★★★☆ |
| 4 | Streaming/long-session leak soak (retained RSS) | Macro | Memory | Local / nightly Mac | trend + soft verdict | ★★★★☆ |
| 5 | WS outbound event marshal allocations | Go micro | CPU+mem | **GitHub CI** | **gate** allocs/op | ★★★☆☆ |
| 6 | Store hot-path query benchmarks (List/Get) | Go micro | CPU+mem | GitHub CI | gate allocs/op | ★★★☆☆ |
| 7 | Classifier deterministic-slice extraction | Go micro | CPU+mem | GitHub CI | gate allocs/op | ★★☆☆☆ |
| 8 | Session spawn / reattach latency | Macro | CPU | Local / nightly Mac | trend | ★★☆☆☆ |
| 9 | Frontend retained JS-heap after teardown | Macro | Memory | Local / nightly Mac | trend | ★★☆☆☆ |

### 1. PTY output datapath allocations (Go micro, GATE) — ★★★★★

- **What it protects:** the single hottest path in attn — one event per output
  chunk, per attached client, forwarded through the daemon/worker. A regression
  here taxes *every* streaming session continuously.
- **What "good" looks like:** with debug logging off, forwarding a chunk costs a
  small, fixed `allocs/op` (ideally the base64 wire payload and little else). The
  gate asserts `allocs/op` doesn't rise above a committed ceiling.
- **The trap it avoids:** exactly the WS-4 regression — an un-gated `logf`
  silently reintroduced, doing `string(data)` + preview + a mutex-held disk
  write per chunk. Invisible in ns/op noise; obvious in `allocs/op`.
- **Starting point already exists:** `internal/daemon/ws_pty_logging_bench_test.go`
  benchmarks the log line. Extend it (or add a sibling) to cover the *forward*
  path end-to-end (marshal + gate), and wire an `allocs/op` assertion.
- **Cost/upkeep:** low. Deterministic, fast, already partly built.

### 2. Transcript parser over a real-size fixture (Go micro, GATE + trend) — ★★★★★

- **What it protects:** classification runs once per assistant turn per session
  and today re-reads the *entire* transcript from offset 0, several unmarshals
  per line (`internal/transcript/parser.go`). This is the biggest per-turn CPU/
  allocation cost we fully own.
- **What "good" looks like:** parsing a large fixture allocates roughly in
  proportion to *what it returns* (last assistant/user turn), not to whole-file
  size — i.e. it proves the tail-read win (WS-6) if/when we do it, and guards it
  after. Gate `allocs/op` and `B/op`; trend ns/op via benchstat.
- **The trap it avoids:** (a) a toy fixture that hides the O(file-size) cost —
  commit a realistic multi-MB transcript; (b) gating ns/op and getting flaky red
  on shared CI.
- **Cost/upkeep:** low-medium. One committed fixture (keep it stable), one
  benchmark. High leverage because this path is on every turn.

### 3. Real-app RSS baseline (macro, TREND) — ★★★★☆

- **What it protects:** attn's own per-session and idle memory footprint — the
  thing that multiplies across many sessions over a multi-day run. This is the
  memory story, and it lives in the frontend, which no Go benchmark can see.
- **What "good" looks like:** idle RSS at a fixed N sessions stays within a band
  of the last known-good number; the per-live-pane slope (warm-set A/B) doesn't
  climb. The harness already computes all of this
  (`scenario-perf-baseline.mjs`: `headline.totalRssMb`, `warmSweep.perLivePane*`).
- **The trap it avoids:** treating a noisy, single-tenant macro number as a
  per-PR gate. It's a **trend**: run it on a Mac (dev or a self-hosted runner),
  record the headline JSON, alert only on a large delta. Also: it must sum the
  *whole process tree* (daemon + one worker per session + reparented WebKit
  PIDs) or the frontend win/regression is invisible — the harness already does
  this correctly.
- **Cost/upkeep:** low to *adopt* (the scenario exists and is maintained);
  medium if we want automated nightly capture + delta alerting. The
  recommendation is to start with "documented manual run on risky PRs + a
  recorded baseline number," not a cron.

### 4. Streaming / long-session leak soak (macro, TREND + soft verdict) — ★★★★☆

- **What it protects:** the failure users actually feel — memory that climbs
  over hours/days and never comes back (the WASM balloon; any per-operation
  frontend leak). "Regressions noticed by feel" are mostly *this*.
- **What "good" looks like:** after streaming a heavy workload and tearing panes
  down, **retained** RSS returns near the idle baseline (within a band); over an
  N-iteration soak, retained RSS has a *flat* slope, not a staircase.
- **How:** build on the soak driver (`run-soak.mjs`, #470) + the baseline
  scenario's `--real-cmd` / `--reclaim-hold-ms` decay sampler. Emit an
  `ATTN_VERDICT` with `ok: false` when the retained-RSS slope exceeds a
  threshold, so the soak run self-reports. Measure retained-after-teardown and a
  decay window — never peak (§5).
- **The trap it avoids:** (a) asserting on peak RSS (transient, one-way
  allocator makes it meaningless); (b) too-tight a threshold on a lazily-
  scavenged OS → false leak. Use a generous band + a decay hold.
- **Cost/upkeep:** medium. Highest-*value* memory test we could add, but it's
  macro (Mac-only, slow), so it's a nightly/manual soak, not a gate.

### 5. WS outbound event marshal allocations (Go micro, GATE) — ★★★☆☆

- **What it protects:** the broadcast fan-out path. Today `EventPtyOutput` is
  marshalled through the fat ~71-field `WebSocketEvent` per chunk (roadmap
  ws-3). Allocation regressions here scale with clients × chunks.
- **What "good" looks like:** stable, low `allocs/op` per broadcast event; if
  ws-3 (a purpose-built 4-field struct) ever lands, this benchmark proves and
  then guards the win.
- **The trap it avoids:** a fat-struct marshal creeping onto the hot outbound
  path unnoticed.
- **Cost/upkeep:** low. Straightforward `testing.B` over the marshal.

### 6. Store hot-path query benchmarks (Go micro, GATE) — ★★★☆☆

- **What it protects:** SQLite-backed `store.List`/`Get` on paths like
  `handleTodos` (which today does `List("")` + linear scan for one id, roadmap
  store-2). Allocation-heavy queries on frequent paths add up.
- **What "good" looks like:** bounded `allocs/op` for a `Get`; `List` scaling
  linearly and not re-allocating per call.
- **The trap it avoids:** an N+1 or full-scan pattern regressing a frequent
  daemon path.
- **Cost/upkeep:** low-medium (needs a seeded in-memory DB fixture). Modest value
  — the store is not currently a headline cost — so tier-2.

### 7. Classifier deterministic-slice extraction (Go micro, GATE) — ★★☆☆☆

- **What it protects:** the just-landed reconcile change (deterministic
  transcript slice fed to one cheap model call). Protects the *slice extraction*
  we own — **not** the LLM call (that cost is `claude -p`, untestable as a
  benchmark, §6).
- **What "good" looks like:** slice extraction over a large transcript is cheap
  and bounded; it's essentially a specialized case of test #2.
- **Cost/upkeep:** low, but overlaps test #2's fixture and coverage — build only
  if the slice logic diverges enough to warrant its own guard.

### 8. Session spawn / reattach latency (macro, TREND) — ★★☆☆☆

- **What it protects:** perceived snappiness of opening/switching sessions.
- **What "good" looks like:** spawn-to-first-pane and reattach-replay wall-clock
  stay in a band. Pure trend — wall-clock, Mac-only, noisy.
- **Cost/upkeep:** medium; the harness can already time these. Lower priority
  than the memory work because latency regressions are more self-evident in use
  than slow memory creep.

### 9. Frontend retained JS-heap after teardown (macro, TREND) — ★★☆☆☆

- **What it protects:** JS-side (non-WASM) leaks — detached DOM, listener/store
  accumulation across session churn.
- **What "good" looks like:** JS heap returns to baseline after closing sessions.
- **The trap it avoids:** conflating JS heap with WASM linear memory (different
  allocators; the balloon is WASM, not JS heap). Needs CDP/`performance.memory`
  sampling wired into the harness.
- **Cost/upkeep:** medium-high (new sampling plumbing), narrower coverage than
  the process-RSS soak (#4) which already catches the dominant case. Tier-2.

---

## Recommended starter set vs fuller tier

**Starter set (do first — small, high-leverage, mostly CI-gateable):**

- **#1 PTY datapath allocations** (extend the existing bench → `allocs/op` gate)
- **#2 Transcript parser over a real fixture** (`allocs/op` gate + ns/op trend)
- **#3 Real-app RSS baseline** adopted as a *documented manual/nightly trend*
  (the scenario already exists — this is mostly "record a baseline + a runbook")
- *(optional 4th, if we want the memory-leak guard now)* **#4 leak soak** on the
  #470 driver with a retained-RSS verdict.

That's 3 tests + 1 adopted scenario, of which 2 are true CI gates and the rest
are trends. It covers the two hottest owned CPU/alloc paths and the dominant
memory failure mode, at low upkeep.

The enabling infra the starter set needs:
- a tiny CI step that runs `go test -run=X -bench=. -benchmem` on the perf
  packages and fails on an `allocs/op` ceiling (a `benchcmp`/threshold check, or
  a hand-rolled assertion inside the benchmark);
- one committed realistic transcript fixture (stable);
- a short runbook: "before merging a memory-risky PR, run
  `pnpm run real-app:scenario-perf-baseline` on dev and compare the headline to
  the recorded baseline."

**Fuller tier (add as the surfaces prove worth guarding):** #5 WS marshal, #6
store queries, #7 classifier slice, #8 spawn latency, #9 JS-heap. Each is a
real-but-narrower guard; add when that surface is touched or has burned us.

**Explicitly out of scope** (don't build): ns/op CI gates, daemon steady-state
heap guards, worker-scrollback RSS guards, jsdom/headless render-timing, and any
end-to-end classifier *timing* benchmark.

---

## Open forks to decide (recommendation first)

These are genuine decisions that change what gets built — I'll bring them to you
rather than assume:

1. **Gate strictness.** Recommend: allocs/op hard-gate on the Go micro-benches;
   ns/op and RSS as trends only. Alternative: also gate a *very generous* ns/op
   ceiling (catches a 3×+ blowup, tolerates noise) — more coverage, some flake
   risk.
2. **Where macro RSS/soak lives.** Recommend: developer-run + recorded baseline
   now (zero infra), because there's no self-hosted Mac runner and GitHub CI
   can't host it. Alternative: invest in a self-hosted Mac + nightly cron with
   delta alerting (more coverage, real infra cost).
3. **Starter scope.** Recommend: the 3 + optional soak above. Alternative:
   ship only the 2 CI-gateable Go benches first (absolute minimum), add macro
   later.

---

## Overlap / collision note

This is docs-only (`docs/plans/`), so it shouldn't collide with the active
Present or agent-cost-tools sessions. It *references* but does not modify the
real-app harness and the #467/#470 verdict/soak tooling; if either of those
sessions is also reshaping the harness, the only coupling is that test #4 would
build on `run-soak.mjs`'s verdict contract. Flagging for awareness — no code
conflict expected.
