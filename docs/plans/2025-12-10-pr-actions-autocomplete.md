# PR Actions, Filesystem Autocomplete & Sidebar Shortcut Implementation Plan

**Status:** Pending

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable users to act on PRs (approve, merge, mute) directly from the app, add filesystem autocomplete to the location picker, and show the ⌘B sidebar shortcut in the UI.

**Architecture:** PR actions route through Go daemon (extends existing `gh` CLI integration). Filesystem autocomplete uses Tauri Rust commands to read directories. Mute state stored in frontend localStorage. All UI updates follow existing component patterns.

**Tech Stack:** Go (daemon), Rust (Tauri commands), TypeScript/React (frontend), Zustand (state), CSS

---

## Phase 1: Sidebar Shortcut (Quick Win)

### Task 1: Add ⌘B Shortcut Hint to Sidebar Footer

**Files:**
- Modify: `app/src/components/Sidebar.tsx`

**Step 1: Add shortcut hint to expanded sidebar footer**

In `app/src/components/Sidebar.tsx`, update the sidebar footer section:

```typescript
// Find this section (around line 100):
<div className="sidebar-footer">
  <span className="shortcut-hint">⌘K drawer</span>
</div>

// Replace with:
<div className="sidebar-footer">
  <span className="shortcut-hint">⌘K drawer</span>
  <span className="shortcut-hint">⌘B sidebar</span>
</div>
```

**Step 2: Verify the change**

Run: `cd app && pnpm tauri dev`
Expected: Sidebar footer shows both "⌘K drawer" and "⌘B sidebar"

**Step 3: Commit**

```bash
git add app/src/components/Sidebar.tsx
git commit -m "feat(app): add ⌘B sidebar shortcut hint to footer"
```

---

## Phase 2: Filesystem Autocomplete

### Task 2: Add Tauri Command for Directory Listing

**Files:**
- Modify: `app/src-tauri/src/lib.rs`

**Step 1: Add directory listing command**

Add this function to `app/src-tauri/src/lib.rs` before the `run()` function:

```rust
#[tauri::command]
async fn list_directory(path: String) -> Result<Vec<String>, String> {
    use std::fs;
    use std::path::Path;

    let dir_path = if path.starts_with('~') {
        let home = dirs::home_dir().ok_or("Cannot get home directory")?;
        home.join(&path[2..]) // Skip "~/"
    } else {
        Path::new(&path).to_path_buf()
    };

    let entries = fs::read_dir(&dir_path)
        .map_err(|e| format!("Cannot read directory: {}", e))?;

    let mut directories: Vec<String> = entries
        .filter_map(|entry| {
            let entry = entry.ok()?;
            let metadata = entry.metadata().ok()?;
            if metadata.is_dir() {
                Some(entry.file_name().to_string_lossy().to_string())
            } else {
                None
            }
        })
        .collect();

    directories.sort();
    directories.truncate(50); // Limit to 50 results

    Ok(directories)
}
```

**Step 2: Register the command in invoke_handler**

Find the `invoke_handler` line and add `list_directory`:

```rust
.invoke_handler(tauri::generate_handler![
    greet,
    pty_bridge::pty_connect,
    pty_bridge::pty_spawn,
    pty_bridge::pty_write,
    pty_bridge::pty_resize,
    pty_bridge::pty_kill,
    list_directory,  // Add this line
])
```

**Step 3: Verify Rust compiles**

Run: `cd app/src-tauri && cargo check`
Expected: Compilation succeeds

**Step 4: Commit**

```bash
git add app/src-tauri/src/lib.rs
git commit -m "feat(tauri): add list_directory command for filesystem autocomplete"
```

---

### Task 3: Create useFilesystemSuggestions Hook

**Files:**
- Create: `app/src/hooks/useFilesystemSuggestions.ts`

**Step 1: Create the hook**

```typescript
// app/src/hooks/useFilesystemSuggestions.ts
import { useState, useEffect, useCallback, useRef } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { homeDir } from '@tauri-apps/api/path';

interface FilesystemSuggestion {
  name: string;
  path: string;
}

interface UseFilesystemSuggestionsResult {
  suggestions: FilesystemSuggestion[];
  loading: boolean;
  error: string | null;
  currentDir: string;
}

export function useFilesystemSuggestions(inputPath: string): UseFilesystemSuggestionsResult {
  const [suggestions, setSuggestions] = useState<FilesystemSuggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [currentDir, setCurrentDir] = useState('');
  const [homePath, setHomePath] = useState('/Users');
  const debounceRef = useRef<number | null>(null);

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setHomePath(dir.replace(/\/$/, ''));
    }).catch(() => {});
  }, []);

  const fetchSuggestions = useCallback(async (path: string) => {
    if (!path || path.length < 1) {
      setSuggestions([]);
      setCurrentDir('');
      return;
    }

    // Parse input to determine directory to query
    let dirToQuery: string;
    let prefix: string;

    // Expand ~ to home directory
    const expandedPath = path.startsWith('~')
      ? path.replace('~', homePath)
      : path;

    if (expandedPath.endsWith('/')) {
      // User typed "/Users/" - query that directory
      dirToQuery = expandedPath;
      prefix = '';
    } else {
      // User typed "/Users/jo" - query parent, filter by "jo"
      const lastSlash = expandedPath.lastIndexOf('/');
      if (lastSlash === -1) {
        setSuggestions([]);
        return;
      }
      dirToQuery = expandedPath.slice(0, lastSlash + 1) || '/';
      prefix = expandedPath.slice(lastSlash + 1).toLowerCase();
    }

    setCurrentDir(dirToQuery.replace(homePath, '~'));
    setLoading(true);
    setError(null);

    try {
      const dirs = await invoke<string[]>('list_directory', { path: dirToQuery });

      // Filter by prefix if present
      const filtered = prefix
        ? dirs.filter(d => d.toLowerCase().startsWith(prefix))
        : dirs;

      setSuggestions(filtered.map(name => ({
        name,
        path: dirToQuery + name + '/',
      })));
    } catch (e) {
      setError(String(e));
      setSuggestions([]);
    } finally {
      setLoading(false);
    }
  }, [homePath]);

  // Debounced fetch
  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
    }

    debounceRef.current = window.setTimeout(() => {
      fetchSuggestions(inputPath);
    }, 150);

    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
    };
  }, [inputPath, fetchSuggestions]);

  return { suggestions, loading, error, currentDir };
}
```

