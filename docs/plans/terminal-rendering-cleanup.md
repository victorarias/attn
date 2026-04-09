# Terminal Rendering Cleanup

Status: Proposed simplification plan
Owner: Next agent
Context date: 2026-04-09

## Why This Exists

The terminal/PTy/UI stack is finally in a state where the important packaged-app canaries are green, which gives us room to simplify instead of continuing to patch behavior opportunistically.

The main opportunity now is to remove complexity that has accumulated around:

- replay vs redraw boundaries
- same-app remounts vs true relaunch attaches
- split/close churn
- harness-driven verification hooks

This plan is intended as a clean handoff for a fresh agent.

Use it together with:

- [PTY_RENDERING.md](/Users/victor.arias/projects/victor/attn/docs/PTY_RENDERING.md)
- [TERMINAL_RENDERING_LEARNINGS.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_RENDERING_LEARNINGS.md)
- [TERMINAL_TEST_ARCHITECTURE.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_TEST_ARCHITECTURE.md)
- [TERMINAL_REGRESSION_SCENARIOS.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_REGRESSION_SCENARIOS.md)

## Current Protected State

At the time of writing, the serial packaged-app matrix is green:

- `TR-205` remote Codex relaunch/split/close
- `TR-205` remote Claude relaunch/split/close
- `TR-504` remote cleanup
- `TR-402` local Codex split-close recovery
- `TR-402` local Claude split-close recovery
- `TR-303` local Codex post-close typing

Run command:

```bash
cd /Users/victor.arias/projects/victor/attn/app
pnpm run real-app:serial-matrix
```

Important:

- packaged-app scenarios must run serially
- parallel runs are invalid evidence because they contend for the same live app automation surface

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

### 2. Minimize remount sensitivity

Question to answer:

- can the main terminal stay mounted through ordinary split churn more often than it does today?

If yes:

- delete binder complexity rather than papering over it

If no:

- at least document exactly which transitions still remount and why

### 3. Remove leftover redraw heuristics that are compensating for architecture

Watch for:

- bounce redraws that exist only because state is reconstructed too often
- resize/redraw sequences that are trying to heal stale replay instead of avoiding stale replay

### 4. Clarify pane-kind differences

Agent panes and shell panes do not necessarily need the same restore rules.

Be explicit if the correct behavior is:

- agent panes: replay-aware relaunch policy
- shell panes: simpler attach/recover model

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
cd /Users/victor.arias/projects/victor/attn/app
pnpm exec vitest run src/hooks/useDaemonSocket.test.tsx scripts/real-app-harness/scenarioAssertions.test.mjs scripts/real-app-harness/terminalScreenSnapshot.test.mjs
pnpm run real-app:serial-matrix
```

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
