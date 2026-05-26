# Native GPUI Workspace Client Prototype

## Why

Attn should have a native frontend that is inexpensive to keep open and fast
under continuous terminal output, without creating a second product model.
The earlier GPUI canvas experiment proved useful daemon-backed mechanics, but
its spatial panel UX and custom GPUI terminal painter are no longer the
product direction. Rendering terminal cells ourselves would prove nothing
useful for this rewrite.

The daemon and Tauri app now use workspace-owned split layouts as described in
`../../2026-05-21-tauri-workspace-layout-migration.md`. The native client must be a
second renderer of that same model: workspaces in the sidebar, a selected
workspace in the main surface, and every working tool hosted by layout panes.

Visual companion:
`../../../prototypes/archive/native-workspace-prototype/index.html`.

## Vision

`attn-native` is a focused GPUI desktop client:

- The left rail groups daemon workspaces and expands pane rows beneath each
  workspace, with status and active-pane indication.
- The main surface displays the selected workspace's full pane layout.
- Pane identity, selected focus, and color-coded session activity indicators
  live in the sidebar rows; rendered panes do not have title/header bars.
- Runtime-backed pane rows also expose the existing session `muted` toggle.
  Muting suppresses attention signalling for that session; it does not stop
  output, detach the pane, or replace its actual lifecycle/activity state.
- The row uses one compact trailing action slot: at rest it displays the
  activity indicator; on hover or keyboard focus it expands and animates into
  mute/unmute plus close controls. Non-runtime panes expose close only. Do
  not render these controls permanently.
- Shell, Claude, Codex, Todo, and later tools are peer panes in that layout.
- `Todo` is a non-terminal workspace pane, not a tab or a separate navigation
  system. Its pane geometry and focus belong to `WorkspaceLayout`; its item
  data begins with daemon-provided session todo messages; a separate
  workspace-wide editable Todo model is added only if required.
- Do not reserve a workspace header bar above the pane surface or a persistent
  combined `+ Workspace / Session` sidebar button. Creation and navigation
  affordances must remain unobtrusive; their exact trigger is a later UI
  decision.
- `New Workspace` opens a native directory/worktree and initial-pane picker.
- Creating submits `register_workspace`, then `spawn_session` with the new
  `workspace_id`; no local placeholder row is canonical.
- `Add Pane` / `New Session` reuses the picker inside an existing workspace
  and asks the daemon to add its new session to that workspace layout.
- `Cmd+Arrow` navigates adjacent panes; `Cmd+number` selects a workspace, and
  an arrow action at a layout edge may move to the neighbouring workspace.

It is a dumb client by design. The Go daemon owns lifecycle, persistence,
layout, PTYs, replay, and protocol results. GPUI owns native application
layout and non-terminal panes. Full Ghostty surfaces own terminal emulation
and Metal rendering inside terminal pane bounds.

## Component Toolkit Decision

Use `gpui-component` for conventional application surfaces in the new native
client. The rewrite needs more than terminal pixels: session creation,
settings, Todo content, Markdown, and diff/review workflows all need controls
and rich text presentation. Rebuilding those primitives in raw GPUI would add
work without strengthening Attn's core architecture.

Adopt it for:

- application `Root`, theme plumbing, icons, buttons, tooltips, and modal
  affordances;
- the new-session/location dialog and settings forms;
- Todo pane controls and long-list rendering where virtualization is useful;
- Markdown rendering for agent/review/document content;
- diff/review pane chrome, file lists, comment inputs, and syntax-highlighted
  code presentation where its editor/text components fit.

Do not delegate Attn ownership boundaries to it:

- `WorkspaceLayout`, pane identity, and pane close/split persistence remain
  daemon-owned protocol state;
- the custom sidebar interaction, including activity/action tray behavior, is
  designed for Attn and may use low-level GPUI elements rather than a generic
  sidebar component;
- Ghostty remains the terminal renderer; do not use an editor component as a
  terminal substitute;
- a diff pane remains an Attn surface backed by existing daemon diff/review
  messages. `gpui-component` can supply controls, Markdown, virtualization,
  and highlighted text, but no dedicated diff-viewer dependency is assumed;
- if `Resizable` is used to implement drag handles, its local state is only
  transient interaction state; resulting layout changes must go through
  daemon commands and confirmed `WorkspaceLayout` updates.

This choice introduces `gpui-component::init(cx)` and its top-level `Root`
requirement into the active app skeleton. Capture binary size, idle memory,
and startup cost once the skeleton launches; keep the dependency only if its
surface coverage justifies measured cost.

## Build Toolchain And Dependency Baseline

The prototype must compile the real Ghostty terminal; a parser-disabled or
terminal-stub feature is not an acceptable development configuration.

Renderer foundation decision:

- Terminal panes embed the full Ghostty Metal surface through its native
  `NSView`; they are not repainted cell by cell in GPUI.
- Attn maintains an upstream-based Ghostty fork at `victorarias/ghostty`,
  branch `attn/external-io`, initially pinned at commit `ba8e595f7`.
- The fork exposes `GHOSTTY_SURFACE_IO_EXTERNAL`,
  `ghostty_surface_process_output`, `ghostty_surface_process_replay`,
  input-write callbacks, and resize callbacks. Replay processing suppresses
  terminal-query replies so historical bytes cannot write into the current
  live PTY. The daemon remains the only PTY owner.