**Step 2: Commit**

```bash
git add app/src/hooks/useFilesystemSuggestions.ts
git commit -m "feat(app): add useFilesystemSuggestions hook for directory autocomplete"
```

---

### Task 4: Update LocationPicker with Filesystem Suggestions

**Files:**
- Modify: `app/src/components/LocationPicker.tsx`
- Modify: `app/src/components/LocationPicker.css`

**Step 1: Update LocationPicker.tsx**

Replace the entire file with:

```typescript
// app/src/components/LocationPicker.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import { homeDir } from '@tauri-apps/api/path';
import { useLocationHistory } from '../hooks/useLocationHistory';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import './LocationPicker.css';

interface LocationPickerProps {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (path: string) => void;
}

export function LocationPicker({ isOpen, onClose, onSelect }: LocationPickerProps) {
  const [inputValue, setInputValue] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [homePath, setHomePath] = useState('/Users');
  const inputRef = useRef<HTMLInputElement>(null);
  const { getRecentLocations, addToHistory } = useLocationHistory();
  const { suggestions: fsSuggestions, loading, currentDir } = useFilesystemSuggestions(inputValue);

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setHomePath(dir.replace(/\/$/, ''));
    }).catch(() => {});
  }, []);

  const recentLocations = getRecentLocations();

  // Filter recent locations based on input
  const filteredRecent = inputValue
    ? recentLocations.filter(
        (loc) =>
          loc.label.toLowerCase().includes(inputValue.toLowerCase()) ||
          loc.path.toLowerCase().includes(inputValue.toLowerCase())
      )
    : recentLocations;

  // Combine suggestions: filesystem first, then recent
  const allSuggestions = [
    ...fsSuggestions.map(s => ({ type: 'dir' as const, ...s })),
    ...filteredRecent.slice(0, 10).map(loc => ({
      type: 'recent' as const,
      name: loc.label,
      path: loc.path
    })),
  ];

  const totalSuggestions = allSuggestions.length;

  // Reset selection when suggestions change
  useEffect(() => {
    setSelectedIndex(0);
  }, [inputValue]);

  // Focus input when opened
  useEffect(() => {
    if (isOpen) {
      setInputValue('');
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  const handleSelect = useCallback(
    (path: string) => {
      addToHistory(path);
      onSelect(path);
      onClose();
    },
    [addToHistory, onSelect, onClose]
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }

      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) => Math.min(prev + 1, totalSuggestions - 1));
        return;
      }

      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
        return;
      }

      // Tab autocompletes the selected directory suggestion
      if (e.key === 'Tab' && fsSuggestions.length > 0) {
        e.preventDefault();
        const selected = allSuggestions[selectedIndex];
        if (selected && selected.type === 'dir') {
          setInputValue(selected.path.replace(homePath, '~'));
        }
        return;
      }

      if (e.key === 'Enter') {
        e.preventDefault();
        const selected = allSuggestions[selectedIndex];
        if (selected) {
          if (selected.type === 'dir') {
            // For directories, expand to input for further navigation or select
            const expanded = selected.path;
            // If user presses Enter on a dir, select it
            handleSelect(expanded);
          } else {
            handleSelect(selected.path);
          }
        } else if (inputValue.startsWith('/') || inputValue.startsWith('~')) {
          // Direct path input
          const path = inputValue.startsWith('~')
            ? inputValue.replace('~', homePath)
            : inputValue;
          handleSelect(path.replace(/\/$/, '')); // Remove trailing slash
        }
        return;
      }
    },
    [allSuggestions, selectedIndex, inputValue, handleSelect, onClose, homePath, fsSuggestions.length, totalSuggestions]
  );

  if (!isOpen) return null;

  return (
    <div className="location-picker-overlay" onClick={onClose}>
      <div className="location-picker" onClick={(e) => e.stopPropagation()}>
        <div className="picker-header">
          <div className="picker-title">New Session Location</div>
          <div className="picker-input-wrap">
            <input
              ref={inputRef}
              type="text"
              className="picker-input"
              placeholder="Type path (e.g., ~/projects) or search..."
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleKeyDown}
            />
            {loading && <div className="picker-loading" />}
          </div>
          {currentDir && (
            <div className="picker-breadcrumb">
              <span className="picker-breadcrumb-label">Browsing:</span>
              <span className="picker-breadcrumb-path">{currentDir}</span>
            </div>
          )}
        </div>

        <div className="picker-results">
          {/* Filesystem suggestions */}
          {fsSuggestions.length > 0 && (
            <div className="picker-section">
              <div className="picker-section-title">Directories</div>
              {fsSuggestions.map((item, index) => (
                <div
                  key={item.path}
                  className={`picker-item ${index === selectedIndex ? 'selected' : ''}`}
                  onClick={() => handleSelect(item.path)}
                  onMouseEnter={() => setSelectedIndex(index)}
                >
                  <div className="picker-icon">📁</div>
                  <div className="picker-info">
                    <div className="picker-name">{item.name}</div>
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Recent locations */}
          {filteredRecent.length > 0 && (
            <div className="picker-section">
              <div className="picker-section-title">Recent</div>
              {filteredRecent.slice(0, 10).map((loc, index) => {
                const globalIndex = fsSuggestions.length + index;
                return (
                  <div
                    key={loc.path}
                    className={`picker-item ${globalIndex === selectedIndex ? 'selected' : ''}`}
                    onClick={() => handleSelect(loc.path)}
                    onMouseEnter={() => setSelectedIndex(globalIndex)}
                  >
                    <div className="picker-icon">🕐</div>
                    <div className="picker-info">
                      <div className="picker-name">{loc.label}</div>
                      <div className="picker-path">{loc.path.replace(homePath, '~')}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          {/* Empty state */}
          {fsSuggestions.length === 0 && filteredRecent.length === 0 && (
            <div className="picker-empty">
              {inputValue
                ? 'No matches. Press Enter to use path directly.'
                : 'Type a path to browse directories'}
            </div>
          )}
        </div>

        <div className="picker-footer">
          <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
          <span className="shortcut"><kbd>Tab</kbd> autocomplete</span>
          <span className="shortcut"><kbd>Enter</kbd> select</span>
          <span className="shortcut"><kbd>Esc</kbd> cancel</span>
        </div>
      </div>
    </div>
  );
}
```

