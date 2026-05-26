# Swift Native Workspace Client

## Why

Attn needs a macOS-native client that is low-memory, fast, workspace-first,
and built on a real modern terminal renderer. The Rust/GPUI workspace client
proved several important backend and terminal mechanics, but it exposed a
fundamental composition mismatch: Ghostty terminal surfaces are native
AppKit views while GPUI draws the surrounding interface in another render
surface. Modal UI either renders underneath live terminals or requires hiding
them, which is not acceptable for a terminal-first application.

The replacement client will be a Swift macOS application using AppKit and
SwiftUI around Ghostty's native surface API. The Go daemon remains the system
of record for workspaces, layouts, sessions, PTYs, activity state and
persistence. This is a client-shell rewrite, not a backend rewrite.

## Decision

Archive the Rust/GPUI client under `native-gpui-archive/` and create the
active Swift client under `native-ui/`.

The Swift client will be built as a command-line Swift Package and packaged
into a signed `.app` bundle by `make`, without an Xcode project. It will
consume the existing patched `GhosttyKit.xcframework`, which exposes
external-I/O surface callbacks:

- Ghostty parses terminal output, renders it through its Metal stack, encodes
  terminal input and calculates terminal cell geometry.
- The attn daemon owns processes, PTYs, replay ordering and session state.
- The Swift client bridges a Ghostty surface to a daemon runtime by forwarding
  surface input/resize callbacks as `pty_input` / `pty_resize`, and forwarding
  daemon replay/live output into `ghostty_surface_process_replay` /
  `ghostty_surface_process_output`.

The client will not embed Ghostty's entire app shell. It owns its
workspace/sidebar/pane/launcher product UI. The first implementation uses a
thin AppKit host over the patched external-I/O C surface API: all terminal
parsing, rendering, grid calculation and key encoding remain in Ghostty.
Before declaring input parity complete, the fork should publish Ghostty's
full native SurfaceView behavior for selection and IME integration rather
than allowing the thin host to expand into a duplicate terminal frontend.

## Vision

```text
AttnNativeApp (SwiftUI lifecycle + AppKit window coordination)
|
+-- WorkspaceStore (@MainActor ObservableObject)
|   +-- DaemonConnection (URLSessionWebSocketTask)
|   +-- sessions, workspaces, layouts, settings
|   +-- pending request/result operations
|   +-- selected workspace / focused pane UI projection
|
+-- RootWindow
|   +-- WorkspaceSidebar
|   |   +-- workspace rows
|   |   +-- nested pane rows with activity / mute / close controls
|   |
|   +-- WorkspacePaneTree
|       +-- TerminalPane -> forked Ghostty SurfaceView host (NSViewRepresentable)
|       +-- TodoPane      (SwiftUI)
|       +-- DiffPane      (SwiftUI / native text rendering)
|       +-- MarkdownPane  (SwiftUI / native rendering)
|
+-- Native presentation layers
|   +-- NewWorkspaceSheet
|   +-- AddPaneSheet
|   +-- SettingsSheet
|
+-- AutomationBridge
    +-- state snapshots
    +-- daemon-backed actions
    +-- native screenshot/window inspection
    +-- input and surface-health verification
```

Because terminal views and sheets are in one native AppKit/SwiftUI
composition hierarchy, a launcher can display above a terminal while the
terminal remains continuously visible and receives new daemon output.

## Ownership Boundaries

### Go Daemon Owns

- Workspace identity, directory, status rollup and persistence.
- `WorkspaceLayout` tree, pane IDs, active pane and split placement.
- Agent and shell runtime lifecycle.
- PTY ownership, authoritative geometry, attach/replay sequencing and output
  ordering.
- Settings, recent locations, repository/worktree operations and session
  activity/mute state.
- Async command result events and protocol versioning.

### Swift Client Owns

- Window/sidebar/sheet presentation and ephemeral selection UI.
- Observable projections of daemon snapshots.
- Creation of visible native Ghostty surface hosts from the fork's reusable
  Swift module for daemon-owned terminal panes.
- Forwarding native terminal surface callbacks to daemon commands.
- Releasing offscreen terminal surface views when a workspace is not
  displayed, subject to measured memory/latency tradeoffs.
- Native automation host and screenshot/state introspection.

### Ghostty Owns

- Terminal rendering and glyph/layout performance.
- Keyboard, mouse, selection, scroll and IME terminal semantics.
- Terminal cell/grid dimensions emitted through the external-I/O resize
  callback.
