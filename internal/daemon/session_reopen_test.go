package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// A repository with an `origin` remote it was cloned from, so a branch can be
// local, only on the remote, or gone from both.
func newReopenRepo(t *testing.T) (repo, origin, root string) {
	t.Helper()
	root = t.TempDir()
	origin = filepath.Join(root, "origin.git")
	runGitDaemon(t, root, "init", "--bare", "--initial-branch=main", origin)

	seed := filepath.Join(root, "seed")
	runGitDaemon(t, root, "clone", origin, seed)
	runGitDaemon(t, seed, "commit", "--allow-empty", "-m", "init")
	runGitDaemon(t, seed, "push", "-u", "origin", "main")

	repo = filepath.Join(root, "repo")
	runGitDaemon(t, root, "clone", origin, repo)
	return attngit.CanonicalizePath(repo), origin, root
}

type reopenSession struct {
	ID         string
	Directory  string
	Branch     string
	Repo       string
	Agent      string
	Resume     string
	ClosedBy   string
	Reason     string
	CostCursor string
}

// Registers a session the way the app does, then closes it into the ledger.
func closeReopenSession(t *testing.T, d *Daemon, session reopenSession) {
	t.Helper()
	now := protocol.TimestampNow().String()
	entry := &protocol.Session{
		ID: session.ID, Label: session.ID,
		Agent:     protocol.SessionAgent(session.Agent),
		Directory: session.Directory, WorkspaceID: "workspace-" + session.ID,
		State:      protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	}
	if session.Branch != "" {
		entry.Branch = protocol.Ptr(session.Branch)
	}
	if session.Repo != "" {
		entry.IsWorktree = protocol.Ptr(true)
		entry.MainRepo = protocol.Ptr(session.Repo)
	}
	d.store.Add(entry)
	if session.Resume != "" {
		d.persistResumeSessionID(session.ID, session.Resume)
	}
	if session.CostCursor != "" {
		if err := d.store.SetSessionCostCursor(session.ID, session.CostCursor); err != nil {
			t.Fatalf("set the cost cursor of %s: %v", session.ID, err)
		}
	}
	closedBy := session.ClosedBy
	if closedBy == "" {
		closedBy = store.SessionClosedByUser
	}
	d.closeSession(session.ID, store.SessionClose{By: closedBy, Reason: session.Reason})
	if !d.store.SessionClosed(session.ID) {
		t.Fatalf("session %s did not close into the ledger", session.ID)
	}
}

// The verdict once every branch check it started has landed. Waits on the
// inspection itself, never on a clock.
func decidedReopenVerdict(t *testing.T, d *Daemon, sessionID string) *sessionReopenVerdict {
	t.Helper()
	verdict, found := d.reopenVerdict(sessionID)
	if !found {
		t.Fatalf("no ledger row for %s", sessionID)
	}
	if !verdict.Checking {
		return verdict
	}
	<-d.inspectBranchInBackground(sessionID, verdict.Execution.RepositoryRoot, verdict.Execution.Branch)
	verdict, found = d.reopenVerdict(sessionID)
	if !found {
		t.Fatalf("no ledger row for %s after its branch check", sessionID)
	}
	if verdict.Checking {
		t.Fatalf("%s is still checking after its branch check landed", sessionID)
	}
	return verdict
}

func actionNames(actions []protocol.SessionReopenAction) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		names = append(names, string(action))
	}
	return names
}

func wantReopenVerdict(
	t *testing.T, verdict *sessionReopenVerdict, reopenable bool, actions []protocol.SessionReopenAction,
) {
	t.Helper()
	if verdict.Reopenable != reopenable {
		t.Errorf("reopenable = %v, want %v (reason %q)", verdict.Reopenable, reopenable, verdict.Reason)
	}
	if !slices.Equal(actionNames(verdict.Actions), actionNames(actions)) {
		t.Errorf("actions = %v, want %v", actionNames(verdict.Actions), actionNames(actions))
	}
	if !reopenable && strings.TrimSpace(verdict.Reason) == "" {
		t.Error("a verdict that refuses a reopen carries no reason; an agent cannot act on that")
	}
}

// ---------------------------------------------------------------------------
// The session row

func TestReopenVerdictSendsALiveSessionBackToItsPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "running", t.TempDir())

	verdict := decidedReopenVerdict(t, d, "running")
	if !verdict.Live {
		t.Fatal("a registered session reads as closed")
	}
	wantReopenVerdict(t, verdict, false, nil)
	if !strings.Contains(verdict.Reason, "focus it") {
		t.Errorf("reason = %q, want it to send the caller to the running session", verdict.Reason)
	}
}

