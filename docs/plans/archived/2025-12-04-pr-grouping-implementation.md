# PR Grouping Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add repository grouping, repo-level muting, and PR title display to the dashboard.

**Architecture:** Refactor store for async persistence with dirty flag, add RepoState for repo-level settings, update dashboard to render grouped/collapsible PRs, update tmux status format.

**Tech Stack:** Go, Bubbletea, Lipgloss

---

### Task 1: Async Persistence - Dirty Flag

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestStore_DirtyFlag(t *testing.T) {
	s := New()

	if s.IsDirty() {
		t.Error("new store should not be dirty")
	}

	s.Add(&protocol.Session{ID: "test", Label: "test"})

	if !s.IsDirty() {
		t.Error("store should be dirty after Add")
	}

	s.ClearDirty()

	if s.IsDirty() {
		t.Error("store should not be dirty after ClearDirty")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store -run TestStore_DirtyFlag -v`
Expected: FAIL with "s.IsDirty undefined"

**Step 3: Write minimal implementation**

Add to `internal/store/store.go` struct:

```go
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*protocol.Session
	prs      map[string]*protocol.PR
	path     string
	dirty    bool  // ADD THIS
}
```

Add methods:

```go
// IsDirty returns whether the store has unsaved changes
func (s *Store) IsDirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dirty
}

// ClearDirty clears the dirty flag (called after successful save)
func (s *Store) ClearDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
}

// markDirty sets the dirty flag
func (s *Store) markDirty() {
	s.dirty = true
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store -run TestStore_DirtyFlag -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): add dirty flag for async persistence"
```

---

### Task 2: Replace save() Calls with markDirty()

**Files:**
- Modify: `internal/store/store.go`

**Step 1: Update all state-changing methods**

Replace `s.save()` with `s.markDirty()` in these methods:
- `Add()`
- `Remove()`
- `UpdateState()`
- `UpdateTodos()`
- `ToggleMute()`
- `SetPRs()`
- `ToggleMutePR()`

Example change in `Add()`:

```go
func (s *Store) Add(session *protocol.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
	s.markDirty()  // was: s.save()
}
```

**Step 2: Add Save() as public method**

Rename `save()` to `Save()` (public) so daemon can call it:

```go
// Save persists state to disk if path is configured
func (s *Store) Save() {
	if s.path == "" {
		return
	}

	s.mu.RLock()
	state := persistedState{
		Sessions: make([]*protocol.Session, 0, len(s.sessions)),
		PRs:      make([]*protocol.PR, 0, len(s.prs)),
	}
	for _, session := range s.sessions {
		state.Sessions = append(state.Sessions, session)
	}
	for _, pr := range s.prs {
		state.PRs = append(state.PRs, pr)
	}
	s.mu.RUnlock()

	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	os.WriteFile(s.path, data, 0600)
}
```

**Step 3: Run all store tests**

Run: `go test ./internal/store -v`
Expected: All PASS

**Step 4: Commit**

```bash
git add internal/store/store.go
git commit -m "refactor(store): use markDirty instead of immediate save"
```

---

### Task 3: Background Persistence Goroutine

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/daemon/daemon.go`
- Test: `internal/store/store_test.go`

**Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestStore_BackgroundPersistence(t *testing.T) {
	// Create temp file for state
	tmpFile, err := os.CreateTemp("", "store-test-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	s := NewWithPersistence(tmpFile.Name())
	done := make(chan struct{})

	// Start background persistence with short interval
	go s.StartPersistence(50*time.Millisecond, done)

	// Add a session (marks dirty)
	s.Add(&protocol.Session{ID: "bg-test", Label: "bg-test"})

	// Wait for persistence to run
	time.Sleep(100 * time.Millisecond)

	// Stop persistence
	close(done)

	// Verify file was written
	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	if !strings.Contains(string(data), "bg-test") {
		t.Error("state file should contain bg-test session")
	}

	// Verify dirty flag was cleared
	if s.IsDirty() {
		t.Error("dirty flag should be cleared after save")
	}
}
```

Add import `"strings"` if not present.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store -run TestStore_BackgroundPersistence -v`
Expected: FAIL with "s.StartPersistence undefined"

**Step 3: Write implementation**

Add to `internal/store/store.go`:

```go
// StartPersistence runs a background loop that saves state when dirty
func (s *Store) StartPersistence(interval time.Duration, done <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			// Final save on shutdown
			if s.IsDirty() {
				s.Save()
				s.ClearDirty()
			}
			return
		case <-ticker.C:
			if s.IsDirty() {
				s.Save()
				s.ClearDirty()
			}
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store -run TestStore_BackgroundPersistence -v`
Expected: PASS

**Step 5: Integrate into daemon**

Modify `internal/daemon/daemon.go` - update `Start()`:

```go
func (d *Daemon) Start() error {
	// Remove stale socket
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	d.log("daemon started")

	// Start background persistence (3 second interval)
	go d.store.StartPersistence(3*time.Second, d.done)

	// Start PR polling
	go d.pollPRs()

	// ... rest of Start() unchanged
```

**Step 6: Run all tests**

Run: `go test ./... -v`
Expected: All PASS

**Step 7: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go internal/daemon/daemon.go
git commit -m "feat(store): add background persistence with dirty flag"
```

---

### Task 4: Add RepoState to Protocol

**Files:**
- Modify: `internal/protocol/types.go`

**Step 1: Add RepoState type and commands**

Add to `internal/protocol/types.go`:

```go
// RepoState tracks per-repo UI state
type RepoState struct {
	Repo      string `json:"repo"`
	Muted     bool   `json:"muted"`
	Collapsed bool   `json:"collapsed"`
}

// Command constants - add these
const (
	// ... existing commands ...
	CmdMuteRepo     = "mute_repo"
	CmdCollapseRepo = "collapse_repo"
	CmdQueryRepos   = "query_repos"
)

// MuteRepoMessage toggles repo muted state
type MuteRepoMessage struct {
	Repo string `json:"repo"`
}

// CollapseRepoMessage sets repo collapsed state
type CollapseRepoMessage struct {
	Repo      string `json:"repo"`
	Collapsed bool   `json:"collapsed"`
}

// QueryReposMessage requests repo states
type QueryReposMessage struct {
	Filter string `json:"filter,omitempty"`
}
```

**Step 2: Update Response type**

Add to Response struct:

```go
type Response struct {
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
	Sessions []*Session `json:"sessions,omitempty"`
	PRs      []*PR      `json:"prs,omitempty"`
	Repos    []*RepoState `json:"repos,omitempty"`  // ADD THIS
}
```

**Step 3: Update ParseMessage**

Add cases to `ParseMessage()`:

```go
case CmdMuteRepo:
	var msg MuteRepoMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", nil, err
	}
	return cmd, &msg, nil
case CmdCollapseRepo:
	var msg CollapseRepoMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", nil, err
	}
	return cmd, &msg, nil
case CmdQueryRepos:
	var msg QueryReposMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", nil, err
	}
	return cmd, &msg, nil
```

**Step 4: Run protocol tests**

Run: `go test ./internal/protocol -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/protocol/types.go
git commit -m "feat(protocol): add RepoState type and commands"
```

---

### Task 5: Add RepoState to Store

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Step 1: Write failing tests**

Add to `internal/store/store_test.go`:

```go
func TestStore_RepoState(t *testing.T) {
	s := New()

	// Initially no repo state
	state := s.GetRepoState("owner/repo")
	if state != nil {
		t.Error("expected nil for unknown repo")
	}

	// Toggle mute creates state
	s.ToggleMuteRepo("owner/repo")
	state = s.GetRepoState("owner/repo")
	if state == nil {
		t.Fatal("expected repo state after toggle")
	}
	if !state.Muted {
		t.Error("repo should be muted")
	}

	// Toggle again unmutes
	s.ToggleMuteRepo("owner/repo")
	state = s.GetRepoState("owner/repo")
	if state.Muted {
		t.Error("repo should be unmuted")
	}

	// Set collapsed
	s.SetRepoCollapsed("owner/repo", true)
	state = s.GetRepoState("owner/repo")
	if !state.Collapsed {
		t.Error("repo should be collapsed")
	}
}

