# Git Worktree Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable seamless git worktree creation and management from the app UI, with branch display for all sessions and directory-based session grouping.

**Architecture:** Add git monitoring to daemon for branch tracking, extend Session model with branch/worktree fields, create worktree registry in SQLite, update frontend for grouping and worktree-aware LocationPicker.

**Tech Stack:** Go (daemon), React/TypeScript (frontend), SQLite (persistence), git CLI (worktree operations)

---

## Phase 1: Git Package & Branch Detection

### Task 1.1: Create git package for branch operations

**Files:**
- Create: `internal/git/git.go`
- Test: `internal/git/git_test.go`

**Step 1: Write the test file**

```go
// internal/git/git_test.go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGetBranchInfo_MainRepo(t *testing.T) {
	// Create temp git repo
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	info, err := GetBranchInfo(dir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "main" && info.Branch != "master" {
		t.Errorf("expected main or master, got %s", info.Branch)
	}
	if info.IsWorktree {
		t.Error("expected IsWorktree=false for main repo")
	}
	if info.MainRepo != "" {
		t.Error("expected MainRepo to be empty for main repo")
	}
}

func TestGetBranchInfo_Worktree(t *testing.T) {
	// Create temp git repo with worktree
	mainDir := t.TempDir()
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	wtDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, mainDir, "worktree", "add", "-b", "feature", wtDir)

	info, err := GetBranchInfo(wtDir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "feature" {
		t.Errorf("expected feature, got %s", info.Branch)
	}
	if !info.IsWorktree {
		t.Error("expected IsWorktree=true for worktree")
	}
	if info.MainRepo == "" {
		t.Error("expected MainRepo to be set for worktree")
	}
}

func TestGetBranchInfo_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	info, err := GetBranchInfo(dir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "" {
		t.Errorf("expected empty branch, got %s", info.Branch)
	}
}

func TestGetBranchInfo_DetachedHead(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	runGit(t, dir, "checkout", "--detach", "HEAD")

	info, err := GetBranchInfo(dir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	// Should return short SHA
	if len(info.Branch) < 7 {
		t.Errorf("expected short SHA for detached HEAD, got %s", info.Branch)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/git/... -v`
Expected: FAIL (package does not exist)

**Step 3: Write the implementation**

```go
// internal/git/git.go
package git

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// BranchInfo contains git branch and worktree information for a directory
type BranchInfo struct {
	Branch     string // Current branch name, or short SHA if detached
	IsWorktree bool   // True if directory is a git worktree (not main repo)
	MainRepo   string // Path to main repo if IsWorktree, empty otherwise
}

// GetBranchInfo returns branch information for a directory.
// Returns empty BranchInfo (no error) if not a git repo.
func GetBranchInfo(dir string) (*BranchInfo, error) {
	info := &BranchInfo{}

	// Check if it's a git repo
	if !isGitRepo(dir) {
		return info, nil
	}

	// Get current branch
	branch, err := getCurrentBranch(dir)
	if err != nil {
		return info, nil
	}
	info.Branch = branch

	// Check if worktree
	mainRepo, isWT := getWorktreeInfo(dir)
	info.IsWorktree = isWT
	info.MainRepo = mainRepo

	return info, nil
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func getCurrentBranch(dir string) (string, error) {
	// Try symbolic-ref first (works for normal branches)
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// Fallback to rev-parse for detached HEAD (returns short SHA)
	cmd = exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err = cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getWorktreeInfo(dir string) (mainRepo string, isWorktree bool) {
	// Get the git dir
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	gitDir := strings.TrimSpace(string(out))

	// If git dir contains "worktrees", it's a worktree
	if strings.Contains(gitDir, "worktrees") {
		// Extract main repo path from gitdir file
		// Worktree git dir is like: /path/to/main/.git/worktrees/name
		// Main repo is: /path/to/main
		parts := strings.Split(gitDir, ".git/worktrees")
		if len(parts) > 0 {
			mainRepo = strings.TrimSuffix(parts[0], "/")
			if !filepath.IsAbs(mainRepo) {
				mainRepo = filepath.Join(dir, mainRepo)
			}
			mainRepo = filepath.Clean(mainRepo)
		}
		return mainRepo, true
	}

	return "", false
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/git/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/git/
git commit -m "feat(git): add package for branch and worktree detection"
```

---

### Task 1.2: Add branch fields to Session protocol

**Files:**
- Modify: `internal/protocol/types.go:238-249`

**Step 1: Update Session struct**

Add these fields after `Directory`:

