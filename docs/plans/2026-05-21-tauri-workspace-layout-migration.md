# Tauri Workspace Layout Migration

## Why

The daemon now has a `Workspace` concept, but the Tauri app still treats
`Session` as the top-level UI object. That blocks the next product step:
one workspace with multiple agents. The existing Tauri split-terminal UI is
the right layout model; the abandoned native-canvas direction should not
drive daemon shape anymore.

This plan moves Tauri to daemon-owned workspaces in one PR. Workspaces become
the sidebar/top-level entity. Sessions become agent runtimes inside a
workspace. The existing split layout becomes a workspace layout, not a
session layout.

This supersedes the canvas-layout assumptions in:

- `docs/plans/2026-04-28-native-canvas-mvp.md`
- `docs/plans/native-canvas-architecture.md`

Those docs remain useful historical context, but the active target for Tauri
is daemon-owned workspace splits, not a canvas with panels.

## Vision

The daemon is the source of truth for:

- workspace existence, title, directory, status, and membership
- sessions/agents inside a workspace
- terminal split layout for the workspace
- active pane inside that workspace layout
- shell utility panes created by splitting

Tauri renders daemon snapshots and sends daemon commands. It does not create
optimistic local sessions, does not keep canonical workspace/layout state in
Zustand, and does not paper over daemon state with local fallbacks.

The first shipped version may still show one agent per workspace, but the
model must already support multiple agent sessions in one workspace. Adding
"new agent in this workspace" later should be an additive UI/control change,
not another data model migration.

## Target Model

### Workspace

`Workspace` is the top-level UI entity:

- `id`
- `title`
- `directory`
- `status`
- `layout`
- member sessions, either embedded or joined by `workspace_id`

Workspace status remains a daemon rollup of member session states.

### Session

`Session` remains the daemon representation of one agent/runtime:

- Claude / Codex / Copilot / Pi are agent sessions.
- A session belongs to exactly one workspace.
- `workspace_id` is required for session creation. The new protocol version
  should reject `spawn_session` without a workspace id instead of inventing a
  compatibility path.

### Workspace Layout

Replace `SessionLayout` with `WorkspaceLayout`.

Expected protocol shape:

```ts
WorkspaceLayout {
  workspace_id: string;
  active_pane_id: string;
  layout_json: string;
  panes: WorkspaceLayoutPane[];
  updated_at?: string;
}

WorkspaceLayoutPane {
  pane_id: string;
  runtime_id?: string;
  session_id?: string;
  kind: "agent" | "shell";
  title: string;
}
```

The main agent pane is no longer special because it is "the session"; it is a
workspace layout pane whose content is an agent session. Shell panes remain
non-session utility PTYs unless there is a strong future reason to promote
them.

## Protocol Changes

Remove the `session_layout_*` protocol surface and replace it with workspace
layout commands/events:

- `workspace_layout_get`
- `workspace_layout_split_pane`
- `workspace_layout_close_pane`
- `workspace_layout_focus_pane`
- `workspace_layout_rename_pane`
- `workspace_layout`
- `workspace_layout_updated`
- `workspace_layout_action_result`
- `workspace_layout_runtime_exited`

`InitialState` should send `workspaces` with their layouts. It should not send
`session_layouts`.

Keep `register_workspace`, `unregister_workspace`, and `spawn_session`
`workspace_id`, but make them serve the Tauri workspace flow rather than the
abandoned native canvas flow.

Increment `ProtocolVersion`, update TypeSpec, run `make generate-types`, and
update both Go and TS generated consumers.

## Store And Migration

Add workspace layout persistence owned by workspace id:

- `workspace_layouts`
- `workspace_layout_panes`

One-off SQLite migration for existing users:

1. For each existing session without `workspace_id`, create a workspace.
2. Use deterministic workspace ids, e.g. `workspace-${session_id}`, not the
   session id itself.
3. Set `sessions.workspace_id` to the new workspace id.
4. Copy `session_workspaces` into `workspace_layouts`.
5. Copy `workspace_panes` into `workspace_layout_panes`.
6. Convert the old `main` pane to an `agent` pane with `session_id` and
   `runtime_id` pointing at the migrated session.
7. Convert old `shell` panes as shell panes, preserving runtime ids, titles,
   and layout placement.
8. Normalize any invalid/missing layout as a single agent pane.

After migration, daemon code should no longer read or write
`session_workspaces` / `workspace_panes`. They can be dropped in the migration
if the project is comfortable doing so, or left unused with a clear cleanup
comment. The runtime path must not depend on them.

## Daemon Cleanup

The native-canvas support added concepts that no longer match the product
direction. Clean these up as part of the PR unless a current code path proves
they are still needed.

