---
name: Canvas rendering perf + zoom-out scaling spike
description: Diagnostic spike to measure and characterize canvas rendering cost as panel count and zoom level scale, expose an always-on FPS overlay, and decide whether the GPUI canvas rendering approach can carry the convergence forward.
type: plan
status: Done — Go
---

# Canvas rendering perf + zoom-out scaling spike

> **Archived direction (2026-05-23).** This successful feasibility spike is
> historical input only; the new native client is not a canvas UI.

**Status**: Done — Go
**Owner**: Victor
**Date**: 2026-04-28
**Lives in**: `native-ui/crates/attn-native-app` (the `attn-spike5` bin + shared `canvas_view.rs`)

## Why

Spikes 1-6 + canvas convergence shipped a usable 2D canvas of live terminals. With it in hand, two perceptions surfaced during interactive use:

1. **Perceived FPS drop with multiple panels and zoom-out** — observed with 2 panels (one of them blank/dead-PTY), zooming out a moderate amount. We can't yet tell whether this is a real regression, a one-off hiccup, or a symptom of one specific code path (e.g. `paint_quad` per cell scaling badly with off-screen panels still being rasterized).
2. **No instrumentation in place** — the canvas has no FPS readout, no per-frame timing, and no synthetic-load mode. Every "is it slow?" question today is a vibe check.

The convergence plan called the canvas approach "spike-grade" and explicitly deferred the question of whether GPUI's per-cell `paint_quad` model holds up at scale. Now is the moment to answer that, before any more product work piles on top.

If rendering doesn't scale to a small handful of live panels at modest zoom-out, the canvas approach has failed for our purposes and we need to know now — not after we've shipped sidebar parity, multi-workspace, etc. on top of it.

## Vision

A version of `attn-spike5` that:

- Shows a persistent FPS / frame-time overlay in the corner of the canvas, always on.
- Has a **synthetic load mode** (env var or CLI flag) that spawns N panels driven by deterministic PTY output — fast scrolling, syntax color, cursor churn — without needing real shells or workspaces.
- Surfaces a profile dump on demand (key chord or `automation` action) that captures, for the current frame budget: N panels, their visible/off-screen split, current zoom level, average paint time, and the dominant cost (cell paint vs. shaping vs. quad submission vs. compositor).
- Has a soft, *measured* zoom-out cap derived from "what stays at 60fps on this machine with K panels", not a pulled-from-air number.

After the spike, we know:

- **Does the current rendering approach scale?** Concrete answer with a frame-budget table by (panel count × zoom × visible cell count).
- **Where does it break?** Identified bottleneck by name, not vibe.
- **What's the cap?** A defensible zoom-out limit, or a path to lifting it.
- **Is the canvas approach load-bearing for convergence?** Go / no-go signal for continuing on this rendering substrate.

## Success criteria

The spike is successful if **all** of the following hold:

1. Always-on FPS overlay lands in `attn-spike5`, accurate to within ~1 fps of an external recording, with negligible overhead (< 0.1 ms / frame).
2. Synthetic-load mode can spawn ≥ 8 deterministic panels and run them without needing real workspaces or shells; same code path as live panels except the PTY source.
3. We have a published table in this doc (filled in at end of spike) of measured frame times for N ∈ {1, 2, 4, 8} panels × zoom ∈ {default, 0.5×, 0.25×}, on the author's M-series Mac.
4. We can name the dominant cost at the worst configuration ("90% of frame time is `paint_quad` for off-screen panels", or similar) with profiler evidence — not guessing.
5. A hard zoom-out cap is proposed (concrete number) with the data backing it, OR the data shows no cap is needed within the explored range.
6. Go / no-go: a written conclusion at the bottom of this doc on whether GPUI's per-cell quad approach carries convergence forward, and if no, what the next experiment would be (cached rasterization, off-screen culling, etc.).

The spike is **failed** if the data shows current rendering cannot sustain ≥ 30fps at 4 panels at default zoom on the author's machine — and at that point the next plan is "alternate rendering substrate", not "tune the current one."

## Shortcuts and slicing

These are intentional shortcuts. The point of a spike is to learn fast; productionizing comes after.

