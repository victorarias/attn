package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

type pluginDriverFixtureRecord struct {
	Method string                  `json:"method"`
	Params pluginDriverSpawnParams `json:"params"`
}

type pluginDriverCloseRecord struct {
	Params pluginDriverSessionClosedParams `json:"params"`
}

func TestPluginDriverEndToEnd_InstalledProcessLaunchReportAndResumeThroughWorkerPTY(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping plugin process and worker PTY end-to-end test in short mode")
	}

	tmpDir := shortTempDir(t)
	repoRoot := findRepoRootForTest(t)
	attnBin := filepath.Join(tmpDir, "attn")
	build := exec.Command("go", "build", "-o", attnBin, "./cmd/attn")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build attn test binary: %v\n%s", err, string(output))
	}

	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate ws port: %v", err)
	}
	socketPath := filepath.Join(tmpDir, "attn.sock")
	pluginDir := filepath.Join(tmpDir, "plugins")
	fixtureCWD := filepath.Join(tmpDir, "driver-cwd")
	fixtureLog := filepath.Join(tmpDir, "driver-requests.jsonl")
	fixtureCloseLog := filepath.Join(tmpDir, "driver-close.jsonl")
	fixtureStateTrigger := filepath.Join(tmpDir, "driver-live-state.trigger")
	if err := os.MkdirAll(fixtureCWD, 0o755); err != nil {
		t.Fatalf("mkdir fixture cwd: %v", err)
	}
	writeTestPluginManifest(t, pluginDir, "fixture-driver")

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bun dir: %v", err)
	}
	bunPath := filepath.Join(binDir, "bun")
	bunScript := "#!/bin/sh\nexec \"$ATTN_TEST_HELPER_BINARY\" -test.run '^TestPluginDriverFixtureProcess$'\n"
	if err := os.WriteFile(bunPath, []byte(bunScript), 0o755); err != nil {
		t.Fatalf("write fake bun: %v", err)
	}

	t.Setenv("ATTN_WS_PORT", fmt.Sprintf("%d", port))
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", attnBin)
	t.Setenv("ATTN_PLUGIN_DRIVER_HELPER", "1")
	t.Setenv("ATTN_TEST_HELPER_BINARY", os.Args[0])
	t.Setenv("ATTN_DRIVER_FIXTURE_LOG", fixtureLog)
	t.Setenv("ATTN_DRIVER_FIXTURE_CLOSE_LOG", fixtureCloseLog)
	t.Setenv("ATTN_DRIVER_FIXTURE_CWD", fixtureCWD)
	t.Setenv("ATTN_DRIVER_FIXTURE_STATE_TRIGGER", fixtureStateTrigger)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := NewForTesting(socketPath)
	d.pluginDir = pluginDir
	d.loginShellEnv = []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("daemon exited: %v", err)
		}
	}()
	defer d.Stop()

	waitForSocket(t, socketPath, 5*time.Second)
	waitForCondition(t, 5*time.Second, func() bool {
		_, ok := d.plugins.driver("fixture")
		return ok
	}, "installed plugin to register fixture driver")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ws, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", port), nil)
	cancel()
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	_ = waitForDaemonWebSocketEvent(t, ws, 10*time.Second, func(event map[string]interface{}) bool {
		return asString(event["event"]) == protocol.EventInitialState
	})

	sessionID := "plugin-driver-e2e"
	workspaceID := "workspace-" + sessionID
	if err := writeWS(ws, map[string]interface{}{
		"cmd":       protocol.CmdRegisterWorkspace,
		"id":        workspaceID,
		"title":     "fixture",
		"directory": tmpDir,
	}); err != nil {
		t.Fatalf("register workspace: %v", err)
	}
	_ = waitForDaemonWebSocketEvent(t, ws, 5*time.Second, func(event map[string]interface{}) bool {
		return asString(event["event"]) == protocol.EventWorkspaceRegistered
	})

	assertPluginFixtureStateTransitions(t, spawnFixtureSession(t, ws, sessionID, workspaceID, tmpDir, true, ""))
	assertPluginFixtureReports(t, d, sessionID, "driver.spawn-native")
	attachAndAssertPluginPTY(t, ws, sessionID, "driver.spawn", fixtureCWD)
	triggerAndAssertPluginFixtureStateTransitions(t, ws, sessionID, fixtureStateTrigger)

	records := waitForPluginFixtureRecords(t, fixtureLog, 1)
	if records[0].Method != "driver.spawn" || !records[0].Params.Yolo {
		t.Fatalf("first plugin request=%+v, want yolo driver.spawn", records[0])
	}

	removePTYSession(t, d, sessionID)
	firstClose := waitForPluginFixtureCloseRecords(t, fixtureCloseLog, 1)[0]
	if firstClose.Params.RunID != records[0].Params.RunID || firstClose.Params.Reason != "exited" {
		t.Fatalf("first close=%+v, want exited notification for spawned run %q", firstClose.Params, records[0].Params.RunID)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		session := d.store.Get(sessionID)
		return session != nil && session.State == protocol.SessionStateIdle
	}, "initial PTY exit to settle before resume")
	assertPluginFixtureStateTransitions(t, spawnFixtureSession(t, ws, sessionID, workspaceID, tmpDir, false, sessionID))
	assertPluginFixtureReports(t, d, sessionID, "driver.resume-native")
	attachAndAssertPluginPTY(t, ws, sessionID, "driver.resume", fixtureCWD)

	records = waitForPluginFixtureRecords(t, fixtureLog, 2)
	resume := records[1]
	if resume.Method != "driver.resume" {
		t.Fatalf("second plugin method=%q, want driver.resume", resume.Method)
	}
	if string(resume.Params.Metadata) != `{"native_id":"driver.spawn-native"}` {
		t.Fatalf("resume metadata=%s, want previous plugin metadata", resume.Params.Metadata)
	}

	removePTYSession(t, d, sessionID)
	secondClose := waitForPluginFixtureCloseRecords(t, fixtureCloseLog, 2)[1]
	if secondClose.Params.RunID != resume.Params.RunID || secondClose.Params.Reason != "exited" {
		t.Fatalf("second close=%+v, want exited notification for resumed run %q", secondClose.Params, resume.Params.RunID)
	}
	d.stopInstalledPlugin("fixture-driver")
	waitForCondition(t, 5*time.Second, func() bool {
		_, ok := d.plugins.driver("fixture")
		return !ok
	}, "plugin disconnect to remove registered driver")
	if _, ok := d.settingsWithAgentAvailability()["fixture_available"]; ok {
		t.Fatal("fixture_available remains advertised after plugin disconnect")
	}
}

