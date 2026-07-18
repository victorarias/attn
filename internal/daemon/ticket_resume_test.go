package daemon

import (
	"encoding/json"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
)

// resumeSpawnForSession returns the spawn opts recorded for sessionID at or after
// index `since`, so a resume respawn can be inspected without matching the original
// delegation spawn of the same id.
func resumeSpawnForSession(t *testing.T, backend *fakeSpawnBackend, sessionID string, since int) ptybackend.SpawnOptions {
	t.Helper()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for i := since; i < len(backend.spawnOpts); i++ {
		if backend.spawnOpts[i].ID == sessionID {
			return backend.spawnOpts[i]
		}
	}
	t.Fatalf("no spawn recorded for %s at/after index %d (spawns=%d)", sessionID, since, len(backend.spawnOpts))
	return ptybackend.SpawnOptions{}
}

func spawnCount(backend *fakeSpawnBackend) int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.spawnOpts)
}

// delegateBoundTicket sets up a chief and delegates a leaf, returning the leaf
// session id and the ticket bound to it. The delegated ticket carries the leaf's
// cwd + agent, which is exactly what Resume reloads from.
func delegateBoundTicket(t *testing.T, d *Daemon, backend *fakeSpawnBackend, agent string) (string, *store.Ticket) {
	t.Helper()
	_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, chiefSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: chiefSessionID,
		Brief:           "Investigate the tracked task.",
		Agent:           protocol.Ptr(agent),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	ticket, err := d.store.ActiveTicketForSession(result.SessionID)
	if err != nil || ticket == nil {
		t.Fatalf("ActiveTicketForSession: ticket=%+v err=%v", ticket, err)
	}
	return result.SessionID, ticket
}

func TestTicketResumeRespawnsClosedBoundSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	leafID, ticket := delegateBoundTicket(t, d, backend, "codex")

	// Close the leaf (its session row — and its own resume_session_id — are gone),
	// then seed the durable resume mirror on the ticket. codex is resumable by
	// default (no transcript probe), so Resume should adopt the mirrored id.
	d.unregisterSession(leafID, syscall.SIGTERM)
	if d.store.Get(leafID) != nil {
		t.Fatalf("session %s still registered after close", leafID)
	}
	d.persistResumeSessionID(leafID, "codex-conv-xyz")

	before, err := d.store.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket before: %v", err)
	}
	eventsBefore := len(before.Activity)
	since := spawnCount(backend)

	outcome, err := d.resumeTicket(ticket.ID)
	if err != nil {
		t.Fatalf("resumeTicket: %v", err)
	}
	if outcome.SessionID != leafID || outcome.AlreadyRunning {
		t.Fatalf("outcome = %+v, want session=%s already_running=false", outcome, leafID)
	}

	// The session is re-registered under the SAME id (assignee == session identity)
	// in the ticket's cwd, so the resumed agent's ticket reports stay on this ticket.
	// The stored directory is canonicalized (validateDelegationDirectory), so compare
	// against the same canonical form.
	wantDir, err := validateDelegationDirectory(ticket.Cwd)
	if err != nil {
		t.Fatalf("canonicalize ticket cwd: %v", err)
	}
	session := d.store.Get(leafID)
	if session == nil || session.Directory != wantDir {
		t.Fatalf("resumed session = %+v, want dir=%s", session, wantDir)
	}
	workspaceID := "workspace-" + leafID
	if outcome.WorkspaceID != workspaceID || d.store.GetWorkspace(workspaceID) == nil {
		t.Fatalf("resume workspace = %q, GetWorkspace=%v", outcome.WorkspaceID, d.store.GetWorkspace(workspaceID))
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != leafID {
		t.Fatalf("resume layout = %+v, want one pane for %s", layout, leafID)
	}

	// The spawn carries the mirrored resume id (precise resume, not the picker).
	spawn := resumeSpawnForSession(t, backend, leafID, since)
	if spawn.ResumeSessionID != "codex-conv-xyz" {
		t.Fatalf("resume spawn ResumeSessionID = %q, want codex-conv-xyz", spawn.ResumeSessionID)
	}

	// Spine: Resume authors NOTHING on the ticket (no status, no event).
	after, err := d.store.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket after: %v", err)
	}
	if len(after.Activity) != eventsBefore {
		t.Fatalf("ticket activity = %d, want unchanged %d (resume must not author events)", len(after.Activity), eventsBefore)
	}
	if after.Status != before.Status {
		t.Fatalf("ticket status changed to %s, want unchanged %s", after.Status, before.Status)
	}
}

func TestTicketResumeAlreadyRunningFocusesInsteadOfSpawning(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	leafID, ticket := delegateBoundTicket(t, d, backend, "codex")

	// The bound session is still registered — Resume must focus it, not spawn a
	// duplicate.
	before := spawnCount(backend)
	outcome, err := d.resumeTicket(ticket.ID)
	if err != nil {
		t.Fatalf("resumeTicket: %v", err)
	}
	if !outcome.AlreadyRunning || outcome.SessionID != leafID {
		t.Fatalf("outcome = %+v, want already_running session=%s", outcome, leafID)
	}
	if got := spawnCount(backend); got != before {
		t.Fatalf("spawn count = %d, want unchanged %d (already-running resume must not spawn)", got, before)
	}
}