- The prior `libghostty-vt`/`TerminalModel`/`TerminalView` painter has been
  removed from the active crate. Do not reintroduce it; archived code is
  reference material for daemon mechanics only.

Verified macOS build constraint:

- The full Ghostty surface build requires Zig `0.15.x`.
- This machine's default Command Line Tools SDK is `MacOSX26.4.sdk`.
- Ghostty documents that Zig `0.15.x` cannot link against Xcode/macOS SDK
  `26.4` without its patched toolchain. A standalone Zig `0.15.2` link fails
  here with missing `libSystem` symbols, while Zig `0.16.0` links but cannot
  compile current Ghostty sources due to deliberate Zig-version/API checks.
- Ghostty's supported fixes are Homebrew `zig@0.15` or its Nix toolchain,
  both patched for the SDK 26.4 issue, or using Xcode SDK `26.3`.

Recommended local toolchain:

1. Install Homebrew `zig@0.15` and invoke native builds with its `bin`
   directory before asdf shims on `PATH`.
2. Install the Xcode Metal Toolchain component for full Ghostty surface
   builds: `xcodebuild -downloadComponent MetalToolchain`.
3. Keep GPUI's supported `runtime_shaders` feature for GPUI itself; it does
   not build Ghostty's Metal rendering library.
4. Do not commit an asdf Zig `0.15.2` pin for this client while SDK 26.4 is
   selected; that selects the known-broken unpatched compiler.
5. Once Ghostty itself supports Zig `0.16` on the chosen source revision,
   reconsider the patched Zig `0.15` constraint and remove it.
6. On macOS, use a stable `Apple Development` signing identity for source/dev
   installs when one is installed. The Makefile deterministically selects one
   certificate fingerprint and permits `MACOS_CODESIGN_IDENTITY` override;
   machines without an identity retain ad-hoc signing, but cannot expect
   Screen Recording authorization to survive binary rebuilds.

Dependency audit performed against current crate metadata and the owned
Ghostty fork:

| Dependency | Current prototype selection | Current available | Decision |
| --- | --- | --- | --- |
| `gpui-component` | `0.5.1` | `0.5.1` | Keep; current crate release |
| `gpui` | `0.2.2` | `0.2.2` | Keep with `runtime_shaders`; current crate release |
| `async-channel` | `2.5.0` | `2.5.0` | Keep |
| `smol` | `2.0.2` | `2.0.2` | Keep |
| `base64` | `0.22.1` | `0.22.1` | Keep |
| `futures-util` | `0.3.32` | `0.3.32` | Keep |
| `serde` | `1.0.228` | `1.0.228` | Keep |
| `serde_json` | `1.0.150` | `1.0.150` | Refreshed |
| `async-tungstenite` | `0.34.1` using `smol-runtime` | `0.34.1` | Updated; avoid deprecated `async-std` connector |
| Ghostty surface renderer | `victorarias/ghostty` `ba8e595f7` | upstream-based fork | Use; attn owns external daemon PTY API and replay suppression |
| `libghostty-vt` | removed from active crate | archived canvas only | Do not reintroduce the GPUI painter |

`getrandom 0.2` remains in the active automation manifest for discovery-token
generation and in excluded archived-canvas reference files; changing RNG
versions is unrelated to the renderer foundation.

## What Is Archived And What Is Reused

The old code is retained at:

- `native-ui/crates/attn-native-canvas-archive/`
- `native-ui/crates/attn-native-canvas-protocol-archive/`
- `docs/plans/archive/native-canvas/`

The archived crates are excluded from the active Cargo workspace. They remain
readable reference material, but must not constrain dependency resolution or
compile verification for `attn-native`.

Reusable mechanics from that work:

- its `adapters` / `state` / `views` / `domain` separation;
- reconnecting websocket adapter and direct daemon command path;
- attach, replay-sequence, desync reattach, resize, and PTY input mechanics;
- the directory/worktree chooser in `views/location_dialog.rs`;
- runtime-gated automation concepts and Observatory theme vocabulary.

### Required Archive Port Audit

The archive is not only visual or structural reference material. Before
building new native features, port or intentionally replace the following
working mechanics:

| Archived mechanic | Workspace-first rule |
| --- | --- |
| `TerminalModel` applies attach snapshots/replay and drops duplicate output by sequence. | Port directly for each visible runtime; do not invent a second terminal lifecycle. |
| `TerminalModel` and `TerminalView` paint parsed cells in GPUI. | Do not port this renderer. Replace it with a full Ghostty surface hosted in the pane. |
| The terminal renderer receives its content bounds before issuing `pty_resize`. | Measure the bounds of each pane element; a split pane must not report full-window PTY dimensions. |
| Panel creation sends `attach_session` immediately and records `terminal_attach_processed`. | A visible pane must attach when its runtime view is created, not only after a later paint pass; automation must distinguish attach receipt from model processing. |
| A websocket reconnect reattaches existing terminal views. | Reattach only terminal views belonging to the selected workspace's visible layout; inactive workspaces remain detached under `release-on-switch`. |
| `send_pty_input` transmits raw text bytes unchanged. | Never trim or normalize PTY data; control bytes such as Enter are part of the contract. |
| `type_into_panel` uses focus plus the real `TerminalView` key-encoding path. | Provide a workspace-pane equivalent before treating automation as user-input coverage; direct PTY writes prove transport only. |
| Pending selection/spawn tracking waits for daemon broadcasts/results before presenting canonical state. | Port the request/result discipline for create/add-pane and failure UI; do not let a successful queue send stand in for daemon confirmation. |
| Structured automation events expose attach, panel creation/removal, selection, and failures. | Expose the pane/layout equivalents, bounded and queryable by agents. |

