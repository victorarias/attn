# Frontend test suite audit — runtime, waste, speed

Date: 2026-07-02. Branch: `chore/fe-test-audit`. Report only — no changes made.

## TL;DR

The vitest suite is 127 files / 1,216 tests and runs in **~9s locally** (10-core M-series) and
**46–54s in CI** (4-vCPU ubuntu runner). It is not slow in absolute terms, but **~79% of all
test execution time (18.7s of 23.7s) is literal sleep**: tests waiting out real production
timers (2500ms badge dismiss, 1000ms reconnect backoff, 700ms/500ms debounces) and
`waitFor`'s 50ms polling interval. Removal candidates exist but are few — the suite is
mostly healthy by Victor's bar; the waste is in *how* tests wait, not *what* they test.

**For CI latency specifically, vitest is the wrong tree to bark up**: it is ~50s of a
6.5-minute Frontend job. The E2E step is ~4min because Playwright runs 154 tests fully
serial (`workers: 1`, shared daemon) plus ~40s of uncached browser install. A yaml-only
restructure — cache browsers, split E2E into its own job sharded 3 ways (each shard gets
its own daemon, preserving the serial constraint per machine) — takes the Frontend gate
to **~2–2.5min** with no test changes (§4.4).

## 1. How the suite runs

- `make test-frontend` → `cd app && pnpm run test` → `vitest run` (vitest 4.0.15).
- CI (`.github/workflows/ci.yml`, Frontend job): `pnpm test` on `ubuntu-latest`
  (public repo → 4 vCPU), gated by path filters.
- Config lives in `app/vite.config.ts` `test:` block: `environment: 'happy-dom'` globally,
  `setupFiles: src/test/setup.ts`, with a `node` env override only for
  `scripts/real-app-harness/**`. Default pool (forks), default isolation (per-file).

## 2. Measurements

All commands run from `app/` on this machine (M-series, 10 cores) unless noted.

### Wall-clock (4 runs, `pnpm exec vitest run`)

| run | wall | notes |
|---|---|---|
| 1 (cold, fresh `pnpm install`) | 9.14s | transform 5.1s, setup 8.8s, import 13.9s, tests 23.8s, environment 20.8s (CPU sums across workers) |
| 2 | 8.22s | |
| 3 | 10.33s | |
| 4 | 9.64s | |

Cold vs warm is noise (±1s); the vite cache doesn't matter here. `pnpm install` itself is ~1s on a warm store.

### CI ground truth (Frontend job, last 3 main-branch runs)

| run | Test step (vitest) | E2E step (Playwright) | whole job |
|---|---|---|---|
| 28615402157 (Jul 2) | 54s | 4m05s | 6m41s |
| 28551320714 (Jul 1) | 46s | 4m00s | 6m18s |
| 28478885149 (Jun 30) | 50s | 4m04s | 6m30s |

The vitest suite is **not** the Frontend job's long pole — Playwright E2E is (~4min, plus 44s
of browser install). Out of scope for this audit, but any CI-latency work should look there
first.

### Where the time goes (per-file, from `--reporter=json`)

Top of the ranking; full suite is 23.7s of summed per-file time:

| file | wall | tests |
|---|---|---|
| `src/components/NotebookBrowser.test.tsx` | 7,118ms | 43 |
| `src/hooks/useDaemonSocket.test.tsx` | 4,405ms | 51 |
| `src/components/DiffDetailPanel.test.tsx` | 3,994ms | 45 |
| `src/components/ShortcutEditorModal.test.tsx` | 1,140ms | 21 |
| `src/components/LocationPicker.test.tsx` | 775ms | 48 |
| next 122 files combined | ~6.3s | 1,058 |

**The top 3 files are 65% of total test time.** 117 individual tests ≥45ms account for
18.7s of the 23.7s; the other 1,099 tests take ~5s combined.

### Root-cause attribution (each verified in source)

1. **Real production timers left running in tests.**
   - `NotebookSurface.tsx:777` — `setTimeout(…, 2500)` to dismiss the "Saved" badge. The
     test *"auto-dismisses the Saved indicator after a successful autosave"* waits it out in
     real time: **3,226ms for one test**.
   - `NotebookSurface.tsx:92` — `AUTOSAVE_DELAY_MS = 700`. Every autosave-path test in
     `NotebookBrowser.test.tsx` pays the real 700ms debounce: five tests at ~730ms each.
   - `useDaemonSocket.ts:831` — reconnect backoff starts at 1000ms. The reconnect test
     (*"refetches persisted tile content after websocket reconnect"*) is **1,090ms**.
   - `DiffDetailPanel.tsx:36` — `BACKGROUND_CHANGE_CHECK_DEBOUNCE_MS = 500` and a 150ms
     local-diff delay (`DiffDetailPanel.tsx:497`). Explains its 212–707ms tests almost
     exactly (150ms cluster, 500ms+ cluster).

