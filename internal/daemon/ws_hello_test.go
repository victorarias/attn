package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// drainClientEvents reads what the daemon queued for a client without waiting:
// every send in these tests happens before the assertion, on this goroutine.
func drainClientEvents(t *testing.T, client *wsClient) []protocol.WebSocketEvent {
	t.Helper()
	var events []protocol.WebSocketEvent
	for {
		select {
		case msg, open := <-client.send:
			if !open {
				return events
			}
			var event protocol.WebSocketEvent
			if err := json.Unmarshal(msg.payload, &event); err != nil {
				t.Fatalf("unmarshal outbound message: %v", err)
			}
			events = append(events, event)
		default:
			return events
		}
	}
}

func eventNames(events []protocol.WebSocketEvent) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Event)
	}
	return names
}

// newHelloTestDaemon is a daemon complete enough to admit a client: a store to
// snapshot from and a hub to join. Nothing listens on the socket path.
func newHelloTestDaemon(t *testing.T, token string) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	d.clientToken = token
	return d
}

func TestClientHelloAcceptsTheDaemonsToken(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	client := newWorkspaceProtocolTestClient()
	d := newHelloTestDaemon(t, "the-token")

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:   "tauri-app",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:  protocol.Ptr("the-token"),
	})

	if !client.speaksWorkspaceProtocol() {
		t.Fatal("hello with the right token did not record the client's capabilities")
	}
	if got := d.wsHub.ClientCount(); got != 1 {
		t.Fatalf("hub holds %d clients after an accepted hello, want 1", got)
	}
	events := drainClientEvents(t, client)
	if len(events) != 1 || events[0].Event != protocol.EventInitialState {
		t.Fatalf("accepted hello sent %+v, want one initial_state", eventNames(events))
	}
}

// An unauthorized connection must not see the machine. initial_state carries
// every session, PR, workspace and ticket, so withholding it — and staying out
// of the hub that fans out everything after it — is most of what the token buys.
func TestRefusedClientNeverJoinsTheHubOrSeesState(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	client := newWorkspaceProtocolTestClient()
	d := newHelloTestDaemon(t, "the-token")

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:   "impostor",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:  protocol.Ptr("guessed"),
	})

	if got := d.wsHub.ClientCount(); got != 0 {
		t.Fatalf("hub holds %d clients after a refused hello, want 0", got)
	}
	for _, event := range drainClientEvents(t, client) {
		if event.Event == protocol.EventInitialState {
			t.Fatal("a refused client was handed initial_state")
		}
	}
	// Anything broadcast afterwards must miss it too. An admitted client is the
	// witness: the hub fans one message out to every client under a single lock,
	// so once the admitted one holds it the refused one has been passed over.
	go d.wsHub.run()
	admitted := newWorkspaceProtocolTestClient()
	d.handleClientHello(admitted, &protocol.ClientHelloMessage{
		ClientKind:   "tauri-app",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:  protocol.Ptr("the-token"),
	})
	d.wsHub.Broadcast(&protocol.WebSocketEvent{Event: protocol.EventSettingsUpdated})
	waitForClientEvent(t, admitted, protocol.EventSettingsUpdated)
	for _, event := range drainClientEvents(t, client) {
		if event.Event == protocol.EventSettingsUpdated {
			t.Fatal("a refused client received a broadcast")
		}
	}
}

// waitForClientEvent blocks on the client's queue until the named event arrives,
// which is the happens-before edge the caller needs — not a timeout.
func waitForClientEvent(t *testing.T, client *wsClient, name string) {
	t.Helper()
	for {
		select {
		case msg, open := <-client.send:
			if !open {
				t.Fatalf("client hung up before %s arrived", name)
			}
			var event protocol.WebSocketEvent
			if err := json.Unmarshal(msg.payload, &event); err != nil {
				t.Fatalf("unmarshal outbound message: %v", err)
			}
			if event.Event == name {
				return
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s never arrived", name)
		}
	}
}

// A port exposed with ATTN_WS_AUTH_TOKEN is gated at the HTTP layer, and a
// browser served from it cannot read a file on the daemon's disk. Clearing the
// bearer stands in for the client token — otherwise remote web would close.
func TestBearerAuthorizedClientNeedsNoClientToken(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	client := newWorkspaceProtocolTestClient()
	client.bearerAuthorized = true
	d := newHelloTestDaemon(t, "the-token")

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:   "remote-web",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
	})

	if !client.speaksWorkspaceProtocol() {
		t.Fatal("a client that cleared the operator bearer was refused")
	}
	if got := d.wsHub.ClientCount(); got != 1 {
		t.Fatalf("hub holds %d clients, want the bearer-authorized one", got)
	}
}

// A daemon built without Start has no token, and must refuse rather than match
// the client that also sent nothing.
func TestDaemonWithoutATokenAuthorizesNobody(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	client := newWorkspaceProtocolTestClient()
	d := newHelloTestDaemon(t, "")

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:   "tauri-app",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
	})

	if client.speaksWorkspaceProtocol() {
		t.Fatal("a tokenless daemon admitted a tokenless client")
	}
}