The archive's canvas coordinator must not be carried over unchanged. It
materialized terminal panels across workspaces; the new runtime owner
materializes and attaches only panes in the selected workspace layout, because
that is the low-memory boundary being evaluated.

Rejected model:

- canvas viewport and panel geometry;
- the archived protocol v64 crate, which still models `WorkspacePanel` and
  cannot speak protocol v65;
- allocating/rendering every workspace terminal in a spatial scene.

Preserved capability:

- first-class shell sessions remain a required near-term direction;
- the archived `shell_as_session` implementation is reference material for a
  protocol-v65 equivalent, not something to erase with the canvas model;
- user-created shell sessions and split-created utility shell panes must both
  belong to `WorkspaceLayout` and appear as nested pane rows without becoming
  top-level workspace entries.

## Target Architecture

```text
                 protocol v65 websocket
  +-----------+  commands / broadcasts  +-----------------------+
  | GPUI app  | <----------------------> | Go daemon             |
  | native UI |                           | canonical ownership   |
  +-----+-----+                           +-----------+-----------+
        |                                             |
        | renders snapshots                           | persists / owns
        v                                             v
  Workspace rail + selected                    Workspace + WorkspaceLayout
  layout view + visible panes                  Sessions + shell PTYs + todos
        |
        v
  Ghostty Metal surfaces for attached visible panes only
```

### Crate Layout

```text
native-ui/crates/
  attn-protocol/               new protocol-v65 native subset
  attn-native-app/
    adapters/daemon.rs         websocket, request/results, reconnect
    state/workspaces.rs        daemon workspace snapshots + selection
    state/pane_runtimes.rs     Ghostty surface lifetimes for visible panes
    adapters/ghostty.rs        FFI, external IO callbacks, replay gating
    views/sidebar.rs           custom workspace groups + pane focus rows
    views/location_dialog.rs   gpui-component create workspace / add pane flow
    views/settings_view.rs     gpui-component settings forms
    views/workspace_view.rs    selected workspace layout surface
    views/terminal_view.rs     AppKit surface host and pane focus wiring
    views/todo_view.rs         gpui-component non-terminal Todo pane renderer
    views/diff_view.rs         Attn diff model + component display primitives
    views/markdown_view.rs     Markdown-rich non-terminal content
    app.rs                     wiring only
  attn-native-canvas-archive/ historical reference
```

### Ownership Boundaries

| Concern | Owner |
| --- | --- |
| Workspaces, status rollups, membership | daemon |
| Layout tree, panes, active pane, shell runtime creation | daemon |
| Session process and PTY stream/replay | daemon |
| Session `muted` state and attention exclusion | daemon, existing `mute` command |
| Todo pane placement and todo item source | daemon (`Session.todos` / todo events first) |
| Selected workspace, selected pane, and current keyboard focus | native UI ephemeral state |
| Pending action indicator/error | native UI until daemon result/event |
| Ghostty surface lifetimes for visible attached panes | native UI |

### Runtime Invariants

- A terminal pane entity is created only for a daemon-confirmed visible
  runtime in the selected workspace layout.
- Creating that entity immediately submits `attach_session`; a render pass is
  not a lifecycle trigger.
- Switching away detaches and drops the former pane entities under the
  default retention experiment. Reconnecting reattaches only entities still
  owned by the selected layout.
- PTY geometry has one interactive owner at a time. Automated scenarios own
  an isolated daemon profile plus disposable workspace; `start-empty` remains
  an additional guardrail for any second manual/dev window.
- Terminal presentation is rendered by the embedded Ghostty Metal surface;
  GPUI never paints parsed terminal cells. Text extraction is readback only.
- Attach replay is fed into Ghostty with outgoing external-I/O writes gated
  off, so historical terminal queries cannot generate new daemon PTY input.
- Each terminal pane derives `pty_resize` from its own rendered element
  bounds. The containing window size is not a valid substitute once a
  workspace displays splits.
- Raw PTY automation and UI-input automation are separate tests: raw input
  checks daemon transport; synthesized keystrokes through a focused pane
  check the actual native keyboard path.
- Any create, focus, close, mute, attach, or replay failure must be observable
  through result state or structured automation events.

## Prototype Slice

The first runnable slice is deliberately complete within a narrow scope:

1. Establish a protocol-v65 Rust subset for initial state, workspace
   lifecycle, session spawn, PTY attach/input/resize/output, and the embedded
   `WorkspaceLayout` snapshot.
2. Open a GPUI window with workspace groups and nested pane rows that focus
   panes from daemon `WorkspaceLayout` snapshots.
3. Read `initial_state.workspaces`; select a workspace and render every
   visible pane from its daemon-owned layout tree.
4. Create one full Ghostty surface per visible terminal pane in the active
   workspace. Feed daemon output through `ghostty_surface_process_output`,
   feed Ghostty input callbacks into PTY input, gate replies during replay,
   and forward surface-derived terminal geometry to PTY resize.