func TestStore_ListRepoStates(t *testing.T) {
	s := New()

	s.ToggleMuteRepo("repo-a")
	s.SetRepoCollapsed("repo-b", true)

	states := s.ListRepoStates()
	if len(states) != 2 {
		t.Errorf("expected 2 repo states, got %d", len(states))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store -run TestStore_RepoState -v`
Expected: FAIL

**Step 3: Add repos map and methods**

Update Store struct:

```go
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*protocol.Session
	prs      map[string]*protocol.PR
	repos    map[string]*protocol.RepoState  // ADD THIS
	path     string
	dirty    bool
}
```

Update `New()` and `NewWithPersistence()`:

```go
func New() *Store {
	return &Store{
		sessions: make(map[string]*protocol.Session),
		prs:      make(map[string]*protocol.PR),
		repos:    make(map[string]*protocol.RepoState),  // ADD THIS
	}
}

func NewWithPersistence(path string) *Store {
	s := &Store{
		sessions: make(map[string]*protocol.Session),
		prs:      make(map[string]*protocol.PR),
		repos:    make(map[string]*protocol.RepoState),  // ADD THIS
		path:     path,
	}
	s.Load()
	return s
}
```

Add methods:

```go
// GetRepoState returns the state for a repo, or nil if not set
func (s *Store) GetRepoState(repo string) *protocol.RepoState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repos[repo]
}

// ToggleMuteRepo toggles a repo's muted state
func (s *Store) ToggleMuteRepo(repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.repos[repo]
	if !ok {
		state = &protocol.RepoState{Repo: repo}
		s.repos[repo] = state
	}
	state.Muted = !state.Muted
	s.markDirty()
}

// SetRepoCollapsed sets a repo's collapsed state
func (s *Store) SetRepoCollapsed(repo string, collapsed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.repos[repo]
	if !ok {
		state = &protocol.RepoState{Repo: repo}
		s.repos[repo] = state
	}
	state.Collapsed = collapsed
	s.markDirty()
}

// ListRepoStates returns all repo states
func (s *Store) ListRepoStates() []*protocol.RepoState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*protocol.RepoState, 0, len(s.repos))
	for _, state := range s.repos {
		result = append(result, state)
	}
	return result
}
```

**Step 4: Update persistence**

Update `persistedState`:

```go
type persistedState struct {
	Sessions []*protocol.Session   `json:"sessions"`
	PRs      []*protocol.PR        `json:"prs,omitempty"`
	Repos    []*protocol.RepoState `json:"repos,omitempty"`  // ADD THIS
}
```

Update `Load()` to load repos:

```go
// In Load(), after loading PRs:
for _, repo := range state.Repos {
	s.repos[repo.Repo] = repo
}
```

Update `Save()` to save repos:

```go
// In Save(), building state:
state := persistedState{
	Sessions: make([]*protocol.Session, 0, len(s.sessions)),
	PRs:      make([]*protocol.PR, 0, len(s.prs)),
	Repos:    make([]*protocol.RepoState, 0, len(s.repos)),  // ADD THIS
}
// ... existing session/PR loops ...
for _, repo := range s.repos {
	state.Repos = append(state.Repos, repo)
}
```

**Step 5: Run tests**

Run: `go test ./internal/store -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): add RepoState storage"
```

---

### Task 6: Add Repo Handlers to Daemon

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Add handler cases**

Add to `handleConnection()` switch:

```go
case protocol.CmdMuteRepo:
	d.handleMuteRepo(conn, msg.(*protocol.MuteRepoMessage))
case protocol.CmdCollapseRepo:
	d.handleCollapseRepo(conn, msg.(*protocol.CollapseRepoMessage))
case protocol.CmdQueryRepos:
	d.handleQueryRepos(conn, msg.(*protocol.QueryReposMessage))
```

**Step 2: Add handler methods**

```go
func (d *Daemon) handleMuteRepo(conn net.Conn, msg *protocol.MuteRepoMessage) {
	d.store.ToggleMuteRepo(msg.Repo)
	d.sendOK(conn)
}