func spawnFixtureSession(t *testing.T, ws *websocket.Conn, sessionID, workspaceID, cwd string, yolo bool, resumeID string) []string {
	t.Helper()
	message := map[string]interface{}{
		"cmd":          protocol.CmdSpawnSession,
		"id":           sessionID,
		"cwd":          cwd,
		"workspace_id": workspaceID,
		"agent":        "fixture",
		"cols":         80,
		"rows":         24,
		"yolo_mode":    yolo,
	}
	if resumeID != "" {
		message["resume_session_id"] = resumeID
	}
	if err := writeWS(ws, message); err != nil {
		t.Fatalf("spawn fixture session: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var states []string
	spawnSucceeded := false
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		_, payload, err := ws.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read fixture spawn events: %v", err)
		}
		var event map[string]interface{}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode fixture spawn event: %v", err)
		}
		if state, ok := pluginFixtureStateEvent(event, sessionID); ok {
			states = append(states, state)
		}
		if asString(event["event"]) == protocol.EventSpawnResult && asString(event["id"]) == sessionID {
			if !asBool(event["success"]) {
				t.Fatalf("fixture spawn failed: %s", asString(event["error"]))
			}
			spawnSucceeded = true
		}
		if spawnSucceeded && containsPluginFixtureStateTransitions(states) {
			return states
		}
	}
	t.Fatalf("timed out waiting for fixture spawn and state reports; spawn_succeeded=%t states=%v", spawnSucceeded, states)
	return nil
}

func triggerAndAssertPluginFixtureStateTransitions(t *testing.T, ws *websocket.Conn, sessionID, triggerPath string) {
	t.Helper()
	if err := os.WriteFile(triggerPath, []byte("report live state"), 0o644); err != nil {
		t.Fatalf("trigger fixture live state report: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var states []string
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		_, payload, err := ws.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read fixture live state events: %v", err)
		}
		var event map[string]interface{}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode fixture live state event: %v", err)
		}
		if state, ok := pluginFixtureStateEvent(event, sessionID); ok {
			states = append(states, state)
			if containsPluginFixtureStateTransitions(states) {
				return
			}
		}
	}
	t.Fatalf("live plugin state events=%v, want working followed by waiting_input", states)
}