2. **`waitFor` 50ms-poll quantization.** In `renderHook`-style tests there are no DOM
   mutations to re-trigger `waitFor`, so every await that isn't immediately true costs one
   or more full 50ms poll intervals. The `useDaemonSocket.test.tsx` per-test durations
   quantize perfectly: 40 tests at 54–61ms (one poll), 10 at 106–115ms (two polls), one at
   1,090ms (real backoff). ~3.3s of that file's 4.4s is poll-sleep.

3. **happy-dom environment instantiation: ~185ms per file.** Measured directly on
   `src/utils` (30 files): `--environment node` → environment column 0.002s total;
   default happy-dom → 5.57s total. 27 of the 30 utils files pass unchanged under node
   (`viewedDiffHashes`, `terminalDiagnosticsLog`, `verbatimTextEntry` genuinely touch DOM).

### Config experiments (dead ends, measured so nobody retries them)

- `vitest run --no-isolate`: 7.67s but **159 tests fail** from shared module state
  (zustand singletons, module-level mocks). Not viable without a large refactor. Setup
  cost drops 8.8s→1.1s, so the theoretical win is real but small locally.
- `vitest run --pool=threads`: 9.95s — no better than the default forks pool.
- Fixed startup floor: a single tiny file runs in 0.65s total (`vitest` boot ~0.4s +
  244ms run), so vitest overhead itself is negligible.

### Reproduce

```bash
cd app && pnpm install --frozen-lockfile
pnpm exec vitest run                                  # wall-clock baseline (ran 4x)
pnpm exec vitest run --reporter=default --reporter=json --outputFile=/tmp/run.json  # per-test timing
pnpm exec vitest run --no-isolate                     # 159 failures — dead end
pnpm exec vitest run --pool=threads                   # no win
pnpm exec vitest run --environment node src/utils     # env cost comparison (3 files fail: need DOM)
gh run view <run-id> --json jobs                      # CI step timings
```

## 3. Removal candidates

All 127 files were reviewed against the bar: type-checker-guaranteed, mock-only, or
duplicate coverage. The suite is mostly healthy — **31 tests qualify, 24 of them in one
file**. Notably, `useDaemonSocket.test.tsx` (51 tests) and all of `src/utils` came back
clean: heavy mocking there (FakeWebSocket etc.) drives real production logic.

### 3a. `src/components/DiffDetailPanel.test.tsx` — 24 tests, ~390 of 1,234 lines

The trailing blocks test only their own fixtures and the mock daemon; no production code
runs. The file's own comment (line 1056) says the production behavior these once backed
moved to DiffView and is exercised by `e2e/diff-view.spec.ts`.

- `describe('guard rails')` (line 789, 2 tests) — MOCK-ONLY. Tests MockDaemon's own
  `maxCalls`/`strict` features, which **no other test in the repo uses**.
- `describe('Deleted-Line Comment Fixtures')` (line 844, 22 tests) — MOCK-ONLY/DUPLICATE:
  - `createReviewComment` (4): asserts the test fixture's own defaults and
    `{...defaults, ...overrides}` spread.
  - `createDeletedLineComment` (5): pins the fixture's one-line `-(i+1)` arithmetic,
    three times with different constants.
  - `deleted line index encoding/decoding` (4): computes `-(index+1)` and
    `Math.abs(x)-1` **inline in the test** and asserts them against each other — zero
    code under test.
  - `comment categorization` (4): filters fixture output with an `isDeletedLine` lambda
    defined in the test, not imported from production.
  - `daemon mock comment operations` (5): configures a mock response, calls the mock,
    asserts the mock returned it. The "save/load cycle" test is two stubs returning the
    same object.

These are all ~0–2ms tests, so deleting them wins hygiene and ~390 lines, not runtime.

### 3b. Scattered duplicates (7 tests)

- `StateIndicator.test.tsx :: renders with default props` (line 6) — DUPLICATE — only
  asserts the testid element exists; `applies state classes correctly` (line 23) renders
  the identical element and asserts more.
- `StateIndicator.test.tsx :: renders unknown state class` (line 67) — DUPLICATE — the
  class is a single non-branching template already pinned for four states by
  `applies state classes correctly`; nothing unique to `unknown` is asserted.