func (d *Daemon) handleCollapseRepo(conn net.Conn, msg *protocol.CollapseRepoMessage) {
	d.store.SetRepoCollapsed(msg.Repo, msg.Collapsed)
	d.sendOK(conn)
}

func (d *Daemon) handleQueryRepos(conn net.Conn, msg *protocol.QueryReposMessage) {
	repos := d.store.ListRepoStates()
	resp := protocol.Response{
		OK:    true,
		Repos: repos,
	}
	json.NewEncoder(conn).Encode(resp)
}
```

**Step 3: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 4: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): add repo state handlers"
```

---

### Task 7: Add Repo Methods to Client

**Files:**
- Modify: `internal/client/client.go`

**Step 1: Add client methods**

```go
// ToggleMuteRepo toggles a repo's muted state
func (c *Client) ToggleMuteRepo(repo string) error {
	msg := map[string]string{
		"cmd":  protocol.CmdMuteRepo,
		"repo": repo,
	}
	return c.sendAndCheck(msg)
}

// SetRepoCollapsed sets a repo's collapsed state
func (c *Client) SetRepoCollapsed(repo string, collapsed bool) error {
	msg := map[string]interface{}{
		"cmd":       protocol.CmdCollapseRepo,
		"repo":      repo,
		"collapsed": collapsed,
	}
	return c.sendAndCheck(msg)
}

// QueryRepos returns all repo states
func (c *Client) QueryRepos() ([]*protocol.RepoState, error) {
	msg := map[string]string{
		"cmd": protocol.CmdQueryRepos,
	}
	resp, err := c.sendAndReceive(msg)
	if err != nil {
		return nil, err
	}
	return resp.Repos, nil
}
```

**Step 2: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 3: Commit**

```bash
git add internal/client/client.go
git commit -m "feat(client): add repo state methods"
```

---

### Task 8: Dashboard - Repo Grouping Data Model

**Files:**
- Modify: `internal/dashboard/model.go`

**Step 1: Update Model struct**

Add fields for repo grouping:

```go
type Model struct {
	client          *client.Client
	sessions        []*protocol.Session
	prs             []*protocol.PR
	repoStates      map[string]*protocol.RepoState
	cursor          int
	prCursor        int       // now indexes into flattened view
	focusPane       int
	showMutedPRs    bool
	showMutedRepos  bool      // ADD THIS
	err             error
	currentSession  string
}
```

**Step 2: Add repoGroup helper type**

```go
// repoGroup represents a repository with its PRs
type repoGroup struct {
	name      string
	prs       []*protocol.PR
	muted     bool
	collapsed bool
}
```

**Step 3: Add method to build repo groups**

```go
// buildRepoGroups groups PRs by repository
func (m *Model) buildRepoGroups() []*repoGroup {
	// Group PRs by repo
	grouped := make(map[string][]*protocol.PR)
	for _, pr := range m.prs {
		// Skip muted PRs if not showing them
		if pr.Muted && !m.showMutedPRs {
			continue
		}
		grouped[pr.Repo] = append(grouped[pr.Repo], pr)
	}

	// Build sorted list of groups
	var repos []string
	for repo := range grouped {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	var groups []*repoGroup
	for _, repo := range repos {
		state := m.repoStates[repo]
		muted := state != nil && state.Muted
		collapsed := state == nil || state.Collapsed  // default collapsed

		// Skip muted repos if not showing them
		if muted && !m.showMutedRepos {
			continue
		}

		groups = append(groups, &repoGroup{
			name:      repo,
			prs:       grouped[repo],
			muted:     muted,
			collapsed: collapsed,
		})
	}

	return groups
}
```

**Step 4: Update refresh to fetch repo states**

```go
func (m *Model) refresh() tea.Msg {
	if m.client == nil {
		return sessionsMsg{sessions: nil, prs: nil, repos: nil}
	}
	sessions, err := m.client.Query("")
	if err != nil {
		return errMsg{err: err}
	}
	prs, _ := m.client.QueryPRs("")
	repos, _ := m.client.QueryRepos()
	return sessionsMsg{sessions: sessions, prs: prs, repos: repos}
}

type sessionsMsg struct {
	sessions []*protocol.Session
	prs      []*protocol.PR
	repos    []*protocol.RepoState
}
```