```go
// Session represents a tracked Claude session
type Session struct {
	ID             string    `json:"id"`
	Label          string    `json:"label"`
	Directory      string    `json:"directory"`
	Branch         string    `json:"branch,omitempty"`     // Current git branch
	IsWorktree     bool      `json:"is_worktree,omitempty"` // True if in a git worktree
	MainRepo       string    `json:"main_repo,omitempty"`   // Path to main repo if worktree
	State          string    `json:"state"`
	StateSince     time.Time `json:"state_since"`
	StateUpdatedAt time.Time `json:"state_updated_at"`
	Todos          []string  `json:"todos,omitempty"`
	LastSeen       time.Time `json:"last_seen"`
	Muted          bool      `json:"muted"`
}
```

**Step 2: Increment protocol version**

Change line 12:
```go
const ProtocolVersion = "4"
```

**Step 3: Add BranchChangedEvent**

Add after line 51:
```go
const (
	// ... existing events
	EventBranchChanged = "branch_changed"
)
```

**Step 4: Verify build passes**

Run: `go build ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/protocol/types.go
git commit -m "feat(protocol): add branch fields to Session, bump version to 4"
```

---

### Task 1.3: Update SQLite schema for branch fields

**Files:**
- Modify: `internal/store/sqlite.go:11-22`
- Modify: `internal/store/sqlite.go:95-118` (migrations)

**Step 1: Add migrations for new columns**

Add to migrations slice in `migrateDB`:

```go
{"SELECT branch FROM sessions LIMIT 1", "ALTER TABLE sessions ADD COLUMN branch TEXT"},
{"SELECT is_worktree FROM sessions LIMIT 1", "ALTER TABLE sessions ADD COLUMN is_worktree INTEGER NOT NULL DEFAULT 0"},
{"SELECT main_repo FROM sessions LIMIT 1", "ALTER TABLE sessions ADD COLUMN main_repo TEXT"},
```

**Step 2: Update schema constant**

Update sessions table definition:

```go
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	directory TEXT NOT NULL,
	branch TEXT,
	is_worktree INTEGER NOT NULL DEFAULT 0,
	main_repo TEXT,
	state TEXT NOT NULL DEFAULT 'idle',
	state_since TEXT NOT NULL,
	state_updated_at TEXT NOT NULL,
	todos TEXT,
	last_seen TEXT NOT NULL,
	muted INTEGER NOT NULL DEFAULT 0
);
```

