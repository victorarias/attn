# Native Workspace And Pane Launcher

## Decision Amendment: 2026-05-25

The launcher parity audit in
`docs/plans/2026-05-25-native-launcher-tauri-parity-analysis.md` resolves
several details that were still tentative in this proposal:

- Both New Workspace and Add Pane remember the last selected pane choice,
  including Terminal, through a new daemon-persisted setting. Keep
  `new_session_agent` compatible as the last selected coding agent.
- A workspace location is its default starting location, not a checkout
  boundary. Panes in the same workspace may start in multiple directories and
  multiple worktrees.
- Add Pane should prefer the focused Ghostty terminal's validated reported PWD
  when available, by consuming GhosttyKit's existing `GHOSTTY_ACTION_PWD`
  runtime action, with the workspace default location as fallback.
- Launcher key behavior must route through semantic actions so future
  daemon-stored, user-configurable bindings do not require replacing dialog
  state or command handling.
- Local-only target support is an approved narrow slice; the model must retain
  target/capability abstractions needed to add remote targets.

## Why

The native client already proves the important foundation: daemon-owned
workspace layouts displayed through Swift/AppKit-hosted Ghostty surfaces.
The next useful product slice is not a new visual concept. It is
the creation workflow users already understand from the Tauri app.

The Tauri `LocationPicker` is currently called "New Session", because Tauri
still presents sessions as its top-level UI object. The native app is
workspace-first. It should reuse the picker's mechanics while giving them the
correct meaning:

- `New Workspace` selects a location and launches the workspace's initial
  terminal or coding-agent session.
- `Add Pane` selects a terminal or coding agent to open as a new split in the
  selected workspace.
- `Cmd+D` and `Cmd+Shift+D` remain immediate shell splits, with no dialog.

This plan supersedes the broad UI proposal archived at
`archive/native-workspace-prototype/native-gpui-workspace-client-prototype.md`.
It deliberately scopes the next implementation step to launchers and pane
creation, rather than prematurely planning Todo, settings, diff, or dashboard
parity.

## Target Experience

### New Workspace

The user invokes `New Workspace` and receives the native equivalent of
Tauri's location picker:

1. Pick a local or connected remote target.
2. Pick a terminal or coding agent.
3. Select a directory, repository checkout, existing worktree, or create a
   worktree.
4. When a coding agent is selected, optionally enable its permissive/yolo mode.
5. Confirm once to create a workspace and immediately open its first terminal
   or agent pane.

The product action is atomic from the user's perspective: a workspace without
an initial session is not a useful visible outcome. Protocol sequencing is
handled through one daemon command:

```text
bootstrap_workspace(workspace_id, title, directory, endpoint_id?,
                    initial_session: { session_id, cwd, kind, agent?,
                                       yolo_mode?, executable? })
workspace_registered / workspace_state_changed / workspace_layout_updated
render first pane and select the workspace
```

`bootstrap_workspace` is required rather than implementing client-side
register-then-spawn compensation. The daemon must either create the workspace
and initial session/layout together or publish no new workspace. This avoids
empty workspaces after a failed first spawn. Existing separate
`register_workspace` and `spawn_session` remain useful for internal/legacy
flows and adding sessions to a workspace.

An initial `Terminal` must be represented as a first-class shell session in
the root pane. This is distinct from a later quick shell split, which remains
a layout-owned utility runtime. The current layout normalization assumes that
the `main` pane is an agent session; implementing shell-first workspaces must
generalize that invariant rather than relabel a shell as an agent.

### Add Pane

Within the selected workspace, the user can invoke `Add Pane`. It is a
variant of the same launcher, seeded with the workspace context:

- The initial path is the selected workspace directory.
- The path remains editable, so an agent can run in a subdirectory or another
  deliberate working directory while still belonging to the workspace.
- The target endpoint defaults to the workspace's endpoint and should not
  silently move a pane to another daemon. Cross-endpoint panes are out of
  scope until their ownership semantics are explicit.
- The user chooses a pane kind: `Terminal` or one of the available coding
  agents.
- For an agent, the agent and yolo controls follow the New Workspace behavior.
- For a terminal, agent/yolo controls are absent or disabled because the
  runtime is a shell.

Agent pane creation is represented by the daemon: spawning a new agent
session with an existing `workspace_id`, target pane and direction creates a
split agent pane at the requested location.

