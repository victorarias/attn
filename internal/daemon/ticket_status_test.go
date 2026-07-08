package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// delegateBoundSession runs a real delegation so the returned session has a
// ticket bound to it (assignee = session, status = working), mirroring the
// production path the agent's forward channel reports against.
func delegateBoundSession(t *testing.T, d *Daemon) string {
	t.Helper()
	backend := &fakeSpawnBackend{}
	_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, chiefSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: chiefSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	return result.SessionID
}

func callSetTicketStatus(t *testing.T, d *Daemon, sessionID, workState, comment string) protocol.Response {
	t.Helper()
	return callSetTicketStatusByID(t, d, sessionID, workState, comment, "")
}

// callSetTicketStatusByID is callSetTicketStatus plus an optional ticket id, for
// the by-id form that bypasses session-bound resolution.
func callSetTicketStatusByID(t *testing.T, d *Daemon, sessionID, workState, comment, ticketID string) protocol.Response {
	t.Helper()
	msg := &protocol.SetTicketStatusMessage{
		Cmd:             protocol.CmdSetTicketStatus,
		SourceSessionID: sessionID,
		WorkState:       protocol.DispatchWorkState(workState),
	}
	if comment != "" {
		msg.Comment = protocol.Ptr(comment)
	}
	if ticketID != "" {
		msg.TicketID = protocol.Ptr(ticketID)
	}
	server, clientConn := net.Pipe()
	go func() {
		d.handleSetTicketStatus(server, msg)
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode set-ticket-status response: %v", err)
	}
	_ = clientConn.Close()
	return resp
}

// The agent reporting ready-for-review moves its bound ticket into the In Review
// column, echoes the resolved id and status, and records the change authored by
// the agent's own session.
func TestSetTicketStatusMovesBoundTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)

	resp := callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateReadyForReview), "ready for a look")
	if !resp.Ok || resp.TicketStatusResult == nil {
		t.Fatalf("response = %+v, want ok with ticket status result", resp)
	}
	if resp.TicketStatusResult.Status != protocol.TicketStatusInReview {
		t.Fatalf("result status = %q, want in_review", resp.TicketStatusResult.Status)
	}

	ticket, err := d.store.GetTicket(resp.TicketStatusResult.TicketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("stored status = %q, want in_review", ticket.Status)
	}

	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	var change *store.TicketEvent
	for i := range events {
		if events[i].TicketID == ticket.ID && events[i].Kind == store.TicketEventStatusChanged {
			change = &events[i]
		}
	}
	if change == nil {
		t.Fatalf("no status-changed event for ticket %q", ticket.ID)
	}
	if change.Author != sessionID {
		t.Fatalf("status-changed author = %q, want agent session %q", change.Author, sessionID)
	}
	if change.ToStatus != store.TicketStatusInReview {
		t.Fatalf("status-changed to = %q, want in_review", change.ToStatus)
	}
	if change.Comment != "ready for a look" {
		t.Fatalf("status-changed comment = %q, want the supplied note", change.Comment)
	}
}

// A completed report closes the ticket (terminal Done), after which the session
// has no active ticket and a further report is rejected.
func TestSetTicketStatusCompletedClosesTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)

	resp := callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateCompleted), "")
	if !resp.Ok || resp.TicketStatusResult == nil || resp.TicketStatusResult.Status != protocol.TicketStatusDone {
		t.Fatalf("response = %+v, want ok done", resp)
	}
	ticket, err := d.store.GetTicket(resp.TicketStatusResult.TicketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if !ticket.Status.IsTerminal() || ticket.ClosedAt == nil {
		t.Fatalf("ticket = %+v, want terminal with closed_at", ticket)
	}

	again := callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateInProgress), "")
	if again.Ok || again.Error == nil || !strings.Contains(*again.Error, "no active ticket") {
		t.Fatalf("second report = %+v, want no-active-ticket error", again)
	}
}