5. Reuse behavior learned from the archived `LocationDialog`, implementing
   `New Workspace` and `Add Pane` with `gpui-component` dialog/form controls:
   new workspace performs register then initial spawn; add pane spawns into
   the existing `workspace_id`.
6. Implement pane focus/navigation in the rendered terminal tree, including
   `Cmd+Arrow` adjacency and direct workspace selection by `Cmd+number`.
7. Switch workspaces by detaching and dropping the old workspace's terminal
   model set before attaching all visible panes for the newly selected
   workspace.

The slice is successful when a user can launch the native app against the dev
daemon, create a workspace, add or display several peer panes within it,
navigate among shell/Claude/Codex terminals, switch between workspaces, and
observe bounded memory growth from only the selected workspace's visible pane
set. `Todo` follows as the first non-terminal pane once this foundation works.

## Extension Path

The slice renders the full layout tree because multi-pane navigation is the
product interaction being proved. It may initially defer pane-management
affordances that mutate the tree.

Next slices:

- send `workspace_layout_split_pane`, `close_pane`, and `rename_pane`
  commands from native controls;
- implement the edge-navigation workspace transition if the direct workspace
  shortcuts prove sufficient for the first pass;
- define and persist a `Todo` pane kind, render existing daemon todo data in
  it, and only extend item persistence after settling workspace aggregation;
- expose first-class shell-session creation once its v65 capability semantics
  are wired.

Later slices can add Markdown-rich agent content, settings, diff/review
panes, pull-request attention, and broader automation scenarios without
changing workspace ownership.

## Automation Contract

Automation is part of the prototype foundation, because every later pane
type must be diagnosable without relying on manual visual inspection. Reuse
the proven transport from the archived native client and keep its wire
compatibility with the Tauri harness:

- When `ATTN_PROFILE=dev` is set, or `ATTN_AUTOMATION=1` explicitly enables
  it, the native app starts a token-authenticated localhost TCP sidecar.
- The automated native smoke runner starts a uniquely profiled daemon and
  GPUI app together. It must not connect to the persistent `dev` daemon or
  any pane visible in a user's running application.
- A manually launched second automation-controlled window must additionally
  set `ATTN_AUTOMATION_START_EMPTY=1`; it starts without attaching an existing
  workspace terminal.
- The app publishes a profile-namespaced manifest at
  `~/Library/Application Support/com.attn.native.<profile>/debug/ui-automation.json`.
- `get_state` exposes daemon connection status, selected workspace,
  daemon-owned workspaces/layouts/sessions, and visible terminal runtimes.
- `tail_events` exposes a bounded structured event stream for connection,
  daemon-event, selection, focus, mute, and close diagnosis.
- Initial fixture actions are `create_workspace`, `spawn_session`,
  `destroy_workspace`, `select_workspace`, `focus_pane`, `mute_session`,
  `close_pane`, `split_pane`, and `kill_runtime`.
  Creation actions accept an executable override so agent validation can use
  a disposable `pi` pane backed by `/bin/sh` rather than launch an AI client
  or type into a user's existing pane; state still appears only after daemon
  confirmation.
- Reuse the Tauri pane action names for shared scenario intent:
  `write_pane`, `type_pane_via_ui`, `read_pane_text`,
  `capture_structured_snapshot`, and `capture_render_health`. Native payloads
  identify daemon-owned workspace panes. Do not add native-only aliases for
  these shared actions.
- `focus_pane` establishes the pane's input focus and
  `type_pane_via_ui` injects keystrokes through `TerminalView` and
  `ghostty_surface_key`; typing fails when the pane has not been focused.
  Use `write_pane` only to prove raw transport/control-byte delivery, not to
  claim keyboard interaction works.
- Include `terminal_attach_processed` (or a renamed pane-equivalent) in
  `tail_events`, including success, snapshot/replay presence, dimensions, and
  last sequence, so agents can tell whether the UI consumed an attach result.
- Append automation request outcomes to
  `debug/ui-automation-server.log` beside the manifest, matching the
  diagnostic surface available in Tauri.

Do not carry forward canvas actions such as zoom or freeform panel movement;
they assert the discarded ownership model.

Screenshot handling differs from Tauri. Tauri can request a PNG from its web
renderer; GPUI does not currently expose an equivalent capture API. The
native automation action invokes macOS `screencapture` from the signed
`attn-native-dev.app` bundle for the exact native window id discovered by the
harness. It requires Screen Recording permission for the native bundle,
rather than whichever external agent process issued the automation request,
and remains trustworthy when another window overlaps it. This is sufficient
for visible-window visual iteration now.
A future occlusion-safe ScreenCaptureKit driver should replace it before
screenshots become a CI assertion surface.
`make dev-native` packages the renderer as
`target/debug/attn-native-dev.app`, with stable bundle identifier
`com.attn.native.dev`, and signs it with the stable development identity
before launching it. The dev bundle compiles in `profile=dev` and the dev
daemon URL so a macOS permission relaunch cannot silently reconnect it to the
production daemon; runtime environment values still override these defaults
for isolated automation runs. Re-signing the renderer ad hoc or launching its
raw binary creates an unsuitable privacy identity and prompts again after
rebuilds.

