# attn native (Swift)

This is the active macOS native client. It is a SwiftPM executable packaged
as a signed app bundle by the repository `Makefile`; it does not use an
Xcode project.

The archived Rust/GPUI experiment is in `../native-gpui-archive/`. Consult it
for protocol and daemon lessons only; do not port its composition layer or
terminal event forwarding.

## Boundary

- The Go daemon owns workspaces, layout, sessions and PTYs.
- The Swift client owns native window/sidebar/modal-overlay presentation.
- The attn Ghostty fork owns terminal rendering and native surface input
  behavior, exposed to this client through an external-I/O surface host.

The architecture and first vertical slice are documented in
`../docs/plans/2026-05-24-swift-native-workspace-client.md`.

The terminal foundation and first launcher slice are implemented: the
selected daemon workspace mounts a native Ghostty external-I/O surface and
routes its PTY through the daemon, while native `New Workspace` and `Add
Pane` overlays create daemon-owned terminals and agent sessions above that
surface. The selected workspace renders its daemon split tree, so dialog and
quick-split panes remain live simultaneously.

Mounted pane focus is explicit: only the active Ghostty surface receives
focused cursor rendering. As in Ghostty.app, inactive splits receive a subtle
background overlay so full-screen agent prompts do not visually read as active.
Attach replay is terminal state restoration, not live output: raw startup bytes
are parsed with Ghostty writes suppressed so stateful modes such as Claude
Code's focus reporting survive an initial attach without historical queries
being sent back into the running PTY.

Add Pane reads the focused terminal's current working directory from Ghostty's
validated shell-integration state and uses it as an initial suggestion only;
the workspace location remains its persistent default. Tests for this behavior
must issue a real shell `cd`: printing a synthetic OSC-7 directory report is
not equivalent, because the shell's next prompt correctly reports its actual
directory and supersedes the fabricated value.

Native shortcuts:

- `Cmd+Shift+N`: open New Workspace.
- `Cmd+N`: add a vertical pane.
- `Cmd+Option+N`: add a horizontal pane.
- `Cmd+D` / `Cmd+Shift+D`: immediately split a shell vertically/horizontally.
- `Cmd+Option+Arrow`: focus the adjacent pane by layout position; at a pane edge,
  switch to the previous/next workspace, wrapping through the sidebar order.
- `Cmd+W`: close panes, then workspaces, then the empty window.

If the last live session in a retained workspace ends outside the close
cascade, the daemon publishes an empty layout instead of leaving stale panes.
The workspace remains selectable: `Cmd+N` creates its next first pane,
`Cmd+D` creates a first shell pane immediately, navigation leaves it normally,
and `Cmd+W` removes it.

## Commands

```bash
make build-native-app-dev
make dev-native
make test-native-swift
make test-native-automation
make test-native-terminal
ATTN_NATIVE_REAL_AGENTS=1 make test-native-terminal
ATTN_NATIVE_REAL_AGENTS=1 ATTN_NATIVE_PHYSICAL_INPUT=1 ATTN_NATIVE_INTERACTIVE_TEST=1 make test-native-terminal
```

`make dev-native` uses the isolated dev daemon profile and never replaces the
running production app. It owns the LaunchServices-launched dev client for the
duration of the command, so `Ctrl-C` closes a client newly launched by that
invocation without killing one that was already open. The app also holds one
client-instance lease per profile: reopening `dev` activates the existing
native client rather than opening a second one, while independently named
automation profiles may run in parallel. The lease lives in the client-neutral
`com.attn/client-instances` application-support scope so other attn shells can
adopt the same per-profile ownership rule during migration.

## Automation

The signed development app enables a tokenized localhost automation server by
default. It writes the existing native harness manifest under:

```text
~/Library/Application Support/com.attn.native.dev/debug/ui-automation.json
```

The implemented actions are `ping`, `get_state`, `list_panes`,
`select_workspace`, `navigate`, `open_new_workspace_dialog`, `open_add_pane_dialog`,
`quick_split`, `get_launcher_state`, `set_launcher_path`, `set_launcher_choice`,
`perform_launcher_action`,
`submit_launcher_location`, `choose_launcher_destination`, `cancel_launcher`,
`close_window`,
`close_selected_content`,
`focus_pane`, `type_terminal`, `press_terminal_enter`, `read_pane_text`, `get_surface_geometry`,
`tail_events`, `get_window_bounds`, `screenshot`, `screenshot_window`,
`set_window_background_mode`, and `park_window`. Use
`ATTN_AUTOMATION_BACKGROUND=1` when launching the app for non-frontmost
automation runs. Background harness launches must go through LaunchServices
without `-g`, for example `open -n --env ATTN_AUTOMATION=1 --env
ATTN_AUTOMATION_BACKGROUND=1 --env
ATTN_AUTOMATION_RESTORE_FOREGROUND_PID=<pid> <app-bundle>`: macOS suppresses
SwiftUI's first `WindowGroup` window under `open -g`, while a direct
executable spawn also does not open that window. When given the previous
foreground PID, the app restores it immediately after its native window is
created; subsequent bridge actions do not activate attn.

Native harness scenarios call `park_window` so only a narrow strip of the
window remains at the right edge of the screen while rendering and bridge
assertions run. Physical keyboard coverage temporarily activates that parked
window and invokes `focus_pane` with `key_window: true` only while sending the
required keys, then restores whichever application was frontmost immediately
before that input. Normal bridge-driven focus never claims the key window;
physical-input validation is not suitable while another person is using the
keyboard.

macOS persists window frames for the signed development bundle. A normal
`make dev-native` launch detects a frame left with only the automation edge
strip visible and recenters it; it does not reset normally visible placement.

`make test-native-terminal` launches a private daemon profile, creates
terminal fixtures directly over the daemon protocol, and verifies Ghostty
grid sizing and input/output for shell, Claude-kind and Codex-kind panes
through the native bridge. It also uses deterministic selectable-text and
DEC mouse-reporting fixtures to verify selection starts at the pressed cell
and captured TUI click/drag events reach the PTY with Ghostty coordinates.
It additionally drives `New Workspace` and `Add Pane` through the native
launcher, then verifies both dialog-created terminal
surfaces through Ghostty input/readback, changes directory in a real shell
and verifies Add Pane defaults from Ghostty's reported PWD, verifies proportional nested split
geometry, verifies pane navigation and workspace-edge fallback against the
live layout, creates a retained workspace with no live session and verifies
close, keyboard escape, and first-pane reopening, and closes a nested pane
while asserting every remaining pane keeps its live Ghostty surface rather
than blanking for a replay-driven remount. With
`ATTN_NATIVE_REAL_AGENTS=1`, it also starts the locally installed Claude Code
and Codex UIs and verifies that their interactive startup screens are readable
from the native Ghostty surfaces without foreground activation; for Claude it
also verifies that daemon-launched startup terminal modes survive initial
attach so an inactive pane receives Ghostty's focus-loss notification. The physical
input suite is intentionally interactive-only: set both
`ATTN_NATIVE_PHYSICAL_INPUT=1` and `ATTN_NATIVE_INTERACTIVE_TEST=1` when the
computer is available for brief focus transfers. It verifies ordinary AppKit
keyboard events, `Cmd+Shift+N`, selecting an Add Pane directory with
the ghost-text `Tab` completion and with `ArrowDown` plus `Return`, the
`Cmd+W` pane/workspace cascade, the empty-window state and process exit after
that empty window closes.
