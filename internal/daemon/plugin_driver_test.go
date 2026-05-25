package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestPluginDriverRegister_PublishesDynamicAgentSettings(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()

	registerTestPluginDriver(t, client, "snipe", map[string]bool{
		"resume": true,
		"yolo":   true,
	})

	settings := d.settingsWithAgentAvailability()
	if got := settings["snipe_available"]; got != "true" {
		t.Fatalf("snipe_available=%v, want true", got)
	}
	if got := settings["snipe_cap_resume"]; got != "true" {
		t.Fatalf("snipe_cap_resume=%v, want true", got)
	}
	if err := d.validateNewSessionAgent("snipe"); err != nil {
		t.Fatalf("validateNewSessionAgent(snipe) error=%v", err)
	}
}

func TestHandleSpawnSession_PluginDriverLaunchesReturnedCommand(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"yolo": true})

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		if request.Method != "driver.spawn" {
			t.Errorf("method=%q, want driver.spawn", request.Method)
			return
		}
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode spawn params: %v", err)
			return
		}
		if params.SessionID != "snipe-session" || !params.Yolo {
			t.Errorf("spawn params=%+v, want session id and yolo request", params)
			return
		}
		if params.RunID == "" {
			t.Error("spawn run_id is empty, want daemon-assigned run identity")
			return
		}
		respondPluginRequest(t, client, request, pluginDriverSpawnResult{
			Argv: []string{"snipe", "--permission-mode", "bypassPermissions"},
			Env:  map[string]string{"SNIPE_BRIDGE": "ready"},
			CWD:  "/tmp/plugin-launch-cwd",
		})
	}()

	addTestWorkspace(d, "workspace-snipe", t.TempDir())
	ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(ws, &protocol.SpawnSessionMessage{
		ID:          "snipe-session",
		Cwd:         t.TempDir(),
		WorkspaceID: "workspace-snipe",
		Agent:       "snipe",
		Cols:        80,
		Rows:        24,
		YoloMode:    protocol.Ptr(true),
	})
	<-requestDone

	spawn, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("expected PTY spawn")
	}
	if got := spawn.ExternalCommand; len(got) != 3 || got[0] != "snipe" || got[2] != "bypassPermissions" {
		t.Fatalf("external command=%v, want returned plugin argv", got)
	}
	if got := spawn.ExternalCWD; got != "/tmp/plugin-launch-cwd" {
		t.Fatalf("external cwd=%q, want plugin override", got)
	}
	if len(spawn.ExternalEnv) != 1 || spawn.ExternalEnv[0] != "SNIPE_BRIDGE=ready" {
		t.Fatalf("external env=%v, want deterministic plugin env", spawn.ExternalEnv)
	}
	if session := d.store.Get("snipe-session"); session == nil || session.Agent != "snipe" {
		t.Fatalf("stored session=%+v, want snipe agent", session)
	}
}

func TestHandleSpawnSession_PluginDriverWithoutResumeRelaunchesWithSpawn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client, done := startPluginPipe(t, d, "spawn-only-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "spawn-only", map[string]bool{})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "spawn-only-session",
		Label:          "existing",
		Agent:          "spawn-only",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		if request.Method != "driver.spawn" {
			t.Errorf("method=%q, want driver.spawn for plugin without resume capability", request.Method)
			return
		}
		respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"spawn-only"}})
	}()

	addTestWorkspace(d, "workspace-spawn-only", t.TempDir())
	ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(ws, &protocol.SpawnSessionMessage{
		ID:          "spawn-only-session",
		Cwd:         t.TempDir(),
		WorkspaceID: "workspace-spawn-only",
		Agent:       "spawn-only",
		Cols:        80,
		Rows:        24,
	})
	<-requestDone
}

func TestPluginDriverReports_StateStopAndMetadataAreOwnedByRegisteredAgent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "snipe-report",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("snipe-report", "snipe-plugin", "run-report") {
		t.Fatal("failed to begin test plugin run")
	}

	sendPluginMethod(t, client, 3, "session.report_metadata", pluginReportMetadataParams{
		SessionID: "snipe-report",
		RunID:     "run-report",
		Seq:       1,
		Metadata:  json.RawMessage(`{"snipe_session_id":"native-id"}`),
	})
	if got := d.store.GetAgentMetadata("snipe-report"); got != `{"snipe_session_id":"native-id"}` {
		t.Fatalf("metadata=%q, want plugin metadata", got)
	}

	sendPluginMethod(t, client, 4, "session.report_state", pluginReportStateParams{
		SessionID: "snipe-report",
		RunID:     "run-report",
		Seq:       2,
		State:     protocol.StateWorking,
	})
	if got := d.store.Get("snipe-report").State; got != protocol.SessionStateWorking {
		t.Fatalf("state=%q, want working", got)
	}

	sendPluginMethod(t, client, 5, "session.report_stop", pluginReportStopParams{
		SessionID: "snipe-report",
		RunID:     "run-report",
		Seq:       3,
		Verdict:   protocol.StateWaitingInput,
	})
	if got := d.store.Get("snipe-report").State; got != protocol.SessionStateWaitingInput {
		t.Fatalf("state=%q, want waiting_input", got)
	}
}

