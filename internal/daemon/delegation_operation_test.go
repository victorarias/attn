package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func waitDelegationOperation(t *testing.T, d *Daemon, id string) *protocol.DelegationOperation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		op, err := d.delegationOperation(id)
		if err != nil {
			t.Fatalf("delegationOperation(%s): %v", id, err)
		}
		if op.State == protocol.DelegationOperationStateCompleted || op.State == protocol.DelegationOperationStateFailed {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for delegation operation %s", id)
	return nil
}

func TestDelegationOperationSequentialAndResponseLossRetryConverge(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceID); err != nil {
		t.Fatal(err)
	}
	consumeDelegatedPrompt(t, backend)
	msg := &protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: "stable-request", SourceSessionID: sourceID, Brief: "Do the work once.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("once")}

	first, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate losing the first accepted/final response: the caller retains only
	// its request key and invokes the same logical request again.
	second, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID || first.SessionID != second.SessionID {
		t.Fatalf("retries diverged: first=%+v second=%+v", first, second)
	}
	done := waitDelegationOperation(t, d, first.OperationID)
	if done.Result == nil {
		t.Fatalf("completed operation has no result: %+v", done)
	}
	if done.WorktreePath != nil {
		t.Fatalf("ordinary delegation reported worktree_path=%q", protocol.Deref(done.WorktreePath))
	}
	if got := len(d.store.List("")); got != 2 {
		t.Fatalf("sessions=%d, want source + one delegate", got)
	}
	tickets, err := d.store.ListTickets(store.TicketListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 || tickets[0].Assignee != done.SessionID {
		t.Fatalf("tickets=%+v, want exactly one bound ticket", tickets)
	}
}

func TestDelegationOperationReservesOperationIDNamespace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	_, err := d.startDelegation(&protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: "op-caller-value", SourceSessionID: sourceID, Brief: "Must reject.", Agent: protocol.Ptr("codex")})
	if err == nil || !strings.Contains(err.Error(), "reserved operation prefix") {
		t.Fatalf("error=%v", err)
	}
}

func TestRecoveredDelegationResultDistinguishesReusedWorktree(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	session := &protocol.Session{ID: "session", WorkspaceID: "workspace", Directory: "/tmp/shared", IsWorktree: protocol.Ptr(true)}
	reused := d.completedDelegationResult(session, delegationPlacementNew, false)
	if reused.WorktreeCreated != nil {
		t.Fatalf("reused worktree reported created=%v", protocol.Deref(reused.WorktreeCreated))
	}
	created := d.completedDelegationResult(session, delegationPlacementNew, true)
	if !protocol.Deref(created.WorktreeCreated) {
		t.Fatal("owned worktree lost created receipt")
	}
}

