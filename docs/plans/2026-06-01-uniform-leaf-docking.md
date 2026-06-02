# Plan: Uniform Leaf Docking (drag any pane/tile, depth-sized drops, tile-only workspaces)

## Goal

Today only the markdown tile can be dragged to re-dock. Generalize the existing
markdown drag into one uniform model:

1. **Any leaf is draggable** — terminal panes and tiles alike — onto any other
   leaf's edge (including tile-on-tile).
2. **The drop signals the size** — how deep you drop sets the incoming leaf's
   fraction (thin at the edge → ½ at center), with magnetic snaps at ¼ / ⅓ / ½.
3. **Container edges** — a perimeter frame docks against the whole workspace
   (the root), not just one neighbor.
4. **A workspace lives while it holds any leaf** — closing the last terminal no
   longer discards tiles you deliberately left behind. Sessionless (PTY-less)
   workspaces are shown/hidden via the sidebar display popover.

The throughline: markdown's drag path is special-cased in ways that are mostly
arbitrary (tile-only targets, only-pane anchors, a carried fraction). Collapse
that special path into one generic operation; the only genuine invariant left is
"don't orphan the last leaf."

Extends and revises `docs/decisions/2026-05-31-docked-tiles.md` (which currently
says "closing the last terminal tears down the workspace"). Update that doc in
the lifecycle PR to reflect last-*leaf* teardown.

## Background / why these limits were arbitrary

- The low-level tree ops are already leaf-generic: `Remove`/`removeNode`,
  `insertBesideLeaf`, `hasLeaf` all handle `"pane"` and `"tile"`
  (`internal/workspacelayout/workspacelayout.go:386,488,611`).
- Markdown-on-markdown is blocked only by two guard layers above that core: the
  frontend offers only `agentPaneById` as drop targets (`index.tsx:586`), and
  daemon `dockTile` forces the anchor to a terminal pane (`daemon/workspacelayout.go:405`).
- The "no tile-only workspace" rule is the one with real substance — it encoded
  "workspace == agent session." We are deliberately changing it to "workspace ==
  arranged content, usually but not always with an agent."

## Architecture Map

```text
Current drag flow:
tile header pointerdown                         // WorkspaceDockTile.tsx:243
  -> beginTileDrag(tileId)                     // index.tsx:574  (tiles only)
    -> computeDockTarget(x,y)                    // index.tsx:57   targets = agent panes only;
    |                                            //                edge = min(px,1-px,py,1-py) [X-pattern];
    |                                            //                band = fixed DOCK_BAND_FRACTION 0.32 (index.tsx:38)
    -> onDockTile(tileId, kind, anchorPaneId, edge)   // on pointerup
      -> ws: workspace_layout_dock_tile
        -> daemon.dockTile()                    // daemon/workspacelayout.go:387  anchor forced to a pane (:405)
          -> workspacelayout.DockTile()         // inserts a *tile* leaf beside anchor (:573)

Target drag flow:
leaf header pointerdown (pane OR tile)          // pane header at index.tsx:654 gains a drag handle
  -> beginLeafDrag(leafId)                       // generalized from beginTileDrag
    -> computeDockTarget(x,y)                    // targets = ALL leaves (panes+tiles);
    |                                            // depth (distances[0].dist) -> incoming fraction, snapped;
    |                                            // + container frame zones on the workspace perimeter
    -> onMoveLeaf(leafId, anchorId|"", edge, ratio)     // anchorId "" => container/root dock
      -> ws: workspace_layout_move_leaf          // self-drop (leafId == anchorId) => no-op, no command
        -> daemon.moveLeaf()                     // anchor may be pane, tile, or root
          -> workspacelayout.MoveLeaf()          // remove-then-reinsert; guards below

Rendering (unchanged invariant worth protecting):
WorkspaceLayoutRenderer renders panes FLAT, keyed by id, positioned from
renderedPaneBounds (WorkspaceLayoutRenderer.tsx:93). The split "tree" is
aria-hidden geometry metadata only. => rearranging the tree repositions a
GhosttyTerminal; it does NOT unmount/remount. A move must not break this (no
key changes, no remount) or it kills the PTY.
```

## Interaction Design

### Drop zones within a leaf (per-leaf docking)

