package daemon

import (
	"path/filepath"
	"testing"
	"time"

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

	d.captureTicketCrashState(sessionID, protocol.StateWaitingInput)

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

	d.captureTicketCrashState(sessionID, protocol.StateWorking)

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
