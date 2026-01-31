# Session Forking Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable forking Claude Code sessions from the attn dashboard to explore different approaches while preserving conversation context.

**Architecture:** Add `--session-id` to claude invocations so attn controls the session ID. Extend PTY spawn to accept fork parameters (`resumeSessionId`, `forkSession`). Create ForkDialog component triggered by `Cmd+Shift+F`.

**Tech Stack:** Go (CLI), Rust (PTY manager), React/TypeScript (frontend), xterm.js

---

## Task 1: Change Session ID to UUID Format

The `--session-id` flag requires a valid UUID. Currently `wrapper.GenerateSessionID()` generates hex-encoded random bytes.

**Files:**
- Modify: `internal/wrapper/wrapper.go:13-17`

**Step 1: Update GenerateSessionID to return UUID**

```go
package wrapper

import (
	"github.com/google/uuid"
	// ... existing imports
)

// GenerateSessionID generates a UUID for use as session ID
func GenerateSessionID() string {
	return uuid.New().String()
}
```

**Step 2: Add uuid dependency**

Run: `go get github.com/google/uuid`

**Step 3: Verify it compiles**

Run: `go build ./...`
Expected: Success

**Step 4: Commit**

```bash
git add internal/wrapper/wrapper.go go.mod go.sum
git commit -m "refactor: use UUID for session IDs

Required for claude --session-id flag compatibility"
```

---

## Task 2: Pass --session-id to Claude

**Files:**
- Modify: `cmd/attn/main.go:212-215`

**Step 1: Add --session-id to claude command**

Find this code in `runClaudeDirectly()`:
```go
// Build claude command
claudeCmd := []string{"--settings", hooksPath}
claudeCmd = append(claudeCmd, claudeArgs...)
```

Replace with:
```go
// Build claude command
claudeCmd := []string{"--session-id", sessionID, "--settings", hooksPath}
claudeCmd = append(claudeCmd, claudeArgs...)
```

**Step 2: Verify it compiles**

Run: `go build ./...`
Expected: Success

**Step 3: Test manually**

Run: `make install && ATTN_INSIDE_APP=1 attn`
Expected: Claude starts successfully with controlled session ID

**Step 4: Commit**

```bash
git add cmd/attn/main.go
git commit -m "feat: pass --session-id to claude

Enables session forking by controlling Claude's session ID"
```

---

## Task 3: Extend PTY Spawn for Fork Parameters

**Files:**
- Modify: `app/src-tauri/src/pty_manager.rs:170-216`

**Step 1: Add fork parameters to pty_spawn**

Update function signature:
```rust
#[tauri::command]
pub async fn pty_spawn(
    state: State<'_, PtyState>,
    app: AppHandle,
    id: String,
    cwd: String,
    cols: u16,
    rows: u16,
    shell: Option<bool>,
    resume_session_id: Option<String>,  // NEW
    fork_session: Option<bool>,          // NEW
) -> Result<u32, String> {
```

**Step 2: Build fork flags into command**

Replace the attn command building section (around line 203-216):
```rust
    let mut cmd = if is_shell {
        // Plain shell for utility terminals
        let mut cmd = CommandBuilder::new(&login_shell);
        cmd.arg("-l");
        cmd
    } else {
        // Claude Code with hooks via attn wrapper
        let attn_path = dirs::home_dir()
            .map(|h| h.join(".local/bin/attn"))
            .filter(|p| p.exists())
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|| "attn".to_string());

        // Build fork flags if provided
        let fork_flags = match (&resume_session_id, fork_session.unwrap_or(false)) {
            (Some(resume_id), true) => format!(" --resume {} --fork", resume_id),
            (Some(resume_id), false) => format!(" --resume {}", resume_id),
            _ => String::new(),
        };

        let mut cmd = CommandBuilder::new(&login_shell);
        cmd.arg("-l");
        cmd.arg("-c");
        cmd.arg(format!("ATTN_INSIDE_APP=1 exec {attn_path}{fork_flags}"));
        cmd
    };
```

**Step 3: Verify it compiles**

Run: `cd app && cargo build`
Expected: Success

