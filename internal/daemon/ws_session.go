package daemon

import (
	"context"
	"os"
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleClearSessionsWS() {
	d.logf("Clearing all sessions")
	d.clearAllSessions()
}

func (d *Daemon) handleClearWarningsWS() {
	d.logf("Clearing daemon warnings")
	d.clearWarnings()
}

func (d *Daemon) handleSessionVisualizedWS(msg *protocol.SessionVisualizedMessage) {
	d.handleSessionVisualized(msg.ID)
}

func (d *Daemon) handleUnregisterWS(client *wsClient, msg *protocol.UnregisterMessage) {
	d.logf("Unregistering session %s via WebSocket", msg.ID)
	d.detachSession(client, msg.ID)
	session := d.store.Get(msg.ID)
	d.terminateSession(msg.ID, syscall.SIGTERM)
	d.store.Remove(msg.ID)
	d.clearLongRunTracking(msg.ID)
	if session != nil {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionUnregistered,
			Session: d.sessionForBroadcast(session),
		})
	}
	d.broadcastSessionsUpdated()
}

func (d *Daemon) handleGetRecentLocationsWS(client *wsClient, msg *protocol.GetRecentLocationsMessage) {
	limit := 20
	if msg.Limit != nil {
		limit = int(*msg.Limit)
	}
	d.logf("Getting recent locations (limit=%d)", limit)
	locations := d.store.GetRecentLocations(limit)
	homePath, _ := os.UserHomeDir()
	d.sendToClient(client, &protocol.RecentLocationsResultMessage{
		Event:           protocol.EventRecentLocationsResult,
		RecentLocations: protocol.RecentLocationsToValues(locations),
		EndpointID:      msg.EndpointID,
		RequestID:       msg.RequestID,
		HomePath:        protocol.Ptr(homePath),
		Success:         true,
	})
}

func (d *Daemon) clearAllSessions() {
	sessionIDs := make(map[string]struct{})
	for _, session := range d.store.List("") {
		sessionIDs[session.ID] = struct{}{}
	}

	if d.ptyBackend != nil {
		recoverCtx, cancel := context.WithTimeout(context.Background(), deferredRecoveryRPCTimeout)
		report, err := d.ptyBackend.Recover(recoverCtx)
		cancel()
		if err != nil {
			d.logf("clear_sessions recovery scan failed: %v", err)
		} else if report.Recovered > 0 || report.Pruned > 0 || report.Missing > 0 || report.Failed > 0 {
			d.logf(
				"clear_sessions recovery summary: recovered=%d pruned=%d missing=%d failed=%d",
				report.Recovered,
				report.Pruned,
				report.Missing,
				report.Failed,
			)
		}
		for _, sessionID := range d.ptyBackend.SessionIDs(context.Background()) {
			sessionIDs[sessionID] = struct{}{}
		}
	}

	d.store.ClearSessions()
	for sessionID := range sessionIDs {
		d.terminateSession(sessionID, syscall.SIGTERM)
		d.clearLongRunTracking(sessionID)
	}
	d.broadcastSessionsUpdated()
}
