# Docked Tiles Are Daemon-Owned Layout Leaves

Date: 2026-05-31

## Decision

A "docked tile" (markdown today, more later) is a first-class leaf in the
daemon's workspace layout tree, next to terminal-pane leaves. The daemon owns
and persists its position and size; the frontend renders it. This is distinct
from the slide-in overlay panels (diff, review loop, git status, PR), which stay
frontend-only and are **not** changed by this work.

## Why

Window configuration must be authoritative and portable: close and reopen the
app, or connect from another client, and you should get the same layout — tiles
included. A frontend-only tile (pixel position in one client) cannot give that.
Putting tiles in the layout tree means they ride the existing `layout_json`
persistence and broadcast for free, and resizing reuses the split-ratio
machinery rather than inventing a parallel one.

## Two docking categories (keep them separate)

| | Docked tiles (this doc) | Slide-in overlays |
|---|---|---|
| Examples | markdown (future: more) | diff, review loop, git status, PR |
| Lives in | daemon layout tree | frontend (`RightDock`/`SidePanel`) |
| Space | takes real space, reflows terminals | floats on top |
| Persistence | daemon-owned, cross-restart/client | ephemeral, per-client |
| Resize | shared split divider + `set_split_ratio` | fixed/width offset |

Do not migrate the slide-in overlays into the layout tree. They are a different,
intentionally transient UX.

## Model

- Layout `Node` gains a `tile` leaf: `{ tile_id, tile_kind, tile_params }`.
  `tile_kind` and `tile_params` are **opaque** to the layout package — it
  persists where a tile sits, not what it renders. `tile_params` carries the
  tile's content reference (for markdown, the absolute file path) so reopening
  the app or another client reproduces the same document, not just an empty
  tile. New kinds are a frontend-only change; the layout stores another opaque
  leaf.
- A docked tile sits behind a normal split, created `ratio_locked` so the tile
  keeps its size and is never auto-equalized with terminals during
  normalization. `splitChainSpanCount` treats a locked split as one opaque unit.
- Tiles are excluded from pane bookkeeping (`PaneIDs`, the `panes` array,
  session reconcile).
- **A workspace lives while its layout holds any leaf, not just a terminal.**
  Teardown keys off `workspacelayout.LayoutEmpty` (no panes *and* no tiles), not
  `len(panes) == 0`. Closing the last terminal while a tile remains leaves a
  sessionless, tile-only workspace alive instead of discarding the tile; the
  workspace is only torn down once the layout has no leaves at all. The guard
  lives in `dissociateSessionFromWorkspace` (keep the workspace entity when a
  tile remains), the layout teardown sites (`ensureWorkspaceLayout`,
  `handleWorkspaceLayoutClosePane`, `removeWorkspaceLayoutPaneForSession`), and
  the startup reap (`loadWorkspacesFromStore`). Sessionless workspaces are hidden
  in the sidebar by default and revealed via the display popover toggle; they
  render with a neutral, non-state indicator. (Revises the original rule, which
  tore down the workspace with the last terminal and took any orphan tile
  with it.)

## Protocol

- `workspace_layout_dock_tile { workspace_id, anchor_pane_id, edge, tile_id,
  tile_kind, tile_params?, ratio? }` — idempotent: an existing instance of
  `tile_id` is relocated, so dock doubles as move. `edge` ∈
  {left,right,top,bottom} → (direction, side). `ratio` is the tile's fraction
  (defaults to ~1/3).
- `workspace_layout_undock_tile { workspace_id, tile_id }`.
- Resize reuses `workspace_layout_set_split_ratio`. Protocol bumped to 76.

## Content is daemon-served, not frontend-read

The frontend never reads tile files itself. The daemon owns content the same
way it owns layout, so every client sees the same thing and live reload is
authoritative.

- **Subscribe/get + broadcast.** On mount (and whenever its `tile_params`
  retargets a new file) the frontend sends `workspace_tile_content_get
  { workspace_id, tile_id }`. The daemon replies with
  `workspace_tile_content { workspace_id, tile_id, tile_kind, path, content,
  error? }`. The same event is **broadcast** to all clients when a watched file
  changes — that is the live-reload path; no polling from the frontend.
- **Keyed by (workspace_id, tile_id).** `tile_id` (e.g. `tile-markdown`)
  repeats across workspaces, so the frontend caches content under the composite
  key, not the tile id alone.
- **Poll-based watcher, no fsnotify.** A single daemon goroutine ticks (~750ms),
  re-derives the watch set from the stored layouts each tick (so it self-heals on
  restart and drops undocked tiles), and broadcasts when a file's
  size/mtime/existence fingerprint changes. Polling was chosen deliberately over
  fsnotify: no new dependency, and it is robust to editors that save atomically
  via rename. Reads are capped (5 MiB) and missing/dir/empty files become a clear
  `error` the tile can render instead of a failure.

## Opening: `attn open`, CLI-driven

- `open_markdown { path, session_id? }` (unix socket) docks a markdown tile on
  the resolved session's pane with `tile_params = absolute path`, then
  broadcasts its content. The `attn open <file.md>` CLI is the entry point; the
  bundled agent skill documents it so agents can show the user a rendered doc.
- **Session resolution:** explicit `--session` / `session_id` > `ATTN_SESSION_ID`
  (the agent's own session) > the daemon's currently-selected session (tracked
  from `session_visualized`). This is what makes a bare `attn open notes.md`
  land next to whatever the user is looking at.
- **No empty "show tile" toggle.** A markdown tile only exists once it points
  at a real file, so there is no sidebar button that docks a blank tile. Tiles
  are still re-dockable by dragging.

## Behavioral rules

- **Resting state is closed or docked — never free-floating.** A floating window
  can't reproduce the same configuration across clients/screen sizes. Dragging a
  tile shows a transient ghost + target highlight and always lands docked; a
  drop outside any pane is a no-op (the tile stays where it was).
- **Terminal arrow-key navigation skips tiles.** A tile is not a focus target.
- Dropping onto a terminal docks on that terminal's nearest edge; dropping near
  the border between two terminals slots the tile between them.

## Adding a future tile kind

1. Add the kind to the frontend `TileKind` union and render it in
   `WorkspaceDockTile`.
2. Dock it with `sendWorkspaceDockTile(workspaceId, id, kind, …)`, passing any
   content reference (path, url, id) as `tile_params`.

For a *layout-only* tile (renders from `tile_params` alone, no daemon-side
content), no daemon or protocol change is required — that is the point of the
opaque `tile_kind`/`tile_params`. A tile that needs the daemon to read and
watch a resource (like markdown) additionally hooks into the content
watcher/broadcast path described above; that is the one place the daemon stops
being kind-agnostic.
