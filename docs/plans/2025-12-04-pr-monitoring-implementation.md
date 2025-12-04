# PR Monitoring Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add GitHub PR monitoring to claude-manager so PRs needing attention appear alongside Claude sessions in dashboard and status bar.

**Architecture:** New `internal/github` package polls PRs via `gh` CLI every 90 seconds. Store holds both sessions and PRs. Dashboard uses horizontal split layout. Status bar combines both.

**Tech Stack:** Go, `gh` CLI (shelled out), bubbletea (existing)

---

## Task 1: Daemon Logging Infrastructure

**Files:**
- Create: `internal/logging/logging.go`
- Modify: `internal/daemon/daemon.go`

**Step 1: Write failing test for logging package**

Create `internal/logging/logging_test.go`:

```go
package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogger_WritesToFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer logger.Close()

	logger.Info("test message")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if !strings.Contains(string(content), "test message") {
		t.Errorf("log file should contain 'test message', got: %s", content)
	}
}

func TestLogger_RespectsDebugLevel(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer logger.Close()

	// Debug disabled by default
	logger.Debug("debug message")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if strings.Contains(string(content), "debug message") {
		t.Errorf("debug message should not appear when debug disabled")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/logging -v
```

Expected: FAIL - package doesn't exist

**Step 3: Write minimal logging implementation**

Create `internal/logging/logging.go`:

```go
package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Logger struct {
	file   *os.File
	logger *log.Logger
	debug  bool
}

func New(path string) (*Logger, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	debugEnv := os.Getenv("CM_DEBUG")
	debug := debugEnv == "debug" || debugEnv == "trace"

	return &Logger{
		file:   file,
		logger: log.New(file, "", 0),
		debug:  debug,
	}, nil
}

func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) log(level, msg string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.logger.Printf("[%s] %s: %s", timestamp, level, msg)
}

func (l *Logger) Info(msg string) {
	l.log("INFO", msg)
}

func (l *Logger) Error(msg string) {
	l.log("ERROR", msg)
}

func (l *Logger) Debug(msg string) {
	if l.debug {
		l.log("DEBUG", msg)
	}
}

func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(fmt.Sprintf(format, args...))
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	l.Debug(fmt.Sprintf(format, args...))
}

func DefaultLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/claude-manager.log"
	}
	return filepath.Join(home, ".claude-manager", "daemon.log")
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/logging -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/logging/
git commit -m "feat: add logging package for daemon"
```

---

## Task 2: Integrate Logging into Daemon

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Add logger to Daemon struct and initialization**

In `internal/daemon/daemon.go`, update imports and struct:

```go
import (
	// ... existing imports ...
	"github.com/victorarias/claude-manager/internal/logging"
)

type Daemon struct {
	socketPath string
	store      *store.Store
	listener   net.Listener
	done       chan struct{}
	logger     *logging.Logger
}

func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())
	return &Daemon{
		socketPath: socketPath,
		store:      store.NewWithPersistence(store.DefaultStatePath()),
		done:       make(chan struct{}),
		logger:     logger,
	}
}

func NewForTesting(socketPath string) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		done:       make(chan struct{}),
		logger:     nil, // No logging in tests
	}
}
```

**Step 2: Add logging helper method**

```go
func (d *Daemon) log(msg string) {
	if d.logger != nil {
		d.logger.Info(msg)
	}
}

func (d *Daemon) logf(format string, args ...interface{}) {
	if d.logger != nil {
		d.logger.Infof(format, args...)
	}
}
```

**Step 3: Add startup log**

In `Start()` method, after listener is created:

```go
func (d *Daemon) Start() error {
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	d.log("daemon started")  // Add this line

	// ... rest of method
}
```

**Step 4: Add shutdown log**

In `Stop()` method:

```go
func (d *Daemon) Stop() {
	d.log("daemon stopping")  // Add this line
	close(d.done)
	if d.listener != nil {
		d.listener.Close()
	}
	os.Remove(d.socketPath)
	if d.logger != nil {
		d.logger.Close()
	}
}
```

**Step 5: Run tests to verify nothing broke**

```bash
go test ./internal/daemon -v
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat: integrate logging into daemon"
```

---

## Task 3: Add PR Type to Protocol

**Files:**
- Modify: `internal/protocol/types.go`

**Step 1: Add PR struct and constants**

Add after the Session struct in `internal/protocol/types.go`:

```go
// PR reasons (why it needs attention)
const (
	PRReasonReadyToMerge     = "ready_to_merge"
	PRReasonCIFailed         = "ci_failed"
	PRReasonChangesRequested = "changes_requested"
	PRReasonReviewNeeded     = "review_needed"
)

// PR roles
const (
	PRRoleAuthor   = "author"
	PRRoleReviewer = "reviewer"
)

// PR represents a tracked GitHub pull request
type PR struct {
	ID          string    `json:"id"`           // "owner/repo#number"
	Repo        string    `json:"repo"`         // "owner/repo"
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Role        string    `json:"role"`         // "author" or "reviewer"
	State       string    `json:"state"`        // "waiting" or "working"
	Reason      string    `json:"reason"`       // why it needs attention
	LastUpdated time.Time `json:"last_updated"`
	LastPolled  time.Time `json:"last_polled"`
	Muted       bool      `json:"muted"`
}
```

