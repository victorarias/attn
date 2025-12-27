# New Session Dialog Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rewrite the LocationPicker component with fish-style autocomplete, proper branch visibility, and terminal-native aesthetics.

**Architecture:** Replace `LocationPicker.tsx` (1000 lines, 16 useState hooks) with a cleaner implementation split into focused sub-components. Extend backend to return commit info with branches and add a `get_repo_info` command that returns everything needed for the repository options screen.

**Tech Stack:** React + TypeScript (frontend), Go (daemon), TypeSpec (protocol), CSS (styling)

---

## Task 1: Extend Branch Type with Commit Info

**Files:**
- Modify: `internal/protocol/schema/main.tsp:102-104`
- Modify: `internal/git/branch.go`
- Run: `make generate-types`

**Step 1: Update TypeSpec Branch model**

Edit `internal/protocol/schema/main.tsp`:

```tsp
model Branch {
  name: string;
  commit_hash?: string;    // Short SHA (7 chars)
  commit_time?: string;    // ISO timestamp
  is_current?: boolean;    // True if this is the checked-out branch
}
```

**Step 2: Add git function to get branch with commit info**

Add to `internal/git/branch.go`:

```go
// BranchWithCommit contains branch name and latest commit info
type BranchWithCommit struct {
	Name       string
	CommitHash string // Short SHA
	CommitTime string // ISO timestamp
	IsCurrent  bool
}

// ListBranchesWithCommits returns branches with their latest commit info.
func ListBranchesWithCommits(repoDir string) ([]BranchWithCommit, error) {
	// Get current branch for marking
	currentBranch, _ := GetCurrentBranch(repoDir)

	// Get all local branches with commit info
	// Format: refname:short | committerdate:iso-strict | objectname:short
	cmd := exec.Command("git", "branch", "--format=%(refname:short)|%(committerdate:iso-strict)|%(objectname:short)")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	// Get branches that are checked out in worktrees
	worktrees, err := ListWorktrees(repoDir)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	checkedOut := make(map[string]bool)
	for _, wt := range worktrees {
		if wt.Branch != "" {
			checkedOut[wt.Branch] = true
		}
	}

	var result []BranchWithCommit
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		// Skip branches checked out in worktrees
		if checkedOut[name] {
			continue
		}
		result = append(result, BranchWithCommit{
			Name:       name,
			CommitTime: parts[1],
			CommitHash: parts[2],
			IsCurrent:  name == currentBranch,
		})
	}

	return result, nil
}
```

**Step 3: Generate types**

Run: `make generate-types`
Expected: Types regenerated in `internal/protocol/generated.go` and `app/src/types/generated.ts`

**Step 4: Run tests**

Run: `make test`
Expected: All tests pass

**Step 5: Commit**

```bash
git add internal/protocol/schema/main.tsp internal/git/branch.go
git add internal/protocol/generated.go app/src/types/generated.ts
git commit -m "feat(protocol): extend Branch type with commit info"
```

---

## Task 2: Add get_repo_info Command

**Files:**
- Modify: `internal/protocol/schema/main.tsp`
- Modify: `internal/protocol/constants.go`
- Modify: `internal/daemon/branch.go`
- Modify: `internal/daemon/websocket.go`

**Step 1: Define TypeSpec models**

Add to `internal/protocol/schema/main.tsp`:

```tsp
model GetRepoInfoMessage {
  cmd: "get_repo_info";
  repo: string;
}

model RepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: Worktree[];
  branches: Branch[];          // Available branches with commit info
  fetched_at?: string;         // ISO timestamp, when branches were last fetched
}

model GetRepoInfoResultMessage {
  event: "get_repo_info_result";
  info?: RepoInfo;
  success: boolean;
  error?: string;
}
```

**Step 2: Run generate-types**

Run: `make generate-types`
Expected: New types generated

**Step 3: Add constants**

Add to `internal/protocol/constants.go`:

```go
const CmdGetRepoInfo = "get_repo_info"
const EventGetRepoInfoResult = "get_repo_info_result"
```

**Step 4: Add ParseMessage case**

Add to `ParseMessage()` in `internal/protocol/constants.go`:

```go
case CmdGetRepoInfo:
	var msg GetRepoInfoMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", nil, err
	}
	return peek.Cmd, &msg, nil
```

**Step 5: Implement handler**

Add to `internal/daemon/branch.go`:

```go
func (d *Daemon) handleGetRepoInfoWS(client *wsClient, msg *protocol.GetRepoInfoMessage) {
	go func() {
		repo := git.ExpandPath(msg.Repo)

		// Get current branch and commit
		currentBranch, err := git.GetCurrentBranch(repo)
		if err != nil {
			d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
				Event:   protocol.EventGetRepoInfoResult,
				Success: false,
				Error:   protocol.Ptr(err.Error()),
			})
			return
		}

		// Get current commit hash and time
		commitHash, commitTime := git.GetHeadCommitInfo(repo)

		// Get default branch
		defaultBranch, _ := git.GetDefaultBranch(repo)
		if defaultBranch == "" {
			defaultBranch = "main"
		}

		// Get worktrees
		worktrees := d.doListWorktrees(repo)

		// Get available branches with commit info
		branchesWithCommits, err := git.ListBranchesWithCommits(repo)
		if err != nil {
			d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
				Event:   protocol.EventGetRepoInfoResult,
				Success: false,
				Error:   protocol.Ptr(err.Error()),
			})
			return
		}

		branches := make([]protocol.Branch, len(branchesWithCommits))
		for i, b := range branchesWithCommits {
			branches[i] = protocol.Branch{
				Name:       b.Name,
				CommitHash: protocol.Ptr(b.CommitHash),
				CommitTime: protocol.Ptr(b.CommitTime),
				IsCurrent:  protocol.Ptr(b.IsCurrent),
			}
		}

		d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
			Event: protocol.EventGetRepoInfoResult,
			Info: &protocol.RepoInfo{
				Repo:              repo,
				CurrentBranch:     currentBranch,
				CurrentCommitHash: commitHash,
				CurrentCommitTime: commitTime,
				DefaultBranch:     defaultBranch,
				Worktrees:         worktrees,
				Branches:          branches,
			},
			Success: true,
		})
	}()
}
```

**Step 6: Add GetHeadCommitInfo helper**

Add to `internal/git/branch.go`:

```go
// GetHeadCommitInfo returns the short hash and ISO timestamp of HEAD
func GetHeadCommitInfo(repoDir string) (hash string, time string) {
	cmd := exec.Command("git", "log", "-1", "--format=%h|%cI")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}
```

**Step 7: Wire up in websocket.go**

Add case in `handleWebSocketMessage` in `internal/daemon/websocket.go`:

```go
case *protocol.GetRepoInfoMessage:
	d.handleGetRepoInfoWS(client, m)
```

**Step 8: Increment protocol version**

Edit `internal/protocol/constants.go`:
```go
const ProtocolVersion = "11"
```

**Step 9: Run tests**

Run: `make test`
Expected: All tests pass

**Step 10: Commit**

```bash
git add internal/protocol/schema/main.tsp internal/protocol/constants.go
git add internal/protocol/generated.go app/src/types/generated.ts
git add internal/daemon/branch.go internal/daemon/websocket.go internal/git/branch.go
git commit -m "feat(daemon): add get_repo_info command with commit info"
```

---

## Task 3: Add Fetch Caching

**Files:**
- Modify: `internal/daemon/daemon.go` (add cache struct)
- Modify: `internal/daemon/branch.go` (use cache)

**Step 1: Add cache structure to daemon**

Add to `internal/daemon/daemon.go` struct:

```go
type repoCache struct {
	fetchedAt time.Time
	branches  []protocol.Branch
}

// In Daemon struct:
repoCaches   map[string]*repoCache
repoCacheMu  sync.RWMutex
```

