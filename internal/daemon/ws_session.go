package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleClearSessionsWS() {
	d.logf("Clearing all sessions")
	d.clearAllSessions()
}

func (d *Daemon) handleClearWarningsWS() {
	d.logf("Clearing daemon warnings")
	d.clearWarnings()
}

func (d *Daemon) handleUnregisterWS(client *wsClient, msg *protocol.UnregisterMessage) {
	if closeErr := d.sessionCloseError(msg.ID); closeErr != nil {
		d.logf("refusing to unregister protected session %s: %v", msg.ID, closeErr)
		d.sendCommandError(client, protocol.CmdUnregister, closeErr.Error())
		return
	}
	d.logf("Unregistering session %s via WebSocket", msg.ID)
	endpointID := ""
	if d.hubManager != nil {
		if resolved, ok := d.hubManager.EndpointIDForSession(msg.ID); ok {
			endpointID = resolved
		}
	}
	teardown, err := d.prepareSessionTeardown(msg.ID)
	if err != nil {
		d.sendCommandError(client, protocol.CmdUnregister, err.Error())
		return
	}
	d.commitSessionUnregister(msg.ID, store.SessionClose{By: store.SessionClosedByUser})
	d.detachSession(client, msg.ID)
	if teardown != nil && teardown.session != nil {
		d.publishSessionUnregistered(teardown.session)
		d.dissociateSessionFromWorkspace(teardown.session.ID)
		d.removeWorkspaceLayoutPaneForSession(teardown.session.ID)
		d.publishFact(FactSessionTerminated, teardown.session.ID, nil)
	}
	if endpointID != "" {
		payload, err := json.Marshal(protocol.UnregisterMessage{
			Cmd: protocol.CmdUnregister,
			ID:  msg.ID,
		})
		if err != nil {
			d.logf("marshal remote unregister failed for %s: %v", msg.ID, err)
		} else if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, payload); err != nil {
			d.logf("remote unregister forward failed for %s on endpoint %s: %v", msg.ID, endpointID, err)
		}
	}
	if teardown != nil {
		d.terminateSessionAsync(msg.ID, syscall.SIGTERM, teardown)
	}
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

func (d *Daemon) handleRecentFilesWS(client *wsClient, msg *protocol.RecentFilesMessage) {
	limit := 20
	if msg.Limit != nil {
		limit = int(*msg.Limit)
	}
	d.sendToClient(client, &protocol.RecentFilesResultMessage{
		Event:     protocol.EventRecentFilesResult,
		Files:     d.store.GetRecentFiles(limit, strings.TrimSpace(protocol.Deref(msg.Root))),
		RequestID: strings.TrimSpace(protocol.Deref(msg.RequestID)),
		Success:   true,
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
		for _, sessionID := range d.liveRuntimeSessionIDs(context.Background()) {
			sessionIDs[sessionID] = struct{}{}
		}
	}

	d.coalesceSnapshots(func() {
		for sessionID := range sessionIDs {
			d.terminateSession(sessionID, syscall.SIGTERM)
		}
		d.store.ClearSessions()
		d.clearChiefOfStaffIfSession(d.chiefOfStaffSessionID())
		for sessionID := range sessionIDs {
			d.publishFact(FactSessionTerminated, sessionID, nil)
		}
	})
}