Remove or replace:

- `WorkspacePanel` protocol model.
- `Workspace.panels`.
- `update_workspace_panel_geometry`.
- `canvas_workspace_panels` table and store methods:
  `SaveWorkspacePanel`, `ListWorkspacePanels`,
  `RemoveWorkspacePanelForSession`.
- Workspace registry panel geometry/default placement logic:
  `ensurePanelForSession`, `panelForSession`, `setPanels`,
  `updatePanelGeometry`, `defaultWorkspacePanel*`.
- Comments and naming that describe workspaces as "native canvas" or
  "canvas UI" state.

Re-evaluate:

- `shell_as_session`: this existed so the native canvas could represent shell
  panels as first-class sessions. Tauri split shells are utility PTYs today.
  Do not keep this capability unless another supported client still needs it.
- Remote hub layout naming: `RemoteWorkspaces()` currently returns
  `[]SessionLayout`, and hub internals call layout snapshots `workspaces`.
  Rename to workspace layout and key by workspace id.
- Workspace unregister semantics: closing a workspace should close member
  agent sessions and utility shell panes owned by the workspace layout.

## Tauri Data Flow

The sidebar should render daemon workspaces, not local sessions.

Remove or shrink `useSessionStore` so it no longer owns canonical state:

- no optimistic local `sessions` array as the source of truth
- no local `activeSessionId` as the top-level selected entity
- no local `workspace: TerminalWorkspaceState` copied from daemon
- no `syncFromDaemonSessions` / `syncFromDaemonWorkspaces` canonical merge

Allowed Tauri-local state:

- selected workspace id / selected pane id as ephemeral UI focus
- modal state, toasts, progress indicators
- terminal renderer refs and transient xterm state
- command pending promises tied to daemon result events

The terminal workspace component can survive, but its props should be
workspace-based:

- `workspaceId`
- `workspaceTitle`
- `workspaceLayout`
- member agent sessions
- pane runtime metadata from daemon snapshot

Split/focus/close commands should address workspace id + pane id. Tauri
should update only after daemon layout events arrive.

## Creation And Closing Flow

### New Workspace

Tauri "new session" becomes "new workspace":

1. User selects directory + agent.
2. Tauri sends daemon command to create/register workspace.
3. Tauri asks daemon to spawn the first agent session with that workspace id.
4. Daemon creates/updates the workspace layout with one agent pane.
5. Tauri sidebar and terminal area update from daemon events.

No optimistic local session row.

### Existing CLI Entrypoints

CLI and other non-Tauri creation paths must create/register a workspace first,
then spawn the agent session with that workspace id. There is no compatibility
mode where session creation without `workspace_id` succeeds. This is a new
protocol version, and old clients should fail the version handshake instead of
creating orphan sessions.

### Close Workspace

Closing a workspace sends `unregister_workspace`. The daemon:

- terminates every member agent session cleanly and waits for the unregister
  path to tear down the underlying PTY/process
- terminates every shell utility runtime referenced by the workspace layout
- treats termination as mandatory before removing workspace/layout persistence;
  the close path must not leave orphan PTYs or child processes hanging
- removes workspace layout persistence only after member agent sessions and
  shell utility runtimes have been scheduled/confirmed for cleanup
- broadcasts session removals and workspace removal in a deterministic order

## Shortcuts & Slicing

The migration is one PR. The following shortcuts are forbidden:

- Keeping `session_layout_*` protocol as aliases while calling it workspace
  layout in Tauri.
- Keeping Tauri `useSessionStore` as canonical state and syncing daemon
  workspaces into it.
- Keeping canvas `WorkspacePanel` / geometry APIs around as "maybe later"
  support.
- Migrating only new sessions to workspaces and leaving old sessions
  session-based.
- Letting shell split panes become first-class sessions just to fit the
  workspace model.
- Skipping remote/hub layout migration and handling only local daemon
  layouts.
- Shipping without a one-off SQLite migration for existing
  `session_workspaces` / `workspace_panes`.

Acceptable incremental behavior inside the single PR:

- The UI may expose only one agent per workspace at first, as long as the
  daemon/store/protocol model supports multiple agent sessions per workspace.
- The "add another agent to this workspace" button/workflow can be deferred,
  but the extension point must be clean: spawn an agent with existing
  `workspace_id`, add an agent pane to `WorkspaceLayout`, broadcast the
  updated workspace.

## Success Criteria

