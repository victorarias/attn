# Pending Approval State

## Problem

When using Claude Code without YOLO mode, Claude often proposes tools that need user approval. Currently this state isn't captured - the session shows as idle or waiting_input when really it's waiting for permission approval.

## Solution

Add a new `pending_approval` state with flashing yellow indicator.

## Design

### New State

Add `"pending_approval"` as a fourth session state alongside `working`, `waiting_input`, and `idle`.

### Hook Changes (`internal/hooks/hooks.go`)

1. **Add `PermissionRequest` hook** - fires when Claude needs tool approval:
   ```
   PermissionRequest(*) → state: "pending_approval"
   ```

2. **Add `PostToolUse(*)` wildcard** - fires after any tool completes, resets to working:
   ```
   PostToolUse(*) → state: "working"
   ```

### Protocol Changes

- Add `StatePendingApproval = "pending_approval"` constant
- Increment protocol version

### UI Changes (`app/src/`)

- Sidebar indicator: flashing yellow for `pending_approval`
- CSS animation on the state dot/badge

### State Flow

```
User submits prompt → "working"
Claude proposes tool needing approval → "pending_approval" (flashing yellow)
User approves → tool runs → "working"
Claude stops → classifier → "idle" or "waiting_input"
```

## Implementation Tasks

1. Add `StatePendingApproval` constant to `internal/protocol/constants.go`
2. Increment `ProtocolVersion`
3. Add `PermissionRequest(*)` hook in `internal/hooks/hooks.go`
4. Add `PostToolUse(*)` wildcard hook for state reset
5. Add flashing yellow CSS animation in `app/src/components/Sidebar.css`
6. Update Sidebar component to use flashing yellow for `pending_approval`
