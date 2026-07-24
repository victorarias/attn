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

// A delegated session whose process ends while still working — and whose teardown
// runs through dropSessionRecord — leaves its bound ticket in the attn-authored
// Crashed column, terminal and closed, so the board never shows a stale Working.
func TestDropSessionRecordCrashesBoundTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.UpdateState(sessionID, protocol.StateWorking)

	d.dropSessionRecord(sessionID)

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

// A session that ends at a clean rest leaves the ticket exactly where the agent
// last reported it — attn never overwrites a clean stop with Crashed.
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

// Once the agent has reported a terminal outcome the ticket is terminal, so a
// crash capture finds no active ticket and is a no-op — the report wins.
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

// The bug this guards (fix/close-not-crash): Victor intentionally closes a
// delegated session — the common case: the agent finished, reported In Review,
// and he's done with the pane. The close route (unregisterSession →
// terminateSession + dropSessionRecord) runs the ticket seam with a mid-flight
// last runtime state whenever the agent happened to look busy, and the seam
// used to crash-stamp on that state alone. An intentional close must leave the
// ticket where the agent reported it and frame the reconcile verdict as a
// clean close, not a crash.
func TestUnregisterSessionIntentionalCloseDoesNotCrashTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	callSetTicketStatus(t, d, sessionID, "ready_for_review", "PR is up")
	// The runtime still reads mid-flight at close time (busy pane, stale state).
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

// The in-memory forced-stop mark has a 30s TTL and dies with the daemon, but
// the seam can run long after both: the startup reap (removeReapedSession →
// dropSessionRecord) re-runs it after a restart, with the session row's
// persisted mid-flight state. The durable mark terminateSession writes onto
// the session row is what keeps the close a close there — this test runs the
// seam on a SECOND daemon sharing only the store, so the in-memory mark cannot
// be the reason it passes.
func TestReapAfterRestartHonorsIntentionalClose(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.UpdateState(sessionID, protocol.StateWorking)

	// The close began: terminateSession marked the stop (memory + durable) and
	// killed the worker — then the daemon died before dropSessionRecord ran.
	d.terminateSession(sessionID, syscall.SIGTERM)

	// Restarted daemon: same persisted store, fresh (empty) forced-stop map.
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

// Clearing the durable mark (recovery adopting the session as live) fully
// re-arms crash detection: a later genuine mid-flight death is stamped again.
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
