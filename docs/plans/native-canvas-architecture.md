# Native Canvas — Architecture

Living design overview for `attn-native`, the GPUI canvas client. Companions:
`2026-04-28-native-canvas-mvp.md` (sequencing) and
`native-gpui-canvas-ui.md` (historical spike plan).

## What It Is

A native macOS app that talks to the existing Go daemon over websocket and
presents the user's workspaces as an infinite, pannable, zoomable canvas.
Each workspace is a directory full of running things — coding agents,
shells, todo lists, diff viewers — laid out in space, navigable by keyboard.
Replaces the canvas use case of the Tauri app; coexists with it during
buildout.

## System Context

```
                ┌──────────────────────┐
                │   Go daemon          │ ← single source of truth
                │   (~/.attn/daemon)   │
                └──┬───────────┬───────┘
                   │ ws:9849   │ unix-socket
        ┌──────────┴───┐   ┌───┴─────────┐
        │ attn-native  │   │ attn (CLI)  │
        │ (GPUI canvas)│   └─────────────┘
        ├──────────────┤
        │ Tauri app    │  ← still ships
        │ (existing)   │
        └──────────────┘
```

The daemon is unchanged in shape. New native client speaks the same wire
protocol as Tauri. Both clients can run simultaneously against the same
daemon — they observe each other's actions, no special coordination.

## Process Model

A single Rust binary:

- **Main thread**: GPUI / Metal. All UI work. Owns the entity tree.
- **Background async (`smol`)**: websocket I/O to the daemon, automation TCP
  server, anything else that shouldn't block paint.
- **Daemon process**: separate, not managed by this client. Auto-launched by
  the existing daemon discovery if not running. Survives client restarts —
  killing the canvas client loses no work.

## Key Abstractions

### Workspace

A directory + a set of running sessions + a canvas layout. Daemon-authoritative.
Has a stable id, a status rollup (`launching > working > waiting_input >
pending_approval > idle`), and a list of panels. One workspace fills one
canvas at a time; the sidebar shows every workspace with its rollup status.

### Session

A single running agent (Claude / Codex / Copilot / Pi / Shell). Daemon
concept, mostly unchanged. A session has one PTY and lives inside exactly
one workspace. Sessions in the same workspace can be different agents.

### Panel

One running thing on the canvas. Exactly one of:

- Terminal (hosts a session — agent or shell)
- Todo list
- Diff viewer
- Future panel types

A panel never hosts two agents. A panel never hosts multiple terminals.
Splits create new sibling panels, they don't subdivide an existing panel.
Panels carry world-space position + size, plus a content type and the
content's local state.

### Canvas + Viewport

The pannable, zoomable surface. A `Viewport { origin, zoom }` transforms
world coordinates to screen coordinates and back. Panels are rendered as
absolute-positioned elements; the canvas itself is an empty interaction
target. No visible grid (snapping reads `GRID_SPACING` directly when
implemented).

### Focus model — two stages

- **Selected**: canvas-level focus. Keyboard navigation (next/prev, splits,
  close) operates on this panel. Visible as a frame highlight.
- **Selected with input focus**: keystrokes route into the panel's
  PTY/widget. Visible as a stronger highlight + active cursor.

The toggle between the two is explicit. Canvas-level keybindings only fire
when no panel has input focus. Escape from input focus drops back to
selected. The user must always know where keys go.

## Module Layout

The client lives in `native-ui/crates/`:

```
attn-protocol/             ← wire types: Workspace, Session, Panel,
                              events, commands. Shared with anything else
                              that wants to talk to the daemon in Rust.

attn-native-app/
  bin: attn-native          ← the production binary
  src/
    main.rs                 entry point (window + Application boilerplate)
    app.rs                  root view, top-level wiring
    daemon_client.rs        websocket I/O + DaemonEvent emitter
    terminal_model.rs       alacritty parser, PTY byte ingestion, screen state
    terminal_view.rs        custom GPUI Element, paint_quad-per-cell
    panel.rs                Panel struct + PanelContent enum dispatch
    canvas.rs               pan/zoom/drag/resize/hit-test on selected workspace
    viewport.rs             world ↔ screen transform, helpers
    workspace.rs            Entity<Workspace> — sidebar + canvas peer view
    sidebar.rs              workspace list with rollup status badges
    automation/             TCP automation sidecar (test harness)
    fps_overlay.rs          opt-in frame-time overlay (perf debugging)
    synthetic.rs            opt-in synthetic-load harness (perf debugging)
```

`canvas.rs` may grow into a `canvas/` directory once split-handling and
keyboard navigation arrive; for now a single file is enough.

### Perf debugging affordances

`fps_overlay.rs` and `synthetic.rs` are dormant in normal use — they
cost nothing unless explicitly turned on. They survive from the
2026-04-28 perf spike not as artifacts of that investigation, but as
the toolkit for the next one.

- `ATTN_NATIVE_FPS=1` enables the FPS / frame-time overlay (top-right
  of the canvas). When on, `automation_snapshot` includes the latest
  readout so headless test scripts can read frame timing without
  inducing a render.