**Step 2: Add PR commands**

Update the Commands const block:

```go
const (
	CmdRegister   = "register"
	CmdUnregister = "unregister"
	CmdState      = "state"
	CmdTodos      = "todos"
	CmdQuery      = "query"
	CmdHeartbeat  = "heartbeat"
	CmdMute       = "mute"
	CmdQueryPRs   = "query_prs"   // Add this
	CmdMutePR     = "mute_pr"     // Add this
)
```

**Step 3: Add PR message types**

Add after MuteMessage:

```go
// QueryPRsMessage queries PRs from daemon
type QueryPRsMessage struct {
	Cmd    string `json:"cmd"`
	Filter string `json:"filter,omitempty"` // "waiting", "working", or empty for all
}

// MutePRMessage toggles a PR's muted state
type MutePRMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}
```

**Step 4: Update Response to include PRs**

```go
type Response struct {
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
	Sessions []*Session `json:"sessions,omitempty"`
	PRs      []*PR      `json:"prs,omitempty"`  // Add this
}
```

**Step 5: Add cases to ParseMessage**

In the switch statement in `ParseMessage()`, add:

```go
	case CmdQueryPRs:
		var msg QueryPRsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMutePR:
		var msg MutePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil
```

**Step 6: Run existing tests**

```bash
go test ./internal/protocol -v
```

Expected: PASS

**Step 7: Commit**

```bash
git add internal/protocol/types.go
git commit -m "feat: add PR type and commands to protocol"
```

---

## Task 4: Add PR Storage to Store

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

**Step 1: Write failing test for PR storage**

Add to `internal/store/store_test.go`:

```go
func TestStore_SetAndListPRs(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWaiting, Muted: false},
		{ID: "owner/repo#2", State: protocol.StateWorking, Muted: false},
	}

	s.SetPRs(prs)

	all := s.ListPRs("")
	if len(all) != 2 {
		t.Errorf("ListPRs('') returned %d PRs, want 2", len(all))
	}

	waiting := s.ListPRs(protocol.StateWaiting)
	if len(waiting) != 1 {
		t.Errorf("ListPRs(waiting) returned %d PRs, want 1", len(waiting))
	}
}

func TestStore_SetPRs_PreservesMuted(t *testing.T) {
	s := New()

	// Initial PRs
	prs := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	// Mute it
	s.ToggleMutePR("owner/repo#1")

	// Set PRs again (simulating poll)
	prs2 := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWorking, Muted: false},
	}
	s.SetPRs(prs2)

	// Should still be muted
	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should still be muted after SetPRs")
	}
}

func TestStore_ToggleMutePR(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	s.ToggleMutePR("owner/repo#1")

	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should be muted after toggle")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/store -v -run TestStore_.*PR
```

Expected: FAIL - methods don't exist

**Step 3: Add PR storage to Store struct**

In `internal/store/store.go`, update struct and constructors:

```go
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*protocol.Session
	prs      map[string]*protocol.PR
	path     string
}

func New() *Store {
	return &Store{
		sessions: make(map[string]*protocol.Session),
		prs:      make(map[string]*protocol.PR),
	}
}

func NewWithPersistence(path string) *Store {
	s := &Store{
		sessions: make(map[string]*protocol.Session),
		prs:      make(map[string]*protocol.PR),
		path:     path,
	}
	s.Load()
	return s
}
```

**Step 4: Add PR methods**

Add to `internal/store/store.go`:

```go
// SetPRs replaces all PRs, preserving muted state
func (s *Store) SetPRs(prs []*protocol.PR) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Preserve muted state from existing PRs
	for _, pr := range prs {
		if existing, ok := s.prs[pr.ID]; ok {
			pr.Muted = existing.Muted
		}
	}

	// Replace all PRs
	s.prs = make(map[string]*protocol.PR)
	for _, pr := range prs {
		s.prs[pr.ID] = pr
	}
	s.save()
}

// ListPRs returns PRs, optionally filtered by state, sorted by repo/number
func (s *Store) ListPRs(stateFilter string) []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*protocol.PR
	for _, pr := range s.prs {
		if stateFilter == "" || pr.State == stateFilter {
			result = append(result, pr)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// ToggleMutePR toggles a PR's muted state
func (s *Store) ToggleMutePR(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pr, ok := s.prs[id]; ok {
		pr.Muted = !pr.Muted
		s.save()
	}
}

// GetPR returns a PR by ID
func (s *Store) GetPR(id string) *protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prs[id]
}
```