**Step 3: Verify build passes**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/store/sqlite.go
git commit -m "feat(store): add branch fields to sessions schema"
```

---

### Task 1.4: Update store methods to handle branch fields

**Files:**
- Modify: `internal/store/store.go`

**Step 1: Update Add method (lines 64-87)**

Update the INSERT statement to include new fields:

```go
func (s *Store) Add(session *protocol.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	todosJSON, _ := json.Marshal(session.Todos)
	_, _ = s.db.Exec(`
		INSERT OR REPLACE INTO sessions
		(id, label, directory, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, muted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.Label,
		session.Directory,
		session.Branch,
		boolToInt(session.IsWorktree),
		session.MainRepo,
		session.State,
		session.StateSince.Format(time.RFC3339),
		session.StateUpdatedAt.Format(time.RFC3339),
		string(todosJSON),
		session.LastSeen.Format(time.RFC3339),
		boolToInt(session.Muted),
	)
}
```

**Step 2: Update Get method (lines 90-129)**

Update SELECT and scan:

```go
func (s *Store) Get(id string) *protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var session protocol.Session
	var todosJSON string
	var stateSince, stateUpdatedAt, lastSeen string
	var muted, isWorktree int
	var branch, mainRepo sql.NullString

	err := s.db.QueryRow(`
		SELECT id, label, directory, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, muted
		FROM sessions WHERE id = ?`, id).Scan(
		&session.ID,
		&session.Label,
		&session.Directory,
		&branch,
		&isWorktree,
		&mainRepo,
		&session.State,
		&stateSince,
		&stateUpdatedAt,
		&todosJSON,
		&lastSeen,
		&muted,
	)
	if err != nil {
		return nil
	}

	session.Branch = branch.String
	session.IsWorktree = isWorktree == 1
	session.MainRepo = mainRepo.String
	session.StateSince, _ = time.Parse(time.RFC3339, stateSince)
	session.StateUpdatedAt, _ = time.Parse(time.RFC3339, stateUpdatedAt)
	session.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
	session.Muted = muted == 1
	if todosJSON != "" && todosJSON != "null" {
		json.Unmarshal([]byte(todosJSON), &session.Todos)
	}

	return &session
}
```

**Step 3: Update List method (lines 156-215)**

Update SELECT and scan similarly.

**Step 4: Add UpdateBranch method**

```go
// UpdateBranch updates a session's branch information
func (s *Store) UpdateBranch(id, branch string, isWorktree bool, mainRepo string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.db.Exec(`UPDATE sessions SET branch = ?, is_worktree = ?, main_repo = ? WHERE id = ?`,
		branch, boolToInt(isWorktree), mainRepo, id)
}
```

**Step 5: Run existing tests**

Run: `go test ./internal/store/... -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/store/store.go
git commit -m "feat(store): update session methods for branch fields"
```

---

### Task 1.5: Add branch monitor to daemon

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Add import**

Add to imports:
```go
"github.com/victorarias/claude-manager/internal/git"
```

**Step 2: Add branch monitor goroutine**

Add to `Start()` after line 121 (`go d.pollPRs()`):

```go
// Start branch monitoring
go d.monitorBranches()
```

**Step 3: Implement monitorBranches**

Add new method:

```go
// monitorBranches polls git branch info for all sessions every 5 seconds
func (d *Daemon) monitorBranches() {
	d.log("Branch monitoring started (5s interval)")

	// Initial check
	d.checkAllBranches()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.checkAllBranches()
		}
	}
}

func (d *Daemon) checkAllBranches() {
	sessions := d.store.List("")
	changed := false

	for _, session := range sessions {
		info, err := git.GetBranchInfo(session.Directory)
		if err != nil {
			continue
		}

		if info.Branch != session.Branch || info.IsWorktree != session.IsWorktree {
			d.store.UpdateBranch(session.ID, info.Branch, info.IsWorktree, info.MainRepo)
			changed = true
			d.logf("Branch changed: session=%s branch=%s isWorktree=%v", session.ID, info.Branch, info.IsWorktree)
		}
	}

	if changed {
		// Broadcast all sessions with updated branch info
		sessions = d.store.List("")
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: sessions,
		})
	}
}
```

**Step 4: Update handleRegister to set initial branch**

Modify `handleRegister` (lines 249-269):

```go
func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	d.logf("session registered: id=%s label=%s dir=%s", msg.ID, msg.Label, msg.Dir)
	now := time.Now()

	// Get branch info
	branchInfo, _ := git.GetBranchInfo(msg.Dir)
	branch := ""
	isWorktree := false
	mainRepo := ""
	if branchInfo != nil {
		branch = branchInfo.Branch
		isWorktree = branchInfo.IsWorktree
		mainRepo = branchInfo.MainRepo
	}

	session := &protocol.Session{
		ID:             msg.ID,
		Label:          msg.Label,
		Directory:      msg.Dir,
		Branch:         branch,
		IsWorktree:     isWorktree,
		MainRepo:       mainRepo,
		State:          protocol.StateWaiting,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	}
	d.store.Add(session)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionRegistered,
		Session: session,
	})
}
```

**Step 5: Verify build passes**

Run: `go build ./...`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): add branch monitoring for sessions"
```

---

## Phase 2: Frontend Branch Display & Session Grouping

### Task 2.1: Update frontend types for branch fields

**Files:**
- Modify: `app/src/hooks/useDaemonSocket.ts:3-12`
- Modify: `app/src/store/sessions.ts:6-12`

**Step 1: Update DaemonSession interface**

In `useDaemonSocket.ts`:

```typescript
export interface DaemonSession {
  id: string;
  label: string;
  directory: string;
  branch?: string;
  is_worktree?: boolean;
  main_repo?: string;
  state: 'working' | 'waiting_input' | 'idle';
  state_since: string;
  todos: string[] | null;
  last_seen: string;
  muted: boolean;
}
```

**Step 2: Update protocol version**

Change line 57:
```typescript
const PROTOCOL_VERSION = '4';
```

**Step 3: Update Session interface in sessions.ts**

```typescript
export interface Session {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  terminal: Terminal | null;
  cwd: string;
  branch?: string;
  isWorktree?: boolean;
}
```

**Step 4: Verify build passes**

Run: `cd app && pnpm build`
Expected: PASS

**Step 5: Commit**

```bash
git add app/src/hooks/useDaemonSocket.ts app/src/store/sessions.ts
git commit -m "feat(app): add branch fields to session types"
```

---

### Task 2.2: Update Sidebar to display branch

**Files:**
- Modify: `app/src/components/Sidebar.tsx`
- Modify: `app/src/components/Sidebar.css`

**Step 1: Update LocalSession interface**

```typescript
interface LocalSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  branch?: string;
  isWorktree?: boolean;
}
```

**Step 2: Update SidebarProps**

```typescript
interface SidebarProps {
  sessions: LocalSession[];
  selectedId: string | null;
  collapsed: boolean;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  onGoToDashboard: () => void;
  onToggleCollapse: () => void;
}
```

**Step 3: Update session item rendering (expanded view)**

Replace the session-item div content:

```tsx
<div
  key={session.id}
  className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
  data-testid={`sidebar-session-${session.id}`}
  data-state={session.state}
  onClick={() => onSelectSession(session.id)}
