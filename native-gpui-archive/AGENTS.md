# native-ui

Instructions for the active GPUI native client. Read
`../docs/plans/2026-05-24-native-workspace-pane-launcher.md` when changing
workspace or pane creation behavior. The broader prototype proposal is
archived under `../docs/plans/archive/native-workspace-prototype/`.

## Scope

- Active crates: `crates/attn-native-app/` and `crates/attn-protocol/`.
- Archived reference only: `crates/attn-native-canvas-archive/` and
  `crates/attn-native-canvas-protocol-archive/`. They are excluded from the
  active Cargo workspace; do not update, build, or use them as evidence for
  current behavior unless the task explicitly targets the archive.
- The active app is a GPUI renderer of the daemon's workspace-layout model,
  not a continuation of the infinite-canvas experiment. Terminal panes must
  embed full Ghostty surfaces; do not reintroduce the removed `libghostty-vt`
  plus GPUI-painted terminal path.

## Build And Verify

The active app must compile with the real Ghostty surface renderer. Do not add
a parser-disabled, terminal-stub, or custom GPUI terminal-painting fallback.

The attn Ghostty fork is cloned at `../ghostty` relative to this worktree and
its `attn/external-io` branch exposes daemon-owned PTY integration. Ghostty
requires Zig `0.15.x`; on macOS SDK 26.4, use Homebrew's patched `zig@0.15`
on `PATH`. Full Metal surface builds also require Xcode's Metal Toolchain
component (`xcodebuild -downloadComponent MetalToolchain`).

```bash
cd native-ui
export PATH="$(brew --prefix zig@0.15)/bin:$PATH"

cargo fmt --check
cargo test -p attn-protocol
cargo test -p attn-native-app --no-run
```

Build the native Ghostty framework used by terminal surface integration:

```bash
make build-native-ghostty-kit
```

When exercising the client against a running Attn installation, use the
isolated dev daemon from the repository root:

```bash
make dev-native
```

`make dev-native` enables the local automation sidecar through
the bundled `dev` profile and daemon URL, so a macOS privacy-permission
relaunch remains isolated from the production daemon. Runtime environment
variables still override those defaults for test-owned profiles. Once it is
running, inspect or drive it from another shell:

```bash
pnpm --dir app run native:bridge-cli -- get_state
pnpm --dir app run native:bridge-cli -- screenshot '{"path":"/tmp/attn-native.png"}'
```

The screenshot command calls the native automation action, which invokes
macOS exact-window capture from the signed `attn-native-dev.app` process; the
harness supplies its native window id so overlapping windows do not replace
the evidence. Grant Screen Recording permission to the generated bundle at
`native-ui/target/debug/attn-native-dev.app`, not the external harness
process or its raw binary.

On macOS, the Makefile selects a locally installed `Apple Development`
code-signing identity when one is available and otherwise falls back to
ad-hoc signing. Keep a stable identity configured while using Screen
Recording automation: ad-hoc signing changes the privacy identity after
source rebuilds. Override selection explicitly with
`MACOS_CODESIGN_IDENTITY="<SHA-1 fingerprint>"` when pinning a specific
identity, particularly if multiple development certificates are installed.

For agent-driven product smoke testing, run:

```bash
make test-native-agent
```

That target launches a uniquely profiled daemon and native window owned by the
scenario and removes its temporary profile data on exit. Set
`ATTN_NATIVE_KEEP_ARTIFACTS=1` only when debugging a failed run. Do not use
the persistent `dev` daemon as automated-test isolation.
It sets `ATTN_AUTOMATION_BACKGROUND=1`: the runner lets the first AppKit
Ghostty surface mount, immediately restores the previously foreground
application, then asserts that bridge-driven output/input on that mounted
surface does not take foreground focus again. Cold Ghostty surface creation
(for example, release-on-workspace-switch reattachment) is covered by the
foreground run because AppKit surface bootstrap currently needs an active
application.
Use `ATTN_NATIVE_FOREGROUND=1 make test-native-agent` for release/reattach and
split-pane lifecycle verification; it intentionally allows the test window to
activate. Add `ATTN_NATIVE_PHYSICAL_INPUT=1` to that command to have the
macOS input driver click and type through real OS events; this additionally
requires Accessibility permission for the compiled input-driver helper. The
harness packages it as `app/scripts/real-app-harness/.build/attn Input
Driver.app` with bundle identifier `com.attn.harness.input-driver` and signs
it with the same available Apple Development identity strategy as the app
bundle. Grant Accessibility to that app bundle, not to a prior raw helper
binary.
When launching a second window manually, additionally pass
`ATTN_AUTOMATION_START_EMPTY=1` so it does not initially attach and resize an
existing workspace pane.