**Step 5: Update Update() to handle repos**

In the `case sessionsMsg:` block:

```go
case sessionsMsg:
	m.sessions = msg.sessions
	m.prs = msg.prs
	// Build repo states map
	m.repoStates = make(map[string]*protocol.RepoState)
	for _, repo := range msg.repos {
		m.repoStates[repo.Repo] = repo
	}
	m.err = nil
	// ... cursor validation ...
```

**Step 6: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 7: Commit**

```bash
git add internal/dashboard/model.go
git commit -m "feat(dashboard): add repo grouping data model"
```

---

### Task 9: Dashboard - Render Repo Groups

**Files:**
- Modify: `internal/dashboard/model.go`

**Step 1: Rewrite buildPRsContent()**

Replace the existing `buildPRsContent()` with grouped version:

```go
func (m *Model) buildPRsContent() string {
	var lines []string
	groups := m.buildRepoGroups()

	totalPRs := 0
	for _, g := range groups {
		totalPRs += len(g.prs)
	}

	lines = append(lines, headerStyle.Render(fmt.Sprintf("Pull Requests (%d)", totalPRs)))
	lines = append(lines, "")

	if len(groups) == 0 {
		if len(m.prs) == 0 {
			lines = append(lines, grayStyle.Render("  No PRs (gh CLI?)"))
		} else {
			lines = append(lines, grayStyle.Render("  All PRs muted"))
		}
		return strings.Join(lines, "\n")
	}

	prIndex := 0
	for _, group := range groups {
		// Render repo header
		cursor := "  "
		if m.focusPane == 1 && m.prCursor == prIndex {
			cursor = "> "
		}

		icon := "▶"
		if !group.collapsed {
			icon = "▼"
		}

		// Short repo name
		repoShort := group.name
		if idx := strings.LastIndex(group.name, "/"); idx >= 0 {
			repoShort = group.name[idx+1:]
		}

		style := lipgloss.NewStyle()
		if group.muted {
			style = grayStyle
		}

		repoLine := fmt.Sprintf("%s%s %s (%d)", cursor, icon, repoShort, len(group.prs))
		lines = append(lines, style.Render(repoLine))
		prIndex++

		// Render PRs if expanded
		if !group.collapsed {
			for _, pr := range group.prs {
				cursor := "    "  // extra indent for PRs
				if m.focusPane == 1 && m.prCursor == prIndex {
					cursor = "  > "
				}

				var style lipgloss.Style
				var stateStr string
				if pr.Muted {
					style = grayStyle
					stateStr = "muted"
				} else if pr.State == protocol.StateWaiting {
					style = yellowStyle
					switch pr.Reason {
					case protocol.PRReasonReadyToMerge:
						stateStr = "merge"
					case protocol.PRReasonCIFailed:
						stateStr = "fix"
					case protocol.PRReasonChangesRequested:
						stateStr = "fix"
					case protocol.PRReasonReviewNeeded:
						stateStr = "review"
					default:
						stateStr = "open"
					}
				} else {
					style = greenStyle
					stateStr = "wait"
				}

				prLine := fmt.Sprintf("%s#%d %s", cursor, pr.Number, stateStr)
				lines = append(lines, style.Render(prLine))

				// PR title on next line(s), wrapped
				titleLines := wrapText(pr.Title, paneWidth-6)
				for _, tl := range titleLines {
					lines = append(lines, grayStyle.Render("      "+tl))
				}

				prIndex++
			}
		}
	}

	return strings.Join(lines, "\n")
}

// wrapText wraps text to maxWidth, returning lines
func wrapText(text string, maxWidth int) []string {
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	for len(text) > maxWidth {
		// Find last space before maxWidth
		breakAt := maxWidth
		for i := maxWidth - 1; i > 0; i-- {
			if text[i] == ' ' {
				breakAt = i
				break
			}
		}
		lines = append(lines, text[:breakAt])
		text = strings.TrimLeft(text[breakAt:], " ")
	}
	if len(text) > 0 {
		lines = append(lines, text)
	}
	return lines
}
```

**Step 2: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 3: Commit**

