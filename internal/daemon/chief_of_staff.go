package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const profileRoleChiefOfStaff = "chief_of_staff"

func (d *Daemon) chiefOfStaffSessionID() string {
	if d.store == nil {
		return ""
	}
	return strings.TrimSpace(d.store.GetProfileRole(profileRoleChiefOfStaff))
}

// isChiefOfStaffSession reports whether sessionID currently holds the
// profile-wide chief-of-staff role. The chief is protected from being closed:
// the close handlers consult this so an accidental ⌘W or close action cannot
// tear down the orchestrator session. To close it deliberately, unset the chief
// role first (set_chief_of_staff false).
func (d *Daemon) isChiefOfStaffSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	return d.chiefOfStaffSessionID() == sessionID
}

// chiefOfStaffProtectedError is the shared message returned to clients that try
// to close the chief-of-staff session.
const chiefOfStaffProtectedError = "chief of staff is protected from closing; unset the chief role first"

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

// nudgeChiefOfStaff types a bounded prompt into the current chief-of-staff
// session's PTY, but only when a chief is set and that session is idle or waiting
// for input — never an agent mid-task. It re-confirms the role right before
// typing (the role is a single-holder upsert that another promotion may have
// moved). Returns true only when the nudge was actually delivered, so callers can
// report whether the chief was pinged live versus only queued in the inbox.
func (d *Daemon) nudgeChiefOfStaff(prompt string) bool {
	if d.ptyBackend == nil || d.store == nil {
		return false
	}
	sessionID := d.chiefOfStaffSessionID()
	if sessionID == "" {
		return false
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return false
	}
	if session.State != protocol.SessionStateIdle && session.State != protocol.SessionStateWaitingInput {
		return false
	}
	if d.chiefOfStaffSessionID() != sessionID {
		return false
	}
	if err := d.typeDoorbell(sessionID, prompt); err != nil {
		d.logf("chief nudge: input failed for %s: %v", sessionID, err)
		return false
	}
	return true
}

// typeDoorbell types a bounded prompt followed by Enter into a session's PTY. It
// is the shared primitive behind the chief-of-staff doorbells (notebook
// activation, inbox nudge): a fixed trigger, never arbitrary streamed content.
func (d *Daemon) typeDoorbell(sessionID, prompt string) error {
	if err := d.ptyBackend.Input(context.Background(), sessionID, []byte(prompt)); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return d.ptyBackend.Input(context.Background(), sessionID, []byte{'\r'})
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
	// Live activation: when a running session becomes the chief, type a bounded
	// doorbell into its PTY so it pulls Notebook guidance. Only fires on an
	// idle/waiting session (guarded in the helper), never an agent mid-task.
	if msg.ChiefOfStaff {
		go d.activateNotebookGuidanceLive(sessionID)
	}
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
