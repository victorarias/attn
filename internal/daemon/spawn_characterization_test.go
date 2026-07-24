package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func newSpawnCharacterizationDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, *wsClient, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	client := newWorkspaceProtocolTestClient()
	cwd := t.TempDir()
	return d, backend, client, cwd
}

func spawnCharacterizationMessage(id, workspaceID, cwd string) *protocol.SpawnSessionMessage {
	return &protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, Cwd: cwd, Agent: protocol.AgentShellValue, WorkspaceID: workspaceID, Cols: 80, Rows: 24}
}

func assertNoSpawnCharacterizationSession(t *testing.T, d *Daemon, backend *fakeSpawnBackend, id string) {
	t.Helper()
	if session := d.store.Get(id); session != nil {
		t.Fatalf("rejected spawn persisted session: %+v", session)
	}
	if got := spawnCount(backend); got != 0 {
		t.Fatalf("Spawn calls = %d, want 0", got)
	}
}

func TestSpawnCharacterizationRejectsUnknownAgent(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("unknown-agent", "workspace", cwd)
	msg.Agent = "no-such-agent"
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRejectsShellInitialPrompt(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("shell-prompt", "workspace", cwd)
	msg.InitialPrompt = protocol.Ptr("hello")
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRejectsZeroColumns(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("zero-columns", "workspace", cwd)
	msg.Cols = 0
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRejectsOversizedDimensions(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("large-dimensions", "workspace", cwd)
	msg.Cols, msg.Rows = maxPTYDimValue+1, maxPTYDimValue+1
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationUnknownWorkspaceFailsPrecreatedPane(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	const workspaceID, sessionID, paneID = "workspace-created-then-removed", "unknown-workspace-pane", "pane-unknown-workspace"
	addTestWorkspace(d, workspaceID, cwd)
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID, PaneID: protocol.Ptr(paneID), SessionID: sessionID})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	layout := d.store.GetWorkspaceLayout(workspaceID)
	d.store.RemoveWorkspace(workspaceID)
	if err := d.store.SaveWorkspaceLayout(*layout); err != nil {
		t.Fatalf("restore pane layout without workspace: %v", err)
	}
	d.handleSpawnSession(client, spawnCharacterizationMessage(sessionID, workspaceID, cwd))
	expectCommandError(t, client, protocol.CmdSpawnSession, "unknown workspace")
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusFailed, "unknown workspace")
	assertNoSpawnCharacterizationSession(t, d, backend, sessionID)
}

func TestSpawnCharacterizationRejectsUnattendedContractMismatch(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("contract-mismatch", "workspace", cwd)
	msg.Agent, msg.Model, msg.Effort, msg.Executable = "claude", protocol.Ptr("wrong-model"), protocol.Ptr("high"), protocol.Ptr("/opt/claude")
	policy := internalSpawnPolicy{unattendedLaunch: launchcontract.UnattendedLaunchSpec{Agent: "claude", Model: "right-model", Effort: "high", Executable: "/opt/claude", ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAuto, DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh}}
	d.handleSpawnSessionWithPolicy(client, msg, policy)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationDefaultsLabelAndRecordsRecentLocation(t *testing.T) {
	d, _, client, root := newSpawnCharacterizationDaemon(t)
	cwd := filepath.Join(root, "myproj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("label-default", "workspace", cwd)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	if got := d.store.Get(msg.ID).Label; got != "myproj" {
		t.Fatalf("stored label = %q, want myproj", got)
	}
	for _, location := range d.store.GetRecentLocations(50) {
		if location.Path == cwd {
			return
		}
	}
	t.Fatalf("recent locations did not include %q", cwd)
}

func TestSpawnCharacterizationAssociatesSessionWithWorkspace(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("workspace-association", "workspace", cwd)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	if got := d.workspaces.workspaceIDForSession(msg.ID); got != "workspace" {
		t.Fatalf("workspace registry association = %q, want workspace", got)
	}
}

func TestSpawnCharacterizationPersistsCodexResumeID(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("codex-resume", "workspace", cwd)
	msg.Agent, msg.ResumeSessionID = "codex", protocol.Ptr("native-codex-resume")
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	if got := d.store.GetResumeSessionID(msg.ID); got != "native-codex-resume" {
		t.Fatalf("persisted resume id = %q, want native-codex-resume", got)
	}
}

func TestSpawnCharacterizationConsumesQueuedResumeID(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	const sessionID = "queued-resume"
	d.setOrQueueResumeSessionID(sessionID, "queued-native-id")
	d.handleSpawnSession(client, spawnCharacterizationMessage(sessionID, "workspace", cwd))
	expectSpawnResult(t, client, sessionID, true)
	if got := d.store.GetResumeSessionID(sessionID); got != "queued-native-id" {
		t.Fatalf("persisted queued resume id = %q, want queued-native-id", got)
	}
	if got := d.consumePendingResumeSessionID(sessionID); got != "" {
		t.Fatalf("pending resume id = %q, want consumed", got)
	}
}

func TestSpawnCharacterizationBroadcastsRegistrationThenStateChange(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	var events []string
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) { events = append(events, event.Event) }
	msg := spawnCharacterizationMessage("broadcast-choice", "workspace", cwd)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	d.ptyBackend = &fakeSpawnBackend{}
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	var sessionEvents []string
	for _, event := range events {
		if event == protocol.EventSessionRegistered || event == protocol.EventSessionStateChanged {
			sessionEvents = append(sessionEvents, event)
		}
	}
	if len(sessionEvents) != 2 || sessionEvents[0] != protocol.EventSessionRegistered || sessionEvents[1] != protocol.EventSessionStateChanged {
		t.Fatalf("session events = %v, want registered then state_changed", sessionEvents)
	}
}

func TestSpawnCharacterizationRearmsTicketReconciliation(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	const sessionID = "ticket-rearm"
	ticket, err := d.store.CreateTicket(store.Ticket{ID: "ticket-rearm", Title: "Rearm", Assignee: sessionID, Status: store.TicketStatusWorking}, "test", time.Now())
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("seed reconciliation flag = (%v, %v), want (true, nil)", claimed, err)
	}
	d.handleSpawnSession(client, spawnCharacterizationMessage(sessionID, "workspace", cwd))
	expectSpawnResult(t, client, sessionID, true)
	if claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("rearmed reconciliation flag = (%v, %v), want (true, nil)", claimed, err)
	}
}

