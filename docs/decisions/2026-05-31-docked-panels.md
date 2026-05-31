# Docked Panels Are Daemon-Owned Layout Leaves

Date: 2026-05-31

## Decision

A "docked panel" (markdown today, more later) is a first-class leaf in the
daemon's workspace layout tree, next to terminal-pane leaves. The daemon owns
and persists its position and size; the frontend renders it. This is distinct
from the slide-in overlay panels (diff, review loop, git status, PR), which stay
frontend-only and are **not** changed by this work.

## Why

Window configuration must be authoritative and portable: close and reopen the
app, or connect from another client, and you should get the same layout — panels
included. A frontend-only panel (pixel position in one client) cannot give that.
Putting panels in the layout tree means they ride the existing `layout_json`
persistence and broadcast for free, and resizing reuses the split-ratio
machinery rather than inventing a parallel one.

## Two panel categories (keep them separate)

| | Docked panels (this doc) | Slide-in overlays |
|---|---|---|
| Examples | markdown (future: more) | diff, review loop, git status, PR |
| Lives in | daemon layout tree | frontend (`RightDock`/`SidePanel`) |
| Space | takes real space, reflows terminals | floats on top |
| Persistence | daemon-owned, cross-restart/client | ephemeral, per-client |
| Resize | shared split divider + `set_split_ratio` | fixed/width offset |

Do not migrate the slide-in overlays into the layout tree. They are a different,
intentionally transient UX.

## Model

- Layout `Node` gains a `panel` leaf: `{ panel_id, panel_kind }`. `panel_kind`
  is **opaque** to the daemon — it persists where a panel sits, not what it
  renders. New kinds are a frontend-only change; the daemon stores another
  opaque leaf.
- A docked panel sits behind a normal split, created `ratio_locked` so the panel
  keeps its size and is never auto-equalized with terminals during
  normalization. `splitChainSpanCount` treats a locked split as one opaque unit.
- Panels are excluded from pane bookkeeping (`PaneIDs`, the `panes` array,
  session reconcile). Closing the last *terminal* still tears down the workspace
  and takes any orphan panel with it.

## Protocol

- `workspace_layout_dock_panel { workspace_id, anchor_pane_id, edge, panel_id,
  panel_kind, ratio? }` — idempotent: an existing instance of `panel_id` is
  relocated, so dock doubles as move. `edge` ∈ {left,right,top,bottom} →
  (direction, side). `ratio` is the panel's fraction (defaults to ~1/3).
- `workspace_layout_undock_panel { workspace_id, panel_id }`.
- Resize reuses `workspace_layout_set_split_ratio`. Protocol bumped to 75.

## Behavioral rules

- **Resting state is closed or docked — never free-floating.** A floating window
  can't reproduce the same configuration across clients/screen sizes. Dragging a
  panel shows a transient ghost + target highlight and always lands docked; a
  drop outside any pane is a no-op (the panel stays where it was).
- **Terminal arrow-key navigation skips panels.** A panel is not a focus target.
- Dropping onto a terminal docks on that terminal's nearest edge; dropping near
  the border between two terminals slots the panel between them.

## Adding a future panel kind

1. Add the kind to the frontend `PanelKind` union and render it in
   `WorkspaceDockPanel`.
2. Dock it with `sendWorkspaceDockPanel(workspaceId, id, kind, …)`.

No daemon or protocol change is required — that is the point of the opaque
`panel_kind`.
