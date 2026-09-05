package daemon

import (
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func boundTicketID(t *testing.T, d *Daemon, sessionID string) string {
	t.Helper()
	ticket, err := d.store.ActiveTicketForSession(sessionID)
	if err != nil {
		t.Fatalf("ActiveTicketForSession: %v", err)
	}
	if ticket == nil {
		t.Fatal("session has no bound ticket")
	}
	return ticket.ID
}

func TestDropSessionRecordCrashesBoundTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.UpdateState(sessionID, protocol.StateWorking)

	d.closeSession(sessionID, store.SessionClose{By: store.SessionClosedByUser})

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status = %q, want crashed", ticket.Status)
	}
	if ticket.ClosedAt == nil {
		t.Fatal("crashed ticket has no closed_at")
	}

	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	var crash *store.TicketEvent
	for i := range events {
		if events[i].TicketID == ticketID && events[i].ToStatus == store.TicketStatusCrashed {
			crash = &events[i]
		}
	}
	if crash == nil {
		t.Fatalf("no crashed event for ticket %q", ticketID)
	}
	if crash.Author != store.TicketAuthorAttn {
		t.Fatalf("crash author = %q, want attn", crash.Author)
	}
}

func TestCaptureTicketCrashStateNoopOnCleanRest(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWaitingInput)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status = %q, want unchanged working", ticket.Status)
	}
}

func TestCaptureTicketCrashStateNoopAfterTerminalReport(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	if _, err := d.store.SetTicketStatus(ticketID, store.TicketStatusDone, sessionID, "", time.Now()); err != nil {
		t.Fatalf("SetTicketStatus done: %v", err)
	}

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWorking)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusDone {
		t.Fatalf("status = %q, want done (report wins over crash)", ticket.Status)
	}
}

func TestIsMidFlightCrashState(t *testing.T) {
	crash := []string{protocol.StateLaunching, protocol.StateWorking, protocol.StatePendingApproval}
	clean := []string{protocol.StateIdle, protocol.StateWaitingInput, protocol.StateUnknown, ""}
	for _, s := range crash {
		if !isMidFlightCrashState(s) {
			t.Errorf("isMidFlightCrashState(%q) = false, want true", s)
		}
	}
	for _, s := range clean {
		if isMidFlightCrashState(s) {
			t.Errorf("isMidFlightCrashState(%q) = true, want false", s)
		}
	}
}

func TestUnregisterSessionIntentionalCloseDoesNotCrashTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	callSetTicketStatus(t, d, sessionID, "ready_for_review", "PR is up")
	d.store.UpdateState(sessionID, protocol.StateWorking)
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)

	d.unregisterSession(sessionID, syscall.SIGTERM)
	waitReconcileDone(t, done)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status == store.TicketStatusCrashed {
		t.Fatal("intentional close crash-stamped the ticket")
	}
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("status = %q, want in_review (left where the agent reported it)", ticket.Status)
	}
	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1 (verdict still posts on a clean close)", len(comments))
	}
	if !strings.Contains(comments[0], "the session was closed (user close or teardown)") {
		t.Fatalf("comment framed as %q, want the clean-close framing", comments[0])
	}
}

func TestUnregisterSessionKillFailureStillDoesNotCrashTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.ptyBackend = &fakeSpawnBackend{sessionIDs: []string{sessionID}, killErr: errors.New("kill unavailable")}

	d.unregisterSession(sessionID, syscall.SIGTERM)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status == store.TicketStatusCrashed {
		t.Fatal("legacy intentional close with a failed kill crash-stamped the ticket")
	}
}

func TestReapAfterRestartHonorsIntentionalClose(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.UpdateState(sessionID, protocol.StateWorking)

	d.terminateSession(sessionID, syscall.SIGTERM)

	d2 := NewForTesting(filepath.Join(t.TempDir(), "restart.sock"))
	d2.store = d.store
	d2.removeReapedSession(sessionID)

	ticket, err := d2.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status == store.TicketStatusCrashed {
		t.Fatal("restart reap crash-stamped an intentionally closed session's ticket")
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status = %q, want unchanged working", ticket.Status)
	}
}

func TestClearedIntentionalCloseMarkReArmsCrashDetection(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.MarkSessionIntentionalClose(sessionID, time.Now())
	d.store.ClearSessionIntentionalClose(sessionID)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWorking)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status = %q, want crashed (spontaneous death after mark cleared)", ticket.Status)
	}
}