func TestDelegationOperationConcurrentRetriesConverge(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	msg := protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: "concurrent-request", SourceSessionID: sourceID, Brief: "Launch once concurrently.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("parallel")}
	const callers = 12
	results := make(chan *protocol.DelegationOperation, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := msg
			op, err := d.startDelegation(&copy)
			if err != nil {
				errs <- err
				return
			}
			results <- op
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	operationID := ""
	for op := range results {
		if operationID == "" {
			operationID = op.OperationID
		}
		if op.OperationID != operationID {
			t.Fatalf("operation ids diverged: %s != %s", op.OperationID, operationID)
		}
	}
	done := waitDelegationOperation(t, d, operationID)
	if done.Result == nil || len(d.store.List("")) != 2 {
		t.Fatalf("operation=%+v sessions=%d", done, len(d.store.List("")))
	}
}

func TestDelegationOperationAcceptedBeforeSlowPreparation(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceID); err != nil {
		t.Fatal(err)
	}
	consumeDelegatedPrompt(t, backend)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	d.delegationWorktreePrepareHook = func(string) {
		entered <- struct{}{}
		<-release
	}
	start := time.Now()
	op, err := d.startDelegation(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "slow-request", SourceSessionID: sourceID,
		Brief: "Slow worktree launch.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("slow"),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: "feat/slow", Path: protocol.Ptr(filepath.Join(root, "repo--slow"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("durable acceptance was not prompt: %v", time.Since(start))
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("launch did not reach controlled slow seam")
	}
	inProgress, err := d.delegationOperation(op.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if inProgress.State != protocol.DelegationOperationStatePreparing {
		t.Fatalf("state=%s, want preparing", inProgress.State)
	}
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "replacement-chief"); err != nil {
		t.Fatal(err)
	}
	close(release)
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted {
		t.Fatalf("done=%+v", done)
	}
	tickets, err := d.store.ListTickets(store.TicketListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 || tickets[0].Assignee != done.SessionID {
		t.Fatalf("tickets=%+v, want admission-time chief delegation ticket", tickets)
	}
}

func TestDelegationOperationRestartResumesAcceptedRecord(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	persistent, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	d1 := NewForTesting(filepath.Join(t.TempDir(), "one.sock"))
	_ = d1.store.Close()
	d1.store = persistent
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d1, backend)
	msg := protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: "restart-request", SourceSessionID: sourceID, Brief: "Resume after restart.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("restart")}
	requestJSON, _ := json.Marshal(msg)
	record, claimed, err := d1.store.ClaimDelegationOperation(msg.RequestID, "operation-restart", "session-restart", "", string(requestJSON), time.Now())
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	if err := d1.store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	d2 := NewForTesting(filepath.Join(t.TempDir(), "two.sock"))
	_ = d2.store.Close()
	d2.store = reopened
	d2.ptyBackend = &fakeSpawnBackend{}
	consumeDelegatedPrompt(t, d2.ptyBackend.(*fakeSpawnBackend))
	d2.loadWorkspacesFromStore()
	d2.resumePendingDelegations()
	done := waitDelegationOperation(t, d2, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.SessionID != "session-restart" {
		t.Fatalf("resumed operation=%+v", done)
	}
}

func TestDelegationOperationAdoptsReconciledReservedRuntime(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceID, cwd := setupDelegationSource(t, d, backend)
	msg := protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: "spawn-crash", SourceSessionID: sourceID, Brief: "Adopt the surviving runtime.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("adopted")}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-spawn-crash", "session-spawn-crash", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Startup worker reconciliation found the stable runtime ID that was spawned
	// before the old daemon could persist its full session association.
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: record.Operation.SessionID, Label: filepath.Base(cwd), Agent: protocol.SessionAgentCodex, Directory: cwd, State: protocol.SessionStateLaunching, StateSince: now, StateUpdatedAt: now, LastSeen: now})
	backend.sessionIDs = append(backend.sessionIDs, record.Operation.SessionID)
	d.runDelegationOperation(record.Operation.OperationID)
	done := waitDelegationOperation(t, d, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.WorkspaceID == nil || protocol.Deref(done.WorkspaceID) != workspaceID {
		t.Fatalf("operation=%+v", done)
	}
	adopted := d.store.Get(record.Operation.SessionID)
	if adopted == nil || adopted.WorkspaceID != workspaceID || adopted.Label != "adopted" {
		t.Fatalf("adopted session=%+v", adopted)
	}
	if got := len(backend.spawnOpts); got != 1 {
		t.Fatalf("spawn count=%d, want only source runtime", got)
	}
}

func TestDelegationOperationRespawnsPersistedSessionWithoutLiveRuntime(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceID, cwd := setupDelegationSource(t, d, backend)
	msg := protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: "spawn-missing", SourceSessionID: sourceID, Brief: "Recover the missing runtime.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("respawned")}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-spawn-missing", "session-spawn-missing", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: record.Operation.SessionID, WorkspaceID: workspaceID, Label: "respawned", Agent: protocol.SessionAgentCodex, Directory: cwd, State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now, Recoverable: protocol.Ptr(true)})
	d.runDelegationOperation(record.Operation.OperationID)
	done := waitDelegationOperation(t, d, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted {
		t.Fatalf("operation=%+v", done)
	}
	if got := len(backend.spawnOpts); got != 2 {
		t.Fatalf("spawn count=%d, want source plus recovered delegation", got)
	}
	if backend.spawnOpts[1].ID != record.Operation.SessionID {
		t.Fatalf("recovered spawn=%+v", backend.spawnOpts[1])
	}
}

func TestDelegationOperationTerminalFailureRetryDoesNotRelaunch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	msg := &protocol.DelegateMessage{Cmd: protocol.CmdDelegate, RequestID: "failed-request", SourceSessionID: sourceID, Brief: "Fail once.", Agent: protocol.Ptr("missing-agent")}
	first, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitDelegationOperation(t, d, first.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	second, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationID != first.OperationID || second.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("retry=%+v first=%+v", second, first)
	}
	if len(d.store.List("")) != 1 {
		t.Fatalf("failure retry created a session")
	}
}

