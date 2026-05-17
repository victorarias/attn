package daemon

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

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
	getGitStatusForDaemon = func(dir string) (*protocol.GitStatusUpdateMessage, error) {
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
	getGitStatusForDaemon = func(dir string) (*protocol.GitStatusUpdateMessage, error) {
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
	getGitStatusForDaemon = func(dir string) (*protocol.GitStatusUpdateMessage, error) {
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