- `GhosttyWebGlRenderer.test.ts :: always converges to the cap and never exceeds it under
  repeated growth` (line 74) — DUPLICATE — the reachable domain is exactly {1024, 2048},
  both already pinned by the doubling and idempotence tests.
- `store/daemonSessions.test.ts :: isRepoMuted > returns true when repo is muted` (53) and
  `returns false when repo exists but is not muted` (46) — DUPLICATE — both branches
  asserted identically by `finds correct repo among multiple` (60). (The not-in-list test
  must stay.)
- `store/daemonSessions.test.ts :: setRepoStates > updates repoStates` (34) — DUPLICATE —
  one-line zustand `set` already exercised as the seeding step of the surviving tests.
- `TicketBoardPanel.test.tsx :: mounts read-only with only the four contract props` —
  TYPE-GUARANTEED — `TicketBoardPanelProps` has exactly those four props; TypeScript
  enforces the contract, and every other test mounts with the same prop set.

### 3c. Borderline (noted, not recommended)

One-line zustand setter passthroughs in `daemonSessions.test.ts` (`setDaemonSessions`,
`setPRs`, `setConnected`); the six per-value passthrough tests in
`types/sessionState.test.ts` (each does pin a distinct daemon string — could be one table
test); `shortcuts/registry.test.ts` constant-restating tests (deliberate drift-guards
given the ⌘⇧Z native-menu history — keep); `ticketResume.test.ts` handler-routing tests
(near-mock-only but the 3-case switch is production code). Full per-agent notes preserved
in the review; none of these clear the bar for deletion.

## 4. Speedups, ordered by win vs effort

### 4.1 Kill the real-timer sleeps in 4 files (biggest win, moderate effort)

~17s of the 18.7s of measured sleep is concentrated in `NotebookBrowser.test.tsx` (6.5s),
`useDaemonSocket.test.tsx` (4.3s), `DiffDetailPanel.test.tsx` (3.7s), and
`scenarioAgents.test.mjs` (0.5s). Two mechanical patterns fix all of it:

- **Fake timers** for the component-timer waits (2500ms badge, 700ms autosave debounce,
  500ms diff debounce, 150ms local-diff delay, 1000ms reconnect backoff).
  `@testing-library/dom`'s `waitFor` auto-detects vitest fake timers and advances them,
  so most tests only need `vi.useFakeTimers()` in `beforeEach`. The repo already has the
  pattern: `scenarioAssertions.test.mjs` uses `vi.useFakeTimers()` +
  `advanceTimersByTimeAsync` — `scenarioAgents.test.mjs`'s 506ms (a literal
  `await delay(500)` at `scenarioAgents.mjs:171` reached through the test) can copy it.
  Alternative where fake timers fight the test: make the delay injectable
  (e.g. `AUTOSAVE_DELAY_MS` as an optional prop defaulting to 700).
- **Tighter `waitFor` interval** for the renderHook quantization: in
  `useDaemonSocket.test.tsx`, a file-local `waitFor` wrapper passing `{ interval: 5 }`
  (or asserting after an explicit microtask flush) collapses the 40×54ms + 10×108ms
  poll-sleeps to near-zero.

Expected effect: total test CPU 23.8s → ~5s. CI Test step ~50s → ~40s (estimate). The
bigger payoff is the inner loop: a single-file `vitest NotebookBrowser` run drops from
~7.5s to under 1.5s, and these are exactly the files one iterates on. It also stops the
pattern from compounding as the suite grows.

### 4.2 Run pure-logic files in the node environment (small CI win, near-zero risk)

happy-dom costs ~185ms/file (measured); node ~0. The review classified **55 files** as
node-eligible, with imports verified: 27 of 30 `src/utils` (all but `viewedDiffHashes`,
`terminalDiagnosticsLog`, `verbatimTextEntry`), both `src/types`, 5 `src/pty`,
`store/daemonSessions` + `workflowRuns`, `shortcuts/formatShortcut` + `cheatsheet`,
`hooks/useWorkspaceSelectionController`, `components/DiffView`, `terminalGlyphFont`,
`terminalGlyphProgram`, `terminalGraphemeMode`, `grid/GridCompositor`,
`grid/UnifiedGridRenderer`, all 8 pure `components/notebook/*` test files,
`SessionTerminalWorkspace/dockTarget` + `paneRuntimeEventRouter`. Mechanism: extend the
existing `environmentMatchGlobs` in `vite.config.ts` (or per-file
`// @vitest-environment node` pragmas — more robust as files move). ~10s of CPU saved;
roughly −3–5s on the CI step, −1s locally.