**Step 2: Add CSS for loading and breadcrumb**

Add to `app/src/components/LocationPicker.css`:

```css
/* Add after .picker-input::placeholder */

.picker-loading {
  position: absolute;
  right: 14px;
  top: 50%;
  transform: translateY(-50%);
  width: 12px;
  height: 12px;
  border: 2px solid #555;
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: translateY(-50%) rotate(360deg); }
}

.picker-breadcrumb {
  font-family: 'JetBrains Mono', monospace;
  font-size: 10px;
  color: #555;
  padding: 4px 14px 0;
}

.picker-breadcrumb-label {
  margin-right: 4px;
}

.picker-breadcrumb-path {
  color: #888;
}

/* Update picker-input-wrap to support loading indicator */
.picker-input-wrap {
  position: relative;
}
```

**Step 3: Verify changes work**

Run: `cd app && pnpm tauri dev`
Test:
- Open location picker (⌘N)
- Type `~/` - should show home directory contents
- Type `~/pro` - should filter to directories starting with "pro"
- Press Tab - should autocomplete
- Press Enter - should select

**Step 4: Commit**

```bash
git add app/src/components/LocationPicker.tsx app/src/components/LocationPicker.css
git commit -m "feat(app): add filesystem autocomplete to LocationPicker"
```

---

## Phase 3: PR Actions

### Task 5: Add PR Action Commands to Go Daemon

**Files:**
- Modify: `internal/github/github.go`

**Step 1: Add approve and merge functions**

Add these functions to `internal/github/github.go`:

```go
// ApprovePR approves a pull request
func (f *Fetcher) ApprovePR(repo string, number int) error {
	if !f.IsAvailable() {
		return fmt.Errorf("gh CLI not available")
	}

	cmd := exec.Command(f.ghPath, "pr", "review",
		"--repo", repo,
		"--approve",
		fmt.Sprintf("%d", number))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("approve failed: %s", string(output))
	}
	return nil
}

// MergePR merges a pull request
func (f *Fetcher) MergePR(repo string, number int, method string) error {
	if !f.IsAvailable() {
		return fmt.Errorf("gh CLI not available")
	}

	// Default to squash if not specified
	if method == "" {
		method = "squash"
	}

	cmd := exec.Command(f.ghPath, "pr", "merge",
		"--repo", repo,
		"--"+method, // --squash, --merge, or --rebase
		"--delete-branch",
		fmt.Sprintf("%d", number))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge failed: %s", string(output))
	}
	return nil
}
```

**Step 2: Verify Go compiles**

Run: `go build ./...`
Expected: Compilation succeeds

**Step 3: Commit**

```bash
git add internal/github/github.go
git commit -m "feat(github): add ApprovePR and MergePR functions"
```

---

### Task 6: Add WebSocket Commands for PR Actions

**Files:**
- Modify: `internal/daemon/websocket.go`
- Modify: `internal/protocol/types.go`

**Step 1: Add message types to protocol/types.go**

Add these constants and types:

```go
// Add to the const block with other message types:
const (
	// ... existing constants ...
	MsgApprovePR = "approve_pr"
	MsgMergePR   = "merge_pr"
	MsgPRActionResult = "pr_action_result"
)

// Add these structs:
type ApprovePRMessage struct {
	Cmd    string `json:"cmd"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

