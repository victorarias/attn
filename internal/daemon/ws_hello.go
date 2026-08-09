package daemon

import (
	"crypto/subtle"
	"strings"

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
func (d *Daemon) handleClientHello(client *wsClient, msg *protocol.ClientHelloMessage) {
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
	if record, ok := d.wsHub.takeEviction(clientID); ok {
		d.sendEvictionNotice(client, record)
	}
}