func TestTicketResumeFallsBackToPickerWhenTranscriptGone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	leafID, ticket := delegateBoundTicket(t, d, backend, "claude")

	d.unregisterSession(leafID, syscall.SIGTERM)
	d.persistResumeSessionID(leafID, leafID)
	// Point ATTN_TOOL_HOME at an empty home so claude's transcript lookup finds
	// nothing for the mirrored id: it is not resumable, so the spawn must fall
	// back to the cwd-scoped picker instead of `claude -r <dead-id>`.
	t.Setenv(toolhome.EnvVar, t.TempDir())
	since := spawnCount(backend)

	outcome, err := d.resumeTicket(ticket.ID)
	if err != nil {
		t.Fatalf("resumeTicket: %v", err)
	}
	if outcome.AlreadyRunning {
		t.Fatalf("outcome = %+v, want a fresh spawn", outcome)
	}
	spawn := resumeSpawnForSession(t, backend, leafID, since)
	if spawn.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want empty (transcript gone → picker)", spawn.ResumeSessionID)
	}
	if !spawn.ResumePicker {
		t.Fatal("ResumePicker = false, want true (fallback to cwd-scoped picker)")
	}
}

func TestTicketResumeValidation(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		name   string
		ticket *store.Ticket // nil → don't create; resume an unknown id
	}{
		{name: "unknown ticket", ticket: nil},
		{name: "no bound agent session", ticket: &store.Ticket{ID: "no-agent", Title: "t", Status: store.TicketStatusInReview, Assignee: "ghost", Cwd: "", LastAgentID: ""}},
		{name: "missing directory", ticket: &store.Ticket{ID: "gone-dir", Title: "t", Status: store.TicketStatusInReview, Assignee: "ghost", Cwd: filepath.Join(cwd, "does-not-exist"), LastAgentID: "codex"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			d.ptyBackend = &fakeSpawnBackend{}
			ticketID := "missing"
			if tc.ticket != nil {
				if _, err := d.store.CreateTicket(*tc.ticket, "chief", time.Now()); err != nil {
					t.Fatalf("CreateTicket: %v", err)
				}
				ticketID = tc.ticket.ID
			}
			if _, err := d.resumeTicket(ticketID); err == nil {
				t.Fatal("resumeTicket succeeded, want validation error")
			}
			// No side effects: the assignee's resume workspace must not exist.
			if tc.ticket != nil {
				if ws := d.store.GetWorkspace("workspace-" + tc.ticket.Assignee); ws != nil {
					t.Fatalf("resume left a phantom workspace: %+v", ws)
				}
			}
		})
	}
}

func TestTicketResumeRollsBackPaneWhenSpawnFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:          "spawn-fails",
		Title:       "Resume me",
		Status:      store.TicketStatusInReview,
		Assignee:    "ghost-session",
		Cwd:         t.TempDir(),
		LastAgentID: "codex",
	}, "chief", time.Now()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	d.ptyBackend = &failingSpawnBackend{err: syscall.EPERM}

	if _, err := d.resumeTicket("spawn-fails"); err == nil {
		t.Fatal("resumeTicket succeeded, want spawn failure")
	}
	// The created workspace and its pane are rolled back — no phantom left behind.
	if ws := d.store.GetWorkspace("workspace-ghost-session"); ws != nil {
		t.Fatalf("workspace survived a failed resume: %+v", ws)
	}
}

func TestTicketResumeMintsFreshSessionWhenAssigneeIsYou(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:          "human-owned",
		Title:       "Resume me",
		Status:      store.TicketStatusInReview,
		Assignee:    "you",
		Cwd:         t.TempDir(),
		LastAgentID: "codex",
	}, "chief", time.Now()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	outcome, err := d.resumeTicket("human-owned")
	if err != nil {
		t.Fatalf("resumeTicket: %v", err)
	}
	if outcome.SessionID == "" || outcome.SessionID == "you" || outcome.AlreadyRunning {
		t.Fatalf("outcome = %+v, want a minted fresh session id", outcome)
	}
	if d.store.Get(outcome.SessionID) == nil {
		t.Fatalf("minted session %s not registered", outcome.SessionID)
	}
	if outcome.WorkspaceID != "workspace-"+outcome.SessionID {
		t.Fatalf("workspace = %q, want workspace-%s", outcome.WorkspaceID, outcome.SessionID)
	}
}

func TestHandleTicketResumeReplyEnvelope(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	leafID, ticket := delegateBoundTicket(t, d, backend, "codex")
	d.unregisterSession(leafID, syscall.SIGTERM)
	d.persistResumeSessionID(leafID, "codex-conv-xyz")

	client := newInternalWSClient()
	d.handleTicketResume(client, &protocol.TicketResumeMessage{
		Cmd:       protocol.CmdTicketResume,
		RequestID: protocol.Ptr("req-1"),
		TicketID:  ticket.ID,
	})
	msg := <-client.send
	var reply protocol.TicketResumeResultMessage
	if err := json.Unmarshal(msg.payload, &reply); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if reply.Event != protocol.EventTicketResumeResult || reply.RequestID != "req-1" {
		t.Fatalf("reply envelope = %+v", reply)
	}
	if !reply.Success || protocol.Deref(reply.SessionID) != leafID {
		t.Fatalf("reply = %+v, want success session=%s", reply, leafID)
	}
}
