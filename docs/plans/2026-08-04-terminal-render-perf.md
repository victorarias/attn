# Terminal rendering performance

Streaming terminal output rebuilt and submitted the full visible grid at display
refresh rate even when a TUI changed only a progress row. Terminal panes stay
open all day, so that repeated work is a standing CPU, GPU, and battery cost.

This is the design record for reducing it: what was measured, what was kept, and
what was tried and rejected. The rejected entries are here so later work does not
repeat the measurements or trade responsiveness for an attractive microbenchmark.

## Guardrails

Any future change to this path is held to the same rules:

- Preserve every terminal byte and always paint the newest model state.
- Keep direct interaction paints immediate. A frame-rate policy applies only
  while PTY output is arriving continuously.
- Do not add a continuous repaint loop.
- Keep renderer-owned CPU staging bounded and reported. Every renderer sample
  carries both `retainedRowVertexBytes` (what the row cache adds) and
  `retainedStagingBytes` (that plus the frame buffer it concatenates into). A
  receipt that names only one of them understates the pane by roughly half.
- Run packaged-app measurements only in an isolated named profile and clean it
  up afterwards. Production `~/.attn` stays read-only.

## Measurement methods

### Packaged PTY burst

`bridge-pty-bench.mjs` injects a 2 MiB payload into one utility pane and records
transport time, process CPU/RSS samples, renderer paint count, and synchronous
renderer CPU time. This is a no-regression receipt for the complete path, but it
is too short to attribute small renderer changes reliably.

### Sustained PTY output

The same harness accepts `--chunk-delay-ms` so writes span many display frames.
This exposes paint cadence and CPU during continuous output instead of collapsing
the whole payload into one browser frame.

### Focused vertex assembly

A CPU-only benchmark assembles 3,200 quads for 250 frames. It isolates JavaScript
staging cost from PTY parsing, WebKit scheduling, and GPU submission.

### Dense in-place redraw

`bridge-pty-bench.mjs --payload progress` first fills every terminal row, then
streams 4 KiB carriage-return/erase-line updates for eight seconds. Because
output paints are paced, the harness waits for a paint that actually covers the
seed (a renderer paint count past the pre-seed value) rather than for a fixed
interval, and records the fixture's printable-cell count. A blank or
not-yet-painted fixture therefore cannot masquerade as a renderer win: the run
fails outright below 50% density. Optional window dimensions locate
size-dependent crossovers.

## Experiments

### 1. Reuse typed vertex staging - accepted

Change: replace per-paint `number[]` construction plus `new Float32Array(...)`
copies with a grow-on-demand reusable `Float32Array` shared by both terminal
renderers.

Focused result across five runs:

| implementation | 250 frames | relative | saved per frame |
| --- | ---: | ---: | ---: |
| `number[]` plus typed copy | 205.85-211.63 ms | 1.0x | - |
| reused typed buffer | 28.33-29.36 ms | 7.07-7.41x | 0.709-0.732 ms |

Packaged 2 MiB burst result:

| batching | before | after | renderer paints after | renderer CPU after |
| --- | ---: | ---: | ---: | ---: |
| x1 | 65 ms | 50 ms | 1 | 7 ms |
| x8 | 49 ms | 51 ms | 1 | < 0.5 ms |
| x32 | 49 ms | 50 ms | 1 | < 0.5 ms |

Decision: keep. The focused benchmark isolates the gain; the short packaged run
shows no transport regression but is intentionally not used to claim the x1
difference is entirely renderer work.

Memory: 128 KiB initially and about 2 MiB retained for a dense 151 x 46 pane,
replacing at least about 4.5 MiB of transient JavaScript and typed-array
allocations on every dense paint.

### 2. Cap sustained output paints - accepted

Hypothesis: output is already coalesced to one animation-frame callback, but
continuous output still drives a full-grid paint on every 60 Hz browser frame.
Capping continuous PTY-output paints at 30 Hz should roughly halve renderer and
GPU submission work while retaining immediate interaction paints and a final
paint of the newest model state.

Instrumentation change: add delayed chunks to the packaged harness so the same
payload crosses many frames. Record the baseline before changing scheduling.

Baseline, packaged `terminal-perf` profile, 240 x 4 KiB chunks with a requested
4 ms inter-chunk delay:

| batching | elapsed | paints | paints/s | renderer CPU | sampled CPU max | RSS max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| x1 | 1,932 ms | 115 | 59.5 | 27 ms | 110.8% | 597.9 MiB |
| x8 | 1,917 ms | 31 | 16.2 | 12 ms | 17.1% | 704.2 MiB |
| x32 | 1,900 ms | 9 | 4.7 | 1 ms | 14.2% | 705.0 MiB |

The timer is clamped to roughly 8 ms in this packaged WebView, so the x1 case
delivers about two writes per 60 Hz display frame. This is the useful cadence
baseline; elapsed time is pacing-controlled and not a throughput score.

Paired 8-second binary-path run, 1,000 x 4 KiB chunks:

| output cap | elapsed | paints | paints/s | total CPU avg | WebContent CPU avg | GPU CPU avg |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 60 Hz control | 8,005 ms | 473 | 59.1 | 18.7% | 10.56% | 4.38% |
| 30 Hz adaptive | 8,015 ms | 231 | 28.8 | 19.1% | 10.60% | 4.02% |

Decision: keep. Paint and GPU submission counts fell by 51% and GPU-process CPU
fell by about 8%. Whole-process CPU was flat, which shows model writes and other
PTY work dominate this particular stream. The scheduler adds no frame buffer or
continuous loop: the first output after idle paints on the next browser frame,
continuous output keeps only the newest model state at 30 Hz, and the final state
is painted within the same 33 ms budget. Direct interaction paints bypass it.

### 3. Skip string escape parsing for plain chunks - rejected

Attempt: scan each byte chunk for ESC and avoid UTF-8 decoding plus OSC 52 and
synchronized-output string scans when no escape protocol could be present.

Focused Node benchmark, 50,000 iterations over a 4,107-byte plain chunk:

| path | five-run range |
| --- | ---: |
| fresh `TextDecoder` plus three string searches | 35.32-37.02 ms |
| reused `TextDecoder` plus three string searches | 30.29-30.68 ms |
| `Uint8Array.indexOf(ESC)` fast path | 48.78-49.35 ms |

The typed-array scan was 33-40% slower than the existing decoder/string work in
this runtime. A packaged run was noisy in the same direction (18.7% to 25.8%
average sampled CPU), so the attempt was removed rather than justified by its
plausible-looking design.

### 4. Pack cell colors and cache codepoint glyph keys - accepted

Change: represent per-cell foreground/background colors as packed numbers and
look up ordinary single-codepoint glyphs by a numeric `(codepoint, style)` key.
This removes two RGB object allocations plus character/style key construction
for nearly every visible cell. Grapheme clusters keep the string-key path.

Focused 151 x 46 hot-loop benchmark, 500 frames, three five-run invocations:

| implementation | 500 frames | relative |
| --- | ---: | ---: |
| RGB objects plus string glyph keys | 26.25-30.52 ms | 1.0x |
| packed RGB plus numeric glyph keys | 15.32-18.38 ms | 1.55-1.92x |

The final-diff recheck produced paired speedups of 1.92x, 1.68x, 1.66x,
1.68x, and 1.70x; its absolute times stayed within the earlier envelope.

Decision: keep. The numeric map contains one extra pointer per atlas glyph and
clears with the atlas, so it is bounded by the existing glyph set rather than by
frame count or terminal history. It adds no full-frame copy and leaves the 2 MiB
vertex-staging guardrail unchanged.

Final packaged run with both retained optimizations, 1,000 x 4 KiB binary chunks
and a requested 4 ms delay:

| elapsed | processed | paints | paints/s | renderer CPU | scheduler requests / coalesced / deferred | RSS max |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 7,993 ms | 4,096,000 bytes | 233 | 29.2 | 37 ms | 1,004 / 772 / 247 | 653.3 MiB |

The whole-process CPU samples in this last run were noisy (23.1% average, 90.0%
p95, 155.5% maximum), so they are recorded rather than used to claim a delta.
The paired control in experiment 2 is the defensible CPU comparison.

### 5. Batch model writes until the next frame - rejected

Attempt: apply the first arriving write immediately, then combine subsequent
writes until the next animation frame, capped at 64 KiB. This reduced scheduler
requests from about 1,003 to 481, but did not remove the expensive model work and
delayed terminal state used by selection, queries, blocks, and automation.