Keep the existing X-pattern edge selection — `min(px,1-px,py,1-py)` already picks
the diagonal triangle (= edge + which side the incoming leaf lands). The new part
is using the depth it already computes (`distances[0].dist`, currently discarded)
to size the drop instead of the fixed `0.32` band.

```text
   edge = X-pattern (already works)           depth -> size (new)

  ┌───────────────────────────┐        near edge -> thin       near center -> half
  │\          TOP           /│      ┌──┬──────────────┐    ┌──────────┬──────────┐
  │  \                   /   │      │▓▓│              │    │▓▓▓▓▓▓▓▓▓▓│          │
  │ L  \       ·       /   R │      │▓▓│      B       │ …  │▓▓ new ▓▓▓│    B     │
  │      \           /       │      │▓▓│              │    │▓▓▓▓▓▓▓▓▓▓│          │
  │        \  BOTTOM /       │      └──┴──────────────┘    └──────────┴──────────┘
  └───────────────────────────┘         f ≈ 0.15               f ≈ 0.50
```

- `f = clamp(distances[0].dist, F_MIN, 0.5)` (≈ one-line change in `computeDockTarget`).
- Magnetic snap to ¼ / ⅓ / ½ within a small tolerance; show the fraction as a tick/label.
- The orange preview rect = the actual resulting area (already true; just size it from `f`).
- The gesture sizes the **incoming** leaf, max ½ (center). Want it bigger? Drag
  the divider after — deliberate boundary that keeps one pointer position = one
  unambiguous result.
- Center square (·): v1 resolves to ½ on the last-stable axis. (Swap gesture = follow-up.)

### Container edges (dock against the whole workspace)

A perimeter **frame** (~24–32px), rendered only during a drag, captures docks
against the root. Mechanically it's `insertBesideLeaf`/`tileSplit` targeting the
**root** instead of a leaf — wrap the whole tree.

```text
┌══════════════════════════┐   ══ / ║  = container frame  (dock at root, spans that side)
║ ┌──────┬───────────────┐ ║   inner    = per-leaf X-pattern
║ │  LT  │               │ ║
║ ├──────┤       R       │ ║   precedence: outermost ~24px = container; inward = leaf edge
║ │  LB  │               │ ║   corners: resolve to nearer outer edge
║ └──────┴───────────────┘ ║
└══════════════════════════┘
```

Container vs per-leaf only differ when a side holds 2+ leaves:

```text
   workspace            left edge of LT (leaf)        left container edge
┌────────┬───────┐    ┌───┬────┬───────┐           ┌───┬────┬───────┐
│   LT   │       │    │ N │ LT │       │           │   │ LT │       │
├────────┤   R   │ →  ├───┴────┤   R   │   vs.     │ N ├────┤   R   │
│   LB   │       │    │   LB   │       │           │   │ LB │       │
└────────┴───────┘    └────────┴───────┘           └───┴────┴───────┘
                       N half-height                N full-height
```

Polish: **only show a side's container zone when that side has ≥2 leaves** — when
they'd coincide, suppress it (no redundant zone; teaches what the frame is for).