>
  <span className={`state-indicator ${session.state}`} data-testid="state-indicator" />
  <span className="session-label">
    {session.label}
    {session.branch && (
      <span className="session-branch"> · {session.branch}</span>
    )}
  </span>
  {session.isWorktree && <span className="worktree-indicator">⎇</span>}
  <span className="session-shortcut">⌘{index + 1}</span>
  <button
    className="close-session-btn"
    onClick={(e) => {
      e.stopPropagation();
      onCloseSession(session.id);
    }}
    title="Close session (⌘W)"
  >
    ×
  </button>
</div>
```

**Step 4: Add CSS for branch display**

Add to `Sidebar.css`:

```css
.session-branch {
  color: #888;
  font-size: 11px;
}

.worktree-indicator {
  color: #888;
  font-size: 12px;
  margin-left: auto;
  margin-right: 4px;
}
```

**Step 5: Verify build passes**

Run: `cd app && pnpm build`
Expected: PASS

**Step 6: Commit**

```bash
git add app/src/components/Sidebar.tsx app/src/components/Sidebar.css
git commit -m "feat(sidebar): display branch and worktree indicator"
```

---

### Task 2.3: Add session grouping by directory

**Files:**
- Modify: `app/src/components/Sidebar.tsx`
- Modify: `app/src/components/Sidebar.css`

**Step 1: Create grouping utility**

Add at top of file:

```typescript
interface SessionGroup {
  directory: string;
  label: string;
  branch?: string;
  sessions: LocalSession[];
}

function groupSessionsByDirectory(sessions: LocalSession[]): SessionGroup[] {
  const groups = new Map<string, SessionGroup>();

  for (const session of sessions) {
    const key = session.cwd || session.id;
    if (!groups.has(key)) {
      groups.set(key, {
        directory: key,
        label: key.split('/').pop() || key,
        branch: session.branch,
        sessions: [],
      });
    }
    groups.get(key)!.sessions.push(session);
  }

  return Array.from(groups.values());
}
```

**Step 2: Update LocalSession to include cwd**

```typescript
interface LocalSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  branch?: string;
  isWorktree?: boolean;
  cwd?: string;
}
```

**Step 3: Update session list rendering**

Replace session-list content with grouped rendering:

```tsx
<div className="session-list">
  {groupSessionsByDirectory(sessions).map((group, groupIndex) => {
    const isSingleSession = group.sessions.length === 1;

    if (isSingleSession) {
      const session = group.sessions[0];
      const globalIndex = sessions.findIndex(s => s.id === session.id);
      return (
        <div
          key={session.id}
          className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
          data-testid={`sidebar-session-${session.id}`}
          data-state={session.state}
          onClick={() => onSelectSession(session.id)}
        >
          <span className={`state-indicator ${session.state}`} />
          <span className="session-label">
            {session.label}
            {session.branch && <span className="session-branch"> · {session.branch}</span>}
          </span>
          {session.isWorktree && <span className="worktree-indicator">⎇</span>}
          <span className="session-shortcut">⌘{globalIndex + 1}</span>
          <button
            className="close-session-btn"
            onClick={(e) => { e.stopPropagation(); onCloseSession(session.id); }}
          >×</button>
        </div>
      );
    }

    return (
      <div key={group.directory} className="session-group">
        <div className="session-group-header">
          {group.label}
          {group.branch && <span className="session-branch"> · {group.branch}</span>}
        </div>
        {group.sessions.map((session) => {
          const globalIndex = sessions.findIndex(s => s.id === session.id);
          return (
            <div
              key={session.id}
              className={`session-item grouped ${selectedId === session.id ? 'selected' : ''}`}
              data-state={session.state}
              onClick={() => onSelectSession(session.id)}
            >
              <span className={`state-indicator ${session.state}`} />
              <span className="session-label">{session.label}</span>
              {session.isWorktree && <span className="worktree-indicator">⎇</span>}
              <span className="session-shortcut">⌘{globalIndex + 1}</span>
              <button
                className="close-session-btn"
                onClick={(e) => { e.stopPropagation(); onCloseSession(session.id); }}
              >×</button>
            </div>
          );
        })}
      </div>
    );
  })}
</div>
```

**Step 4: Add CSS for grouping**

```css
.session-group {
  margin-bottom: 8px;
}

.session-group-header {
  padding: 4px 12px;
  font-size: 11px;
  color: #888;
  font-family: 'JetBrains Mono', monospace;
}

