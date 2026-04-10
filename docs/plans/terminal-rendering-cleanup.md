# Terminal Rendering Cleanup

Status: In progress
Owner: Current terminal cleanup stream
Context date: 2026-04-09

## Why This Exists

The terminal/PTy/UI stack is finally in a state where the important packaged-app canaries are green, which gives us room to simplify instead of continuing to patch behavior opportunistically.

The main opportunity now is to remove complexity that has accumulated around:

- replay vs redraw boundaries
- same-app remounts vs true relaunch attaches
- split/close churn
- harness-driven verification hooks

This started as a clean handoff plan. It is now also the running status document for the cleanup work already completed.

Use it together with:

- [PTY_RENDERING.md](/Users/victor.arias/projects/victor/attn/docs/PTY_RENDERING.md)
- [TERMINAL_RENDERING_LEARNINGS.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_RENDERING_LEARNINGS.md)
- [TERMINAL_TEST_ARCHITECTURE.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_TEST_ARCHITECTURE.md)
- [TERMINAL_REGRESSION_SCENARIOS.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_REGRESSION_SCENARIOS.md)

## Current Protected State

Last known fully green serial packaged-app matrix during this cleanup stream:

- verified on 2026-04-10 after a fresh `make install-all`
- executed against `~/Applications/attn.app` via `ATTN_REAL_APP_PATH="$HOME/Applications/attn.app"`

- `TR-205` remote Codex relaunch/split/close
- `TR-205` remote Claude relaunch/split/close
- `TR-504` remote cleanup
- `TR-402` local Codex split-close recovery
- `TR-402` local Claude split-close recovery
- `TR-303` local Codex post-close typing

Run command:

```bash
cd /Users/victor.arias/projects/victor/attn
make install-all

cd /Users/victor.arias/projects/victor/attn/app
ATTN_REAL_APP_PATH="$HOME/Applications/attn.app" pnpm run real-app:serial-matrix
```

Important:

- if you want packaged-app evidence for current workspace changes, rebuild/install first; otherwise you may be testing an older installed app
- packaged-app scenarios must run serially
- parallel runs are invalid evidence because they contend for the same live app automation surface
- the serial matrix wrapper does not forward `--app-path`; use `ATTN_REAL_APP_PATH` or rely on the default installed path
- repeated reruns are sometimes necessary before calling a matrix failure product-side, especially on remote `TR-205`

## Current Status

Completed app-side cleanup slices:

- PTY attach/replay policy cleanup and transport-state cleanup in the frontend socket/runtime path
- terminal resize lifecycle extraction
- terminal viewport action extraction
- terminal renderer lifecycle extraction
- terminal viewport lifecycle extraction
- pane runtime binder lifecycle/write-state deduplication
- pane runtime binder same-app remount state consolidation
- pane runtime binder per-pane lifecycle state consolidation
- pane runtime binder unmount/write-state consolidation
- pane runtime binder queued-event/input-subscription consolidation
- pane runtime lifecycle registry extraction
- pane runtime controls / ensure / binding / utility extraction
- same-app resize redraw heuristic removal and shell/non-shell geometry unification in the binder path
- app-side session close acknowledgement path

Completed validation/hardening that matters to rendering cleanup:

- remote cleanup verification proves close tears down worker-side processes
- packaged-app matrix can run green end-to-end after cold-daemon reruns
- fresh-install packaged-app matrix passed end-to-end on 2026-04-09 after `make install-all`, confirming the current cleanup state is green when build provenance is correct
- fresh-install packaged-app matrix stayed green on 2026-04-09 after removing the same-app non-shell deferred redraw bounce, including `TR-402` and `TR-303`
- fresh-install packaged-app matrix passed end-to-end again on 2026-04-10 after removing the remaining same-size `forceRedraw` / `fit_same_size` / `visibility_flush_same_size` path
- native window capture now prefers app-reported window bounds before falling back to `System Events`

Current code-size tracking metric for this cleanup stream:

- tracked terminal-rendering surface baseline before the latest Phase 2 slice: `7357` lines
- tracked terminal-rendering surface after the latest Phase 2 slice: `7263` lines
- `usePaneRuntimeBinder.ts`: `994` -> `947`
- tracked terminal-rendering surface after experiment 1: `7154` lines
- `usePaneRuntimeBinder.ts`: `947` -> `878`
- `Terminal.tsx`: `983` -> `967`
- `geometryLifecycle.ts`: `148` -> `124`
- this is intentionally only a rough signal of simplification, not a correctness metric

