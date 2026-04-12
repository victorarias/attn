# Native GPUI Canvas UI

## Status

Spike plan. Prototypes to prove feasibility before building the real client.

**Completed:** Spikes 1, 2, 3, 4 (`attn-native`, `attn-native`, `attn-canvas`, `attn-spike4`)
**Next:** Spike 5 (Workspace Entity Model + Sidebar) — prerequisite: SessionLayout rename commit first

## Why

The current Tauri+React frontend works but is a web view. A native GPUI canvas UI would give:

- GPU-accelerated rendering via Metal (macOS) with native performance
- tldraw-style infinite canvas where agent panels are spatial objects you pan, zoom, drag, and resize
- Multiple panel types per workspace: terminals, todo lists, browsers, drawing canvases
- Multiple terminals with different agents per workspace/session
- The same framework that powers Zed editor, with access to its component ecosystem

The Go daemon stays. Communication stays over websocket (`ws://localhost:9849`). This replaces only the frontend.

## Core Concepts

### Workspace = Session

A workspace is a session with metadata (title, directory, agents, state). It is the top-level container.

### Panels are Views, Not Agents

A panel is a typed UI container placed on the canvas. A terminal panel may host an agent, but the panel itself is just a view. A workspace can have many panels of different types:

- Terminal panels (agents, shells)
- Todo list panels
- Browser panels
- Drawing canvas panels
- Future: whatever makes sense

Session metadata (title, state, directory, branch) belongs to the workspace, not to individual panels.

### Entity Model

```
Workspace (session metadata, canvas state)
  ├── Panel: Terminal (agent A)
  ├── Panel: Terminal (agent B)
  ├── Panel: Terminal (shell)
  ├── Panel: TodoList
  └── Panel: Browser
```

## Architecture Summary

### Rendering

Two-layer approach (proven by gpui-flow):

1. `canvas()` layer for painted elements: grid background, connection lines, selection box
2. Absolute-positioned `div()` elements for interactive panels

Nodes store canvas-space (world) coordinates. Screen positions AND sizes are computed via `Viewport { origin, zoom }` transform every render — panels scale with zoom so the canvas feels like tldraw.

The one exception is the terminal surface inside a panel: the `TerminalSurfaceElement` paints at a fixed cell size regardless of zoom so text stays readable. The panel frame scales; the terminal content inside does not.

### Terminal Rendering

