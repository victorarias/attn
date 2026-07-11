package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func callTicketTake(t *testing.T, d *Daemon, sessionID, ticketID string, confirm bool) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	msg := &protocol.TicketTakeMessage{
		Cmd:             protocol.CmdTicketTake,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
	}
	if confirm {
		msg.Confirm = protocol.Ptr(true)
	}
	d.handleTicketTake(conn, msg)
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-take response: %v", err)
	}
	return resp
}

// Taking over an actively-worked ticket: x cannot take z's ticket without
// --confirm (the steal guard), takes it with --confirm, and the displaced
// assignee z — a participant because it reported status while working — is nudged
// about the attach. The taker's first inbox then delivers the ticket's history
// (take does not advance the cursor). codex agents are used so the doorbell is
// observable; handleTicketTake notifies synchronously, so the assertion is
// deterministic.
func TestTicketTakeOverNotifiesPreviousAssignee(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopTicketBackstops)
	t.Cleanup(d.stopNudgeCountdowns)
	_, agents, inputs := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1] // z owns ticket Y; x owns its own ticket
	ticketY := boundTicketID(t, d, z)

	// z reports progress, authoring a status event on Y. That makes z a participant
	// that survives losing the assignee slot — the realistic "actively working agent
	// gets taken over" case (a never-reporting assignee would not be notified, by
	// design).
	if resp := callSetTicketStatus(t, d, z, string(protocol.DispatchWorkStateInProgress), "on it"); !resp.Ok {
		t.Fatalf("z status report failed: %+v", resp)
	}
	for _, id := range agents {
		d.store.UpdateState(id, protocol.StateIdle)
	}

	// x cannot take Y without --confirm — it is assigned to z.
	if resp := callTicketTake(t, d, x, ticketY, false); resp.Ok {
		t.Fatalf("take of an assigned ticket without --confirm returned ok: %+v", resp)
	}
	if tk, _ := d.store.GetTicket(ticketY); tk == nil || tk.Assignee != z {
		t.Fatalf("ticket reassigned despite refused take: %+v", tk)
	}

	// x takes Y over with --confirm.
	resp := callTicketTake(t, d, x, ticketY, true)
	if !resp.Ok || resp.TicketTakeResult == nil ||
		resp.TicketTakeResult.TicketID != ticketY || resp.TicketTakeResult.PreviousAssignee != z {
		t.Fatalf("confirmed take response = %+v, want ok echoing %s and previous=%s", resp, ticketY, z)
	}
	if tk, _ := d.store.GetTicket(ticketY); tk == nil || tk.Assignee != x {
		t.Fatalf("ticket not reassigned to taker: %+v", tk)
	}

	// The displaced assignee z is nudged about the attach.
	fireNudgeNow(t, d, z) // the takeover armed z's countdown
	if !wasNudged(inputs(z)) {
		t.Fatal("previous assignee was not nudged about the takeover")
	}
	// Take delivers history: x's first inbox carries Y's activity (cursor not advanced).
	if !inboxHasTicket(callTicketInbox(t, d, x), ticketY) {
		t.Fatal("taker's inbox did not deliver the taken ticket's history")
	}
}

// Taking an unassigned ticket needs no --confirm and claims it; re-taking one
// already owned is a harmless no-op; taking an unknown ticket is a clear error.
func TestTicketTakeUnassignedSelfAndUnknown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agents, _ := delegateMany(t, d, "codex", "Task X")
	x := agents[0]

	// An unbound backlog ticket (no assignee), authored by the chief.
	if _, err := d.store.CreateTicket(store.Ticket{ID: "backlog-1", Title: "spec"}, "chief", time.Now()); err != nil {
		t.Fatalf("seed backlog ticket: %v", err)
	}

	// Claiming an unassigned ticket: no --confirm, empty previous assignee.
	resp := callTicketTake(t, d, x, "backlog-1", false)
	if !resp.Ok || resp.TicketTakeResult == nil || resp.TicketTakeResult.PreviousAssignee != "" {
		t.Fatalf("take of unassigned ticket = %+v, want ok with empty previous assignee", resp)
	}
	if tk, _ := d.store.GetTicket("backlog-1"); tk == nil || tk.Assignee != x {
		t.Fatalf("unassigned ticket not claimed by taker: %+v", tk)
	}

	// Re-taking a ticket already owned is a no-op success (previous = self).
	if resp := callTicketTake(t, d, x, "backlog-1", false); !resp.Ok ||
		resp.TicketTakeResult == nil || resp.TicketTakeResult.PreviousAssignee != x {
		t.Fatalf("self-take = %+v, want ok with previous=%s", resp, x)
	}
	if tk, _ := d.store.GetTicket("backlog-1"); tk == nil || tk.Assignee != x {
		t.Fatalf("self-take changed the assignee: %+v", tk)
	}

	// Taking an unknown ticket is an error even with --confirm.
	if resp := callTicketTake(t, d, x, "no-such-ticket", true); resp.Ok {
		t.Fatalf("take of unknown ticket returned ok: %+v", resp)
	}
}