```bash
git add internal/dashboard/model.go
git commit -m "feat(dashboard): render grouped PRs with titles"
```

---

### Task 10: Dashboard - Cursor Navigation

**Files:**
- Modify: `internal/dashboard/model.go`

**Step 1: Add helper to count visible items**

```go
// countPRItems returns total navigable items in PR pane
func (m *Model) countPRItems() int {
	groups := m.buildRepoGroups()
	count := 0
	for _, g := range groups {
		count++ // repo header
		if !g.collapsed {
			count += len(g.prs) // individual PRs
		}
	}
	return count
}

// getPRItemAt returns what's at the given cursor position
// Returns: (isRepo bool, repoName string, pr *protocol.PR)
func (m *Model) getPRItemAt(cursor int) (bool, string, *protocol.PR) {
	groups := m.buildRepoGroups()
	idx := 0
	for _, g := range groups {
		if idx == cursor {
			return true, g.name, nil
		}
		idx++
		if !g.collapsed {
			for _, pr := range g.prs {
				if idx == cursor {
					return false, g.name, pr
				}
				idx++
			}
		}
	}
	return false, "", nil
}
```

**Step 2: Update movePRCursor**

```go
func (m *Model) movePRCursor(delta int) {
	m.prCursor += delta
	maxItems := m.countPRItems()
	if m.prCursor < 0 {
		m.prCursor = 0
	}
	if m.prCursor >= maxItems && maxItems > 0 {
		m.prCursor = maxItems - 1
	}
}
```

**Step 3: Update SelectedPR**

```go
// SelectedPR returns the currently selected PR, or nil if on a repo header
func (m *Model) SelectedPR() *protocol.PR {
	if m.focusPane != 1 {
		return nil
	}
	isRepo, _, pr := m.getPRItemAt(m.prCursor)
	if isRepo {
		return nil
	}
	return pr
}

// SelectedRepo returns the repo name if cursor is on a repo header
func (m *Model) SelectedRepo() string {
	if m.focusPane != 1 {
		return ""
	}
	isRepo, repoName, _ := m.getPRItemAt(m.prCursor)
	if isRepo {
		return repoName
	}
	return ""
}
```

**Step 4: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 5: Commit**

```bash
git add internal/dashboard/model.go
git commit -m "feat(dashboard): cursor navigation for grouped PRs"
```

---

### Task 11: Dashboard - Key Handlers

**Files:**
- Modify: `internal/dashboard/model.go`

**Step 1: Update Enter key handler**

In the `case "enter":` section of `Update()`:

```go
case "enter":
	if m.focusPane == 0 {
		if s := m.SelectedSession(); s != nil && s.TmuxTarget != "" {
			return m, m.jumpToPane(s.TmuxTarget)
		}
	} else {
		// PR pane - check if on repo or PR
		if repo := m.SelectedRepo(); repo != "" {
			// Toggle collapse
			return m, m.toggleRepoCollapsed(repo)
		} else if pr := m.SelectedPR(); pr != nil {
			return m, m.openPRInBrowser(pr.URL)
		}
	}
```

**Step 2: Update m key handler**

```go
case "m":
	if m.focusPane == 0 {
		if s := m.SelectedSession(); s != nil {
			return m, m.toggleMute(s.ID)
		}
	} else {
		// PR pane - mute repo or PR
		if repo := m.SelectedRepo(); repo != "" {
			return m, m.toggleMuteRepo(repo)
		} else if pr := m.SelectedPR(); pr != nil {
			return m, m.toggleMutePR(pr.ID)
		}
	}
```

**Step 3: Add V key handler**

```go
case "V":
	m.showMutedRepos = !m.showMutedRepos
```

**Step 4: Add toggle methods**

```go
func (m *Model) toggleRepoCollapsed(repo string) tea.Cmd {
	return func() tea.Msg {
		// Get current state
		state := m.repoStates[repo]
		collapsed := true
		if state != nil {
			collapsed = !state.Collapsed
		}
		if err := m.client.SetRepoCollapsed(repo, collapsed); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}

func (m *Model) toggleMuteRepo(repo string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.ToggleMuteRepo(repo); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}
```

**Step 5: Update help text**