Custom GPUI `Element` (following Zed's `TerminalElement` pattern):

- `TerminalModel` receives PTY bytes from daemon over websocket, feeds to VTE parser
- `TerminalSurfaceElement` implements `Element` trait: `prepaint` builds glyph runs for dirty rows, `paint` renders cells
- `TerminalView` owns `FocusHandle` + `InputHandler` for keyboard routing
- PTY updates go directly to `TerminalModel`, never touching workspace/canvas state — one busy terminal does not re-render other panels

### Focus Routing

- `WorkspaceStore.focused = Some(panel_id)` is the single source of truth
- Only the focused panel's `TerminalView` forwards keyboard events
- Click terminal body = focus that terminal
- Panel drag starts from header/resize handles only, never from terminal body

### Daemon Integration

- Session lifecycle events (register, state change, unregister) route to `WorkspaceStore`
- PTY stream events (output, attach, desync) route directly to per-panel `TerminalModel`, bypassing workspace state
- Keyboard input flows: focused terminal → encode keystrokes → `DaemonClient` → websocket → Go daemon

## Spike Plan

Spikes 1+2 and spike 3 can run in parallel. They merge at spike 4.

```
Spike 1 (GPUI + WS)  ──→  Spike 2 (Terminal Element)  ──┐
                                                           ├──→  Spike 4 (Merge)  ──→  Spike 5 (Entity Model)
Spike 3 (Infinite Canvas)  ───────────────────────────────┘

Spike 6 (UI Test Automation) starts after Spike 1, needs a running GPUI window to test against.

Spike 6 (UI Test Automation)  ←── Spike 1 (needs a window + daemon connection)
```

### Spike 1: Bare GPUI Window + Daemon WebSocket

**Proves**: GPUI app can connect to the existing Go daemon, receive events, stay alive without blocking the UI thread.

**Build**:
- Minimal `attn-native-app` binary that opens a GPUI window with a status label
- `DaemonClient` entity that connects to `ws://localhost:9849` and deserializes `ServerEvent`
- On `InitialState`: display session count
- On `SessionStateChanged`: update display in real time

**Done when**: App starts, connects to running daemon, shows live session list updating.

**Key risk**: Can GPUI + tokio websocket coexist without blocking the UI thread?

**Effort**: Small (hours)

---

### Spike 2: Terminal Surface Element (Daemon-Fed)

**Proves**: Terminal output from the daemon PTY stream can be rendered as a custom GPUI `Element` with keyboard input flowing back.

**Build**:
- `TerminalModel` that receives base64 PTY bytes, feeds to VTE parser (`alacritty_terminal` or `vte` crate), maintains `ScreenGrid`
- `TerminalSurfaceElement` implementing GPUI `Element` trait — paints cells in `prepaint`/`paint`
- `TerminalView` wrapping the element, owning `FocusHandle`, implementing `InputHandler` for keyboard
- Hardcode: pick the first session from daemon, send `AttachSession`, render its PTY output fullscreen
- Type in the terminal, keystrokes go back via `PtyInput`

**Done when**: You can interact with a live coding agent through the native GPUI terminal. `ls`, `vim`, colors, cursor positioning all work.

**Key risks**:
- Is the VTE → ScreenGrid → paint pipeline fast enough?
- Does GPUI's `InputHandler` keyboard routing work for terminal input?
- Can we handle ANSI escape sequences correctly enough for agent CLIs?

**This is the hardest spike. Everything after applies known patterns.**

**Effort**: Large (days)

---

### Spike 3: Infinite Canvas with Dummy Panels

**Proves**: The gpui-flow-style canvas works for this use case — pan, zoom, drag, resize, select.

**Build**:
- `Viewport` with `flow_to_screen` / `screen_to_flow`
- `WorkspaceCanvasView` as root div with `canvas()` layer for grid + absolute-positioned panel divs
- `PanelView` as simple colored rectangles with title bar, drag handle, 4 resize handles
- Populate with 5-10 dummy panels at fixed positions
- Implement: pan (drag empty space), zoom toward cursor (scroll), drag panels, resize panels, box selection (shift+drag)
- Viewport culling: skip offscreen panels

**Done when**: You can pan and zoom around a canvas with draggable, resizable colored rectangles. Feels like tldraw.

**Key risks**:
- Does absolute-positioned div approach scale with many panels?
- Does GPUI event routing handle overlapping panels correctly?
- Is pan/zoom smooth at 60fps?

**Effort**: Medium (1-2 days)

---

### Spike 4: Terminal Panels on Canvas

**Proves**: Terminal rendering works inside canvas panels with correct focus routing, resize, and multiple terminals.

**Build**:
- Merge spike 2 + spike 3
- Replace dummy panel content with `TerminalView`
- Wire focus: click terminal body → `WorkspaceStore.focused = panel_id` → that panel's `TerminalView.focus()`
- Wire resize: panel resize → compute cols/rows from pixel size → `PtyResize` to daemon
- Open 2-3 terminals attached to different sessions

**Done when**: Two or more live agent terminals on an infinite canvas. Click one to type in it. Resize and the PTY reflows. Pan/zoom around them.

**Key risks**:
- Does focus routing work with multiple terminals on a canvas?
- Does resizing cause PTY geometry issues?
- Can you switch between panels without dropping keystrokes?

**Effort**: Medium (1-2 days)

---

### Spike 5: Workspace Entity Model + Sidebar

**Proves**: The workspace/session/panel entity model works end-to-end — daemon owns workspace lifecycle, native UI reflects it, sidebar shows rollup status, canvas has mixed panel types.

#### Concept definitions

**Session** (existing daemon concept): one agent, many PTYs, one directory. Registered via `spawn_session` WebSocket command from the UI, or via unix socket from `cmd/attn` CLI.

**Workspace** (new concept): a directory + multiple agents (sessions) + panels of any type. First-class daemon entity with its own ID and lifecycle. One sidebar row per workspace. Workspace status = rollup of its member sessions' statuses (`working > waiting_input > pending_approval > idle > unknown`).

#### Prerequisite: SessionLayout rename

The daemon already has a concept called `workspace` — but it means something different: the split-pane layout within a single session (main agent pane + shell panes). To free the name and eliminate confusion, rename it first in its own commit:

| Before | After |
|---|---|
| `internal/workspace` package | `internal/sessionlayout` package |
| `workspace.Snapshot` | `sessionlayout.SessionLayout` |
| `protocol.WorkspaceSnapshot` | `protocol.SessionLayout` |
| `EventWorkspaceSnapshot`, `EventWorkspaceUpdated` | `EventSessionLayout`, `EventSessionLayoutUpdated` |
| `CmdWorkspaceGet`, `CmdWorkspaceSplitPane`, etc. | `CmdSessionLayoutGet`, `CmdSessionLayoutSplitPane`, etc. |

This rename cascades through Go daemon, generated protocol types, and Tauri frontend (event/command name strings). Tauri behavior does not change — only names change. Bump protocol version.

#### Daemon changes

New `Workspace` entity in the daemon:
```
Workspace
  id:        string   (uuid)
  title:     string   (basename of directory, or user-set)
  directory: string
  status:    WorkspaceStatus  (rollup of member sessions)
```

Status rollup rule: `working > waiting_input > pending_approval > idle > unknown`. Recomputed whenever any member session's state changes.

New WebSocket events (old Tauri client ignores unknown event types — forward compatible):
```
WorkspaceRegistered   { id, title, directory, status }
WorkspaceUnregistered { id }
WorkspaceStateChanged { id, status }
```

New WebSocket commands:
```
register_workspace    { id, title, directory }  →  WorkspaceRegistered broadcast
unregister_workspace  { id }                    →  WorkspaceUnregistered broadcast
```

`InitialState` grows a `workspaces: []Workspace` field (old Tauri client ignores unknown fields on deserialized objects).

Sessions get an optional `workspace_id: string | null` field — populated when a session is spawned within a workspace context.

Bump protocol version. Tauri frontend gets a no-op handler for the three new event types.

#### Native UI module layout

New files:
```
spike5_main.rs          binary entry point (cargo run --bin attn-spike5)
spike5_app.rs           Spike5App root view — owns sessions, lays out sidebar + canvas
workspace_session.rs    Entity<WorkspaceSession> — metadata + panels + rollup status
panel.rs                Panel, PanelContent enum, PlaceholderView
sidebar.rs              Sidebar view — workspace list with rollup status badges
spike5_canvas.rs        Canvas view refactored to read panels from one WorkspaceSession
```

Unchanged: `canvas_view.rs`, `daemon_client.rs`, `terminal_model.rs`, `terminal_view.rs`

#### Native UI entity model

`WorkspaceSession` is a GPUI `Entity` (not a plain struct) because the sidebar and canvas are siblings — they can't share data through a parent-child read. Each holds cloned `Entity<WorkspaceSession>` handles and subscribes independently.

```
Spike5App
  daemon:    Entity<DaemonClient>
  sessions:  Vec<Entity<WorkspaceSession>>   ← source of truth
  sidebar:   Entity<Sidebar>
  canvas:    Entity<Spike5Canvas>

WorkspaceSession
  id, title, directory
  status: WorkspaceStatus                    ← updated from DaemonEvent::WorkspaceStateChanged
  panels: Vec<Panel>

Panel
  id, world_x, world_y, width, height
  content: PanelContent

PanelContent                                 ← enum dispatch, not trait objects
  Terminal(Entity<TerminalView>)
  Placeholder(Entity<PlaceholderView>)
```

**Why enum dispatch over trait objects or `AnyView`**: panels need type-specific behavior beyond rendering (resize → `PtyResize` only for Terminal panels, focus handle only on Terminal panels). `AnyView` gives type erasure for rendering only; when you need to call type-specific methods, you need the enum back anyway.

**Why `Entity<WorkspaceSession>` over plain struct**: sidebar and canvas are peer views, not parent-child. Two views sharing state across a sibling boundary requires GPUI entity handles. Agent waiting-status badges in the sidebar also require reactive updates from daemon events — `cx.notify()` on the entity re-renders all subscribers.

#### Data flow

```
DaemonClient
  └── Spike5App subscribes
        on WorkspaceRegistered/Unregistered → create/remove Entity<WorkspaceSession>
        → push handles to Sidebar + Spike5Canvas

WorkspaceSession (each) subscribes to DaemonClient, filters by own id
  on WorkspaceStateChanged → update status → cx.notify()
    ├── Sidebar re-renders status badge
    └── Spike5Canvas re-renders panel border color (focused/unfocused)
```

#### Layout

```
┌─────────────────────────────────────────────────────┐
│  Sidebar (fixed width, anchored left)  │  Canvas    │
│                                        │            │
│  ● Workspace A  [working]              │  panels    │
│  ● Workspace B  [waiting]              │  from      │
│  ● Workspace C  [idle]                 │  selected  │
│                                        │  workspace │
└─────────────────────────────────────────────────────┘
```

Clicking a workspace row sets `Spike5App.selected` → passes new `Entity<WorkspaceSession>` handle to `Spike5Canvas`.

**Build**:
- Prerequisite rename commit: `internal/workspace` → `internal/sessionlayout` + protocol + Tauri no-ops
- Daemon `Workspace` entity: register/unregister/status-rollup, new WS events + commands
- `InitialState` extended with `workspaces` array; sessions get optional `workspace_id`
- Protocol version bump
- Tauri: no-op handlers for three new workspace events
- Native UI: `WorkspaceSession` entity, `Panel`/`PanelContent` enum, `Sidebar`, `Spike5Canvas`, `Spike5App`
- Placeholder panel type (`PlaceholderView`: static text "Todo Panel") alongside Terminal panels

**Done when**: Sidebar shows live workspaces with rollup status badges. Canvas shows panels for the selected workspace, with at least one Terminal panel and one Placeholder panel coexisting. Adding a new panel type is: define struct, implement `Render`, add one enum arm.

**Key risks**:
- SessionLayout rename is wide — protocol, Go, TypeScript — easy to miss a reference
- Forward-compat assumptions (old client ignores unknown fields/events) need verification against the actual Tauri deserialization code
- Workspace status rollup logic must handle sessions joining/leaving mid-lifecycle

**Effort**: Medium (2-3 days — daemon + protocol + rename + Tauri compat + native UI entity model)

### Spike 6: UI Test Automation Sidecar

**Proves**: The native GPUI app can be driven programmatically by external test scripts, matching the existing Tauri app's 3-layer test automation capability.

**Why this matters**: The current Tauri app has a battle-tested test infrastructure (`useUiAutomationBridge.ts` + `uiAutomationClient.mjs` + `InputDriver.swift`). Without an equivalent for the native app, every spike after this one ships untestable. Build this early so every subsequent spike gets automated regression coverage.

**Existing infrastructure (what we have today)**:

1. **App-side bridge** (`app/src/hooks/useUiAutomationBridge.ts`): React hook that listens for Tauri events, handles ~40 actions (`ping`, `get_state`, `create_session`, `select_session`, `read_pane_text`, `type_pane_via_ui`, `capture_screenshot_data`, etc.), writes a manifest to `~/Library/Application Support/com.attn.manager/debug/ui-automation.json` with `{ enabled, port, token, pid }`.
2. **Client driver** (`app/scripts/real-app-harness/uiAutomationClient.mjs`): Node.js TCP client. Reads manifest → connects → sends `{ id, token, action, payload }` → receives `{ ok, result/error }`.
3. **macOS input driver** (`app/scripts/real-app-harness/macosDriver.mjs` + `InputDriver.swift`): Compiled Swift binary using CGEvent APIs for real keyboard/mouse input. Commands: `activate`, `text`, `key`, `keycode`, `click`. Targets by `--bundle-id`.

**What the native app needs (build)**:

Layer 1 — **Rust TCP automation server** (replaces `useUiAutomationBridge.ts`):
- Embedded TCP server inside the GPUI app, started on launch when a `--automation` flag is present (or env var `ATTN_AUTOMATION=1`)
- Same JSON wire protocol: receives `{ id, token, action, payload }`, returns `{ ok, result/error }`
- Writes the same manifest shape to a known path (e.g. `~/Library/Application Support/com.attn.native/debug/ui-automation.json`) so the existing client can discover it
- Initial action set (minimal for spike):
  - `ping` — liveness check
  - `get_state` — return workspace/session state from `WorkspaceStore`
  - `list_sessions` — enumerate sessions from daemon
  - `get_window_geometry` — return window size, position
- The server runs on a background tokio task, queries GPUI state via `cx.update()` / model handles, and serializes responses

Layer 2 — **Client driver** (reuse existing):
- `uiAutomationClient.mjs` should work as-is with a different manifest path
- Add a `--manifest-path` option (or env var) so the same client can target either the Tauri app or the native app
- Alternatively, the native app writes to the same manifest path as the Tauri app (simpler, but can't run both simultaneously)

Layer 3 — **macOS input driver** (reuse as-is):
- `InputDriver.swift` and `macosDriver.mjs` work against any macOS app given a bundle ID
- The native app just needs a different `--bundle-id` (e.g. `com.attn.native` vs `com.attn.manager`)
- No changes needed

**Done when**:
- Native GPUI app starts with `--automation`, opens TCP server, writes manifest
- `uiAutomationClient.mjs` connects to the native app, sends `ping`, gets `{ ok: true }`
- `get_state` returns live session data from the daemon (proves the automation server can read GPUI app state)
- A simple test script: launch native app → connect automation client → verify daemon state → send OS-level keystrokes via `InputDriver.swift` → verify state changed

**Key risks**:
- Can a Rust TCP server inside GPUI access model/view state safely from a background thread? (Likely yes — GPUI entities are accessed via `cx.update()` which schedules onto the main thread)
- Wire protocol compatibility: can we share the exact JSON format with the existing client, or do the Tauri-specific actions need abstraction?
- Manifest path collision if both apps are installed

**Design decision**: Start with a minimal action set. Each subsequent spike adds the actions it needs for its own tests (e.g. Spike 4 adds `read_pane_text`, `focus_panel`). The automation server grows incrementally with the app.

**Effort**: Small-Medium (1 day). The hard design work is already done in the Tauri app — this is a port of the server side to Rust, reuse of the client side as-is.

---

## Things to Watch

### Terminal body vs canvas gestures
Panel drag must NOT start from the terminal body — only from header and resize handles. Terminal body click means "focus this terminal for keyboard input." Getting this wrong makes the whole thing feel broken.

### Wheel event routing
Empty space scroll: pan/zoom canvas. Over terminal: scroll terminal scrollback. Cmd+scroll over terminal: zoom canvas. Must be explicit, not inherited.

### PTY sequence ordering
The daemon sends sequence numbers on `PtyOutput`. Drop any output where `seq <= last_seq`. On `AttachResult`, replay segments are restore data, not live output. Mark `last_seq` from the attach response before switching to live mode.

### Layout persistence
Layout state (panel positions, sizes, which panels are open) is not part of the current daemon protocol. For now, ephemeral is fine. If it needs to persist across restarts, that is a separate protocol addition — do not overload session events for this.

## Dependencies

- GPUI: `gpui` crate from `zed-industries/zed` (git dependency)
- VTE parsing: `alacritty_terminal` or `vte` crate
- WebSocket: `tokio-tungstenite` or similar async WS client
- Existing Go daemon: no changes needed for the spike phase

## Not In Scope (for spikes)

- Minimap (add after spike 4, depends only on panel rects)
- Undo/redo history for panel layout changes
- Magnetic snap-to-edge for panel positioning
- Connection lines between panels
- Any panel types beyond terminal and placeholder
- Persistence of workspace layout
- Remote daemon support in native UI
- Mobile or web targets