## Shell Session Compatibility

The workspace migration and the shell-session capability solve separate
problems:

- A `WorkspaceLayoutPaneKind.Shell` pane created by split is a utility PTY in
  the workspace layout.
- A first-class shell session is a user-created/runtime session that belongs
  to a workspace and is represented by a pane in that workspace's layout.
- Neither one changes the top-level workspace model: the sidebar still
  groups workspaces; session and shell panes appear beneath the owning
  workspace and in its pane layout.

Protocol v65 already retains shell agent and shell-pane vocabulary, but the
archived native client's `shell_as_session` handshake behavior should be
reviewed and reintroduced or replaced explicitly before the shell-session UX
ships. The new native protocol crate must not omit shell types just because
the first prototype slice creates only Claude or Codex workspaces.

## Navigation And Focus

- Selecting a workspace displays its pane layout and restores or requests its
  active pane.
- Each nested pane row identifies a pane in the daemon `WorkspaceLayout`.
- For runtime-backed panes, the row also presents color-coded daemon session
  activity state; non-session panes such as Todo do not invent runtime state.
- Runtime-backed rows expose a mute toggle next to activity. Muted sessions
  continue to show their underlying state and remain fully interactive. The
  activity dot occupies a compact trailing slot until hover/focus expands an
  animated action tray containing mute/unmute and close. Unlike the
  session-first sidebar, a muted pane remains nested under its owning
  workspace so muting does not implicitly alter `WorkspaceLayout`; style it
  as parked and remove it from attention navigation/rollups.
- Close is a pane action: it requests the appropriate daemon-confirmed
  session/pane closure and must not optimistically delete a row from layout.
- Selecting a nested pane row focuses the corresponding rendered pane and
  sends `workspace_layout_focus_pane`; daemon updates remain canonical.
- `Cmd+Arrow` moves focus geometrically among panes in the displayed terminal
  or non-terminal layout.
- `Cmd+number` selects a workspace directly.
- Moving beyond a pane edge into a neighbouring workspace is an intended
  interaction, but may be deferred until direct workspace selection and
  intra-workspace arrows are stable.

## Todo Pane Decision

`Todo` is a pane in the workspace layout for this direction. This keeps one
interaction model: switch workspace, then focus or arrange panes. It allows a
task list to stay visible next to the shell or agent performing the work.

This simplifies UI navigation, but it is not free in the architecture:

- `WorkspaceLayout` must eventually support a non-terminal Todo pane kind;
- the first Todo pane should consume existing daemon `Session.todos` and
  `session_todos_updated` data instead of inventing native-local task state;
- whether the pane displays one focused session's todos or aggregates todos
  across workspace sessions must be decided before implementation;
- a new workspace-wide editable Todo store/protocol is needed only if the
  session-derived list is insufficient;
- the native client must render Todo without routing it through Ghostty or PTY
  attachment logic.

Do not implement workspace surface tabs for now. A tabbed mode can be
reconsidered later, after the pane-based native rewrite exists and actual use
shows a reason for a second navigation layer.

## Low-Memory Strategy

- Keep daemon snapshot metadata for all workspaces; it is small and needed for
  the rail.
- Keep Ghostty surfaces only for panes visible in the selected workspace.
- In the first slice, that is one live terminal surface for each visible pane
  in the selected workspace.
- A Todo pane is not a terminal surface: retain only the daemon-provided item
  snapshot and lightweight native view state needed to paint it.
- Route PTY bytes directly into each pane's Ghostty surface and allow only
  that surface to redraw;
  never append terminal output to root workspace state.
- On workspace switch, detach and release the prior workspace's terminal
  surface set rather than caching terminal scrollback in the native process.
- Let daemon replay reconstruct surfaces when a workspace is reattached.
- Measure resident memory, idle CPU, terminal-output redraw latency, and
  workspace-switch attach latency before adding model retention.

### Terminal Retention Experiment

Releasing an inactive workspace's terminal surfaces is a prototype hypothesis, not an
unconditional product decision.

Expected benefit:

- memory remains proportional to currently displayed terminal panes rather
  than every previously visited workspace;
- inactive terminal output cannot trigger hidden GPUI redraw work.

Expected cost:

- switching back requires attach/replay and pane-set reconstruction;
- very long replay histories or slow remote links may make switching feel
  materially slower than Tauri;
- dropping local viewport/selection state may feel abrupt unless explicitly
  restored or accepted as prototype scope.

Instrument the first slice with two comparison modes:

| Mode | Behavior | Purpose |
| --- | --- | --- |
| `release-on-switch` | Detach and drop all prior workspace Ghostty surfaces immediately. | Establish the lowest-memory baseline. |
| `retain-selected-workspace` | Keep one inactive previously selected workspace's visible surfaces warm until the next switch evicts them. | Establish the responsiveness cost of the memory-first choice. |

Record for both modes with 1, 5, and 10 workspaces visited, and with 1, 3,
and 6 visible panes in the selected workspace:

- resident memory after idle stabilization;
- workspace switch-to-visible-output latency;
- reattach/replay bytes and time;
- idle CPU while non-selected sessions continue producing output.

Start the prototype with `release-on-switch` as the default. If measurements
show that retaining one previous workspace's pane set materially improves
switch latency for a modest, measured memory cost, adopt that bounded cache
deliberately. Do not grow retention proportional to workspace count.