Terminal pane creation shares the dialog's placement intent through the shell
split operation, which now accepts an edited working directory. Quick shell
splits omit that override and use the workspace directory.

`Add Pane` must also carry an insertion direction. Proposed keyboard-first
invocation:

| Invocation | Outcome |
| --- | --- |
| `Cmd+Shift+N` | Open `New Workspace`. |
| `Cmd+N` | Open `Add Pane` for a vertical split. |
| `Cmd+Option+N` | Open `Add Pane` for a horizontal split. |

The selected direction applies to either a terminal or coding-agent pane.
Both operations carry an explicit target pane and direction, so placement
does not depend on a focus race.

### Close Cascade

`Cmd+W` consumes workspace content before it closes the application window:

1. Close auxiliary panes in the selected workspace one at a time.
2. Once only the root pane remains, close that workspace.
3. Continue with the next selected workspace until the pane area displays its
   empty background.
4. Only when no workspace remains does `Cmd+W` close the window and terminate
   the native client.

If the fixed root pane has focus while auxiliary panes remain, the last
auxiliary pane closes first; the daemon root-pane identity is not promoted or
rewritten solely for window-close navigation.

### Fast Shell Splits

The existing terminal shortcuts remain first-class:

| Action | Outcome |
| --- | --- |
| `Cmd+D` | Split the active pane vertically and create a shell runtime. |
| `Cmd+Shift+D` | Split the active pane horizontally and create a shell runtime. |

These actions must not open `Add Pane`; their value is instantaneous terminal
creation. The dialog is for choosing agent/runtime type or overriding launch
inputs.

## Tauri Dialog Feature Map

The source behavior is implemented in:

- `app/src/components/LocationPicker.tsx`
- `app/src/components/NewSessionDialog/PathInput.tsx`
- `app/src/components/NewSessionDialog/RepoOptions.tsx`

### Stage 1: Location Selection

| Tauri behavior | Native launcher requirement |
| --- | --- |
| Modal overlay closes on outside click and participates in an Escape stack. | Provide a modal with cancel/outside-click/Escape behavior; nested repo actions consume Escape before the modal closes. |
| Agent selector orders `Claude`, `Codex`, `Copilot`, `Pi`, then dynamic agents; unavailable agents are disabled. | Both variants add `Terminal` beside the agent choices and preserve disabled agent visibility and CLI-not-found explanation. |
| Selected agent is persisted as `new_session_agent`; unavailable persisted choices fall back to an available agent. | Persist the last Terminal-or-agent pane choice in a new daemon setting and continue storing a selected coding agent in `new_session_agent` for compatibility. |
| Target selector includes local plus enabled remote endpoints, displays status, and disables disconnected endpoints. | New Workspace copies target selection. Add Pane locks or defaults target to its workspace endpoint for the first slice. |
| `Option+1..9` changes agent and `Option+<letter>` changes target. | Preserve keyboard-first selection through semantic shortcut actions whose bindings can later be supplied by daemon settings. |
| Selecting the current target again toggles yolo mode when the selected agent supports it. | Preserve for coding agents; hide or disable for terminal panes. |
| Yolo preference is stored per local target or remote daemon/endpoint. | Use the same setting keys so native and Tauri choices agree. |
| Path input starts at projects directory, remote projects directory, or `~`. | New Workspace uses the target default. Add Pane uses focused terminal reported PWD when trustworthy, then falls back to the workspace default location. |
| Recent locations are fetched per target and filtered by the current input. | Use one target-scoped recent-location history shared by New Workspace and Add Pane; a location used in either flow appears in both. |
| Filesystem directory suggestions update while typing. | Copy; path validation remains daemon-backed. |
| Ghost-text completion and `Tab` completion are supported. | Copy the completion interaction, not just a plain text field. |
| Arrow keys cycle recent/directory results; Enter inspects and chooses. | Preserve keyboard navigation. |
| Directory inspection resolves the physical path and detects repository roots. | Copy; do not infer repository status locally. |
| Loading and error states distinguish inspection and repository-option loading. | Surface pending operations and errors rather than closing the dialog optimistically. |

### Stage 2: Repository And Worktree Selection

When the selected location belongs to a repository, Tauri changes from path
results to destination selection.

