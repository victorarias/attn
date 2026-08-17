package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/logging"
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
	attnBin := attnBinaryForE2ETest(t, tmpDir)

	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate ws port: %v", err)
	}
	socketPath := filepath.Join(tmpDir, "attn.sock")
	pluginDir := filepath.Join(tmpDir, "plugins")
	fixtureCWD := filepath.Join(tmpDir, "driver-cwd")
	fixtureLog := filepath.Join(tmpDir, "driver-requests.jsonl")
	fixtureCloseLog := filepath.Join(tmpDir, "driver-close.jsonl")
	fixtureStderr := filepath.Join(tmpDir, "driver-stderr.log")
	fixtureReady := filepath.Join(tmpDir, "driver-ready")
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
	bunScript := "#!/bin/sh\nexec \"$ATTN_TEST_HELPER_BINARY\" -test.run '^TestPluginDriverFixtureProcess$' >\"$ATTN_DRIVER_FIXTURE_STDERR\" 2>&1\n"
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
	t.Setenv("ATTN_DRIVER_FIXTURE_STDERR", fixtureStderr)
	t.Setenv("ATTN_DRIVER_FIXTURE_READY", fixtureReady)
	t.Setenv("ATTN_DRIVER_FIXTURE_CWD", fixtureCWD)
	t.Setenv("ATTN_DRIVER_FIXTURE_STATE_TRIGGER", fixtureStateTrigger)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := NewForTesting(socketPath)
	d.pluginDir = pluginDir
	// Tests normally run the daemon mute. This one spans three processes and its
	// only observed failure is a close notification that never arrives, so keep
	// the daemon's own account of every exit and every dropped notification —
	// it is what the failure prints instead of a bare deadline.
	daemonLog := filepath.Join(tmpDir, "daemon.log")
	if logger, err := logging.New(daemonLog); err == nil {
		d.logger = logger
		t.Cleanup(func() { _ = logger.Close() })
	} else {
		t.Logf("daemon log unavailable: %v", err)
	}
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
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := os.Stat(fixtureReady)
		return err == nil
	}, "fixture plugin to observe driver.register acknowledgement")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ws, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", port), nil)
	cancel()
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// The hello is what unlocks initial_state; nothing arrives before it.
	sendWorkspaceClientHello(t, ws)
	_ = waitForDaemonWebSocketEvent(t, ws, 10*time.Second, func(event map[string]interface{}) bool {
		return asString(event["event"]) == protocol.EventInitialState
	})

	sessionID := "plugin-driver-e2e"
	workspaceID := "workspace-" + sessionID
	fixture := pluginFixtureSession{
		daemon:    d,
		sessionID: sessionID,
		closeLog:  fixtureCloseLog,
		stderr:    fixtureStderr,
		daemonLog: daemonLog,
	}
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

	assertPluginFixtureStateTransitions(t, spawnFixtureSession(t, ws, sessionID, workspaceID, tmpDir, true, "", "spotify-glm/zai-org/GLM-5.2-FP8", "max"))
	assertPluginFixtureReports(t, d, sessionID, "driver.spawn-native")
	attachAndAssertPluginPTY(t, ws, sessionID, "driver.spawn", fixtureCWD)
	triggerAndAssertPluginFixtureStateTransitions(t, ws, sessionID, fixtureStateTrigger)

	records := waitForPluginFixtureRecords(t, fixtureLog, 1)
	if records[0].Method != "driver.spawn" || !records[0].Params.Yolo || records[0].Params.Model != "spotify-glm/zai-org/GLM-5.2-FP8" || records[0].Params.Effort != "max" {
		t.Fatalf("first plugin request=%+v, want yolo driver.spawn", records[0])
	}

	terminatePluginFixturePTY(t, d, sessionID)
	firstClose := waitForPluginFixtureCloseRecords(t, fixture, 1)[0]
	if firstClose.Params.RunID != records[0].Params.RunID || firstClose.Params.Reason != "exited" {
		t.Fatalf("first close=%+v, want exited notification for spawned run %q", firstClose.Params, records[0].Params.RunID)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		session := d.store.Get(sessionID)
		return session != nil && session.State == protocol.SessionStateIdle
	}, "initial PTY exit to settle before resume")
	assertPluginFixtureStateTransitions(t, spawnFixtureSession(t, ws, sessionID, workspaceID, tmpDir, false, sessionID, "spotify-glm/zai-org/GLM-5.2-FP8", "max"))
	assertPluginFixtureReports(t, d, sessionID, "driver.resume-native")
	attachAndAssertPluginPTY(t, ws, sessionID, "driver.resume", fixtureCWD)

	records = waitForPluginFixtureRecords(t, fixtureLog, 2)
	resume := records[1]
	if resume.Method != "driver.resume" || resume.Params.Model != "spotify-glm/zai-org/GLM-5.2-FP8" || resume.Params.Effort != "max" {
		t.Fatalf("second plugin method=%q, want driver.resume", resume.Method)
	}
	if string(resume.Params.Metadata) != `{"native_id":"driver.spawn-native"}` {
		t.Fatalf("resume metadata=%s, want previous plugin metadata", resume.Params.Metadata)
	}

	terminatePluginFixturePTY(t, d, sessionID)
	secondClose := waitForPluginFixtureCloseRecords(t, fixture, 2)[1]
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