Before changing terminal lifecycle or automation input, read the archived
implementations only for attach/reconnect/replay sequencing, raw PTY bytes,
and automation behavior. Do not carry over its terminal painting. The
surface-backed implementation must feed daemon output into Ghostty's external
I/O surface, send Ghostty input callbacks to the daemon, suppress callback
responses during historical replay, and forward Ghostty-derived resize
geometry to the daemon.

Never run `make install` or `make install-daemon` while developing this client
against an active Attn session; those commands overwrite or restart the live
installation.

## Ownership Rules

- The Go daemon is canonical for workspaces, `WorkspaceLayout`, pane
  membership and focus, session lifecycle, PTY ownership and replay, persisted
  splits, and session `muted` state.
- The native client owns only ephemeral UI state: selected/focused views,
  pending-result feedback, GPUI entities, and visible terminal renderer
  lifetimes.
- Submit layout or focus changes through daemon commands and reconcile from
  confirmed events. Do not persist or optimistically mutate a competing local
  layout.
- Keep terminal bytes off root application state. Allocate Ghostty surfaces
  only for visible attached panes in the selected workspace; measure before
  retaining inactive-workspace surfaces.
- Use existing daemon surfaces before inventing protocol: `mute` for session
  muting, existing settings events/commands for settings, existing diff and
  review events for review panes, and session todo messages for the initial
  Todo pane.

## Product Constraints

- The sidebar is workspace-first and contains nested pane rows; do not add a
  second workspace-tab navigation system.
- Pane identity, focus indication, session activity, and mute/close actions
  belong in sidebar rows, not duplicated title bars on rendered panes.
- Muting a runtime-backed pane suppresses attention only. It must not pause
  the session, detach the PTY, hide underlying state, or remove the pane.
- Selecting a workspace renders its daemon-owned layout. Agent sessions,
  first-class shell sessions, utility shells, Todo, diff, and future content
  are panes inside that layout, not top-level workspace items.
- Todo begins as a non-terminal pane driven by existing daemon session todo
  messages. Do not create native-local checklist persistence or broader Todo
  protocol without an explicit product decision.
- Keep creation affordances unobtrusive; do not add a permanent workspace
  header/shortcut bar or a large combined sidebar creation button.
- `Cmd+Arrow` pane navigation operates on daemon pane identities and reports
  focus through `workspace_layout_focus_pane`.

## Code Placement

- `crates/attn-protocol/`: protocol-v65 Rust subset corresponding to
  `../internal/protocol/schema/main.tsp`. Model `Workspace`,
  `WorkspaceLayout`, relevant workspace commands/events, and PTY messages
  here.
- `crates/attn-native-app/src/adapters/`: websocket, reconnect,
  command/result, and external-process boundaries.
- `crates/attn-native-app/src/state/`: observable client snapshots and
  Ghostty surface lifetimes. Daemon state stored here is a received snapshot,
  not a new authority.
- `crates/attn-native-app/src/views/`: GPUI rendering and input forwarding.
- `crates/attn-native-app/src/domain/`: add only pure logic that does not
  depend on GPUI runtime entities or external I/O.
- `crates/attn-native-app/src/app.rs`: wiring and top-level composition;
  move domain or protocol branching out of it as features grow.

Use `gpui-component` for conventional app surfaces such as modal/form
controls, settings, Todo content, Markdown, and review UI. Initialize and
retain its required `Root`/theme boundary. Do not use it to own daemon layout
state, render terminals instead of Ghostty, or replace the custom sidebar
activity/mute/close interaction.

## Reuse From The Archive

The archive may be consulted for mechanics that remain valid:

- adapter/state/view/domain organization;
- reconnecting websocket behavior;
- attach/replay/desync and PTY input/resize protocol behavior;
- location/worktree browsing interaction;
- runtime-gated automation patterns;
- theme tokens where they fit the non-canvas client.

Do not copy canvas viewport/pan/zoom/panel geometry, its GPUI terminal
renderer, protocol-v64 `WorkspacePanel` wire models, canvas-era
`shell_as_session` assumptions, or local layout mutation. Preserve the need
for first-class shell sessions by designing their protocol-v65
workspace-layout behavior explicitly.