Traps found while measuring: `gridLayout`/`gridMembership` look pure but their modules
touch `window.localStorage`; `store/sessions.ts` assigns `window.__TEST_*` in DEV;
`chordState`/`registry`/`resolver` construct `KeyboardEvent`. Don't flip those.

### 4.3 Not worth doing (measured or judged)

- **`--no-isolate`**: 159 failures from shared module state. The refactor to make the
  suite isolation-safe is far larger than the ~1.5s it would win.
- **`--pool=threads`**: measured, no win (9.95s vs ~9.3s).
- **Sharding / `test.concurrent`**: the suite is 9s local / ~50s CI — sharding overhead
  isn't justified, and `test.concurrent` would only paper over sleeps that 4.1 removes
  properly.
- **Startup/transform tuning**: vitest boot is 0.4s and transform ~5s across all workers;
  nothing folkloric to gain.

### 4.4 The real CI cost center: E2E (measured addendum)

Since CI latency is the driving concern, the E2E step was measured too. The vitest suite
is ~50s of a 6.5-minute Frontend job; Playwright E2E is ~4min plus ~40s of browser
install.

**Why E2E is slow: it is configured fully serial.** `playwright.config.ts` sets
`workers: 1, fullyParallel: false` ("shared daemon") — 25 spec files / 154 tests run one
at a time on a 4-vCPU runner.

Measured locally (`ATTN_PROFILE=feaudit pnpm run e2e`, throwaway daemon, 2m26s wall —
CI's ~4min is the same suite at ~1.65× runner slowdown), per-spec totals:

| spec | time | tests |
|---|---|---|
| keyboard-shortcuts | 17.6s | 17 |
| terminal-interactions | 17.5s | 17 |
| diff-view | 14.9s | 23 |
| location-picker | 14.2s | 13 |
| pr-actions | 8.9s | 6 |
| split-blank-repro / terminal-blocks | 8.1s each | 4 / 7 |
| remaining 17 specs | ≤7s each, 55s combined | |

The distribution is flat — no dominating spec — which is the good case for **sharding**:
splitting by file balances well and scales near-linearly.

**Recommended CI restructure (yaml-only, no test or product changes):**

1. **Cache Playwright browsers** (`actions/cache` on `~/.cache/ms-playwright`, keyed on
   the Playwright version). Saves ~40s on every run. Trivial.
2. **Split E2E into its own job, sharded 3 ways** (`playwright test --shard=N/3` matrix).
   Each shard is a separate machine running its own daemon + vite server through the
   existing `webServer` config, so the shared-daemon serial constraint is preserved
   *within* each shard — no isolation work needed. Per-shard: ~82s of tests (245s CI ÷ 3)
   plus ~35s setup (checkout/node/pnpm/go/binary build, browsers cached). The unit job
   (typecheck + vitest) runs in parallel at ~1.5–2min.
   **Frontend gate: ~6.5min → ~2–2.5min.**
3. Only if that's not enough later: within-machine parallelism (`workers > 1` with
   per-worker daemon port bands). The `e2e/profileEnv.ts` port-band machinery is halfway
   there, but per-worker daemon spawning is real design work — unnecessary if sharding
   lands.

Reproduce: `go build -o ./attn ./cmd/attn && cd app && ATTN_PROFILE=feaudit pnpm run e2e`
(named profile → disjoint ports, safe next to other sessions; `attn profile clean feaudit`
afterwards).

## 5. Do these first (ordered for CI latency, the driving concern)

1. **CI restructure (§4.4): browser cache + separate sharded E2E job.** Yaml-only.
   This is where the minutes are: Frontend gate ~6.5min → ~2–2.5min.
2. **Delete the DiffDetailPanel fixture/guard-rails blocks** (24 tests, ~390 lines) and
   the 7 scattered duplicates in §3b. Pure hygiene, ~30 minutes, zero coverage lost.
3. **Fake-timer the sleep-heavy vitest files** (§4.1: NotebookBrowser, DiffDetailPanel,
   useDaemonSocket, scenarioAgents). CI Test step ~50s → ~40s; single-file iteration on
   NotebookBrowser ~7.5s → <1.5s.
4. **Defer**: node-env flip for the 55 pure files (small win, adds config surface to
   maintain); within-machine E2E parallelism (design work, unnecessary once sharding
   lands).

End state estimate: Frontend CI gate ~2–2.5min (from ~6.5), local vitest ~9s → ~7s with
sleep-free iteration on the three worst files.
