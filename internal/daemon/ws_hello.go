package daemon

import (
	"crypto/subtle"
	"fmt"
	"strings"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// handleClientHello records the client's identity and capabilities for the
// rest of the connection. Idempotent — a client that re-sends hello overwrites
// its prior identity.
//
// We deliberately don't reply: the hello is a fire-and-forget setup
// message. The next command the client sends is the real signal that
// the connection is alive and ready.
//
// The one exception is a client returning from an eviction. Its previous
// connection was hung up on and could not be told why over a socket that was
// already backed up, so the reason is handed to it here — once, and only if
// the daemon is still holding one for the client_id it just named.
//
// Unix-socket commands never reach here: they have their own dispatch in
// daemon.go, and file permissions on the socket already decide who may speak.
// The WebSocket port has no such gate, which is what client_token is for.
func (d *Daemon) handleClientHello(client *wsClient, msg *protocol.ClientHelloMessage) {
	if !d.authorizeClientHello(client, msg) {
		return
	}
	requiredToken := config.BrowserHostToken()
	providedToken := strings.TrimSpace(protocol.Deref(msg.BrowserHostToken))
	client.setBrowserHostAuthenticated(
		requiredToken != "" &&
			subtle.ConstantTimeCompare([]byte(requiredToken), []byte(providedToken)) == 1,
	)
	client.setIdentity(msg.ClientKind, msg.Version, msg.Capabilities)
	clientID := strings.TrimSpace(protocol.Deref(msg.ClientID))
	client.setClientID(clientID)
	client.updateReadLimit()
	d.logf(
		"client hello: kind=%q version=%q client_id=%q capabilities=%v",
		msg.ClientKind,
		msg.Version,
		clientID,
		msg.Capabilities,
	)
	d.admitClient(client)
	if record, ok := d.wsHub.takeEviction(clientID); ok {
		d.sendEvictionNotice(client, record)
	}
}

// admitClient lets an authorized connection into the hub and hands it the
// snapshot it has been waiting for.
//
// Registration happens here rather than at accept, and that is the read side of
// the client token: the hub is the only fan-out, so a connection that has not
// presented the token sees no broadcast and no initial_state — which is most of
// what there is to see. A client that helloes twice is admitted once.
func (d *Daemon) admitClient(client *wsClient) {
	client.admitted.Do(func() {
		d.wsHub.add(client)
		d.logf("WebSocket client connected (%d total)", d.wsHub.ClientCount())
		d.scheduleInitialState(client)
	})
}

// authorizeClientHello refuses a hello that does not carry this profile's
// client token. It runs before any identity is recorded, so a refused client
// also fails the workspace_sessions gate on everything it sends afterwards, and
// admitClient never runs for it.
//
// The refusal names the file it should have read: nobody reads our code, their
// agents read our errors, and "unauthorized" alone is unfixable.
func (d *Daemon) authorizeClientHello(client *wsClient, msg *protocol.ClientHelloMessage) bool {
	if client.bearerAuthorized {
		// Already proved itself at the HTTP layer with the operator's bearer,
		// which is how a deliberately exposed port is gated. Asking a browser
		// served from that port for a file on this disk would only close it.
		return true
	}
	// The d.clientToken != "" half matters: a daemon holding no token refuses
	// everyone rather than matching the client that also sent nothing.
	provided := strings.TrimSpace(protocol.Deref(msg.ClientToken))
	if d.clientToken != "" && subtle.ConstantTimeCompare([]byte(d.clientToken), []byte(provided)) == 1 {
		return true
	}
	reason := fmt.Sprintf(
		"client_hello refused: client_token does not match this daemon's. Read it from %s (owner-only) and send it as client_token; the daemon serving profile %q minted it.",
		config.ClientTokenPath(),
		config.Profile(),
	)
	if d.clientToken == "" {
		reason = "client_hello refused: this daemon minted no client token, so it can authorize nobody. It was started without Daemon.Start."
	}
	d.logf("rejecting client hello from kind=%q: client_token mismatch", msg.ClientKind)
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:     protocol.EventCommandError,
		Cmd:       protocol.Ptr(protocol.CmdClientHello),
		Success:   protocol.Ptr(false),
		Error:     protocol.Ptr(reason),
		ErrorCode: protocol.Ptr(protocol.ErrorCodeUnauthorizedClient),
	})
	// Close through the send channel, not the connection: the write pump drains
	// what is queued before it hangs up, which is what makes the refusal arrive
	// rather than race the close.
	client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, protocol.ErrorCodeUnauthorizedClient)
	return false
}
