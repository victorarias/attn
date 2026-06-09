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

ChiefOfStaffDispatch {
  id
  chief_session_id
  session_id
  workspace_id
  brief
  label
  agent
  directory
  branch?
  latest_report?
  reported_at?
  created_at
  updated_at

  // projected from the target session when read
  status
  status_since
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
- Dispatch status is projected from the target session. The dispatch store owns
  relationship metadata and reports, not a second session-state machine.
- Structured coordination is intentionally latest-report state, not a report
  history or customizable workflow engine.
- Chief responses are delivered through dispatch persistence and
  `attn dispatch status`; attn does not type arbitrary text into a live agent
  PTY because that is not a safe prompt-delivery primitive.
- Attn delegation is reserved for visible, full interactive agents the user
  wants to inspect and steer. Chiefs use native subagents for internal
  research, adversarial analysis, verification, and parallel reasoning, and
  default to native subagents when intent is unclear.

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
- [x] Inject workspace-context management guidance through supported agent
      session-start hooks without embedding a stale context snapshot.
- [x] Add the unique profile-scoped chief-of-staff session role.
- [x] Persist delegations initiated by the current chief as tracked dispatches.
- [x] Add `attn dispatch list` for coordinators and `attn dispatch report` for
      delegated agents.
- [x] Broadcast dispatch snapshots and visualize them on the dashboard with
      status, latest report, and session navigation.
- [x] Teach chief and delegated agents when and how to list/report dispatches.
- [x] Separate projected runtime status from a narrow delegated-work state.
- [x] Persist one structured latest-report envelope alongside the narrative.
- [x] Support one durable decision request and chief resolution per dispatch.
- [x] Derive actionable summaries and artifact-current verification for CLI/API/UI.
- [x] Clarify the boundary between user-visible attn delegation and internal
      native subagents in the bundled skill and protect it with focused tests.

## Verification

- A packaged `attn-dev` Codex chief delegated a changelog inspection to Claude
  in a new workspace without a worktree.
- The Claude worker submitted `LIVE_DISPATCH_OK` through `attn dispatch report`;
  `attn dispatch list` returned the report and projected `idle` session state.
- The dashboard showed the current chief, target agent, live state, and report,
  with navigation to the delegated session.

## Decisions

- Use `delegate`; the source agent continues working, so this is not a handover.
- The delegated task text is a brief and does not create lineage by itself.
- Workspace context uses per-session working copies under the profile data
  directory; SQLite remains canonical.
- Context updates require the checked-out revision to prevent lost updates.
- At most one session per attn profile can hold the chief-of-staff role.
- Chief-role transfer does not rewrite dispatch ownership; records remain
  attached to the agent that created them and remain visible as history.
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