**Step 4: Commit**

```bash
git add app/src-tauri/src/pty_manager.rs
git commit -m "feat(pty): add fork parameters to pty_spawn

Accepts resume_session_id and fork_session for session forking"
```

---

## Task 4: Handle Fork Flags in CLI

**Files:**
- Modify: `cmd/attn/main.go:144-252`

**Step 1: Parse --resume and --fork flags**

Update `runClaudeDirectly()` to parse these flags:
```go
func runClaudeDirectly() {
	// Parse flags
	fs := flag.NewFlagSet("attn", flag.ContinueOnError)
	labelFlag := fs.String("s", "", "session label")
	resumeFlag := fs.String("resume", "", "session ID to resume from")
	forkFlag := fs.Bool("fork", false, "fork the resumed session")

	// Find where our flags end and claude flags begin
	var attnArgs []string
	var claudeArgs []string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-s" && i+1 < len(args) {
			attnArgs = append(attnArgs, arg, args[i+1])
			i++
		} else if arg == "--resume" && i+1 < len(args) {
			attnArgs = append(attnArgs, arg, args[i+1])
			i++
		} else if arg == "--fork" {
			attnArgs = append(attnArgs, arg)
		} else if arg == "--" {
			claudeArgs = append(claudeArgs, args[i+1:]...)
			break
		} else {
			claudeArgs = append(claudeArgs, arg)
		}
	}

	fs.Parse(attnArgs)
	// ... rest of function
```

**Step 2: Build claude command with fork flags**

Update the claude command building:
```go
	// Build claude command
	claudeCmd := []string{"--session-id", sessionID, "--settings", hooksPath}

	// Add fork flags if resuming
	if *resumeFlag != "" {
		claudeCmd = append(claudeCmd, "--resume", *resumeFlag, "--fork-session")
	}

	claudeCmd = append(claudeCmd, claudeArgs...)
```

**Step 3: Verify it compiles**

Run: `go build ./...`
Expected: Success

**Step 4: Commit**

```bash
git add cmd/attn/main.go
git commit -m "feat(cli): handle --resume and --fork flags

Passes through to claude for session forking"
```

---

## Task 5: Create ForkDialog Component

**Files:**
- Create: `app/src/components/ForkDialog.tsx`
- Create: `app/src/components/ForkDialog.css`

**Step 1: Create ForkDialog.tsx**

```tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import './ForkDialog.css';

interface ForkDialogProps {
  isOpen: boolean;
  sessionLabel: string;
  sessionId: string;
  onClose: () => void;
  onFork: (name: string, createWorktree: boolean) => void;
}

export function ForkDialog({
  isOpen,
  sessionLabel,
  sessionId,
  onClose,
  onFork,
}: ForkDialogProps) {
  const [name, setName] = useState('');
  const [createWorktree, setCreateWorktree] = useState(true);
  const [isLoading, setIsLoading] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // Generate default name when dialog opens
  useEffect(() => {
    if (isOpen) {
      setName(`${sessionLabel}-fork-1`);
      setCreateWorktree(true);
      setIsLoading(false);
      // Focus and select input after render
      setTimeout(() => {
        inputRef.current?.focus();
        inputRef.current?.select();
      }, 0);
    }
  }, [isOpen, sessionLabel]);

  const handleSubmit = useCallback(() => {
    if (!name.trim() || isLoading) return;
    setIsLoading(true);
    onFork(name.trim(), createWorktree);
  }, [name, createWorktree, isLoading, onFork]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      handleSubmit();
    }
  }, [onClose, handleSubmit]);

  if (!isOpen) return null;

  return (
    <div className="fork-dialog-overlay" onClick={onClose}>
      <div
        className="fork-dialog"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <div className="fork-dialog-header">
          <h3>Fork Session</h3>
        </div>
        <div className="fork-dialog-body">
          <div className="fork-field">
            <label htmlFor="fork-name">Name</label>
            <input
              ref={inputRef}
              id="fork-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={isLoading}
            />
          </div>
          <div className="fork-field fork-checkbox">
            <input
              id="fork-worktree"
              type="checkbox"
              checked={createWorktree}
              onChange={(e) => setCreateWorktree(e.target.checked)}
              disabled={isLoading}
            />
            <label htmlFor="fork-worktree">Create git worktree</label>
          </div>
        </div>
        <div className="fork-dialog-footer">
          <span className="fork-shortcuts">
            <kbd>Enter</kbd> confirm · <kbd>Esc</kbd> cancel
          </span>
          <button
            className="fork-confirm-btn"
            onClick={handleSubmit}
            disabled={!name.trim() || isLoading}
          >
            {isLoading ? 'Creating...' : 'Fork'}
          </button>
        </div>
      </div>
    </div>
  );
}
```

