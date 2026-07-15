package daemon

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDaemon_HandleConnection_LegacySocketMessageStillWorks(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	heartbeatPayload, err := json.Marshal(protocol.HeartbeatMessage{
		Cmd: protocol.CmdHeartbeat,
		ID:  "legacy-session",
	})
	if err != nil {
		t.Fatalf("marshal legacy heartbeat: %v", err)
	}
	if _, err := clientConn.Write(heartbeatPayload); err != nil {
		t.Fatalf("write legacy heartbeat: %v", err)
	}

	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode legacy heartbeat response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("legacy heartbeat response ok=%v, want true", resp.Ok)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy connection did not finish")
	}
}

func TestDaemon_HandleConnection_PluginHelloRegistersLongLivedConnection(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "worktree-provider")
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHello(t, clientConn, "worktree-provider")
	helloResp := decodeJSONRPCMessage(t, clientConn)
	if helloResp.Error != nil {
		t.Fatalf("hello error = %#v, want nil", helloResp.Error)
	}
	var result pluginHelloResult
	if err := json.Unmarshal(helloResp.Result, &result); err != nil {
		t.Fatalf("decode hello result: %v", err)
	}
	if !result.OK {
		t.Fatal("hello result ok=false, want true")
	}
	if got := d.plugins.get("worktree-provider"); got == nil {
		t.Fatal("plugin registry missing worktree-provider after hello")
	}
	assertPluginConnectionBroadcast(t, readPluginUpdatedBroadcast(t, d), true)

	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin connection did not close after client disconnect")
	}
	assertPluginConnectionBroadcast(t, readPluginUpdatedBroadcast(t, d), false)
}

func TestDaemon_HandleConnection_PluginHelloCanArriveAcrossWrites(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	payload, err := json.Marshal(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "hello",
		Params: mustMarshalPluginHelloParams(t, pluginHelloParams{
			Name:           "split-provider",
			Version:        "0.1.0",
			AttnAPIVersion: pluginAPIVersion,
			Generation:     1,
		}),
	})
	if err != nil {
		t.Fatalf("marshal split hello: %v", err)
	}

	splitAt := len(payload) / 2
	if _, err := clientConn.Write(payload[:splitAt]); err != nil {
		t.Fatalf("write first hello fragment: %v", err)
	}
	if _, err := clientConn.Write(payload[splitAt:]); err != nil {
		t.Fatalf("write second hello fragment: %v", err)
	}

	resp := decodeJSONRPCMessage(t, clientConn)
	if resp.Error != nil {
		t.Fatalf("split hello error=%#v, want nil", resp.Error)
	}
	if got := d.plugins.get("split-provider"); got == nil {
		t.Fatal("plugin registry missing split-provider after fragmented hello")
	}

	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fragmented plugin connection did not close after client disconnect")
	}
}

func TestParsePluginHelloRejectsOldAPIAndMissingGeneration(t *testing.T) {
	tests := []struct {
		name   string
		params pluginHelloParams
		want   string
	}{
		{
			name: "old api",
			params: pluginHelloParams{
				Name:           "old-provider",
				Version:        "0.1.0",
				AttnAPIVersion: pluginAPIVersion - 1,
				Generation:     1,
			},
			want: "unsupported attn_api_version",
		},
		{
			name: "missing generation",
			params: pluginHelloParams{
				Name:           "missing-generation",
				Version:        "0.1.0",
				AttnAPIVersion: pluginAPIVersion,
			},
			want: "hello params.generation is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal(jsonRPCMessage{
				JSONRPC: "2.0",
				ID:      json.RawMessage("1"),
				Method:  "hello",
				Params:  mustMarshalPluginHelloParams(t, tt.params),
			})
			if err != nil {
				t.Fatalf("marshal hello: %v", err)
			}
			_, _, matched, err := parsePluginHello(payload)
			if !matched || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("matched=%v error=%v, want %q", matched, err, tt.want)
			}
		})
	}
}

