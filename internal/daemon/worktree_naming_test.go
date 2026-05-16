package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDoCreateWorktree_ResolvesMainRepoFromWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "hurdy-gurdy")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("failed to create main repo dir: %v", err)
	}

	runGitDaemon(t, mainDir, "init")
	runGitDaemon(t, mainDir, "commit", "--allow-empty", "-m", "init")

	// Simulate a pre-existing worktree with a generated suffix-heavy name.
	existingWorktree := filepath.Join(tmpDir, "hurdy-gurdy--feat-auto-bump-yt-dlp--fork-hurdy-gurdy")
	runGitDaemon(t, mainDir, "worktree", "add", "-b", "feat/auto-bump-yt-dlp", existingWorktree)

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	path, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		MainRepo: existingWorktree,
		Branch:   "fork/fun",
	})
	if err != nil {
		t.Fatalf("doCreateWorktree failed: %v", err)
	}

	wantPath := filepath.Join(tmpDir, "hurdy-gurdy--fork-fun")
	if canonicalPathDaemon(path) != canonicalPathDaemon(wantPath) {
		t.Fatalf("worktree path = %q, want %q", path, wantPath)
	}

	created := d.store.GetWorktree(path)
	if created == nil {
		t.Fatalf("expected created worktree in store for path %q", path)
	}
	if canonicalPathDaemon(created.MainRepo) != canonicalPathDaemon(mainDir) {
		t.Fatalf("stored main repo = %q, want %q", created.MainRepo, mainDir)
	}
}

func TestDoCreateWorktreeFromBranch_ResolvesMainRepoFromWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "hurdy-gurdy")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("failed to create main repo dir: %v", err)
	}

	runGitDaemon(t, mainDir, "init")
	runGitDaemon(t, mainDir, "commit", "--allow-empty", "-m", "init")
	runGitDaemon(t, mainDir, "branch", "feature/fun")

	existingWorktree := filepath.Join(tmpDir, "hurdy-gurdy--feat-auto-bump-yt-dlp--fork-hurdy-gurdy")
	runGitDaemon(t, mainDir, "worktree", "add", "-b", "feat/auto-bump-yt-dlp", existingWorktree)

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	path, err := d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
		MainRepo: existingWorktree,
		Branch:   "feature/fun",
	})
	if err != nil {
		t.Fatalf("doCreateWorktreeFromBranch failed: %v", err)
	}

	wantPath := filepath.Join(tmpDir, "hurdy-gurdy--feature-fun")
	if canonicalPathDaemon(path) != canonicalPathDaemon(wantPath) {
		t.Fatalf("worktree path = %q, want %q", path, wantPath)
	}

	created := d.store.GetWorktree(path)
	if created == nil {
		t.Fatalf("expected created worktree in store for path %q", path)
	}
	if canonicalPathDaemon(created.MainRepo) != canonicalPathDaemon(mainDir) {
		t.Fatalf("stored main repo = %q, want %q", created.MainRepo, mainDir)
	}
}

func TestDoDeleteWorktree_BroadcastsGitOperationLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "hurdy-gurdy")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("failed to create main repo dir: %v", err)
	}

	runGitDaemon(t, mainDir, "init")
	runGitDaemon(t, mainDir, "commit", "--allow-empty", "-m", "init")
	worktreePath := filepath.Join(tmpDir, "hurdy-gurdy--feature-slow-delete")
	runGitDaemon(t, mainDir, "worktree", "add", "-b", "feature/slow-delete", worktreePath)
	worktreePath = canonicalPathDaemon(worktreePath)

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	cap := captureBroadcasts(d)
	endpointID := "endpoint-1"

	if err := d.doDeleteWorktree(worktreePath, protocol.Ptr(endpointID)); err != nil {
		t.Fatalf("doDeleteWorktree failed: %v", err)
	}

	var started, finished protocol.WebSocketEvent
	var sawStarted, sawFinished bool
	for _, event := range cap.snapshot() {
		switch event.Event {
		case protocol.EventGitOperationStarted:
			started = event
			sawStarted = true
		case protocol.EventGitOperationFinished:
			finished = event
			sawFinished = true
		}
	}
	if !sawStarted {
		t.Fatalf("missing %s event", protocol.EventGitOperationStarted)
	}
	if !sawFinished {
		t.Fatalf("missing %s event", protocol.EventGitOperationFinished)
	}
	if started.Operation == nil || finished.Operation == nil {
		t.Fatalf("expected operation payloads: started=%#v finished=%#v", started.Operation, finished.Operation)
	}
	if started.Operation.ID == "" {
		t.Fatalf("started operation id is empty")
	}
	if finished.Operation.ID != started.Operation.ID {
		t.Fatalf("finished operation id = %q, want %q", finished.Operation.ID, started.Operation.ID)
	}
	if started.Operation.Kind != protocol.GitOperationKindDeleteWorktree {
		t.Fatalf("started operation kind = %q", started.Operation.Kind)
	}
	if started.Operation.Status != protocol.GitOperationStatusRunning {
		t.Fatalf("started operation status = %q", started.Operation.Status)
	}
	if finished.Operation.Status != protocol.GitOperationStatusSucceeded {
		t.Fatalf("finished operation status = %q", finished.Operation.Status)
	}
	if protocol.Deref(started.Operation.Path) != worktreePath {
		t.Fatalf("started operation path = %q, want %q", protocol.Deref(started.Operation.Path), worktreePath)
	}
	if protocol.Deref(started.Operation.EndpointID) != endpointID {
		t.Fatalf("started endpoint = %q, want %q", protocol.Deref(started.Operation.EndpointID), endpointID)
	}
	if finished.Operation.FinishedAt == nil {
		t.Fatalf("finished operation missing finished_at")
	}
	if finished.Operation.DurationMs == nil {
		t.Fatalf("finished operation missing duration_ms")
	}
}

func runGitDaemon(t *testing.T, dir string, args ...string) {
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

func canonicalPathDaemon(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