Current caveat:

- the remaining instability is concentrated in remote packaged-app `TR-205`, especially Codex startup/bootstrap/native-capture timing
- recent failures in that scenario have included:
  - localhost `ECONNREFUSED`
  - `create_session` timeout
  - native paint capture seeing a narrow painted strip while UI-visible content was healthy
  - baseline prompt/anchor seeding timing out while Codex was still starting MCP servers
- those failures reproduced independently of the most recent app-side simplification slices, so they should not automatically be treated as regressions from the cleanup work

Recent cleanup commits in this stream:

- `c032784` `Clean up terminal rendering flow and close semantics`
- `7b1845a` `Extract terminal renderer lifecycle`
- `9c88236` `Extract terminal viewport lifecycle`
- `e3a7759` `Clean up pane runtime binder state`

## High-Confidence Learnings

These should be treated as working assumptions unless disproven by a tighter experiment.

### 1. Same-app split churn and true relaunch attach are different problems

Do not design one restore path that tries to solve both.

The clean boundary is:

- same-app split/open/close churn should preserve the existing live xterm as much as possible
- true relaunch attach may need replay because the frontend terminal state is gone

### 2. Visible-frame replay is the wrong restore primitive

Prototype work showed:

- visible-frame replay is not xterm-state-equivalent
- later Codex resize recovery can fail after visible-frame replay even when it succeeds on a live xterm

So:

- do not reintroduce frontend local frame replay for agent panes
- do not use a painted frame as if it were authoritative terminal state

### 3. Raw replay is better than screen snapshots, but still not “terminal state restore”

Prototype work also showed:

- raw-byte replay preserves Codex resize recovery better than `screen_snapshot`
- no-replay is insufficient for fresh xterm rebuilds in the cases captured

So the current direction is:

- if replay is needed on relaunch, bounded raw replay is the least-wrong candidate
- but replay remains provisional, not authoritative redraw

### 4. Some “header loss” bugs are actually viewport bugs

Especially for Codex:

- the header can still exist in the buffer while the viewport moves down
- this is not the same bug as missing content or remount/redraw churn

So any simplification pass must keep these failure classes separate:

- buffer missing content
- stale wrapping / stale geometry
- viewport drift
- remount / redraw side effects

### 5. Harness support code is now part of the product safety net

The following are no longer throwaway experiments:

- serial matrix runner
- pane native capture and assertions
- visible content assertions
- runtime trace canaries
- prototype xterm replay characterization tests

Do not delete them casually while simplifying the product code.

## Simplification Goal

Reduce the terminal rendering model to two explicit policies:

### Policy A: Same-App Split Churn

Applies to:

- split open
- split close
- focus churn
- local pane resize within the same running app session

Desired properties:

- no replay tricks
- no remount unless unavoidable
- no attach/re-attach as a redraw repair mechanism
- no persistent “replay restored” state that influences future ordinary resizes
- redraw activity stays targeted and minimal

### Policy B: True Relaunch Attach

Applies to:

- app quit and reopen
- reconnecting to an already-running remote or local agent runtime

Desired properties:

- allow provisional replay
- prefer bounded raw-byte replay over visible-frame snapshot semantics
- do not assume replay is authoritative
- do not let replay-induced state leak into future same-app split churn logic

## Main Cleanup Targets

### 1. Make the two-policy split explicit in code

Likely files:

- [app/src/hooks/useDaemonSocket.ts](/Users/victor.arias/projects/victor/attn/app/src/hooks/useDaemonSocket.ts)
- [app/src/components/SessionTerminalWorkspace/usePaneRuntimeBinder.ts](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/usePaneRuntimeBinder.ts)
- [internal/daemon/ws_pty.go](/Users/victor.arias/projects/victor/attn/internal/daemon/ws_pty.go)

Desired outcome:

- attach/replay logic is clearly relaunch-specific
- split/close/resize logic is clearly same-app-specific
- fewer conditionals that mix the two

Status:

- partially done
- a lot of the policy has already been pulled into dedicated helpers
- the remaining work is mostly concentrated in `usePaneRuntimeBinder.ts` and any daemon/frontend seams that still mix relaunch policy with ordinary pane churn

