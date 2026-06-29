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

// delegatedFromChiefSessionIDs returns the set of session IDs that the chief of
// staff delegated, so a single broadcast can decorate every session without one
// store lookup per session.
func (d *Daemon) delegatedFromChiefSessionIDs() map[string]bool {
	if d.store == nil {
		return nil
	}
	chiefSessionID := d.chiefOfStaffSessionID()
	if chiefSessionID == "" {
		return nil
	}
	return d.store.DelegatedFromChiefSessionIDs(chiefSessionID)
}

// decorateDelegatedFromChief marks a session that was delegated from the chief
// of staff. Mirrors decorateChiefOfStaffWithSessionID: the field is set only
// when true and cleared otherwise so it round-trips as an omitted boolean.
func (d *Daemon) decorateDelegatedFromChief(session *protocol.Session, delegatedFromChief map[string]bool) {
	if session == nil {
		return
	}
	if delegatedFromChief[session.ID] {
		session.DelegatedFromChief = protocol.Ptr(true)
		return
	}
	session.DelegatedFromChief = nil
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

// maybeAssignChiefOnSpawn assigns the chief-of-staff role at a session's first
// launch when the spawn requested it (the "create as chief" toggle). This is the
// only way the very first launch injects chief guidance: ChiefGuidance is gated
// on the role at launch time, and the post-launch promote path (reload) cannot
// resume a zero-turn session. Setting the role here — before ptyBackend.Spawn —
// lets the agent's async notebook-guide query (which can fire before the session
// row exists) observe chief=true and pull the guidance, because both the role
// check and the notebook-root resolution are independent of the sessions table.
//
// It is intentionally conservative: it assigns only on a genuine first launch
// (existingSession == nil, never a reload/respawn) of a guidance-capable agent
// (claude/codex — shells and plugin agents have no guidance launch path) and only
// when no chief exists yet. A create-as-chief request while a chief is already
// live is logged and ignored, never a silent role transfer. Returns whether it
// assigned the role so the caller can roll it back if the launch then fails.
func (d *Daemon) maybeAssignChiefOnSpawn(sessionID, agent string, requested bool, existingSession *protocol.Session) bool {
	if !requested || existingSession != nil || d.store == nil {
		return false
	}
	if !agentSupportsChiefReload(agent) {
		d.logf("create-as-chief: agent %q for session %s has no chief-guidance launch path; ignoring", agent, sessionID)
		return false
	}
	if current := d.chiefOfStaffSessionID(); current != "" {
		d.logf("create-as-chief: a chief (%s) already exists; ignoring request for session %s", current, sessionID)
		return false
	}
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
		d.logf("create-as-chief: set chief role failed for session %s: %v", sessionID, err)
		return false
	}
	d.logf("create-as-chief: session %s assigned chief role at launch", sessionID)
	return true
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
	// Reload the agent(s) whose chief status actually changed so the new status reaches
	// the system prompt: ChiefGuidance is injected only at agent-launch, so a live
	// promotion/demotion must re-run the launch path. The reload is destructive
	// (kill + resume-respawn), so fire it ONLY on a real role change — a redundant
	// toggle (re-assigning the current chief, or demoting a session that wasn't chief,
	// which ClearProfileRole no-ops) must not kill+respawn an innocent agent.
	// Resume-preserving, fire-and-forget; symmetric — assign injects the guidance,
	// demote drops it.
	roleChanged := previousSessionID != sessionID
	if !msg.ChiefOfStaff {
		// Demote: changed only if this session actually held the role.
		roleChanged = previousSessionID == sessionID
	}
	if roleChanged {
		go d.reloadSessionAgent(sessionID)
		// A role transfer (promote B while A still held it) demotes A via the
		// single-holder upsert. Reload A too so the displaced chief drops the guidance
		// now, not whenever it next restarts. Different id → different reload lock, so
		// this runs concurrently with the promotion reload.
		if msg.ChiefOfStaff && previousSessionID != "" {
			go d.reloadSessionAgent(previousSessionID)
		}
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