func TestSpawnCharacterizationChiefSettingsFillModelAndEffort(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	d.store.SetSetting(SettingChiefModelPrefix+"claude", "chief-model")
	d.store.SetSetting(SettingChiefEffortPrefix+"claude", "chief-effort")
	msg := spawnCharacterizationMessage("chief-fallback", "workspace", cwd)
	msg.Agent, msg.ChiefOfStaff = "claude", protocol.Ptr(true)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	spawn, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("expected PTY spawn")
	}
	if spawn.Model != "chief-model" || spawn.Effort != "chief-effort" {
		t.Fatalf("chief spawn pins = (%q, %q), want configured fallback", spawn.Model, spawn.Effort)
	}
}

func TestSpawnCharacterizationRejectsPluginChiefWithoutResumeCapability(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	plugin, done := startPluginPipe(t, d, "characterization-plugin", nil)
	defer func() { _ = plugin.Close(); <-done }()
	registerTestPluginDriver(t, plugin, "characterization", map[string]bool{"launch_instructions": true})
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("plugin-chief-resume", "workspace", cwd)
	msg.Agent, msg.ChiefOfStaff = "characterization", protocol.Ptr(true)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationPluginChiefResumeFailureMentionsCapability(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	plugin, done := startPluginPipe(t, d, "characterization-plugin-error", nil)
	defer func() { _ = plugin.Close(); <-done }()
	registerTestPluginDriver(t, plugin, "characterization-error", map[string]bool{"launch_instructions": true})
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("plugin-chief-error", "workspace", cwd)
	msg.Agent, msg.ChiefOfStaff = "characterization-error", protocol.Ptr(true)
	d.handleSpawnSession(client, msg)
	select {
	case outbound := <-client.send:
		if !strings.Contains(string(outbound.payload), "resume capability") {
			t.Fatalf("failure payload = %s, want resume capability", outbound.payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for spawn failure")
	}
}

func TestSpawnCharacterizationAlreadyLivePluginRespawnSkipsPluginPrep(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	plugin, pluginDone := startPluginPipe(t, d, "characterization-live-plugin", nil)
	defer func() { _ = plugin.Close(); <-pluginDone }()
	registerTestPluginDriver(t, plugin, "characterization-live", nil)
	addTestWorkspace(d, "workspace", cwd)

	driverSpawns := make(chan bool, 1)
	go func() {
		request := decodeJSONRPCMessage(t, plugin)
		if request.Method != "driver.spawn" {
			driverSpawns <- false
			return
		}
		respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"characterization-live"}})

		_ = plugin.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		var secondRequest jsonRPCMessage
		if err := json.NewDecoder(plugin).Decode(&secondRequest); err != nil {
			driverSpawns <- false
			return
		}
		driverSpawns <- secondRequest.Method == "driver.spawn"
		if secondRequest.Method == "driver.spawn" {
			respondPluginRequest(t, plugin, secondRequest, pluginDriverSpawnResult{Argv: []string{"characterization-live"}})
		}
	}()

	msg := spawnCharacterizationMessage("already-live-plugin", "workspace", cwd)
	msg.Agent = "characterization-live"
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)

	backend.mu.Lock()
	backend.sessionIDs = []string{msg.ID}
	backend.mu.Unlock()

	secondSpawnDone := make(chan struct{})
	go func() {
		d.handleSpawnSession(client, msg)
		close(secondSpawnDone)
	}()
	select {
	case <-secondSpawnDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for already-live spawn result")
	}
	expectSpawnResult(t, client, msg.ID, true)

	select {
	case gotSecondDriverSpawn := <-driverSpawns:
		if gotSecondDriverSpawn {
			t.Fatal("already-live respawn sent a second driver.spawn request")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plugin spawn probe")
	}
	if got := spawnCount(backend); got != 1 {
		t.Fatalf("Spawn calls = %d, want 1", got)
	}
}
