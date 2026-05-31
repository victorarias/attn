# Daemon Protocol Test Policy

This policy applies to daemon and websocket protocol integration tests in this directory.

## Real-App Parity

- Daemon/protocol scenarios must model real app usage, not invented command sequences.
- When the app changes its workspace/session flow, update these tests to match that flow in the same PR.
- If these tests pass while users can reproduce app-level workspace/session errors, treat that as a test design bug.
- Do not preserve daemon compatibility paths just to keep old test flows passing.

## Workspace Sessions

- Workspace session lifecycle tests should use the same command ordering as the app:
  - register the workspace
  - add a session pane to the workspace layout
  - spawn the session runtime
  - close panes through workspace layout commands
- Session panes should be daemon-owned lifecycle entities with explicit status transitions such as `spawning`, `ready`, and `failed`.
- Tests should assert observable protocol state, not only internal store details, when the behavior is user-facing.