func TestDaemon_PluginConnectionGenerationDrivesDisconnectRecovery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newTestPluginSupervisor(clock, launcher)
	if err := d.pluginSupervisor.Ensure(pluginManifest{Name: "supervised"}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	initial, _ := d.pluginSupervisor.Snapshot("supervised")
	client, done := startPluginPipeGeneration(t, d, "supervised", nil, initial.Generation)
	connected, _ := d.pluginSupervisor.Snapshot("supervised")
	if connected.Phase != pluginPhaseConnected {
		t.Fatalf("phase=%q after hello, want connected", connected.Phase)
	}
	_ = client.Close()
	<-done

	clock.Advance(pluginDisconnectGrace)
	waitForSupervisor(t, func() bool {
		snapshot, _ := d.pluginSupervisor.Snapshot("supervised")
		return snapshot.Phase == pluginPhaseBackoff
	})
	clock.Advance(pluginRestartBackoff[0])
	waitForSupervisor(t, func() bool { return launcher.count() == 2 })
	restarted, _ := d.pluginSupervisor.Snapshot("supervised")
	if restarted.Generation == initial.Generation {
		t.Fatal("restart did not advance generation")
	}

	serverConn, staleConn := net.Pipe()
	staleDone := make(chan struct{})
	go func() {
		defer close(staleDone)
		d.handleConnection(serverConn)
	}()
	sendPluginHelloWithGeneration(t, staleConn, "supervised", nil, initial.Generation)
	if response := decodeJSONRPCMessage(t, staleConn); response.Error == nil {
		t.Fatal("stale generation hello succeeded")
	}
	_ = staleConn.Close()
	<-staleDone

	current, currentDone := startPluginPipeGeneration(t, d, "supervised", nil, restarted.Generation)
	d.pluginSupervisor.Stop("supervised", pluginStopRemove)
	_ = current.Close()
	<-currentDone
}

func TestDaemon_HandleConnection_PluginHelloRegistersSurfaces(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHelloWithSurfaces(t, clientConn, "hello-provider", []string{"worktree.create"})
	if resp := decodeJSONRPCMessage(t, clientConn); resp.Error != nil {
		t.Fatalf("provider hello error=%#v, want nil", resp.Error)
	}

	handlers := d.plugins.handlersForSurface("worktree.create")
	if len(handlers) != 1 || handlers[0].PluginName != "hello-provider" {
		t.Fatalf("worktree.create handlers=%v, want hello-provider only", handlers)
	}

	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("provider plugin connection did not close after client disconnect")
	}
}

func TestPluginRegistry_OrdersHandlersByUserPriority(t *testing.T) {
	registry := newPluginRegistry()
	alpha := &pluginConnection{name: "alpha"}
	beta := &pluginConnection{name: "beta"}
	gamma := &pluginConnection{name: "gamma"}

	for _, plugin := range []*pluginConnection{alpha, beta, gamma} {
		if err := registry.register(plugin); err != nil {
			t.Fatalf("register %s: %v", plugin.name, err)
		}
		if err := registry.registerSurfaces(plugin, []string{"worktree.create"}); err != nil {
			t.Fatalf("register %s surface: %v", plugin.name, err)
		}
	}

	registry.setPriorities(map[string]int{
		"beta":  50,
		"alpha": 20,
	})

	handlers := registry.handlersForSurface("worktree.create")
	got := []string{handlers[0].PluginName, handlers[1].PluginName, handlers[2].PluginName}
	want := []string{"beta", "alpha", "gamma"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("handler order=%v, want %v", got, want)
		}
	}
}

func TestDaemon_HandleConnection_PluginHelloRejectsUnknownSurface(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHelloWithSurfaces(t, clientConn, "typo-provider", []string{"worktree.cretae"})
	resp := decodeJSONRPCMessage(t, clientConn)
	if resp.Error == nil {
		t.Fatal("unknown surface hello error=nil, want rejection")
	}
	if got := d.plugins.get("typo-provider"); got != nil {
		t.Fatalf("typo provider registered unexpectedly: %#v", got)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("unknown surface connection did not close")
	}
}