## Creation Flow

```text
User invokes New Workspace
  -> component-backed dialog chooses directory/worktree and initial session type
  -> native sends register_workspace(id, title, directory)
  <- daemon broadcasts workspace_registered
  -> native sends spawn_session(id, workspace_id, agent, cwd)
  <- daemon broadcasts session_registered / workspace_state_changed
  <- daemon snapshot includes WorkspaceLayout with agent pane
  -> native attaches pane session PTY and renders terminal

User invokes Add Pane in workspace
  -> component-backed dialog chooses session type for existing workspace
  -> native sends spawn_session(id, selected_workspace_id, agent, cwd)
  <- daemon updates WorkspaceLayout with another peer pane
  -> native attaches and renders every visible pane in the updated layout
```

The native UI may show a pending spinner or error message, but it must not
create an optimistic canonical workspace or layout.

## Shortcuts And Slicing

### Forbidden Shortcuts

- Renaming canvas panels to panes while keeping panel geometry or viewport
  logic underneath.
- Copying the archived protocol crate and changing only its version string.
- Copying archived GPUI dialog widgets wholesale when `gpui-component`
  provides the active form/modal primitives; preserve behavior and protocol
  flow rather than keeping discarded presentation code alive.
- Reusing the canvas-era `shell_as_session` behavior without specifying its
  protocol-v65 workspace-layout semantics.
- Removing shell-session protocol support merely because the first slice does
  not expose its create button yet.
- Treating pane/session rows as top-level workspaces or creating one
  native-local layout per session.
- Adding workspace surface tabs while the pane-based layout is the chosen
  interaction model.
- Adding a permanent workspace header/shortcut bar or prominent combined
  workspace/session creation button to the minimal pane surface.
- Adding title/header bars inside each pane when the sidebar already carries
  its identity, focus, and runtime activity.
- Faking Todo as native-local checklist data or as a terminal pane; it must
  become a daemon-backed non-terminal pane when implemented.
- Applying layout changes optimistically before daemon broadcasts the updated
  `WorkspaceLayout`.
- Sending PTY automation through helpers that trim, normalize, or otherwise
  alter raw control bytes.
- Declaring terminal automation complete with only direct `write_pane`;
  the archive already established a real focused-keyboard action.
- Rendering a Ghostty terminal as a plain extracted text node; extraction is
  for automation readback, not terminal presentation.
- Rendering terminal cells in GPUI instead of embedding Ghostty's native
  Metal surface.
- Computing split terminal PTY geometry from the full app window instead of
  the rendered pane bounds.
- Attaching a secondary automation window to a pre-existing runtime or
  allowing it to issue PTY resize traffic for a user's visible pane.
- Creating terminal views only from render-time side effects instead of from
  daemon-confirmed visible pane lifecycle.
- Omitting reconnect reattach or attach-processing instrumentation already
  proven necessary in the archived client.
- Removing a pane immediately from the sidebar when close is pressed instead
  of waiting for the daemon-confirmed layout/session update.
- Treating mute as pause, detach, hidden terminal output, or a locally stored
  preference that other clients cannot observe.
- Keeping terminal surfaces alive for inactive workspaces without measurements.
- Adding a build feature that bypasses Ghostty surface rendering to conceal a
  native toolchain failure.
- Restarting or overwriting the live installed app while iterating; use the
  isolated dev daemon/profile.

### Intentional First Slice

The first runnable prototype renders a selected workspace's existing terminal
pane tree, but does not need every pane-management command or the Todo pane on
day one. This is intentional because terminal panes prove protocol-v65
creation, multiple PTY attachments, Ghostty rendering, focus navigation, and
memory lifetime discipline before introducing the first non-terminal pane.

The clean extension point is daemon ownership: tree mutations are added as
commands over `WorkspaceLayout`, not as local view-model edits. Todo is then
added as a daemon-owned pane kind that initially renders existing daemon todo
events without changing navigation structure.

## Feature Build Order

Build and verify one slice at a time:

1. **Archive and skeleton**: preserve canvas reference code under archive
   names, create fresh active crates, initialize `gpui-component`, and mount
   its required root/theme boundary.
2. **Protocol v65 connection**: handshake, reconnect, initial workspace
   snapshots, request/result errors, and workspace layout decoding.
3. **Workspace rail and layout reader**: render workspace groups and nested
   pane rows from daemon state; show session activity colors and focus panes
   without PTYs yet, including the existing `Session.muted` indicator and
   `mute` command.
4. **Terminal panes**: host real Ghostty Metal surfaces for visible
   shell/agent panes, while porting only create-time attach,
   replay/sequence/reply-gating, external input/resize callbacks,
   reconnect/desync reattach, and attach-processing instrumentation.
5. **Workspace and pane creation**: rebuild the location dialog with
   `gpui-component` form/modal primitives for New Workspace and Add Pane
   through register/spawn/daemon confirmation, without requiring a permanent
   header bar or sidebar create button.
6. **Keyboard and memory behavior**: `Cmd+Arrow`, `Cmd+number`, and measured
   release-on-switch versus one bounded warm workspace cache.
7. **Todo pane**: render a `gpui-component`-backed non-terminal Todo pane
   beside live terminals using existing daemon todo events first; settle the
   workspace aggregation rule before adding new persisted data.