- Screen state processing for replay and live bytes.

Ghostty does **not** own attn workspace splits, agent sessions, daemon PTYs or
the New Workspace/Add Pane product flow.

## Protocol Boundary

The Swift client uses the canonical daemon WebSocket protocol defined in
`internal/protocol/schema/main.tsp`; it does not create a parallel native
protocol.

Connection flow:

```text
connect ws://localhost:<profile-port>/ws
send client_hello(client_kind: "swift-native", version: "protocol-66")
receive initial_state(protocol_version, sessions, workspaces, settings)
reject or clearly surface protocol mismatch
request workspace layouts as required
render sidebar and selected layout
```

Terminal attachment flow:

```text
visible terminal pane has runtime_id
create DaemonTerminalSurface(runtime_id)
send attach_session(runtime_id, attach_policy)
receive attach_result(screen_snapshot/replay_segments, last_seq, geometry)
feed all attach replay bytes through ghostty_surface_process_replay so
  historical terminal modes are restored without emitting new PTY input
receive pty_output(seq, bytes)
drop stale sequences and feed live bytes to ghostty_surface_process_output

Ghostty io_write(bytes)  -> send pty_input(runtime_id, bytes)
Ghostty io_resize(cols, rows, pixels) -> send pty_resize(runtime_id, cols, rows)
```

Workspace operations:

```text
New Workspace -> bootstrap_workspace(initial terminal or agent session)
Add terminal pane -> workspace_layout_split_pane(target pane, direction, cwd)
Add agent pane -> spawn_session(workspace, target pane, direction, cwd, agent)
Focus pane -> workspace_layout_focus_pane
Close pane -> workspace_layout_close_pane
```

`Cmd+Option+Arrow` navigation follows the rendered split geometry: it focuses
the nearest overlapping pane in that direction, then switches to the previous
workspace for left/up or the next workspace for right/down when the current
pane is at an edge. Workspace movement wraps in sidebar order, matching the
Tauri client behavior.

An empty retained workspace is a valid daemon lifecycle state: a session can
end without the workspace itself being closed. In that state the daemon emits
an empty layout rather than replaying stale pane rows; the Swift client renders
the no-pane background, lets `Cmd+W` remove the workspace, allows navigation
to cross it, and uses `bootstrap_workspace` to install a new first pane into
the existing workspace identity.

All actions that can fail use command/result handling. The Swift UI must not
optimistically create daemon-owned layouts or sessions.

## Swift Module Layout

The active codebase should be small and layered:

```text
native-ui/
  Package.swift                       # SwiftPM executable and unit-test targets
  Sources/AttnNative/
    App/
      AttnNativeApp.swift
      AppEnvironment.swift
    Protocol/
      ProtocolModels.swift            # generated or generated-adjacent Codable models
      ProtocolVersion.swift
    Daemon/
      DaemonConnection.swift          # WebSocket, reconnect, hello/version handshake
      DaemonCommands.swift            # typed command sending and pending results
      PTYStreamCoordinator.swift      # attach/replay/live sequencing per runtime
    Workspace/
      WorkspaceStore.swift            # main-actor received snapshots
      WorkspaceLayoutTree.swift       # decode/render daemon tree, no ownership mutation
    Terminal/
      GhosttyRuntime.swift            # one app-scoped ghostty_app_t
      DaemonTerminalSurface.swift     # configures forked SurfaceView for external I/O
      TerminalPaneView.swift          # SwiftUI bridge for pane/runtime identity
    Views/
      RootWindowView.swift
      WorkspaceSidebar.swift
      WorkspacePaneTreeView.swift
      Launcher/
        WorkspaceLauncherModel.swift
        NewWorkspaceSheet.swift
        AddPaneSheet.swift
    Automation/
      NativeAutomationServer.swift
      AutomationSnapshot.swift
  Tests/AttnNativeTests/
  Resources/
    Info.plist                        # stable development bundle identity
  Vendor/
    GhosttyKit.xcframework            # staged from the attn Ghostty fork by make
```

`swift build` and `swift test` are the active compiler/test interface.
`make build-native-app-dev` stages the local fork's XCFramework and packages
the Swift executable with a stable bundle identifier and codesigning
identity. That retains stable permission behavior without introducing an
Xcode project.

## Ghostty Integration

The existing Ghostty fork exposes the correct foundation in
`GhosttyKit.xcframework/macos-arm64/Headers/ghostty.h`:

- `GHOSTTY_SURFACE_IO_EXTERNAL`;
- `io_write` and `io_resize` callbacks;
- `ghostty_surface_process_output` and
  `ghostty_surface_process_replay`;
- native key, mouse, selection, IME and size APIs.

The first terminal slice now mounts `GhosttyTerminalView`, a thin AppKit host
over that C API, to prove the architecture using Ghostty's real parser and
Metal renderer rather than an interim terminal model. It forwards external
I/O to daemon PTYs, applies replay/live bytes, provides basic native
keyboard/mouse routing and exposes surface readback/geometry to automation.

The full production `Ghostty.SurfaceView` still lives inside Ghostty's app
source and is not a reusable Swift package boundary. A follow-up fork patch
should publish a reusable host retaining Ghostty's mature selection and IME
behavior while accepting attn external-I/O callbacks. That is a hardening
step for input parity, not a replacement for the renderer now in use.

App-level rules:

- Hold one forked `GhosttyRuntime` for the application and one surface per visible
  terminal pane.
- Retain the daemon runtime ID as the stable bridge key; never derive session
  ownership from view instances.
- Forward Ghostty callback bytes to the daemon; do not create a local shell or
  PTY.
- Preserve Ghostty's host-side mouse contract: pointer position must be
  reported on entry/movement and immediately before a button press. Ghostty
  uses that stored position both to anchor text selection and to encode mouse
  events for TUIs that enable DEC mouse reporting.
- Feed replay through the replay-specific fork API so historical terminal
  queries cannot create new live PTY input.
- Do not reduce a running TUI to a visible-frame snapshot when it may have
  emitted stateful startup control sequences. Claude Code enables DEC focus
  reporting before the native surface may mount; verified raw replay restores
  that mode so later pane focus changes reach Claude.
- Let daemon layout events create/destroy visible surface hosts; do not ask
  Ghostty to manage attn splits.

## Launcher Architecture

`NewWorkspaceSheet` and `AddPaneSheet` share
`WorkspaceLauncherModel`, matching the Tauri workflow:

- terminal or coding-agent choice;
- local target first, remote target added through the same model later;
- settings-backed agent/yolo defaults;
- recent locations and directory suggestions;
- inspect-path and repository/worktree destination stage;
- nested Escape cancellation behavior;
- pending command/result feedback.

The key difference from the GPUI attempt is composition, not product
behavior: these are ordinary SwiftUI sheets/overlays above live terminal
views.

## Automation Before Launchers

Automation is a prerequisite for launcher implementation, not a finishing
feature. Add the app-owned automation transport, process discovery, window
inspection and background-mode controls before building New Workspace or Add
Pane dialogs. Once one real daemon-backed Ghostty terminal pane renders and
accepts input, bind its input, text and geometry actions into that existing
bridge before proceeding to launcher UI.

The bridge must be usable while the application is not frontmost so an agent
can run terminal and layout checks without taking over the user's computer.
It should follow the daemon-backed approach already proven in the Tauri app:
invoke product actions through the same controller/command paths as the UI,
and expose typed state rather than depending on pixel inspection alone.

Initial automation API:

```text
get_state                  -> connection/profile, workspaces, selected pane
list_panes                 -> layout leaves, runtime IDs, pane kinds
focus_pane                 -> daemon/UI focus request
type_terminal              -> terminal input through the native surface path
wait_for_terminal_text     -> observable terminal evidence for smoke tests
get_surface_geometry       -> view bounds and Ghostty cell geometry
screenshot_window          -> captured app window for rendering evidence
set_window_background_mode -> launch/run without becoming active
park_window                -> retain rendering while showing only an edge strip
```

The first automation smoke test should open a daemon-backed shell pane, send
input, observe output, validate surface geometry and capture a screenshot
while the app stays non-frontmost. Terminal correctness must be testable
before launcher complexity is added. Routine harness scenarios park the app
window mostly outside the main screen through the app-owned bridge. Coverage
that must deliver real AppKit key events activates that parked window only for
the event burst, then returns focus to the previously frontmost application.
That HID coverage is interactive-only and requires a deliberate second
opt-in; unattended validation uses bridge-driven terminal input.

## First Vertical Slice

The first Swift implementation is deliberately narrow but must be real:

1. Launch a signed dev app with isolated daemon profile support.
2. Expose the tokenized non-frontmost automation bridge for app state,
   window inspection and eventual terminal controls.
3. Establish the WebSocket handshake and decode initial workspace/layout
   state.
