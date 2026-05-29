# Workspace-First Sidebar Sketch

## Target

The sidebar should make workspaces the top-level navigation unit. Selecting a
workspace shows the whole workspace: every child session is rendered as a pane
in the workspace layout. Selecting a child session focuses that pane without
leaving the workspace.

```text
Sessions

> fish                         # Workspace row, selected
  ~/repo

> attn                         # Workspace row
  ~/projects/victor/attn
  - exit          main         # Session row, focused
  - attn          main
  - fish          master

> victor                       # Workspace row
  ~
  - victor        shell
```

## Behavior

- `Cmd+1..9` selects workspaces by sidebar order, not sessions.
- Selecting a workspace focuses its remembered child session, or its first
  session when there is no remembered focus.
- Creating a workspace always creates the initial session in the same flow.
- Creating a session inside a workspace uses the workspace directory as cwd.
- Closing a child session removes that pane from the workspace layout.
- Closing the last session in a workspace closes the workspace.
- Shell panes become full sessions with `agent = "shell"`.

## Implementation Notes

- The prep PR keeps the current session-first UI running while adding
  workspace view models and selection helpers.
- The implementation PR should make `activeWorkspaceId` primary and treat the
  active session as the focused pane within the active workspace.
- The workspace layout leaf should identify a session, not an anonymous
  terminal runtime.
