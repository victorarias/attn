package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func callTicketInbox(t *testing.T, d *Daemon, sessionID string) []protocol.TicketEventBundle {
	t.Helper()
	server, clientConn := net.Pipe()
	go func() {
		d.handleTicketInbox(server, &protocol.TicketInboxMessage{
			Cmd:             protocol.CmdTicketInbox,
			SourceSessionID: sessionID,
		})
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode ticket inbox response: %v", err)
	}
	_ = clientConn.Close()
	if !resp.Ok {
		t.Fatalf("ticket inbox not ok: %+v", resp)
	}
	if resp.TicketInboxResult == nil {
		return nil
	}
	return resp.TicketInboxResult.Bundles
}

// The inbox is a consume keyed on observer identity: the agent sees the
// chief-authored events on its ticket (its assignment, later steers) but never its
// own; the chief sees the agent's reports but never its own; and a second read is
// empty because the first advanced the cursor.
func TestTicketInboxConsumesByIdentity(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
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
	agentSession := result.SessionID
	ticketID := boundTicketID(t, d, agentSession)

	// Agent's first read: the chief-authored created event (its assignment).
	bundles := callTicketInbox(t, d, agentSession)
	if len(bundles) != 1 || bundles[0].TicketID != ticketID {
		t.Fatalf("agent inbox = %+v, want one bundle for %q", bundles, ticketID)
	}
	if len(bundles[0].Events) != 1 || bundles[0].Events[0].Kind != protocol.TicketEventKind(store.TicketEventCreated) {
		t.Fatalf("agent inbox events = %+v, want one created event", bundles[0].Events)
	}
	if bundles[0].Events[0].Author != chiefSessionID {
		t.Fatalf("created event author = %q, want chief %q", bundles[0].Events[0].Author, chiefSessionID)
	}

	// Second read: empty — the first consume advanced the agent's cursor.
	if again := callTicketInbox(t, d, agentSession); len(again) != 0 {
		t.Fatalf("second agent inbox = %+v, want empty", again)
	}

	// The agent reports ready-for-review. Its own event is self-authored, so it
	// stays out of the agent's inbox but lands in the chief's.
	callSetTicketStatus(t, d, agentSession, string(protocol.DispatchWorkStateReadyForReview), "take a look")
	if again := callTicketInbox(t, d, agentSession); len(again) != 0 {
		t.Fatalf("agent inbox after self-report = %+v, want empty", again)
	}

	chiefBundles := callTicketInbox(t, d, chiefSessionID)
	if len(chiefBundles) != 1 || chiefBundles[0].TicketID != ticketID {
		t.Fatalf("chief inbox = %+v, want one bundle for %q", chiefBundles, ticketID)
	}
	ev := chiefBundles[0].Events[len(chiefBundles[0].Events)-1]
	if ev.Kind != protocol.TicketEventKind(store.TicketEventStatusChanged) || ev.Author != agentSession {
		t.Fatalf("chief inbox last event = %+v, want agent status change", ev)
	}
	if ev.ToStatus == nil || *ev.ToStatus != protocol.TicketStatusInReview {
		t.Fatalf("chief inbox status event to = %v, want in_review", ev.ToStatus)
	}
	if ev.Comment == nil || *ev.Comment != "take a look" {
		t.Fatalf("chief inbox status event comment = %v, want the supplied note", ev.Comment)
	}
}

func TestTicketInboxRequiresSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	server, clientConn := net.Pipe()
	go func() {
		d.handleTicketInbox(server, &protocol.TicketInboxMessage{Cmd: protocol.CmdTicketInbox})
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = clientConn.Close()
	if resp.Ok || resp.Error == nil {
		t.Fatalf("response = %+v, want error", resp)
	}
}
