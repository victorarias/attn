package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

var errSpawnRefusedInThisTest = errors.New("the pty backend refuses to spawn in this test")

func reopenDaemonWithBackend(t *testing.T, d *Daemon) *fakeSpawnBackend {
	t.Helper()
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	t.Cleanup(d.stopEventBus)
	return backend
}

func worktreeRegistrations(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read the worktree registrations of %s: %v", repo, err)
	}
	return string(out)
}

// Decision 4: reopen never writes to the repository on its own. Asking for a
// verdict, and a reopen the verdict refuses, both leave git as they found it.
func TestNoReopenTouchesTheRepositoryWithoutBeingAskedByName(t *testing.T) {
	d, repo, worktree := closedWorktreeWithDeletedDirectory(t, "untouched", "feat/untouched", false)
	reopenDaemonWithBackend(t, d)
	before := worktreeRegistrations(t, repo)

	verdict := decidedReopenVerdict(t, d, "untouched")
	if !verdict.offers(protocol.SessionReopenActionRecreateWorktreeAndReopen) {
		t.Fatalf("the verdict does not offer the recreate action: %q", verdict.Reason)
	}
	if _, err := d.reopenSession("untouched", "", ""); err == nil {
		t.Fatal("a plain reopen ran instead of refusing a directory that is gone")
	}
	if _, err := d.reopenSession("untouched", protocol.SessionReopenActionReopen, ""); err == nil {
		t.Fatal("--action reopen ran instead of refusing a directory that is gone")
	}

	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("the worktree directory is back at %s without anyone asking for it", worktree)
	}
	if after := worktreeRegistrations(t, repo); after != before {
		t.Errorf("the worktree registrations changed without an explicit action:\nbefore:\n%s\nafter:\n%s",
			before, after)
	}
	if !d.store.SessionClosed("untouched") {
		t.Error("a refused reopen took the session out of the ledger")
	}
}

func TestARefusedReopenNamesTheActionsItOffersInstead(t *testing.T) {
	d, _, _ := closedWorktreeWithDeletedDirectory(t, "refused", "feat/refused", false)
	reopenDaemonWithBackend(t, d)

	_, err := d.reopenSession("refused", "", "")
	if err == nil {
		t.Fatal("the plain reopen was not refused")
	}
	if !strings.Contains(err.Error(), string(protocol.SessionReopenActionRecreateWorktreeAndReopen)) {
		t.Errorf("refusal %q does not name the action offered instead", err)
	}
}

func TestRecreatingTheWorktreeBringsTheSessionBackOnItsOwnBranch(t *testing.T) {
	d, _, worktree := closedWorktreeWithDeletedDirectory(t, "recreate", "feat/recreate", false)
	backend := reopenDaemonWithBackend(t, d)
	before := spawnCount(backend)

	outcome, err := d.reopenSession("recreate", protocol.SessionReopenActionRecreateWorktreeAndReopen, "")
	if err != nil {
		t.Fatalf("recreate_worktree_and_reopen: %v", err)
	}

	if outcome.WorktreeCreated == "" {
		t.Error("the outcome does not name the worktree it recreated")
	}
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("the worktree is not back at %s: %v", worktree, err)
	}
	if branch, err := attngit.GetCurrentBranch(worktree); err != nil || branch != "feat/recreate" {
		t.Errorf("the recreated worktree is on %q (%v), want feat/recreate", branch, err)
	}
	if d.store.SessionClosed("recreate") {
		t.Error("the session is still closed after its reopen")
	}
	if session := d.store.Get("recreate"); session == nil {
		t.Fatal("the reopened session is not registered")
	}
	if d.store.GetWorkspace(outcome.WorkspaceID) == nil {
		t.Errorf("workspace %s was not created for the reopened session", outcome.WorkspaceID)
	}
	spawn := resumeSpawnForSession(t, backend, "recreate", before)
	if spawn.CWD != attngit.CanonicalizePath(worktree) {
		t.Errorf("spawn cwd = %s, want the recreated worktree %s", spawn.CWD, worktree)
	}
	if spawn.ResumeSessionID != "conv-recreate" {
		t.Errorf("resume id = %q, want the saved conversation conv-recreate", spawn.ResumeSessionID)
	}
}

