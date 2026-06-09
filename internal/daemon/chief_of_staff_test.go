package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func readChiefOfStaffResult(t *testing.T, client *wsClient) protocol.ChiefOfStaffResultMessage {
	t.Helper()
	select {
	case raw := <-client.send:
		var result protocol.ChiefOfStaffResultMessage
		if err := json.Unmarshal(raw.payload, &result); err != nil {
			t.Fatalf("decode chief_of_staff_result: %v", err)
		}
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chief_of_staff_result")
		return protocol.ChiefOfStaffResultMessage{}
	}
}

func newChiefOfStaffTestDaemon(t *testing.T) (*Daemon, *wsClient) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	return d, newRenameTestClient()
}

func addChiefOfStaffTestSession(d *Daemon, id, label string) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: label, Agent: protocol.SessionAgentCodex,
		Directory: "/tmp/" + id, WorkspaceID: "workspace-" + id,
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestSetChiefOfStaffTransfersSingletonRole(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "session-a", "first")
	addChiefOfStaffTestSession(d, "session-b", "second")

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "session-a", ChiefOfStaff: true,
	})
	first := readChiefOfStaffResult(t, client)
	if !first.Success || d.chiefOfStaffSessionID() != "session-a" {
		t.Fatalf("first assignment = %+v role=%q", first, d.chiefOfStaffSessionID())
	}

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "session-b", ChiefOfStaff: true,
	})
	second := readChiefOfStaffResult(t, client)
	if !second.Success || protocol.Deref(second.PreviousSessionID) != "session-a" {
		t.Fatalf("transfer result = %+v", second)
	}
	if got := d.chiefOfStaffSessionID(); got != "session-b" {
		t.Fatalf("role after transfer = %q, want session-b", got)
	}

	sessions := d.mergedSessionsForBroadcast()
	for _, session := range sessions {
		switch session.ID {
		case "session-a":
			if protocol.Deref(session.ChiefOfStaff) {
				t.Fatal("previous session still marked chief")
			}
		case "session-b":
			if !protocol.Deref(session.ChiefOfStaff) {
				t.Fatal("new session not marked chief")
			}
		}
	}
}

func TestSetChiefOfStaffRejectsUnknownSession(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "missing", ChiefOfStaff: true,
	})
	result := readChiefOfStaffResult(t, client)
	if result.Success || protocol.Deref(result.Error) == "" {
		t.Fatalf("result = %+v, want failure", result)
	}
	if got := d.chiefOfStaffSessionID(); got != "" {
		t.Fatalf("role = %q, want empty", got)
	}
}

func TestClearChiefOfStaffKeepsTransferredRole(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "session-a", "first")
	addChiefOfStaffTestSession(d, "session-b", "second")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "session-b"); err != nil {
		t.Fatal(err)
	}

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "session-a", ChiefOfStaff: false,
	})
	result := readChiefOfStaffResult(t, client)
	if !result.Success {
		t.Fatalf("clear result = %+v", result)
	}
	if got := d.chiefOfStaffSessionID(); got != "session-b" {
		t.Fatalf("role after stale clear = %q, want session-b", got)
	}
}