.session-item.grouped {
  padding-left: 24px;
}
```

**Step 5: Verify build passes**

Run: `cd app && pnpm build`
Expected: PASS

**Step 6: Commit**

```bash
git add app/src/components/Sidebar.tsx app/src/components/Sidebar.css
git commit -m "feat(sidebar): group sessions by directory"
```

---

## Phase 3: Worktree Registry

### Task 3.1: Add worktrees table to SQLite

**Files:**
- Modify: `internal/store/sqlite.go`

**Step 1: Add worktrees table to schema**

Add after `pr_interactions` table:

```go
CREATE TABLE IF NOT EXISTS worktrees (
	path TEXT PRIMARY KEY,
	branch TEXT NOT NULL,
	main_repo TEXT NOT NULL,
	created_at TEXT NOT NULL
);
```

**Step 2: Verify build passes**

Run: `go build ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/store/sqlite.go
git commit -m "feat(store): add worktrees table schema"
```

---

### Task 3.2: Add worktree store methods

**Files:**
- Create: `internal/store/worktree.go`
- Test: `internal/store/worktree_test.go`

**Step 1: Write test file**

```go
// internal/store/worktree_test.go
package store

import (
	"testing"
	"time"
)

func TestWorktreeStore(t *testing.T) {
	store := New()
	defer store.Close()

	wt := &Worktree{
		Path:      "/projects/repo--feature",
		Branch:    "feature/auth",
		MainRepo:  "/projects/repo",
		CreatedAt: time.Now(),
	}

	// Add
	store.AddWorktree(wt)

	// Get
	got := store.GetWorktree(wt.Path)
	if got == nil {
		t.Fatal("expected worktree, got nil")
	}
	if got.Branch != wt.Branch {
		t.Errorf("expected branch %s, got %s", wt.Branch, got.Branch)
	}

	// List by repo
	list := store.ListWorktreesByRepo(wt.MainRepo)
	if len(list) != 1 {
		t.Errorf("expected 1 worktree, got %d", len(list))
	}

	// Remove
	store.RemoveWorktree(wt.Path)
	got = store.GetWorktree(wt.Path)
	if got != nil {
		t.Error("expected nil after remove")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store/... -run TestWorktreeStore -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/store/worktree.go
package store

import (
	"time"
)

// Worktree represents a tracked git worktree
type Worktree struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	MainRepo  string    `json:"main_repo"`
	CreatedAt time.Time `json:"created_at"`
}

// AddWorktree adds a worktree to the registry
func (s *Store) AddWorktree(wt *Worktree) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, _ = s.db.Exec(`
		INSERT OR REPLACE INTO worktrees (path, branch, main_repo, created_at)
		VALUES (?, ?, ?, ?)`,
		wt.Path, wt.Branch, wt.MainRepo, wt.CreatedAt.Format(time.RFC3339),
	)
}

// GetWorktree returns a worktree by path
func (s *Store) GetWorktree(path string) *Worktree {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var wt Worktree
	var createdAt string

	err := s.db.QueryRow(`
		SELECT path, branch, main_repo, created_at
		FROM worktrees WHERE path = ?`, path).Scan(
		&wt.Path, &wt.Branch, &wt.MainRepo, &createdAt,
	)
	if err != nil {
		return nil
	}

	wt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &wt
}

// RemoveWorktree removes a worktree from the registry
func (s *Store) RemoveWorktree(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.db.Exec("DELETE FROM worktrees WHERE path = ?", path)
}