| Tauri behavior | Native launcher requirement |
| --- | --- |
| Shows the main checkout with branch, abbreviated commit, age, and path. | Copy for a selected repository. |
| Lists existing worktrees and their branches. | Copy. |
| Supports selecting a main checkout or worktree with keyboard or pointer. | Copy. |
| Supports `Create worktree...` with a branch name and source choice: current branch or default branch. | Copy in both modes. Add Pane may intentionally create a pane in another worktree without changing the workspace default location. |
| Supports refreshing repository destinations. | Copy. |
| Supports deleting an existing worktree with a confirmation interaction. | Copy only where it already makes sense for a launch dialog; never delete the active workspace directory from Add Pane. |
| Nested Escape unwinds delete/create sub-state, then returns to location selection, then closes the modal. | Preserve the ordered cancellation behavior. |

## Dialog Variants

Implement one shared launcher model with explicit mode, not two drifting
dialogs.

```swift
enum LauncherMode {
    NewWorkspace,
    AddPane {
        workspace_id: String,
        target_pane_id: String,
        direction: SplitDirection,
    },
}

enum PaneChoice {
    Shell,
    Agent(SessionAgent),
}
```

### Shared Controls

- location path input, recent locations, directory suggestions, completion;
- repository/worktree destination stage;
- target display and connectivity/error state;
- pending operation state, cancel behavior, and keyboard navigation;
- settings-backed agent and yolo preference handling where applicable.

### New Workspace Differences

- Title: `New Workspace`.
- Pane choice includes `Terminal` and each available coding agent.
- Target can be changed among local and connected remote endpoints.
- Successful confirmation uses `bootstrap_workspace` to create the workspace,
  first session, and root pane atomically.

If `Terminal` is selected, the initial root pane is a first-class shell
session. If an agent is selected, the initial root pane is that coding agent
session.

### Add Pane Differences

- Title: `Add Pane`.
- Target workspace is displayed and preselected.
- Pane-kind control begins with `Terminal`, `Claude`, `Codex`, `Copilot`,
  `Pi`, filtered/disabled by availability.
- Location begins at the workspace directory but stays editable.
- The invocation carries vertical or horizontal insertion direction and shows
  it before submission.
- An agent selection uses `spawn_session(..., existing_workspace_id, ...)`;
  the daemon adds an agent pane at the requested target/direction.
- A terminal selection creates a shell split relative to the active/target
  pane.

For placement, the current daemon behavior differs by pane kind:

| Choice | Existing daemon behavior | Required first implementation |
| --- | --- | --- |
| Terminal | `workspace_layout_split_pane` accepts target, direction and optional `cwd`. | Use it for quick shortcuts and dialog-created terminal panes. |
| Coding agent | `spawn_session` with an existing workspace accepts target pane and direction. | Use it for both directional Add Pane invocations. |

Implemented backend decisions:

1. Direction is required for `Add Pane` and is passed for terminal and agent
   insertion.
2. Edited shell-pane paths are carried as a validated `cwd` on the split
   command.

## Required Daemon Changes

### Atomic Workspace Bootstrap

Add `bootstrap_workspace` and its matching result protocol surface. It
accepts workspace identity, location and endpoint plus an initial pane/session
specification (`Shell` or an agent), then commits workspace registration,
runtime spawn, membership and initial layout atomically. If runtime creation
fails, the daemon must persist and publish none of that new workspace state.

### Root Pane Kind

Generalize `DefaultWorkspaceLayout` and `NormalizeWorkspaceLayout`: the root
pane represents the workspace's initial session, not implicitly an agent. A
root shell pane is `kind: shell` with a `session_id`, because it is a
first-class shell session. Quick shell splits remain layout-owned utility PTYs
without session identity. Closing the root pane remains a workspace close
operation regardless of whether its initial runtime is shell or agent.

### Directional Agent Insertion

Extend additional agent-session creation with `target_pane_id` and
`direction`, or introduce an explicit pane-spawn operation that covers this
placement while preserving agent sessions as first-class sessions. `Add Pane`
must not change focus and then rely on the daemon guessing the intended split
target.

## Commands And Data Flow

### New Workspace Submission

```text
launcher validates selected target, path and selected terminal/agent type
  -> bootstrap_workspace with workspace fields and initial session spec
  <- bootstrap result and workspace/session/layout broadcasts
  -> select workspace and focus its confirmed first pane
```

Do not create a canonical sidebar row or terminal view from local form state.
Pending UI may say `Creating workspace...`; visible workspace and pane state
come from daemon broadcasts.