func TestClientHelloWithoutTheTokenIsRefusedAndSaysWhere(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	client := newWorkspaceProtocolTestClient()
	d := newHelloTestDaemon(t, "the-token")

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:   "impostor",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
	})

	if client.speaksWorkspaceProtocol() {
		t.Fatal("a refused client kept its capabilities; every later command would be accepted")
	}
	events := drainClientEvents(t, client)
	if len(events) != 1 {
		t.Fatalf("refusal sent %d events, want exactly one", len(events))
	}
	refusal := events[0]
	if refusal.Event != protocol.EventCommandError {
		t.Fatalf("event = %q, want %q", refusal.Event, protocol.EventCommandError)
	}
	if got := protocol.Deref(refusal.ErrorCode); got != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("error_code = %q, want %q", got, protocol.ErrorCodeUnauthorizedClient)
	}
	// The message is the whole fix: an agent that reads it knows which file to
	// read next, which "unauthorized" alone never says.
	message := protocol.Deref(refusal.Error)
	if !strings.Contains(message, config.ClientTokenPath()) {
		t.Fatalf("refusal %q does not name the token path %q", message, config.ClientTokenPath())
	}
	code, reason := client.closeStatus()
	if reason != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("close reason = %q, want %q", reason, protocol.ErrorCodeUnauthorizedClient)
	}
	if code == 0 {
		t.Fatal("connection was left open after a refused hello")
	}
}

func TestClientHelloWithAnotherProfilesTokenIsRefused(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	client := newWorkspaceProtocolTestClient()
	d := newHelloTestDaemon(t, "this-profiles-token")

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:   "tauri-app",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:  protocol.Ptr("another-profiles-token"),
	})

	if client.speaksWorkspaceProtocol() {
		t.Fatal("a neighbouring profile's client was let onto this daemon")
	}
}

// dialWhenListening retries until the connection is made, which is the signal itself:
// Start() binds the unix socket before the WebSocket port, so waitForSocket
// returning does not mean the port answers yet.
func dialWhenListening(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	for {
		conn, _, err := websocket.Dial(ctx, url, nil)
		if err == nil {
			return conn
		}
		if ctx.Err() != nil {
			t.Fatalf("websocket dial %s: %v", url, err)
		}
	}
}

// readCloseReason drains whatever is still in flight and reports why the daemon
// hung up.
func readCloseReason(t *testing.T, ctx context.Context, conn *websocket.Conn) string {
	t.Helper()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			var closeErr websocket.CloseError
			if !errors.As(err, &closeErr) {
				t.Fatalf("read until close: %v", err)
			}
			return closeErr.Reason
		}
	}
}

// The wire receipt: a daemon that actually started refuses an anonymous hello
// over a real WebSocket and accepts the token it minted into its data root.
func TestDaemonWebSocketRequiresTheClientToken(t *testing.T) {
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", strconv.Itoa(port))
	// Unset so Start() mints into the daemon's own data root, which is what
	// production does and what the refusal message points at.
	t.Setenv("ATTN_CLIENT_TOKEN", "")
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	t.Setenv("ATTN_DATA_DIR", tmpDir)

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)

	if len(d.clientToken) != 64 {
		t.Fatalf("daemon client token = %q, want the 64 hex chars Start() mints", d.clientToken)
	}
	if got := config.ClientToken(); got != d.clientToken {
		t.Fatalf("token a client would read = %q, daemon holds %q", got, d.clientToken)
	}

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	refused := dialWhenListening(t, ctx, wsURL)
	defer refused.Close(websocket.StatusNormalClosure, "")
	if err := writeWS(refused, map[string]interface{}{
		"cmd":          protocol.CmdClientHello,
		"client_kind":  "impostor",
		"version":      "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{protocol.CapabilityWorkspaceSessions},
	}); err != nil {
		t.Fatalf("send tokenless hello: %v", err)
	}
	// Pipelined behind the hello, the way a real client sends: it does not wait
	// for a reply it has no reason to expect. This command must not be answered
	// — the gate that refuses an unidentified client hangs up on the connection
	// itself, which would destroy the refusal still queued behind it.
	if err := writeWS(refused, map[string]interface{}{"cmd": protocol.CmdGetSettings}); err != nil {
		t.Fatalf("send pipelined get_settings: %v", err)
	}
	event := waitForDaemonWebSocketEvent(t, refused, 10*time.Second, func(evt map[string]interface{}) bool {
		return asString(evt["event"]) == protocol.EventCommandError
	})
	if got := asString(event["error_code"]); got != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("error_code = %q, want %q", got, protocol.ErrorCodeUnauthorizedClient)
	}
	if message := asString(event["error"]); !strings.Contains(message, filepath.Join(tmpDir, config.ClientTokenFile)) {
		t.Fatalf("refusal %q does not name the token file under %q", message, tmpDir)
	}
	if reason := readCloseReason(t, ctx, refused); reason != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("closed with reason %q, want %q", reason, protocol.ErrorCodeUnauthorizedClient)
	}

	accepted := dialWhenListening(t, ctx, wsURL)
	defer accepted.Close(websocket.StatusNormalClosure, "")
	sendWorkspaceClientHello(t, accepted)
	if err := writeWS(accepted, map[string]interface{}{"cmd": protocol.CmdGetSettings}); err != nil {
		t.Fatalf("send get_settings: %v", err)
	}
	waitForDaemonWebSocketEvent(t, accepted, 10*time.Second, func(evt map[string]interface{}) bool {
		return asString(evt["event"]) == protocol.EventSettingsUpdated
	})
}