8. **First-class shell session**: restore the near-term `shell_as_session`
   capability using protocol-v65 workspace semantics.
9. **Settings and Markdown**: build settings forms and Markdown-rich content
   using shared native components.
10. **Diff and review pane**: consume existing daemon file/branch diff and
   review messages, using component controls and syntax-highlighted text
   without assuming a ready-made diff widget.
11. **Layout controls and parity**: split, close, rename, remaining Tauri UX,
   automation, and performance polish.

## Feature Inventory And Existing Backend Hooks

| Native feature | Initial implementation surface | Existing daemon/protocol hook | Toolkit role |
| --- | --- | --- | --- |
| Session/workspace creation | Native modal flow | `register_workspace`, `spawn_session`, picker/worktree operations | Modal, form inputs, selects, validation |
| Settings | Modal or pane, chosen during parity work | `get_settings`, `settings_updated` | Settings/form controls |
| Todo pane | Non-terminal pane beside sessions | `Session.todos`, `todos`, `session_todos_updated` | Lists, checkboxes, empty states |
| Markdown rendering | Embedded content in suitable panes | Content carried by existing/session/review responses as applicable | Native Markdown renderer |
| Diff/review pane | Non-terminal pane | `get_file_diff`, `get_branch_diff_files`, review-loop/comment events | File list, virtual list, actions, highlighted text |
| Terminal pane | Shell/agent terminal pane | PTY attach/input/resize/output/replay | None; use Ghostty |
| Sidebar actions | Custom pane row UI | `mute`, close/layout commands | Low-level elements only; custom behavior |
| Native automation | Runtime-gated TCP sidecar and harness CLI | Reads native snapshot; submits existing daemon commands | macOS capture for screenshots |

Do not infer from `gpui-component`'s editor and Markdown support that it
provides the product-level diff/review workflow. The existing daemon messages
and Attn review state define that surface.

## Success Criteria

- The archived canvas app and its protocol subset no longer occupy active
  crate names or active plan locations.
- A new native protocol crate targets protocol v65 workspace and layout
  messages without `WorkspacePanel`, while retaining the shell session/pane
  types needed for a v65-compatible first-class shell capability.
- Sidebar workspace groups and nested pane rows render from daemon workspace
  and layout snapshots without duplicating ownership or terminal state.
- Runtime-backed rows can toggle persisted `Session.muted`; a muted pane is
  visibly parked in its workspace while retaining its true session state and
  no longer contributing attention.
- Creating a workspace uses the daemon register-then-spawn flow and displays
  its first live agent terminal.
- The selected workspace renders every visible shell/agent pane in its layout
  and supports keyboard pane focus navigation.
- Todo is implemented as a daemon-backed non-terminal pane without introducing
  a separate tab navigation system or native-local source of truth.
- Workspace selection changes the attached/displayed pane set without
  retaining inactive workspace Ghostty surfaces by default, with
  comparative data captured for a one-workspace warm cache.
- Terminal state consumes daemon output independently of GPUI repaint
  scheduling; rendering redraws only its embedded Ghostty surface.
- The code layout preserves reusable archive learnings without copying canvas
  ownership assumptions.
- `gpui-component` supplies conventional controls and rich-content surfaces
  without owning terminal rendering or canonical workspace layout.
- Dev launches publish an authenticated native automation manifest and expose
  state/readback/control actions without reintroducing canvas semantics.
- An isolated automation-owned daemon/app run can create, attach, type into,
  observe, and remove a disposable pane without attaching or resizing any
  existing user runtime.
- Terminal lifecycle verification covers both raw PTY delivery and the real
  focus/key-encoding input path inherited from the archived native client.

## Cleanup Checklist

- Remove active references to the old `attn-native-app` canvas binary.
- Keep canvas code, v64 Rust protocol subset, plans, and visual plate under
  clearly named archive paths.
- Exclude the canvas crates from the active Cargo workspace so obsolete native
  dependency pins cannot block prototype dependency updates.
- Give the new client a new `attn-protocol` crate generated or modelled from
  v65 schema.
- Do not port `trackpad_zoom`, canvas placement/snapping, geometry commands,
  or canvas automation actions.
- Adapt only reusable location-dialog, terminal lifecycle, adapter, theme,
  and automation concepts into the new crate; do not adapt terminal painting.
- Port archived terminal lifecycle and automation-input characterization tests
  before adding more panel types or dialog surface work.
- Replace archived dialog presentation with `gpui-component` forms/modals
  while retaining the daemon-confirmed creation behavior.
- Keep `make dev-native` as the one-command launcher for the workspace-first
  prototype; it selects the patched Zig toolchain and routes to the isolated
  dev daemon.

## Automated Verification

Archive/preparation change:

```bash
cargo metadata --manifest-path native-ui/Cargo.toml --no-deps --format-version 1
cargo fmt --manifest-path native-ui/Cargo.toml --all --check
```

Prototype implementation change:

```bash
brew install zig@0.15 # Ghostty-patched Zig for macOS SDK 26.4
xcodebuild -downloadComponent MetalToolchain # once per Xcode installation
make build-native-ghostty-kit
cd native-ui
export PATH="$(brew --prefix zig@0.15)/bin:$PATH"
cargo test -p attn-native-app --no-run
cargo test -p attn-protocol
cargo fmt --all --check
pnpm --dir ../app test
```