func terminatePluginFixturePTY(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.ptyBackend.Kill(ctx, sessionID, syscall.SIGTERM); err != nil {
		t.Fatalf("terminate plugin fixture PTY: %v", err)
	}
}

func spawnFixtureSession(t *testing.T, ws *websocket.Conn, sessionID, workspaceID, cwd string, yolo bool, resumeID, model, effort string) []string {
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
		"model":        model,
		"effort":       effort,
	}
	if resumeID != "" {
		message["resume_session_id"] = resumeID
	}
	if err := writeWS(ws, message); err != nil {
		t.Fatalf("spawn fixture session: %v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
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
				details := ""
				if data, readErr := os.ReadFile(os.Getenv("ATTN_DRIVER_FIXTURE_STDERR")); readErr == nil && len(data) > 0 {
					details = "\nfixture stderr:\n" + string(data)
				}
				t.Fatalf("fixture spawn failed: %s%s", asString(event["error"]), details)
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

// pluginFixtureSession is everything the close wait needs to explain itself when
// the notification never lands.
type pluginFixtureSession struct {
	daemon    *Daemon
	sessionID string
	closeLog  string
	stderr    string
	daemonLog string
}

// waitForPluginFixtureCloseRecords waits for the fixture plugin to record
// `count` driver.session_closed notifications.
//
// The notification crosses three processes: the worker sees the PTY child exit,
// the daemon ends the driver run, the plugin gets driver.session_closed, and the
// fixture appends to the log. Measured on Linux with six copies of this test
// running in parallel under CPU load, that whole chain finishes in under 50ms
// (240 samples, worst 48.2ms). The deadline below is therefore a tripwire two
// orders of magnitude past any healthy run: reaching it means a link went quiet,
// not that the machine was slow. A CI flake reaching it told us only that, which
// is unfixable, so failing here prints who saw what — the daemon's account of
// the exit, the fixture's stderr (a fixture that died takes its connection with
// it, and the daemon then drops the notification as "owner disconnected"), and
// the session state the store ended up in.
func waitForPluginFixtureCloseRecords(t *testing.T, fixture pluginFixtureSession, count int) []pluginDriverCloseRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		records, ok := readPluginFixtureCloseRecords(fixture.closeLog, count)
		if ok {
			return records
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d fixture plugin close record(s)\n%s", count, fixture.diagnose())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func readPluginFixtureCloseRecords(path string, count int) ([]pluginDriverCloseRecord, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var records []pluginDriverCloseRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record pluginDriverCloseRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, false
		}
		records = append(records, record)
	}
	return records, len(records) >= count
}

func (f pluginFixtureSession) diagnose() string {
	var out strings.Builder
	state := "session missing from store"
	if session := f.daemon.store.Get(f.sessionID); session != nil {
		state = string(session.State)
	}
	run := f.daemon.store.GetAgentDriverRun(f.sessionID)
	fmt.Fprintf(&out, "session state=%s active_run=%q plugin=%q driver_registered=%t\n",
		state, run.RunID, run.PluginName, f.driverRegistered())
	records, _ := readPluginFixtureCloseRecords(f.closeLog, 0)
	fmt.Fprintf(&out, "close records recorded: %d\n", len(records))
	out.WriteString(pluginFixtureFileTail("fixture stderr", f.stderr, 40))
	out.WriteString(pluginFixtureFileTail("daemon log", f.daemonLog, 60))
	return out.String()
}

func (f pluginFixtureSession) driverRegistered() bool {
	_, ok := f.daemon.plugins.driver("fixture")
	return ok
}

func pluginFixtureFileTail(label, path string, lines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("--- %s: unreadable (%v)\n", label, err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return fmt.Sprintf("--- %s: empty\n", label)
	}
	all := strings.Split(trimmed, "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return fmt.Sprintf("--- %s (last %d lines):\n%s\n", label, len(all), strings.Join(all, "\n"))
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

	peer := newPluginFixturePeer(t, conn)
	sendPluginHello(t, conn, os.Getenv("ATTN_PLUGIN_NAME"))
	if response := peer.awaitResponse("1"); response.Error != nil {
		t.Fatalf("fixture hello error=%#v", response.Error)
	}
	peer.callOK("driver.register", pluginDriverRegisterParams{
		Agent: "fixture",
		Capabilities: map[string]bool{
			"resume":          true,
			"yolo":            true,
			"state_reporting": true,
			"model_pin":       true,
			"effort_pin":      true,
		},
	})
	if err := os.WriteFile(os.Getenv("ATTN_DRIVER_FIXTURE_READY"), []byte("ready"), 0o644); err != nil {
		t.Fatalf("write fixture ready marker: %v", err)
	}

	peer.serve()
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
