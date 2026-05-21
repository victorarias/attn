package daemon

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
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

	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin connection did not close after client disconnect")
	}
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

func TestDaemon_HandleConnection_PluginHelloRegistersProviderSurfaces(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHelloWithOptions(t, clientConn, "hello-provider", nil, []string{"worktree.create"})
	if resp := decodeJSONRPCMessage(t, clientConn); resp.Error != nil {
		t.Fatalf("provider hello error=%#v, want nil", resp.Error)
	}

	providers := d.plugins.providersForSurface("worktree.create")
	if len(providers) != 1 || providers[0].PluginName != "hello-provider" {
		t.Fatalf("worktree.create providers=%v, want hello-provider only", providers)
	}

	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("provider plugin connection did not close after client disconnect")
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

func TestDaemon_ProviderSurfaces_OrderProvidersAndCleanUpOnDisconnect(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	lowClient, lowDone := startPluginPipe(t, d, "alpha-provider", []string{"worktree.create", "worktree.delete"})
	defer lowClient.Close()
	highClient, highDone := startPluginPipe(t, d, "zeta-provider", []string{"worktree.create"})
	defer highClient.Close()

	providers := d.plugins.providersForSurface("worktree.create")
	if len(providers) != 2 {
		t.Fatalf("worktree.create provider count=%d, want 2", len(providers))
	}
	if providers[0].PluginName != "alpha-provider" || providers[1].PluginName != "zeta-provider" {
		t.Fatalf("worktree.create provider order=%v, want alpha-provider then zeta-provider", providers)
	}
	deleteProviders := d.plugins.providersForSurface("worktree.delete")
	if len(deleteProviders) != 1 || deleteProviders[0].PluginName != "alpha-provider" {
		t.Fatalf("worktree.delete providers=%v, want alpha-provider only", deleteProviders)
	}

	_ = highClient.Close()
	select {
	case <-highDone:
	case <-time.After(2 * time.Second):
		t.Fatal("zeta provider connection did not close")
	}
	providers = d.plugins.providersForSurface("worktree.create")
	if len(providers) != 1 || providers[0].PluginName != "alpha-provider" {
		t.Fatalf("worktree.create providers after disconnect=%v, want alpha-provider only", providers)
	}

	_ = lowClient.Close()
	select {
	case <-lowDone:
	case <-time.After(2 * time.Second):
		t.Fatal("alpha provider connection did not close")
	}
}

func startPluginPipe(t *testing.T, d *Daemon, name string, providerSurfaces []string) (net.Conn, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHelloWithOptions(t, clientConn, name, nil, providerSurfaces)
	helloResp := decodeJSONRPCMessage(t, clientConn)
	if helloResp.Error != nil {
		t.Fatalf("hello error = %#v, want nil", helloResp.Error)
	}
	return clientConn, done
}

func sendPluginHello(t *testing.T, conn net.Conn, name string) {
	sendPluginHelloWithOptions(t, conn, name, nil, nil)
}

func sendPluginHelloWithOptions(t *testing.T, conn net.Conn, name string, roles, providerSurfaces []string) {
	t.Helper()
	params, err := json.Marshal(pluginHelloParams{
		Name:             name,
		Version:          "0.1.0",
		AttnAPIVersion:   pluginAPIVersion,
		Roles:            roles,
		ProviderSurfaces: providerSurfaces,
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
