# Terminal Test Architecture

Status: Active

This document describes how terminal, PTY, split-layout, and relaunch regressions should be tested in `attn`.

It complements [TERMINAL_REGRESSION_SCENARIOS.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_REGRESSION_SCENARIOS.md):

- the scenario matrix defines what must hold
- this document defines how the automated suite is structured

## Design Principles

- Real Codex and Claude sessions are the acceptance target.
- Deterministic or unit-style checks are only supporting layers for assertion correctness and plumbing.
- Geometry alone is not enough.
- Buffer text alone is not enough.
- For agent panes, the suite must eventually prove visible rendered content, not merely replayed or hidden content.

## Suite Tiers

### Tier 0: Logic And Protocol

Purpose:
- workspace split math
- attach/replay semantics
- geometry reconciliation
- focus ownership
- redraw routing

Examples:
- `workspace.go` split normalization tests
- binder and daemon attach/resize tests

### Tier 1: Deterministic Render Plumbing

Purpose:
- prove the harness and assertions themselves are trustworthy
- validate visible-content extraction and render-coverage checks

Examples:
- visible viewport content extraction
- xterm screen underfill detection
- pane-level content summary logic

This layer does not replace real-agent acceptance tests.

### Tier 2: Packaged-App Local Real-Agent

Purpose:
- run the packaged app against a real local agent session
- verify split, focus, and visible rendering behavior end to end

Current initial scenarios:
- `TR-101` via `scenario-tr101-claude-main-split.mjs`
- `TR-102` via `scenario-tr102-claude-utility-split.mjs`

### Tier 3: Packaged-App Remote Real-Agent

Purpose:
- verify the same behaviors through the full remote daemon / worker / websocket path
- verify relaunch, restore, and split persistence where regressions have been worst
- verify session teardown and app quit do not leak remote PTYs or agent workers across runs

Current initial scenario:
- `TR-502` via `scenario-tr502-remote-relaunch-splits.mjs`

### Tier 4: Perf And Stress

Purpose:
- transport budget
- resize churn
- repeated split/close cycles
- long-running render health

These should usually run nightly or pre-release rather than on every edit.

## Shared Harness Foundation

The initial foundation lives in:

- [scenarioRunner.mjs](/Users/victor.arias/projects/victor/attn/app/scripts/real-app-harness/scenarioRunner.mjs)
- [scenarioAssertions.mjs](/Users/victor.arias/projects/victor/attn/app/scripts/real-app-harness/scenarioAssertions.mjs)
- [scenarioAgents.mjs](/Users/victor.arias/projects/victor/attn/app/scripts/real-app-harness/scenarioAgents.mjs)
- [scenarioRemote.mjs](/Users/victor.arias/projects/victor/attn/app/scripts/real-app-harness/scenarioRemote.mjs)

Responsibilities:

- `scenarioRunner.mjs`
  Step logging, assertions, run context, artifact summary, failure summary.

- `scenarioAssertions.mjs`
  Shared waits and checks for pane visibility, pane focus, visible-content summaries, coverage, session artifacts, and split discovery.

- `scenarioAgents.mjs`
  Real-agent helpers, currently starting with Claude prompt readiness and structured-output prompting.

- `scenarioRemote.mjs`
  Isolated remote daemon bootstrap helpers for SSH-backed scenarios.

## Visible-Content Proof

The current bridge foundation now exposes pane-level visible viewport content:

- [terminalVisibleContent.ts](/Users/victor.arias/projects/victor/attn/app/src/utils/terminalVisibleContent.ts)
- threaded through the session workspace binder and UI automation bridge

This gives the harness:

- the currently visible rows
- per-line occupied-column measurements against terminal `cols`
- non-empty line count
- dense-line count
- visible character count
- maximum visible line length
- first and last non-empty visible lines

That now supports a first width-usage assertion:

- does visible content actually spread across the terminal width
- or is it collapsed into a narrow visible strip despite the pane itself being wide

This is the first real step away from treating:

- pane bounds
- PTY buffer text
- “terminal ready”

as if they were enough to prove on-screen rendering.

## Native Pixel Layer

The harness now also has a first native pane-pixel layer:

- capture a real macOS window screenshot
- crop it to one pane body using UI automation bounds
- infer screenshot scale from the saved PNG dimensions
- measure non-background pixel spread across rows and columns

That supports a stronger assertion:

- does painted content actually occupy the pane body
- or is it collapsed into a narrow strip or a bottom footer band

This is now wired into `TR-101`.
The same absolute-plus-tolerance model is now also used for restored panes in `TR-502`.

The intended verification model is range-based:

- absolute pane health thresholds determine whether one pane crop looks healthy on its own
- optional before/after delta tolerances determine whether a split, relaunch, or resize materially degraded an otherwise healthy pane

It is intentionally not a strict image-equality model.

## Current Gap

The remaining major gap is not screenshot capture anymore. It is calibration and reuse:

- tune thresholds across both Claude and Codex
- extend pixel assertions to more scenarios
- add pane-cropped assertions that distinguish real content from decorative chrome when needed

## Initial Review Targets

The first scenarios to review on top of the new foundation are:

- [scenario-tr101-claude-main-split.mjs](/Users/victor.arias/projects/victor/attn/app/scripts/real-app-harness/scenario-tr101-claude-main-split.mjs)
- [scenario-tr102-claude-utility-split.mjs](/Users/victor.arias/projects/victor/attn/app/scripts/real-app-harness/scenario-tr102-claude-utility-split.mjs)
- [scenario-tr502-remote-relaunch-splits.mjs](/Users/victor.arias/projects/victor/attn/app/scripts/real-app-harness/scenario-tr502-remote-relaunch-splits.mjs)
