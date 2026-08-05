package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// handleSettleTurn is the user saying they are done with a session for now. It
// is the only way a turn ever ends — no state transition removes one — so it is
// also the ordinary move on a session that is still running.
func (d *Daemon) handleSettleTurn(msg *protocol.SettleTurnMessage) {
	if d == nil || d.store == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	if !d.store.SettleTurn(sessionID, time.Now()) {
		return
	}
	// A hand settle makes any pending auto-settle moot: there is no turn left to
	// close, and leaving a countdown on screen would promise a second settle.
	d.cancelAutoSettle(sessionID, "settled by user")
	d.traceSettle(sessionID)
	d.broadcastSessionStateChanged(sessionID)
}

// handlePinSession takes one session out of the queue, or puts it back, leaving
// its workspace and every sibling in it where they are.
//
// Unlike settle it does not close the turn: the stamps go on accruing while the
// session is pinned, so releasing the pin surfaces whatever was outstanding at
// its true age. Pinning is "I will come to this myself", not "I am done".
func (d *Daemon) handlePinSession(client *wsClient, msg *protocol.PinSessionMessage) {
	if msg == nil {
		return
	}
	if errMsg := d.setSessionPinned(msg.SessionID, msg.Pinned); errMsg != "" {
		d.sendCommandError(client, protocol.CmdPinSession, errMsg)
	}
}

// setSessionPinned is the one place the pin is written, shared by the WebSocket
// and unix-socket entry points. It returns a message when nothing was pinned.
func (d *Daemon) setSessionPinned(sessionID string, pinned bool) string {
	if d == nil || d.store == nil {
		return "store unavailable"
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return "missing session_id"
	}
	session := d.store.Get(id)
	if session == nil {
		return "session not found"
	}
	// The chief already has an anchored slot above the queue, so it has nothing
	// to be pinned out of; accepting the pin would hide it from the one place it
	// is guaranteed to be visible.
	//
	// Asked of the role registry, not of the session record: chief_of_staff is
	// decorated onto a session at broadcast and is never stored, so a stored
	// record's copy of it is always nil.
	if d.isChiefOfStaffSession(id) {
		return "the chief of staff is already anchored above the queue"
	}
	// Idempotent, so a repeated pin neither re-stamps the band order nor emits a
	// fact nothing acts on.
	alreadyPinned := strings.TrimSpace(protocol.Deref(session.PinnedAt)) != ""
	if alreadyPinned == pinned {
		return ""
	}
	if !d.store.SetSessionPinned(id, pinned, time.Now()) {
		return "persist session pin failed"
	}
	d.publishFact(FactSessionPinChanged, id, nil)
	return ""
}

// traceSettle records the settle beside the state it settled. A turn the user
// closed while the daemon could not explain the state it opened on is a
// detection failure with a witness — the trace is where that pairing survives.
func (d *Daemon) traceSettle(sessionID string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	reason := ""
	if session.StateReason != nil {
		reason = *session.StateReason
	}
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  "user",
		Claim:   string(session.State),
		Detail:  reason,
		Cause:   "settle",
		Outcome: statetrace.OutcomeApplied,
	})
}

// decorateSessionWithTurn derives whether the user owes this session a turn.
// It is derived at broadcast rather than stored because it depends on two
// stamps plus five exclusions; deriving it in the decoration seam makes every
// path that already broadcasts a session correct for free.
//
// It runs whether or not queue mode is enabled: the mode gates the band in the
// sidebar, not the daemon, so a hub renders a remote agent's turn correctly no
// matter what the remote daemon's own setting says.
func (d *Daemon) decorateSessionWithTurn(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	session.TurnOwed = nil
	session.TurnOpenedAt = nil

	stamps := d.store.TurnStamps(session.ID)
	in := attention.Input{
		OpenedAt:      stamps.OpenedAt,
		SettledAt:     stamps.SettledAt,
		IsShell:       string(session.Agent) == protocol.AgentShellValue,
		ChiefOfStaff:  protocol.Deref(session.ChiefOfStaff),
		SessionPinned: strings.TrimSpace(protocol.Deref(session.PinnedAt)) != "",
	}
	if workspace := d.store.GetWorkspace(session.WorkspaceID); workspace != nil {
		in.WorkspacePinned = workspace.Pinned
		in.WorkspaceMuted = workspace.Muted
	}
	if !attention.Owed(in) {
		return
	}
	session.TurnOwed = protocol.Ptr(true)
	session.TurnOpenedAt = protocol.Ptr(stamps.OpenedAt.UTC().Format(time.RFC3339Nano))
}
