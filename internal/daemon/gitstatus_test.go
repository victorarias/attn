package daemon

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func TestParseGitStatusPorcelain(t *testing.T) {
	// Porcelain v1 format: XY PATH or XY ORIG -> PATH for renames
	input := " M src/App.tsx\x00A  src/new.ts\x00?? untracked.txt\x00"

	staged, unstaged, untracked := parseGitStatusPorcelain(input, "")

	if len(unstaged) != 1 || unstaged[0].Path != "src/App.tsx" {
		t.Errorf("Expected 1 unstaged file, got %v", unstaged)
	}
	if len(staged) != 1 || staged[0].Path != "src/new.ts" {
		t.Errorf("Expected 1 staged file, got %v", staged)
	}
	if len(untracked) != 1 || untracked[0].Path != "untracked.txt" {
		t.Errorf("Expected 1 untracked file, got %v", untracked)
	}
}

func TestParseGitDiffNumstat(t *testing.T) {
	input := "42\t12\tsrc/App.tsx\n8\t3\tsrc/hook.ts\n"

	stats := parseGitDiffNumstat(input)

	if stats["src/App.tsx"].Additions != 42 || stats["src/App.tsx"].Deletions != 12 {
		t.Errorf("Expected 42/12 for App.tsx, got %v", stats["src/App.tsx"])
	}
}

func TestGitStatusSchedulerCoalescesDirtyRefreshes(t *testing.T) {
	restore := overrideGitStatusSchedulerForTesting(10*time.Millisecond, time.Hour, time.Hour, time.Hour)
	defer restore()

	var calls atomic.Int32
	var inFlight atomic.Int32
	var overlapped atomic.Bool
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	previousGetGitStatus := getGitStatusForDaemon
	getGitStatusForDaemon = func(dir string, _ gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
		if inFlight.Add(1) > 1 {
			overlapped.Store(true)
		}
		defer inFlight.Add(-1)

		call := calls.Add(1)
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return testGitStatus(dir, fmt.Sprintf("file-%d.txt", call)), nil
	}
	defer func() {
		getGitStatusForDaemon = previousGetGitStatus
	}()

	d := &Daemon{}
	client := &wsClient{send: make(chan outboundMessage, 10)}
	d.handleSubscribeGitStatus(client, &protocol.SubscribeGitStatusMessage{
		Cmd:       protocol.CmdSubscribeGitStatus,
		Directory: "/repo",
	})
	defer client.stopGitStatusPoll()

	<-firstStarted
	for i := 0; i < 5; i++ {
		client.requestGitStatusRefresh(gitStatusRefreshRequest{reason: gitStatusRefreshReasonDirty})
	}
	close(releaseFirst)

	waitForGitStatusTestCondition(t, 500*time.Millisecond, func() bool {
		return calls.Load() == 2
	})
	time.Sleep(50 * time.Millisecond)

	if got := calls.Load(); got != 2 {
		t.Fatalf("git status calls = %d, want 2", got)
	}
	if overlapped.Load() {
		t.Fatal("git status refreshes overlapped")
	}
}

func TestGitOperationMarksMatchingStatusSubscriptionDirty(t *testing.T) {
	restore := overrideGitStatusSchedulerForTesting(10*time.Millisecond, time.Hour, time.Hour, time.Hour)
	defer restore()

	var calls atomic.Int32
	previousGetGitStatus := getGitStatusForDaemon
	getGitStatusForDaemon = func(dir string, _ gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
		call := calls.Add(1)
		return testGitStatus(dir, fmt.Sprintf("file-%d.txt", call)), nil
	}
	defer func() {
		getGitStatusForDaemon = previousGetGitStatus
	}()

	hub := newWSHub()
	d := &Daemon{wsHub: hub}
	client := &wsClient{send: make(chan outboundMessage, 10)}
	hub.clients[client] = true

	d.handleSubscribeGitStatus(client, &protocol.SubscribeGitStatusMessage{
		Cmd:       protocol.CmdSubscribeGitStatus,
		Directory: "/repo",
	})
	defer client.stopGitStatusPoll()
	waitForGitStatusTestCondition(t, 500*time.Millisecond, func() bool {
		return calls.Load() == 1
	})

	finish := d.beginGitOperation(protocol.GitOperationKindDeleteWorktree, "/repo/worktree", nil)
	finish(nil)

	waitForGitStatusTestCondition(t, 500*time.Millisecond, func() bool {
		return calls.Load() == 2
	})
}