**Step 2: Create ForkDialog.css**

```css
.fork-dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.fork-dialog {
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  width: 400px;
  max-width: 90vw;
}

.fork-dialog-header {
  padding: 16px;
  border-bottom: 1px solid var(--border-color);
}

.fork-dialog-header h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.fork-dialog-body {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.fork-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.fork-field label {
  font-size: 13px;
  color: var(--text-secondary);
}

.fork-field input[type="text"] {
  padding: 8px 12px;
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  color: var(--text-primary);
  font-size: 14px;
}

.fork-field input[type="text"]:focus {
  outline: none;
  border-color: var(--accent-color);
}

.fork-checkbox {
  flex-direction: row;
  align-items: center;
  gap: 8px;
}

.fork-checkbox input[type="checkbox"] {
  width: 16px;
  height: 16px;
}

.fork-dialog-footer {
  padding: 12px 16px;
  border-top: 1px solid var(--border-color);
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.fork-shortcuts {
  font-size: 12px;
  color: var(--text-tertiary);
}

.fork-shortcuts kbd {
  background: var(--bg-primary);
  padding: 2px 6px;
  border-radius: 3px;
  font-family: inherit;
}

.fork-confirm-btn {
  padding: 8px 16px;
  background: var(--accent-color);
  color: white;
  border: none;
  border-radius: 4px;
  font-size: 14px;
  cursor: pointer;
}

.fork-confirm-btn:hover:not(:disabled) {
  opacity: 0.9;
}

.fork-confirm-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
```

**Step 3: Verify components render**

Run: `cd app && pnpm run build`
Expected: Success (type check passes)

**Step 4: Commit**

```bash
git add app/src/components/ForkDialog.tsx app/src/components/ForkDialog.css
git commit -m "feat(ui): add ForkDialog component

Keyboard-first dialog for forking sessions"
```

---

## Task 6: Update Session Store for Fork Support

**Files:**
- Modify: `app/src/store/sessions.ts:129-146` and `180-237`

**Step 1: Add forkSession method to store**

Add after `createSession`:
```typescript
  forkSession: async (
    originalSessionId: string,
    label: string,
    cwd: string,
    resumeSessionId: string
  ) => Promise<string>;
```

**Step 2: Implement forkSession**

Add implementation after `createSession` implementation:
```typescript
  forkSession: async (
    originalSessionId: string,
    label: string,
    cwd: string,
    resumeSessionId: string
  ) => {
    const id = `session-${++sessionCounter}`;
    const session: Session = {
      id,
      label,
      state: 'working',
      terminal: null,
      cwd,
      terminalPanel: createDefaultPanelState(),
    };

    set((state) => ({
      sessions: [...state.sessions, session],
      activeSessionId: id,
    }));

    return id;
  },
```

**Step 3: Update connectTerminal to accept fork params**

Modify `connectTerminal` signature and invoke call:
```typescript
  connectTerminal: async (
    id: string,
    terminal: Terminal,
    resumeSessionId?: string,
    forkSession?: boolean
  ) => Promise<void>;
```

Update the invoke call:
```typescript
      await invoke('pty_spawn', {
        id,
        cwd: session.cwd,
        cols,
        rows,
        resumeSessionId,
        forkSession,
      });
```

**Step 4: Verify it compiles**

Run: `cd app && pnpm run build`
Expected: Success

**Step 5: Commit**

```bash
git add app/src/store/sessions.ts
git commit -m "feat(store): add forkSession support

Enables creating forked sessions with resume parameters"
```

