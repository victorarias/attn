# Plan: Chief of Staff

## Goal

Let an attn-managed agent delegate a brief to another agent without abandoning
its current task. Build workspace context and one profile-scoped chief-of-staff
session on top of that generic delegation primitive.

## Architecture Map

```text
Current:
React session creation
  -> register workspace / add pane / optional worktree
    -> spawn_session websocket command
      -> daemon PTY backend
        -> agent driver

Phase 1 target:
attn delegate (inside an attn-managed agent)
  -> unix-socket DelegateMessage
    -> daemon delegation coordinator
      -> resolve source session + target placement
      -> optional worktree / workspace + pane
      -> spawn session with initial brief
      -> return session/workspace/directory/branch

Phase 2 target:
attn workspace context checkout/update/status
  -> per-session working copy under config.DataDir()
    -> revision-checked daemon update
      -> canonical SQLite workspace context
      -> workspace_context_changed event

Phase 3 target:
one session per profile marked chief_of_staff
  -> delegates through the Phase 1 coordinator
    -> tracked dispatch records
      -> status/report queries and UI
```

## Data Model / Interfaces

```text
DelegateRequest {
  source_session_id
  brief
  agent?
  placement: current_workspace | existing_workspace | new_workspace
  workspace_id?
  cwd?
  worktree?: { repo, branch, start_point? }
  label?
  yolo_mode?
}

DelegateResult {
  session_id
  workspace_id
  directory
  branch?
}

WorkspaceContext {
  workspace_id
  content
  revision
  updated_by_session_id
  updated_at
}

ChiefOfStaff {
  session_id // unique per attn profile
}
```

## Boundaries

- The daemon owns delegation as one operation, including rollback.
- Agent drivers translate an initial brief into the selected CLI's launch
  arguments. The daemon must not type the brief into a live PTY.
- Ordinary delegation creates no durable parent/child relationship.
- Workspace context belongs to the workspace and is independent of delegation.
- The chief-of-staff role belongs to a session; its dispatch records survive
  independently of workspace context.

## Implementation Steps

- [x] Add initial-prompt support to Codex, Claude, and plugin launch contracts.
- [x] Add `attn delegate` for the current workspace and current directory.
- [x] Move current-workspace pane creation plus spawn behind a daemon-owned
      operation with rollback.
- [x] Add new-workspace and explicit existing-workspace placement.
- [x] Add optional worktree creation with repo, branch, path, and start-point
      options.
- [x] Allow every configured built-in or plugin agent that declares initial
      prompt support.
- [x] Manually verify real agents delegating in attn-dev: Codex to Claude in a
      new workspace and default worktree, plus Claude to Codex at a custom
      directory and explicit worktree path.
- [ ] Reuse the daemon operation from the Tauri app to remove duplicate React
      orchestration.
- [x] Add workspace-context checkout/update/status with revision conflicts.
- [x] Emit `workspace_context_changed` and keep context-bearing workspaces alive.
- [ ] Add the unique profile-scoped chief-of-staff session role.
- [ ] Add tracked dispatch status/reporting and UI visualization.

## Decisions

- Use `delegate`; the source agent continues working, so this is not a handover.
- The delegated task text is a brief and does not create lineage by itself.
- Workspace context uses per-session working copies under the profile data
  directory; SQLite remains canonical.
- Context updates require the checked-out revision to prevent lost updates.
- At most one session per attn profile can hold the chief-of-staff role.
- Placement defaults to the current workspace. `--new-workspace`, `--cwd`, and
  `--worktree` opt into a new workspace; `--workspace` targets an existing one.
- Copilot is excluded from delegation for now; ordinary Copilot sessions remain
  supported.

## Follow-ups

- Explore an agent-specific outside-in notification capability consuming
  `workspace_context_changed`; do not use generic PTY typing as the core model.
- Decide how remote endpoint delegation routes the daemon-owned transaction.
- Update plugin agents such as Snipe to declare and consume the
  `initial_prompt` driver capability before using them as delegation targets.
