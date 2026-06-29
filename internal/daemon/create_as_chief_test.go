package daemon

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// spawnForChiefTest runs the app's create ordering (register workspace, add the
// session pane, spawn the session) and leaves the spawn_result on the client for
// the caller to drain. It mirrors the real new-session flow so the create-as-chief
// wiring is exercised end to end through handleSpawnSession.
func spawnForChiefTest(t *testing.T, d *Daemon, client *wsClient, workspaceID, sessionID, agent string, chief bool) {
	t.Helper()
	cwd := t.TempDir()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Chief Test",
		Directory: cwd,
	})
	paneID := "pane-" + sessionID
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(sessionID),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:          protocol.CmdSpawnSession,
		ID:           sessionID,
		Label:        protocol.Ptr(sessionID),
		Cwd:          cwd,
		Agent:        agent,
		WorkspaceID:  workspaceID,
		Cols:         80,
		Rows:         24,
		ChiefOfStaff: protocol.Ptr(chief),
	})
}

// A create-as-chief spawn assigns the profile-wide chief role at launch — the only
// way the very first boot injects chief guidance — and the spawned session
// broadcasts as the chief.
func TestCreateAsChiefAssignsRoleAtLaunch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()

	spawnForChiefTest(t, d, client, "ws-chief", "sess-chief", string(protocol.SessionAgentClaude), true)
	expectSpawnResult(t, client, "sess-chief", true)

	if got := d.chiefOfStaffSessionID(); got != "sess-chief" {
		t.Fatalf("chief role holder = %q, want sess-chief", got)
	}
	session := d.store.Get("sess-chief")
	if session == nil {
		t.Fatal("session was not registered")
	}
	decorated := d.sessionForBroadcast(session)
	if decorated.ChiefOfStaff == nil || !*decorated.ChiefOfStaff {
		t.Fatalf("broadcast session ChiefOfStaff = %v, want true", decorated.ChiefOfStaff)
	}
}

// The chief role is single-holder: a create-as-chief request while a chief already
// exists is ignored, never a silent role transfer.
func TestCreateAsChiefSkippedWhenChiefExists(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()

	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "incumbent"); err != nil {
		t.Fatalf("seed incumbent chief: %v", err)
	}

	spawnForChiefTest(t, d, client, "ws-second", "sess-second", string(protocol.SessionAgentClaude), true)
	expectSpawnResult(t, client, "sess-second", true)

	if got := d.chiefOfStaffSessionID(); got != "incumbent" {
		t.Fatalf("chief role holder = %q, want incumbent (unchanged)", got)
	}
	if d.isChiefOfStaffSession("sess-second") {
		t.Fatal("second session must not have taken the chief role")
	}
}

// A shell has no chief-guidance launch path, so create-as-chief is ignored for it
// even when requested.
func TestCreateAsChiefIgnoredForShell(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()

	spawnForChiefTest(t, d, client, "ws-shell", "sess-shell", protocol.AgentShellValue, true)
	expectSpawnResult(t, client, "sess-shell", true)

	if got := d.chiefOfStaffSessionID(); got != "" {
		t.Fatalf("chief role holder = %q, want empty (shell cannot be chief)", got)
	}
}

// A spawn that fails after the role was assigned must roll the role back, so a
// session that never launched never holds the chief role.
func TestCreateAsChiefRolledBackOnSpawnFailure(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newWorkspaceProtocolTestClient()

	spawnForChiefTest(t, d, client, "ws-fail", "sess-fail", string(protocol.SessionAgentClaude), true)
	expectSpawnResult(t, client, "sess-fail", false)

	if got := d.chiefOfStaffSessionID(); got != "" {
		t.Fatalf("chief role holder = %q, want empty (assignment rolled back on spawn failure)", got)
	}
}

// maybeAssignChiefOnSpawn never assigns on a respawn/reload (existingSession set):
// a respawn of a non-chief session must not silently promote it just because the
// client echoed the flag.
func TestMaybeAssignChiefOnSpawnSkipsRespawn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })

	existing := &protocol.Session{ID: "sess-respawn"}
	if assigned := d.maybeAssignChiefOnSpawn("sess-respawn", string(protocol.SessionAgentClaude), true, existing); assigned {
		t.Fatal("respawn (existingSession != nil) must not assign the chief role")
	}
	if got := d.chiefOfStaffSessionID(); got != "" {
		t.Fatalf("chief role holder = %q, want empty", got)
	}
}