func TestFetchingTheBranchBackRecreatesTheWorktreeFromTheRemote(t *testing.T) {
	d, repo, worktree := closedWorktreeWithDeletedDirectory(t, "fetch", "feat/fetch", true)
	backend := reopenDaemonWithBackend(t, d)
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/fetch")
	before := spawnCount(backend)

	verdict := decidedReopenVerdict(t, d, "fetch")
	if !verdict.offers(protocol.SessionReopenActionFetchRecreateAndReopen) {
		t.Fatalf("the verdict does not offer the fetch action: %q", verdict.Reason)
	}
	if _, err := d.reopenSession("fetch", protocol.SessionReopenActionFetchRecreateAndReopen, ""); err != nil {
		t.Fatalf("fetch_recreate_and_reopen: %v", err)
	}

	if branch, err := attngit.GetCurrentBranch(worktree); err != nil || branch != "feat/fetch" {
		t.Errorf("the recreated worktree is on %q (%v), want feat/fetch", branch, err)
	}
	if d.store.SessionClosed("fetch") {
		t.Error("the session is still closed after its reopen")
	}
	if spawn := resumeSpawnForSession(t, backend, "fetch", before); spawn.ResumeSessionID != "conv-fetch" {
		t.Errorf("resume id = %q, want the saved conversation conv-fetch", spawn.ResumeSessionID)
	}
}

func TestStartingFreshFromTheDefaultBranchPutsTheWorktreeBackWithoutTheConversation(t *testing.T) {
	d, repo, worktree := closedWorktreeWithDeletedDirectory(t, "default", "feat/default", false)
	backend := reopenDaemonWithBackend(t, d)
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/default")
	before := spawnCount(backend)

	if _, err := d.reopenSession("default", protocol.SessionReopenActionStartFreshDefaultBranch, ""); err != nil {
		t.Fatalf("start_fresh_default_branch: %v", err)
	}

	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("the worktree is not back at %s: %v", worktree, err)
	}
	if spawn := resumeSpawnForSession(t, backend, "default", before); spawn.ResumeSessionID != "" {
		t.Errorf("resume id = %q, want a fresh conversation", spawn.ResumeSessionID)
	}
}

func TestStartingFreshInTheSamePlaceKeepsTheDirectoryAndDropsTheConversation(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	writeCodexRolloutFixture(t, "another-conversation")
	directory := t.TempDir()
	closeReopenSession(t, d, reopenSession{
		ID: "fresh-here", Directory: directory, Agent: "codex", Resume: "conv-that-is-gone",
	})
	before := spawnCount(backend)

	outcome, err := d.reopenSession("fresh-here", protocol.SessionReopenActionStartFreshSamePlace, "")
	if err != nil {
		t.Fatalf("start_fresh_same_place: %v", err)
	}
	if attngit.CanonicalizePath(outcome.Directory) != attngit.CanonicalizePath(directory) {
		t.Errorf("directory = %s, want the saved one %s", outcome.Directory, directory)
	}
	if spawn := resumeSpawnForSession(t, backend, "fresh-here", before); spawn.ResumeSessionID != "" {
		t.Errorf("resume id = %q, want no resume of a conversation that is gone", spawn.ResumeSessionID)
	}
}

func TestStartingFreshElsewhereNeedsTheDirectoryToStartIn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	reopenDaemonWithBackend(t, d)
	writeCodexRolloutFixture(t, "conv-elsewhere")
	gone := filepath.Join(t.TempDir(), "deleted")
	closeReopenSession(t, d, reopenSession{
		ID: "elsewhere", Directory: gone, Agent: "codex", Resume: "conv-elsewhere",
	})

	if _, err := d.reopenSession("elsewhere", protocol.SessionReopenActionStartFreshElsewhere, ""); err == nil {
		t.Fatal("start_fresh_elsewhere ran without a directory to start in")
	}
	if !d.store.SessionClosed("elsewhere") {
		t.Fatal("a refused start took the session out of the ledger")
	}

	chosen := t.TempDir()
	outcome, err := d.reopenSession("elsewhere", protocol.SessionReopenActionStartFreshElsewhere, chosen)
	if err != nil {
		t.Fatalf("start_fresh_elsewhere with a directory: %v", err)
	}
	if attngit.CanonicalizePath(outcome.Directory) != attngit.CanonicalizePath(chosen) {
		t.Errorf("directory = %s, want the one the caller chose %s", outcome.Directory, chosen)
	}
}

