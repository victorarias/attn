# Terminal Rendering And Harness Learnings

Status: Active working notes

This document records what we learned while chasing recent PTY, split, relaunch, and remote-agent regressions in the packaged app.

It exists to prevent a familiar failure mode:

- see a visual regression
- change terminal behavior quickly
- later discover the app was fine and the harness was wrong

Related context now lives in the packaged-app harness scripts and the release notes.

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

### 7. Raw replay is not enough to blame by itself

We added a second prototype case:

- restore a fresh xterm from raw Codex history that already represents the current wide state
- then run another narrow-to-wide resize cycle with the same captured resize bytes

What happened:

- the header still recovered
- the replay-restored xterm stayed healthy through the later resize cycle

Implication:

- the remaining relaunch bug in the app is less likely to be explained by "raw replay is inherently insufficient"
- more likely causes are:
  - the exact replay payload the app is applying differs from the prototype-quality history
  - or the app's attach/reconcile sequence after replay is damaging otherwise healthy restored state

### 8. xterm resize can keep the header in scrollback while jumping the viewport downward

The `TR-205` first-split prototype exposed a cleaner failure mode than "header bytes were lost":

- a healthy wide Codex buffer resized narrow with plain `xterm.resize()`
- the visible viewport jumped down into the tip/transcript area
- the header was still present in scrollback above the viewport
- `scrollToTop()` immediately made the header visible again

Implication:

- some "header disappeared" regressions are actually viewport-position regressions
- we should distinguish:
  - content missing from the buffer
  - content still present but hidden because the viewport drifted
- for agent panes near the top of the transcript, repinning the viewport after resize is a valid mitigation

### 9. Local first-split Codex header loss can be a pure viewport shift with no redraw bounce

We later reproduced the first-launch local Codex split-open case directly in the packaged app and checked the runtime trace at the same time.

What happened:

- before split, the main pane had the normal Codex header in view
- after split, `viewportY` increased further
- full pane text still contained the full Codex header
- the visible rows started in the middle of the header/tip/prompt region instead
- the trace showed only normal live PTY output and geometry scheduling
- there was no `terminal.mounted`, no `pty.attach.*`, and no `pty.redraw.requested`

Implication:

- not every split-open Codex regression is a remount or redraw bug
- some of them are viewport-drift bugs even when the buffer contents are intact
- the presence or absence of redraw events is a useful discriminator when deciding whether the app changed behavior or xterm/Codex simply moved the viewport

### 10. Typing after split-close did not trigger remounts or redraws in the live app

We added a direct packaged-app runtime-trace canary for:

- local Codex
- split main
- close split
- type into the surviving main pane

What happened:

- typing produced only live `pty.output.live` events
- it did not produce:
  - `terminal.mounted`
  - `pty.attach.*`
  - `pty.geometry.*`
  - `pty.redraw.requested`

Implication:

- if typing looks jumpy after split-close, typing itself is probably not the mechanism
- the more likely cause is bad terminal state left behind by the earlier resize/close path
- runtime-trace canaries are useful for proving whether a user-visible issue is caused by keypress side effects or by the prior layout transition

### 11. Local relaunch close-resplit can erase the Codex header from the buffer entirely

We later exercised the local Codex path that is closer to the user report:

- create a local Codex session
- split
- quit and reopen the packaged app
- close the restored split
- split again from `main`
- type into `main`

What happened in the nonrecovering variant:

- before typing, the visible main pane already no longer showed the Codex header
- full pane text no longer contained `OpenAI Codex`
- `scrollToTop()` restored the viewport to the top border, but the header text was still missing
- native window screenshots matched the missing-header state before typing, after typing, and after scroll

Implication:

- not every relaunch close-resplit Codex bug is a viewport-only quirk
- there is also a real buffer-loss failure class where the restored header bytes are gone
- local relaunch header regressions need their own dedicated scenario instead of being inferred from `TR-205` or first-launch split canaries

### 12. Replayed Codex history can answer old terminal queries again

We then froze the nonrecovering relaunch payload itself and replayed it into a plain xterm outside the app.

What that showed:

- the bad replay payload still contained `OpenAI Codex`
- the daemon-side raw scrollback for that session still contained the header bytes too
- but replaying those bytes into a fresh xterm at `58x46` still produced a top-of-buffer view with the border and prompt but no header line
- while those historical bytes were being restored, xterm emitted live `onData` responses such as DA1 and CPR replies