func TestPluginDriverReports_ReregisteredAgentCannotTakeOverActiveRun(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	owner, ownerDone := startPluginPipe(t, d, "owner-plugin", nil)
	registerTestPluginDriver(t, owner, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "owned-run",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("owned-run", "owner-plugin", "run-owner") {
		t.Fatal("failed to begin owner plugin run")
	}
	_ = owner.Close()
	<-ownerDone

	replacement, replacementDone := startPluginPipe(t, d, "replacement-plugin", nil)
	defer func() {
		_ = replacement.Close()
		<-replacementDone
	}()
	registerTestPluginDriver(t, replacement, "snipe", map[string]bool{"state_reporting": true})

	response := sendPluginMethodResponse(t, replacement, 20, "session.report_state", pluginReportStateParams{
		SessionID: "owned-run",
		RunID:     "run-owner",
		Seq:       1,
		State:     protocol.StateWorking,
	})
	if response.Error == nil {
		t.Fatal("replacement plugin report succeeded, want ownership error")
	}
	if got := d.store.Get("owned-run").State; got != protocol.SessionStateLaunching {
		t.Fatalf("state=%q after replacement report, want launching", got)
	}
}

func TestHandleSpawnSession_PluginDriverQueuesReportsDuringPTYStartup(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	backend := &fakeSpawnBackend{}
	launchRunID := make(chan string, 1)
	backend.onSpawn = func() {
		runID := <-launchRunID
		sendPluginMethod(t, client, 7, "session.report_metadata", pluginReportMetadataParams{
			SessionID: "early-report",
			RunID:     runID,
			Seq:       1,
			Metadata:  json.RawMessage(`{"snipe_session_id":"early-native-id"}`),
		})
		sendPluginMethod(t, client, 8, "session.report_state", pluginReportStateParams{
			SessionID: "early-report",
			RunID:     runID,
			Seq:       2,
			State:     protocol.StateWorking,
		})
	}
	d.ptyBackend = backend

	go func() {
		request := decodeJSONRPCMessage(t, client)
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode launch params: %v", err)
			return
		}
		launchRunID <- params.RunID
		respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()

	addTestWorkspace(d, "workspace-early", t.TempDir())
	ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(ws, &protocol.SpawnSessionMessage{
		ID:          "early-report",
		Cwd:         t.TempDir(),
		WorkspaceID: "workspace-early",
		Agent:       "snipe",
		Cols:        80,
		Rows:        24,
	})

	session := d.store.Get("early-report")
	if session == nil || session.State != protocol.SessionStateWorking {
		t.Fatalf("session=%+v, want queued working state applied", session)
	}
	if got := d.store.GetAgentMetadata("early-report"); got != `{"snipe_session_id":"early-native-id"}` {
		t.Fatalf("metadata=%q, want queued startup metadata applied", got)
	}
}

func TestPluginDriverReports_StaleStateAndStopCannotOverwriteNewerState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "ordered-report",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("ordered-report", "snipe-plugin", "run-current") {
		t.Fatal("failed to begin test plugin run")
	}

	sendPluginMethod(t, client, 9, "session.report_state", pluginReportStateParams{
		SessionID: "ordered-report",
		RunID:     "run-current",
		Seq:       2,
		State:     protocol.StateWorking,
	})
	sendPluginMethod(t, client, 10, "session.report_state", pluginReportStateParams{
		SessionID: "ordered-report",
		RunID:     "run-current",
		Seq:       1,
		State:     protocol.StateIdle,
	})
	if got := d.store.Get("ordered-report").State; got != protocol.SessionStateWorking {
		t.Fatalf("state after stale lifecycle report=%q, want working", got)
	}

	sendPluginMethod(t, client, 11, "session.report_state", pluginReportStateParams{
		SessionID: "ordered-report",
		RunID:     "run-current",
		Seq:       4,
		State:     protocol.StatePendingApproval,
	})
	sendPluginMethod(t, client, 12, "session.report_stop", pluginReportStopParams{
		SessionID: "ordered-report",
		RunID:     "run-current",
		Seq:       3,
		Verdict:   protocol.StateWaitingInput,
	})
	if got := d.store.Get("ordered-report").State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("state after stale stop verdict=%q, want pending_approval", got)
	}

	response := sendPluginMethodResponse(t, client, 13, "session.report_state", pluginReportStateParams{
		SessionID: "ordered-report",
		RunID:     "run-stale",
		Seq:       99,
		State:     protocol.StateIdle,
	})
	if response.Error == nil {
		t.Fatal("previous-run report succeeded, want ownership error")
	}
	if got := d.store.Get("ordered-report").State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("state after previous-run report=%q, want pending_approval", got)
	}
}

