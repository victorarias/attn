# Real App Harness Policy

This policy applies to packaged-app scenarios in this directory.

## Real-App Parity

- Scenarios must match real app usage. Do not invent command sequences that the app cannot perform.
- If workspace/session product behavior changes, update these scenarios in the same PR.
- If these scenarios pass while users can reproduce workspace/session errors in the packaged app, treat that as a test design bug.

## Workspace Sessions

- A visible pane is a session pane. Do not model durable non-session terminals.
- Resolve pane IDs from daemon/app state. Do not hardcode legacy pane IDs such as `main` for new scenarios.
- Empty workspaces are invalid user-visible state. Tests that create or observe one should assert it is removed or hidden.
- Shortcut scenarios should exercise the documented app shortcuts or the same shortcut registry IDs used by the app.