// ListWorktreesByRepo returns all worktrees for a main repo
func (s *Store) ListWorktreesByRepo(mainRepo string) []*Worktree {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT path, branch, main_repo, created_at
		FROM worktrees WHERE main_repo = ?`, mainRepo)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*Worktree
	for rows.Next() {
		var wt Worktree
		var createdAt string

		err := rows.Scan(&wt.Path, &wt.Branch, &wt.MainRepo, &createdAt)
		if err != nil {
			continue
		}

		wt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		result = append(result, &wt)
	}

	return result
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store/... -run TestWorktreeStore -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/worktree.go internal/store/worktree_test.go
git commit -m "feat(store): add worktree registry methods"
```

---

### Task 3.3: Add git worktree operations

**Files:**
- Create: `internal/git/worktree.go`
- Test: `internal/git/worktree_test.go`

**Step 1: Write test file**

```go
// internal/git/worktree_test.go
package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListWorktrees(t *testing.T) {
	mainDir := t.TempDir()
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	// Create a worktree
	wtDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, mainDir, "worktree", "add", "-b", "feature", wtDir)

	worktrees, err := ListWorktrees(mainDir)
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}

	// Should have 2: main + worktree
	if len(worktrees) < 1 {
		t.Errorf("expected at least 1 worktree, got %d", len(worktrees))
	}

	// Find the feature worktree
	found := false
	for _, wt := range worktrees {
		if wt.Branch == "feature" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find feature worktree")
	}
}

func TestCreateWorktree(t *testing.T) {
	mainDir := t.TempDir()
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	wtDir := filepath.Join(t.TempDir(), "new-wt")
	err := CreateWorktree(mainDir, "new-feature", wtDir)
	if err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}

	// Verify worktree exists
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}

	// Verify branch
	info, err := GetBranchInfo(wtDir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "new-feature" {
		t.Errorf("expected branch new-feature, got %s", info.Branch)
	}
}

func TestDeleteWorktree(t *testing.T) {
	mainDir := t.TempDir()
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	wtDir := filepath.Join(t.TempDir(), "wt-to-delete")
	runGit(t, mainDir, "worktree", "add", "-b", "temp", wtDir)

	err := DeleteWorktree(mainDir, wtDir)
	if err != nil {
		t.Fatalf("DeleteWorktree failed: %v", err)
	}

	// Directory might still exist but shouldn't be a worktree
	worktrees, _ := ListWorktrees(mainDir)
	for _, wt := range worktrees {
		if wt.Path == wtDir {
			t.Error("worktree should have been removed")
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/git/... -run TestListWorktrees -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/git/worktree.go
package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeEntry represents a git worktree from `git worktree list`
type WorktreeEntry struct {
	Path   string
	Branch string
}

// ListWorktrees returns all worktrees for a repository
func ListWorktrees(repoDir string) ([]WorktreeEntry, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var worktrees []WorktreeEntry
	var current WorktreeEntry

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = WorktreeEntry{Path: strings.TrimPrefix(line, "worktree ")}
		} else if strings.HasPrefix(line, "branch refs/heads/") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}

	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// CreateWorktree creates a new worktree with a new branch
func CreateWorktree(repoDir, branch, path string) error {
	cmd := exec.Command("git", "worktree", "add", "-b", branch, path)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// CreateWorktreeFromBranch creates a worktree from an existing branch
func CreateWorktreeFromBranch(repoDir, branch, path string) error {
	cmd := exec.Command("git", "worktree", "add", path, branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// DeleteWorktree removes a worktree
func DeleteWorktree(repoDir, path string) error {
	cmd := exec.Command("git", "worktree", "remove", path)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove failed: %s", out)
	}
	return nil
}

// GenerateWorktreePath generates a worktree path as sibling to main repo
func GenerateWorktreePath(mainRepo, branch string) string {
	repoName := filepath.Base(mainRepo)
	safeBranch := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(filepath.Dir(mainRepo), repoName+"--"+safeBranch)
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/git/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/git/worktree.go internal/git/worktree_test.go
git commit -m "feat(git): add worktree list, create, delete operations"
```

---

## Phase 4: Protocol & Daemon Worktree Commands

### Task 4.1: Add worktree protocol messages

**Files:**
- Modify: `internal/protocol/types.go`

**Step 1: Add command constants**

Add to Commands section:

```go
CmdListWorktrees   = "list_worktrees"
CmdCreateWorktree  = "create_worktree"
CmdDeleteWorktree  = "delete_worktree"
```

**Step 2: Add event constants**

```go
EventWorktreeCreated = "worktree_created"
EventWorktreeDeleted = "worktree_deleted"
EventWorktreesUpdated = "worktrees_updated"
```

**Step 3: Add message types**

```go
// ListWorktreesMessage requests worktrees for a repo
type ListWorktreesMessage struct {
	Cmd      string `json:"cmd"`
	MainRepo string `json:"main_repo"`
}

// CreateWorktreeMessage creates a new worktree
type CreateWorktreeMessage struct {
	Cmd      string `json:"cmd"`
	MainRepo string `json:"main_repo"`
	Branch   string `json:"branch"`
	Path     string `json:"path,omitempty"` // Auto-generated if empty
}

// DeleteWorktreeMessage removes a worktree
type DeleteWorktreeMessage struct {
	Cmd  string `json:"cmd"`
	Path string `json:"path"`
}

// WorktreeCreatedEvent is broadcast when a worktree is created
type WorktreeCreatedEvent struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	MainRepo string `json:"main_repo"`
}
```

**Step 4: Add Worktree to WebSocketEvent**

Update WebSocketEvent struct:

```go
type WebSocketEvent struct {
	Event           string       `json:"event"`
	ProtocolVersion string       `json:"protocol_version,omitempty"`
	Session         *Session     `json:"session,omitempty"`
	Sessions        []*Session   `json:"sessions,omitempty"`
	PRs             []*PR        `json:"prs,omitempty"`
	Repos           []*RepoState `json:"repos,omitempty"`
	Worktrees       []*Worktree  `json:"worktrees,omitempty"` // Add this
}

// Worktree for protocol (matches store.Worktree)
type Worktree struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	MainRepo  string `json:"main_repo"`
	CreatedAt string `json:"created_at"`
}
```

**Step 5: Add to ParseMessage switch**

Add cases for new commands.

**Step 6: Commit**

```bash
git add internal/protocol/types.go
git commit -m "feat(protocol): add worktree commands and events"
```

---

### Task 4.2: Add daemon worktree handlers

**Files:**
- Modify: `internal/daemon/daemon.go`
- Create: `internal/daemon/worktree.go`

**Step 1: Create worktree handlers file**

```go
// internal/daemon/worktree.go
package daemon

import (
	"net"
	"time"

	"github.com/victorarias/claude-manager/internal/git"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
)

func (d *Daemon) handleListWorktrees(conn net.Conn, msg *protocol.ListWorktreesMessage) {
	// Get from registry first
	worktrees := d.store.ListWorktreesByRepo(msg.MainRepo)

	// Also scan git for any we don't have
	gitWorktrees, err := git.ListWorktrees(msg.MainRepo)
	if err == nil {
		for _, gwt := range gitWorktrees {
			// Skip main repo
			if gwt.Path == msg.MainRepo {
				continue
			}
			// Add if not in registry
			found := false
			for _, wt := range worktrees {
				if wt.Path == gwt.Path {
					found = true
					break
				}
			}
			if !found {
				newWt := &store.Worktree{
					Path:      gwt.Path,
					Branch:    gwt.Branch,
					MainRepo:  msg.MainRepo,
					CreatedAt: time.Now(),
				}
				d.store.AddWorktree(newWt)
				worktrees = append(worktrees, newWt)
			}
		}
	}

	// Convert to protocol type
	protoWorktrees := make([]*protocol.Worktree, len(worktrees))
	for i, wt := range worktrees {
		protoWorktrees[i] = &protocol.Worktree{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: wt.CreatedAt.Format(time.RFC3339),
		}
	}

	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: protoWorktrees,
	})
	d.sendOK(conn)
}

func (d *Daemon) handleCreateWorktree(conn net.Conn, msg *protocol.CreateWorktreeMessage) {
	path := msg.Path
	if path == "" {
		path = git.GenerateWorktreePath(msg.MainRepo, msg.Branch)
	}

	// Create the worktree
	err := git.CreateWorktree(msg.MainRepo, msg.Branch, path)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	// Register in store
	wt := &store.Worktree{
		Path:      path,
		Branch:    msg.Branch,
		MainRepo:  msg.MainRepo,
		CreatedAt: time.Now(),
	}
	d.store.AddWorktree(wt)

	d.sendOK(conn)

	// Broadcast
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventWorktreeCreated,
		Worktrees: []*protocol.Worktree{{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: wt.CreatedAt.Format(time.RFC3339),
		}},
	})
}

func (d *Daemon) handleDeleteWorktree(conn net.Conn, msg *protocol.DeleteWorktreeMessage) {
	wt := d.store.GetWorktree(msg.Path)
	if wt == nil {
		d.sendError(conn, "worktree not found in registry")
		return
	}

	// Delete the worktree
	err := git.DeleteWorktree(wt.MainRepo, msg.Path)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	// Remove from store
	d.store.RemoveWorktree(msg.Path)

	d.sendOK(conn)

	// Broadcast
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventWorktreeDeleted,
		Worktrees: []*protocol.Worktree{{
			Path: msg.Path,
		}},
	})
}
```

**Step 2: Add handlers to daemon.go handleConnection**

Add to switch statement:

```go
case protocol.CmdListWorktrees:
	d.handleListWorktrees(conn, msg.(*protocol.ListWorktreesMessage))
case protocol.CmdCreateWorktree:
	d.handleCreateWorktree(conn, msg.(*protocol.CreateWorktreeMessage))
case protocol.CmdDeleteWorktree:
	d.handleDeleteWorktree(conn, msg.(*protocol.DeleteWorktreeMessage))
```

**Step 3: Verify build passes**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/worktree.go
git commit -m "feat(daemon): add worktree command handlers"
```

---

## Phase 5: Frontend Worktree Integration

### Task 5.1: Add worktree hook to useDaemonSocket

**Files:**
- Modify: `app/src/hooks/useDaemonSocket.ts`

**Step 1: Add Worktree interface**

```typescript
export interface DaemonWorktree {
  path: string;
  branch: string;
  main_repo: string;
  created_at: string;
}
```

**Step 2: Add worktree event handlers**

Add cases to onmessage switch:

```typescript
case 'worktrees_updated':
  if (data.worktrees) {
    worktreesRef.current = data.worktrees;
    onWorktreesUpdate?.(data.worktrees);
  }
  break;

case 'worktree_created':
case 'worktree_deleted':
  // Handled via worktrees_updated
  break;
```

**Step 3: Add sendListWorktrees method**

```typescript
const sendListWorktrees = useCallback((mainRepo: string) => {
  const ws = wsRef.current;
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ cmd: 'list_worktrees', main_repo: mainRepo }));
}, []);