Packaged 1,000 x 4 KiB progress run at the default pane size:

| path | CPU avg | paints | renderer CPU | measured write CPU |
| --- | ---: | ---: | ---: | ---: |
| direct writes | 25.2% | 228 | 14 ms | 84 ms |
| frame-batched writes | 20.2% | 227 | 18 ms | 79 ms |

Decision: reject. The five-millisecond write delta is too small and noisy to
justify making model state asynchronous. The prototype and its tests were
removed.

### 6. Paint only dirty framebuffer rows - rejected

Attempt: use Ghostty's partial dirty state, expand changed rows by one neighbor
for cross-row glyph pixels, scissor-clear those rows, and submit only their
vertices. A real-WASM probe (`scripts/bench-terminal-dirty-rows.mjs`) confirmed
that progress redraws are partial while scrolling, erase-display, and
alternate-screen transitions are full. That probe survived the rejection and is
the source of the cursor contract recorded in experiment 7.

Measured against a dense fixture, the design failed the quality guardrail rather
than the performance one: WebGL contexts default to `preserveDrawingBuffer:
false`, so untouched pixels are not guaranteed to survive compositing. Enabling
retention would add a device-pixel framebuffer — potentially tens of MiB for a
large Retina pane — rather than the bounded CPU staging memory allowed here.

Decision: reject. The scissored framebuffer path was removed. The real-WASM
dirty-state probe remains as a repeatable contract check.

### 7. Rebuild dirty rows into a bounded vertex cache - accepted above 2,048 cells

Change: retain each active-grid row in the existing CPU vertex format. On a
partial Ghostty update, rebuild the dirty row plus its neighbors, concatenate
the cached rows, then clear and submit the complete frame. This preserves exact
pixels without a retained framebuffer. Scrolled views, overlays, images, full
dirty states, atlas changes, and forced renders take or invalidate the full
path.

The complete frame still reaches the GPU, so this targets JavaScript cell/glyph
assembly rather than GPU fill. A size gate avoids paying the cache-copy overhead
when a small grid is cheaper to rebuild.

Packaged dense-progress A/B, 1,000 x 4 KiB binary chunks:

| split-pane fixture | control rows rebuilt | cached rows rebuilt | control renderer CPU | cached renderer CPU | cache memory | result |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| default, 31 x 25 | 5,500 | 440 | 15 ms | 22 ms | 0.160 MiB | reject cache |
| 1,785 printable cells | 8,085 | 462 | 57 ms | 33 ms | 0.382 MiB | measured win; below final gate |
| 3,212 printable cells | 10,252 | 458 | 66 ms | 46 ms | 0.680 MiB | keep cache |

At the medium point, renderer staging fell 42%; at the large point its per-frame
average fell from 0.283 ms to 0.201 ms (29%). The large run's total CPU average
also fell from 53.0% to 26.9%, but process sampling and write-stage timings were
too noisy across runs to attribute that whole delta to the renderer. The exact
row counts and renderer-local timings are the acceptance evidence.

Four large-pane repetitions reported 16, 27, 43, and 66 ms of renderer-local CPU
(median 35 ms). All four retained 0.680 MiB, began with 3,212 printable cells,
and rebuilt two rows per partial paint. The timing range is wider than the
structural work counters, so the median is recorded without being turned into a
stronger CPU claim.

A faster-looking 9 ms result was discarded on inspection: its fixture held only
77 printable cells, because late shell output had overwritten the seed. That is
why the harness now waits for a paint covering the seed and fails any progress
run below 50% density — a benchmark that measures an empty screen reports a
large win for doing nothing.

The wide timing range earlier in this section is why the structural counters
(rows rebuilt, quads submitted, bytes retained) are the acceptance evidence and
the CPU numbers are recorded rather than claimed.

Decision: keep behind a 2,048-cell threshold and a 2 MiB row-cache ceiling. The
measured default pane and a 62 x 25 packaged interaction pane stay on the direct
path with no cache allocation; the latter had inconsistent native Command-click
results in two validation runs at an earlier 1,536 cutoff, so the performance
crossover alone was not enough to justify the more aggressive gate.

