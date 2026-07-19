# Plan: Faster Go tests

## Goal

Keep the ordinary Go verification contract intact while making a cache-controlled
`go test` run fast and predictable enough for pre-commit use. Tests must remain
isolated from production `~/.attn`.

## Starting Measurements

Measured on an Apple M5 Max with macOS 26.5.2 and Go 1.26.2. Full-test timings
use a warm compiler cache and `-count=1` so Go's test-result cache cannot hide
execution. These numbers describe this machine and cache state, not a universal
budget.

| Candidate | Starting result |
| --- | ---: |
| Full `go test -count=1 ./...`, inherited Spotify Git wrapper | 131.07s, pass |
| `internal/daemon` within that run | 129.882s, pass |
| `internal/git`, inherited Spotify Git wrapper | 14.97s, pass |
| `internal/git`, direct Git | 5.93s, pass |
| Full suite, direct Git diagnostic | 70.41s, one load-sensitive Codex-resume failure |
| Four direct-Git daemon shards | 18.81s, one shared `/tmp/attn.pid` collision |
| Compile only, fresh `GOCACHE` | 29.68s |
| Compile only, warm `GOCACHE` | 0.29s |

## Test Path

```text
Current:
make / pre-commit
  -> one `go test ./...`
    -> packages run concurrently
      -> 778 daemon tests run almost entirely serially
      -> every `git` lookup inherits the developer PATH

Target:
make / pre-commit
  -> one repository test runner
    -> temp-scoped ATTN_DATA_DIR with inherited path overrides cleared
    -> direct, uninstrumented Git for test subprocesses
    -> bounded package and per-process Go parallelism
    -> five deterministic daemon test shards
      -> temp-scoped sockets, PID files, ports, and data
    -> one reusable attn test binary for process E2E tests
    -> 90-second per-job fail-fast budget
```

## Implementation Tasks

- [x] Run tests with a direct Git executable rather than an inherited wrapper.
- [x] Isolate daemon socket, PID-file, port, and other shared test resources.
- [x] Shard `internal/daemon` deterministically across five test processes.
- [x] Replace deliberate wall-clock sleeps with test-controlled thresholds.
- [x] Reuse a prebuilt `attn` binary across process E2E tests.
- [x] Add `t.Parallel()` only to audited tests without process-global state.
- [x] Repeat cold and warm measurements and compare them with this baseline.
- [x] Replace the eight real SSH dial timeouts with one deterministic protocol failure while preserving the OS-level child-reaping assertion.
- [x] Add a private Claude transcript-retry seam so duplicate-turn behavior is tested without the production two-second window.
- [x] Test the daemon's no-new-turn handling through a fake extraction adapter instead of the real Claude retry loop.
- [x] Move remote URL variants to the pure parser and retain one Git-backed origin integration test.
- [x] Remeasure the focused tests and ten complete cache-controlled runs.
- [x] Prototype compiling the daemon test binary once for all shards; reject it
  if the measured gain does not justify duplicating Go's flag rewriting.
- [x] Stabilize only the runner's outer isolation paths so unchanged daemon
  shards can use Go's result cache while executed tests keep fresh temp dirs.
- [x] Prove cache invalidation when the E2E binary or direct Git executable
  changes, plus concurrent-run isolation.
- [x] Measure the unchanged-hook warm path and a cache-controlled changed path.
- [x] Make pre-commit's temporary E2E build metadata deterministic.
- [x] Enable the tracked `.githooks/pre-commit` for every attn worktree.
- [x] Commit through the installed hook and measure its cache behavior.

## Results

Ten consecutive five-shard `-count=1` runs passed. Their wall times were
24.71–27.45s, with a 25.64s median and 27.45s observed p95. A fresh-`GOCACHE`
run passed in 48.43s. The cache-controlled median is 80% lower than the 131.07s
starting measurement; the observed p95 and cold run meet their budgets. The
median missed the aspirational sub-25s target by 0.64s.

The focused `internal/git` package improved from 14.97s through the inherited
wrapper to 1.99s using direct Git plus audited parallel tests. The worker theme
argument test improved from 6.3s and load-sensitive failure to 0.42s by testing
argument construction without launching a process.

A second pass removed the remaining deliberate waits without deleting their
regression contracts. Isolated `-count=1 -json` timings changed as follows:

| Test contract | Before | After |
| --- | ---: | ---: |
| SSH child reaping after dial failure | 4.30s | 0.16s |
| Claude duplicate-turn extraction | 2.02s | <0.01s |
| Daemon no-new-turn handling | 2.03s | 0.01s |
| Git-backed origin resolution | 0.50s | 0.10s |

