# Plan: In-app spike for annotating an agent's message in the terminal

## Goal

Prove — inside the running app, on a live terminal — that we can paint
annotations over an agent's message and keep them pointing at the right words
while the terminal changes shape underneath them. This is the second of two
spikes. The first one (`spikes/msg-grid-align/`) answered *where an annotation
lives*; this one answers *whether we can draw it and hold it there*.

Nothing here ships. The exit condition is a recorded verdict with numbers, in
the same shape as `spikes/msg-grid-align/NOTES.md`, plus a decision on whether
to build the feature for real.

## Where the first spike left us

The design under test then, and still:

> **The transcript span is the anchor. The grid is a projection of it.**

Annotations are anchored to byte offsets in the message's markdown. Grid rows
are re-derived on demand and never stored, which is what makes a width reflow —
the case that defeats `TerminalBlockStore` outright — cost nothing.

Measured on six real Claude sessions (100–307 cols):

| property | result |
|---|---|
| forward alignment recall in span | 98.9–100% |
| row-order inversions | 0 everywhere |
| reverse quote exactness, 2/3/5-row selections | 97.5–100% (93.4% table-heavy) |
| height ±12 | 0 wrong (9 safe refusals) |
| width −20, −60 | **0 wrong** |
| all rows shifted | 0 wrong |

Every failure mode degraded to a refusal rather than a misquote. Codex remains
**unmeasured** — no captured session has annotatable prose on screen — and that
gap is unchanged by this spike.

The UX is settled in `docs/prototypes/terminal-annotation-sketch.html`: highlight
a range, get an emoji popup, one option opens a comment box, saved annotations
collect in a draggable floating panel with a single "Send all". The panel floats
specifically so it cannot resize the terminal.

## What the sketch cheated on

The prototype renders terminal rows as DOM text with `user-select: text` and
wraps annotations in `<mark>`. The real terminal is a WebGL canvas with no DOM
text to select and no element to wrap. So the prototype proves the interaction
and proves nothing about the mechanism.

## What already exists (read, not assumed)

The painting half is largely built, and the sketch's "wash + rails" turns out to
be an idiom already shipping for block selection.

- `WebGlOverlay` (`app/src/components/GhosttyWebGlRenderer.ts:24`) takes a
  cell-space rect plus `color`, `alpha`, and
  `kind: 'background' | 'underline' | 'outline'`.
- `renderSurface` (`app/src/components/GhosttyTerminal.tsx:752`) already pushes
  three concurrent overlays: the selection wash, the hover-link underline, and
  the block-selection wash plus outline. An annotation wash is one more entry.
- Selection already exists in buffer coordinates: `SelectionRange`,
  `normalizeSelection`, `selectionLineAtBufferRow`, `textForSelectionRange`, and
  `terminalStyledSelectionToMarkdown` (which already reconstructs markdown from
  styled cells).
- Cell metrics and px→(row,col) hit-testing are available for a DOM gutter
  (`app/src/components/GhosttyTerminal.tsx:445`).

So: **no `<mark>`, no DOM text layer, and no reserved gutter column.** The
annotation wash is a `background` overlay; the gutter is a DOM layer positioned
in cell space above the canvas.

One consequence worth stating up front: because the gutter overlays the leftmost
cells rather than reserving width, it will sit on top of real output — the
agent's `⏺` marker and tree glyphs live in exactly those columns. That is
accepted for the spike; whether it is acceptable in the product is one of the
things looking at it will tell us.

## What is genuinely unproven

Ordered by risk. Q1 is the one that kills the feature if it fails.

### Q1 — Does the projection stay correct while the terminal changes?

Overlays are painted in **viewport** rows; annotations are anchored to
**markdown offsets**. Every frame needs offset → buffer row → viewport row, and
a stale mapping paints a wash over the wrong words. That is the single
unacceptable outcome — worse than painting nothing, because it silently
misattributes text to the agent.

Perturbations to run against a live session, with the annotation set held fixed:

| perturbation | expectation |
|---|---|
| agent appends output below the message | wash holds, or refuses |
| user scrolls the message partly/fully out of view | wash clips, or refuses |
| height resize | wash holds |
| width resize (reflow) | wash re-derives onto new rows |
| font-size change | wash holds (cell metrics change, cells don't) |
| session switch away and back | wash holds or is cleanly dropped |
| alt-screen enter/exit | wash dropped, not stranded |

Pass = the wash covers the anchored words, or nothing at all. Fail = the wash
covers different words. Count the third case separately and loudly; it is the
verdict.

This needs an alignment **cache and an invalidation policy**, which is the real
design question hiding inside Q1. Candidates to try, cheapest first: re-align on
resize only; re-align on an idle debounce after writes; re-align on every write
(certainly too slow, useful as the correctness baseline). Measure the staleness
window each policy leaves — that window is exactly when wrong-word paints
happen.

### Q2 — What does alignment cost in the real app?

The aligner is O(n·m) LCS. The fixtures are ≤832 × ≤895 tokens; real scrollback
is far bigger. Measure a full re-align at realistic buffer sizes and decide
whether banding or rare-token anchoring is required before the feature is
usable at all, and whether alignment has to move daemon-side (the first spike
argues it should, since the daemon owns both the parsed terminal and the
transcript — see `spikes/msg-grid-align/NOTES.md`).

Keep the spike's aligner client-side and slow. The point here is projection
correctness, not the final architecture.

### Q3 — Does the selection gesture even reach us?

The Claude TUI takes mouse reporting, which is what the "application selection"
path exists for. Determine whether a drag over the message selects text or gets
forwarded to the TUI as a mouse event. If it is forwarded, the highlight-first
UX in the sketch is unreachable as drawn, and the fallback needs deciding — a
modifier-held drag, an explicit annotate mode, or a keyboard-driven range.

Answering this early matters: a bad answer changes the UX, not just the code.

### Q4 — Does a DOM gutter stay glued to the canvas?

Position it with `renderer.cellWidth`/`cellHeight` and check it tracks scroll,
resize, and font-size changes without lag or drift against the WebGL repaint.
Settle z-order against `TerminalContextMenu`, and confirm an emoji renders
sanely over the leftmost cells (they are double-width in a monospace grid).

### Q5 — Do many overlays composite correctly?

Today at most four are live. Annotations could push dozens. Check per-frame cost
and whether overlapping `background` overlays blend sanely: two annotations on
one row, and an annotation underneath the selection wash or a block wash. Look
for an implicit cap or per-row assumption in `OverlaySpan`.

## Shape of the spike

Throwaway, obviously named, and gated so it cannot reach a normal user.

- A single throwaway module beside the component it prototypes for
  (`app/src/components/annotationOverlaySpike.ts`) owning: a hardcoded
  annotation set anchored to markdown offsets, the offset → viewport-row
  projection, the cache and its invalidation, and the `WebGlOverlay` emission.
- One call site appending to the existing overlay array in `renderSurface`,
  behind a dev-only flag, so production paint paths are untouched when it is off.
- A small DOM gutter layer for Q4.
- Instrumentation over correctness rather than eyeballing: on every projection,
  record annotation id, offsets, resolved rows, and the text actually at those
  rows, then apply the same containment check `checkPerturbation` uses in
  `spikes/msg-grid-align/stability_test.go`. Write it to
  `$APPLOCALDATA/debug/*.jsonl` following `terminalDiagnosticsLog.ts`, per
  AGENTS.md guidance for hard-to-reproduce UI behaviour.

Reuse the first spike's aligner logic rather than reinventing it, so the numbers
stay comparable.

## Non-goals

- The submit path (typing the annotation batch into the PTY). The existing
  `markdown_annotations_submit` already solves this and it is not at risk.
- The floating panel, its styling, and drag. Settled in the sketch.
- Persistence of annotations across restarts.
- Moving alignment into the daemon, or any protocol change.
- Closing the Codex measurement gap. Still one fresh session away: drive a Codex
  session to a prose answer and run `spikes/msg-grid-align/capture`.

## Verification

This touches the terminal and the UI, so per AGENTS.md it needs live
verification from a non-production install — `make dev`, then the bundled
preflight before treating any scenario output as evidence. Automated tests
cannot answer Q1, Q3, or Q4.

Tests added under the spike must follow the house rule: scope `ATTN_DATA_DIR`,
never redirect `HOME`.

## Exit criteria

1. A verdict on Q1 with a count of wrong-word paints per perturbation. Anything
   above zero is a stop-and-redesign, not a bug to file.
2. A named cache-invalidation policy with its measured staleness window.
3. A yes/no on Q3, and if no, the chosen fallback gesture.
4. Q2's timing numbers, and a call on client-side vs daemon-side alignment.
5. Then delete the spike or absorb the decision into real code. Do not leave it
   behind a flag indefinitely.