func assertPluginFixtureStateTransitions(t *testing.T, states []string) {
	t.Helper()
	if !containsPluginFixtureStateTransitions(states) {
		t.Fatalf("plugin state events=%v, want working followed by waiting_input", states)
	}
}

func containsPluginFixtureStateTransitions(states []string) bool {
	working := false
	for _, state := range states {
		if state == protocol.StateWorking {
			working = true
		}
		if working && state == protocol.StateWaitingInput {
			return true
		}
	}
	return false
}

func pluginFixtureStateEvent(event map[string]interface{}, sessionID string) (string, bool) {
	if asString(event["event"]) != protocol.EventSessionStateChanged {
		return "", false
	}
	session, ok := event["session"].(map[string]interface{})
	if !ok || asString(session["id"]) != sessionID {
		return "", false
	}
	return asString(session["state"]), true
}

func assertPluginFixtureReports(t *testing.T, d *Daemon, sessionID, nativeID string) {
	t.Helper()
	waitForCondition(t, 5*time.Second, func() bool {
		session := d.store.Get(sessionID)
		return session != nil &&
			session.Agent == "fixture" &&
			session.State == protocol.SessionStateWaitingInput &&
			d.store.GetAgentMetadata(sessionID) == `{"native_id":"`+nativeID+`"}`
	}, "plugin state, stop verdict, and metadata reports")
}

func attachAndAssertPluginPTY(t *testing.T, ws *websocket.Conn, sessionID, method, cwd string) {
	t.Helper()
	if err := writeWS(ws, map[string]interface{}{
		"cmd": protocol.CmdAttachSession,
		"id":  sessionID,
	}); err != nil {
		t.Fatalf("attach fixture session: %v", err)
	}
	attach := waitForDaemonWebSocketEvent(t, ws, 10*time.Second, func(event map[string]interface{}) bool {
		return asString(event["event"]) == protocol.EventAttachResult && asString(event["id"]) == sessionID
	})
	if !asBool(attach["success"]) {
		t.Fatalf("fixture attach failed: %s", asString(attach["error"]))
	}
	if err := writeWS(ws, map[string]interface{}{
		"cmd":  protocol.CmdPtyInput,
		"id":   sessionID,
		"data": "ping\n",
	}); err != nil {
		t.Fatalf("write fixture input: %v", err)
	}
	marker := fmt.Sprintf("PLUGIN_RUN method=%s cwd=%s input=ping", method, canonicalPathDaemon(cwd))
	output := waitForPtyOutputContaining(t, ws, sessionID, "PLUGIN_RUN", 10*time.Second)
	if !strings.Contains(output, marker) {
		t.Fatalf("pty output %q does not contain %q", output, marker)
	}
}

func waitForPluginFixtureRecords(t *testing.T, path string, count int) []pluginDriverFixtureRecord {
	t.Helper()
	var records []pluginDriverFixtureRecord
	waitForCondition(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		records = nil
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var record pluginDriverFixtureRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				return false
			}
			records = append(records, record)
		}
		return len(records) >= count
	}, "fixture plugin request log")
	return records
}

func waitForPluginFixtureCloseRecords(t *testing.T, path string, count int) []pluginDriverCloseRecord {
	t.Helper()
	var records []pluginDriverCloseRecord
	waitForCondition(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		records = nil
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var record pluginDriverCloseRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				return false
			}
			records = append(records, record)
		}
		return len(records) >= count
	}, "fixture plugin close log")
	return records
}