**Step 5: Update persistence (Load and save)**

Update the `Load` method to also load PRs:

```go
type persistedState struct {
	Sessions []*protocol.Session `json:"sessions"`
	PRs      []*protocol.PR      `json:"prs,omitempty"`
}

func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		// Try legacy format (just sessions array)
		var sessions []*protocol.Session
		if err := json.Unmarshal(data, &sessions); err != nil {
			return err
		}
		state.Sessions = sessions
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range state.Sessions {
		s.sessions[session.ID] = session
	}
	for _, pr := range state.PRs {
		s.prs[pr.ID] = pr
	}
	return nil
}
```

Update the `save` method:

```go
func (s *Store) save() {
	if s.path == "" {
		return
	}

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

	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	os.WriteFile(s.path, data, 0600)
}
```

**Step 6: Run tests to verify they pass**

```bash
go test ./internal/store -v
```

Expected: PASS

**Step 7: Commit**

```bash
git add internal/store/
git commit -m "feat: add PR storage to store"
```

---

## Task 5: GitHub PR Fetcher

**Files:**
- Create: `internal/github/github.go`
- Create: `internal/github/github_test.go`

**Step 1: Write test for parsing gh output**

Create `internal/github/github_test.go`:

```go
package github

import (
	"testing"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestParsePRFromGH(t *testing.T) {
	ghOutput := `{
		"number": 123,
		"title": "Fix bug",
		"url": "https://github.com/owner/repo/pull/123",
		"headRepository": {"nameWithOwner": "owner/repo"},
		"statusCheckRollup": {"state": "SUCCESS"},
		"reviewDecision": "APPROVED",
		"mergeable": "MERGEABLE"
	}`

	pr, err := parsePR([]byte(ghOutput), protocol.PRRoleAuthor)
	if err != nil {
		t.Fatalf("parsePR error: %v", err)
	}

	if pr.ID != "owner/repo#123" {
		t.Errorf("ID = %q, want %q", pr.ID, "owner/repo#123")
	}
	if pr.State != protocol.StateWaiting {
		t.Errorf("State = %q, want %q (ready to merge)", pr.State, protocol.StateWaiting)
	}
	if pr.Reason != protocol.PRReasonReadyToMerge {
		t.Errorf("Reason = %q, want %q", pr.Reason, protocol.PRReasonReadyToMerge)
	}
}

func TestDetermineState_CIFailed(t *testing.T) {
	state, reason := determineState("FAILURE", "", "", protocol.PRRoleAuthor)
	if state != protocol.StateWaiting {
		t.Errorf("State = %q, want waiting", state)
	}
	if reason != protocol.PRReasonCIFailed {
		t.Errorf("Reason = %q, want ci_failed", reason)
	}
}

func TestDetermineState_ChangesRequested(t *testing.T) {
	state, reason := determineState("SUCCESS", "CHANGES_REQUESTED", "", protocol.PRRoleAuthor)
	if state != protocol.StateWaiting {
		t.Errorf("State = %q, want waiting", state)
	}
	if reason != protocol.PRReasonChangesRequested {
		t.Errorf("Reason = %q, want changes_requested", reason)
	}
}

func TestDetermineState_ReviewNeeded(t *testing.T) {
	state, reason := determineState("SUCCESS", "REVIEW_REQUIRED", "", protocol.PRRoleReviewer)
	if state != protocol.StateWaiting {
		t.Errorf("State = %q, want waiting", state)
	}
	if reason != protocol.PRReasonReviewNeeded {
		t.Errorf("Reason = %q, want review_needed", reason)
	}
}

func TestDetermineState_WaitingOnOthers(t *testing.T) {
	state, reason := determineState("PENDING", "", "", protocol.PRRoleAuthor)
	if state != protocol.StateWorking {
		t.Errorf("State = %q, want working (CI pending)", state)
	}
	if reason != "" {
		t.Errorf("Reason = %q, want empty", reason)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/github -v
```

Expected: FAIL - package doesn't exist

**Step 3: Implement GitHub package**

Create `internal/github/github.go`:

```go
package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// ghPR is the structure returned by gh pr list --json
type ghPR struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	HeadRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	StatusCheckRollup struct {
		State string `json:"state"` // SUCCESS, FAILURE, PENDING
	} `json:"statusCheckRollup"`
	ReviewDecision string `json:"reviewDecision"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED
	Mergeable      string `json:"mergeable"`      // MERGEABLE, CONFLICTING, UNKNOWN
}

// Fetcher fetches PRs from GitHub
type Fetcher struct {
	ghPath string
}

// NewFetcher creates a new GitHub PR fetcher
func NewFetcher() *Fetcher {
	ghPath, _ := exec.LookPath("gh")
	return &Fetcher{ghPath: ghPath}
}