Initialize in `NewDaemon`:
```go
repoCaches: make(map[string]*repoCache),
```

**Step 2: Add cache methods**

Add to `internal/daemon/branch.go`:

```go
const fetchCacheTTL = 30 * time.Minute

func (d *Daemon) getCachedBranches(repo string) ([]protocol.Branch, time.Time, bool) {
	d.repoCacheMu.RLock()
	defer d.repoCacheMu.RUnlock()

	cache, ok := d.repoCaches[repo]
	if !ok || time.Since(cache.fetchedAt) > fetchCacheTTL {
		return nil, time.Time{}, false
	}
	return cache.branches, cache.fetchedAt, true
}

func (d *Daemon) setCachedBranches(repo string, branches []protocol.Branch) {
	d.repoCacheMu.Lock()
	defer d.repoCacheMu.Unlock()

	d.repoCaches[repo] = &repoCache{
		fetchedAt: time.Now(),
		branches:  branches,
	}
}

func (d *Daemon) invalidateBranchCache(repo string) {
	d.repoCacheMu.Lock()
	defer d.repoCacheMu.Unlock()
	delete(d.repoCaches, repo)
}
```

**Step 3: Use cache in handleGetRepoInfoWS**

Modify `handleGetRepoInfoWS` to check cache:

```go
// Check cache first
if cached, fetchedAt, ok := d.getCachedBranches(repo); ok {
	// Use cached branches
	d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
		Event: protocol.EventGetRepoInfoResult,
		Info: &protocol.RepoInfo{
			// ... other fields
			Branches:  cached,
			FetchedAt: protocol.Ptr(fetchedAt.Format(time.RFC3339)),
		},
		Success: true,
	})
	return
}

// ... fetch fresh and cache
d.setCachedBranches(repo, branches)
```

**Step 4: Run tests**

Run: `make test`
Expected: All tests pass

**Step 5: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/branch.go
git commit -m "feat(daemon): add 30-minute branch cache"
```

---

## Task 4: Add Starting Point to Create Worktree

**Files:**
- Modify: `internal/protocol/schema/main.tsp`
- Modify: `internal/git/worktree.go`
- Modify: `internal/daemon/worktree.go`

**Step 1: Update TypeSpec**

Edit `CreateWorktreeMessage` in `internal/protocol/schema/main.tsp`:

```tsp
model CreateWorktreeMessage {
  cmd: "create_worktree";
  main_repo: string;
  branch: string;
  path?: string;
  starting_from?: string;  // Branch to create worktree from (default: current HEAD)
}
```

**Step 2: Generate types**

Run: `make generate-types`

**Step 3: Update git function**

Modify `internal/git/worktree.go`:

```go
// CreateWorktreeFromPoint creates a worktree with a new branch starting from a specific ref.
func CreateWorktreeFromPoint(repoDir, branch, path, startingFrom string) error {
	args := []string{"worktree", "add", "-b", branch, path}
	if startingFrom != "" {
		args = append(args, startingFrom)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}
```

**Step 4: Update daemon handler**

Modify `doCreateWorktree` in `internal/daemon/worktree.go`:

```go
func (d *Daemon) doCreateWorktree(msg *protocol.CreateWorktreeMessage) (string, error) {
	path := protocol.Deref(msg.Path)
	if path == "" {
		path = git.GenerateWorktreePath(msg.MainRepo, msg.Branch)
	}

	startingFrom := protocol.Deref(msg.StartingFrom)
	var err error
	if startingFrom != "" {
		err = git.CreateWorktreeFromPoint(msg.MainRepo, msg.Branch, path, startingFrom)
	} else {
		err = git.CreateWorktree(msg.MainRepo, msg.Branch, path)
	}
	if err != nil {
		return "", err
	}
	// ... rest of function unchanged
}
```

**Step 5: Run tests**

Run: `make test`
Expected: All tests pass

**Step 6: Commit**

```bash
git add internal/protocol/schema/main.tsp internal/protocol/generated.go
git add internal/git/worktree.go internal/daemon/worktree.go
git add app/src/types/generated.ts
git commit -m "feat(worktree): add starting_from parameter for new worktrees"
```

---

## Task 5: Frontend - Add useDaemonSocket handlers

**Files:**
- Modify: `app/src/hooks/useDaemonSocket.ts`

**Step 1: Add RepoInfo result type**

Add to `app/src/hooks/useDaemonSocket.ts`:

```typescript
interface RepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: DaemonWorktree[];
  branches: Branch[];
  fetched_at?: string;
}