func TestPluginDriverFixtureProcess(t *testing.T) {
	if os.Getenv("ATTN_PLUGIN_DRIVER_HELPER") != "1" {
		return
	}

	conn, err := dialPluginHelper(os.Getenv("ATTN_SOCKET_PATH"), 5*time.Second)
	if err != nil {
		t.Fatalf("dial daemon socket: %v", err)
	}
	defer conn.Close()

	sendPluginHello(t, conn, os.Getenv("ATTN_PLUGIN_NAME"))
	if response := decodeJSONRPCMessage(t, conn); response.Error != nil {
		t.Fatalf("fixture hello error=%#v", response.Error)
	}
	registerTestPluginDriver(t, conn, "fixture", map[string]bool{
		"resume":          true,
		"yolo":            true,
		"state_reporting": true,
	})

	for {
		request, err := decodePluginFixtureMessage(conn)
		if err != nil {
			return
		}
		if request.Method == "attn.health" {
			_ = json.NewEncoder(conn).Encode(jsonRPCResult(request.ID, map[string]bool{"ok": true}))
			continue
		}
		if request.Method == "driver.session_closed" {
			var params pluginDriverSessionClosedParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("decode fixture session close params: %v", err)
			}
			appendPluginFixtureCloseRecord(t, pluginDriverCloseRecord{Params: params})
			_ = json.NewEncoder(conn).Encode(jsonRPCResult(request.ID, pluginDriverSessionClosedResult{OK: true}))
			continue
		}
		if request.Method != "driver.spawn" && request.Method != "driver.resume" {
			continue
		}

		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Fatalf("decode fixture launch params: %v", err)
		}
		appendPluginFixtureRecord(t, pluginDriverFixtureRecord{Method: request.Method, Params: params})
		script := `IFS= read -r input; printf 'PLUGIN_RUN method=%s cwd=%s input=%s\n' "$ATTN_PLUGIN_FIXTURE_METHOD" "$PWD" "$input"; trap 'exit 0' TERM INT; while :; do sleep 1; done`
		respondPluginRequest(t, conn, request, pluginDriverSpawnResult{
			Argv: []string{"/bin/sh", "-c", script},
			Env:  map[string]string{"ATTN_PLUGIN_FIXTURE_METHOD": request.Method},
			CWD:  os.Getenv("ATTN_DRIVER_FIXTURE_CWD"),
		})
		sendPluginMethod(t, conn, 20, "session.report_state", pluginReportStateParams{
			SessionID: params.SessionID,
			RunID:     params.RunID,
			Seq:       1,
			State:     protocol.StateWorking,
		})
		sendPluginMethod(t, conn, 21, "session.report_metadata", pluginReportMetadataParams{
			SessionID: params.SessionID,
			RunID:     params.RunID,
			Seq:       2,
			Metadata:  json.RawMessage(`{"native_id":"` + request.Method + `-native"}`),
		})
		sendPluginMethod(t, conn, 22, "session.report_stop", pluginReportStopParams{
			SessionID: params.SessionID,
			RunID:     params.RunID,
			Seq:       3,
			Verdict:   protocol.StateWaitingInput,
		})
		if request.Method == "driver.spawn" {
			waitForPluginFixtureStateTrigger(t)
			sendPluginMethod(t, conn, 23, "session.report_state", pluginReportStateParams{
				SessionID: params.SessionID,
				RunID:     params.RunID,
				Seq:       4,
				State:     protocol.StateWorking,
			})
			sendPluginMethod(t, conn, 24, "session.report_stop", pluginReportStopParams{
				SessionID: params.SessionID,
				RunID:     params.RunID,
				Seq:       5,
				Verdict:   protocol.StateWaitingInput,
			})
		}
	}
}

func decodePluginFixtureMessage(conn net.Conn) (jsonRPCMessage, error) {
	var message jsonRPCMessage
	err := json.NewDecoder(conn).Decode(&message)
	return message, err
}

func appendPluginFixtureRecord(t *testing.T, record pluginDriverFixtureRecord) {
	t.Helper()
	file, err := os.OpenFile(os.Getenv("ATTN_DRIVER_FIXTURE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open fixture log: %v", err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		t.Fatalf("append fixture log: %v", err)
	}
}

func appendPluginFixtureCloseRecord(t *testing.T, record pluginDriverCloseRecord) {
	t.Helper()
	file, err := os.OpenFile(os.Getenv("ATTN_DRIVER_FIXTURE_CLOSE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open fixture close log: %v", err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		t.Fatalf("append fixture close log: %v", err)
	}
}

func waitForPluginFixtureStateTrigger(t *testing.T) {
	t.Helper()
	path := os.Getenv("ATTN_DRIVER_FIXTURE_STATE_TRIGGER")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for live-state trigger at %s", path)
}