func TestPluginDriverReports_RequireRunCursor(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "missing-seq",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("missing-seq", "snipe-plugin", "run-current") {
		t.Fatal("failed to begin test plugin run")
	}

	response := sendPluginMethodResponse(t, client, 14, "session.report_state", pluginReportStateParams{
		SessionID: "missing-seq",
		RunID:     "run-current",
		State:     protocol.StateWorking,
	})
	if response.Error == nil {
		t.Fatal("session.report_state without seq succeeded, want protocol error")
	}
	if got := d.store.Get("missing-seq").State; got != protocol.SessionStateLaunching {
		t.Fatalf("state=%q after invalid report, want launching", got)
	}
}

func TestPluginDriverSessionClosed_InvalidatesRunAndNotifiesOwner(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "session-close",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("session-close", "snipe-plugin", "run-close") {
		t.Fatal("failed to begin test plugin run")
	}
	d.ptyBackend = &fakeSpawnBackend{}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		if request.Method != "driver.session_closed" {
			t.Errorf("method=%q, want driver.session_closed", request.Method)
			return
		}
		var params pluginDriverSessionClosedParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode session_closed params: %v", err)
			return
		}
		if params.SessionID != "session-close" || params.RunID != "run-close" || params.Reason != "killed" {
			t.Errorf("session_closed params=%+v, want killed run-close", params)
		}
		respondPluginRequest(t, client, request, pluginDriverSessionClosedResult{OK: true})
	}()

	ws := &wsClient{send: make(chan outboundMessage, 1), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleKillSession(ws, &protocol.KillSessionMessage{ID: "session-close"})
	<-requestDone
	if d.store.ApplyAgentDriverState("session-close", "run-close", 1, protocol.StateIdle) {
		t.Fatal("report from closed run was accepted")
	}
}

func TestPluginDriverSessionClosed_UsesRecordedOwnerAfterRegistrationChanges(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	owner, ownerDone := startPluginPipe(t, d, "owner-plugin", nil)
	defer func() {
		_ = owner.Close()
		<-ownerDone
	}()
	registerTestPluginDriver(t, owner, "snipe", map[string]bool{"state_reporting": true})
	replacement, replacementDone := startPluginPipe(t, d, "replacement-plugin", nil)
	defer func() {
		_ = replacement.Close()
		<-replacementDone
	}()

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "owner-close",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("owner-close", "owner-plugin", "run-owned") {
		t.Fatal("failed to begin owner plugin run")
	}

	d.plugins.mu.Lock()
	d.plugins.drivers["snipe"] = pluginDriverRegistration{PluginName: "replacement-plugin", Agent: "snipe"}
	d.plugins.mu.Unlock()

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, owner)
		if request.Method != "driver.session_closed" {
			t.Errorf("method=%q, want driver.session_closed delivered to recorded owner", request.Method)
			return
		}
		respondPluginRequest(t, owner, request, pluginDriverSessionClosedResult{OK: true})
	}()

	d.closePluginDriverSession("owner-close", "exited", nil, "")
	<-requestDone
}

func TestPluginDriverSessionClosed_FailedKillKeepsRunActive(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "failed-kill",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("failed-kill", "snipe-plugin", "run-live") {
		t.Fatal("failed to begin test plugin run")
	}
	d.ptyBackend = &fakeSpawnBackend{killErr: errors.New("kill failed")}

	ws := &wsClient{send: make(chan outboundMessage, 1), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleKillSession(ws, &protocol.KillSessionMessage{ID: "failed-kill"})
	sendPluginMethod(t, client, 21, "session.report_state", pluginReportStateParams{
		SessionID: "failed-kill",
		RunID:     "run-live",
		Seq:       1,
		State:     protocol.StatePendingApproval,
	})
	if got := d.store.Get("failed-kill").State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("state=%q after failed kill report, want pending_approval", got)
	}
}

func registerTestPluginDriver(t *testing.T, conn net.Conn, agent string, capabilities map[string]bool) {
	t.Helper()
	sendPluginMethod(t, conn, 2, "driver.register", pluginDriverRegisterParams{
		Agent:        agent,
		Capabilities: capabilities,
	})
}

func sendPluginMethod(t *testing.T, conn net.Conn, id int, method string, params interface{}) {
	t.Helper()
	response := sendPluginMethodResponse(t, conn, id, method, params)
	if response.Error != nil {
		t.Fatalf("%s error=%#v", method, response.Error)
	}
}

func sendPluginMethodResponse(t *testing.T, conn net.Conn, id int, method string, params interface{}) jsonRPCMessage {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	if err := json.NewEncoder(conn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage([]byte(strconv.Itoa(id))),
		Method:  method,
		Params:  payload,
	}); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
	return decodeJSONRPCMessage(t, conn)
}

func respondPluginRequest(t *testing.T, conn net.Conn, request jsonRPCMessage, result interface{}) {
	t.Helper()
	if err := json.NewEncoder(conn).Encode(jsonRPCResult(request.ID, result)); err != nil {
		t.Fatalf("respond plugin request: %v", err)
	}
}