The daemon must make bootstrap failure atomic: if the initial runtime cannot
be created, the command returns an error and no empty workspace/layout remains
visible or persisted.

### Add Agent Pane Submission

```text
launcher captures selected workspace, target pane and direction
  -> spawn_session with existing workspace_id, chosen cwd, agent options,
     target pane and direction
  <- session/workspace_layout broadcasts
  -> render and focus the daemon-confirmed agent pane
```

The current daemon inserts additional agent sessions vertically beside its
active pane. Extend this operation with explicit target pane and direction so
Add Pane placement is independent of a focus race and matches its invocation.

### Add Terminal Pane Submission

```text
launcher captures selected workspace, target pane and direction
  -> workspace_layout_split_pane(workspace_id, target_pane_id, direction)
  <- workspace_layout_action_result and workspace_layout_updated
  -> attach and focus the confirmed shell pane
```

### Shortcut Submission

```text
Cmd+D / Cmd+Shift+D
  -> workspace_layout_split_pane(selected_workspace, active_pane, direction)
  <- workspace_layout_updated
  -> attach and focus confirmed shell pane
```

## Ownership And Invariants

| Concern | Owner |
| --- | --- |
| Workspaces, workspace membership, lifecycle rollup | daemon |
| Atomic workspace bootstrap, pane layout, pane kind, active pane, runtime creation | daemon |
| Agent and shell PTYs, replay and output | daemon |
| Dialog open state, draft fields, highlighted option and pending spinner | native UI |
| Location/repo/worktree lookup results | daemon responses presented by native UI |
| Visible terminal rendering and keyboard focus | GPUI/Ghostty after daemon confirmation |

Invariants:

- Native never persists canonical workspaces or layout changes from dialog
  drafts.
- An agent pane is a real session associated with its workspace, not a shell
  pane painted with an agent label.
- A root terminal pane is a first-class shell session; it is not an agent
  placeholder or a utility split.
- A shell quick split remains a utility pane, not a top-level session.
- A new session must carry `workspace_id`.
- A dialog action does not manufacture a `GhosttySurface` until its pane is
  visible in daemon-confirmed layout state.
- Terminal rendering remains Ghostty-backed; this launcher work does not
  reopen cell-by-cell rendering.

## Shortcuts And Slicing

Forbidden shortcuts:

- Building a simplified native path text box and calling it parity with the
  Tauri picker.
- Creating workspace rows or panes optimistically and reconciling them later.
- Implementing New Workspace as visible `register_workspace` followed by
  fallible client-side `spawn_session`; use atomic `bootstrap_workspace`.
- Letting Add Pane silently ignore an edited shell path.
- Replacing `Cmd+D` / `Cmd+Shift+D` with the dialog flow.
- Porting Tauri's session-shaped local store into the native workspace-first
  client.
- Hard-coding native shortcut behavior without a semantic action layer that
  can consume daemon-persisted binding overrides later.
- Expanding this slice into Todo, settings, diff, Markdown, or full dashboard
  work before the launcher works end to end.

Intentional slices:

| Slice | Deferred behavior | Extension point / completion trigger |
| --- | --- | --- |
| Worktree deletion is not in the first Swift sheet. | Deleting an existing worktree from a launch flow. | Add explicit confirmation and prevent deletion of the active workspace directory. |
| Local target first is acceptable for initial UI bring-up. | Remote picker target parity. | Retain launcher target model and wire endpoint commands before claiming complete Tauri dialog parity. |

## Implementation Sequence

1. Add native launcher state types for mode, stage, target, path,
   destination, pane choice, agent/yolo options, pending operation, and error.
2. Add the protocol command/results needed by native for recent locations,
   browsing, inspection, repository information, worktree creation/deletion,
   and settings-backed launcher preferences.
3. Implement shared native launcher shell and path-selection stage using
   `gpui-component` form/modal controls.
4. Implement repository/worktree stage, including nested cancel behavior and
   pending/error display.
5. Implement daemon `bootstrap_workspace`, including agent-first and
   shell-first root panes, atomic failure semantics, protocol generation, and
   native protocol support.
6. Wire `NewWorkspace` submission through `bootstrap_workspace` confirmation.
7. Extend agent insertion to accept target pane and direction; wire `AddPane`
   agent submission using an existing workspace id and confirm the new pane is
   rendered/focused.
8. Add native `Cmd+D` and `Cmd+Shift+D` bindings for immediate shell splits,
   then wire the Terminal choice and `Cmd+N` / `Cmd+Option+N` directional
   launchers in `AddPane`.