interface RepoInfoResult {
  success: boolean;
  info?: RepoInfo;
  error?: string;
}
```

**Step 2: Add getRepoInfo function**

Add to the hook's return object:

```typescript
const getRepoInfo = useCallback((repo: string): Promise<RepoInfoResult> => {
  return new Promise((resolve, reject) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error('WebSocket not connected'));
      return;
    }

    const key = `repo_info_${repo}`;
    pendingActionsRef.current.set(key, { resolve, reject });

    ws.send(JSON.stringify({ cmd: 'get_repo_info', repo }));

    setTimeout(() => {
      if (pendingActionsRef.current.has(key)) {
        pendingActionsRef.current.delete(key);
        reject(new Error('get_repo_info timeout'));
      }
    }, 30000);
  });
}, []);
```

**Step 3: Handle result event**

Add case in event handler:

```typescript
case 'get_repo_info_result': {
  const key = `repo_info_${event.info?.repo || 'unknown'}`;
  const pending = pendingActionsRef.current.get(key);
  if (pending) {
    pendingActionsRef.current.delete(key);
    if (event.success) {
      pending.resolve({ success: true, info: event.info });
    } else {
      pending.resolve({ success: false, error: event.error });
    }
  }
  break;
}
```

**Step 4: Update PROTOCOL_VERSION**

```typescript
const PROTOCOL_VERSION = '11';
```

**Step 5: Run frontend tests**

Run: `cd app && pnpm test`
Expected: Tests pass

**Step 6: Commit**

```bash
git add app/src/hooks/useDaemonSocket.ts
git commit -m "feat(frontend): add getRepoInfo hook handler"
```

---

## Task 6: Create PathInput Component (Fish-Style Autocomplete)

**Files:**
- Create: `app/src/components/NewSessionDialog/PathInput.tsx`
- Create: `app/src/components/NewSessionDialog/PathInput.css`

**Step 1: Create component file**

Create `app/src/components/NewSessionDialog/PathInput.tsx`:

```tsx
import { useState, useRef, useEffect, useCallback } from 'react';
import './PathInput.css';

interface PathInputProps {
  value: string;
  onChange: (value: string) => void;
  onSelect: (path: string) => void;
  ghostText: string;
  placeholder?: string;
  autoFocus?: boolean;
}

export function PathInput({
  value,
  onChange,
  onSelect,
  ghostText,
  placeholder = 'Type path (e.g., ~/projects)...',
  autoFocus = true,
}: PathInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (autoFocus) {
      inputRef.current?.focus();
    }
  }, [autoFocus]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Tab' && ghostText) {
      e.preventDefault();
      // Complete to ghost text
      onChange(ghostText);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      onSelect(value || ghostText);
    }
  }, [ghostText, value, onChange, onSelect]);

  // Calculate ghost text to show (portion not yet typed)
  const visibleGhost = ghostText.startsWith(value)
    ? ghostText.slice(value.length)
    : '';

  return (
    <div className="path-input-container">
      <input
        ref={inputRef}
        type="text"
        className="path-input"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        spellCheck={false}
        autoComplete="off"
      />
      {visibleGhost && (
        <span className="path-ghost">{visibleGhost}</span>
      )}
    </div>
  );
}
```

**Step 2: Create CSS file**

Create `app/src/components/NewSessionDialog/PathInput.css`:

```css
.path-input-container {
  position: relative;
  font-family: 'JetBrains Mono', 'Fira Code', 'SF Mono', monospace;
}

