# Plan: Warp-inspired terminal features (links, find, command blocks)

## Goal

Bring three Warp terminal behaviors to attn's Ghostty terminal, with Warp's performance
model (lazy, hover-scoped, chunked — never scan-the-world):

1. **Clickable paths + URLs** — file paths (with `:line[:col]`) and URLs in output are
   hover-underlined and Cmd+click-openable.
2. **Find in scrollback (Cmd+F)** — incremental search over scrollback + viewport with
   highlighted matches and next/prev navigation.
3. **Command blocks (OSC 133)** — group command + output via shell semantic-prompt
   markers; click selects a block; Cmd+C copies command+output, Cmd+Shift+C copies the
   command only.

## What we learned from Warp's source (~/projects/warp)

- **Link detection is lazy**: nothing scanned per-line or per-write. On hover, compute the
  word-fragment boundary around the pointer, cache it, and only re-detect when the pointer
  enters a *new* fragment (`app/src/terminal/view/link_detection.rs`). File-existence
  validation runs off-thread. Bounds: URL scan ≤1000 chars, candidates longest-first with
  early exit.
- **Blocks come from shell integration**, not heuristics: preexec carries the command
  text, precmd carries the exit code (Warp uses custom DCS JSON; the open standard is
  OSC 133). Copy semantics are `command_to_string()` / `output_to_string()` /
  `command\n+output` over block row ranges.
- **Find is chunked + async**: ~1000 rows per chunk, results streamed, re-render throttled
  (50ms), dirty-row ranges rescanned instead of full re-search while output streams.
- **fish 4.x emits OSC 133 natively** (verified locally with fish 4.7.1):
  `]133;A;click_events=1` (prompt start), `]133;B` (input start),
  `]133;C;cmdline_url=echo%20hello` (pre-exec, command URL-encoded!), `]133;D;0`
  (exit code). The `cmdline_url` payload means copy-command needs no prompt-stripping.

## Architecture Map

```text
Current:
pty bytes -> GhosttyTerminal.write()
  -> parseOsc52Writes (clipboard) / parseSynchronizedOutput
  -> ghostty-vt wasm model -> WebGlTerminalRenderer.render(model, viewportCells, selection)
mousemove -> literalUrlAtColumn(visible line) -> cursor change only (needs Cmd held)

Target:
pty bytes -> GhosttyTerminal.write()
  -> parseOsc52Writes / parseSynchronizedOutput
  -> parseOsc133Markers (new, stateful across chunks; fast-path: skip unless "]133;" present)
       marker + absoluteRow(scrollbackLen + cursor.y) -> TerminalBlockStore (new)
  -> ghostty model -> renderer.render(model, viewportCells, overlays[])   // generalized

mousemove (not selecting)
  -> fragment cache check (row, startCol..endCol, generation)   // generation bumps on write/scroll
  -> miss: linkAtCell (new utils/terminalLinks.ts)
       URL regex on fragment | path candidates (longest-first, cap 4) -> exists() via plugin-fs (async, cached)
  -> hover overlay (underline range) + pointer cursor; Cmd+click -> openUrl / openPath

Cmd+F (terminalKeyHandler -> pane shortcut)
  -> TerminalFindBar (new component, per active pane)
  -> TerminalFindController (new utils/terminalFind.ts)
       chunked scan (rows/chunk, setTimeout-yield) over scrollback+viewport text
       matches: {bufferRow, startCol, endCol}[] -> visible subset -> overlays
       next/prev -> set viewportOffset to reveal focused match

click (no drag, no mouse-tracking, marker data exists)
  -> TerminalBlockStore.blockAtBufferRow -> selected block -> outline overlay
  -> Cmd+C: command + "\n" + output rows text; Cmd+Shift+C: command only
     (text selection, when present, keeps existing copy behaviors)

Tests:
vitest: parseOsc133Markers chunk-split cases, block store lifecycle/trim,
        path candidate generation, fragment boundary, find scanning/merge
playwright e2e (real frontend + real ghostty wasm + injected PTY bytes):
        __TEST_EMIT_PTY_DATA with byte streams captured from real fish 4.7.1
        clipboard via navigator.clipboard, opener/fs probes via __TAURI_INTERNALS__ shim
real-app (packaged, serial, dev install): one block-copy scenario in a utility
        terminal running real fish (daemon -> PTY -> fish -> OSC133 end to end)
```

## Data Model / Interfaces

```ts
// utils/terminalOsc133.ts — parser owns nothing; pure + carry state like Osc52State
type Osc133State = { pending: string }
type Osc133Marker =
  | { kind: 'prompt-start' }            // A
  | { kind: 'input-start' }             // B
  | { kind: 'pre-exec'; cmdline?: string } // C;cmdline_url=...
  | { kind: 'command-end'; exitCode?: number } // D;0
// parse(state, chunk) -> { state, segments: Array<{ bytes: string; marker?: Osc133Marker }> }
// caller writes segment bytes to the model, then records marker at the model's
// current absolute row (scrollbackLength + cursor.y)

// utils/terminalBlocks.ts — owned by GhosttyTerminal instance (per pane)
type TerminalBlock = {
  promptRow: number      // absolute buffer row at A
  inputRow?: number      // at B
  outputStartRow?: number // at C (cursor row after command echo)
  endRow?: number        // at D
  command?: string       // decoded cmdline_url
  exitCode?: number
}
// store caps blocks (200), drops oldest; clears on terminal reset/alt-screen;
// invalidates blocks whose rows fall outside the live buffer after trimming

// GhosttyWebGlRenderer — selection generalized to overlay list
type WebGlOverlay = {
  startRow: number; startCol: number; endRow: number; endCol: number // viewport coords
  color: string
  kind: 'background' | 'underline' | 'outline'
}
// render(terminal, force, viewportCells, overlays: WebGlOverlay[], viewportOffset)

// utils/terminalFind.ts — controller owned by GhosttyTerminal via handle
type FindMatch = { bufferRow: number; startCol: number; endCol: number }
type FindState = { query: string; caseSensitive: boolean; matches: FindMatch[]; focusedIndex: number; scanning: boolean }
// scan(terminalAccess, query, opts) — chunked, cancellable, no row-text caching

// utils/terminalLinks.ts — pure helpers
type DetectedLink =
  | { kind: 'url'; uri: string; startCol: number; endCol: number; row: number }
  | { kind: 'path'; path: string; line?: number; col?: number; startCol: number; endCol: number; row: number }
// fragmentAt(lineText, col) -> {startCol, endCol} | null
// pathCandidates(fragment) -> string[] (longest-first, cap, parses :line:col)
```