---

## Task 7: Add Keyboard Shortcut for Fork

**Files:**
- Modify: `app/src/hooks/useKeyboardShortcuts.ts`

**Step 1: Add onForkSession handler**

Find the handlers interface and add:
```typescript
  onForkSession?: () => void;
```

**Step 2: Add keyboard handler**

Add in the keydown handler:
```typescript
    // Cmd+Shift+F - Fork session
    if (e.metaKey && e.shiftKey && e.key === 'f') {
      e.preventDefault();
      handlers.onForkSession?.();
      return;
    }
```

**Step 3: Commit**

```bash
git add app/src/hooks/useKeyboardShortcuts.ts
git commit -m "feat(shortcuts): add Cmd+Shift+F for fork session"
```

---

## Task 8: Wire Up ForkDialog in App.tsx

**Files:**
- Modify: `app/src/App.tsx`

**Step 1: Import ForkDialog**

Add to imports:
```typescript
import { ForkDialog } from './components/ForkDialog';
```

**Step 2: Add fork dialog state**

Add after thumbs state (around line 299):
```typescript
  // Fork dialog state
  const [forkDialogOpen, setForkDialogOpen] = useState(false);
  const [forkTargetSession, setForkTargetSession] = useState<{
    id: string;
    label: string;
    cwd: string;
    daemonSessionId: string;
  } | null>(null);
```

**Step 3: Add fork handler**

Add after thumbs handlers:
```typescript
  // Fork session handler
  const handleOpenForkDialog = useCallback(() => {
    if (!activeSessionId) return;
    const localSession = sessions.find((s) => s.id === activeSessionId);
    if (!localSession) return;
    const daemonSession = daemonSessions.find((ds) => ds.directory === localSession.cwd);
    if (!daemonSession) return;

    setForkTargetSession({
      id: localSession.id,
      label: localSession.label,
      cwd: localSession.cwd,
      daemonSessionId: daemonSession.id,
    });
    setForkDialogOpen(true);
  }, [activeSessionId, sessions, daemonSessions]);

  const handleForkConfirm = useCallback(async (name: string, createWorktree: boolean) => {
    if (!forkTargetSession) return;

    try {
      let targetCwd = forkTargetSession.cwd;

      // Create worktree if requested
      if (createWorktree) {
        const branchName = `fork/${name}`;
        const result = await sendCreateWorktree(
          forkTargetSession.cwd,
          branchName
        );
        if (!result.success) {
          console.error('[App] Failed to create worktree:', result.error);
          setForkDialogOpen(false);
          return;
        }
        targetCwd = result.path!;
      }

      // Create the forked session
      const sessionId = await createSession(name, targetCwd);

      // Connect terminal with fork parameters
      // The terminal will be connected via onReady callback
      // Store fork params temporarily to use when connecting
      // TODO: Need to pass resumeSessionId to connectTerminal

      setForkDialogOpen(false);
      setForkTargetSession(null);

      // Fit terminal after view becomes visible
      setTimeout(() => {
        const handle = terminalRefs.current.get(sessionId);
        handle?.fit();
        handle?.focus();
      }, 100);
    } catch (err) {
      console.error('[App] Fork failed:', err);
      setForkDialogOpen(false);
    }
  }, [forkTargetSession, sendCreateWorktree, createSession]);

  const handleForkClose = useCallback(() => {
    setForkDialogOpen(false);
    setForkTargetSession(null);
  }, []);
```

**Step 4: Add to keyboard shortcuts**

Update useKeyboardShortcuts call:
```typescript
    onForkSession: view === 'session' ? handleOpenForkDialog : undefined,
```

**Step 5: Add ForkDialog to JSX**

Add before closing `</DaemonProvider>`:
```tsx
      <ForkDialog
        isOpen={forkDialogOpen}
        sessionLabel={forkTargetSession?.label || ''}
        sessionId={forkTargetSession?.daemonSessionId || ''}
        onClose={handleForkClose}
        onFork={handleForkConfirm}
      />
```

**Step 6: Update enabled condition for shortcuts**