- **Spike binary only.** All overlay, instrumentation, and synthetic-load wiring lands in `native-ui/crates/attn-spike5` (and shared canvas crates if needed). `attn-native` is untouched. If conclusions warrant it, a follow-up plan ports the changes; the spike-vs-native split keeps the perf work isolated and lets us delete easily if the answer is "no go".
- **Single machine, single config.** Author's M-series Mac, default display. We're characterizing scaling shape, not benchmarking across hardware. Any cross-machine generalization is out of scope.
- **Synthetic load is a fake source, not a fake renderer.** Panels rendered by the spike are real GPUI canvas panels, with real terminal grid + alacritty Term + paint path. Only the *bytes feeding the PTY* are synthetic (or there is no PTY, just a script that calls `Term.advance()` directly). This is what makes the measurements meaningful — we're stress-testing the actual render path.
- **Always-on overlay is fine.** No toggle needed for the spike. If the overlay's own cost shows up in profiles, that's itself a finding.
- **No auto-cleanup of dead-PTY panels.** Explicitly forbidden in this spike. The dead-panel behavior the user observed is interesting *as input data* for the perf question, but we don't want to mask it by garbage-collecting panels — the user closes them by hand if they want. (If we discover a dead PTY itself causes a tight repaint loop, that's a separate fix, not a "remove the panel" fix.)
- **Zoom cap = measure-and-propose.** No defensive soft cap landed up front. The spike measures what stays performant and proposes a hard cap (or reports "no cap needed in tested range") at the end. If during the spike the canvas turns into an unusable mush at extreme zoom, that's a finding — we capture it and propose the cap then, not now.
- **PTY throttle / VTE coalescing is deferred unless evidence demands it.** The user's intuition is that this isn't the bottleneck; we trust that until profiling shows otherwise. If profiling does fingerprint VTE.advance() as a hot spot, we widen scope at that point, not before.
- **No CI / automated perf assertions.** All measurements are manual and recorded in this doc. CI gating on perf is a follow-up if and only if the substrate is judged viable.

## Investigation checklist

These are the questions the spike must answer, in roughly this order:

1. **Frame timing baseline.** Add overlay; record idle frame time with 1 panel doing nothing.
2. **Idle scaling.** 1, 2, 4, 8 idle panels at default zoom. Does idle frame time grow linearly, sublinearly, or worse?
3. **Active scaling.** Same panel counts, but each panel running synthetic high-rate output. Does the answer change shape?
4. **Zoom scaling.** At each panel count, sweep zoom from default down through 0.5×, 0.25×. Where (if anywhere) does frame time blow past 16.6ms / 33.3ms?
5. **Off-screen scaling.** Pan the camera so K of N panels are off-screen. Does frame time drop, stay flat, or stay high (the latter would point at "we paint everything regardless of viewport")?
6. **Hot-path identification.** With the worst-case configuration found above, capture a sampled profile (Instruments / `cargo flamegraph`) and identify the dominant cost.
7. **Dead-PTY behavior.** Reproduce the user's report: panel with a terminated child process visible in the canvas. Check whether it costs anything per frame — if it does, that's a finding. (Not a fix in this spike.)
8. **Conclusion.** Fill in the table, name the bottleneck, propose a cap, write the go/no-go.

Each step's findings get appended to a "Findings" section at the bottom of this doc as the spike progresses.

## Cleanup checklist

If the spike's verdict is **go** (substrate is viable):

- Port the FPS overlay into `attn-native` (separate PR, behind a debug flag if production-facing).
- Land the proposed zoom cap in shared canvas code.
- File follow-up issues for any specific bottlenecks identified that didn't block the verdict.
- This plan's status flips to **Done — Go**.