func TestReopeningALiveSessionChangesNothing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "running", t.TempDir())

	outcome, err := d.reopenSession("running", "", "")
	if err != nil {
		t.Fatalf("reopenSession(live) error = %v", err)
	}
	if !outcome.AlreadyRunning {
		t.Error("reopening a live session did not report it as already running")
	}
}

func TestReopeningASessionWithNoLedgerRowSaysWhereToLookInstead(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))

	_, err := d.reopenSession("never-ran", "", "")
	if err == nil {
		t.Fatal("reopening an unknown session id succeeded")
	}
	for _, want := range []string{"never-ran", "ledger", "seed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}

// ---------------------------------------------------------------------------
// The host

func TestReopenVerdictSendsARemoteSessionToItsOwnDaemon(t *testing.T) {
	endpoints := []protocol.EndpointInfo{
		{ID: "outpost-7", Name: "big-linux", Status: "connected"},
		{ID: "outpost-8", Name: "sleepy-linux", Status: "disconnected"},
	}
	cases := map[string]struct {
		endpointID string
		wantTail   string
	}{
		"reachable":   {endpointID: "outpost-7", wantTail: "reopen it there"},
		"unreachable": {endpointID: "outpost-8", wantTail: "retry when it is"},
		"forgotten":   {endpointID: "outpost-nobody-configured", wantTail: "retry when it is"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			verdict := &sessionReopenVerdict{
				SessionID: "remote-one",
				Execution: garden.Dispatch{HostKind: garden.HostRemote, EndpointID: tc.endpointID},
			}
			if decideReopenHost(verdict, endpoints) {
				t.Fatal("a remote session went on being decided on this daemon")
			}
			wantReopenVerdict(t, verdict, false, nil)
			if !strings.Contains(verdict.Reason, tc.endpointID) &&
				!strings.Contains(verdict.Reason, "big-linux") &&
				!strings.Contains(verdict.Reason, "sleepy-linux") {
				t.Errorf("reason = %q, want the host named", verdict.Reason)
			}
			if !strings.Contains(verdict.Reason, tc.wantTail) {
				t.Errorf("reason = %q, want it to end with %q", verdict.Reason, tc.wantTail)
			}
		})
	}
}

func TestALocalSessionIsDecidedHere(t *testing.T) {
	verdict := &sessionReopenVerdict{SessionID: "local", Execution: garden.Dispatch{HostKind: garden.HostLocal}}
	if !decideReopenHost(verdict, nil) {
		t.Fatalf("a local session stopped at the host check: %q", verdict.Reason)
	}
}

// ---------------------------------------------------------------------------
// The conversation and the agent

func TestReopenVerdictOffersAFreshStartWhenTheConversationIsGone(t *testing.T) {
	cases := map[string]struct {
		resume  string
		fixture bool
		want    string
	}{
		"resume id unknown":  {resume: "", fixture: false, want: "nothing to resume"},
		"transcript missing": {resume: "conv-vanished", fixture: false, want: "no longer in codex's storage"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
			if tc.fixture {
				writeCodexRolloutFixture(t, tc.resume)
			} else {
				writeCodexRolloutFixture(t, "some-other-conversation")
			}
			directory := t.TempDir()
			closeReopenSession(t, d, reopenSession{
				ID: "no-conversation", Directory: directory, Agent: "codex", Resume: tc.resume,
			})

			verdict := decidedReopenVerdict(t, d, "no-conversation")
			wantReopenVerdict(t, verdict, false,
				[]protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace})
			if !strings.Contains(verdict.Reason, tc.want) {
				t.Errorf("reason = %q, want it to contain %q", verdict.Reason, tc.want)
			}
		})
	}
}

func TestReopenVerdictNamesAnAgentThatIsNotInstalled(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	closeReopenSession(t, d, reopenSession{
		ID: "gone-agent", Directory: t.TempDir(), Agent: "a-harness-nobody-installed", Resume: "conv-1",
	})

	verdict := decidedReopenVerdict(t, d, "gone-agent")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace})
	if !strings.Contains(verdict.Reason, "a-harness-nobody-installed") {
		t.Errorf("reason = %q, want the missing agent named", verdict.Reason)
	}
}

// ---------------------------------------------------------------------------
// The directory

func TestReopenVerdictReopensAPresentDirectory(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-present")
	repo, _, root := newReopenRepo(t)
	worktree := filepath.Join(root, "wt-present")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/present", worktree)

	closeReopenSession(t, d, reopenSession{
		ID: "present", Directory: worktree, Branch: "feat/present", Repo: repo,
		Agent: "codex", Resume: "conv-present",
	})

	verdict := decidedReopenVerdict(t, d, "present")
	wantReopenVerdict(t, verdict, true, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen})
	if verdict.Warning != "" {
		t.Errorf("warning = %q, want none while the directory is on its own branch", verdict.Warning)
	}
}