.path-input {
  width: 100%;
  background: var(--bg-deep, #0d1117);
  border: 1px solid var(--text-muted, #484f58);
  padding: 12px;
  font-family: inherit;
  font-size: 14px;
  color: var(--text-primary, #e6edf3);
  caret-color: var(--git-green, #3fb950);
  outline: none;
  transition: border-color 80ms ease-out, box-shadow 80ms ease-out;
}

.path-input:focus {
  border-color: var(--git-blue, #58a6ff);
  box-shadow: 0 0 0 3px rgba(88, 166, 255, 0.15);
}

.path-input::placeholder {
  color: var(--text-muted, #484f58);
}

.path-ghost {
  position: absolute;
  left: 13px;
  top: 50%;
  transform: translateY(-50%);
  font-size: 14px;
  color: var(--text-muted, #484f58);
  pointer-events: none;
  white-space: pre;
}
```

**Step 3: Commit**

```bash
git add app/src/components/NewSessionDialog/
git commit -m "feat(ui): add PathInput component with fish-style ghost text"
```

---

## Task 7: Create RepoOptions Component

**Files:**
- Create: `app/src/components/NewSessionDialog/RepoOptions.tsx`
- Create: `app/src/components/NewSessionDialog/RepoOptions.css`

**Step 1: Create component**

Create `app/src/components/NewSessionDialog/RepoOptions.tsx`:

```tsx
import { useState, useCallback, useEffect, useRef } from 'react';
import type { DaemonWorktree, Branch } from '../../hooks/useDaemonSocket';
import './RepoOptions.css';

interface RepoInfo {
  repo: string;
  currentBranch: string;
  currentCommitHash: string;
  currentCommitTime: string;
  defaultBranch: string;
  worktrees: DaemonWorktree[];
  branches: Branch[];
  fetchedAt?: string;
}

interface RepoOptionsProps {
  repoInfo: RepoInfo;
  currentSessionBranch?: string;
  onSelectMainRepo: () => void;
  onSelectWorktree: (path: string) => void;
  onSelectBranch: (branch: string) => void;
  onCreateWorktree: (branchName: string, startingFrom: string) => Promise<void>;
  onRefresh: () => void;
  onBack: () => void;
  refreshing?: boolean;
}

export function RepoOptions({
  repoInfo,
  currentSessionBranch,
  onSelectMainRepo,
  onSelectWorktree,
  onSelectBranch,
  onCreateWorktree,
  onRefresh,
  onBack,
  refreshing = false,
}: RepoOptionsProps) {
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [showNewWorktree, setShowNewWorktree] = useState(false);
  const [newBranchName, setNewBranchName] = useState('');
  const [startingFrom, setStartingFrom] = useState<'current' | 'default'>('current');
  const [creating, setCreating] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // Calculate total items for navigation
  const totalItems = 1 + repoInfo.worktrees.length + 1 + repoInfo.branches.length;
  const newWorktreeIndex = 1 + repoInfo.worktrees.length;
  const branchStartIndex = newWorktreeIndex + 1;

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (showNewWorktree) {
      if (e.key === 'Escape') {
        setShowNewWorktree(false);
        setNewBranchName('');
      }
      return;
    }

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setSelectedIndex(i => Math.min(i + 1, totalItems - 1));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setSelectedIndex(i => Math.max(i - 1, 0));
        break;
      case 'Enter':
        e.preventDefault();
        handleSelect(selectedIndex);
        break;
      case 'r':
      case 'R':
        e.preventDefault();
        onRefresh();
        break;
      case 'Escape':
        e.preventDefault();
        onBack();
        break;
    }
  }, [selectedIndex, totalItems, showNewWorktree, onRefresh, onBack]);

  useEffect(() => {
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [handleKeyDown]);

  const handleSelect = (index: number) => {
    if (index === 0) {
      onSelectMainRepo();
    } else if (index <= repoInfo.worktrees.length) {
      onSelectWorktree(repoInfo.worktrees[index - 1].path);
    } else if (index === newWorktreeIndex) {
      setShowNewWorktree(true);
    } else {
      const branchIndex = index - branchStartIndex;
      onSelectBranch(repoInfo.branches[branchIndex].name);
    }
  };

  const handleCreateWorktree = async () => {
    if (!newBranchName || creating) return;
    setCreating(true);
    try {
      const from = startingFrom === 'current'
        ? currentSessionBranch || repoInfo.defaultBranch
        : `origin/${repoInfo.defaultBranch}`;
      await onCreateWorktree(newBranchName, from);
    } finally {
      setCreating(false);
    }
  };

  const formatTime = (isoTime?: string) => {
    if (!isoTime) return '';
    const date = new Date(isoTime);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    if (diffMins < 60) return `${diffMins}m ago`;
    const diffHours = Math.floor(diffMins / 60);
    if (diffHours < 24) return `${diffHours}h ago`;
    const diffDays = Math.floor(diffHours / 24);
    return `${diffDays}d ago`;
  };

  return (
    <div className="repo-options" ref={containerRef}>
      {/* Main Repository */}
      <div className="repo-section">
        <div className="section-header">MAIN REPOSITORY</div>
        <div
          className={`repo-item ${selectedIndex === 0 ? 'selected' : ''}`}
          onClick={onSelectMainRepo}
          onMouseEnter={() => setSelectedIndex(0)}
        >
          <span className="item-icon main">●</span>
          <div className="item-info">
            <span className="item-name">{repoInfo.currentBranch}</span>
            <span className="commit-info">
              <span className="commit-hash">{repoInfo.currentCommitHash}</span>
              <span className="commit-time">{formatTime(repoInfo.currentCommitTime)}</span>
            </span>
          </div>
        </div>
      </div>

      {/* Worktrees */}
      {repoInfo.worktrees.length > 0 && (
        <div className="repo-section">
          <div className="section-header">WORKTREES</div>
          {repoInfo.worktrees.map((wt, i) => (
            <div
              key={wt.path}
              className={`repo-item ${selectedIndex === i + 1 ? 'selected' : ''}`}
              onClick={() => onSelectWorktree(wt.path)}
              onMouseEnter={() => setSelectedIndex(i + 1)}
            >
              <span className="item-icon worktree">◎</span>
              <div className="item-info">
                <span className="item-name">{wt.branch}</span>
                <span className="item-path">{wt.path}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Branches */}
      <div className="repo-section">
        <div className="section-header">
          BRANCHES
          <button
            className="refresh-btn"
            onClick={onRefresh}
            disabled={refreshing}
          >
            {refreshing ? '...' : 'R'}
          </button>
        </div>

        {/* New worktree */}
        {showNewWorktree ? (
          <div className="new-worktree-form">
            <input
              type="text"
              className="new-branch-input"
              placeholder="feature/my-branch"
              value={newBranchName}
              onChange={(e) => setNewBranchName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && newBranchName) {
                  handleCreateWorktree();
                }
                if (e.key === 'Escape') {
                  setShowNewWorktree(false);
                  setNewBranchName('');
                }
              }}
              autoFocus
              disabled={creating}
            />
            <div className="starting-from">
              <span>from:</span>
              <label className={startingFrom === 'current' ? 'active' : ''}>
                <input
                  type="radio"
                  checked={startingFrom === 'current'}
                  onChange={() => setStartingFrom('current')}
                />
                {currentSessionBranch || repoInfo.currentBranch}
              </label>
              <label className={startingFrom === 'default' ? 'active' : ''}>
                <input
                  type="radio"
                  checked={startingFrom === 'default'}
                  onChange={() => setStartingFrom('default')}
                />
                origin/{repoInfo.defaultBranch}
              </label>
            </div>
          </div>
        ) : (
          <div
            className={`repo-item new-worktree ${selectedIndex === newWorktreeIndex ? 'selected' : ''}`}
            onClick={() => setShowNewWorktree(true)}
            onMouseEnter={() => setSelectedIndex(newWorktreeIndex)}
          >
            <span className="item-icon new">+</span>
            <span className="item-name">New worktree...</span>
          </div>
        )}

        {/* Available branches */}
        {repoInfo.branches.map((branch, i) => (
          <div
            key={branch.name}
            className={`repo-item ${selectedIndex === branchStartIndex + i ? 'selected' : ''}`}
            onClick={() => onSelectBranch(branch.name)}
            onMouseEnter={() => setSelectedIndex(branchStartIndex + i)}
          >
            <span className="item-icon branch">○</span>
            <div className="item-info">
              <span className="item-name">{branch.name}</span>
              <span className="commit-info">
                <span className="commit-hash">{branch.commit_hash}</span>
                <span className="commit-time">{formatTime(branch.commit_time)}</span>
              </span>
            </div>
          </div>
        ))}
      </div>

      <div className="repo-footer">
        <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
        <span className="shortcut"><kbd>Enter</kbd> select</span>
        <span className="shortcut"><kbd>R</kbd> refresh</span>
        <span className="shortcut"><kbd>Esc</kbd> back</span>
      </div>
    </div>
  );
}
```

**Step 2: Create CSS file**

Create `app/src/components/NewSessionDialog/RepoOptions.css`:

```css
.repo-options {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.repo-section {
  margin-bottom: 8px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 11px;
  letter-spacing: 0.1em;
  color: var(--text-muted, #484f58);
  text-transform: uppercase;
  padding: 8px 0 4px;
  border-bottom: 1px solid var(--bg-elevated, #21262d);
}

.refresh-btn {
  background: var(--bg-elevated, #21262d);
  border: none;
  color: var(--text-muted, #484f58);
  padding: 2px 8px;
  font-family: inherit;
  font-size: 11px;
  cursor: pointer;
  border-radius: 3px;
}

.refresh-btn:hover {
  color: var(--text-primary, #e6edf3);
}

.repo-item {
  display: grid;
  grid-template-columns: 24px 1fr;
  gap: 8px;
  padding: 8px 12px;
  align-items: center;
  cursor: pointer;
  transition: background 80ms ease-out;
}

.repo-item:hover,
.repo-item.selected {
  background: rgba(88, 166, 255, 0.15);
  border-left: 2px solid var(--git-blue, #58a6ff);
  padding-left: 10px;
}

.item-icon {
  font-size: 12px;
  text-align: center;
}

.item-icon.main { color: var(--git-green, #3fb950); }
.item-icon.worktree { color: var(--git-purple, #a371f7); }
.item-icon.branch { color: var(--text-muted, #484f58); }
.item-icon.new { color: var(--git-blue, #58a6ff); }

.item-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.item-name {
  font-size: 13px;
  color: var(--text-primary, #e6edf3);
}

.item-path {
  font-size: 11px;
  color: var(--text-muted, #484f58);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.commit-info {
  display: flex;
  gap: 8px;
  font-size: 11px;
}

.commit-hash {
  color: var(--git-yellow, #d29922);
  font-family: 'JetBrains Mono', monospace;
}

.commit-time {
  color: var(--text-muted, #484f58);
}

.new-worktree-form {
  padding: 12px;
  background: var(--bg-elevated, #21262d);
}

.new-branch-input {
  width: 100%;
  background: var(--bg-deep, #0d1117);
  border: 1px solid var(--text-muted, #484f58);
  padding: 8px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 13px;
  color: var(--text-primary, #e6edf3);
  margin-bottom: 8px;
}

.new-branch-input:focus {
  outline: none;
  border-color: var(--git-blue, #58a6ff);
}

.starting-from {
  display: flex;
  gap: 12px;
  align-items: center;
  font-size: 12px;
  color: var(--text-muted, #484f58);
}

.starting-from label {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
}

.starting-from label.active {
  color: var(--text-primary, #e6edf3);
}

.repo-footer {
  display: flex;
  gap: 16px;
  padding: 8px 12px;
  border-top: 1px solid var(--bg-elevated, #21262d);
  font-size: 11px;
  color: var(--text-muted, #484f58);
}

.shortcut {
  display: flex;
  align-items: center;
  gap: 4px;
}

kbd {
  background: var(--bg-elevated, #21262d);
  padding: 2px 6px;
  border-radius: 3px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 10px;
}
```

**Step 3: Commit**

```bash
git add app/src/components/NewSessionDialog/RepoOptions.tsx
git add app/src/components/NewSessionDialog/RepoOptions.css
git commit -m "feat(ui): add RepoOptions component with branch/worktree selection"
```

---

## Task 8: Rewrite LocationPicker

**Files:**
- Rewrite: `app/src/components/LocationPicker.tsx`
- Rewrite: `app/src/components/LocationPicker.css`

**Step 1: Create new LocationPicker**

Rewrite `app/src/components/LocationPicker.tsx` to orchestrate PathInput and RepoOptions components. This is a significant rewrite - see design doc for full specifications.

Key structure:
```tsx
type Mode = 'browse' | 'repo-options';

interface State {
  mode: Mode;
  path: string;
  selectedRepo: string | null;
  repoInfo: RepoInfo | null;
}
```

**Step 2: Update CSS with terminal-native styling**

Apply the design specifications from the design doc (JetBrains Mono, dark theme, git-native colors).

**Step 3: Run frontend tests**

Run: `cd app && pnpm test`

**Step 4: Test manually**

Run: `cd app && pnpm run dev:all`
Test: Press Cmd+N, verify fish-style autocomplete and repository options

**Step 5: Commit**

```bash
git add app/src/components/LocationPicker.tsx
git add app/src/components/LocationPicker.css
git commit -m "feat(ui): rewrite LocationPicker with fish-style autocomplete and branch visibility"
```

---

## Task 9: Integration Testing

**Files:**
- Test: `app/e2e/location-picker.spec.ts` (create if needed)

**Step 1: Write E2E test for basic flow**

```typescript
test('new session dialog shows recent locations', async () => {
  // Open dialog
  await page.keyboard.press('Meta+n');

  // Verify recent locations visible
  await expect(page.getByText('RECENT')).toBeVisible();
});

test('tab completes path', async () => {
  await page.keyboard.press('Meta+n');
  await page.keyboard.type('~/pro');
  await page.keyboard.press('Tab');

  // Path should be completed
  const input = page.locator('.path-input');
  await expect(input).toHaveValue('~/projects/');
});
```

**Step 2: Run E2E tests**

Run: `cd app && pnpm run e2e`
Expected: Tests pass

**Step 3: Commit**

```bash
git add app/e2e/
git commit -m "test(e2e): add location picker tests"
```

---

## Task 10: Final Cleanup and Documentation

**Step 1: Run full test suite**

Run: `make test-all`
Expected: All tests pass

**Step 2: Update CLAUDE.md if needed**

Add any new keyboard shortcuts or commands.

**Step 3: Rebuild and install**

Run: `make install`

**Step 4: Final commit**

```bash
git add -A
git commit -m "chore: final cleanup for new session dialog"
```

---

Plan complete and saved to `docs/plans/2025-12-21-new-session-dialog-impl.md`. Two execution options:

**1. Subagent-Driven (this session)** - I dispatch fresh subagent per task, review between tasks, fast iteration

**2. Parallel Session (separate)** - Open new session with executing-plans, batch execution with checkpoints

**Which approach?**