func TestDaemon_CallPlugin_RoundTripsRequestAndResponse(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHello(t, clientConn, "probe-plugin")
	helloResp := decodeJSONRPCMessage(t, clientConn)
	if helloResp.Error != nil {
		t.Fatalf("hello error = %#v, want nil", helloResp.Error)
	}

	type probeResult struct {
		Accepted bool `json:"accepted"`
	}
	resultCh := make(chan error, 1)
	var result probeResult
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resultCh <- d.callPlugin(ctx, "probe-plugin", "transport.probe", map[string]string{"kind": "roundtrip"}, &result)
	}()

	request := decodeJSONRPCMessage(t, clientConn)
	if request.Method != "transport.probe" {
		t.Fatalf("plugin request method=%q, want transport.probe", request.Method)
	}
	var params map[string]string
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("decode plugin request params: %v", err)
	}
	if got := params["kind"]; got != "roundtrip" {
		t.Fatalf("plugin request params.kind=%q, want roundtrip", got)
	}

	responseResult, err := json.Marshal(probeResult{Accepted: true})
	if err != nil {
		t.Fatalf("marshal plugin response result: %v", err)
	}
	if err := json.NewEncoder(clientConn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  responseResult,
	}); err != nil {
		t.Fatalf("encode plugin response: %v", err)
	}

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("callPlugin error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callPlugin did not complete")
	}
	if !result.Accepted {
		t.Fatal("callPlugin result accepted=false, want true")
	}

	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin connection did not close after round trip")
	}
}

func TestDaemon_Surfaces_OrderHandlersAndCleanUpOnDisconnect(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	lowClient, lowDone := startPluginPipe(t, d, "alpha-provider", []string{"worktree.create", "worktree.delete"})
	defer lowClient.Close()
	highClient, highDone := startPluginPipe(t, d, "zeta-provider", []string{"worktree.create"})
	defer highClient.Close()

	handlers := d.plugins.handlersForSurface("worktree.create")
	if len(handlers) != 2 {
		t.Fatalf("worktree.create handler count=%d, want 2", len(handlers))
	}
	if handlers[0].PluginName != "alpha-provider" || handlers[1].PluginName != "zeta-provider" {
		t.Fatalf("worktree.create handler order=%v, want alpha-provider then zeta-provider", handlers)
	}
	deleteHandlers := d.plugins.handlersForSurface("worktree.delete")
	if len(deleteHandlers) != 1 || deleteHandlers[0].PluginName != "alpha-provider" {
		t.Fatalf("worktree.delete handlers=%v, want alpha-provider only", deleteHandlers)
	}

	_ = highClient.Close()
	select {
	case <-highDone:
	case <-time.After(2 * time.Second):
		t.Fatal("zeta provider connection did not close")
	}
	handlers = d.plugins.handlersForSurface("worktree.create")
	if len(handlers) != 1 || handlers[0].PluginName != "alpha-provider" {
		t.Fatalf("worktree.create handlers after disconnect=%v, want alpha-provider only", handlers)
	}

	_ = lowClient.Close()
	select {
	case <-lowDone:
	case <-time.After(2 * time.Second):
		t.Fatal("alpha provider connection did not close")
	}
}