func TestReopenVerdictWarnsWhenTheDirectorySwitchedBranch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-switched")
	repo, _, root := newReopenRepo(t)
	worktree := filepath.Join(root, "wt-switched")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/switched", worktree)

	closeReopenSession(t, d, reopenSession{
		ID: "switched", Directory: worktree, Branch: "feat/switched", Repo: repo,
		Agent: "codex", Resume: "conv-switched",
	})
	runGitDaemon(t, worktree, "switch", "-c", "feat/somewhere-else")

	verdict := decidedReopenVerdict(t, d, "switched")
	wantReopenVerdict(t, verdict, true, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen})
	if !strings.Contains(verdict.Warning, "feat/somewhere-else") {
		t.Errorf("warning = %q, want the branch the directory is on now", verdict.Warning)
	}
}

func TestReopenVerdictShowsADirectoryItCannotOpen(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-unreadable")
	notADirectory := filepath.Join(t.TempDir(), "checkout")
	if err := os.WriteFile(notADirectory, []byte("this is a file"), 0o644); err != nil {
		t.Fatalf("write the stand-in for an unreadable directory: %v", err)
	}

	closeReopenSession(t, d, reopenSession{
		ID: "unreadable", Directory: notADirectory, Agent: "codex", Resume: "conv-unreadable",
	})

	verdict := decidedReopenVerdict(t, d, "unreadable")
	wantReopenVerdict(t, verdict, false, nil)
	if !strings.Contains(verdict.Reason, notADirectory) {
		t.Errorf("reason = %q, want the directory that cannot be opened", verdict.Reason)
	}
}

func TestReopenVerdictOffersAnotherPlaceWhenThePlainDirectoryIsGone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-plain")
	gone := filepath.Join(t.TempDir(), "deleted")

	closeReopenSession(t, d, reopenSession{
		ID: "plain-gone", Directory: gone, Agent: "codex", Resume: "conv-plain",
	})

	verdict := decidedReopenVerdict(t, d, "plain-gone")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere})
	if !strings.Contains(verdict.Reason, "not a worktree attn can put back") {
		t.Errorf("reason = %q, want it to say the directory was not a worktree", verdict.Reason)
	}
}

func TestReopenVerdictRefusesWhenTheWorktreesRepositoryIsGoneToo(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-norepo")
	root := t.TempDir()

	closeReopenSession(t, d, reopenSession{
		ID: "no-repo", Directory: filepath.Join(root, "wt"), Branch: "feat/x",
		Repo: filepath.Join(root, "repo-that-never-existed"), Agent: "codex", Resume: "conv-norepo",
	})

	verdict := decidedReopenVerdict(t, d, "no-repo")
	wantReopenVerdict(t, verdict, false, nil)
	if !strings.Contains(verdict.Reason, "repository") {
		t.Errorf("reason = %q, want the missing repository named", verdict.Reason)
	}
}

func TestReopenVerdictOffersToPutABranchStillHereBackInPlace(t *testing.T) {
	d, repo, worktree := closedWorktreeWithDeletedDirectory(t, "local-branch", "feat/local", false)
	_ = repo

	verdict := decidedReopenVerdict(t, d, "local-branch")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionRecreateWorktreeAndReopen})
	if verdict.BranchState != branchStateLocal {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateLocal)
	}
	if verdict.RecreatePath != attngit.CanonicalizePath(worktree) {
		t.Errorf("recreate path = %q, want the saved worktree %q", verdict.RecreatePath, worktree)
	}
}

func TestReopenVerdictOffersToFetchABranchOnlyOnTheRemote(t *testing.T) {
	d, repo, _ := closedWorktreeWithDeletedDirectory(t, "remote-branch", "feat/remote", true)
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/remote")

	verdict := decidedReopenVerdict(t, d, "remote-branch")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionFetchRecreateAndReopen})
	if verdict.BranchState != branchStateRemoteOnly {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateRemoteOnly)
	}
	if !strings.Contains(verdict.Reason, "origin") {
		t.Errorf("reason = %q, want the remote carrying the branch named", verdict.Reason)
	}
}

func TestReopenVerdictOffersTheDefaultBranchWhenNothingCarriesTheBranch(t *testing.T) {
	d, repo, _ := closedWorktreeWithDeletedDirectory(t, "gone-branch", "feat/gone", false)
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/gone")

	verdict := decidedReopenVerdict(t, d, "gone-branch")
	wantReopenVerdict(t, verdict, false, []protocol.SessionReopenAction{
		protocol.SessionReopenActionStartFreshDefaultBranch,
		protocol.SessionReopenActionStartFreshElsewhere,
	})
	if verdict.BranchState != branchStateGone {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateGone)
	}
}