```go
help := legendStyle.Render("[Tab] Switch  [m] Mute  [M] Muted PRs  [V] Muted repos  [Enter] Open/Expand  [R] Restart  [q] Quit")
```

**Step 6: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 7: Commit**

```bash
git add internal/dashboard/model.go
git commit -m "feat(dashboard): key handlers for repo expand/collapse/mute"
```

---

### Task 12: Update Tmux Status Bar

**Files:**
- Modify: `internal/status/status.go`
- Test: `internal/status/status_test.go`

**Step 1: Write failing test**

Add to `internal/status/status_test.go`:

```go
func TestFormatWithPRs_RepoGrouping(t *testing.T) {
	tests := []struct {
		name     string
		sessions []*protocol.Session
		prs      []*protocol.PR
		repos    []*protocol.RepoState
		want     string
	}{
		{
			name: "1 repo",
			prs: []*protocol.PR{
				{Repo: "owner/repo-a", State: protocol.StateWaiting},
				{Repo: "owner/repo-a", State: protocol.StateWaiting},
			},
			want: "● 2 waiting | repo-a(2)",
		},
		{
			name: "2 repos",
			prs: []*protocol.PR{
				{Repo: "owner/repo-a", State: protocol.StateWaiting},
				{Repo: "owner/repo-b", State: protocol.StateWaiting},
			},
			want: "● 2 waiting | repo-a(1) repo-b(1)",
		},
		{
			name: "3+ repos shows counts",
			prs: []*protocol.PR{
				{Repo: "owner/repo-a", State: protocol.StateWaiting},
				{Repo: "owner/repo-b", State: protocol.StateWaiting},
				{Repo: "owner/repo-c", State: protocol.StateWaiting},
			},
			want: "● 3 waiting | 3 PRs in 3 repos",
		},
		{
			name: "muted repo excluded",
			prs: []*protocol.PR{
				{Repo: "owner/repo-a", State: protocol.StateWaiting},
				{Repo: "owner/muted", State: protocol.StateWaiting},
			},
			repos: []*protocol.RepoState{
				{Repo: "owner/muted", Muted: true},
			},
			want: "● 1 waiting | repo-a(1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatWithPRsAndRepos(tt.sessions, tt.prs, tt.repos)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/status -run TestFormatWithPRs_RepoGrouping -v`
Expected: FAIL

**Step 3: Add FormatWithPRsAndRepos function**

```go
// FormatWithPRsAndRepos formats status with repo-aware PR display
func FormatWithPRsAndRepos(sessions []*protocol.Session, prs []*protocol.PR, repos []*protocol.RepoState) string {
	// Build muted repos set
	mutedRepos := make(map[string]bool)
	for _, r := range repos {
		if r.Muted {
			mutedRepos[r.Repo] = true
		}
	}

	// Count sessions
	sessionWaiting := 0
	for _, s := range sessions {
		if s.State == protocol.StateWaiting && !s.Muted {
			sessionWaiting++
		}
	}

	// Group PRs by repo, excluding muted
	repoCount := make(map[string]int)
	prWaiting := 0
	for _, pr := range prs {
		if pr.Muted || mutedRepos[pr.Repo] {
			continue
		}
		if pr.State == protocol.StateWaiting {
			prWaiting++
			repoCount[pr.Repo]++
		}
	}

	// Format session part
	var parts []string
	if sessionWaiting > 0 {
		parts = append(parts, fmt.Sprintf("● %d waiting", sessionWaiting))
	}

	// Format PR part
	if prWaiting > 0 {
		var prPart string
		if len(repoCount) <= 2 {
			// Show repo names
			var repoParts []string
			// Sort for consistent output
			var repoNames []string
			for r := range repoCount {
				repoNames = append(repoNames, r)
			}
			sort.Strings(repoNames)
			for _, r := range repoNames {
				// Short name
				short := r
				if idx := strings.LastIndex(r, "/"); idx >= 0 {
					short = r[idx+1:]
				}
				repoParts = append(repoParts, fmt.Sprintf("%s(%d)", short, repoCount[r]))
			}
			prPart = strings.Join(repoParts, " ")
		} else {
			// Show counts
			prPart = fmt.Sprintf("%d PRs in %d repos", prWaiting, len(repoCount))
		}

		if len(parts) > 0 {
			parts = append(parts, "| "+prPart)
		} else {
			parts = append(parts, "● "+fmt.Sprintf("%d waiting", prWaiting)+" | "+prPart)
		}
	}

	if len(parts) == 0 {
		return "✓ all clear"
	}

	return strings.Join(parts, " ")
}
```