// IsAvailable returns true if gh CLI is available
func (f *Fetcher) IsAvailable() bool {
	return f.ghPath != ""
}

// FetchAll fetches all PRs that need tracking
func (f *Fetcher) FetchAll() ([]*protocol.PR, error) {
	if !f.IsAvailable() {
		return nil, fmt.Errorf("gh CLI not available")
	}

	var allPRs []*protocol.PR

	// Fetch authored PRs
	authored, err := f.fetchAuthored()
	if err != nil {
		return nil, fmt.Errorf("fetch authored PRs: %w", err)
	}
	allPRs = append(allPRs, authored...)

	// Fetch review requests
	reviews, err := f.fetchReviewRequests()
	if err != nil {
		return nil, fmt.Errorf("fetch review requests: %w", err)
	}
	allPRs = append(allPRs, reviews...)

	return allPRs, nil
}

func (f *Fetcher) fetchAuthored() ([]*protocol.PR, error) {
	cmd := exec.Command(f.ghPath, "pr", "list",
		"--author", "@me",
		"--state", "open",
		"--json", "number,title,url,headRepository,statusCheckRollup,reviewDecision,mergeable")

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parsePRList(output, protocol.PRRoleAuthor)
}

func (f *Fetcher) fetchReviewRequests() ([]*protocol.PR, error) {
	cmd := exec.Command(f.ghPath, "pr", "list",
		"--search", "review-requested:@me",
		"--state", "open",
		"--json", "number,title,url,headRepository,statusCheckRollup,reviewDecision,mergeable")

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parsePRList(output, protocol.PRRoleReviewer)
}

func parsePRList(data []byte, role string) ([]*protocol.PR, error) {
	var ghPRs []ghPR
	if err := json.Unmarshal(data, &ghPRs); err != nil {
		return nil, err
	}

	var prs []*protocol.PR
	for _, gh := range ghPRs {
		pr := convertPR(gh, role)
		prs = append(prs, pr)
	}
	return prs, nil
}

func parsePR(data []byte, role string) (*protocol.PR, error) {
	var gh ghPR
	if err := json.Unmarshal(data, &gh); err != nil {
		return nil, err
	}
	return convertPR(gh, role), nil
}

func convertPR(gh ghPR, role string) *protocol.PR {
	repo := gh.HeadRepository.NameWithOwner
	state, reason := determineState(
		gh.StatusCheckRollup.State,
		gh.ReviewDecision,
		gh.Mergeable,
		role,
	)

	return &protocol.PR{
		ID:          fmt.Sprintf("%s#%d", repo, gh.Number),
		Repo:        repo,
		Number:      gh.Number,
		Title:       gh.Title,
		URL:         gh.URL,
		Role:        role,
		State:       state,
		Reason:      reason,
		LastUpdated: time.Now(),
		LastPolled:  time.Now(),
	}
}