const sendCreateWorktree = useCallback((mainRepo: string, branch: string, path?: string): Promise<PRActionResult> => {
  return new Promise((resolve, reject) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error('WebSocket not connected'));
      return;
    }

    ws.send(JSON.stringify({
      cmd: 'create_worktree',
      main_repo: mainRepo,
      branch,
      ...(path && { path }),
    }));

    // Simple resolve - could add result event handling
    setTimeout(() => resolve({ success: true }), 100);
  });
}, []);
```

**Step 4: Commit**

```bash
git add app/src/hooks/useDaemonSocket.ts
git commit -m "feat(app): add worktree methods to daemon socket hook"
```

---

### Task 5.2: Update LocationPicker with worktree selection

**Files:**
- Modify: `app/src/components/LocationPicker.tsx`
- Modify: `app/src/components/LocationPicker.css`

This is a larger task - the full implementation would extend LocationPicker to:
1. Detect when selected path is a git repo
2. Show existing worktrees for that repo
3. Add "New branch" option
4. Support keyboard navigation with number shortcuts

**Step 1: Add worktree state**

```typescript
const [worktrees, setWorktrees] = useState<DaemonWorktree[]>([]);
const [showWorktreeOptions, setShowWorktreeOptions] = useState(false);
const [newBranchMode, setNewBranchMode] = useState(false);
const [newBranchName, setNewBranchName] = useState('');
```

**Step 2: Add worktree section to results**

(Full implementation in actual code)

**Step 3: Add keyboard handling for number shortcuts**

(Full implementation in actual code)

**Step 4: Commit**

```bash
git add app/src/components/LocationPicker.tsx app/src/components/LocationPicker.css
git commit -m "feat(location-picker): add worktree selection and new branch mode"
```

---

## Phase 6: PR "Open" Action

### Task 6.1: Add Open button to PR cards

**Files:**
- Modify: `app/src/components/PRActions.tsx`
- Modify: `app/src/components/PRActions.css`

**Step 1: Add Open button**

Add before Approve button:

```tsx
<button
  className="action-btn open"
  onClick={() => onOpen?.(pr)}
  title="Open in worktree"
