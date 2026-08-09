package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
)

// Recovering a session's evidence after a daemon restart. The in-memory
// evidence table starts empty, which is fatal for a quiet agent that never
// writes to its PTY again. Three sources outlived the daemon and are re-seeded:
// the worker's last signal level, the persisted-state record of an outstanding
// approval/question edge, and the worker registry's approval route. The bracket
// pair (turn/tool open) is deliberately NOT reconstructed: it is hook-driven and
// gone, and inventing it would hold a session `working` on a bracket whose
// closing hook can never arrive.

// seedRecoveredEvidence files what the worker and store still know about a
// re-adopted session, so the resolver's first tick has a basis.
func (d *Daemon) seedRecoveredEvidence(sessionID string, existing *protocol.Session, info ptybackend.SessionInfo) {
	if d == nil || existing == nil {
		return
	}
	if route, ok := d.recoveredApprovalRoute(sessionID); ok {
		d.recordReviewerEvidence(sessionID, route.ReviewerInLoop())
	}
	if info.HasLastSignal {
		// Via the ordinary PTY evidence path: codex announces approvals in its
		// title, and recovery must not grow a second copy of that translation.
		d.recordPTYEvidence(sessionID, info.LastSignal)
	}
	d.seedRecoveredHarnessEdge(sessionID, existing, info)
}

// seedRecoveredHarnessEdge restores the blocked-on-a-person edge for a session
// that was in such a state at shutdown; without it the resolver settles the
// session to idle, losing the loudest state attn has. Guard: a title painted
// after the state was concluded means the prompt was answered while nobody was
// watching, so only a level no newer than the conclusion corroborates the edge.
func (d *Daemon) seedRecoveredHarnessEdge(sessionID string, existing *protocol.Session, info ptybackend.SessionInfo) {
	claim, ok := recoveredHarnessClaim(existing.State)
	if !ok {
		return
	}
	concludedAt, ok := parseSessionStateSince(existing)
	if !ok {
		return
	}
	if info.HasLastSignal && info.LastSignal.At.After(concludedAt) {
		return
	}
	d.recordEvidence(sessionID, concludedAt, func(e *sessionstate.Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      claim,
			Detail:     "recovered from persisted state",
			ObservedAt: concludedAt,
		}
		// These states imply a turn was taken; without this the resolver reads the
		// session as sitting at a fresh prompt.
		e.TurnEverOpened = true
	})
}

// recoveredHarnessClaim maps the two outstanding-harness-edge states onto the
// claim that produced them.
func recoveredHarnessClaim(state protocol.SessionState) (sessionstate.Claim, bool) {
	switch state {
	case protocol.SessionStatePendingApproval:
		return sessionstate.ClaimApprovalPending, true
	case protocol.SessionStateWaitingInput:
		return sessionstate.ClaimNeedsInput, true
	default:
		return "", false
	}
}

// parseSessionStateSince is when the daemon last concluded the current state.
// An unparseable stamp means no seeding rather than a made-up instant.
func parseSessionStateSince(session *protocol.Session) (time.Time, bool) {
	stamp, err := time.Parse(time.RFC3339Nano, session.StateSince)
	if err != nil {
		return time.Time{}, false
	}
	return stamp, true
}