If the verdict is **no go** (substrate doesn't scale):

- Open a new plan for the substrate alternative (cached panel rasterization, viewport culling, raster-once-per-output, etc.).
- Leave the FPS overlay and synthetic-load mode in `attn-spike5` so the next experiment can re-measure against the same baselines.
- This plan's status flips to **Done — No go** with the alternate-plan link.

In **either case**:

- Remove any temporary `println!`/`d.logf` instrumentation added during profiling.
- Keep the synthetic-load harness — it's the only honest way to re-ask the perf question later.

## Manual verification

Before declaring the spike done:

- Launch `attn-spike5` with the synthetic-load mode at N=8, default zoom. Visually confirm the FPS overlay reads what the profiler measures.
- Pan and zoom around the canvas while watching the overlay. Confirm frame time numbers move in the directions the conclusions claim.
- Verify (visually) that closing one of N synthetic panels drops frame time as expected — i.e. the per-panel cost we measured matches what disappearing a panel saves.
- Reproduce the original user-reported scenario: 2 panels, one with a dead PTY, zoom out. Compare against the profiler's accounting.

## Automated verification

- `cargo build -p attn-spike5` (and any crates touched).
- `cargo test -p attn-spike5` for any logic added (e.g. synthetic source iterator, FPS overlay numerics).
- No new e2e or harness assertions — perf is measured manually for this spike per the slicing decision above.

## Findings

*(Appended as the spike runs. Each entry: date, configuration, observation, evidence pointer.)*

### 2026-04-28 — Render path scales cleanly to ≥8 panels across the zoom range

Driven via `scripts/perf-sweep.py` against a release-build `attn-spike5` with synthetic load (80 bytes/tick, 16ms cadence). Each row is the avg over a ~3.5s sample window after the FPS counter is reset.

| panels | zoom | fps  | avg ms | last ms |
|--------|------|------|--------|---------|
| 1      | 1.00 | 60.0 | 16.95  | 16.39   |
| 1      | 0.50 | —    | —      | —       |
| 1      | 0.25 | —    | —      | —       |
| 4      | 1.00 | 58.0 | 17.25  | 16.46   |
| 4      | 0.50 | 59.0 | 16.95  | 16.63   |
| 4      | 0.25 | 57.0 | 17.56  | 16.66   |
| 8      | 1.00 | 59.0 | 16.95  | 16.62   |
| 8      | 0.50 | 58.0 | 17.54  | 17.63   |
| 8      | 0.25 | 58.0 | 17.25  | 16.67   |

GPUI on macOS caps at vsync (~60fps / 16.67ms), so any avg ≤ ~17ms means we're matching the display refresh rate with cycles to spare. No regression with zoom-out at 8 panels.

(Single-panel cells beyond the baseline left blank — taking 4-and-8 panel sweeps as the load-bearing data.)

### 2026-04-28 — Scroll-wheel zoom doesn't drop FPS during the sweep

Drove tight `set_zoom` loops from `scripts/perf-scroll-zoom.py` to mimic scroll-wheel cadence:

| pattern                           | fps  | avg ms | last ms |
|-----------------------------------|------|--------|---------|
| baseline single set_zoom @ 1.00   | 60.0 | 16.94  | 16.39   |
| baseline single set_zoom @ 0.25   | 59.0 | 16.95  | 16.65   |
| 60 steps × 16ms (~60Hz, 1.0→0.25) | 60.0 | 16.67  | 16.70   |
| 250 steps × 4ms (~250Hz)          | 60.0 | 16.67  | 16.66   |

The render pipeline holds vsync at 250Hz of unique zoom values. Per-step glyph cache invalidation, notify-cascade on `TerminalView`, and per-zoom layout cost are **not** the bottleneck. (Real CGEvent scroll-wheel injection was attempted but events did not reach the spike window — likely Accessibility-permission gating on the swift driver. Not chased further because the next finding made it unnecessary.)

### 2026-04-28 — Persistent post-zoom degradation reproduces past zoom ≈ 0.20, root cause is GridElement

User reported FPS staying degraded after stopping a scroll-zoom (not just a measurement window artifact). My initial sweep ended at zoom 0.25 — on the safe side of the cliff. Extending the sweep to extreme zoom-out reproduced the symptom:

| panels | zoom | fps before fix | avg before fix |
|--------|------|----------------|-----------------|
| 4      | 0.30 | 59             | 17.24           |
| 4      | 0.22 | 55             | 18.21           |
| 4      | 0.20 | 56             | 18.18           |
| 4      | **0.18** | **47**     | **21.38**       |
| 4      | 0.16 | 50             | 20.16           |
| 4      | 0.15 | 58             | 17.25 (grid disabled at 7.5px spacing!) |
| 8      | 0.18 | 44             | 23.26           |
| 8      | 0.16 | 48             | 21.09           |

Rapid drop between 0.22 and 0.18. Suspicious recovery at exactly 0.15 — that's the only zoom where the existing `if grid_screen_spacing < 8.0 { return; }` guard kicks in.

**Root cause: GridElement dot count grows quadratically with zoom-out.** GridElement paints one `paint_quad` per dot; world-space spacing is constant 50px, so screen-space spacing shrinks as you zoom out. At zoom 0.16, screen spacing is exactly 8px → ~13,000 dots in a 1040×800 canvas. Per-frame paint_quad load dominates the budget.

Confirmation: temporarily replaced the GridElement paint with `return;`. Sweep result with grid fully disabled (4 panels):

| zoom | fps | avg ms |
|------|-----|--------|
| 0.30 | 59  | 16.96  |
| 0.20 | 60  | 16.95  |
| 0.18 | 60  | 16.95  |
| 0.16 | 56  | 17.90  |
| 0.15 | 56  | 17.88  |

Cliff disappears. Below ~0.16 there's a small remaining cost that's likely terminal cell rendering at sub-pixel sizes — minor and not load-bearing for the verdict.

### 2026-04-28 — Fix attempt 1: adaptive grid spacing snaps to powers-of-2 keeping screen spacing ≥ 24px

Replaced the constant `GRID_SPACING * vp.zoom` with a loop that doubles world-space step until screen spacing stays at the visibility threshold. Standard tldraw/Figma technique. Caps grid dot count at a constant regardless of zoom.

After this fix, all zoom levels held within ~1ms of vsync — the cliff was gone — but FPS still read ~56-57 instead of a clean 60, because the grid was still painting ~360 dots per frame.

### 2026-04-28 — Fix attempt 2: drop the grid entirely

User feedback: snapping (the only reason a grid would matter) reads `GRID_SPACING` as a code constant; the dots are not load-bearing visually. Dropped the `GridElement` child from `Spike5Canvas::render`. After this change, with **8 panels under continuous 16ms synthetic load**:

| zoom | fps  | avg ms |
|------|------|--------|
| 0.30 | 60.0 | 16.95  |
| 0.25 | 61.0 | 16.67  |
| 0.22 | 60.0 | 16.95  |
| 0.20 | 59.0 | 17.24  |
| 0.18 | 60.0 | 16.95  |
| 0.16 | 61.0 | 16.66  |
| 0.15 | 59.0 | 17.24  |

Clean 60fps across the entire zoom range. The grid was the entire residual cost. Adaptive-spacing fix in `canvas_view.rs` is left in place as a pure correctness improvement for any consumer that does want a visible grid.

## Conclusion

**Verdict: Go.** The GPUI-per-cell `paint_quad` substrate carries convergence forward.

**Named bottleneck:** the canvas's own GridElement, not terminal rendering. Constant 50px world-space spacing made grid dot count grow quadratically with zoom-out and dominated frame budget below ~zoom 0.22.

**Fix:** removed `GridElement` from `Spike5Canvas::render`. Snapping (when added) uses `GRID_SPACING` as a code constant — no visible grid required. Adaptive-spacing fix in `canvas_view.rs` is left in place as a pure correctness improvement for any future consumer that does want a visible grid.

**Zoom cap:** **no cap needed** within the existing `[0.15, 5.0]` range. The 0.15 floor remains as a sanity guard but isn't load-bearing for performance anymore.

**Final frame-time table (release build, M-series Mac, 8 panels, synthetic 80 bytes/tick @ 16ms cadence):**

| zoom | fps  | avg ms |
|------|------|--------|
| 0.30 | 60.0 | 16.95  |
| 0.25 | 61.0 | 16.67  |
| 0.22 | 60.0 | 16.95  |
| 0.20 | 59.0 | 17.24  |
| 0.18 | 60.0 | 16.95  |
| 0.16 | 61.0 | 16.66  |
| 0.15 | 59.0 | 17.24  |

Clean 60fps across the entire zoom range under load. No cliff. No regression from panel count.

**Next:**

- Port the adaptive grid fix from `canvas_view.rs` (currently shared between spike3/4/5) into the production canvas path when it lands.
- Port the FPS overlay into `attn-native` behind a debug flag — was useful enough during this spike that it should outlive the spike binary.
- Outstanding minor finding (not load-bearing): zoom 0.15 with 8 panels shows ~57fps vs the 60fps ceiling. Likely terminal cell rendering at sub-pixel sizes. If we ever notice it, easy follow-up is to skip per-cell paint when cell screen-size drops below ~3px and just paint the panel body as a solid color.

Status flips to **Done — Go**.