>
  Open
</button>
```

**Step 2: Add handler prop**

```typescript
interface PRActionsProps {
  // ... existing props
  onOpen?: (pr: DaemonPR) => void;
}
```

**Step 3: Add CSS**

```css
.action-btn.open:hover {
  background: rgba(59, 130, 246, 0.1);
  color: #3b82f6;
}
```

**Step 4: Commit**

```bash
git add app/src/components/PRActions.tsx app/src/components/PRActions.css
git commit -m "feat(pr-actions): add Open button for worktree creation"
```

---

### Task 6.2: Implement PR open handler in App.tsx

**Files:**
- Modify: `app/src/App.tsx`

**Step 1: Add handler**

```typescript
const handleOpenPR = useCallback(async (pr: DaemonPR) => {
  // Extract branch from PR (would need to fetch from GitHub)
  // For now, use PR number as branch identifier
  const branch = `pr-${pr.number}`;

  // Get main repo path (would need to map repo to local path)
  // This requires additional configuration

  // Create worktree
  // const result = await sendCreateWorktree(mainRepo, branch);

  // Create session in worktree
  // const sessionId = await createSession(pr.title, worktreePath);
}, []);
```

**Note:** Full implementation requires:
1. Mapping GitHub repo (owner/repo) to local path
2. Fetching PR branch name from GitHub
3. Creating worktree if needed, or reusing existing
4. Creating session in worktree directory

**Step 2: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat(app): add PR open handler for worktree sessions"
```

---

## Testing & Verification

After each phase:

1. Run Go tests: `go test ./... -v`
2. Run frontend build: `cd app && pnpm build`
3. Manual test: `make install && cd app && pnpm run dev:all`
4. Verify in UI:
   - Sessions show branch names
   - Sessions grouped by directory
   - Worktree indicator (⎇) appears for worktree sessions
   - LocationPicker shows worktrees for git repos
