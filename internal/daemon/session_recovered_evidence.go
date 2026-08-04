package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
)

// Recovering a session's evidence after a daemon restart.
//
// The evidence table is in memory, so a new daemon starts knowing nothing about
// agents that have been running for hours. That is survivable for a busy agent —
// it repaints its title within a second and the table refills itself — and fatal
// for a quiet one, which writes nothing at all to its PTY and would therefore
// never produce a single piece of evidence again. The resolver would then have
// nothing to say about it for the rest of its life.
//
// Two things are recoverable without persisting anything, because both sources
// outlived the daemon:
//
//   - The level. The worker holds the last observation its signal observers
//     emitted, timestamped. A level is a standing claim, so reading it back is
//     not a guess about the past — it is what the agent is still saying.
//   - The outstanding harness edge. An approval or a question is not in the
//     worker, but the session's own persisted state *is* the record that one was
//     outstanding, and nothing has happened since to answer it.
//
// What is deliberately not reconstructed is the bracket pair (turn open, tool
// open). Those are hook-driven and genuinely gone, and inventing them would hold
// a session `working` on a bracket whose closing hook can never arrive.

// seedRecoveredEvidence files what the worker and the store between them still
// know about a re-adopted session, so the resolver has a basis for its first
// tick instead of an empty row.
func (d *Daemon) seedRecoveredEvidence(sessionID string, existing *protocol.Session, info ptybackend.SessionInfo) {
	if d == nil || existing == nil {
		return
	}
	if info.HasLastSignal {
		// Routed through the ordinary PTY evidence path rather than written
		// directly: the translation from a title claim to evidence has a case that
		// matters here — codex announces approvals in its title — and recovery must
		// not grow a second, subtly different copy of it.
		d.recordPTYEvidence(sessionID, info.LastSignal)
	}
	d.seedRecoveredHarnessEdge(sessionID, existing, info)
}

// seedRecoveredHarnessEdge restores the "the agent is blocked on a person" edge
// for a session that was in one of those states when the daemon went down.
//
// Without it the level alone decides, and the level cannot tell an agent waiting
// on an approval from one that has simply finished: both paint the same not-busy
// glyph. The resolver would read the restored session as a fresh prompt and
// settle it to idle, which loses the loudest state attn has.
//
// The guard is what keeps that from being a lie in the other direction. If the
// agent painted a title *after* the daemon concluded the approval, then it moved
// while nobody was watching — the prompt was answered, the turn ran on — and the
// edge describes a moment that is over. Only a level no newer than the
// conclusion still corroborates it.
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
		// A session cannot have reached either of these states without having
		// taken a turn, and the resolver reads "never took a turn" as a session
		// sitting at a fresh prompt. Saying so keeps a recovered agent from being
		// described as one that has not started yet.
		e.TurnEverOpened = true
	})
}

// recoveredHarnessClaim maps the two states that mean an outstanding harness edge
// onto the claim that produced them. Every other state is either resolvable from
// the level alone or not the resolver's to begin with.
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

// parseSessionStateSince is when the daemon last concluded the session's current
// state. It is the timestamp a recovered edge is stamped with, and the one the
// level is compared against, so an unparseable stamp means no seeding rather
// than a made-up instant.
func parseSessionStateSince(session *protocol.Session) (time.Time, bool) {
	stamp, err := time.Parse(time.RFC3339Nano, session.StateSince)
	if err != nil {
		return time.Time{}, false
	}
	return stamp, true
}
