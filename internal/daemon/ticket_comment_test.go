package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// syncConn is a net.Conn whose writes append to an in-memory buffer and never
// block. Running a handler against it executes the WHOLE handler synchronously in
// the caller's goroutine — including the notifyTicketObservers call that, in
// production, follows the response write. A net.Pipe + goroutine would let the test
// inspect doorbell side effects before that notify ran (the response decode unblocks
// the moment Encode finishes, racing the notify). With syncConn the nudge is
// delivered before the helper returns, so the assertion is deterministic.
type syncConn struct{ buf bytes.Buffer }

func (c *syncConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *syncConn) Write(p []byte) (int, error)      { return c.buf.Write(p) }
func (c *syncConn) Close() error                     { return nil }
func (c *syncConn) LocalAddr() net.Addr              { return nil }
func (c *syncConn) RemoteAddr() net.Addr             { return nil }
func (c *syncConn) SetDeadline(time.Time) error      { return nil }
func (c *syncConn) SetReadDeadline(time.Time) error  { return nil }
func (c *syncConn) SetWriteDeadline(time.Time) error { return nil }

// callTicketComment drives the agent comment handler synchronously, mirroring the
// unix-socket request the CLI makes, and returns the decoded response so a test can
// assert both the success echo and the error path.
func callTicketComment(t *testing.T, d *Daemon, sessionID, ticketID, comment string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketComment(conn, &protocol.TicketCommentMessage{
		Cmd:             protocol.CmdTicketComment,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
		Comment:         comment,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-comment response: %v", err)
	}
	return resp
}

// An agent can comment on a ticket it is not assigned to, and naming a ticket that
// does not exist is a clear error rather than a silently dropped note.
func TestHandleTicketCommentValidatesTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agents, _ := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1]
	ticketY := boundTicketID(t, d, z)

	// X comments on Z's ticket — a ticket X does not own.
	resp := callTicketComment(t, d, x, ticketY, "looks good to me")
	if !resp.Ok || resp.TicketCommentResult == nil || resp.TicketCommentResult.TicketID != ticketY {
		t.Fatalf("comment response = %+v, want ok echoing %s", resp, ticketY)
	}

	// A bad ticket id surfaces an error, not a success.
	if bad := callTicketComment(t, d, x, "no-such-ticket", "hi"); bad.Ok {
		t.Fatalf("comment on unknown ticket returned ok: %+v", bad)
	}
}

// The core of the feature: commenting informs a ticket's participants WITHOUT
// enrolling the commenter. X comments on Z's ticket; Z (the assignee) is nudged,
// X is not nudged about its own note — and crucially, a LATER event on that ticket
// does not reach X at all (no doorbell, nothing in its inbox), because a one-shot
// comment confers no participation. This is the multi-step proof that "comment !=
// subscribe": a trivial "commenting didn't nudge me" check would pass even if the
// exclusion were broken.
func TestAgentCommentDoesNotSubscribeCommenter(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agents, inputs := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1] // z owns ticket Y; x owns its own ticket
	ticketY := boundTicketID(t, d, z)
	for _, id := range agents {
		d.store.UpdateState(id, protocol.StateIdle)
	}

	resp := callTicketComment(t, d, x, ticketY, "looks good, ship it")
	if !resp.Ok {
		t.Fatalf("comment response = %+v, want ok", resp)
	}

	// The assignee is a participant -> nudged about the comment on its ticket. This
	// makes the X-exclusion assertions meaningful: the comment really did route.
	if !wasNudged(inputs(z)) {
		t.Fatal("assignee was not nudged about the comment on its ticket")
	}
	// X authored the comment -> never doorbelled about its own note.
	if wasNudged(inputs(x)) {
		t.Fatal("commenter was nudged about its own comment")
	}

	// A later event lands on Y (Z reports ready-for-review). X only commented on Y,
	// so it is not a participant: no nudge, and nothing for Y in its inbox.
	callSetTicketStatus(t, d, z, string(protocol.DispatchWorkStateReadyForReview), "done")
	if wasNudged(inputs(x)) {
		t.Fatal("commenter was nudged by a later event on a ticket it only commented on")
	}
	for _, b := range callTicketInbox(t, d, x) {
		if b.TicketID == ticketY {
			t.Fatalf("commenter's inbox carried events for a ticket it only commented on: %+v", b)
		}
	}
}
