package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
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

// A user reload (kill_session reload:true + respawn of the same id) is a
// lifecycle transition, not a death: exactly the reload-killed exit skips the
// crash/reconcile seam, and a later real exit of the respawned worker is judged
// normally again (the mark is one-shot).
func TestHandlePTYExitReloadKillSkipsTicketSeam(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	d.store.UpdateState(sessionID, protocol.StateWorking)

	d.markReloadKill(sessionID)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 0})

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status after reload exit = %q, want unchanged working", ticket.Status)
	}

	// The respawned worker dying for real must still crash the ticket.
	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})

	ticket, err = d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after real exit = %q, want crashed", ticket.Status)
	}
}

// An expired reload mark must not swallow a real crash: consume reports false
// past the TTL, and the mark is cleared either way.
func TestConsumeReloadKillExpiresAndIsOneShot(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	d.markReloadKill("fresh")
	if !d.consumeReloadKill("fresh") {
		t.Fatal("fresh mark not consumable")
	}
	if d.consumeReloadKill("fresh") {
		t.Fatal("mark consumable twice, want one-shot")
	}

	d.markReloadKill("stale")
	d.reloadingMu.Lock()
	d.reloadKills["stale"] = time.Now().Add(-reloadKillMarkTTL - time.Second)
	d.reloadingMu.Unlock()
	if d.consumeReloadKill("stale") {
		t.Fatal("expired mark consumed as valid")
	}
}

// The reload flag survives the wire: a kill_session JSON payload with
// reload:true decodes into KillSessionMessage.Reload.
func TestKillSessionMessageDecodesReloadFlag(t *testing.T) {
	_, msg, err := protocol.ParseMessage([]byte(`{"cmd":"kill_session","id":"s1","reload":true}`))
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	kill, ok := msg.(*protocol.KillSessionMessage)
	if !ok {
		t.Fatalf("parsed type = %T, want *KillSessionMessage", msg)
	}
	if !protocol.Deref(kill.Reload) {
		t.Fatal("reload flag lost in decode")
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