9. Handle GhosttyKit's existing validated `GHOSTTY_ACTION_PWD` runtime action
   in native state and use it as the Add Pane starting-directory preference,
   falling back to the workspace default location.
10. Extend native automation to drive both launcher variants without stealing
   focus from the user's foreground app.

## Feature Checklist

### New Workspace

- [x] Modal launcher opens and cancels correctly.
- [x] Terminal/agent selection and unavailable-agent display.
- [ ] Local/remote target selection and target-specific yolo preference.
- [x] Initial path, recent paths, directory browsing and completion.
- [x] Repository checkout and worktree selection.
- [x] Worktree creation.
- [ ] Worktree refresh and safe deletion behavior.
- [x] Atomically bootstrap an agent-first or shell-first workspace and show
  only confirmed result.
- [x] Bootstrap errors leave no partial workspace state.

### Add Pane

- [x] Opens from selected workspace and identifies the insertion target.
- [x] Prepopulates editable workspace path.
- [x] Pane kind chooses terminal versus available coding agent.
- [x] Direction is selected by invocation and visible in the launcher.
- [x] Agent submission creates a daemon-confirmed split in that direction.
- [x] Terminal submission creates a daemon-confirmed split in that direction.
- [x] Edited shell-path behavior is either implemented or visibly disallowed.
- [x] Resulting pane gains focus without stealing it from another pane
  prematurely.

### Existing Terminal Splits

- [x] `Cmd+D` creates a vertical shell split.
- [x] `Cmd+Shift+D` creates a horizontal shell split.
- [x] Both actions use current workspace/active pane and bypass the launcher.
- [x] Split close/focus and terminal input continue working through Ghostty.
- [x] Nested split geometry preserves daemon ratios when quick splitting.
- [x] `Cmd+W` closes panes/workspaces in sequence, then closes the empty window.

## Cleanup Checklist

- Archive the broader native workspace-client proposal and its explainer so
  it is not mistaken for the current launcher scope.
- Remove any newly introduced duplicate dialog state once both variants share
  one launcher model.
- Remove temporary local-only target/path stubs when daemon-backed lookup is
  wired.
- Do not introduce client-side partial-failure compensation now that
  `bootstrap_workspace` owns atomic workspace-plus-initial-session creation.

## Automated Verification

- Unit-test launcher state transitions: path selection, repo stage, nested
  Escape handling, target change reset, agent availability fallback and
  yolo-setting selection.
- Unit-test `bootstrap_workspace` atomicity for agent-first and shell-first
  workspaces, including runtime failure leaving no persisted workspace.
- Unit-test `AddPane` routing: agent choice issues session spawn against the
  selected workspace with target/direction; terminal choice issues layout
  split with target/direction.
- Unit-test shortcut bindings so `Cmd+D` and `Cmd+Shift+D` issue shell splits
  without opening the launcher.
- Unit-test native close policy for auxiliary panes, final workspace close and
  empty-window fall-through.
- Extend native background automation with:
  - create an agent-first workspace and observe its confirmed pane;
  - create a shell-first workspace and observe its confirmed pane;
  - add a second agent pane and type through its Ghostty input path;
  - create shell panes through both keyboard splits;
  - open/cancel the launcher and assert no daemon state changed;
  - assert nested quick-split geometry preserves the outer sibling ratio;
  - press `Cmd+W` through panes, workspaces and final window process exit;
  - assert structured state reports pane kind, workspace id and focused pane.

## Manual Verification

1. Open `New Workspace`, navigate with the keyboard, choose a repository or
   worktree, start an agent, and confirm the workspace appears with one live
   rendered terminal. Repeat with `Terminal` selected.
2. In that workspace, open `Add Pane`, leave its path unchanged, choose
   another coding agent, and confirm a second focused agent terminal appears.
3. Open `Add Pane` for `Terminal`; verify only supported path/direction
   choices are offered and the shell receives keyboard input.
4. Use `Cmd+D` and `Cmd+Shift+D` without opening the dialog; confirm immediate
   vertical and horizontal shell splits.
5. Switch away and back, reconnect the daemon, and confirm all confirmed panes
   restore with correct Ghostty rendering and input.
6. When remote support is wired, repeat New Workspace and Add Pane through a
   connected endpoint and verify disconnected endpoints remain unavailable.