func TestDaemon_PluginHealthCheckRecordsStatus(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "health-provider")

	client, done := startPluginPipe(t, d, "health-provider", []string{"worktree.create"})
	defer client.Close()

	plugin := d.plugins.get("health-provider")
	if plugin == nil {
		t.Fatal("plugin registry missing health-provider")
	}

	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		request := decodeJSONRPCMessage(t, client)
		if request.Method != pluginHealthMethod {
			t.Errorf("health method=%q, want %s", request.Method, pluginHealthMethod)
			return
		}
		result, err := json.Marshal(pluginHealthResult{OK: true})
		if err != nil {
			t.Errorf("marshal health result: %v", err)
			return
		}
		if err := json.NewEncoder(client).Encode(jsonRPCMessage{
			JSONRPC: "2.0",
			ID:      request.ID,
			Result:  result,
		}); err != nil {
			t.Errorf("encode health result: %v", err)
		}
	}()

	d.checkPluginHealth(plugin)
	select {
	case <-healthDone:
	case <-time.After(2 * time.Second):
		t.Fatal("health request did not complete")
	}

	plugins := d.pluginsUpdatedMessage().Plugins
	if len(plugins) != 1 {
		t.Fatalf("plugin count=%d, want 1", len(plugins))
	}
	if got := protocol.Deref(plugins[0].HealthStatus); got != "healthy" {
		t.Fatalf("health status=%q, want healthy", got)
	}
	if protocol.Deref(plugins[0].LastHealthAt) == "" {
		t.Fatal("last health timestamp empty")
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("health provider connection did not close")
	}
}

func startPluginPipe(t *testing.T, d *Daemon, name string, surfaces []string) (net.Conn, <-chan struct{}) {
	return startPluginPipeGeneration(t, d, name, surfaces, 1)
}

func startPluginPipeGeneration(t *testing.T, d *Daemon, name string, surfaces []string, generation uint64) (net.Conn, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHelloWithGeneration(t, clientConn, name, surfaces, generation)
	helloResp := decodeJSONRPCMessage(t, clientConn)
	if helloResp.Error != nil {
		t.Fatalf("hello error = %#v, want nil", helloResp.Error)
	}
	return clientConn, done
}

func sendPluginHello(t *testing.T, conn net.Conn, name string) {
	sendPluginHelloWithSurfaces(t, conn, name, nil)
}

func sendPluginHelloWithSurfaces(t *testing.T, conn net.Conn, name string, surfaces []string) {
	sendPluginHelloWithGeneration(t, conn, name, surfaces, 1)
}

func sendPluginHelloWithGeneration(t *testing.T, conn net.Conn, name string, surfaces []string, generation uint64) {
	t.Helper()
	params, err := json.Marshal(pluginHelloParams{
		Name:           name,
		Version:        "0.1.0",
		AttnAPIVersion: pluginAPIVersion,
		Generation:     generation,
		Surfaces:       surfaces,
	})
	if err != nil {
		t.Fatalf("marshal hello params: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "hello",
		Params:  params,
	}); err != nil {
		t.Fatalf("encode hello: %v", err)
	}
}

func decodeJSONRPCMessage(t *testing.T, conn net.Conn) jsonRPCMessage {
	t.Helper()
	var msg jsonRPCMessage
	if err := json.NewDecoder(conn).Decode(&msg); err != nil {
		t.Fatalf("decode JSON-RPC message: %v", err)
	}
	return msg
}

func mustMarshalPluginHelloParams(t *testing.T, params pluginHelloParams) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal hello params: %v", err)
	}
	return payload
}

func readPluginUpdatedBroadcast(t *testing.T, d *Daemon) map[string]interface{} {
	t.Helper()
	select {
	case outbound := <-d.wsHub.broadcast:
		var event map[string]interface{}
		if err := json.Unmarshal(outbound.payload, &event); err != nil {
			t.Fatalf("decode plugin broadcast: %v", err)
		}
		if event["event"] != protocol.EventPluginsUpdated {
			t.Fatalf("plugin broadcast event=%v, want %q", event["event"], protocol.EventPluginsUpdated)
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for plugins_updated broadcast")
		return nil
	}
}

func assertPluginConnectionBroadcast(t *testing.T, event map[string]interface{}, connected bool) {
	t.Helper()
	plugins, ok := event["plugins"].([]interface{})
	if !ok || len(plugins) != 1 {
		t.Fatalf("plugins=%v, want one plugin", event["plugins"])
	}
	info, ok := plugins[0].(map[string]interface{})
	if !ok {
		t.Fatalf("plugin info=%T, want object", plugins[0])
	}
	if got := info["connected"]; got != connected {
		t.Fatalf("connected=%v, want %v", got, connected)
	}
}