func TestGitStatusSchedulerDelaysSafetyRefreshAfterSlowRun(t *testing.T) {
	restore := overrideGitStatusSchedulerForTesting(10*time.Millisecond, 20*time.Millisecond, time.Hour, time.Millisecond)
	defer restore()

	var calls atomic.Int32
	previousGetGitStatus := getGitStatusForDaemon
	getGitStatusForDaemon = func(dir string, _ gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
		call := calls.Add(1)
		time.Sleep(5 * time.Millisecond)
		return testGitStatus(dir, fmt.Sprintf("file-%d.txt", call)), nil
	}
	defer func() {
		getGitStatusForDaemon = previousGetGitStatus
	}()

	d := &Daemon{}
	client := &wsClient{send: make(chan outboundMessage, 10)}
	d.handleSubscribeGitStatus(client, &protocol.SubscribeGitStatusMessage{
		Cmd:       protocol.CmdSubscribeGitStatus,
		Directory: "/repo",
	})
	defer client.stopGitStatusPoll()

	waitForGitStatusTestCondition(t, 500*time.Millisecond, func() bool {
		return calls.Load() == 1
	})
	time.Sleep(60 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Fatalf("git status calls = %d, want 1 slow run without normal safety refresh", got)
	}
}

func TestGitStatusSchedulerUsesTrackedOnlyAfterLimitedRefresh(t *testing.T) {
	restore := overrideGitStatusSchedulerForTesting(10*time.Millisecond, time.Hour, time.Hour, time.Hour)
	defer restore()

	var calls atomic.Int32
	modes := make(chan gitStatusMode, 2)
	previousGetGitStatus := getGitStatusForDaemon
	getGitStatusForDaemon = func(dir string, mode gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
		modes <- mode
		call := calls.Add(1)
		status := testGitStatus(dir, fmt.Sprintf("file-%d.txt", call))
		if call == 1 {
			status.Limited = protocol.Ptr(true)
			status.Mode = protocol.Ptr(string(gitStatusModeTrackedOnly))
		}
		return status, nil
	}
	defer func() {
		getGitStatusForDaemon = previousGetGitStatus
	}()

	d := &Daemon{}
	client := &wsClient{send: make(chan outboundMessage, 10)}
	d.handleSubscribeGitStatus(client, &protocol.SubscribeGitStatusMessage{
		Cmd:       protocol.CmdSubscribeGitStatus,
		Directory: "/repo",
	})
	defer client.stopGitStatusPoll()

	waitForGitStatusTestCondition(t, 500*time.Millisecond, func() bool {
		return calls.Load() == 1
	})
	client.requestGitStatusRefresh(gitStatusRefreshRequest{reason: gitStatusRefreshReasonDirty})
	waitForGitStatusTestCondition(t, 500*time.Millisecond, func() bool {
		return calls.Load() == 2
	})

	first := <-modes
	second := <-modes
	if first != gitStatusModeFull {
		t.Fatalf("first mode = %q, want %q", first, gitStatusModeFull)
	}
	if second != gitStatusModeTrackedOnly {
		t.Fatalf("second mode = %q, want %q", second, gitStatusModeTrackedOnly)
	}
}

func TestGitStatusCoordinatorSharesInFlightStatusForRepoAndMode(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	previousGetGitStatus := getGitStatusForDaemon
	getGitStatusForDaemon = func(dir string, _ gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
		call := calls.Add(1)
		if call == 1 {
			close(started)
			<-release
		}
		return testGitStatus(dir, "src/shared.ts"), nil
	}
	defer func() {
		getGitStatusForDaemon = previousGetGitStatus
	}()

	d := &Daemon{}
	results := make(chan *protocol.GitStatusUpdateMessage, 2)
	for i := 0; i < 2; i++ {
		go func() {
			status, _, err := d.coordinator().Status("/repo", gitStatusModeFull)
			if err != nil {
				t.Errorf("Status failed: %v", err)
			}
			results <- status
		}()
	}

	<-started
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("git status calls while first refresh is in flight = %d, want 1", got)
	}
	close(release)

	for i := 0; i < 2; i++ {
		status := <-results
		if status == nil || len(status.Unstaged) != 1 || status.Unstaged[0].Path != "src/shared.ts" {
			t.Fatalf("status = %+v, want shared status result", status)
		}
	}
}