// The ledger has to survive a reopen that does not finish: the row goes back
// under the closer and reason it had, not out of the ledger altogether.
func TestAFailedReopenPutsTheCloseBackAsItWas(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	backend.spawnErr = errSpawnRefusedInThisTest
	writeCodexRolloutFixture(t, "conv-failed")
	closeReopenSession(t, d, reopenSession{
		ID: "failed", Directory: t.TempDir(), Agent: "codex", Resume: "conv-failed",
		ClosedBy: "sess-boss", Reason: "brief delivered",
	})

	if _, err := d.reopenSession("failed", "", ""); err == nil {
		t.Fatal("the reopen reported success although the spawn failed")
	}

	entry := d.store.SessionLedgerEntry("failed")
	if entry == nil {
		t.Fatal("the failed reopen took the session out of the ledger")
	}
	if protocol.Deref(entry.ClosedAt) == "" {
		t.Error("the failed reopen left the session open")
	}
	if by := protocol.Deref(entry.ClosedBy); by != "sess-boss" {
		t.Errorf("closed_by = %q, want the original closer restored", by)
	}
	if reason := protocol.Deref(entry.CloseReason); reason != "brief delivered" {
		t.Errorf("close_reason = %q, want the original reason restored", reason)
	}
}

// A closed run's cost stays counted; the cursor says where counting stopped.
// Clearing it while keeping the ledger would count the same usage twice.
func TestReopeningLeavesTheCostCursorWhereTheCloseLeftIt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	reopenDaemonWithBackend(t, d)
	writeCodexRolloutFixture(t, "conv-cost")
	const cursor = "cursor-at-the-close"
	closeReopenSession(t, d, reopenSession{
		ID: "cost", Directory: t.TempDir(), Agent: "codex", Resume: "conv-cost", CostCursor: cursor,
	})
	closed, err := d.store.SessionCost("cost")
	if err != nil {
		t.Fatalf("read the cost at the close: %v", err)
	}
	if closed.Cursor != cursor {
		t.Fatalf("cost cursor at the close = %q, want the fixture %q", closed.Cursor, cursor)
	}

	if _, err := d.reopenSession("cost", "", ""); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	after, err := d.store.SessionCost("cost")
	if err != nil {
		t.Fatalf("read the cost after the reopen: %v", err)
	}
	if after.Cursor != cursor {
		t.Errorf("cost cursor = %q, want the close's cursor %q kept across the reopen", after.Cursor, cursor)
	}
}

func sessionShowResult(t *testing.T, d *Daemon, sessionID string) protocol.SessionShowResult {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go d.handleConnection(server)
	if err := json.NewEncoder(client).Encode(protocol.SessionShowMessage{
		Cmd: protocol.CmdSessionShow, SessionID: sessionID,
	}); err != nil {
		t.Fatalf("encode session_show: %v", err)
	}
	var response protocol.Response
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatalf("decode session_show response: %v", err)
	}
	if !response.Ok || response.SessionShowResult == nil {
		t.Fatalf("session_show failed: %+v", response)
	}
	return *response.SessionShowResult
}

// A verdict is served from the last inspection, so asking has to start the next
// one: a branch pushed back into the repository must reach the following ask.
func TestAskingForAVerdictRefreshesTheBranchItRead(t *testing.T) {
	d, repo, _ := closedWorktreeWithDeletedDirectory(t, "refreshed", "feat/refreshed", false)
	reopenDaemonWithBackend(t, d)
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/refreshed")

	gone := decidedReopenVerdict(t, d, "refreshed")
	if gone.BranchState != branchStateGone {
		t.Fatalf("branch state = %q, want %q", gone.BranchState, branchStateGone)
	}

	shown := sessionShowResult(t, d, "refreshed")
	if shown.Reopen == nil || protocol.Deref(shown.Reopen.BranchState) != branchStateGone {
		t.Fatalf("session_show carried %+v, want the branch reported gone", shown.Reopen)
	}

	// Somebody pushes the branch back outside attn. The ask that follows serves the
	// stale answer and must leave a fresh inspection behind it.
	runGitDaemon(t, repo, "branch", "feat/refreshed", "main")
	sessionShowResult(t, d, "refreshed")
	waitForBranchInspection(t, d, repo, "feat/refreshed")

	back := sessionShowResult(t, d, "refreshed")
	if back.Reopen == nil || protocol.Deref(back.Reopen.BranchState) != branchStateLocal {
		t.Fatalf("session_show carried %+v, want the branch back as local", back.Reopen)
	}
}

// Waits on the inspection the last ask started, without starting one: a refresh
// that never ran leaves neither a running check nor a fresher observation.
func waitForBranchInspection(t *testing.T, d *Daemon, repo, branch string) {
	t.Helper()
	key := branchInspectionKey(repo, branch)
	d.branchInspectionsMu.Lock()
	running := d.branchInspectionsRunning[key]
	d.branchInspectionsMu.Unlock()
	if running != nil {
		<-running
	}
}