func determineState(ciState, reviewDecision, mergeable, role string) (string, string) {
	// CI failed - author needs to fix
	if ciState == "FAILURE" || ciState == "ERROR" {
		if role == protocol.PRRoleAuthor {
			return protocol.StateWaiting, protocol.PRReasonCIFailed
		}
		return protocol.StateWorking, "" // Reviewer waiting for author to fix
	}

	// Changes requested - author needs to address
	if reviewDecision == "CHANGES_REQUESTED" {
		if role == protocol.PRRoleAuthor {
			return protocol.StateWaiting, protocol.PRReasonChangesRequested
		}
		return protocol.StateWorking, "" // Reviewer waiting for author
	}

	// Review needed - reviewer needs to act
	if reviewDecision == "REVIEW_REQUIRED" || reviewDecision == "" {
		if role == protocol.PRRoleReviewer {
			return protocol.StateWaiting, protocol.PRReasonReviewNeeded
		}
		return protocol.StateWorking, "" // Author waiting for reviews
	}

	// Approved + CI passed - author can merge
	if reviewDecision == "APPROVED" && (ciState == "SUCCESS" || ciState == "") {
		if role == protocol.PRRoleAuthor {
			return protocol.StateWaiting, protocol.PRReasonReadyToMerge
		}
		return protocol.StateWorking, "" // Reviewer done, waiting for author
	}

	// CI pending or other states - waiting on external
	return protocol.StateWorking, ""
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/github -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/
git commit -m "feat: add GitHub PR fetcher using gh CLI"
```

---

## Task 6: Add PR Handlers to Daemon

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Add PR command handlers to handleConnection switch**

In `handleConnection`, add new cases:

```go
	case protocol.CmdQueryPRs:
		d.handleQueryPRs(conn, msg.(*protocol.QueryPRsMessage))
	case protocol.CmdMutePR:
		d.handleMutePR(conn, msg.(*protocol.MutePRMessage))
```

**Step 2: Implement handler methods**

Add to `internal/daemon/daemon.go`:

```go
func (d *Daemon) handleQueryPRs(conn net.Conn, msg *protocol.QueryPRsMessage) {
	prs := d.store.ListPRs(msg.Filter)
	resp := protocol.Response{
		OK:  true,
		PRs: prs,
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleMutePR(conn net.Conn, msg *protocol.MutePRMessage) {
	d.store.ToggleMutePR(msg.ID)
	d.sendOK(conn)
}
```

**Step 3: Run tests**

```bash
go test ./internal/daemon -v
```

Expected: PASS

**Step 4: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat: add PR query and mute handlers to daemon"
```

---

## Task 7: Add PR Polling to Daemon

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Add github import and fetcher to struct**

```go
import (
	// ... existing imports ...
	"github.com/victorarias/claude-manager/internal/github"
)

type Daemon struct {
	socketPath string
	store      *store.Store
	listener   net.Listener
	done       chan struct{}
	logger     *logging.Logger
	ghFetcher  *github.Fetcher
}
```

**Step 2: Update constructors**

```go
func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())
	return &Daemon{
		socketPath: socketPath,
		store:      store.NewWithPersistence(store.DefaultStatePath()),
		done:       make(chan struct{}),
		logger:     logger,
		ghFetcher:  github.NewFetcher(),
	}
}
```

**Step 3: Add polling goroutine to Start**

```go
func (d *Daemon) Start() error {
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	d.log("daemon started")

	// Start PR polling
	go d.pollPRs()

	for {
		// ... existing accept loop ...
	}
}
```

**Step 4: Implement polling method**

```go
func (d *Daemon) pollPRs() {
	if d.ghFetcher == nil || !d.ghFetcher.IsAvailable() {
		d.log("gh CLI not available, PR polling disabled")
		return
	}

	d.log("PR polling started (90s interval)")

	// Initial poll
	d.doPRPoll()

	ticker := time.NewTicker(90 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.doPRPoll()
		}
	}
}

func (d *Daemon) doPRPoll() {
	prs, err := d.ghFetcher.FetchAll()
	if err != nil {
		d.logf("PR poll error: %v", err)
		return
	}

	// Filter: skip muted PRs that were polled within 24h
	existingPRs := d.store.ListPRs("")
	mutedRecently := make(map[string]bool)
	for _, pr := range existingPRs {
		if pr.Muted && time.Since(pr.LastPolled) < 24*time.Hour {
			mutedRecently[pr.ID] = true
		}
	}

	var activePRs []*protocol.PR
	for _, pr := range prs {
		if !mutedRecently[pr.ID] {
			activePRs = append(activePRs, pr)
		}
	}

	d.store.SetPRs(activePRs)

	waiting := 0
	for _, pr := range activePRs {
		if pr.State == protocol.StateWaiting {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(activePRs), waiting)
}
```

**Step 5: Run tests**

```bash
go test ./internal/daemon -v
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat: add PR polling to daemon"
```

---

## Task 8: Add PR Methods to Client

**Files:**
- Modify: `internal/client/client.go`

**Step 1: Add QueryPRs method**

```go
// QueryPRs returns PRs matching the filter
func (c *Client) QueryPRs(filter string) ([]*protocol.PR, error) {
	msg := protocol.QueryPRsMessage{
		Cmd:    protocol.CmdQueryPRs,
		Filter: filter,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.PRs, nil
}
```

**Step 2: Add ToggleMutePR method**

```go
// ToggleMutePR toggles a PR's muted state
func (c *Client) ToggleMutePR(id string) error {
	msg := protocol.MutePRMessage{
		Cmd: protocol.CmdMutePR,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}
```

**Step 3: Run tests**

```bash
go test ./internal/client -v
```

Expected: PASS

**Step 4: Commit**

```bash
git add internal/client/client.go
git commit -m "feat: add PR query and mute methods to client"
```

---

## Task 9: Update Status Bar for PRs

**Files:**
- Modify: `internal/status/status.go`
- Modify: `internal/status/status_test.go`

**Step 1: Write failing test**

Add to `internal/status/status_test.go`:

```go
func TestFormat_WithPRs(t *testing.T) {
	sessions := []*protocol.Session{}
	prs := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWaiting, Reason: protocol.PRReasonReadyToMerge, Repo: "owner/repo", Number: 1, Muted: false},
	}

	result := FormatWithPRs(sessions, prs)
	if !strings.Contains(result, "1 PR") {
		t.Errorf("Expected '1 PR' in output, got: %s", result)
	}
}

func TestFormat_SessionsAndPRs(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "foo", State: protocol.StateWaiting, Muted: false},
	}
	prs := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWaiting, Repo: "owner/repo", Number: 1, Muted: false},
	}

	result := FormatWithPRs(sessions, prs)
	if !strings.Contains(result, "1 waiting") {
		t.Errorf("Expected '1 waiting' in output, got: %s", result)
	}
	if !strings.Contains(result, "1 PR") {
		t.Errorf("Expected '1 PR' in output, got: %s", result)
	}
}

func TestFormat_AllClear(t *testing.T) {
	sessions := []*protocol.Session{}
	prs := []*protocol.PR{}

	result := FormatWithPRs(sessions, prs)
	if result != "✓ all clear" {
		t.Errorf("Expected '✓ all clear', got: %s", result)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/status -v -run TestFormat_.*PR
```

Expected: FAIL - FormatWithPRs doesn't exist

**Step 3: Update status.go**

Replace `internal/status/status.go` content:

```go
package status

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/claude-manager/internal/protocol"
)

const maxLabels = 3

// Format returns a status string for sessions only (backwards compatible)
func Format(sessions []*protocol.Session) string {
	return FormatWithPRs(sessions, nil)
}

// FormatWithPRs returns a status string for sessions and PRs
func FormatWithPRs(sessions []*protocol.Session, prs []*protocol.PR) string {
	// Filter to waiting sessions (non-muted)
	var waitingSessions []*protocol.Session
	for _, s := range sessions {
		if s.State == protocol.StateWaiting && !s.Muted {
			waitingSessions = append(waitingSessions, s)
		}
	}

	// Filter to waiting PRs (non-muted)
	var waitingPRs []*protocol.PR
	for _, pr := range prs {
		if pr.State == protocol.StateWaiting && !pr.Muted {
			waitingPRs = append(waitingPRs, pr)
		}
	}

	// Nothing waiting
	if len(waitingSessions) == 0 && len(waitingPRs) == 0 {
		return "✓ all clear"
	}

	var parts []string

	// Sessions part
	if len(waitingSessions) > 0 {
		sort.Slice(waitingSessions, func(i, j int) bool {
			return waitingSessions[i].StateSince.Before(waitingSessions[j].StateSince)
		})

		var labels []string
		for i, s := range waitingSessions {
			if i >= maxLabels {
				break
			}
			labels = append(labels, s.Label)
		}
		labelStr := strings.Join(labels, ", ")
		if len(waitingSessions) > maxLabels {
			labelStr += ", ..."
		}
		parts = append(parts, fmt.Sprintf("%d waiting: %s", len(waitingSessions), labelStr))
	}

	// PRs part
	if len(waitingPRs) > 0 {
		sort.Slice(waitingPRs, func(i, j int) bool {
			return waitingPRs[i].ID < waitingPRs[j].ID
		})

		var labels []string
		for i, pr := range waitingPRs {
			if i >= maxLabels {
				break
			}
			// Show repo#number or just #number if space is tight
			labels = append(labels, fmt.Sprintf("#%d", pr.Number))
		}
		labelStr := strings.Join(labels, ", ")
		if len(waitingPRs) > maxLabels {
			labelStr += ", ..."
		}
		parts = append(parts, fmt.Sprintf("%d PR: %s", len(waitingPRs), labelStr))
	}

	return strings.Join(parts, " | ")
}
```

**Step 4: Run tests**

```bash
go test ./internal/status -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/status/
git commit -m "feat: update status bar to include PRs"
```

---

## Task 10: Update Dashboard - Model with PRs

**Files:**
- Modify: `internal/dashboard/model.go`

**Step 1: Add PR fields to Model**

Update struct:

```go
type Model struct {
	client         *client.Client
	sessions       []*protocol.Session
	prs            []*protocol.PR
	cursor         int
	prCursor       int
	focusPane      int // 0 = sessions, 1 = PRs
	showMutedPRs   bool
	err            error
	currentSession string
}
```

**Step 2: Add PR refresh**

Update `refresh` method:

```go
func (m *Model) refresh() tea.Msg {
	if m.client == nil {
		return sessionsMsg{sessions: nil, prs: nil}
	}
	sessions, err := m.client.Query("")
	if err != nil {
		return errMsg{err: err}
	}
	prs, _ := m.client.QueryPRs("") // Ignore error, PRs are optional
	return sessionsMsg{sessions: sessions, prs: prs}
}

type sessionsMsg struct {
	sessions []*protocol.Session
	prs      []*protocol.PR
}
```

**Step 3: Update Update method for PRs message**

```go
	case sessionsMsg:
		m.sessions = msg.sessions
		m.prs = msg.prs
		m.err = nil
		// Ensure cursors are valid
		if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
			m.cursor = len(m.sessions) - 1
		}
		visiblePRs := m.getVisiblePRs()
		if m.prCursor >= len(visiblePRs) && len(visiblePRs) > 0 {
			m.prCursor = len(visiblePRs) - 1
		}
		return m, TickCmd()
```

**Step 4: Add getVisiblePRs helper**

```go
func (m *Model) getVisiblePRs() []*protocol.PR {
	if m.showMutedPRs {
		return m.prs
	}
	var visible []*protocol.PR
	for _, pr := range m.prs {
		if !pr.Muted {
			visible = append(visible, pr)
		}
	}
	return visible
}
```

**Step 5: Update keybindings in Update method**

Replace the keybinding section:

```go
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "l":
			if m.focusPane == 0 {
				m.focusPane = 1
			} else {
				m.focusPane = 0
			}
		case "h":
			m.focusPane = 0
		case "up", "k":
			if m.focusPane == 0 {
				m.moveCursor(-1)
			} else {
				m.movePRCursor(-1)
			}
		case "down", "j":
			if m.focusPane == 0 {
				m.moveCursor(1)
			} else {
				m.movePRCursor(1)
			}
		case "r":
			return m, m.refresh
		case "enter":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil && s.TmuxTarget != "" {
					return m, m.jumpToPane(s.TmuxTarget)
				}
			} else {
				if pr := m.SelectedPR(); pr != nil {
					return m, m.openPRInBrowser(pr.URL)
				}
			}
		case "m":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil {
					return m, m.toggleMute(s.ID)
				}
			} else {
				if pr := m.SelectedPR(); pr != nil {
					return m, m.toggleMutePR(pr.ID)
				}
			}
		case "M":
			m.showMutedPRs = !m.showMutedPRs
		case "x", "d":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil {
					return m, m.deleteSession(s.ID)
				}
			}
		case "R":
			return m, m.restartDaemon
		}
```

**Step 6: Add PR cursor and selection methods**

```go
func (m *Model) movePRCursor(delta int) {
	m.prCursor += delta
	visiblePRs := m.getVisiblePRs()
	if m.prCursor < 0 {
		m.prCursor = 0
	}
	if m.prCursor >= len(visiblePRs) && len(visiblePRs) > 0 {
		m.prCursor = len(visiblePRs) - 1
	}
}

func (m *Model) SelectedPR() *protocol.PR {
	visiblePRs := m.getVisiblePRs()
	if m.prCursor >= 0 && m.prCursor < len(visiblePRs) {
		return visiblePRs[m.prCursor]
	}
	return nil
}

func (m *Model) toggleMutePR(prID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.ToggleMutePR(prID); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}

func (m *Model) openPRInBrowser(url string) tea.Cmd {
	c := exec.Command("open", url) // macOS; use xdg-open on Linux
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return nil
	})
}
```

**Step 7: Run tests**

```bash
go test ./internal/dashboard -v
```

Expected: PASS

**Step 8: Commit**

```bash
git add internal/dashboard/model.go
git commit -m "feat: add PR tracking to dashboard model"
```

---

## Task 11: Update Dashboard - Horizontal View

**Files:**
- Modify: `internal/dashboard/model.go`

**Step 1: Replace View method with horizontal layout**

Replace the entire `View` method:

```go
func (m *Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress 'r' to retry, 'q' to quit", m.err)
	}

	// Get terminal width (default to 120 if can't detect)
	width := 120

	// Calculate pane widths
	leftWidth := width/2 - 2
	rightWidth := width/2 - 2

	// Build left pane (sessions)
	leftLines := m.renderSessionsPane(leftWidth)

	// Build right pane (PRs)
	rightLines := m.renderPRsPane(rightWidth)

	// Ensure both panes have same height
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}
	for len(leftLines) < maxLines {
		leftLines = append(leftLines, strings.Repeat(" ", leftWidth))
	}
	for len(rightLines) < maxLines {
		rightLines = append(rightLines, strings.Repeat(" ", rightWidth))
	}

	// Combine panes
	var s strings.Builder

	// Header
	leftHeader := " Sessions "
	rightHeader := fmt.Sprintf(" Pull Requests (%d) ", len(m.getVisiblePRs()))
	if m.focusPane == 0 {
		leftHeader = "[" + leftHeader + "]"
	} else {
		rightHeader = "[" + rightHeader + "]"
	}

	s.WriteString(fmt.Sprintf("┌─%s%s┬─%s%s┐\n",
		leftHeader, strings.Repeat("─", leftWidth-len(leftHeader)-1),
		rightHeader, strings.Repeat("─", rightWidth-len(rightHeader)-1)))

	for i := 0; i < maxLines; i++ {
		s.WriteString(fmt.Sprintf("│ %s│ %s│\n", padRight(leftLines[i], leftWidth-1), padRight(rightLines[i], rightWidth-1)))
	}

	s.WriteString(fmt.Sprintf("└%s┴%s┘\n", strings.Repeat("─", leftWidth), strings.Repeat("─", rightWidth)))

	// Legend
	s.WriteString(fmt.Sprintf("%s●%s waiting  %s○%s working  %s◌%s muted\n",
		colorYellow, colorReset, colorGreen, colorReset, colorGray, colorReset))
	s.WriteString("[Tab] Switch pane  [m] Mute  [M] Show muted PRs  [Enter] Open  [r] Refresh  [q] Quit\n")

	return s.String()
}

func (m *Model) renderSessionsPane(width int) []string {
	var lines []string

	if len(m.sessions) == 0 {
		lines = append(lines, "No active sessions")
		return lines
	}

	for i, session := range m.sessions {
		cursor := "  "
		if i == m.cursor && m.focusPane == 0 {
			cursor = "> "
		}

		var color, indicator, stateStr string
		if session.Muted {
			color = colorGray
			indicator = "◌"
			stateStr = "muted"
		} else if session.State == protocol.StateWaiting {
			color = colorYellow
			indicator = "●"
			stateStr = "waiting"
		} else {
			color = colorGreen
			indicator = "○"
			stateStr = "working"
		}

		line := fmt.Sprintf("%s%s%s %-12s %s%s",
			cursor, color, indicator, truncate(session.Label, 12), stateStr, colorReset)
		lines = append(lines, line)
	}

	return lines
}

func (m *Model) renderPRsPane(width int) []string {
	var lines []string

	visiblePRs := m.getVisiblePRs()

	if len(visiblePRs) == 0 {
		if len(m.prs) == 0 {
			lines = append(lines, "No PRs (gh CLI?)")
		} else {
			lines = append(lines, "All PRs muted")
		}
		return lines
	}

	for i, pr := range visiblePRs {
		cursor := "  "
		if i == m.prCursor && m.focusPane == 1 {
			cursor = "> "
		}

		var color, stateStr string
		if pr.Muted {
			color = colorGray
			stateStr = "muted"
		} else if pr.State == protocol.StateWaiting {
			color = colorYellow
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
				stateStr = "wait"
			}
		} else {
			color = colorGreen
			stateStr = "wait"
		}

		// Format: ⬡ repo#123  state
		repoShort := truncate(pr.Repo, 15)
		line := fmt.Sprintf("%s%s⬡ %s#%d %s%s",
			cursor, color, repoShort, pr.Number, stateStr, colorReset)
		lines = append(lines, line)
	}

	return lines
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func padRight(s string, length int) string {
	// Strip ANSI codes for length calculation
	visible := stripANSI(s)
	padding := length - len(visible)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

func stripANSI(s string) string {
	// Simple ANSI stripper for length calculation
	result := s
	for _, code := range []string{colorReset, colorYellow, colorGreen, colorGray} {
		result = strings.ReplaceAll(result, code, "")
	}
	return result
}
```

**Step 2: Run and visually verify**

```bash
make install && cm -d
```

Visually verify the horizontal layout looks correct.

**Step 3: Commit**

```bash
git add internal/dashboard/model.go
git commit -m "feat: horizontal split layout for dashboard"
```

---

## Task 12: Update CLI Status Command

**Files:**
- Modify: `cmd/cm/main.go`

**Step 1: Find and update status command**

In `cmd/cm/main.go`, find where `-s` flag is handled and update to include PRs:

```go
// In the status flag handling section, replace the existing code with:
if statusFlag {
	sessions, err := c.Query("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "daemon not running")
		os.Exit(1)
	}
	prs, _ := c.QueryPRs("") // Ignore error, PRs optional
	fmt.Println(status.FormatWithPRs(sessions, prs))
	return
}
```

**Step 2: Run and verify**

```bash
make install && cm -s
```

Should show combined status with PRs.

**Step 3: Commit**

```bash
git add cmd/cm/main.go
git commit -m "feat: update status command to include PRs"
```

---

## Task 13: Final Integration Test

**Step 1: Manual testing checklist**

```bash
# 1. Restart daemon to pick up changes
cm -d  # opens dashboard, press 'R' to restart daemon

# 2. Verify PR polling (check logs)
tail -f ~/.claude-manager/daemon.log

# 3. Test status bar
cm -s

# 4. Test dashboard navigation
# - Tab between panes
# - j/k to navigate
# - m to mute a PR
# - M to toggle showing muted
# - Enter on PR to open in browser

# 5. Verify muted PRs poll less frequently
# Mute a PR, wait, check logs show it's skipped
```

**Step 2: Commit any fixes needed**

**Step 3: Final commit message if all working**

```bash
git add -A
git commit -m "feat: complete PR monitoring integration"
```

---

## Summary

Tasks in order:
1. Daemon logging infrastructure
2. Integrate logging into daemon
3. Add PR type to protocol
4. Add PR storage to store
5. GitHub PR fetcher
6. Add PR handlers to daemon
7. Add PR polling to daemon
8. Add PR methods to client
9. Update status bar for PRs
10. Update dashboard model with PRs
11. Update dashboard horizontal view
12. Update CLI status command
13. Final integration test

Each task is self-contained with tests, implementation, and commit.