func TestTrackedOnlyStatusResultStaysLimited(t *testing.T) {
	previousRunGitStatusCommand := runGitStatusCommandForDaemon
	runGitStatusCommandForDaemon = func(_ string, _ time.Duration, args ...string) ([]byte, error) {
		if !containsArg(args, "--untracked-files=no") {
			t.Fatalf("args = %v, want tracked-only status", args)
		}
		return []byte(" M tracked.txt\x00"), nil
	}
	defer func() {
		runGitStatusCommandForDaemon = previousRunGitStatusCommand
	}()

	status, err := getGitStatusWithOptions("/repo", gitStatusOptions{
		mode: gitStatusModeTrackedOnly,
	})
	if err != nil {
		t.Fatalf("getGitStatusWithOptions failed: %v", err)
	}
	if !protocol.Deref(status.Limited) {
		t.Fatal("tracked-only status limited = false, want true")
	}
	if protocol.Deref(status.LimitedReason) == "" {
		t.Fatal("tracked-only status missing limited reason")
	}
}

func TestGetGitStatusWithOptionsFallsBackToTrackedOnlyAfterFullTimeout(t *testing.T) {
	var calls atomic.Int32
	argsSeen := make(chan []string, 2)
	previousRunGitStatusCommand := runGitStatusCommandForDaemon
	runGitStatusCommandForDaemon = func(_ string, _ time.Duration, args ...string) ([]byte, error) {
		argsSeen <- append([]string(nil), args...)
		if calls.Add(1) == 1 {
			return nil, fmt.Errorf("git status timed out after 5s: git status")
		}
		return []byte(" M tracked.txt\x00"), nil
	}
	defer func() {
		runGitStatusCommandForDaemon = previousRunGitStatusCommand
	}()

	status, err := getGitStatusWithOptions("/repo", gitStatusOptions{
		mode:        gitStatusModeFull,
		fullTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("getGitStatusWithOptions failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("status command calls = %d, want 2", calls.Load())
	}
	firstArgs := <-argsSeen
	secondArgs := <-argsSeen
	if !containsArg(firstArgs, "--untracked-files=all") {
		t.Fatalf("first args = %v, want full untracked status", firstArgs)
	}
	if !containsArg(secondArgs, "--untracked-files=no") {
		t.Fatalf("second args = %v, want tracked-only status", secondArgs)
	}
	if !protocol.Deref(status.Limited) {
		t.Fatal("status limited = false, want true")
	}
	if got := protocol.Deref(status.Mode); got != string(gitStatusModeTrackedOnly) {
		t.Fatalf("status mode = %q, want %q", got, gitStatusModeTrackedOnly)
	}
	if len(status.Untracked) != 0 {
		t.Fatalf("untracked = %v, want none in tracked-only fallback", status.Untracked)
	}
}

func TestSameOrNestedPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		dir  string
		want bool
	}{
		{name: "same", path: "/repo", dir: "/repo", want: true},
		{name: "nested", path: "/repo/worktree", dir: "/repo", want: true},
		{name: "sibling prefix", path: "/repo-two", dir: "/repo", want: false},
		{name: "parent", path: "/repo", dir: "/repo/worktree", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameOrNestedPath(tt.path, tt.dir); got != tt.want {
				t.Fatalf("sameOrNestedPath(%q, %q) = %v, want %v", tt.path, tt.dir, got, tt.want)
			}
		})
	}
}

func TestBranchDiffSnapshotSharesInFlightRefreshWithoutCache(t *testing.T) {
	previousGetBranchDiffFiles := getBranchDiffFilesForDaemon
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	getBranchDiffFilesForDaemon = func(_, _ string) ([]attngit.DiffFileInfo, error) {
		call := calls.Add(1)
		if call == 1 {
			close(started)
			<-release
		}
		return []attngit.DiffFileInfo{{Path: "src/shared.ts", Status: "modified"}}, nil
	}
	defer func() {
		getBranchDiffFilesForDaemon = previousGetBranchDiffFiles
	}()

	d := &Daemon{}
	results := make(chan branchDiffSnapshot, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			snapshot, err := d.coordinator().BranchDiffSnapshot("/repo", "origin/main")
			results <- snapshot
			errs <- err
		}()
	}

	<-started
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("branch diff calls while first refresh is in flight = %d, want 1", got)
	}
	close(release)

	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("getBranchDiffSnapshot failed: %v", err)
		}
		snapshot := <-results
		if len(snapshot.files) != 1 || snapshot.files[0].Path != "src/shared.ts" {
			t.Fatalf("snapshot files = %+v, want shared file", snapshot.files)
		}
	}
}