### 2. Minimize remount sensitivity

Question to answer:

- can the main terminal stay mounted through ordinary split churn more often than it does today?

If yes:

- delete binder complexity rather than papering over it

If no:

- at least document exactly which transitions still remount and why

Status:

- improved, but not finished
- same-app remount hydration is more explicit now
- the binder still carries too many parallel per-pane refs/maps for runtime, geometry, and hydration state

### 3. Remove leftover redraw heuristics that are compensating for architecture

Watch for:

- bounce redraws that exist only because state is reconstructed too often
- resize/redraw sequences that are trying to heal stale replay instead of avoiding stale replay

Status:

- partially done
- some redraw coupling was already removed when replay-restored runtimes stopped forcing later redraw bounces
- remaining redraw behavior should now be challenged mostly in binder/runtime coordination, not in `Terminal.tsx`

### 4. Clarify pane-kind differences

Agent panes and shell panes do not necessarily need the same restore rules.

Be explicit if the correct behavior is:

- agent panes: replay-aware relaunch policy
- shell panes: simpler attach/recover model

Status:

- still open
- shell-vs-agent restore policy is clearer than before, but not yet documented as a final explicit contract in one place

## Suggested Work Order

### Phase 1: Refactor For Clarity Without Behavior Change

Goal:

- expose the existing two-policy model in code structure
- avoid changing semantics yet

Tasks:

1. Identify the exact codepaths for:
   - same-app split churn
   - true relaunch attach
2. Rename/refactor helpers so the distinction is visible
3. Delete dead branches left behind by earlier experiments

Verification:

- `pnpm run real-app:serial-matrix`

Status:

- complete for the current cleanup stream
- the latest slices finished the obvious no-behavior-change extraction work around pane lifecycle state, binding sync, runtime ensure/retry, controls, terminal utils, and stale-pane cleanup
- further splitting of `usePaneRuntimeBinder.ts` is now more of a readability tradeoff than an obvious cleanup win

### Phase 2: Reduce Same-App Complexity

Goal:

- stop doing anything replay-like or attach-like during split/open/close that we do not absolutely need

Tasks:

1. audit split/open/close for remount sensitivity
2. minimize remount-induced restore work
3. remove any remaining redraw heuristics that were only needed because of replay-remount coupling

Primary scenario coverage:

- `TR-402`
- `TR-303`

Status:

- complete for the current same-app cleanup goal
- `Terminal.tsx` is much smaller than when this plan was written
- latest slice consolidated same-app remount hydration tracking in `usePaneRuntimeBinder.ts` so detach arming and remount hydration no longer live in separate per-pane refs
- latest slice also moved cached spawn args, pending geometry, committed geometry, geometry timers, and same-app remount status behind one per-pane lifecycle record in `usePaneRuntimeBinder.ts`
- latest slice folded deferred unmount cleanup and pane write-chain state into that same per-pane lifecycle record, leaving only the live object bindings in separate refs
- latest slice also moved queued PTY event backlog and pane input-subscription ownership into that per-pane lifecycle record, so the remaining standalone refs are mostly live `xterm`/handle bindings and runtime binding registrations
- latest slice extracted that per-pane lifecycle bookkeeping into `paneRuntimeLifecycleState.ts`, giving the binder a dedicated registry helper plus focused unit coverage
- latest slice extracted terminal utilities, runtime binding synchronization, pane controls, active-pane pruning, and runtime ensure retry logic into dedicated helpers; `usePaneRuntimeBinder.ts` is now back under 1k lines and is more obviously the orchestration layer
- latest slice removed the deferred post-resize redraw bounce for already-running same-app non-shell panes and collapsed that shell/non-shell fork out of `geometryLifecycle.ts`
- focused tests and the full fresh-install packaged-app matrix stayed green after that removal, so the redraw bounce now looks like retired repair logic rather than a live requirement
- the remaining binder mostly holds PTY write/replay orchestration, geometry/resize orchestration, and attach/remount orchestration
- Phase 2 no longer needs more extraction-for-extraction's-sake; the next high-value step is Phase 3 relaunch-policy tightening and documentation

### Phase 3: Tighten Relaunch Restore Model

Goal:

- make relaunch behavior explicit and bounded

Tasks:

1. document current replay source and byte budget
2. confirm whether `screen_snapshot` is still used anywhere agent-critical
3. bias relaunch attach toward bounded raw replay where possible
4. ensure replay is treated as provisional bridge only

Primary scenario coverage:

- `TR-205`
- `TR-204`
- `TR-502`

Status:

- blocked more by scenario stability than by lack of code structure
- `TR-205` is still the main place where product behavior and harness/bootstrap flakiness are hardest to disentangle

## Ranked Deletion Experiments

These are intentionally framed as removal experiments, not preservation work. The working assumption is that some terminal-rendering code was added defensively during regressions and may no longer be required.

### 1. Remove Same-Size Force-Redraw Plumbing

Files:

- [app/src/components/Terminal.tsx](/Users/victor.arias/projects/victor/attn/app/src/components/Terminal.tsx)
- [app/src/utils/terminalViewportLifecycle.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/terminalViewportLifecycle.ts)
- [app/src/utils/terminalResizeLifecycle.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/terminalResizeLifecycle.ts)
- [app/src/pty/geometryLifecycle.ts](/Users/victor.arias/projects/victor/attn/app/src/pty/geometryLifecycle.ts)

Why this is a good candidate:

- this is the remaining `forceRedraw` / `fit_same_size` / `visibility_flush_same_size` path
- the larger same-app redraw bounce was already removed and the packaged-app matrix stayed green
- that makes this same-size redraw path look like likely leftover repair logic rather than a proven requirement

Main regression risk:

- a pane that becomes visible again without changing cols/rows might temporarily show stale paint until live output arrives

Minimum verification:

- `Terminal.test.tsx`
- `terminalViewportLifecycle.test.ts`
- `TR-402`
- `TR-303`
- session switch away/back
- hide/show pane or app visibility toggle

Status:

- executed on 2026-04-10
- removed from production code in `Terminal.tsx`, `terminalViewportLifecycle.ts`, `terminalResizeLifecycle.ts`, and `geometryLifecycle.ts`
- focused frontend tests passed:
  - `Terminal.test.tsx`
  - `terminalViewportLifecycle.test.ts`
  - `terminalResizeLifecycle.test.ts`
  - `geometryLifecycle.test.ts`
  - `usePaneRuntimeBinder.test.ts`
- packaged-app canaries after fresh `make install-all`:
  - `TR-402 local codex`: passed
  - `TR-402 local claude`: passed in the later full-matrix rerun
  - `TR-303 local codex`: passed
- full fresh-install packaged-app serial matrix passed on 2026-04-10:
  - `TR-205 remote codex`
  - `TR-205 remote claude`
  - `TR-504`
  - `TR-402 local codex`
  - `TR-402 local claude`
  - `TR-303 local codex`
- current read: this experiment is now matrix-backed and should be treated as the current baseline rather than a tentative branch

### 2. Remove Attach-Time Redraw Bounce And Shell-Specific Redraw Knobs

Files:

- [app/src/hooks/useDaemonSocket.ts](/Users/victor.arias/projects/victor/attn/app/src/hooks/useDaemonSocket.ts)
- [app/src/pty/attachPlanning.ts](/Users/victor.arias/projects/victor/attn/app/src/pty/attachPlanning.ts)
- [app/src/pty/runtimeLifecycle.ts](/Users/victor.arias/projects/victor/attn/app/src/pty/runtimeLifecycle.ts)

Why this is a good candidate:

- `sendPtyRedraw`, `redrawRequired`, and `forceShellRedraw` look like the relaunch-era cousin of the same heuristic already removed from same-app resize handling
- this is also one of the biggest remaining shell-vs-non-shell forks

Main regression risk:

- relaunch or fresh shell attach could look stale briefly before live output arrives

Minimum verification:

- `runtimeLifecycle.test.ts`
- `useDaemonSocket.test.tsx`
- `TR-205`
- `TR-502`

### 3. Collapse Viewport Resize Scheduling Branches

Files:

- [app/src/utils/terminalResizeLifecycle.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/terminalResizeLifecycle.ts)
- [app/src/utils/terminalViewportLifecycle.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/terminalViewportLifecycle.ts)

Why this is a good candidate:

- `idle_resize_both`, `debounce_x`, and `resize_y_then_debounce_x` look like layered caution on top of the binder’s own settled geometry scheduling

Main regression risk:

- more resize churn or a return of width-instability during large-buffer reflow