- Tauri sidebar lists workspaces.
- Selecting a workspace shows its daemon-owned split layout.
- Creating a new workspace creates a daemon workspace and first agent session.
- Splitting terminals mutates daemon workspace layout, not local app state.
- Closing/reopening Tauri restores workspace layout from daemon state.
- Existing upgraded installs keep their sessions and split layouts.
- All app-created sessions have `workspace_id`.
- Future multi-agent support is a workspace-level addition, not another
  sidebar/data-model rewrite.
- No runtime code reads or writes old `SessionLayout` concepts.
- No runtime code uses canvas panel geometry.

## Implementation Outline

1. Add `WorkspaceLayout` domain types in Go, replacing `internal/sessionlayout`
   naming or moving it to `internal/workspacelayout`.
2. Add store APIs keyed by `workspace_id`.
3. Add SQLite migration from session layouts to workspace layouts.
4. Update TypeSpec protocol and regenerate types.
5. Replace daemon session-layout handlers with workspace-layout handlers.
6. Update PTY lifecycle cleanup to remove shell runtimes by workspace layout.
7. Update workspace creation/spawn association so layouts are created for
   workspaces, not sessions.
8. Update remote hub manager from remote session layouts to remote workspace
   layouts.
9. Refactor Tauri socket code to consume `workspaces` and workspace layout
   events.
10. Refactor Tauri sidebar and terminal workspace rendering to use workspace
    ids.
11. Remove old session-layout store/tests/types and canvas panel APIs.
12. Update automation bridge and E2E helpers to speak workspace ids.

## Cleanup Checklist

- Delete or rename `internal/daemon/sessionlayout.go`.
- Delete or rename `internal/sessionlayout`.
- Delete/replace `internal/store/sessionlayout.go`.
- Remove `SessionLayout*` protocol models, commands, events, constants, and
  parser branches.
- Remove `session_layouts` from initial state and response models.
- Remove `canvas_workspace_panels` runtime access.
- Remove `WorkspacePanel` model and geometry commands.
- Remove `CapabilityShellAsSession` unless a supported current client still
  needs it.
- Rename `DaemonWorkspace` in Tauri if it currently aliases
  `SessionLayout`; reserve that name for real daemon `Workspace`.
- Remove Tauri canonical session store behavior after workspace snapshots are
  authoritative.
- Update comments that mention native canvas as the reason for workspace
  behavior.
- Revisit old native canvas plan docs if they are confusing after this lands.

## Risks

- Protocol churn is broad. Missing one generated consumer will compile but
  break runtime behavior if TypeScript and Go disagree.
- The migration touches user data. It needs direct SQLite tests with realistic
  pre-migration rows.
- Remote endpoint/hub layout propagation is easy to miss because it uses the
  same protocol shapes but different storage.
- Removing optimistic Tauri state may expose ordering bugs in command/result
  flows that were previously hidden by local state.
- PTY cleanup must keep utility shell runtimes from leaking after workspace
  close or pane close.

## Automated Verification

Run at minimum:

```bash
make generate-types
go test ./internal/store ./internal/daemon ./internal/hub ./internal/protocol
pnpm --dir app test
make test-frontend
make test-e2e
```

Add focused tests:

- SQLite migration from old `session_workspaces` / `workspace_panes` to new
  workspace layout tables.
- Daemon rejects `spawn_session` without `workspace_id` under the new protocol.
- CLI creation path creates/registers a workspace before spawning its agent
  session.
- Daemon creates workspace + first agent layout for app-created workspace.
- Workspace split/focus/close commands persist and broadcast layout updates.
- Workspace unregister closes member agent sessions and shell runtimes.
- Remote hub forwards workspace layouts keyed by workspace id.
- Tauri renders sidebar from workspaces and does not show hidden shell utility
  sessions as sidebar entries.
- Tauri waits for daemon events before reflecting layout mutation.

## Manual Verification

Use the dev install, not prod:

```bash
make dev
eval "$(./attn profile-env dev)"
```

Verify:

- Fresh install: create a workspace, see it in sidebar, type into agent.
- Split terminal, switch panes, close pane, restart Tauri, layout returns.
- Close workspace, confirm all member PTYs disappear and no orphan shell
  panes remain.
- Upgrade path: seed an old DB with session layout rows, launch new daemon,
  confirm workspace appears with the same split layout.
- CLI spawn: confirm the CLI creates/registers a workspace first, then spawns
  the session with that workspace id.
- Protocol rejection: send `spawn_session` without `workspace_id` and confirm
  the daemon rejects it without creating a session, PTY, or workspace.
- Remote endpoint session still appears under the correct workspace and can be
  attached/resized.

## Open Follow-Up

The PR should prepare multi-agent workspace support, but it does not need to
ship the full UX for adding a second agent. After this lands, the next small
PR can add an explicit "add agent to workspace" command and UI affordance.
