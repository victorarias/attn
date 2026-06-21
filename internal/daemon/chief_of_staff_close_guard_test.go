package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// addChiefWorkspaceLayout wires a workspace whose layout holds two agent panes
// so the close-pane guard can be exercised against the chief pane while leaving
// a sibling pane to prove ordinary panes still close.
func addChiefWorkspaceLayout(t *testing.T, d *Daemon, workspaceID, chiefSessionID, chiefPaneID, otherSessionID, otherPaneID string) {
	t.Helper()
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: "shared", Directory: "/tmp/" + workspaceID})
	if err := d.store.SaveWorkspaceLayout(workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: chiefPaneID,
		Layout: workspacelayout.Node{
			Type:      "split",
			SplitID:   "split-1",
			Direction: workspacelayout.DirectionVertical,
			Ratio:     0.5,
			Children: []workspacelayout.Node{
				{Type: "pane", PaneID: chiefPaneID},
				{Type: "pane", PaneID: otherPaneID},
			},
		},
		Panes: []workspacelayout.Pane{
			{PaneID: chiefPaneID, RuntimeID: chiefSessionID, SessionID: chiefSessionID, Kind: workspacelayout.PaneKindAgent, Title: "Chief"},
			{PaneID: otherPaneID, RuntimeID: otherSessionID, SessionID: otherSessionID, Kind: workspacelayout.PaneKindAgent, Title: "Worker"},
		},
	}); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}
}

// A direct unregister of the chief-of-staff session is refused: the session
// survives and the client is told the chief is protected, so an accidental ⌘W or
// close action cannot tear down the orchestrator.
func TestHandleUnregisterWS_RefusesChiefOfStaff(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "chief"})

	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session was unregistered despite the close guard")
	}
	if got := d.chiefOfStaffSessionID(); got != "chief" {
		t.Fatalf("chief role after refused close = %q, want chief", got)
	}
	expectCommandError(t, client, protocol.CmdUnregister, chiefOfStaffProtectedError)
}

// The guard is scoped to the chief alone: an ordinary session still unregisters
// normally even while a chief exists in the same profile.
func TestHandleUnregisterWS_AllowsNonChiefWhileChiefExists(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	addChiefOfStaffTestSession(d, "worker", "Worker")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "worker"})

	if d.store.Get("worker") != nil {
		t.Fatal("ordinary session was not unregistered while a chief existed")
	}
	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session must survive a sibling's close")
	}
}

// Closing the chief's workspace pane is refused too: the pane and its session
// survive and the layout is untouched.
func TestHandleWorkspaceLayoutClosePane_RefusesChiefOfStaff(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	addChiefOfStaffTestSession(d, "worker", "Worker")
	addChiefWorkspaceLayout(t, d, "workspace-shared", "chief", "pane-chief", "worker", "pane-worker")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: "workspace-shared", PaneID: "pane-chief",
	})

	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, "workspace-shared", "pane-chief", false)
	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session was closed via its workspace pane despite the guard")
	}
	layout := d.store.GetWorkspaceLayout("workspace-shared")
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("chief pane was removed from layout: %+v", layout)
	}
}

// A non-chief pane in the same workspace still closes, removing only that
// session and leaving the protected chief pane in place.
func TestHandleWorkspaceLayoutClosePane_AllowsNonChiefPane(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	addChiefOfStaffTestSession(d, "worker", "Worker")
	addChiefWorkspaceLayout(t, d, "workspace-shared", "chief", "pane-chief", "worker", "pane-worker")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: "workspace-shared", PaneID: "pane-worker",
	})

	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, "workspace-shared", "pane-worker", true)
	if d.store.Get("worker") != nil {
		t.Fatal("ordinary pane's session was not closed")
	}
	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session must survive closing a sibling pane")
	}
}