Update the enabled prop:
```typescript
    enabled: !locationPickerOpen && !branchPickerOpen && !thumbsOpen && !forkDialogOpen,
```

**Step 7: Verify it compiles**

Run: `cd app && pnpm run build`
Expected: Success

**Step 8: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat(ui): wire up ForkDialog in App

Cmd+Shift+F opens fork dialog for active session"
```

---

## Task 9: Pass Fork Parameters Through Terminal Connection

This task connects the fork parameters from App.tsx through to the PTY spawn.

**Files:**
- Modify: `app/src/store/sessions.ts`
- Modify: `app/src/App.tsx`

**Step 1: Store pending fork params**

Add to sessions.ts after pendingConnections:
```typescript
const pendingForkParams = new Map<string, { resumeSessionId: string; forkSession: boolean }>();
```

**Step 2: Add setForkParams method**

Add to store interface and implementation:
```typescript
  setForkParams: (sessionId: string, resumeSessionId: string) => void;
```

```typescript
  setForkParams: (sessionId: string, resumeSessionId: string) => {
    pendingForkParams.set(sessionId, { resumeSessionId, forkSession: true });
  },
```

**Step 3: Use fork params in connectTerminal**

Update connectTerminal to check for pending fork params:
```typescript
      // Check for pending fork params
      const forkParams = pendingForkParams.get(id);
      pendingForkParams.delete(id);

      await invoke('pty_spawn', {
        id,
        cwd: session.cwd,
        cols,
        rows,
        resumeSessionId: forkParams?.resumeSessionId,
        forkSession: forkParams?.forkSession,
      });
```

**Step 4: Use setForkParams in App.tsx handleForkConfirm**

Update handleForkConfirm:
```typescript
      // Create the forked session
      const sessionId = await createSession(name, targetCwd);

      // Set fork params before terminal connects
      setForkParams(sessionId, forkTargetSession.daemonSessionId);
```

Add setForkParams to destructuring:
```typescript
  const {
    // ... existing
    setForkParams,
  } = useSessionStore();
```

**Step 5: Verify it compiles**

Run: `cd app && pnpm run build`
Expected: Success

**Step 6: Test fork flow manually**

1. Start a session
2. Press Cmd+Shift+F
3. Enter a name, confirm
4. Verify forked session starts with conversation context

**Step 7: Commit**

```bash
git add app/src/store/sessions.ts app/src/App.tsx
git commit -m "feat: pass fork parameters through terminal connection

Completes the fork flow from dialog to PTY spawn"
```

---

## Task 10: Final Integration Test

**Step 1: Build and install everything**

```bash
make install
cd app && pnpm run build
```

**Step 2: Test the complete flow**

1. Start attn app
2. Create a new session
3. Have a brief conversation with Claude
4. Press Cmd+Shift+F
5. Keep default name, confirm
6. Verify:
   - New session appears in sidebar
   - Claude remembers the conversation
   - Original session still works

**Step 3: Test worktree creation**

1. Fork a session with "Create git worktree" checked
2. Verify worktree is created
3. Verify forked session runs in worktree directory

**Step 4: Final commit**

```bash
git add -A
git commit -m "feat: session forking complete

- UUID session IDs for claude --session-id compatibility
- PTY spawn accepts resume/fork parameters
- ForkDialog with keyboard-first UX
- Cmd+Shift+F shortcut
- Optional git worktree creation"
```

---

## Summary

The implementation adds session forking in 10 tasks:

1. **UUID session IDs** - Change from hex to UUID for `--session-id` compatibility
2. **Pass --session-id to claude** - Control Claude's session ID
3. **PTY fork parameters** - Rust side accepts resume_session_id and fork_session
4. **CLI fork flags** - Handle --resume and --fork in attn CLI
5. **ForkDialog component** - UI for configuring fork
6. **Store fork support** - Session store methods for forking
7. **Keyboard shortcut** - Cmd+Shift+F handler
8. **Wire up App.tsx** - Connect dialog to handlers
9. **Fork params flow** - Pass params through terminal connection
10. **Integration test** - Verify complete flow