func TestDelegationRestartDoesNotOwnWorktreeFromPathJournalAlone(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := filepath.Join(root, "repo--external")
	msg := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "path-only", SourceSessionID: sourceID,
		Brief: "Do not adopt external work.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("path-only"),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: "feat/external", Path: protocol.Ptr(path)},
	}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-path-only", "session-path-only", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.UpdateDelegationOperation(record.Operation.OperationID, protocol.DelegationOperationStatePreparing,
		"preparing worktree "+path, "", "", path, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The daemon stopped after journaling intent but before creation. A different
	// actor then created the same requested branch/path.
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/external", path)
	d.runDelegationOperation(record.Operation.OperationID)
	failed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("external worktree was removed: %v", err)
	}
	stored, err := d.store.GetDelegationOperation(record.Operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorktreeOwned {
		t.Fatal("path intent was incorrectly promoted to cleanup ownership")
	}
}

func TestDelegationRestartDoesNotDeleteReplacementForPreviouslyOwnedWorktree(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := filepath.Join(root, "repo--replacement")
	msg := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "owned-replaced", SourceSessionID: sourceID,
		Brief: "Do not delete replacement work.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("owned-replaced"),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: "feat/replacement", Path: protocol.Ptr(path)},
	}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-owned-replaced", "session-owned-replaced", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/replacement", path)
	if err := d.store.MarkDelegationWorktreeOwned(record.Operation.OperationID, path, "original-owner-token", time.Now()); err != nil {
		t.Fatal(err)
	}
	// The original daemon stopped after recording ownership. During the outage,
	// another actor replaced the worktree with a new instance at the same path.
	runGitDaemon(t, mainRepo, "worktree", "remove", path)
	runGitDaemon(t, mainRepo, "branch", "-D", "feat/replacement")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/replacement", path)

	d.runDelegationOperation(record.Operation.OperationID)
	failed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement worktree was removed: %v", err)
	}
}

func TestDelegationRestartResumesPreviouslyOwnedWorktreeWithMatchingMarker(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := filepath.Join(root, "repo--owned")
	msg := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "owned-resume", SourceSessionID: sourceID,
		Brief: "Resume original work.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("owned-resume"),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: "feat/owned", Path: protocol.Ptr(path)},
	}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-owned-resume", "session-owned-resume", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/owned", path)
	const ownerToken = "matching-owner-token"
	if err := writeDelegationWorktreeOwner(path, ownerToken); err != nil {
		t.Fatal(err)
	}
	if err := d.store.MarkDelegationWorktreeOwned(record.Operation.OperationID, path, ownerToken, time.Now()); err != nil {
		t.Fatal(err)
	}

	d.runDelegationOperation(record.Operation.OperationID)
	completed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if completed.State != protocol.DelegationOperationStateCompleted || completed.Result == nil {
		t.Fatalf("operation=%+v", completed)
	}
	if completed.Result.SessionID != record.Operation.SessionID || !protocol.Deref(completed.Result.WorktreeCreated) {
		t.Fatalf("result=%+v", completed.Result)
	}
}

func TestDelegationRestartLeavesOwnedWorktreeWhenAnotherSessionOccupiesIt(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	path := filepath.Join(root, "repo--occupied")
	msg := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "owned-occupied", SourceSessionID: sourceID,
		Brief: "Do not disturb the occupant.", Agent: protocol.Ptr("codex"), Label: protocol.Ptr("owned-occupied"),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: "feat/occupied", Path: protocol.Ptr(path)},
	}
	encoded, _ := json.Marshal(msg)
	record, _, err := d.store.ClaimDelegationOperation(msg.RequestID, "operation-owned-occupied", "session-owned-occupied", "", string(encoded), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "feat/occupied", path)
	const ownerToken = "occupied-owner-token"
	if err := writeDelegationWorktreeOwner(path, ownerToken); err != nil {
		t.Fatal(err)
	}
	if err := d.store.MarkDelegationWorktreeOwned(record.Operation.OperationID, path, ownerToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: "other-session", Label: "other", Agent: protocol.SessionAgentCodex, Directory: path, State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now})

	d.runDelegationOperation(record.Operation.OperationID)
	failed := waitDelegationOperation(t, d, record.Operation.OperationID)
	if failed.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation=%+v", failed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("occupied worktree was removed: %v", err)
	}
	if d.store.Get("other-session") == nil {
		t.Fatal("occupying session was removed")
	}
}