type MergePRMessage struct {
	Cmd    string `json:"cmd"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Method string `json:"method"` // "squash", "merge", "rebase"
}

type PRActionResultMessage struct {
	Event   string `json:"event"`
	Action  string `json:"action"` // "approve" or "merge"
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
```

**Step 2: Handle PR actions in websocket.go**

Find the `handleClientMessage` function and add cases:

```go
case protocol.MsgApprovePR:
	var msg protocol.ApprovePRMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	go func() {
		err := h.daemon.ghFetcher.ApprovePR(msg.Repo, msg.Number)
		result := protocol.PRActionResultMessage{
			Event:   protocol.MsgPRActionResult,
			Action:  "approve",
			Repo:    msg.Repo,
			Number:  msg.Number,
			Success: err == nil,
		}
		if err != nil {
			result.Error = err.Error()
		}
		h.sendJSON(result)
		// Trigger PR refresh after action
		h.daemon.RefreshPRs()
	}()

case protocol.MsgMergePR:
	var msg protocol.MergePRMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	go func() {
		err := h.daemon.ghFetcher.MergePR(msg.Repo, msg.Number, msg.Method)
		result := protocol.PRActionResultMessage{
			Event:   protocol.MsgPRActionResult,
			Action:  "merge",
			Repo:    msg.Repo,
			Number:  msg.Number,
			Success: err == nil,
		}
		if err != nil {
			result.Error = err.Error()
		}
		h.sendJSON(result)
		// Trigger PR refresh after action
		h.daemon.RefreshPRs()
	}()
```

**Step 3: Verify Go compiles**

Run: `go build ./...`
Expected: Compilation succeeds

**Step 4: Commit**

```bash
git add internal/protocol/types.go internal/daemon/websocket.go
git commit -m "feat(daemon): add WebSocket commands for PR approve and merge"
```

---

### Task 7: Create PR Actions Hook in Frontend

**Files:**
- Create: `app/src/hooks/usePRActions.ts`

**Step 1: Create the hook**

```typescript
// app/src/hooks/usePRActions.ts
import { useState, useCallback, useEffect, useRef } from 'react';

interface PRActionState {
  loading: boolean;
  success: boolean;
  error: string | null;
}

type ActionStates = Map<string, PRActionState>;

interface UsePRActionsResult {
  approve: (repo: string, number: number) => Promise<void>;
  merge: (repo: string, number: number, method?: string) => Promise<void>;
  getActionState: (repo: string, number: number, action: string) => PRActionState | undefined;
}

export function usePRActions(wsUrl = 'ws://127.0.0.1:9849/ws'): UsePRActionsResult {
  const [actionStates, setActionStates] = useState<ActionStates>(new Map());
  const wsRef = useRef<WebSocket | null>(null);
  const pendingActions = useRef<Map<string, (result: { success: boolean; error?: string }) => void>>(new Map());

  // Connect to WebSocket
  useEffect(() => {
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.event === 'pr_action_result') {
          const key = `${data.repo}#${data.number}:${data.action}`;

          // Update state
          setActionStates(prev => {
            const next = new Map(prev);
            next.set(key, {
              loading: false,
              success: data.success,
              error: data.error || null,
            });
            return next;
          });

          // Resolve pending promise
          const resolve = pendingActions.current.get(key);
          if (resolve) {
            resolve({ success: data.success, error: data.error });
            pendingActions.current.delete(key);
          }

          // Clear success state after 2 seconds
          if (data.success) {
            setTimeout(() => {
              setActionStates(prev => {
                const next = new Map(prev);
                next.delete(key);
                return next;
              });
            }, 2000);
          }
        }
      } catch (e) {
        console.error('[PRActions] Parse error:', e);
      }
    };

    wsRef.current = ws;
    return () => ws.close();
  }, [wsUrl]);

  const sendAction = useCallback(async (
    action: string,
    repo: string,
    number: number,
    extra?: object
  ): Promise<void> => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      throw new Error('Not connected');
    }

    const key = `${repo}#${number}:${action}`;

    // Set loading state
    setActionStates(prev => {
      const next = new Map(prev);
      next.set(key, { loading: true, success: false, error: null });
      return next;
    });

    // Create promise for result
    return new Promise((resolve, reject) => {
      pendingActions.current.set(key, (result) => {
        if (result.success) {
          resolve();
        } else {
          reject(new Error(result.error || 'Action failed'));
        }
      });

      ws.send(JSON.stringify({
        cmd: `${action}_pr`,
        repo,
        number,
        ...extra,
      }));

      // Timeout after 30 seconds
      setTimeout(() => {
        if (pendingActions.current.has(key)) {
          pendingActions.current.delete(key);
          setActionStates(prev => {
            const next = new Map(prev);
            next.set(key, { loading: false, success: false, error: 'Timeout' });
            return next;
          });
          reject(new Error('Timeout'));
        }
      }, 30000);
    });
  }, []);

  const approve = useCallback((repo: string, number: number) => {
    return sendAction('approve', repo, number);
  }, [sendAction]);

  const merge = useCallback((repo: string, number: number, method = 'squash') => {
    return sendAction('merge', repo, number, { method });
  }, [sendAction]);

  const getActionState = useCallback((repo: string, number: number, action: string) => {
    return actionStates.get(`${repo}#${number}:${action}`);
  }, [actionStates]);

  return { approve, merge, getActionState };
}
```

**Step 2: Commit**

```bash
git add app/src/hooks/usePRActions.ts
git commit -m "feat(app): add usePRActions hook for PR approve/merge"
```

---

### Task 8: Create Mute Store for Local Mute State

**Files:**
- Create: `app/src/store/mutes.ts`

**Step 1: Create the mute store**

```typescript
// app/src/store/mutes.ts
import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface MuteState {
  mutedPRs: Set<string>;    // PR IDs like "owner/repo#123"
  mutedRepos: Set<string>;  // Repo names like "owner/repo"
  undoStack: Array<{ type: 'pr' | 'repo'; id: string; timestamp: number }>;
}