Native automation smoke while `make dev-native` is running:

```bash
pnpm --dir app run native:bridge-cli -- ping
pnpm --dir app run native:bridge-cli -- get_state
pnpm --dir app run native:bridge-cli -- screenshot '{"path":"/tmp/attn-native.png"}'
```

Agent-owned end-to-end smoke, using its own daemon profile and native window:

```bash
make test-native-agent
```

The smoke runner deletes its temporary native app-data and daemon profile
directories on exit. Set `ATTN_NATIVE_KEEP_ARTIFACTS=1` only when retaining a
failed run's manifest/log/database is needed for diagnosis.
It launches with `ATTN_AUTOMATION_BACKGROUND=1`: after the first AppKit
Ghostty surface is mounted, the runner restores the previously active
application. It then exercises output/readback and bridge-driven
`ghostty_surface_key` input on that mounted surface, and sampled foreground
ownership fails the run if the native process becomes frontmost again.
This run does not claim cold surface creation while backgrounded:
release-on-switch deliberately drops the terminal surface, and a newly
created AppKit Ghostty surface currently needs a foreground bootstrap.
The input marker, including its Return key, is submitted entirely through
Ghostty; mixing Ghostty-queued key events with direct daemon PTY writes would
make command ordering nondeterministic.

Run lifecycle reconstruction in foreground, and optionally add physical
macOS mouse/key delivery:

```bash
ATTN_NATIVE_FOREGROUND=1 make test-native-agent
ATTN_NATIVE_FOREGROUND=1 ATTN_NATIVE_PHYSICAL_INPUT=1 make test-native-agent
```

The physical-input variant requires Accessibility permission for
`app/scripts/real-app-harness/.build/attn Input Driver.app`. The harness
packages the helper with fixed bundle identifier `com.attn.harness.input-driver`
and signs it with an installed Apple Development identity when available.
Grant permission to that app bundle, not to the obsolete raw helper path.
Without permission it intentionally fails before posting OS input events.

For manual diagnostics only, a separate native window may be launched with
`ATTN_PROFILE=agenttest ATTN_AUTOMATION=1 ATTN_AUTOMATION_START_EMPTY=1`;
do not treat a window connected to a persistent dev daemon as isolated test
evidence.

Add focused native tests for:

- protocol parsing of v65 workspace snapshots and layout events;
- workspace registry replacement/upsert/removal from daemon broadcasts;
- create-flow command ordering and spawn failure display;
- pane-runtime retention dropping inactive workspace Ghostty surface sets;
- terminal sequence/desync/replay behavior retained from the archive,
  including no live PTY replies from historical replay;
- visible-pane create-time attach, selected-workspace-only reconnect
  reattachment, and no fallback attach in start-empty automation mode;
- raw PTY input byte preservation, `ghostty_surface_key` input forwarding,
  pane-selection focus ownership, and unfocused `type_pane_via_ui` rejection;
- `terminal_attach_processed` event emission after the model consumes an
  attach snapshot/replay;
- native automation gate, manifest/token validation, snapshot shape, and
  command/readback actions, including background runs that restore the prior
  foreground app after surface bootstrap and do not take focus during
  mounted-surface actions;
- settings/Todo/diff component surfaces consuming daemon data without local
  canonical mutations.

## Manual Verification

Use the isolated dev daemon and never replace the live app during this work:

```bash
make dev-native
```

For the first runnable prototype:

1. Confirm `make dev-native` launches `attn-native` against
   `ws://localhost:29849/ws`.
2. Confirm existing daemon workspaces appear in the native sidebar.
3. Create a new workspace via the imported/adapted location dialog.
4. Confirm it appears only after daemon confirmation and its pane accepts
   input and displays agent output.
5. Open or load a workspace with shell, Claude, and Codex panes; verify the
   full layout renders and `Cmd+Arrow` moves input focus between panes.
6. Switch between two workspaces and confirm the pane set is reconstructed
   through attach/replay.
7. Observe native memory while switching repeatedly; inactive workspaces
   should not accumulate live terminal buffers.
8. Compare `release-on-switch` against the single-workspace warm-cache mode and
   record memory plus switch-to-output latency before finalizing retention.
9. When the Todo slice is built, open it beside a terminal pane; verify it
   focuses through pane navigation and persists through daemon state.

## Risks

- The archived native Rust protocol is now stale relative to protocol v65; a
  careless reuse would fail at handshake or silently miss workspace layout
  events.
- Tauri migration implementation may still contain transitional session-first
  code; the daemon protocol and migration plan, not incidental frontend
  structure, define native ownership.
- Todo as a pane simplifies the UI but requires a new non-terminal layout kind
  and persisted daemon data; implementing it before terminal foundations would
  expand the initial rewrite unnecessarily.
- `gpui-component` provides broad surface coverage but adds initialization,
  theming, and dependency cost; measure memory/startup before broadening its
  role beyond conventional controls and rich non-terminal panes.
- Ghostty static linking depends on a patched Zig `0.15` build when using
  macOS SDK 26.4; unpatched asdf Zig fails before the native app compiles.
- GPUI can compile without the standalone Metal toolchain through its
  `runtime_shaders` feature; measure its runtime/startup tradeoff rather than
  removing it merely to mirror a release build.