func TestReopenVerdictLabelsAGoneBranchThatWasMerged(t *testing.T) {
	d, repo, _ := closedWorktreeWithDeletedDirectory(t, "merged-branch", "feat/merged", true)
	runGitDaemon(t, repo, "push", "origin", "--delete", "feat/merged")
	runGitDaemon(t, repo, "fetch", "--prune", "origin")
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/merged")
	if _, err := d.store.RecordSessionPullRequest(store.SessionPullRequestRecord{
		SessionID: "merged-branch", PRID: "github.com:o/r#7", Repository: "github.com/o/r", Number: 7,
		URL: "https://github.com/o/r/pull/7",
	}, time.Now()); err != nil {
		t.Fatalf("record the session's pull request: %v", err)
	}
	if err := d.store.UpdateSessionPullRequestStatus("github.com:o/r#7", store.SessionPullRequestStatus{
		State: "merged", HeadBranch: "feat/merged",
	}, time.Now()); err != nil {
		t.Fatalf("mark the pull request merged: %v", err)
	}

	verdict := decidedReopenVerdict(t, d, "merged-branch")
	wantReopenVerdict(t, verdict, false, []protocol.SessionReopenAction{
		protocol.SessionReopenActionStartFreshDefaultBranch,
		protocol.SessionReopenActionStartFreshElsewhere,
	})
	if verdict.BranchState != branchStateMerged {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateMerged)
	}
	if !strings.Contains(verdict.Reason, "merged") {
		t.Errorf("reason = %q, want it to say the branch was merged", verdict.Reason)
	}
}

// A closed session whose worktree directory somebody deleted, leaving git's
// registration behind — how this arrives in practice.
func closedWorktreeWithDeletedDirectory(
	t *testing.T, sessionID, branch string, pushed bool,
) (*Daemon, string, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-"+sessionID)
	repo, _, root := newReopenRepo(t)
	worktree := filepath.Join(root, "wt-"+sessionID)
	runGitDaemon(t, repo, "worktree", "add", "-b", branch, worktree)
	if pushed {
		runGitDaemon(t, worktree, "push", "-u", "origin", branch)
	}

	closeReopenSession(t, d, reopenSession{
		ID: sessionID, Directory: worktree, Branch: branch, Repo: repo,
		Agent: "codex", Resume: "conv-" + sessionID,
	})
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("delete the worktree directory: %v", err)
	}
	return d, repo, worktree
}

// ---------------------------------------------------------------------------
// Workspace and pane

func TestReopenVerdictLandsInTheOriginalWorkspaceWhenItIsStillThere(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	writeCodexRolloutFixture(t, "conv-ws")
	directory := t.TempDir()
	client := newWorkspaceProtocolTestClient()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-kept", Title: "Kept", Directory: directory,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "workspace-kept",
		PaneID: protocol.Ptr("pane-in-workspace"), SessionID: "in-workspace", Title: protocol.Ptr("Kept"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane,
		"workspace-kept", "pane-in-workspace", true)

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "in-workspace", Label: "kept", Agent: protocol.SessionAgentCodex,
		Directory: directory, WorkspaceID: "workspace-kept", State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.persistResumeSessionID("in-workspace", "conv-ws")
	d.closeSession("in-workspace", store.SessionClose{By: store.SessionClosedByUser})

	verdict := decidedReopenVerdict(t, d, "in-workspace")
	if verdict.WorkspaceID != "workspace-kept" || verdict.WorkspacePlan != reopenPlaceReuse {
		t.Errorf("workspace = %s (%s), want workspace-kept reused", verdict.WorkspaceID, verdict.WorkspacePlan)
	}
	if verdict.PanePlan != reopenPlaceReuse {
		t.Errorf("pane plan = %s, want the surviving pane reused", verdict.PanePlan)
	}
}

func TestReopenVerdictMakesAWorkspaceNamedAfterTheSessionWhenItsOwnIsGone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-nows")
	closeReopenSession(t, d, reopenSession{
		ID: "no-workspace", Directory: t.TempDir(), Agent: "codex", Resume: "conv-nows",
	})

	verdict := decidedReopenVerdict(t, d, "no-workspace")
	if verdict.WorkspaceID != "workspace-no-workspace" || verdict.WorkspacePlan != reopenPlaceCreate {
		t.Errorf("workspace = %s (%s), want one created and named after the session",
			verdict.WorkspaceID, verdict.WorkspacePlan)
	}
	if verdict.PanePlan != reopenPlaceAdd {
		t.Errorf("pane plan = %s, want a pane added", verdict.PanePlan)
	}
}