Two limits, deliberately: the 2,048-cell floor is a performance gate (below it,
copying the rows costs more than rebuilding them) and the 2 MiB ceiling is a
memory gate. There is no size *cap* — an earlier draft also refused to cache any
grid whose cell count times one quad exceeded the ceiling, which excluded the
largest panes (above ~9,700 cells) on an estimate that assumed exactly one quad
per cell and so was wrong in both directions. The ceiling is enforced against
what a built frame actually retained.

The ceiling governs the cache's marginal cost — the retained rows — not the
frame buffer they concatenate into. A frame has to be staged somewhere whether
or not the cache exists: a dense styled 100 x 30 grid emitting four quads per
cell grows that buffer to 4 MiB on the direct path alone. Folding it into the
gate would trip the cache on memory the direct path spends anyway, so both
numbers are reported on every sample and only the rows are gated.

Retained rows are proportional to the visible grid and rendered quads: 0.680 MiB
at the accepted large measurement point, roughly 1.5 MiB for the dense 151 x 46
reference pane. If real styling pushes them past 2 MiB the renderer releases
them at that transition and keeps the grid on the direct path until it resizes.
Full-frame submission keeps the visual result and the 30 Hz sustained-output
policy unchanged.

**The model's dirty set covers the cursor only across rows.** Measured with
`scripts/bench-terminal-dirty-rows.mjs` against the vendored WASM:

| cursor change | reported dirty state | reported rows |
| --- | --- | --- |
| `\x1b[2;4H` (row 0 → row 1) | PARTIAL | both rows |
| `\x1b[2;9H` (within one row) | NONE | — |
| `\x1b[D` (back one column) | NONE | — |
| `\x1b[?25l` / `\x1b[?25h` | NONE | — |

The cursor is drawn as an inverted cell, so where it sits is part of the frame
even when no cell content changed. The table says the model will not tell us
that on its own, in two different ways:

*Nothing else wakes the renderer.* A same-row move or a visibility toggle
reports NONE, and `render()` returns early on NONE — so the old inverted cell
survives until unrelated output happens to arrive. Comparing the cursor against
the last frame *before* that early return is the only thing standing between a
user pressing `←` and a cursor that visibly does not move.

*Nothing marks the row.* When such a move rides along with an unrelated dirty
row the frame is PARTIAL, the cursor's own row is absent from the set, and the
cursor is baked into that cached row, so the row keeps the cursor at its old
column until a full paint.

So the renderer compares the cursor position ahead of the early return, and a
partial paint marks the cursor's row and the row it was last drawn on on top of
`isRowDirty()`. A frame that exists *only* because the cursor moved has no dirty
cells but is still a partial paint — otherwise it would fall through to
`dirty !== DIRTY_PARTIAL` and full-paint the grid on every arrow key, which is
the cost this whole document exists to remove. At most six rows either way.
ghostty-web's own canvas renderer compensates the same way, and future
partial-paint work has to keep doing so.

## Verification

- Packaged `terminal-perf` preflight passed with the final build.
- The packaged Markdown-link scenario passed plain selection, two Command-click
  opens, and repeated-link tile reuse.
- The streaming regression `hovered file link survives unrelated terminal
  writes (streaming TUI redraws)` passed.
- A styled 3,000-cell unit fixture exceeded the 2 MiB row-cache ceiling,
  released the cache, and used the direct full-frame path on its next paint.
- The full frontend suite and production frontend build passed; Playwright
  finished with 192 passed and the expected real-PTY-only case skipped.
- All benchmark streams verified the expected chunk count and byte count; no
  terminal output was dropped.
- Cursor movement with no dirty rows repaints both the vacated and the arrived
  row; a hidden cursor moving adds no work.
- A bare same-row move and a bare visibility toggle — both NONE dirty states —
  each produce a three-row partial paint rather than no paint or a full one.
  A frame where nothing at all changed still returns null.
- Live on the packaged build (profile `cursorpaint`, 155x46 = 7,130-cell pane):
  every paint partial, 1.54 MB retained row vertices under the 2 MiB ceiling,
  and the `bridge-pty-bench` progress payload rebuilt 2 rows per frame with
  `retainedRowVertexMb` 0.726 / `retainedStagingMb` 1.726. The renderer's
  `modelPrintable` and `paintQuads` witnesses agreed (3,367 / 3,368, the extra
  quad being the cursor).