The faster SSH harness still failed in 0.21s and found one zombie when
`cmd.Wait()` was temporarily mutation-removed, proving that it preserves the
historical regression signal.

Ten additional complete runs passed at 27.95–40.36s with a 29.45s median.
This sample is not directly comparable to the earlier low-contention median:
other attn worktrees ran `go test ./...` concurrently, and observed system CPU
included `mediaanalysisd` above 75% and `JamfDaemon` above 50%. Eight of ten
runs clustered at 27.95–32.81s; the 40.36s outlier coincided with that external
load. The focused timings establish the code-path savings; the full-run sample
shows no correctness regression but cannot establish a new low-contention p50.

A compiled-daemon-binary prototype also passed default, verbose, short, JSON
fallback, and focused race-instrumented verification. Ten cache-controlled runs
passed at 24.85–36.16s with a 29.60s median, compared with 29.87s immediately
before the prototype. The roughly 0.27s median difference is within ordinary
run-to-run variance and does not justify maintaining a partial reimplementation
of Go's build/test flag rewriting, changing daemon test-cache behavior, or
degrading package-aware output. The runner change was therefore removed.

The hook-oriented cache pass instead kept Go in charge and stabilized only the
runner's outer environment. `ATTN_DATA_DIR` now points at a non-production cache
namespace while every executing package `TestMain` still replaces it with a
fresh per-process temp directory. The direct Git path is keyed by both path and
binary content, and the shared E2E executable is copied to a content-addressed
path, so changing either external input invalidates the test results that read
it. An explicitly supplied `ATTN_E2E_BIN` is normalized through the same path.

After one population run, five unchanged full runs passed in 5.19–6.36s with a
5.80s median and all 44 package results cached. The final runner revision took
30.61s to populate its new namespace and 5.77s on the immediate 44/44 cached
rerun. `-count=1` still forced all tests to run and passed in 33.11s. Two
simultaneous cache-miss runners both passed, covering ten concurrent daemon
shards without sharing runtime data. A cmd-only mutation invalidated only the
two daemon shards that read the changed E2E binary; a semantics-preserving
alternate Git executable invalidated all Git-reading shards. Both mutation
probes passed and were removed.

The tracked `.githooks/pre-commit` is now enabled through the repository's
shared `core.hooksPath=.githooks`, which resolves each worktree's own checked-out
hook instead of a script from the main worktree. The hook already routes by
staged path: Go-affecting changes run build plus the full Go suite, while
unrelated commits skip that bucket. Enabling it exposed and fixed two dormant
portability issues in `scripts/pre-commit.sh`: macOS Bash 3 lacks `mapfile`, and
one exported command substitution masked its status under ShellCheck.

Pre-commit E2E binaries now use fixed test-only build metadata and
`-buildvcs=false`; two builds were byte-identical. Production and release
metadata are unchanged. The first real commit through the installed hook passed
in 28.97s. An exact retry of the same staged state passed in 4.97s, while a new
staged hook change took 27–30s because it correctly produced a new test-input
identity. The practical contract is therefore about 30s for newly changed
daemon-bucket inputs, about 5s for an exact cached retry, and effectively no Go
cost for staged paths outside the daemon bucket.

## Success Criteria

- [x] The same default Go test coverage passes ten consecutive cache-controlled runs.
- [ ] Warm compiler cache with test-result caching disabled: p50 under 25s on
  the baseline machine (observed 25.64s).
- [x] Warm compiler cache with test-result caching disabled: p95 under 30s on
  the baseline machine (observed 27.45s).
- [x] Fresh `GOCACHE`: p95 under 60s on the baseline machine.
- [x] No failures from fixed ports, shared PID files, sockets, inherited test paths,
  or production attn state.

## Decisions

- Preserve the complete default suite before considering a separate integration
  tier.
- Prefer process sharding over broad in-process parallelism because daemon tests
  mutate environment variables and package globals.
- Keep Git selection test-scoped; do not change interactive Git or global Spotify
  configuration.
- Five daemon shards with `GOMAXPROCS=3` replaced the initial four-shard shape
  after the measured concurrency sweep: 25.32s versus 30.75s for four/four and
  41.85s for six/four.
- Keep the Go driver responsible for compiling and invoking every test binary;
  compiling the daemon binary once did not produce a material wall-time gain.
- Cache only stable outer inputs. Runtime data remains unique per executing test
  process, while content-addressed external executables prevent cached results
  from hiding changed Git or E2E behavior.
- Use Git's existing tracked `.githooks` seam rather than adding a hook manager.
  The relative hooks path keeps hook code worktree-local while configuration is
  shared by the repository.