Minimum verification:

- `terminalResizeLifecycle.test.ts`
- `TR-402`
- manual split-open/close spam
- real window resize drag

### 4. Remove Zero-Delay Detach Cleanup

Files:

- [app/src/components/SessionTerminalWorkspace/usePaneRuntimeBinder.ts](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/usePaneRuntimeBinder.ts)
- [app/src/components/SessionTerminalWorkspace/paneRuntimeLifecycleState.ts](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/paneRuntimeLifecycleState.ts)

Why this is a good candidate:

- the zero-delay unmount cleanup timer reads like leftover race-management code

Main regression risk:

- fast detach/remount could break remount hydration or input-subscription ordering

Minimum verification:

- binder remount tests
- `TR-205`
- `TR-402`
- `TR-303`

### 5. Gate Or Trim Hot-Path Diagnostics

Files:

- [app/src/utils/paneRuntimeDebug.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/paneRuntimeDebug.ts)
- [app/src/utils/terminalRuntimeLog.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/terminalRuntimeLog.ts)
- [app/src/utils/runtimeTimeline.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/runtimeTimeline.ts)

Why this is a good candidate:

- several explorers called out duplicate or always-on logging work that still happens when debugging is effectively off

Main regression risk:

- weaker post-mortem evidence when a failure reproduces unexpectedly

Minimum verification:

- UI automation debug dump flows
- scenarios that explicitly inspect pane/runtime traces

## Additional Scenario Gaps Worth Covering After Cleanup Starts

Do not block the initial simplification on all of these, but add them when the cleanup touches the behavior.

### 1. `TR-201 Relaunch Preserves Existing Split Session`

We cover aggressive relaunch split churn through `TR-205`, but a more direct “reopen existing split session and simply inspect it before new actions” scenario would sharpen restore regressions.

### 2. `TR-204 Relaunch Restore Keeps Formatting`

We still need stronger direct formatting/color coverage instead of inferring that from generic healthy content.

### 3. `TR-301 Utility Focus Survives Session Switch`

The focus/utility path is still important and can regress independently of rendering cleanup.

### 4. `TR-401 Window Resize Preserves Render Health`

Split churn is covered better than raw app window resize right now.

## Things To Avoid

- Do not reintroduce frontend visible-frame replay as a restore mechanism.
- Do not add new product-side viewport-forcing behavior just to make the harness happy.
- Do not use packaged-app parallel runs as evidence.
- Do not discard harness failures until you confirm whether they are:
  - real product regression
  - viewport-only quirk
  - screenshot-source issue
  - late-event/state race

## Fast Verification Checklist

After each simplification slice:

```bash
cd /Users/victor.arias/projects/victor/attn
make install-all

cd /Users/victor.arias/projects/victor/attn/app
pnpm exec vitest run src/hooks/useDaemonSocket.test.tsx scripts/real-app-harness/scenarioAssertions.test.mjs scripts/real-app-harness/terminalScreenSnapshot.test.mjs
ATTN_REAL_APP_PATH="$HOME/Applications/attn.app" pnpm run real-app:serial-matrix
```

Practical note:

- if the packaged-app matrix result surprises you, verify the installed app provenance before diagnosing the product; an older installed bundle can invalidate the result
- if `real-app:serial-matrix` fails only in remote `TR-205`, rerun `pnpm run real-app:scenario-tr205` in isolation before concluding the last product change regressed behavior
- recent failures there have often been bootstrap/native-capture issues rather than app rendering regressions
- the focused fast-check that passed for this slice was:
  - `cd /Users/victor.arias/projects/victor/attn/app && pnpm exec vitest run src/components/SessionTerminalWorkspace/usePaneRuntimeBinder.test.ts src/pty/geometryLifecycle.test.ts && pnpm exec tsc --noEmit`

If the slice touches daemon-side PTY/replay code:

```bash
cd /Users/victor.arias/projects/victor/attn
go test ./internal/daemon ./internal/pty ./internal/hub ./internal/workspace
```

## What Success Looks Like

- the code clearly distinguishes relaunch restore from same-app resize/split churn
- there are fewer replay-related conditionals in ordinary split/close logic
- there are fewer redraw heuristics whose purpose is unclear
- the serial packaged-app matrix stays green
- the remaining scenario gaps are about new coverage, not about unstable fundamentals