interface MuteActions {
  mutePR: (prId: string) => void;
  unmutePR: (prId: string) => void;
  muteRepo: (repo: string) => void;
  unmuteRepo: (repo: string) => void;
  isPRMuted: (prId: string, repo: string) => boolean;
  isRepoMuted: (repo: string) => boolean;
  processUndo: () => { type: 'pr' | 'repo'; id: string } | null;
}

const UNDO_WINDOW_MS = 5000;

export const useMuteStore = create<MuteState & MuteActions>()(
  persist(
    (set, get) => ({
      mutedPRs: new Set(),
      mutedRepos: new Set(),
      undoStack: [],

      mutePR: (prId: string) => {
        set((state) => ({
          mutedPRs: new Set([...state.mutedPRs, prId]),
          undoStack: [...state.undoStack, { type: 'pr', id: prId, timestamp: Date.now() }],
        }));
      },

      unmutePR: (prId: string) => {
        set((state) => ({
          mutedPRs: new Set([...state.mutedPRs].filter(id => id !== prId)),
        }));
      },

      muteRepo: (repo: string) => {
        set((state) => ({
          mutedRepos: new Set([...state.mutedRepos, repo]),
          undoStack: [...state.undoStack, { type: 'repo', id: repo, timestamp: Date.now() }],
        }));
      },

      unmuteRepo: (repo: string) => {
        set((state) => ({
          mutedRepos: new Set([...state.mutedRepos].filter(r => r !== repo)),
        }));
      },

      isPRMuted: (prId: string, repo: string) => {
        const state = get();
        return state.mutedPRs.has(prId) || state.mutedRepos.has(repo);
      },

      isRepoMuted: (repo: string) => {
        return get().mutedRepos.has(repo);
      },

      processUndo: () => {
        const state = get();
        const now = Date.now();

        // Find most recent undo within window
        const validUndo = [...state.undoStack]
          .reverse()
          .find(u => now - u.timestamp < UNDO_WINDOW_MS);

        if (validUndo) {
          // Remove from undo stack and unmute
          set((s) => ({
            undoStack: s.undoStack.filter(u => u !== validUndo),
            mutedPRs: validUndo.type === 'pr'
              ? new Set([...s.mutedPRs].filter(id => id !== validUndo.id))
              : s.mutedPRs,
            mutedRepos: validUndo.type === 'repo'
              ? new Set([...s.mutedRepos].filter(r => r !== validUndo.id))
              : s.mutedRepos,
          }));
          return validUndo;
        }
        return null;
      },
    }),
    {
      name: 'attn-mutes',
      // Custom serialization for Sets
      storage: {
        getItem: (name) => {
          const str = localStorage.getItem(name);
          if (!str) return null;
          const data = JSON.parse(str);
          return {
            state: {
              ...data.state,
              mutedPRs: new Set(data.state.mutedPRs || []),
              mutedRepos: new Set(data.state.mutedRepos || []),
              undoStack: data.state.undoStack || [],
            },
          };
        },
        setItem: (name, value) => {
          const data = {
            state: {
              ...value.state,
              mutedPRs: [...value.state.mutedPRs],
              mutedRepos: [...value.state.mutedRepos],
            },
          };
          localStorage.setItem(name, JSON.stringify(data));
        },
        removeItem: (name) => localStorage.removeItem(name),
      },
    }
  )
);
```

**Step 2: Commit**

```bash
git add app/src/store/mutes.ts
git commit -m "feat(app): add mute store for local PR/repo muting"
```

---

### Task 9: Create PRActions Component

**Files:**
- Create: `app/src/components/PRActions.tsx`
- Create: `app/src/components/PRActions.css`

**Step 1: Create PRActions.tsx**

```typescript
// app/src/components/PRActions.tsx
import { useState } from 'react';
import { usePRActions } from '../hooks/usePRActions';
import { useMuteStore } from '../store/mutes';
import './PRActions.css';

interface PRActionsProps {
  repo: string;
  number: number;
  prId: string;
  compact?: boolean;
  onMuted?: () => void;
}

