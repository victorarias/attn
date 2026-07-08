package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func callTicketInboxResult(t *testing.T, d *Daemon, sessionID string) *protocol.TicketInboxResult {
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
	return resp.TicketInboxResult
}

// TestTicketInboxSurfacesUserPresence verifies the ticket inbox result carries
// last_user_activity_at once the daemon has observed a UI-presence websocket
// command (see isUserPresenceCommand), and omits it when no presence has been
// recorded since the daemon started.
func TestTicketInboxSurfacesUserPresence(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	// No presence observed yet: the field is absent.
	result := callTicketInboxResult(t, d, "session-1")
	if result.LastUserActivityAt != nil {
		t.Fatalf("last_user_activity_at = %v, want nil before any recorded activity", *result.LastUserActivityAt)
	}

	stampedAt := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	d.recordUserActivity(stampedAt)

	result = callTicketInboxResult(t, d, "session-1")
	if result.LastUserActivityAt == nil {
		t.Fatalf("last_user_activity_at = nil, want %s after recording activity", stampedAt.Format(time.RFC3339))
	}
	if got, want := *result.LastUserActivityAt, stampedAt.Format(time.RFC3339); got != want {
		t.Fatalf("last_user_activity_at = %q, want %q", got, want)
	}
}

// TestHandleClientMessageStampsUserPresence drives the real websocket
// pre-dispatch path (not the recordUserActivity helper directly) to prove a
// UI-origin command like session_selected reaches isUserPresenceCommand and
// stamps presence, and that the stamp then surfaces on the ticket inbox
// result.
func TestHandleClientMessageStampsUserPresence(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	client := &wsClient{}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	before := time.Now()
	d.handleClientMessage(client, []byte(`{"cmd":"session_selected","id":"session-1"}`))
	after := time.Now()

	result := callTicketInboxResult(t, d, "session-1")
	if result.LastUserActivityAt == nil {
		t.Fatal("last_user_activity_at = nil, want stamped after session_selected")
	}
	got, err := time.Parse(time.RFC3339, *result.LastUserActivityAt)
	if err != nil {
		t.Fatalf("parse last_user_activity_at: %v", err)
	}
	if got.Before(before.Add(-time.Second)) || got.After(after.Add(time.Second)) {
		t.Fatalf("last_user_activity_at = %v, want within [%v, %v]", got, before, after)
	}
}

// TestIsUserPresenceCommandAllowlist locks the exact allowlist named in the
// design: UI-origin websocket commands that indicate the user is at the app,
// as opposed to unix-socket CLI/agent commands (which never reach
// isUserPresenceCommand) or other websocket commands that don't reflect direct
// user attention.
func TestIsUserPresenceCommandAllowlist(t *testing.T) {
	present := []string{
		protocol.CmdSessionSelected,
		protocol.CmdWorkspaceSelected,
		protocol.CmdSessionVisualized,
		protocol.CmdPRVisited,
		protocol.CmdPtyInput,
		protocol.CmdPtyResize,
	}
	for _, cmd := range present {
		if !isUserPresenceCommand(cmd) {
			t.Errorf("isUserPresenceCommand(%q) = false, want true", cmd)
		}
	}

	absent := []string{
		protocol.CmdClientHello,
		protocol.CmdDelegate,
		protocol.CmdTicketInbox,
		protocol.CmdGetSettings,
		"",
	}
	for _, cmd := range absent {
		if isUserPresenceCommand(cmd) {
			t.Errorf("isUserPresenceCommand(%q) = true, want false", cmd)
		}
	}
}
