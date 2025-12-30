# Session Forking Design

Fork Claude Code sessions from the attn dashboard to explore different approaches while preserving shared conversation context.

## Problem

When working with Claude, you often reach decision points where you want to explore multiple approaches:
- Mid-conversation: Claude suggests two approaches, you want to try both
- Before risky changes: Create a safe branch before attempting something
- After orientation: Once Claude understands the codebase, fork to tackle different aspects

Currently, you'd lose context by starting a new session, or have to manually manage conversation state.

## Solution

Add a "Fork Session" feature that:
1. Duplicates conversation context using `claude --resume <id> --fork-session`
2. Optionally creates a git worktree for file isolation
3. Lets both original and fork continue independently

## Key Decisions

### Single Session ID

Pass `--session-id <uuid>` to Claude at session start so attn's session ID equals Claude's session ID. This enables forking without needing to capture Claude's ID from hooks.

```go
// Before
claudeCmd := []string{"--settings", hooksPath}

// After
claudeCmd := []string{"--session-id", sessionID, "--settings", hooksPath}
```

### Fork Command

```bash
claude --resume <original-session-id> --fork-session --session-id <new-session-id>
```

- `--resume <original-session-id>` → Load conversation context from the original
- `--fork-session` → Create new session instead of modifying original
- `--session-id <new-session-id>` → Control the fork's ID for tracking

### Git Worktree Integration

When "Create git worktree" is checked (default):
1. Generate branch name: `fork/<label>-fork-N`
2. Create worktree via existing `create_worktree` command
3. Spawn forked Claude session in the worktree directory

When unchecked:
- Fork runs in same directory as original
- Useful for lightweight "what if" exploration without file isolation

### Display

Forks appear as regular sessions in flat list with naming convention:
- Original: `my-feature`
- Fork: `my-feature-fork-1`, `my-feature-fork-2`, etc.

## User Flow

1. Select session in sidebar
2. Press `Cmd+Shift+F` (or click fork button)
3. Fork dialog appears:
   - Name field (auto-populated, e.g., `my-feature-fork-1`)
   - "Create git worktree" checkbox (default: checked)
4. Keyboard navigation:
   - `Tab` / `Shift+Tab`: Move between name → checkbox → confirm
   - `Space`: Toggle checkbox when focused
   - `Enter`: Confirm and fork (from any field)
   - `Esc`: Cancel and dismiss
5. On confirm:
   - If worktree: create worktree first, show loading spinner
   - Generate new session ID (UUID)
   - Spawn new terminal with fork command
   - Register new session with daemon
6. Both sessions now operate independently

## Error Handling

| Scenario | Handling |
|----------|----------|
| Original session not found | Error toast: "Session no longer exists" |
| Worktree creation fails | Show error in dialog, don't dismiss, let user retry or uncheck worktree |
| Claude `--resume` fails | Error toast: "Could not resume session context" |
| Fork name conflicts | Auto-increment suffix (`-fork-2`, `-fork-3`) |

## Required Changes

### Backend

1. **`cmd/attn/main.go`**: Add `--session-id sessionID` to claude command args

### Frontend

2. **Fork dialog component**: Name field, worktree checkbox, keyboard handling
3. **Keyboard shortcut**: `Cmd+Shift+F` to open fork dialog
4. **PTY spawn logic**: Handle fork with resume flags and optional worktree directory

### Protocol (if needed)

5. **Fork session command**: May not need new protocol - can reuse existing spawn/worktree commands

## Non-Goals

- Provider abstraction (Claude-specific for now)
- Tree visualization of fork relationships
- Merging forked sessions back together
