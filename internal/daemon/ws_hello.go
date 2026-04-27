package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
)

// handleClientHello records the client's identity and capabilities so
// per-client behavior (gated via wsClient.HasCapability) can take effect
// for the rest of the connection. Idempotent — a client that re-sends
// hello overwrites its prior identity.
//
// We deliberately don't reply: the hello is a fire-and-forget setup
// message. The next command the client sends is the real signal that
// the connection is alive and ready.
func (d *Daemon) handleClientHello(client *wsClient, msg *protocol.ClientHelloMessage) {
	client.setIdentity(msg.ClientKind, msg.Version, msg.Capabilities)
	d.logf(
		"client hello: kind=%q version=%q capabilities=%v",
		msg.ClientKind,
		msg.Version,
		msg.Capabilities,
	)
}