func TestBranchDiffSnapshotReturnsCachedResultDuringRefresh(t *testing.T) {
	previousGetBranchDiffFiles := getBranchDiffFilesForDaemon
	var calls atomic.Int32
	refreshStarted := make(chan struct{})
	refreshDone := make(chan struct{})
	releaseRefresh := make(chan struct{})
	getBranchDiffFilesForDaemon = func(_, _ string) ([]attngit.DiffFileInfo, error) {
		switch calls.Add(1) {
		case 1:
			return []attngit.DiffFileInfo{{Path: "src/old.ts", Status: "modified"}}, nil
		case 2:
			close(refreshStarted)
			<-releaseRefresh
			close(refreshDone)
			return []attngit.DiffFileInfo{{Path: "src/new.ts", Status: "modified"}}, nil
		default:
			return []attngit.DiffFileInfo{{Path: "src/new.ts", Status: "modified"}}, nil
		}
	}
	defer func() {
		getBranchDiffFilesForDaemon = previousGetBranchDiffFiles
	}()

	d := &Daemon{}
	first, err := d.coordinator().BranchDiffSnapshot("/repo", "origin/main")
	if err != nil {
		t.Fatalf("first getBranchDiffSnapshot failed: %v", err)
	}
	if got := first.files[0].Path; got != "src/old.ts" {
		t.Fatalf("first cached path = %q, want old snapshot", got)
	}

	second, err := d.coordinator().BranchDiffSnapshot("/repo", "origin/main")
	if err != nil {
		t.Fatalf("second getBranchDiffSnapshot failed: %v", err)
	}
	if got := second.files[0].Path; got != "src/old.ts" {
		t.Fatalf("second cached path = %q, want old snapshot while refresh runs", got)
	}

	<-refreshStarted
	close(releaseRefresh)
	<-refreshDone

	third, err := d.coordinator().BranchDiffSnapshot("/repo", "origin/main")
	if err != nil {
		t.Fatalf("third getBranchDiffSnapshot failed: %v", err)
	}
	if got := third.files[0].Path; got != "src/new.ts" {
		t.Fatalf("third cached path = %q, want refreshed snapshot", got)
	}
	waitForGitStatusTestCondition(t, 500*time.Millisecond, func() bool {
		return calls.Load() >= 3
	})
}

func TestGitCoordinatorSharesInFlightFileDiff(t *testing.T) {
	previousReadFileDiff := readFileDiffForDaemon
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	readFileDiffForDaemon = func(_, _, _ string, _ bool) (fileDiffContent, error) {
		call := calls.Add(1)
		if call == 1 {
			close(started)
			<-release
		}
		return fileDiffContent{original: "before", modified: "after"}, nil
	}
	defer func() {
		readFileDiffForDaemon = previousReadFileDiff
	}()

	d := &Daemon{}
	results := make(chan fileDiffContent, 2)
	for i := 0; i < 2; i++ {
		go func() {
			content, err := d.coordinator().FileDiff("/repo", "src/file.ts", "HEAD", false)
			if err != nil {
				t.Errorf("FileDiff failed: %v", err)
			}
			results <- content
		}()
	}

	<-started
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("file diff calls while first refresh is in flight = %d, want 1", got)
	}
	close(release)

	for i := 0; i < 2; i++ {
		content := <-results
		if content.original != "before" || content.modified != "after" {
			t.Fatalf("file diff content = %+v, want shared content", content)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func overrideGitStatusSchedulerForTesting(debounce, safety, slowSafety, slowThreshold time.Duration) func() {
	previousDebounce := gitStatusRefreshDebounce
	previousSafety := gitStatusSafetyInterval
	previousSlowSafety := gitStatusSlowSafetyInterval
	previousSlowThreshold := gitStatusSlowRefreshDuration

	gitStatusRefreshDebounce = debounce
	gitStatusSafetyInterval = safety
	gitStatusSlowSafetyInterval = slowSafety
	gitStatusSlowRefreshDuration = slowThreshold

	return func() {
		gitStatusRefreshDebounce = previousDebounce
		gitStatusSafetyInterval = previousSafety
		gitStatusSlowSafetyInterval = previousSlowSafety
		gitStatusSlowRefreshDuration = previousSlowThreshold
	}
}

func testGitStatus(dir, path string) *protocol.GitStatusUpdateMessage {
	return &protocol.GitStatusUpdateMessage{
		Event:     protocol.EventGitStatusUpdate,
		Directory: dir,
		Staged:    []protocol.GitFileChange{},
		Unstaged:  []protocol.GitFileChange{{Path: path, Status: "modified"}},
		Untracked: []protocol.GitFileChange{},
	}
}

func waitForGitStatusTestCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
