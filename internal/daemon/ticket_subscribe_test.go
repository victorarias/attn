package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func callTicketSubscribe(t *testing.T, d *Daemon, sessionID, ticketID string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketSubscribe(conn, &protocol.TicketSubscribeMessage{
		Cmd:             protocol.CmdTicketSubscribe,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-subscribe response: %v", err)
	}
	return resp
}

func callTicketUnsubscribe(t *testing.T, d *Daemon, sessionID, ticketID string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketUnsubscribe(conn, &protocol.TicketUnsubscribeMessage{
		Cmd:             protocol.CmdTicketUnsubscribe,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-unsubscribe response: %v", err)
	}
	return resp
}

func inboxHasTicket(bundles []protocol.TicketEventBundle, ticketID string) bool {
	for _, b := range bundles {
		if b.TicketID == ticketID && len(b.Events) > 0 {
			return true
		}
	}
	return false
}

// The full subscribe lifecycle over the daemon: an agent subscribes to a ticket it
// does not own, is then nudged by — and its inbox delivers — a later event on that
// ticket, and after unsubscribing it is neither nudged by nor served further events.
// codex (no self-monitor) is used so the doorbell is observable; the trigger is a
// chief comment on the ticket, an event the subscriber did not author. The trigger
// goes through commentOnTicket (synchronous handleTicketAddComment), so the doorbell
// — typeDoorbell sleeps mid-delivery — completes before the assertion, unlike the
// net.Pipe-based callSetTicketStatus which would return before the async notify ran.
func TestTicketSubscribeLifecycle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agents, inputs := delegateMany(t, d, "codex", "Task Y", "Task X")
	z, x := agents[0], agents[1] // z owns ticket Y; x owns its own ticket
	ticketY := boundTicketID(t, d, z)
	for _, id := range agents {
		d.store.UpdateState(id, protocol.StateIdle)
	}

	// x subscribes to Y — a ticket it is not assigned to.
	if resp := callTicketSubscribe(t, d, x, ticketY); !resp.Ok ||
		resp.TicketSubscribeResult == nil || resp.TicketSubscribeResult.TicketID != ticketY {
		t.Fatalf("subscribe response = %+v, want ok echoing %s", resp, ticketY)
	}

	// A later event on Y, authored by neither z nor x (a chief comment).
	commentOnTicket(t, d, ticketY, "chief checking in on this thread")

	// As a subscriber, x is nudged and its inbox carries Y's activity.
	fireNudgeNow(t, d, x) // the comment armed the subscriber's countdown
	if !wasNudged(inputs(x)) {
		t.Fatal("subscriber was not nudged about activity on the ticket it subscribed to")
	}
	nudgesAfterSubscribe := nudgeCount(inputs(x))
	if !inboxHasTicket(callTicketInbox(t, d, x), ticketY) {
		t.Fatal("subscriber's inbox did not deliver the subscribed ticket's activity")
	}

	// x unsubscribes, then another event lands on Y.
	if resp := callTicketUnsubscribe(t, d, x, ticketY); !resp.Ok {
		t.Fatalf("unsubscribe response = %+v, want ok", resp)
	}
	commentOnTicket(t, d, ticketY, "chief following up")

	// No new doorbell, and nothing for Y in its inbox — x is no longer a participant.
	if got := nudgeCount(inputs(x)); got != nudgesAfterSubscribe {
		t.Fatalf("subscriber nudged after unsubscribe: count %d -> %d", nudgesAfterSubscribe, got)
	}
	if inboxHasTicket(callTicketInbox(t, d, x), ticketY) {
		t.Fatal("unsubscribed agent still received the ticket's events in its inbox")
	}
}

// Subscribing to a ticket that doesn't exist is a clear error; unsubscribing is
// idempotent and tolerant of an unknown id.
func TestTicketSubscribeValidatesTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, _ := delegateMany(t, d, "codex", "Task Y")
	x := agents[0]

	if resp := callTicketSubscribe(t, d, x, "no-such-ticket"); resp.Ok {
		t.Fatalf("subscribe to unknown ticket returned ok: %+v", resp)
	}
	// Unsubscribing from a ticket never subscribed (and unknown) still succeeds.
	if resp := callTicketUnsubscribe(t, d, x, "no-such-ticket"); !resp.Ok {
		t.Fatalf("idempotent unsubscribe returned error: %+v", resp)
	}
}