Implication:

- the worker did not simply "forget" the header bytes
- relaunch replay is not passive for this payload; it can make a fresh terminal answer old queries from history
- duplicate query responses are a credible mechanism for why the next Codex shrink redraw becomes malformed after relaunch
- the right deterministic test level is the replay/query-response/xterm layer, not only the packaged-app relaunch canary

### 13. The destructive relaunch tail can be the exact earlier split bytes, not new relaunch-only output

For the `2026-04-10` `TR-205` remote relaunch-close-redraw failure, we compared:

- the relaunch main-pane `pty.attach.replay_applied` payload tail from `03-post-relaunch-terminal-runtime-trace.json`
- the earlier live main-pane `pty.output.live` bytes `seq 94-97` from `02-after-initial-split-terminal-runtime-trace.json`

What that showed:

- the replay tail was byte-for-byte the earlier live split redraw tail, with only a leading `ESC[?2026l` disable sequence prepended
- replaying the prefix alone left `OpenAI Codex` present with xterm cursor state at row `15`, col `3`
- replaying the embedded `CSI 6 n` emitted a fresh CPR reply of `ESC[15;3R`
- replaying the rest of that same historical tail then moved xterm to `1;1`, issued `CSI J`, and cleared the header before repainting only the anchor block/status line

Implication:

- this failure is not explained by "relaunch produced brand-new bad bytes"
- it is also not explained by pure viewport drift
- the relaunch restore path is re-executing a historical split redraw/query cycle in a fresh xterm state where that cycle becomes destructive
- the promising fix surface is replay selection/sanitization, not viewport forcing

### 14. Geometry-aware replay selection is the fix surface, but clipped raw tails are not state-safe

We then exercised the replay-selection fix directly on real packaged-app relaunches and on isolated xterm replays.

What that showed:

- `TR-205` and `TR-502` both restored the Codex main pane through `scrollback_segments`, not `screen_snapshot`
- the successful relaunch traces carried two geometry runs, one resize, and preserved both `OpenAI Codex` and the transcript anchor after replay
- the same bytes still lose the header if they are flattened into one final-geometry replay instead of being replayed across their original wide-to-narrow transition
- a transport-bounded segmented tail can still reconstruct the current visible frame while omitting older geometry context, so "matches the live screen snapshot" is not enough to call that clipped tail state-equivalent

Implication:

- full geometry-segmented replay is the right restore primitive for Codex relaunches when the daemon can verify it against the live screen model
- transport-clipped raw replay should not be treated as safe just because it paints the same current screen
- when a fresh live snapshot is missing, the least-wrong fallback is a geometry-aware derived snapshot from replay segments, not blind trust in raw replay bytes

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

### 4. The wrong screenshot source can completely invalidate native paint checks

We hit a case where the native paint harness captured the wrong app window entirely.

What happened:

- pane state and visible content said Codex was healthy
- the native screenshot crop showed an unrelated window instead
- the resulting paint failure looked severe but had nothing to do with the terminal under test

Implication:

- whole-pane native paint failures are only trustworthy if window selection is trustworthy
- prefer the app-owned screenshot path over a loose "frontmost window" capture
- when screenshot content and pane state disagree, treat the run as harness-invalid until proven otherwise

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

### 4. Viewport normalization belongs in the harness when the viewport quirk is accepted

For Codex, some split-open cases currently leave the viewport a few rows lower even though the header is still present in the buffer.

If we decide that this viewport drift is acceptable for now, then the test should normalize the viewport before diffing semantic content.

Implication:

- the harness may `scrollToTop()` before a preservation comparison when the goal is "did the content survive?"
- that normalization should not automatically become product behavior
- otherwise we risk "fixing" the app to satisfy a test when the actual decision was only about what the test should consider acceptable

### 5. Native paint thresholds must be agent- and phase-aware

Codex first-launch panes can be visibly healthy while using only a relatively small vertical portion of the pane:

- compact header box
- one or two tip/status lines
- prompt area near the bottom

Implication:

- baseline native paint thresholds that work for denser panes can be too strict for healthy Codex startup
- recovery comparisons should not fail just because a wider healthy pane redistributes painted pixels differently
- absolute coverage and semantic-content assertions are usually the stronger signal for these cases

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
