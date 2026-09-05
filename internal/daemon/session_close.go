package daemon

import (
	"context"
	"encoding/json"
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// What a close still owes once the ledger row is written: the remote daemon that
// also runs the session, and the kill.
type sessionCloseInFlight struct {
	teardown   *sessionTeardown
	endpointID string
}

// Records the close and fans it out to every surface. `client` is the WebSocket
// client that asked, when one did; an agent close passes nil.
func (d *Daemon) beginSessionClose(
	sessionID string, closed store.SessionClose, client *wsClient,
) (sessionCloseInFlight, error) {
	endpointID := ""
	if d.hubManager != nil {
		if resolved, ok := d.hubManager.EndpointIDForSession(sessionID); ok {
			endpointID = resolved
		}
	}
	teardown, err := d.prepareSessionTeardown(sessionID)
	if err != nil {
		return sessionCloseInFlight{}, err
	}
	d.commitSessionUnregister(sessionID, closed)
	if client != nil {
		d.detachSession(client, sessionID)
	}
	if teardown != nil && teardown.session != nil {
		d.publishSessionUnregistered(teardown.session)
		d.dissociateSessionFromWorkspace(teardown.session.ID)
		d.removeWorkspaceLayoutPaneForSession(teardown.session.ID)
		d.publishFact(FactSessionTerminated, teardown.session.ID, nil)
	}
	return sessionCloseInFlight{teardown: teardown, endpointID: endpointID}, nil
}

// finishSessionClose kills the runtime. Split from beginSessionClose so a caller
// can answer its requester before the session it is closing dies.
func (d *Daemon) finishSessionClose(sessionID string, closing sessionCloseInFlight) {
	if closing.endpointID != "" {
		payload, err := json.Marshal(protocol.UnregisterMessage{
			Cmd: protocol.CmdUnregister,
			ID:  sessionID,
		})
		if err != nil {
			d.logf("marshal remote unregister failed for %s: %v", sessionID, err)
		} else if err := d.hubManager.ForwardEndpointCommand(context.Background(), closing.endpointID, payload); err != nil {
			d.logf("remote unregister forward failed for %s on endpoint %s: %v", sessionID, closing.endpointID, err)
		}
	}
	if closing.teardown != nil {
		d.terminateSessionAsync(sessionID, syscall.SIGTERM, closing.teardown)
	}
}
