package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

const profileRoleChiefOfStaff = "chief_of_staff"

func (d *Daemon) chiefOfStaffSessionID() string {
	if d.store == nil {
		return ""
	}
	return strings.TrimSpace(d.store.GetProfileRole(profileRoleChiefOfStaff))
}

func (d *Daemon) decorateChiefOfStaffWithSessionID(session *protocol.Session, chiefOfStaffSessionID string) {
	if session == nil {
		return
	}
	if session.ID == chiefOfStaffSessionID {
		session.ChiefOfStaff = protocol.Ptr(true)
		return
	}
	session.ChiefOfStaff = nil
}

func (d *Daemon) sessionExists(sessionID string) bool {
	if d.store != nil && d.store.Get(sessionID) != nil {
		return true
	}
	return d.hubManager != nil && d.hubManager.RemoteSession(sessionID) != nil
}

func (d *Daemon) clearChiefOfStaffIfSession(sessionID string) {
	if d.store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if err := d.store.ClearProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
		d.logf("clear chief of staff role failed for session %s: %v", sessionID, err)
	}
}

func (d *Daemon) handleSetChiefOfStaff(client *wsClient, msg *protocol.SetChiefOfStaffMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		d.sendChiefOfStaffResult(client, sessionID, msg.ChiefOfStaff, "", fmt.Errorf("missing session_id"))
		return
	}

	previousSessionID := d.chiefOfStaffSessionID()
	if msg.ChiefOfStaff {
		if !d.sessionExists(sessionID) {
			d.sendChiefOfStaffResult(
				client,
				sessionID,
				true,
				previousSessionID,
				fmt.Errorf("session not found: %s", sessionID),
			)
			return
		}
		if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
			d.sendChiefOfStaffResult(client, sessionID, true, previousSessionID, err)
			return
		}
	} else if err := d.store.ClearProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
		d.sendChiefOfStaffResult(client, sessionID, false, previousSessionID, err)
		return
	}

	d.broadcastSessionsUpdated()
	d.sendChiefOfStaffResult(client, sessionID, msg.ChiefOfStaff, previousSessionID, nil)
}

func (d *Daemon) sendChiefOfStaffResult(
	client *wsClient,
	sessionID string,
	chiefOfStaff bool,
	previousSessionID string,
	err error,
) {
	result := protocol.ChiefOfStaffResultMessage{
		Event:        protocol.EventChiefOfStaffResult,
		SessionID:    sessionID,
		ChiefOfStaff: chiefOfStaff,
		Success:      err == nil,
	}
	if previousSessionID != "" {
		result.PreviousSessionID = protocol.Ptr(previousSessionID)
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}
