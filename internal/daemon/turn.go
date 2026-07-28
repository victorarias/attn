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
// stamps plus four exclusions; deriving it in the decoration seam makes every
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
		OpenedAt:     stamps.OpenedAt,
		SettledAt:    stamps.SettledAt,
		IsShell:      string(session.Agent) == protocol.AgentShellValue,
		ChiefOfStaff: protocol.Deref(session.ChiefOfStaff),
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