Container size: **snapped default ½** (thin gutter can't carry a depth gesture);
refine via divider. Graduated frame = follow-up if it feels missing.

## Data Model / Interfaces

```ts
// frontend -> daemon (new command). Protocol bump required.
WorkspaceLayoutMoveLeaf = {
  workspace_id: string
  leaf_id: string                              // dragged pane OR tile id
  anchor_id: string                            // target leaf id; "" => container (root) dock
  edge: 'left' | 'right' | 'top' | 'bottom'
  ratio?: number                               // incoming leaf fraction from depth gesture (snapped)
}
// daemon replies with the existing *_result pattern; broadcasts workspace_layout_updated.
```

```go
// internal/workspacelayout/workspacelayout.go (new; reuses Remove + insertBesideLeaf + tileSplit)
// MoveLeaf relocates an existing leaf (pane or tile) beside anchorID, or wraps
// the root when anchorID == "" (container dock).
func MoveLeaf(node Node, leafID, anchorID string, dir Direction, before bool, splitID string, ratio float64) (Node, bool)
//  guards:
//   leafID == "" || leafID == anchorID  -> (node,false)   // self-drop no-op
//   !hasLeaf(node, leafID)              -> (node,false)
//   cleaned = Remove(node, leafID)
//   if cleaned empty (was the only leaf) -> (node,false)   // nothing to anchor against
//   anchorID == ""  -> tileSplit(cleaned-root, movedLeaf, dir, before, splitID, ratio)  // container
//   else            -> insertBesideLeaf(cleaned, anchorID, dir, before, splitID, ratio, movedLeaf)
//   if anchor vanished with the removal (e.g. collapsed) -> (node,false)
```

- `DockTile` stays for **first** dock (a tile that doesn't exist yet, e.g.
  `attn open file.md`). `MoveLeaf` handles **relocating** an existing leaf. Both
  funnel through `insertBesideLeaf`.
- Daemon `moveLeaf()` (new, in `daemon/workspacelayout.go`) must accept pane,
  tile, **or** empty (root) anchors — i.e. do not force the anchor to a pane the
  way `dockTile` does at `:405`.

### Lifecycle change (the substantive one)

```text
handleWorkspaceLayoutClosePane (daemon/workspacelayout.go:607)
  before:  if len(normalized.Panes) == 0 { RemoveWorkspaceLayout }   // tiles excluded => dies w/ tile present
  after:   if layoutEmpty(normalized.Layout) { RemoveWorkspaceLayout } // empty == no panes AND no tiles

removeWorkspaceLayoutPaneForSession (daemon/workspacelayout.go:622)
  same rule when an agent exits on its own: keep the workspace if a tile remains.

Audit also: RemoveWorkspaceLayout call near daemon/workspacelayout.go:26 (normalize-empty path),
firstWorkspaceLayoutPaneID, and session-spawn reconcile (must NOT resurrect the closed agent).
```

A sessionless workspace = `Panes: []` + a layout tree of tile leaf(s). Sidebar
gets a toggle in the existing display popover (`Sidebar.tsx:197` `displayMode`
state; popover at `:311`) to show/hide PTY-less workspaces.

## Boundaries

- `computeDockTarget` (frontend) owns geometry only: pointer → {anchorId | root,
  edge, ratio, preview rect}. It never mutates the tree.
- `MoveLeaf` (workspacelayout) owns the pure tree transform + all structural
  guards (self-drop, last-leaf, vanished anchor). No daemon/session knowledge.
- `daemon.moveLeaf` owns persistence + broadcast + the result event. It must not
  re-impose "anchor must be a pane."
- The flat-render invariant (panes keyed, positioned from bounds) must survive —
  a move is a bounds change, never a remount.

## Implementation Steps

Two PRs along the natural risk seam. **PR A** is additive and shippable on its
own — closing the last terminal still tears down as today, so it needs nothing
from PR B. **PR B** changes daemon teardown semantics, the riskier half, isolated
so a revert doesn't drag the docking UX with it.

### PR A — Uniform docking interaction (drag any leaf, depth size, container edges) ✅
- [x] `MoveLeaf` in `workspacelayout` + unit tests (self-drop, only-leaf, unknown leaf, pane↔tile, nesting, container/root wrap, locked-ratio survives normalize). `tileSplit` generalized to `lockedSplit`; new `findLeaf` helper.
- [x] Protocol: added `workspace_layout_move_leaf` (TypeSpec → `make generate-types` → constants → `ProtocolVersion` 78→79; frontend `PROTOCOL_VERSION` + test fixtures bumped).
- [x] `daemon.moveLeaf` handler + dispatch + command metadata; accepts pane / tile / "" (root) anchors. Daemon integration tests (relocate, self-drop reject).
- [x] Frontend: `beginTileDrag` → `beginLeafDrag`; pane header is a drag handle (`workspace-pane-header--draggable`, grab cursor); `showPaneHeader` now counts tiles too; targets widened to all leaves; self-drop excluded.
- [x] Depth-driven size + magnetic ¼/⅓/½ snaps; `ratio` plumbed `onMoveLeaf` → `moveLeaf`. Geometry extracted to pure `dockTarget.ts` + 10 unit tests.
- [x] Container edges: perimeter frame → root wrap; per-side suppression when a side has <2 leaves; snapped-½ default.
- [x] Flat-render invariant holds (panes keyed + positioned from bounds; a move repositions, never remounts). Removed the now-dead frontend dock path (`onDockTile`/`sendWorkspaceDockTile`); `attn open` still creates tiles server-side.
- [x] Gesture state machine extracted to pure `leafDrag.ts` (`startLeafDrag`) so the pointerdown→move→up wiring is testable; `beginLeafDrag` is now a thin React wrapper injecting selection-lock/state/`onMoveLeaf` as handlers. `leafDrag.test.ts` drives the real window listeners with synthetic `PointerEvent`s (edge preview, drop target, self-drop no-op, container-edge dock, cancel/teardown).
- [x] CHANGELOG updated.
- [x] Live end-to-end verification in the packaged dev app via a new `drag_leaf` automation verb (synthesizes pointerdown on a leaf header + window move/up). Confirmed: pane→pane bottom dock restructures the tree (pane + tile + pane → tile + stacked pane/pane); fresh split panes keep their PTY scrollback markers across the move and still accept input afterward (no remount). `drag_leaf` lives beside `split_pane`/`click_pane` in `useUiAutomationBridge.ts` and only activates when automation is enabled.

### PR B — Tile-only workspaces (lifecycle + sidebar reveal) ✅
- [x] `LayoutEmpty` (no panes *and* no tiles) teardown rule replaces `len(Panes)==0` at the three layout sites: `ensureWorkspaceLayout` (the linchpin — every handler calls it), `handleWorkspaceLayoutClosePane`, `removeWorkspaceLayoutPaneForSession`. Helper + unit test in `workspacelayout`.
- [x] Workspace **entity** survival: guard in `dissociateSessionFromWorkspace` keeps the workspace (and broadcasts a neutral state-changed) when its last session leaves but a tile remains. Relies on the call ordering (dissociate runs before the layout pane is removed, so the stored layout still shows the tile). New `workspaceLayoutHasTiles` helper.
- [x] Startup reap (`loadWorkspacesFromStore`) keeps sessionless, tile-only workspaces across daemon restarts. Session-spawn reconcile only prunes — never resurrects the closed agent (verified).
- [x] Sidebar display popover toggle for PTY-less workspaces, **hidden by default** + localStorage persistence; sessionless workspaces render a neutral (non-state) indicator. Derived purely from `sessions.length === 0` (the daemon only retains sessionless workspaces that hold tiles), so **no protocol bump**.
- [x] Updated `docs/decisions/2026-05-31-docked-tiles.md` to last-leaf teardown.
- [x] Daemon protocol tests model the real app flow (close last pane / reap / startup) per `internal/daemon/CLAUDE.md`; frontend tests cover the reveal toggle, neutral indicator, and persistence.

Deferred to a small follow-up (cosmetic, fully separable from lifecycle):
- [ ] Tile header title from content: markdown H1 → beginning of text → basename fallback (`WorkspaceDockTile.tsx:237`). A sessionless workspace already renders fine; the tile just shows its basename until this lands.
- [ ] Focus a tile on select for fully tile-only workspaces (extend the AGENTS.md terminal-focus rule with a "no terminal" branch). Tiles are not interactive focus targets today; selection renders the layout without stealing focus, which is acceptable for v1.

## Decisions

- **Edge + depth, capped at ½** over divider-position sizing. One pointer
  position = one unambiguous {edge, size}; bigger-than-half is a divider drag.
- **One generic `MoveLeaf`** rather than a bolted-on `MovePane`. The tree ops are
  already leaf-generic; the markdown special-casing was the accident.
- **Container size is a snapped default, not depth-driven.** A 24px perimeter
  gutter can't carry a depth gesture without tiny/unusable sizes or corner math.
- **Workspace teardown moves from last-terminal to last-leaf.** Reflects "if I
  left a tile here, I want it." Revises the docked-tiles decision doc.
- **Keep `DockTile` for first-dock, add `MoveLeaf` for relocation.** Creating a
  new leaf and moving an existing one are different ops sharing `insertBesideLeaf`.

## Resolved

- **PTY-less workspaces hidden by default**; the sidebar display popover toggle
  reveals them.
- **Sessionless workspace keeps its workspace name** (don't relabel it to the
  tile). Separately, the **tile header title** derives from content: markdown
  H1 title → else beginning of text → else filename basename (today it's always
  `basename(path)`, `WorkspaceDockTile.tsx:237`). Title may start as basename and
  refine once content arrives.

## Follow-ups

- Center-square **swap** gesture (drop a leaf on another's center → swap places).
- Graduated container frame (depth-sized container docks) if fixed-½ feels limiting.
- Document-only workspaces created without any agent (e.g. `attn open file.md`
  into a fresh sessionless workspace) — only meaningful once PR4 lands.
