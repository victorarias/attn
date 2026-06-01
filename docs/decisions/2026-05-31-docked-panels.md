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

- Layout `Node` gains a `panel` leaf: `{ panel_id, panel_kind, panel_params }`.
  `panel_kind` and `panel_params` are **opaque** to the layout package — it
  persists where a panel sits, not what it renders. `panel_params` carries the
  panel's content reference (for markdown, the absolute file path) so reopening
  the app or another client reproduces the same document, not just an empty
  panel. New kinds are a frontend-only change; the layout stores another opaque
  leaf.
- A docked panel sits behind a normal split, created `ratio_locked` so the panel
  keeps its size and is never auto-equalized with terminals during
  normalization. `splitChainSpanCount` treats a locked split as one opaque unit.
- Panels are excluded from pane bookkeeping (`PaneIDs`, the `panes` array,
  session reconcile). Closing the last *terminal* still tears down the workspace
  and takes any orphan panel with it.

## Protocol

- `workspace_layout_dock_panel { workspace_id, anchor_pane_id, edge, panel_id,
  panel_kind, panel_params?, ratio? }` — idempotent: an existing instance of
  `panel_id` is relocated, so dock doubles as move. `edge` ∈
  {left,right,top,bottom} → (direction, side). `ratio` is the panel's fraction
  (defaults to ~1/3).
- `workspace_layout_undock_panel { workspace_id, panel_id }`.
- Resize reuses `workspace_layout_set_split_ratio`. Protocol bumped to 76.

## Content is daemon-served, not frontend-read

The frontend never reads panel files itself. The daemon owns content the same
way it owns layout, so every client sees the same thing and live reload is
authoritative.

- **Subscribe/get + broadcast.** On mount (and whenever its `panel_params`
  retargets a new file) the frontend sends `workspace_panel_content_get
  { workspace_id, panel_id }`. The daemon replies with
  `workspace_panel_content { workspace_id, panel_id, panel_kind, path, content,
  error? }`. The same event is **broadcast** to all clients when a watched file
  changes — that is the live-reload path; no polling from the frontend.
- **Keyed by (workspace_id, panel_id).** `panel_id` (e.g. `panel-markdown`)
  repeats across workspaces, so the frontend caches content under the composite
  key, not the panel id alone.
- **Poll-based watcher, no fsnotify.** A single daemon goroutine ticks (~750ms),
  re-derives the watch set from the stored layouts each tick (so it self-heals on
  restart and drops undocked panels), and broadcasts when a file's
  size/mtime/existence fingerprint changes. Polling was chosen deliberately over
  fsnotify: no new dependency, and it is robust to editors that save atomically
  via rename. Reads are capped (5 MiB) and missing/dir/empty files become a clear
  `error` the panel can render instead of a failure.

## Opening: `attn open`, CLI-driven

- `open_markdown { path, session_id? }` (unix socket) docks a markdown panel on
  the resolved session's pane with `panel_params = absolute path`, then
  broadcasts its content. The `attn open <file.md>` CLI is the entry point; the
  bundled agent skill documents it so agents can show the user a rendered doc.
- **Session resolution:** explicit `--session` / `session_id` > `ATTN_SESSION_ID`
  (the agent's own session) > the daemon's currently-selected session (tracked
  from `session_visualized`). This is what makes a bare `attn open notes.md`
  land next to whatever the user is looking at.
- **No empty "show panel" toggle.** A markdown panel only exists once it points
  at a real file, so there is no sidebar button that docks a blank panel. Panels
  are still re-dockable by dragging.

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
2. Dock it with `sendWorkspaceDockPanel(workspaceId, id, kind, …)`, passing any
   content reference (path, url, id) as `panel_params`.

For a *layout-only* panel (renders from `panel_params` alone, no daemon-side
content), no daemon or protocol change is required — that is the point of the
opaque `panel_kind`/`panel_params`. A panel that needs the daemon to read and
watch a resource (like markdown) additionally hooks into the content
watcher/broadcast path described above; that is the one place the daemon stops
being kind-agnostic.
