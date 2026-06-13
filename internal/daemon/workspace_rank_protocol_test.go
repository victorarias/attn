package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// registerRankWorkspace registers a workspace the way the app does and drains
// the registration broadcast. Creation order seeds the rank, so the first
// registered workspace sorts first.
func registerRankWorkspace(t *testing.T, d *Daemon, client *wsClient, id, dir string) {
	t.Helper()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        id,
		Title:     id,
		Directory: dir,
	})
}

// sendSetWorkspaceRank drives the reorder command and waits for its action
// result. prevID ends up ABOVE the moved workspace, nextID BELOW it; an empty
// id means top/bottom.
func sendSetWorkspaceRank(t *testing.T, d *Daemon, client *wsClient, workspaceID, prevID, nextID string) {
	t.Helper()
	msg := &protocol.SetWorkspaceRankMessage{
		Cmd:         protocol.CmdSetWorkspaceRank,
		WorkspaceID: workspaceID,
	}
	if prevID != "" {
		msg.PrevWorkspaceID = protocol.Ptr(prevID)
	}
	if nextID != "" {
		msg.NextWorkspaceID = protocol.Ptr(nextID)
	}
	d.handleSetWorkspaceRank(client, msg)
	expectSetWorkspaceRankResult(t, client, workspaceID, true)
}

func expectSetWorkspaceRankResult(t *testing.T, client *wsClient, workspaceID string, success bool) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.WorkspaceLayoutActionResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventWorkspaceLayoutActionResult {
				continue
			}
			if result.Action != protocol.CmdSetWorkspaceRank || result.WorkspaceID != workspaceID {
				continue
			}
			if result.Success != success {
				t.Fatalf("set_workspace_rank success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for set_workspace_rank result for %s", workspaceID)
		}
	}
}

// storedWorkspaceOrder returns the workspace ids in persisted sidebar order
// (ORDER BY rank, created_at) straight from the store, which is the canonical
// order the frontend mirrors.
func storedWorkspaceOrder(t *testing.T, d *Daemon) []string {
	t.Helper()
	var ids []string
	for _, ws := range d.store.ListWorkspaces() {
		ids = append(ids, ws.ID)
	}
	return ids
}

func assertWorkspaceOrder(t *testing.T, d *Daemon, want ...string) {
	t.Helper()
	got := storedWorkspaceOrder(t, d)
	if len(got) != len(want) {
		t.Fatalf("workspace order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("workspace order = %v, want %v", got, want)
		}
	}
}

// TestSetWorkspaceRankReordersWorkspaces moves a workspace to the top, middle,
// and bottom of the sidebar via neighbour ids and asserts the persisted order
// changes each time.
func TestSetWorkspaceRankReordersWorkspaces(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	dir := t.TempDir()

	// Creation order seeds ascending ranks: a < b < c.
	registerRankWorkspace(t, d, client, "ws-a", dir)
	registerRankWorkspace(t, d, client, "ws-b", dir)
	registerRankWorkspace(t, d, client, "ws-c", dir)
	assertWorkspaceOrder(t, d, "ws-a", "ws-b", "ws-c")

	// Move ws-c to the top (no prev neighbour => above everything).
	sendSetWorkspaceRank(t, d, client, "ws-c", "", "ws-a")
	assertWorkspaceOrder(t, d, "ws-c", "ws-a", "ws-b")

	// Move ws-c to the middle, between ws-a and ws-b.
	sendSetWorkspaceRank(t, d, client, "ws-c", "ws-a", "ws-b")
	assertWorkspaceOrder(t, d, "ws-a", "ws-c", "ws-b")

	// Move ws-a to the bottom (no next neighbour => below everything).
	sendSetWorkspaceRank(t, d, client, "ws-a", "ws-b", "")
	assertWorkspaceOrder(t, d, "ws-c", "ws-b", "ws-a")
}

// TestSetWorkspaceRankSurvivesReRegister confirms a reorder is durable: the new
// key is read back from the store, and re-registering the moved workspace (the
// reconnect/retry path) does not reset it, so the order holds.
func TestSetWorkspaceRankSurvivesReRegister(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	dir := t.TempDir()

	registerRankWorkspace(t, d, client, "ws-a", dir)
	registerRankWorkspace(t, d, client, "ws-b", dir)
	registerRankWorkspace(t, d, client, "ws-c", dir)

	// Move ws-a below ws-c (to the bottom).
	sendSetWorkspaceRank(t, d, client, "ws-a", "ws-c", "")
	assertWorkspaceOrder(t, d, "ws-b", "ws-c", "ws-a")

	movedRank := ""
	if ws := d.store.GetWorkspace("ws-a"); ws != nil {
		movedRank = ws.Rank
	}
	if movedRank == "" {
		t.Fatal("expected ws-a to have a persisted rank after reorder")
	}

	// Re-register ws-a (reconnect/retry path). Like title/muted, the stored rank
	// must survive so the user's reorder sticks.
	registerRankWorkspace(t, d, client, "ws-a", dir)

	if ws := d.store.GetWorkspace("ws-a"); ws == nil || ws.Rank != movedRank {
		got := ""
		if ws != nil {
			got = ws.Rank
		}
		t.Fatalf("ws-a rank after re-register = %q, want %q", got, movedRank)
	}
	assertWorkspaceOrder(t, d, "ws-b", "ws-c", "ws-a")
}