## Boundaries

- `GhosttyTerminal` owns per-pane state (fragment cache, block store, find controller,
  hover overlay) and is the only layer touching the wasm model.
- `GhosttyWebGlRenderer` knows only viewport-relative overlay ranges; it never computes
  them and has no feature knowledge.
- Parsers (`terminalOsc133`, `terminalLinks`, `terminalFind` scanning) are pure modules —
  unit-testable without wasm or DOM.
- File existence/open go through Tauri plugins (`plugin-fs` exists, `plugin-opener`
  openPath/openUrl); e2e shims them via `__TAURI_INTERNALS__` like the existing opener probe.
- Relative path resolution uses the pane's session `cwd` (already available in
  `SessionTerminalWorkspace`), passed to `GhosttyTerminal` as a prop. No OSC 7 tracking in v1.

## Performance budget (hard requirement)

- Idle mouse, streaming output: **zero added work** beyond a substring check for `]133;`
  per write chunk (and only stateful parsing when present).
- Hover: work only on fragment *change*; one line extraction + regex + ≤4 cached async
  `exists()` calls. Cache invalidated by write/scroll generation counter.
- Find: chunked scan with yields; no persistent row-text cache (scrollback cap is 8MB —
  a text mirror would double terminal memory); debounced rescan (200ms) on new output
  while the bar is open.
- Blocks: marker recording is O(1) per marker; selection/copy extracts text only on demand.
- Overlays: only visible-range overlays are computed per frame (binary search over
  match/block lists by buffer row).

## Implementation Steps

- [x] 1. Renderer: generalize `WebGlSelection` to `WebGlOverlay[]` (background/underline/outline), selection becomes one overlay; unit-test overlay quad emission counts
- [ ] 2. Links: `terminalLinks.ts` (fragment, URL, path candidates `:line:col`) + hover cache + underline overlay + Cmd+click openPath/openUrl + e2e spec (hover underline probe, cmd+click file path with fs/opener shims)
- [ ] 3. Find: `terminalFind.ts` controller + `TerminalFindBar` UI + Cmd+F wiring + match overlays + next/prev + scroll-to-match + e2e spec (inject 200 rows, search, navigate, assert highlight + scroll probes)
- [ ] 4. Blocks: `terminalOsc133.ts` parser + `terminalBlocks.ts` store + click-select + outline overlay + copy shortcuts + e2e spec (replay captured fish byte stream, click block, assert both copy flavors)
- [ ] 5. Click-count selection: triple click selects the visual row; single click inside a
       block's command region selects the command text (Warp-style). Double-click word
       select already exists. e2e coverage for both.
- [ ] 6. Real-app scenario: utility terminal + real fish + block copy (serial, dev install)
- [ ] 7. CHANGELOG + docs/context updates; perf spot-check via `__ATTN_TERMINAL_PERF_DUMP` before/after on a streaming session

## Decisions

- **OSC 133 over Warp's DCS protocol**: open standard, fish emits it natively (verified,
  including command text via `cmdline_url`); no shell rc injection needed for v1.
  zsh/bash users get no blocks until we ship integration snippets (follow-up).
- **Hover-lazy link detection over per-line scanning**: Warp's model; keeps streaming
  cost at zero. Trade-off: links wrapped across visual rows are not detected in v1.
- **Copy shortcuts are block-scoped**: `Cmd+Shift+C` already means "copy selection as
  markdown" when a text selection exists; block copy shortcuts apply only when a block is
  selected and no text selection exists. No regression to existing copy behaviors.
- **Plain click opens nothing** (Cmd+click required, like Warp's default): plain clicks
  must keep feeding selection and app mouse-tracking.
- **No daemon/protocol changes**: everything is frontend-side over the existing PTY byte
  stream. Avoids protocol version bump and keeps the PRs splittable.
- **Block rows can drift after scrollback trimming** (8MB cap): v1 invalidates blocks
  rather than re-anchoring (Warp uses sum-tree absolute indexing). Acceptable for utility
  shells; revisit if real usage hits it.

## Open Questions

- Open-path target: v1 uses `plugin-opener` openPath (default app). Editor integration
  with `:line:col` is a follow-up (needs an editor setting attn doesn't have yet).

## Follow-ups

- Shell-integration snippets for zsh/bash (ghostty's own scripts are a good base) so
  non-fish users get blocks.
- Block gutter UI affordances (hover chip with copy buttons, exit-code badge).
- Cross-wrap link detection; OSC 8 hyperlink URIs (needs ghostty-web API work upstream).
