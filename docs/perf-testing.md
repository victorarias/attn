# Performance testing

This is the practical runbook for attn's performance-test suite. It covers how
to run each layer and how to read the result. For the *why* behind this shape
(trend-only, no hard gates, per-machine registry instead of a single "Victor's
number"), see the strategy doc:
[`docs/plans/2026-07-05-performance-test-strategy.md`](plans/2026-07-05-performance-test-strategy.md).

## Philosophy: trend-only, no hard gates (for now)

Nothing here fails a PR on a perf number. Every layer records a metric and
prints a machine-parseable verdict; a human (or a future promote-to-gate PR)
reads the trend. This sidesteps flaky-perf-CI at the cost of relying on
someone actually looking. The one exception in spirit: `allocs/op` from the Go
benchmarks is deterministic enough that it's the primary signal to watch, and
the most likely candidate to graduate to a real gate later.

## Go micro-benches (CPU/allocs)

Targeted `go test -bench` benchmarks for hot paths — currently the PTY
datapath (`internal/daemon`) and the transcript parser (`internal/transcript`).
Run them locally:

```bash
go test -run '^$' -bench . -benchmem ./internal/daemon/ ./internal/transcript/
```

- `-run '^$'` skips regular tests so only benchmarks run.
- `-benchmem` adds `allocs/op` and `B/op` alongside `ns/op`.
- `allocs/op` is the primary trend: it's deterministic regardless of machine
  noise, unlike `ns/op` on a shared runner.
- `ns/op` is noisy cross-machine but consistent on the *same* machine, which is
  why CI (forthcoming) runs `main` and the PR head back-to-back on the same
  runner and diffs the two with `benchstat` instead of comparing against a
  fixed number.

A CI job that runs this same-machine A/B automatically is a later PR — not
built yet.

## Real-app RSS baseline (macro memory)

`scenario-perf-baseline.mjs` drives a packaged dev-app install to a fixed
number of shell sessions and measures the resident-memory footprint of the
whole attn tree (app + WebKit + daemon + pty-workers). It requires a running
dev install (`make dev`) since it's a packaged-app scenario — see
[`app/scripts/real-app-harness/CLAUDE.md`](../app/scripts/real-app-harness/CLAUDE.md)
for the harness's profile/single-tenant rules.

Run it:

```bash
pnpm --dir app run real-app:scenario-perf-baseline -- --sessions 8 --stream 2
```

### Self-baselining per machine

Every machine is fingerprinted (`hw.model` + CPU brand + core count + RAM +
OS major + arch — see `app/scripts/real-app-harness/machineRegistry.mjs`) and
compared against *that machine's own* recorded baseline, not a fixed number
from someone else's laptop:

- **First run on a machine**: there's no baseline yet, so the run always
  passes and records one.
- **Later runs**: the headline total RSS is compared to the stored baseline.
  Growth beyond `--rss-tolerance-pct` (default 15%) fails the verdict; an
  improvement (RSS below baseline) always passes.
- **Re-recording**: pass `--record-baseline` to overwrite the stored baseline
  with this run's number — use this after an intentional change to the memory
  footprint (a real fix or a deliberate trade-off), not to silence a
  regression you haven't understood. This run's verdict always passes
  (`ok:true`, `reason:'recorded'`): the run *defines* the new baseline, so it
  is never evaluated against — and can never regress against — the number it
  is replacing.

Local per-machine baselines live in `~/.attn-perf-registry/<fingerprint>.json`
(outside the repo — every dev's cache is their own). A small set of known reference
machines can also have a baseline **committed** to
`app/scripts/real-app-harness/perf-baselines.json`; a committed entry for a
given fingerprint always wins over the local cache, so it's the way to pin a
canonical number for review.

### Reading the verdict

The scenario prints one `ATTN_VERDICT ` line (compact JSON) as the last thing
it emits on success. Take the **last** such line if there's more than one.
Shape:

```json
{
  "ok": true,
  "scenarioId": "perf-baseline",
  "runId": "perf-baseline-...",
  "failureCount": 0,
  "firstFailure": null,
  "artifactsDir": "...",
  "summaryPath": ".../summary.json",
  "durationMs": 12345,
  "rss": { "ok": true, "value": 512.3, "baseline": 498.1, "deltaPct": 2.9, "tolerancePct": 15, "reason": "within-band" },
  "metrics": { "totalRssMb": 512.3 }
}
```

`rss` and `metrics` are extensions on top of the core verdict contract (`ok`,
`scenarioId`, `runId`, `failureCount`, `firstFailure`, `artifactsDir`,
`summaryPath`, `durationMs` — the same shape every `createScenarioRunner`
scenario emits). `rss.reason` is one of `no-baseline` (first run on this
machine, pass), `within-band` (pass), `regression` (fail), or `recorded`
(`--record-baseline` established/overwrote the baseline this run — always a
pass). A regression **does not** set a non-zero process exit code by itself —
only a genuine harness error does that — so treat `verdict.ok:false` here as a
trend to investigate, not a build break.

`summary.json` under the run's artifacts directory has the full detail behind
the headline number (per-process-class RSS, worker count, optional warm-set
sweep, optional real-output peak/retained RSS) plus `baselineComparison`, the
same object as the verdict's `rss` field.