func TestSetTicketStatusErrors(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)

	cases := []struct {
		name      string
		session   string
		workState string
		wantErr   string
	}{
		{"empty session", "", string(protocol.DispatchWorkStateInProgress), "source_session_id is required"},
		{"unknown work state", sessionID, "marshmallow", "unknown work state"},
		{"no bound ticket", "ghost-session", string(protocol.DispatchWorkStateInProgress), "no active ticket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := callSetTicketStatus(t, d, tc.session, tc.workState, "")
			if resp.Ok || resp.Error == nil {
				t.Fatalf("response = %+v, want error", resp)
			}
			if !strings.Contains(*resp.Error, tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", *resp.Error, tc.wantErr)
			}
		})
	}
}

// The work-state -> ticket-column mapping is the contract the agent reports
// against; lock every reachable state and reject the unreachable ones.
func TestTicketStatusFromWorkState(t *testing.T) {
	want := map[protocol.DispatchWorkState]store.TicketStatus{
		protocol.DispatchWorkStateInProgress:     store.TicketStatusWorking,
		protocol.DispatchWorkStateNeedsInput:     store.TicketStatusBlocked,
		protocol.DispatchWorkStateReadyForReview: store.TicketStatusInReview,
		protocol.DispatchWorkStateCompleted:      store.TicketStatusDone,
		protocol.DispatchWorkStateFailed:         store.TicketStatusFailed,
	}
	for ws, status := range want {
		got, ok := ticketStatusFromWorkState(ws)
		if !ok || got != status {
			t.Fatalf("ticketStatusFromWorkState(%q) = (%q, %v), want (%q, true)", ws, got, ok, status)
		}
	}
	if _, ok := ticketStatusFromWorkState(protocol.DispatchWorkState("nonsense")); ok {
		t.Fatal("ticketStatusFromWorkState accepted an unknown work state")
	}
}

// The by-id form is deliberately permissive: a session with no ticket bound to
// it at all can still move someone else's ticket by naming its id. This is the
// bug the by-id form exists to fix — on the pre-change handler this call would
// error "no active ticket bound to this session" because the session lookup
// happened unconditionally.
func TestSetTicketStatusByIDMovesUnboundSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:     "store-migration",
		Title:  "Migrate the store",
		Status: store.TicketStatusTodo,
	}, "someone-else", now); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	observerSessionID := "observer-session"
	resp := callSetTicketStatusByID(t, d, observerSessionID, string(protocol.DispatchWorkStateReadyForReview), "moving it along", "store-migration")
	if !resp.Ok || resp.TicketStatusResult == nil {
		t.Fatalf("response = %+v, want ok with ticket status result", resp)
	}
	if resp.TicketStatusResult.TicketID != "store-migration" {
		t.Fatalf("result ticket id = %q, want store-migration", resp.TicketStatusResult.TicketID)
	}
	if resp.TicketStatusResult.Status != protocol.TicketStatusInReview {
		t.Fatalf("result status = %q, want in_review", resp.TicketStatusResult.Status)
	}

	ticket, err := d.store.GetTicket("store-migration")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("stored status = %q, want in_review", ticket.Status)
	}

	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	var change *store.TicketEvent
	for i := range events {
		if events[i].TicketID == "store-migration" && events[i].Kind == store.TicketEventStatusChanged {
			change = &events[i]
		}
	}
	if change == nil {
		t.Fatalf("no status-changed event for ticket store-migration")
	}
	if change.Author != observerSessionID {
		t.Fatalf("status-changed author = %q, want the acting session %q", change.Author, observerSessionID)
	}
}

// Naming an id that doesn't exist surfaces the store's not-found error rather
// than falling back to session resolution or panicking.
func TestSetTicketStatusByIDUnknownTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	resp := callSetTicketStatusByID(t, d, "observer-session", string(protocol.DispatchWorkStateInProgress), "", "does-not-exist")
	if resp.Ok || resp.Error == nil {
		t.Fatalf("response = %+v, want error", resp)
	}
	if !strings.Contains(*resp.Error, "not found") && !strings.Contains(*resp.Error, "does-not-exist") {
		t.Fatalf("error = %q, want it to mention the missing ticket", *resp.Error)
	}
}
