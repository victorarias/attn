---
title: Notebook UI Stage 7 — tile mode
status: in progress
related: docs/plans/2026-06-20-notebook-ui-gap.md
---

> Final stage of the Notebook UI gap-map rebuild (gap item 1 + item 13). UX decided
> with Victor in conversation; implementation plan synthesized by an ultracode workflow
> (sonnet scouts mapping the seams + opus plan/critic). This doc is the as-designed
> spec + the PR ladder.

# Stage 7 — Notebook tile mode

Today the Notebook is a **fullscreen modal** you pop open. Tile mode makes it *live
where you work*: the Notebook becomes a **tile** in the workspace split-tree, beside
agent terminals, sharing the existing dock/resize/persist machinery — and it is
**size-aware**, auto-folding its rails by the tile's own width.

## Decided UX (do not re-litigate)

- **New `notebook` TileKind.** The existing read-only `markdown` tile is **untouched**
  (it solves a different problem — dock one file, read-only). Reuse the existing tile
  dock/undock/drag/resize/persist machinery.
- **Independent per-view state.** Each notebook view owns its open file (+ scroll/fold)
  in its **own `tileParams`**, persisted per-workspace in `layout_json` (survives
  restart). **Multiple notebook tiles per workspace**, each on a different file. A tile
  in workspace A and one in workspace B are unrelated. Fullscreen is its own separate
  view. **No shared/global current-note.**
- **Same responsive component as fullscreen.** A `ResizeObserver` on the tile's width
  drives the **stage-5 fold seam** (`folded = override ?? auto`; a manual edge-rail
  override still beats auto):
  - **wide** (≥900px) → tree + note + rail
  - **medium** → tree + note (rail auto-folds)
  - **narrow** → note + compact file-picker (tree folds)
  - **short** → trimmed chrome
  Fullscreen keeps `auto = false` (manual folds, shipped in stage 5 / #376).
- **Fresh tile → no-selection screen + tile-local fuzzy finder.** A new tile opens to an
  empty state with a **Cmd+P-style fuzzy file finder overlaid inside the tile** (focused,
  autocomplete over the vault's files). Pick → opens that file (persisted to `tileParams`).
  **Esc** dismisses the finder → browse the tree (or, when narrow, the compact picker).
  The finder is **scoped to the tile** (not a global portal) and re-summonable inside it.
- **Entry (two independent doors):**
  - **Tile:** a new ActionMenu item + the `notebook.openTile` shortcut (`Cmd+Opt+N`) docks
    a **new** notebook tile (unique `tileId` each time) into the current workspace.
  - **Fullscreen:** the existing "Browse the Notebook" ActionMenu item + a button + the
    `notebook.openFullscreen` shortcut (`Cmd+Opt+Shift+N`).
  - Both shortcuts are rebindable; defaults dodge native macOS menu accelerators.

## Architecture — it's an add-on, not a rewrite

attn's workspace layout is already a split-tree of **leaves**, and a leaf is already a
union: a terminal *pane* or a *tile* (`TileLeaf { tileKind: 'markdown' | 'browser' | … }`).
Dock/undock RPCs, drag-to-relocate, split-ratio resize, and per-workspace persistence
(`layout_json`) all exist. So tile mode adds a `notebook` tile kind and an embedded
render path; it does **not** touch the layout engine.

- **No protocol bump.** The dock message carries no `tile_params`; the daemon reads
  pre-existing params by id. The frontend's only param-setting path is `update_tile`,
  gated by a handler that today rejects unknown kinds / runs browser-only URL validation.
  Widening that gate to accept `notebook` (passing its `tileParams` through as-is) is the
  whole daemon change. The notebook tile **self-serves content via the fs surface**
  (`fs_list`/`fs_read`/`fs_write`/`fs_exists`) the modal already uses — `tileParams` holds
  only the open file path (an opaque string; can widen to JSON later, non-breaking).
- **Unique tileId per dock.** `DockTile` treats a duplicate id as a *move*, so each
  Cmd+K must mint a fresh `tileId` or the second invocation just relocates the first tile.
- **Embedded variant.** Extract `NotebookSurface` (`variant: 'modal' | 'tile'`) from
  `NotebookBrowser`; the modal stays a thin shell (overlay + FocusTrap + Close +
  `useEscapeStack`). The reusable body (FileTree, live editor, context rail, fold handles,
  the tri-state fold seam, tasks panel) moves into the surface. The only behavioral delta
  vs. the modal is swapping `const autoFold = false` for the width-driven ResizeObserver
  value feeding the existing `treeFolded`/`railFolded` derivation.

## PR ladder

Each PR is independently shippable + manually verifiable in `attn-dev`, ≤1k lines.

1. **daemon + dock plumbing** (combine the planner's PR1+PR2; ~210 lines). Add
   `TileKindNotebook` (`internal/workspacelayout`), widen `handleWorkspaceLayoutUpdateTile`
   to accept it (skip the browser-only URL validation, accept notebook `tileParams`
   as-is). Add the FE `sendWorkspaceDockTile` helper (mirror `sendWorkspaceUndockTile`,
   4-place App plumbing). *Verify:* `go test -run Tile`; dock + update_tile + restart.
2. **extract `NotebookSurface` (modal|tile)** (~700). Pure refactor — the modal stays
   byte-identical (`autoFold` stays `false`); all existing specs unchanged. Risk
   concentration; keep the diff mechanical.
3. **width-driven auto-fold** (~160; needs PR2). `useTileAutoFold` ResizeObserver →
   the wide/medium/narrow/short ladder; must initialize synchronously (no flash).
4. **shortcuts + ActionMenu items** (~110; needs PR1). `notebook.openTile` (`Cmd+Opt+N`)
   + `notebook.openFullscreen` (`Cmd+Opt+Shift+N`) in the registry + metadata + the
   keyboard hook; ActionMenu items; **unique `tileId` per dock**. *Verify:* a 2nd
   `Cmd+Opt+N` makes a 2nd tile, not a move.
5. **`WorkspaceDockTile` notebook branch** (~220; needs PR1, PR3, PR4). Mount
   `NotebookSurface variant="tile"`; omit the chief pulse by default; keep markdown/
   browser tiles unchanged. *Verify:* persist across restart.
6. **tile-local fuzzy finder** (~320; needs PR5). A `filterScore` util (model on
   `ActionMenu`'s scorer) + `useNotebookFileIndex` (one-shot `notebook_list` empty-prefix
   walk on mount, refetch on `fsChangeSignal`, 300ms debounce) + the in-tile overlay with
   its own scoped focus; `onOpenFile` → `update_tile`. *Verify:* fresh-tile finder; persist
   on pick.

## Open questions (chosen defaults)

- **Chief pulse in a tile** → **omit** (ambient/global status; a tile is focused work).
  Revisit if it proves wanted.
- **`tileParams` shape** → **path-only opaque string** first; widen to JSON later
  (non-breaking).
- **Finder re-summon key** → **`Cmd+P` scoped to the focused notebook tile**, pending a
  check that it doesn't collide with a terminal binding (verify in the finder PR).

## Risks

- Persistence depends entirely on the PR1 gate-widen → PR1 before the tile-render PR.
- The `NotebookSurface` extraction (PR2) is the risk concentration — keep the modal path
  byte-identical so the shipped fullscreen + stage-5 folds don't regress.
- The ResizeObserver auto-fold must init synchronously to avoid a fold flash on mount.
- The finder must own scoped focus so it doesn't fight the editor or the workspace.