export function PRActions({ repo, number, prId, compact = false, onMuted }: PRActionsProps) {
  const { approve, merge, getActionState } = usePRActions();
  const { mutePR } = useMuteStore();
  const [showMergeConfirm, setShowMergeConfirm] = useState(false);

  const approveState = getActionState(repo, number, 'approve');
  const mergeState = getActionState(repo, number, 'merge');

  const handleApprove = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    try {
      await approve(repo, number);
    } catch (err) {
      console.error('Approve failed:', err);
    }
  };

  const handleMerge = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setShowMergeConfirm(true);
  };

  const confirmMerge = async () => {
    setShowMergeConfirm(false);
    try {
      await merge(repo, number);
    } catch (err) {
      console.error('Merge failed:', err);
    }
  };

  const handleMute = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    mutePR(prId);
    onMuted?.();
  };

  const renderButton = (
    action: string,
    state: { loading?: boolean; success?: boolean; error?: string | null } | undefined,
    onClick: (e: React.MouseEvent) => void,
    label: string,
    icon: string
  ) => {
    const isLoading = state?.loading;
    const isSuccess = state?.success;
    const hasError = state?.error;

    return (
      <button
        className={`pr-action-btn ${compact ? 'compact' : ''}`}
        data-action={action}
        data-loading={isLoading}
        data-success={isSuccess}
        data-error={!!hasError}
        onClick={onClick}
        disabled={isLoading}
        title={hasError || label}
      >
        {isLoading ? (
          <span className="spinner" />
        ) : isSuccess ? (
          '✓'
        ) : (
          compact ? icon : label
        )}
      </button>
    );
  };

  return (
    <>
      <div className={`pr-actions ${compact ? 'compact' : ''}`}>
        {renderButton('approve', approveState, handleApprove, 'Approve', '✓')}
        {renderButton('merge', mergeState, handleMerge, 'Merge', '⇋')}
        <button
          className={`pr-action-btn ${compact ? 'compact' : ''}`}
          data-action="mute"
          onClick={handleMute}
          title="Mute this PR"
        >
          {compact ? '⊘' : 'Mute'}
        </button>
      </div>

      {showMergeConfirm && (
        <div className="merge-confirm-overlay" onClick={() => setShowMergeConfirm(false)}>
          <div className="merge-confirm-modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-title">Merge PR #{number}?</span>
            </div>
            <div className="modal-body">
              This will merge the pull request and delete the branch.
            </div>
            <div className="modal-footer">
              <button className="modal-btn modal-btn-cancel" onClick={() => setShowMergeConfirm(false)}>
                Cancel
              </button>
              <button className="modal-btn modal-btn-primary" onClick={confirmMerge}>
                Merge
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
```

**Step 2: Create PRActions.css**

```css
/* app/src/components/PRActions.css */
.pr-actions {
  display: flex;
  gap: 4px;
  margin-left: auto;
  opacity: 0;
  transition: opacity 150ms ease-out;
}

.pr-row:hover .pr-actions,
.pr-row:focus-within .pr-actions,
.pr-actions.compact {
  opacity: 1;
}

.pr-action-btn {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 4px;
  padding: 4px 8px;
  font-size: 11px;
  font-weight: 600;
  color: #888;
  cursor: pointer;
  transition: all 150ms ease-out;
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 56px;
}

.pr-action-btn.compact {
  min-width: 24px;
  width: 24px;
  height: 24px;
  padding: 0;
  font-size: 12px;
}

.pr-action-btn:hover {
  background: #111113;
  color: #e8e8e8;
  border-color: #4a4a4d;
}

.pr-action-btn:focus {
  outline: 2px solid #ff6b35;
  outline-offset: 1px;
}

.pr-action-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Action-specific colors */
.pr-action-btn[data-action="approve"]:hover {
  background: rgba(34, 197, 94, 0.1);
  color: #22c55e;
  border-color: #22c55e;
}

.pr-action-btn[data-action="merge"]:hover {
  background: rgba(167, 139, 250, 0.1);
  color: #a78bfa;
  border-color: #a78bfa;
}

.pr-action-btn[data-action="mute"]:hover {
  background: rgba(85, 85, 85, 0.2);
}

/* Success state */
.pr-action-btn[data-success="true"] {
  background: #22c55e;
  color: #000;
  border-color: #22c55e;
}

/* Error state */
.pr-action-btn[data-error="true"] {
  border-color: #ef4444;
  color: #ef4444;
}

/* Loading spinner */
.spinner {
  width: 10px;
  height: 10px;
  border: 2px solid #555;
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Merge confirmation modal */
.merge-confirm-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 300;
}

.merge-confirm-modal {
  width: 400px;
  background: #111113;
  border: 1px solid #2a2a2d;
  border-radius: 8px;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.6);
}

.modal-header {
  padding: 16px;
  border-bottom: 1px solid #2a2a2d;
}

.modal-title {
  font-family: 'JetBrains Mono', monospace;
  font-size: 13px;
  font-weight: 600;
  color: #e8e8e8;
}

.modal-body {
  padding: 16px;
  color: #888;
  font-size: 12px;
}

.modal-footer {
  padding: 16px;
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  border-top: 1px solid #2a2a2d;
}

.modal-btn {
  padding: 8px 16px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid #2a2a2d;
}

.modal-btn-cancel {
  background: transparent;
  color: #888;
}

.modal-btn-cancel:hover {
  background: #1a1a1d;
  color: #e8e8e8;
}

.modal-btn-primary {
  background: #a78bfa;
  color: #000;
  border-color: #a78bfa;
}

.modal-btn-primary:hover {
  background: #b695f5;
}
```

**Step 3: Commit**

```bash
git add app/src/components/PRActions.tsx app/src/components/PRActions.css
git commit -m "feat(app): add PRActions component with approve/merge/mute buttons"
```

---

### Task 10: Create UndoToast Component

**Files:**
- Create: `app/src/components/UndoToast.tsx`
- Create: `app/src/components/UndoToast.css`

**Step 1: Create UndoToast.tsx**

```typescript
// app/src/components/UndoToast.tsx
import { useState, useEffect, useCallback } from 'react';
import { useMuteStore } from '../store/mutes';
import './UndoToast.css';

export function UndoToast() {
  const [visible, setVisible] = useState(false);
  const [message, setMessage] = useState('');
  const [countdown, setCountdown] = useState(5);
  const { undoStack, processUndo } = useMuteStore();

  // Watch for new mutes
  useEffect(() => {
    if (undoStack.length > 0) {
      const latest = undoStack[undoStack.length - 1];
      const itemType = latest.type === 'pr' ? 'PR' : 'Repository';
      setMessage(`${itemType} muted`);
      setVisible(true);
      setCountdown(5);
    }
  }, [undoStack.length]);

  // Countdown timer
  useEffect(() => {
    if (!visible) return;

    const timer = setInterval(() => {
      setCountdown(prev => {
        if (prev <= 1) {
          setVisible(false);
          return 5;
        }
        return prev - 1;
      });
    }, 1000);

    return () => clearInterval(timer);
  }, [visible]);

  const handleUndo = useCallback(() => {
    const undone = processUndo();
    if (undone) {
      setVisible(false);
    }
  }, [processUndo]);

  if (!visible) return null;

  return (
    <div className="undo-toast">
      <span className="toast-message">{message}</span>
      <button className="toast-undo-btn" onClick={handleUndo}>
        Undo ({countdown}s)
      </button>
    </div>
  );
}
```

**Step 2: Create UndoToast.css**

```css
/* app/src/components/UndoToast.css */
.undo-toast {
  position: fixed;
  bottom: 24px;
  left: 50%;
  transform: translateX(-50%);
  background: #111113;
  border: 1px solid #2a2a2d;
  border-radius: 6px;
  padding: 12px 16px;
  display: flex;
  align-items: center;
  gap: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  z-index: 400;
  animation: toast-appear 200ms ease-out;
}

@keyframes toast-appear {
  from {
    opacity: 0;
    transform: translateX(-50%) translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateX(-50%) translateY(0);
  }
}

.toast-message {
  color: #e8e8e8;
  font-size: 12px;
  font-family: 'JetBrains Mono', monospace;
}

.toast-undo-btn {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 4px;
  padding: 4px 12px;
  color: #ff6b35;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 150ms ease-out;
}

.toast-undo-btn:hover {
  background: rgba(255, 107, 53, 0.15);
  border-color: #ff6b35;
}
```

**Step 3: Commit**

```bash
git add app/src/components/UndoToast.tsx app/src/components/UndoToast.css
git commit -m "feat(app): add UndoToast component for mute undo"
```

---

### Task 11: Integrate PR Actions into Dashboard

**Files:**
- Modify: `app/src/components/Dashboard.tsx`
- Modify: `app/src/App.tsx`

**Step 1: Update Dashboard.tsx to use PRActions**

Add import at top:

```typescript
import { PRActions } from './PRActions';
import { useMuteStore } from '../store/mutes';
```

Add mute filtering after the existing useMemo:

```typescript
const { isPRMuted, isRepoMuted, muteRepo } = useMuteStore();

// Update prsByRepo to filter muted PRs
const prsByRepo = useMemo(() => {
  const activePRs = prs.filter((p) => !p.muted && !isPRMuted(p.id, p.repo));
  const grouped = new Map<string, DaemonPR[]>();
  for (const pr of activePRs) {
    if (isRepoMuted(pr.repo)) continue;
    const existing = grouped.get(pr.repo) || [];
    grouped.set(pr.repo, [...existing, pr]);
  }
  return grouped;
}, [prs, isPRMuted, isRepoMuted]);
```

Update the PR row to include actions:

```typescript
// Find the pr-row anchor tag and update to:
<div key={pr.id} className="pr-row">
  <a
    href={pr.url}
    target="_blank"
    rel="noopener noreferrer"
    className="pr-link"
  >
    <span className={`pr-role ${pr.role}`}>
      {pr.role === 'reviewer' ? '👀' : '✏️'}
    </span>
    <span className="pr-number">#{pr.number}</span>
    <span className="pr-title">{pr.title}</span>
    {pr.role === 'author' && (
      <span className="pr-reason">{pr.reason.replace(/_/g, ' ')}</span>
    )}
  </a>
  <PRActions repo={pr.repo} number={pr.number} prId={pr.id} />
</div>
```

Add repo-level mute button:

```typescript
// In repo-header, add after repo-counts:
<button
  className="repo-mute-btn"
  onClick={(e) => {
    e.stopPropagation();
    muteRepo(repo);
  }}
  title="Mute all PRs from this repo"
>
  ⊘
</button>
```

**Step 2: Update Dashboard.css**

Add these styles:

```css
/* PR row update for actions */
.pr-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 6px;
}

.pr-link {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  font-size: 12px;
  color: #888;
  text-decoration: none;
  overflow: hidden;
}

.pr-row:hover {
  background: #1a1a1d;
}

.pr-row:hover .pr-link {
  color: #e8e8e8;
}

/* Repo mute button */
.repo-mute-btn {
  background: none;
  border: 1px solid transparent;
  border-radius: 4px;
  color: #555;
  font-size: 12px;
  padding: 2px 6px;
  cursor: pointer;
  opacity: 0;
  transition: all 150ms;
}

.repo-header:hover .repo-mute-btn {
  opacity: 1;
}

.repo-mute-btn:hover {
  background: rgba(85, 85, 85, 0.2);
  border-color: #2a2a2d;
  color: #888;
}
```

**Step 3: Add UndoToast to App.tsx**

Add import:

```typescript
import { UndoToast } from './components/UndoToast';
```

Add before closing `</div>` of the app:

```typescript
<UndoToast />
```

**Step 4: Verify changes work**

Run: `cd app && pnpm tauri dev`
Test:
- Hover over PR row - action buttons appear
- Click Approve - loading then success
- Click Merge - confirmation modal appears
- Click Mute - PR disappears, toast appears with undo

**Step 5: Commit**

```bash
git add app/src/components/Dashboard.tsx app/src/components/Dashboard.css app/src/App.tsx
git commit -m "feat(app): integrate PR actions into Dashboard"
```

---

### Task 12: Integrate PR Actions into AttentionDrawer

**Files:**
- Modify: `app/src/components/AttentionDrawer.tsx`
- Modify: `app/src/components/AttentionDrawer.css`

**Step 1: Update AttentionDrawer.tsx**

Add imports:

```typescript
import { PRActions } from './PRActions';
import { useMuteStore } from '../store/mutes';
```

Add mute filtering:

```typescript
const { isPRMuted, isRepoMuted } = useMuteStore();

// Update the PR filters to exclude muted:
const reviewPRs = prs.filter((p) =>
  p.role === 'reviewer' &&
  !p.muted &&
  !isPRMuted(p.id, p.repo) &&
  !isRepoMuted(p.repo)
);
const authorPRs = prs.filter((p) =>
  p.role === 'author' &&
  !p.muted &&
  !isPRMuted(p.id, p.repo) &&
  !isRepoMuted(p.repo)
);
```

Update PR items to include actions (for both reviewPRs and authorPRs sections):

```typescript
// Replace the existing PR <a> tags with:
<div key={pr.id} className="attention-item pr-item">
  <a
    href={pr.url}
    target="_blank"
    rel="noopener noreferrer"
    className="pr-link"
  >
    <span className="item-dot pr" />
    <span className="item-name">
      {pr.repo.split('/')[1]} #{pr.number}
    </span>
    {pr.role === 'author' && (
      <span className="item-reason">{pr.reason.replace(/_/g, ' ')}</span>
    )}
  </a>
  <PRActions repo={pr.repo} number={pr.number} prId={pr.id} compact />
</div>
```

**Step 2: Update AttentionDrawer.css**

Add these styles:

```css
/* PR item with actions */
.attention-item.pr-item {
  display: flex;
  align-items: center;
}

.attention-item .pr-link {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  color: #e8e8e8;
  text-decoration: none;
  overflow: hidden;
}

.attention-item .pr-link:hover {
  color: #fff;
}

/* Compact actions always visible in drawer */
.attention-item .pr-actions.compact {
  opacity: 1;
  flex-shrink: 0;
}
```

**Step 3: Verify changes work**

Run: `cd app && pnpm tauri dev`
Test:
- Open drawer (⌘K)
- PR items show compact action buttons
- Actions work same as Dashboard

**Step 4: Commit**

```bash
git add app/src/components/AttentionDrawer.tsx app/src/components/AttentionDrawer.css
git commit -m "feat(app): integrate PR actions into AttentionDrawer"
```

---

### Task 13: Final Integration Test

**Files:** None (testing only)

**Step 1: Build and install daemon**

```bash
make install
```

**Step 2: Start the app**

```bash
cd app && pnpm run dev:all
```

**Step 3: Test checklist**

Run through all features:
- [ ] Sidebar shows "⌘B sidebar" in footer
- [ ] ⌘B toggles sidebar collapse
- [ ] LocationPicker shows filesystem suggestions when typing path
- [ ] Tab autocompletes directory names
- [ ] Recent locations still work
- [ ] PR action buttons appear on hover (Dashboard)
- [ ] PR action buttons always visible (Drawer)
- [ ] Approve button works (sends to daemon, shows success)
- [ ] Merge button shows confirmation, then merges
- [ ] Mute button hides PR, shows undo toast
- [ ] Undo works within 5 seconds
- [ ] Repo mute hides all PRs from repo
- [ ] Muted state persists across app restarts

**Step 4: Final commit if needed**

```bash
git add -A
git commit -m "feat(app): complete PR actions, filesystem autocomplete, and sidebar shortcut"
```

---

## Summary

This plan implements:

1. **Sidebar Shortcut** (Task 1) - Quick UI update to show ⌘B hint
2. **Filesystem Autocomplete** (Tasks 2-4) - Tauri command + hook + updated LocationPicker
3. **PR Actions** (Tasks 5-12) - Go daemon commands + WebSocket + frontend components

**Total:** 13 tasks, ~50 steps

**Execution time estimate:** 2-3 hours for implementation, 30 min for testing
