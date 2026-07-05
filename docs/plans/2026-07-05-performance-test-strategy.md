# attn performance-test strategy — a proposed baseline (proposal, no implementation)

Status: **proposal — direction agreed, not yet built.** No tests are built here.
This doc argues *which* performance tests attn should keep, *why*, and *where
each can actually run* — then names a starter set and a fuller tier.
Implementation is a separate PR.

### Decisions (2026-07-05, with Victor)

- **Trends only, no hard gates (for now).** Nothing fails a PR on a perf number.
  All perf metrics are recorded and reviewed; we can promote the most
  deterministic one (`allocs/op`) to a gate later if trend-watching proves too
  passive. This sidesteps flaky-perf-CI entirely at the cost of relying on
  someone reading the trend.
- **Macro RSS/leak-soak = dev-Mac run + recorded baseline.** Confirmed by infra:
  Victor's self-hosted runners (savannah) are **Linux x64 VMs — there is no Mac
  in the fleet**, and GitHub-hosted CI has no GPU/packaged app. So the packaged-
  app harness stays a developer-run/nightly-on-a-Mac artifact with a committed
  baseline; no runner can host it.
- **Starter set includes the leak soak.** The retained-RSS soak (the "noticed by
  feel" long-session memory creep) is in the first implementation batch, not
  deferred.
- **Where the Go benches run → GitHub-hosted shared runners (for now).** The
  self-hosted savannah runners are org-scoped to `solenesinc`; using them would
  mean **moving `victorarias/attn` into that org**, which is out of scope for
  now — **postponed.** So the Go benchmark job runs on the same shared
  `ubuntu-latest` runners as the rest of CI. `allocs/op`/`B/op` are deterministic
  regardless; see the next bullet for how we make ns/op honest on a noisy box.
- **Beat cross-machine noise two ways, per layer.** The noise in ns/op and RSS is
  *cross-machine* variance; a machine is consistent with itself. So:
  - **Go micro-benches → same-machine A/B, no registry.** Run `main` and the PR
    head back-to-back *in the same CI job on the same runner* and `benchstat` the
    delta. Both revisions share the identical box, so absolute noise cancels and
    the delta is meaningful — this makes even ns/op a usable signal on shared
    runners. `allocs/op` stays the primary trend; ns/op-delta is now a real
    secondary signal instead of noise.
  - **Macro RSS/soak → a per-machine timings registry.** You can't clean-A/B a
    slow, single-tenant macro run, and you want long-term *drift*, not just
    PR-vs-base. Key baselines by a machine fingerprint (`hw.model` + CPU brand +
    cores + RAM + OS + arch) and compare each run to *that machine's* stored
    baseline within a band. Any dev self-baselines instead of chasing "Victor's
    number."
  - **Registry storage → local cache + committed canonical baselines.** Each
    machine writes its own baseline to a gitignored local cache (no repo churn);
    a small set of known reference machines have their baseline *committed* for
    review. (Chosen over both all-in-repo JSON — merge/churn — and victor-cloud
    object storage — infra to wire; the latter stays a later option if we want
    central drift dashboards.)
  - Moving attn to `solenesinc` (dedicated savannah runners) remains the
    postponed upgrade that would make raw ns/op reliable and unlock a
    promote-to-gate story — non-blocking.

Scope: **performance = CPU (latency/throughput) AND memory** (resident
footprint, allocation churn, long-session growth/leaks). Memory is first-class,
not an afterthought — for attn it is arguably the *bigger* axis, because the app
is meant to run for days with many sessions open.

Companion doc: [`2026-06-08-performance-memory-cpu.md`](2026-06-08-performance-memory-cpu.md)
is the *optimization* roadmap (what to make faster). This doc is the *test*
strategy (how we keep it from silently regressing). They share the same map of
hot surfaces and the same measurement tooling.

---

## TL;DR — the plan

1. **3 Go micro-benchmarks, run same-machine A/B.** PTY output datapath,
   transcript parser, WS event marshal. In one CI job, benchmark `main` and the
   PR head on the same runner and `benchstat` the delta — absolute noise cancels,
   so `allocs/op`/`B/op` (deterministic) *and* the ns/op-delta are usable even on
   shared runners. Trend-only to start; `allocs/op` is the promote-to-gate
   candidate. No cross-machine registry needed here.
2. **Adopt the existing real-app harness as a Mac-only trend, backed by a
   per-machine registry.** `scenario-perf-baseline.mjs` already measures RSS, the
   warm-set per-pane cost, and the streaming balloon. It can't run on any runner
   we have (no GPU/packaged app; the fleet is Linux) — so it's a developer-run /
   nightly-Mac artifact that compares each run to *that machine's* stored baseline
   (fingerprint-keyed), not a single absolute number.
3. **Add the leak soak** on top of the soak driver (#470) to catch long-session
   growth — the failure users actually hit — measuring *retained-after-teardown*,
   not peak.

Everything else (store benchmarks, classifier-slice benchmark, frontend JS-heap
sampling, spawn-latency timing) is the fuller tier: real but lower
value-per-maintenance-cost. Details and ranking below.

The single most important idea in this doc: **trust the deterministic number,
be skeptical of the noisy one.** `allocs/op` is input-deterministic; ns/op and
RSS are noisy and must be read as trends with generous bands. Confuse the two
and you either chase phantom regressions or miss real ones.

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

The runner we have, and the one we don't:

- **GitHub-hosted `ubuntu-latest` shared runners** run today's CI
  (`.github/workflows/ci.yml`) — plus one `macos-14` only in release-preflight.
  This is where the Go benchmark job will live. Shared runners are **CPU-noisy**:
  a given box's *absolute* ns/op swings run-to-run (neighbours, frequency
  scaling), so an absolute wall-clock threshold is a coin-flip → flaky red →
  ignored suite (*the* classic perf-CI trap). `allocs/op`/`B/op` are deterministic
  and immune. **The fix for ns/op is same-machine A/B** (§2): benchmark base and
  PR in the *same job on the same box* and compare the delta — absolute noise
  cancels, so the delta is usable even here (next subsection). No absolute-timing
  gate on a shared runner, ever.
- **Self-hosted savannah runners** exist (`victor-cloud`:
  `savannah-github-runners-*`): 4 ephemeral **Linux x64** VMs, 8 vCPU / 32 GB,
  dedicated and network-isolated — a *quiet* box where even ns/op would be a
  meaningful trend. **But they're org-scoped to `solenesinc`, and using them
  would require moving `victorarias/attn` into that org — postponed for now.**
  Noted as the upgrade path that would later make ns/op reliable and unlock a
  promote-to-gate story; not part of this plan.

And the hard wall for the memory story:

- **No Mac in any runner fleet, and no GPU/WindowServer/packaged `.app`
  anywhere in CI.** The real-app harness (Ghostty WebGL, WKWebView, native
  window capture) **physically cannot run on a runner we have.** Every macro
  RSS/memory-balloon/soak test is a **developer-run / nightly-on-a-real-Mac**
  artifact, full stop. Pretending otherwise is how you get a perf "gate" that's
  silently disabled.

Also useful: **`go test ./...` compiles benchmarks but does not run them** (needs
`-bench`). So Go benchmarks already build in CI for free (a compile check), and
adding a trend/gate job is a small, contained workflow addition — not new infra.

**Design rule that falls out of this:** the deterministic quantity
(`allocs/op`, and weakly `B/op`) is trustworthy on *any* runner and is the
primary signal. For the machine-dependent metrics, don't compare absolutes
across machines — compare *deltas on the same machine*: same-job A/B for CI
timing, a per-machine baseline for macro RSS. An absolute ns/op or RSS number
from one box is never a decision input on another.

### 2. Killing cross-machine noise — same-machine A/B and a per-machine registry

The noise in ns/op and RSS is almost entirely **cross-machine** variance; a
given machine is fairly consistent with itself. That gives two clean fixes, one
per layer — and note **`allocs/op` needs neither** (it's machine-independent, so
a single committed baseline covers every box).

**(a) Go micro-benches → same-machine A/B, no registry.** In one CI job, check
out and benchmark `main`, then the PR head, on the *same* ephemeral runner, and
`benchstat` the two. Because both revisions ran on the identical box in one
session, the absolute noise cancels and the *delta* is meaningful — this makes
even ns/op usable on a shared runner. It's the standard continuous-benchmark
move: cheap (2× bench time), no fingerprinting, no persistence, no
re-baselining. It answers "did *this PR* change it," which is exactly the
per-PR question.

**(b) Macro RSS/soak → a per-machine timings registry.** You can't clean-A/B a
macro run — it's slow, single-tenant, and the question is long-term *drift*
("has idle RSS crept up on this Mac over months"), not just PR-vs-base. So
persist a baseline keyed by a **machine fingerprint** (`hw.model` + CPU brand +
core count + RAM + OS version + arch) and compare each run to *that machine's*
entry within a generous band. Every entry carries `{metric, value, band, commit,
date}`. The win: any developer (or you on a second Mac) self-baselines instead
of comparing against one person's absolute numbers.

**Storage: local cache + committed canonical baselines.** Each machine writes
its own entry to a **gitignored local cache** (zero repo churn, no merge
conflicts when many machines write); a small set of **known reference machines**
have their baseline **committed** to the repo for review and as the shared
reference point. Rejected alternatives: all-baselines-in-repo JSON (commit churn
+ conflicts) and victor-cloud object storage (infra to wire) — the latter stays
a later option if we ever want a central cross-machine drift dashboard.

**Registry traps to design around:**
- **First run is record-only.** A machine with no entry can't compare; it seeds
  its baseline and compares on subsequent runs.
- **Re-baseline on hardware/OS change.** A fingerprint change (new Mac, OS
  upgrade) invalidates the old entry — needs an explicit `--rebaseline` path, not
  a silent stale compare.
- **Laptop thermal throttling is real within-machine noise.** Bands must absorb
  it; a plugged-in, thermally-settled run is the honest baseline. Sample a window
  (the harness already supports `--reclaim-hold-ms`) rather than one spot read.
- **The registry is for *drift*, not a hard gate.** Same trend-only stance: it
  flags "this machine drifted past its band," a human looks; it never auto-fails.

### 3. Gates vs trends — pick per-metric, not per-test

- A **gate** fails the build. It must be deterministic and have a *generous*
  threshold (catch a 2× regression, not a 5% wobble). Only `allocs/op` qualifies
  cleanly for attn's CI.
- A **trend** is recorded and looked at by a human (or a nightly alert on a big
  delta). ns/op, throughput MiB/s, and RSS are trends. They're *diagnostic and
  valuable* — they just must not block a PR on a shared runner.

The mistake is treating these as one knob. "Add a perf test" almost always means
"add a trend + (optionally) gate the one deterministic sub-metric of it."

**Decision for attn: trend-only to start.** Nothing fails a PR yet. We record
every metric and watch it; `allocs/op` is the candidate we'd promote to a hard
gate first if passive trend-watching lets a regression slip through. Starting
trend-only means the perf suite can never be the reason a good PR goes red —
which is the right way to earn trust in it before giving it a veto.

### 4. Allocations are the deterministic CPU-and-memory proxy

`-benchmem` reports `B/op` and `allocs/op`. `allocs/op` is essentially
input-deterministic — it doesn't care how busy the runner is. It's a great proxy
for *both* axes: fewer allocations = less GC pressure (memory) and less
per-operation work (CPU). The WS-4 regression that this repo already fought (an
un-gated `logf` doing a `string(data)` + preview + disk write on every PTY
chunk) shows up cleanly as `allocs/op` on the datapath and is invisible to a
noisy ns/op number. **Track `allocs/op` as the trustworthy signal; benchstat the ns/op as a secondary trend** (promote `allocs/op` to a gate later if desired).

`benchstat` (the standard Go tool) takes N runs of old vs new and reports the
delta with a confidence interval, so it separates signal from noise even on a
shared runner — but it needs multiple runs and a baseline, which is more suited
to a nightly/manual "did this PR change anything" check than a hard gate.

### 5. Fixture realism or don't bother

A transcript-parser benchmark over a 3-line file proves nothing — the whole cost
is the O(file-size) scan of real transcripts (~23 MiB Claude / ~6.8 MiB Codex
per the roadmap). A benchmark's fixture must be *representative in the dimension
that drives cost*: file size for the parser, chunk size + client count for the
PTY path, session count for macro RSS. Commit a realistic (possibly synthetic-
but-large) fixture, or the benchmark is theater. Corollary: keep the fixture
*stable* — regenerating it every run reintroduces noise and destroys
comparability.

### 6. Memory tests measure the RIGHT number, and it's usually "retained"

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

### 7. What is NOT worth measuring (so we don't waste maintenance)

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

"Signal" = the metric to watch (all trend-only for now; `allocs/op` is the
promote-to-gate candidate). "Where" is the hard runner constraint from §1.

| # | Test | Layer | Axis | Where it runs | Signal (trend) | Rank |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | PTY output datapath allocations | Go micro | CPU+mem | GitHub CI (shared, A/B) | allocs/op + ns/op-delta | ★★★★★ |
| 2 | Transcript parser over a real-size fixture | Go micro | CPU+mem | GitHub CI (shared, A/B) | allocs/op, B/op + ns/op-delta | ★★★★★ |
| 3 | Real-app RSS baseline (idle @ N sessions + per-pane) | Macro | Memory | Dev / nightly Mac (registry) | RSS, per-live-pane slope | ★★★★☆ |
| 4 | Streaming/long-session leak soak (retained RSS) | Macro | Memory | Dev / nightly Mac (registry) | retained RSS slope | ★★★★☆ |
| 5 | WS outbound event marshal allocations | Go micro | CPU+mem | GitHub CI (shared) | allocs/op | ★★★☆☆ |
| 6 | Store hot-path query benchmarks (List/Get) | Go micro | CPU+mem | GitHub CI (shared) | allocs/op | ★★★☆☆ |
| 7 | Classifier deterministic-slice extraction | Go micro | CPU+mem | GitHub CI (shared) | allocs/op, B/op | ★★☆☆☆ |
| 8 | Session spawn / reattach latency | Macro | CPU | Dev / nightly Mac | wall-clock | ★★☆☆☆ |
| 9 | Frontend retained JS-heap after teardown | Macro | Memory | Dev / nightly Mac | JS heap | ★★☆☆☆ |

Tests #1, #2, #4 are the starter set (see below); #1–#2 gate-promotable later.

### 1. PTY output datapath allocations (Go micro, trend; gate-promotable) — ★★★★★

- **What it protects:** the single hottest path in attn — one event per output
  chunk, per attached client, forwarded through the daemon/worker. A regression
  here taxes *every* streaming session continuously.
- **What "good" looks like:** with debug logging off, forwarding a chunk costs a
  small, fixed `allocs/op` (ideally the base64 wire payload and little else). The
  primary trend watches `allocs/op` against a committed baseline (and is the first
  candidate to promote to a hard ceiling); the same-machine A/B (§2) also yields a
  usable ns/op-delta as a secondary signal.
- **The trap it avoids:** exactly the WS-4 regression — an un-gated `logf`
  silently reintroduced, doing `string(data)` + preview + a mutex-held disk
  write per chunk. Invisible in raw cross-machine ns/op; obvious in `allocs/op`.
- **Starting point already exists:** `internal/daemon/ws_pty_logging_bench_test.go`
  benchmarks the log line. Extend it (or add a sibling) to cover the *forward*
  path end-to-end (marshal + gate), and wire an `allocs/op` assertion.
- **Cost/upkeep:** low. Deterministic, fast, already partly built.

### 2. Transcript parser over a real-size fixture (Go micro, trend; gate-promotable) — ★★★★★

- **What it protects:** classification runs once per assistant turn per session
  and today re-reads the *entire* transcript from offset 0, several unmarshals
  per line (`internal/transcript/parser.go`). This is the biggest per-turn CPU/
  allocation cost we fully own.
- **What "good" looks like:** parsing a large fixture allocates roughly in
  proportion to *what it returns* (last assistant/user turn), not to whole-file
  size — i.e. it proves the tail-read win (WS-6) if/when we do it, and guards it
  after. Track `allocs/op` and `B/op` (deterministic) as the primary signal; the
  same-machine A/B (§2) makes the ns/op-delta trustworthy too.
- **The trap it avoids:** (a) a toy fixture that hides the O(file-size) cost —
  commit a realistic multi-MB transcript; (b) reading *raw cross-machine* ns/op as
  signal — always compare the A/B delta, never one runner's absolute number.
- **Cost/upkeep:** low-medium. One committed fixture (keep it stable), one
  benchmark. High leverage because this path is on every turn.

### 3. Real-app RSS baseline (macro, trend) — ★★★★☆

- **What it protects:** attn's own per-session and idle memory footprint — the
  thing that multiplies across many sessions over a multi-day run. This is the
  memory story, and it lives in the frontend, which no Go benchmark can see.
- **What "good" looks like:** idle RSS at a fixed N sessions stays within a band
  of the last known-good number; the per-live-pane slope (warm-set A/B) doesn't
  climb. The harness already computes all of this
  (`scenario-perf-baseline.mjs`: `headline.totalRssMb`, `warmSweep.perLivePane*`).
- **The trap it avoids:** treating a noisy, single-tenant macro number as a
  per-PR gate, *and* comparing across machines. It's a **per-machine trend**: run
  it on a Mac (there is no Mac runner in the fleet — this is a dev/nightly-Mac
  artifact), compare against *that machine's* fingerprint-keyed baseline in the
  registry (§2), alert only on a large same-machine delta. Also: it must sum the
  *whole process tree* (daemon + one worker per session + reparented WebKit
  PIDs) or the frontend win/regression is invisible — the harness already does
  this correctly.
- **Cost/upkeep:** low to *adopt* (the scenario exists and is maintained);
  medium if we want automated nightly capture + delta alerting. The
  recommendation is to start with "documented manual run on risky PRs + a
  recorded baseline number," not a cron.

### 4. Streaming / long-session leak soak (macro, trend + soft verdict) — ★★★★☆

- **What it protects:** the failure users actually feel — memory that climbs
  over hours/days and never comes back (the WASM balloon; any per-operation
  frontend leak). "Regressions noticed by feel" are mostly *this*.
- **What "good" looks like:** after streaming a heavy workload and tearing panes
  down, **retained** RSS returns near the idle baseline (within a band); over an
  N-iteration soak, retained RSS has a *flat* slope, not a staircase.
- **How:** build on the soak driver (`run-soak.mjs`, #470) + the baseline
  scenario's `--real-cmd` / `--reclaim-hold-ms` decay sampler. Emit an
  `ATTN_VERDICT` with `ok: false` when the retained-RSS slope exceeds a
  threshold, so the soak run self-reports. Compare the retained baseline against
  *that machine's* registry entry (§2). Measure retained-after-teardown and a
  decay window — never peak (§6).
- **The trap it avoids:** (a) asserting on peak RSS (transient, one-way
  allocator makes it meaningless); (b) too-tight a threshold on a lazily-
  scavenged OS → false leak. Use a generous band + a decay hold.
- **Cost/upkeep:** medium. Highest-*value* memory test we could add, but it's
  macro (Mac-only, slow), so it's a nightly/manual soak, not a gate.

### 5. WS outbound event marshal allocations (Go micro, trend; gate-promotable) — ★★★☆☆

- **What it protects:** the broadcast fan-out path. Today `EventPtyOutput` is
  marshalled through the fat ~71-field `WebSocketEvent` per chunk (roadmap
  ws-3). Allocation regressions here scale with clients × chunks.
- **What "good" looks like:** stable, low `allocs/op` per broadcast event; if
  ws-3 (a purpose-built 4-field struct) ever lands, this benchmark proves and
  then guards the win.
- **The trap it avoids:** a fat-struct marshal creeping onto the hot outbound
  path unnoticed.
- **Cost/upkeep:** low. Straightforward `testing.B` over the marshal.

### 6. Store hot-path query benchmarks (Go micro, trend; gate-promotable) — ★★★☆☆

- **What it protects:** SQLite-backed `store.List`/`Get` on paths like
  `handleTodos` (which today does `List("")` + linear scan for one id, roadmap
  store-2). Allocation-heavy queries on frequent paths add up.
- **What "good" looks like:** bounded `allocs/op` for a `Get`; `List` scaling
  linearly and not re-allocating per call.
- **The trap it avoids:** an N+1 or full-scan pattern regressing a frequent
  daemon path.
- **Cost/upkeep:** low-medium (needs a seeded in-memory DB fixture). Modest value
  — the store is not currently a headline cost — so tier-2.

### 7. Classifier deterministic-slice extraction (Go micro, trend; gate-promotable) — ★★☆☆☆

- **What it protects:** the just-landed reconcile change (deterministic
  transcript slice fed to one cheap model call). Protects the *slice extraction*
  we own — **not** the LLM call (that cost is `claude -p`, untestable as a
  benchmark, §7).
- **What "good" looks like:** slice extraction over a large transcript is cheap
  and bounded; it's essentially a specialized case of test #2.
- **Cost/upkeep:** low, but overlaps test #2's fixture and coverage — build only
  if the slice logic diverges enough to warrant its own guard.

### 8. Session spawn / reattach latency (macro, trend) — ★★☆☆☆

- **What it protects:** perceived snappiness of opening/switching sessions.
- **What "good" looks like:** spawn-to-first-pane and reattach-replay wall-clock
  stay in a band. Pure trend — wall-clock, Mac-only, noisy.
- **Cost/upkeep:** medium; the harness can already time these. Lower priority
  than the memory work because latency regressions are more self-evident in use
  than slow memory creep.

### 9. Frontend retained JS-heap after teardown (macro, trend) — ★★☆☆☆

- **What it protects:** JS-side (non-WASM) leaks — detached DOM, listener/store
  accumulation across session churn.
- **What "good" looks like:** JS heap returns to baseline after closing sessions.
- **The trap it avoids:** conflating JS heap with WASM linear memory (different
  allocators; the balloon is WASM, not JS heap). Needs CDP/`performance.memory`
  sampling wired into the harness.
- **Cost/upkeep:** medium-high (new sampling plumbing), narrower coverage than
  the process-RSS soak (#4) which already catches the dominant case. Tier-2.

---

## Agreed starter set vs fuller tier

**Starter set (first implementation batch — decided 2026-07-05):**

- **#1 PTY datapath allocations** (extend the existing bench; record `allocs/op`
  as the primary trend + a same-machine A/B ns/op-delta — on GitHub-hosted shared
  runners)
- **#2 Transcript parser over a real fixture** (`allocs/op` + `B/op` primary +
  same-machine A/B ns/op-delta; one committed realistic transcript)
- **#4 Streaming/long-session leak soak** on the #470 driver with a retained-RSS
  soft verdict compared to the machine's registry entry — the "noticed by feel"
  memory-creep guard (dev/nightly Mac)
- **#3 Real-app RSS baseline** adopted as a *documented dev-run/nightly trend*
  keyed to the per-machine registry (the scenario already exists — this is mostly
  "record a fingerprint-keyed baseline + a runbook")

That's the two hottest owned CPU/alloc paths, the dominant memory failure mode
(long-session retained growth), and the per-session/idle RSS baseline. All
trend-only to start; `allocs/op` on #1/#2 is the promote-to-gate candidate.

The enabling infra the starter set needs:
- a small workflow job (GitHub-hosted `ubuntu-latest`, same as the rest of CI)
  that runs the perf benchmarks **twice on the one runner — `main` then the PR
  head — and `benchstat`s the delta** (same-machine A/B). This makes `allocs/op`/
  `B/op` *and* the ns/op-delta usable despite shared-runner noise. No failing
  threshold yet — trend-only per the decision — but structured so an `allocs/op`
  ceiling can be turned on later without reshaping the job;
- one committed realistic transcript fixture (stable, multi-MB);
- a per-machine timings registry: a fingerprint helper (`hw.model` + CPU + cores
  + RAM + OS + arch) and a gitignored local baseline cache, with a committed
  canonical baseline for a small set of reference machines;
- a retained-RSS verdict added to the soak path (reuse `--reclaim-hold-ms` +
  `emitVerdict`) so `run-soak.mjs` self-reports a growth-slope violation against
  the machine's registry entry;
- a short runbook: "before merging a memory-risky PR, run
  `pnpm run real-app:scenario-perf-baseline` on dev and compare the headline to
  *this machine's* recorded baseline; for long-session risk, run the leak soak."

**Fuller tier (add as the surfaces prove worth guarding):** #5 WS marshal, #6
store queries, #7 classifier slice, #8 spawn latency, #9 JS-heap. Each is a
real-but-narrower guard; add when that surface is touched or has burned us.

**Explicitly out of scope** (don't build): ns/op *gates* (trend only), daemon
steady-state heap guards, worker-scrollback RSS guards, jsdom/headless
render-timing, and any end-to-end classifier *timing* benchmark.

---

## Resolved forks (2026-07-05, with Victor)

1. **Gate strictness → trend-only.** Nothing fails a PR on a perf number to
   start. `allocs/op` (deterministic on any runner) is the candidate to promote
   to a hard gate later if passive trend-watching lets a regression slip.
2. **Macro RSS/soak home → dev-run + per-machine registry baseline.** No Mac
   exists in the runner fleet (savannah is Linux) and GitHub CI has no GPU/
   packaged app, so the packaged-app harness stays a developer/nightly-Mac
   artifact whose runs compare against *that machine's* fingerprint-keyed baseline
   (registry: local cache + committed canonical). A self-hosted Mac + nightly cron
   remains a future option if trend-watching proves too manual.
3. **Go benchmark runner → GitHub-hosted shared runners, same-machine A/B.** The
   self-hosted savannah runners would require moving `victorarias/attn` into the
   `solenesinc` org; **postponed** — we roll with the shared runners we have.
   `allocs/op`/`B/op` are deterministic there, and running `main` vs the PR head
   back-to-back on the same runner (`benchstat` the delta) rescues ns/op as a real
   secondary signal despite cross-machine noise. Revisit if/when attn moves orgs.
4. **Beat noise per-layer → A/B for micro, registry for macro.** Go micro-benches
   use same-machine A/B (no registry needed — the machine is consistent with
   itself); macro RSS/soak use a per-machine fingerprint-keyed registry, stored as
   a gitignored local cache plus committed canonical baselines for reference
   machines. (Chosen over all-in-repo JSON and victor-cloud object storage.)
5. **Starter scope → 3 tests + adopted baseline, including the leak soak** (#1,
   #2, #4, plus #3 adopted). See the starter set above.

**Postponed (not a blocker):** moving attn to `solenesinc` to use the dedicated
savannah runners — would make ns/op reliable and unlock a promote-to-gate story.

---

## Overlap / collision note

This is docs-only (`docs/plans/`), so it shouldn't collide with the active
Present or agent-cost-tools sessions. It *references* but does not modify the
real-app harness and the #467/#470 verdict/soak tooling; if either of those
sessions is also reshaping the harness, the only coupling is that test #4 would
build on `run-soak.mjs`'s verdict contract. Flagging for awareness — no code
conflict expected.