Wait, the logic is a bit off. Let me fix:

```go
// FormatWithPRsAndRepos formats status with repo-aware PR display
func FormatWithPRsAndRepos(sessions []*protocol.Session, prs []*protocol.PR, repos []*protocol.RepoState) string {
	// Build muted repos set
	mutedRepos := make(map[string]bool)
	for _, r := range repos {
		if r.Muted {
			mutedRepos[r.Repo] = true
		}
	}

	// Count sessions
	sessionWaiting := 0
	for _, s := range sessions {
		if s.State == protocol.StateWaiting && !s.Muted {
			sessionWaiting++
		}
	}

	// Group PRs by repo, excluding muted
	repoCount := make(map[string]int)
	prWaiting := 0
	for _, pr := range prs {
		if pr.Muted || mutedRepos[pr.Repo] {
			continue
		}
		if pr.State == protocol.StateWaiting {
			prWaiting++
			repoCount[pr.Repo]++
		}
	}

	totalWaiting := sessionWaiting + prWaiting
	if totalWaiting == 0 {
		return "✓ all clear"
	}

	// Format PR part
	var prPart string
	if prWaiting > 0 {
		if len(repoCount) <= 2 {
			// Show repo names
			var repoParts []string
			var repoNames []string
			for r := range repoCount {
				repoNames = append(repoNames, r)
			}
			sort.Strings(repoNames)
			for _, r := range repoNames {
				short := r
				if idx := strings.LastIndex(r, "/"); idx >= 0 {
					short = r[idx+1:]
				}
				repoParts = append(repoParts, fmt.Sprintf("%s(%d)", short, repoCount[r]))
			}
			prPart = strings.Join(repoParts, " ")
		} else {
			prPart = fmt.Sprintf("%d PRs in %d repos", prWaiting, len(repoCount))
		}
	}

	result := fmt.Sprintf("● %d waiting", totalWaiting)
	if prPart != "" {
		result += " | " + prPart
	}
	return result
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/status -run TestFormatWithPRs_RepoGrouping -v`
Expected: PASS

**Step 5: Update cmd/cm/main.go to use new function**

In the status command handler, update to fetch and pass repos:

```go
// In handleStatus or wherever FormatWithPRs is called:
repos, _ := c.QueryRepos()
fmt.Println(status.FormatWithPRsAndRepos(sessions, prs, repos))
```

**Step 6: Commit**

```bash
git add internal/status/status.go internal/status/status_test.go cmd/cm/main.go
git commit -m "feat(status): repo-aware tmux status format"
```

---

### Task 13: Integration Test and Polish

**Files:**
- Various

**Step 1: Run full test suite**

Run: `go test ./... -v`
Expected: All PASS

**Step 2: Manual testing checklist**

- [ ] Start daemon, open dashboard
- [ ] Verify PRs are grouped by repo
- [ ] Test expand/collapse with Enter
- [ ] Test repo mute with 'm' on repo header
- [ ] Test PR mute with 'm' on PR
- [ ] Test 'M' shows muted PRs
- [ ] Test 'V' shows muted repos
- [ ] Verify collapsed state persists after daemon restart
- [ ] Verify muted repo state persists after daemon restart
- [ ] Check tmux status shows repo names (1-2) or counts (3+)

**Step 3: Fix any issues found**

**Step 4: Final commit**

```bash
git add -A
git commit -m "feat: PR grouping with repo muting and titles"
```

---

## Summary

This plan implements:
1. Async persistence with dirty flag
2. RepoState for per-repo settings
3. Grouped PR display with collapsible repos
4. PR titles shown when expanded
5. Repo muting with 'm' on repo header
6. 'V' key to show muted repos
7. Updated tmux status with repo-aware format