4. Display a workspace sidebar and one selected workspace.
5. Embed one live Ghostty surface backed by a daemon PTY through external I/O.
6. Bind terminal automation actions and prove input/output/geometry without
   foreground activation.
7. Open a native test sheet over the actively updating terminal and prove the
   terminal remains visible and updates beneath it.
8. Display two daemon-owned split terminal panes with correct focus/input and
   resize callbacks.
9. Smoke-test terminal input, output, geometry and screenshots through an
   isolated daemon profile.

With this slice passing, the first local `New Workspace`/`Add Pane` launcher
is implemented above the live pane tree. Remote target parity and the
remaining picker refinements stay as follow-up work.

Extension point: the pane tree is keyed by daemon `WorkspaceLayoutPaneKind`;
Todo, Diff and Markdown panes plug into the same renderer without changing
terminal transport.

## Success Criteria

- No active GPUI client remains in `native-ui/`; the Rust experiment is
  explicitly archived.
- A Swift macOS app embeds Ghostty live surfaces without implementing terminal
  cell rendering.
- A sheet or overlay can be displayed above an active terminal while output
  continues visibly underneath.
- The client receives daemon-owned workspace layouts and never locally
  persists competing layouts.
- New Workspace creates the workspace plus first session atomically through
  `bootstrap_workspace`.
- Add Pane can create terminal or coding-agent panes in the requested
  direction.
- The app can be driven and inspected in an isolated dev profile without
  replacing the live production daemon.

## Shortcuts And Slicing

### Forbidden

- Do not return to GPUI for the active macOS client.
- Do not hide, pause, snapshot or release live terminal rendering to make
  native modal UI appear.
- Do not implement an interim terminal cell renderer.
- Do not grow the thin first-slice AppKit host into a parallel selection/IME
  implementation; publish and consume Ghostty's mature host for that parity.
- Do not let Swift own PTYs, workspace layout persistence or session truth.
- Do not skip protocol version checks or request/result error handling.
- Do not port launcher polish before a continuously rendered Ghostty surface
  beneath native presentation is verified.
- Do not import archived GPUI architecture as an active dependency.

### Intentional First Slice

The first slice is local-daemon, workspace/sidebar, terminal/split and
automation focused. Remote endpoint selection, Todo, Diff, Markdown, settings
parity and the complete launcher picker are deferred.

This slice is acceptable because it proves the irreducible risk: native
Ghostty rendering and native app composition against daemon-owned PTYs and
layouts. Deferred panes enter through pane-kind views; deferred remote targets
enter through the existing endpoint-aware command model.

## Cleanup Checklist

- Keep `native-gpui-archive/` read-only and clearly documented as reference
  for daemon/external-I/O mechanics only.
- Archive GPUI-only prototype plans and explainers rather than updating them.
- Remove the rejected Ghostty-hide workaround and any assertions that enshrine
  it.
- Replace Rust-native Makefile targets with Swift app targets once the first
  live Ghostty slice builds.
- Move native automation scenario ownership to the Swift app before porting
  New Workspace/Add Pane dialogs.
- Remove obsolete Rust dependencies and toolchain instructions from the
  active `native-ui/` documentation.

## Manual Verification

1. Start the isolated dev daemon and the signed Swift app bundle.
2. Open a workspace containing a running shell or agent terminal.
3. Type and observe live output in the Ghostty surface.
4. Present a native sheet over the terminal and confirm output remains
   visible and continues updating beneath it.
5. Dismiss the sheet and confirm input focus returns correctly.
6. Create vertical and horizontal terminal splits; confirm both surfaces
   resize and accept input.
7. Switch workspaces and return; confirm terminal release/reattach policy is
   deliberate and terminal content restores correctly.

## Automated Verification

- Codable protocol tests for handshake, workspace/layout and PTY events.
- Daemon connection tests for version mismatch and pending command results.
- PTY coordinator tests for replay-before-live sequencing and stale output
  handling.
- Swift UI tests that open/dismiss a sheet over a live terminal surface.
- Native automation smoke test using an isolated daemon profile:
  - attach one Ghostty terminal;
  - type through native input and observe daemon-backed output;
  - drag selectable fixture text after moving over a different row and verify
    selection anchors at the pressed cell;
  - drive a DEC mouse-reporting fixture and verify captured click/drag events
    are encoded to its PTY;
  - present an overlay while output continues;
  - create two layout-owned terminal panes and verify per-pane geometry;
  - capture a window screenshot as evidence of terminal-plus-overlay
    composition.
