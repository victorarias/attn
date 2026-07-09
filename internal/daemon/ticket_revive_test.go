package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

// respawnDelegatedSession re-spawns a delegated session under its existing id —
// the frontend path for reloading a dead pane (and the tail of a ticket Resume).
func respawnDelegatedSession(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("session %s missing before respawn", sessionID)
	}
	client := newWorkspaceProtocolTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         session.Directory,
		WorkspaceID: session.WorkspaceID,
		Agent:       string(session.Agent),
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)
}

// The observed bug (2026-07-08): a delegated session crashes mid-flight — its
// ticket is stamped Crashed — and the user then reloads the dead pane. The
// session comes back and works, but the ticket used to sit in Crashed until
// moved by hand. Reviving the owning session must move the ticket back to
// Working automatically, authored by attn like the crash stamp itself.
func TestRespawnOfCrashedSessionRevivesTicketToWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	// Spontaneous mid-flight death: the ticket is crash-stamped and terminal.
	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after crash: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after crash = %q, want crashed", ticket.Status)
	}

	// The user reloads the dead pane: same id, frontend-driven respawn.
	respawnDelegatedSession(t, d, sessionID)

	ticket, err = d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after respawn: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status after respawn = %q, want working", ticket.Status)
	}
	if ticket.ClosedAt != nil {
		t.Fatal("revived ticket still carries closed_at")
	}

	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	var revive *store.TicketEvent
	for i := range events {
		if events[i].TicketID == ticketID &&
			events[i].FromStatus == store.TicketStatusCrashed &&
			events[i].ToStatus == store.TicketStatusWorking {
			revive = &events[i]
		}
	}
	if revive == nil {
		t.Fatalf("no crashed->working event for ticket %q", ticketID)
	}
	if revive.Author != store.TicketAuthorAttn {
		t.Fatalf("revive author = %q, want attn", revive.Author)
	}

	// Crash detection is re-armed for the revived run: a later genuine
	// mid-flight death stamps Crashed again.
	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
	ticket, err = d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after second crash: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after second crash = %q, want crashed (detection re-armed)", ticket.Status)
	}
}

// Revival flips ONLY the Crashed column. A respawn under a ticket the agent
// left in another column (here In Review) must not move it.
func TestRespawnLeavesNonCrashedTicketAlone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateReadyForReview), "PR is up")

	// A clean exit (no crash stamp) followed by a reload of the pane.
	d.store.UpdateState(sessionID, protocol.StateWaitingInput)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 0})
	respawnDelegatedSession(t, d, sessionID)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("status = %q, want in_review (revival must not touch other columns)", ticket.Status)
	}
}

// Daemon-restart safety: startup recovery adopting a still-live worker for a
// crash-stamped ticket's session (a reap on an earlier boot stamped it, the
// worker survived) moves the ticket back to Working — the same durable-vs-
// in-memory rigor as the intentional-close mark it clears at the same spot.
func TestRecoveryAdoptRevivesCrashedTicketToWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	session := d.store.Get(sessionID)

	// Crash-stamp via the seam, as a reap would.
	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWorking)
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after crash: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after crash = %q, want crashed", ticket.Status)
	}

	// Restart-time recovery finds the worker alive and adopts the session.
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{sessionID},
		info: map[string]ptybackend.SessionInfo{
			sessionID: {
				SessionID: sessionID,
				Agent:     string(session.Agent),
				CWD:       session.Directory,
				Running:   true,
				State:     protocol.StateWorking,
			},
		},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	ticket, err = d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after adopt: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status after adopt = %q, want working", ticket.Status)
	}
}
