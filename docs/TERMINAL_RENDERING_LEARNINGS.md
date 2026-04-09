# Terminal Rendering And Harness Learnings

Status: Active working notes

This document records what we learned while chasing recent PTY, split, relaunch, and remote-agent regressions in the packaged app.

It exists to prevent a familiar failure mode:

- see a visual regression
- change terminal behavior quickly
- later discover the app was fine and the harness was wrong

Use this alongside:

- [PTY_RENDERING.md](/Users/victor.arias/projects/victor/attn/docs/PTY_RENDERING.md)
- [TERMINAL_TEST_ARCHITECTURE.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_TEST_ARCHITECTURE.md)
- [TERMINAL_REGRESSION_SCENARIOS.md](/Users/victor.arias/projects/victor/attn/docs/TERMINAL_REGRESSION_SCENARIOS.md)

## What We Confirmed Was Real

### 1. Geometry can be correct while rendered content is stale

Several failures showed:

- pane bounds were correct
- terminal `cols/rows` were correct
- workspace layout was correct
- visible content was still wrapped to an older, narrower width

This happened most clearly after relaunch, split churn, and pane close/re-expand.

Implication:

- layout correctness is not enough
- `fit()` correctness is not enough
- terminal resize signals are not enough
- we must prove that the process actually rerendered for the new geometry

### 2. Attach replay is a convenience path, not a redraw primitive

The most useful failure traces showed this pattern:

- client asked for one geometry
- daemon attach replay returned content captured at an older geometry
- replay was applied to a fresh xterm
- no later live output arrived to repair it

Implication:

- replay can make a pane look healthy while actually pinning it to stale wrapping
- replay compatibility must be treated as a first-class decision
- stale replay after relaunch/remount is a high-risk path

### 3. Closing a split is a real rendering case, not just a layout case

Opening a split and closing a split are not symmetric from the user's perspective.

Closing a split must prove that:

- the surviving pane regains width
- the terminal geometry follows that width
- the agent actually repaints into reclaimed space

This is especially important for Codex and Claude because their header and transcript regions can stay visually stale even when the footer remains alive.

### 4. Frontend visible-frame replay is not state-equivalent to a live xterm buffer

A real first-launch Codex PTY capture plus a controlled xterm comparison showed:

- Codex resize bytes can rely on wrapped-line state already held by the terminal emulator
- replaying only a painted visible frame reproduces appearance, not that internal buffer state
- the same later resize bytes can recover correctly on a live xterm and fail on a replay-restored xterm

Implication:

- local visible-frame replay should not be used as the restore primitive for remounted non-shell panes
- it is acceptable as a characterization/debug tool, not as trusted terminal state
- true relaunch restore still needs daemon-backed replay as a provisional bridge when the process does not immediately repaint enough content on attach

### 5. Raw-byte replay preserves Codex resize recovery better than screen snapshots

We pushed the prototype one step further and compared three restore models with the same captured Codex bytes:

- full live xterm history
- daemon-style visible-frame `screen_snapshot`
- raw PTY byte replay into a fresh xterm

What happened:

- live history recovered the wide Codex header after the later widen-resize bytes
- daemon-style `screen_snapshot` did not
- raw PTY byte replay did

Implication:

- the current daemon `screen_snapshot` is not equivalent to raw replay for later Codex resize recovery
- if we want relaunch restore that remains resize-safe, bounded raw-byte replay is a better candidate than visible-frame snapshots

### 6. The raw replay budget may be smaller than we feared

For the captured first-launch Codex case:

- total `launch + resize_small` history was `93` chunks / about `16.0KB`
- the first suffix that still recovered the header after widen was `83` chunks / about `13.8KB`

Implication:

- a bounded raw-byte restore path may be operationally realistic
- we do not yet know the right general budget, but the prototype suggests we should measure real session distributions before assuming raw replay is too heavy

## What We Confirmed Was Harness Noise

### 1. Exact shell token matching produced false negatives

We saw failures where the harness reported:

- timed out waiting for a token

while the failure artifact itself already contained the token in the pane text tail.

Implication:

- exact echoed-token assertions were too brittle for latency and shell readiness checks
- text-change and tolerant marker matching are safer for these steps

### 2. Healthy output can fail naive preservation checks after width reduction

We saw main Codex panes fail preservation because:

- the header reflowed
- lines wrapped or truncated differently
- exact anchor matching dropped below the threshold

even though the pane still contained meaningful, healthy content.

Implication:

- preservation checks must be semantic and resize-aware
- they should reject blank/footer-only collapse
- they should not require literal line preservation across narrower widths

### 3. Some remote failures were startup readiness races, not render failures

More than once the harness failed while the user could already see healthy content shortly after.

Implication:

- readiness gates for remote agent panes must require actual visible content, not just:
  - pane existence
  - terminal readiness
  - initial geometry

## Mistakes We Made

### 1. Changing the typing path before proving typing was the problem

We changed automation typing to synthetic full-string input in the wrong layer.

Result:

- main agent typing regressed
- the change did not address the real remote race

Rule:

- do not change typing/input delivery unless the evidence directly points there

### 2. Treating every harness failure as an app bug

This wasted time repeatedly.

Examples:

- token assertion failed when token was already present
- preservation failed on valid width-induced reflow
- remote pane failed before content had actually arrived

Rule:

- before changing app behavior, ask whether the artifact proves:
  - app bug
  - harness bug
  - ambiguous signal

### 3. Trusting geometry-only and buffer-only signals

We repeatedly found that these were insufficient on their own:

- pane width
- terminal `cols/rows`
- workspace model
- full buffer text

Rule:

- agent panes need visible-content proof
- render regressions need both semantic and visual evidence

## Test Automation Learnings

### 1. Real Codex and Claude sessions must stay the acceptance target

Synthetic tests are useful for assertion validation, not signoff.

Why:

- the real product path includes PTY, worker, replay, relaunch, focus, resize, and agent-specific rendering behavior
- many regressions only show up with actual agent output

### 2. The harness needs layered proof

The most reliable model so far is:

1. workspace and pane geometry
2. terminal readiness and transport traces
3. visible-row semantic content
4. native pane paint coverage

No single layer is enough.

### 3. Healthy baseline gating matters

A scenario should not assert preservation against a bad baseline.

Examples of bad baselines:

- blank main pane
- panic screen when the scenario expects welcome/header content
- shell prompt not yet ready

Rule:

- fail early as `baseline_unhealthy`
- do not continue into preservation checks with a broken baseline

### 4. The harness itself needs tests

When assertions get sophisticated, they need their own test surface.

Current examples:

- visible-content extraction
- native pane paint analysis
- tolerant before/after coverage comparisons
- pane text change helpers

Rule:

- any non-trivial assertion should have isolated tests before it is trusted in a real-agent scenario

### 5. Artifact quality determines debugging speed

The most useful artifacts were:

- native window screenshots
- pane-cropped image analysis
- visible-row summaries
- runtime transport traces
- structured workspace snapshots

Rule:

- if a failure does not produce enough evidence to distinguish app vs harness, the artifact bundle is incomplete

## Rendering-Specific Learnings

### 1. "Terminal ready" is not equivalent to "rendered correctly"

A pane can be:

- `ready`
- `visible`
- correctly sized

and still show stale or underfilled content.

### 2. Agent panes need different assertions than shell panes

Shell panes are tolerant of:

- short prompts
- narrow echo regions
- intermittent line wrapping

Agent panes are not.

For agent panes, we care about:

- meaningful transcript visibility
- header or body presence
- visible width usage
- recovery after resize, split, close, and relaunch

### 3. Reclaimed-space recovery is its own scenario

A pane that survives split-open may still fail split-close.

That is why `TR-402` and `TR-205` need to exist independently of split-creation scenarios.

## Operational Learnings

### 1. Remote runs can poison later runs if cleanup is incomplete

Observed symptoms:

- `resource temporarily unavailable`
- startup failures
- remote shells or workers apparently left behind

Implication:

- cleanup must become a first-class scenario
- repeated remote harness runs should be treated as a lifecycle test, not just a render test

### 2. Restart-dependent bugs deserve restart-dependent scenarios

We did not reproduce the close/re-expand bug on a fresh remote session.
We did reproduce it after:

- split
- app quit
- reopen same session
- split more
- close again

Implication:

- relaunch changes state in ways that fresh-session tests do not cover

## Working Rules Going Forward

Before changing terminal behavior:

1. Decide whether the evidence points to app, harness, or ambiguity.
2. Verify the scenario has a healthy baseline.
3. Prefer instrumentation and artifact quality over speculative fixes.
4. Do not use attach replay as a generic redraw solution.
5. Do not change input delivery unless the failure is clearly input delivery.

Before trusting a scenario:

1. Make sure the assertion itself has isolated tests if it is non-trivial.
2. Make sure the scenario proves visible rendered content, not only geometry or buffer text.
3. Make sure failure artifacts can explain what went wrong without requiring live observation.

## What Still Seems Most Likely

The current strongest rendering hypothesis is:

- relaunch/remount paths can apply stale attach replay from an older geometry
- the pane then regains width without receiving authoritative output that repaints the old content
- the footer remains alive, which makes the pane look partially healthy even while the header/body stay stale

That hypothesis should keep being tested against `TR-205` until either:

- it is disproven by stronger traces
- or the replay/reconcile path is fixed and the restart-close recovery scenario becomes stable