- `ATTN_NATIVE_SYNTHETIC_PANELS=N` (1..256) creates a synthetic
  workspace of `N` panels driven by a deterministic byte stream
  through the real renderer. `ATTN_NATIVE_SYNTHETIC_TICK_MS=K`
  controls cadence (0 = static, no ticker).
  `ATTN_NATIVE_SYNTHETIC_BYTES=B` controls bytes per panel per tick.
- The `set_zoom` automation action drives the canvas zoom
  programmatically; when the FPS overlay is on, it resets the counter
  for a clean measurement window.

## State Ownership Rules

The single hardest question in a multi-client app: who owns what.

| What                        | Lives where                | Notes                                          |
| --------------------------- | -------------------------- | ---------------------------------------------- |
| Workspaces (existence, dir) | Daemon                     | Created/destroyed via WS commands              |
| Sessions                    | Daemon                     | Spawn/kill via WS commands                     |
| Session state (working/idle)| Daemon (classifier output) | Streamed via events                            |
| PTY bytes                   | Daemon                     | Streamed via `PtyOutput` with sequence numbers |
| Panel layout (positions, sizes, types) | Daemon            | Persisted; survives client restarts            |
| Viewport (per workspace)    | Daemon                     | Persisted; survives client restarts            |
| Focus selection             | Local to client            | Multiple clients can focus different panels    |
| Scroll offset within terminal | Local to client          | Visual-only, ephemeral                         |
| In-flight render/animation  | Local to client            | Never persisted                                |

Layout writes go daemon-side at meaningful boundaries (drag-end,
resize-end, viewport-pan-end), not on every delta. Reads from the daemon
are authoritative — a remote change from the Tauri app or another machine
shows up in real time.

## Invariants Carried From The Spike

These are non-negotiable. They were learned the hard way; don't relitigate
without a reason.

- **PTY geometry has one authority.** The most recently active interactive
  client owns it. `pty_resize` is authoritative; `attach_session` replay is
  provisional context. Local `fit()` calls are not proof the PTY is correct.
- **Sequence numbers gate live PTY output.** Drop output where
  `seq <= last_seq`. Lock `last_seq` from the attach response before
  accepting live mode.
- **Panel drag starts only from header/handles, never from terminal body.**
  A click on the terminal body means "give this terminal input focus." The
  whole thing feels broken if this is wrong.
- **Wheel routing is explicit.**
  - Empty canvas space: pan/zoom canvas
  - Over a terminal panel (no modifier): scroll its scrollback
  - `Cmd+wheel` over a terminal: zoom canvas
  - Scope decisions like this should never be inherited from defaults.
- **Cell rendering uses `paint_quad`, not `div().bg()`.** Div backgrounds
  are constrained to text layout height; cells need full row height.
- **Panel size scales with zoom; cols/rows do not.** `cols/rows` are derived
  from world-space dimensions and only change on user resize → `PtyResize`.
- **Subscribe-and-filter, don't broadcast-and-rerender.** PTY output routes
  directly to the addressed `TerminalModel`. One busy terminal must not
  re-render the whole app.

## Test Story

Test infra is a workstream that grows with features, not a phase. Three
layers, all ported from the Tauri side:

- **Layer 1 — Automation server**: in-process Rust TCP server, JSON-RPC.
  Each new feature ships with the actions needed to drive it
  (`type_into_panel`, `create_workspace`, `split_panel_right`, etc.).
- **Layer 2 — Client driver**: existing `uiAutomationClient.mjs` (Node)
  reused as-is via the manifest discovery contract. Profile-aware
  manifest paths so prod vs. dev installs don't collide.
- **Layer 3 — OS input driver**: existing `InputDriver.swift` reused
  as-is. Targets the native bundle id (`com.attn.native[.<profile>]`).

`make test-native` becomes the entry point. Renderer snapshot tests where
they pay off; not a target by themselves.

## Distribution

Long deferred. Built-from-source is the distribution channel until M3.

- Dev iteration: `cargo run --bin attn-native` against a dev daemon.
- Profile separation: `ATTN_PROFILE=dev` → bundle id `com.attn.native.dev`,
  data dir `~/.attn-dev/`, port 29849. Same scheme as the Tauri side.
- No code signing, no auto-update, no installer until M3. Author runs HEAD.

## Out Of Scope (For Now)

- Panel types beyond terminals (todo, diff, etc.) — unlocked once the
  panel-content enum is ergonomic; deferred until terminals are rock-solid.
- Multi-machine canvas synchronization — daemon is local. Remote daemon
  support is an M3+ topic.
- Mobile or web targets.
- Connection lines between panels, magnetic snapping visuals, minimap,
  undo/redo of layout changes.
- Complex IME, RTL text — terminal renderer minimums first; cover the
  edges as users hit them.
- Telemetry, crash reporting, automatic bug submission — M3+.

## Related Documents

- `docs/plans/2026-04-28-native-canvas-mvp.md` — what we build, in what
  order, to reach dogfood.
- `docs/plans/2026-04-28-canvas-perf-spike.md` — perf spike findings; named
  the bottleneck (visible grid) and the cure (drop it).
- `docs/plans/native-gpui-canvas-ui.md` — historical record of spikes 1–6.
- `native-ui/CLAUDE.md` — accumulating gotchas and rework lessons. Add to
  it when something looks reasonable but ends up being wrong.
