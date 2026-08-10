package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// sessionStateCause is a package-private sum type. Each variant identifies one
// valid store commit rule and one valid set of post-commit effects.
type sessionStateCause interface {
	isSessionStateCause()
}

// liveSignal is the worker poll reporting the state a worker was spawned into —
// what takes a session out of `launching`, and the only claim from outside the
// resolver that still commits; see pty.Source.AppliesState.
type liveSignal struct{}

// resolverObservation is the resolver's verdict on its tick: all evidence re-read
// at once, so it can move a session no source spoke about. No timestamp.
type resolverObservation struct{}

// pluginReport carries the active driver run cursor used for ordered state CAS.
type pluginReport struct {
	runID string
	seq   uint64
}

// startupRecovery rewrites persisted state before clients cross the recovery
// barrier. It deliberately produces no per-session effects or broadcasts.
type startupRecovery struct{}

// hostExitRecovery is a conversation session whose headless host is gone while
// the daemon is still running. It moves the session to `recoverable`, which the
// resolver does not own, so the exit evidence the same death recorded cannot
// then settle it to something that reads as finished.
//
// It broadcasts, unlike startupRecovery: a client is watching this session right
// now and its whole picture of "can I type here?" comes off the state.
type hostExitRecovery struct{}

func (liveSignal) isSessionStateCause()          {}
func (resolverObservation) isSessionStateCause() {}
func (pluginReport) isSessionStateCause()        {}
func (startupRecovery) isSessionStateCause()     {}
func (hostExitRecovery) isSessionStateCause()    {}

type sessionStateChange struct {
	sessionID string
	state     string
	cause     sessionStateCause
	// origin describes the evidence behind the change for the diagnostic trace;
	// optional (zero traces under the cause name) and never affects the commit.
	origin stateOrigin
}

// stateOrigin is where a state claim came from, as distinct from the commit
// rule it travels under; several sources share one cause.
type stateOrigin struct {
	source     string
	detail     string
	observedAt time.Time
}

// stateEffectProfile is internal policy derived from a closed cause. Callers do
// not assemble these flags themselves.
type stateEffectProfile struct {
	touch     bool
	syncNudge bool
	broadcast bool
}

func stateEffectProfileFor(cause sessionStateCause) (stateEffectProfile, bool) {
	switch cause.(type) {
	case liveSignal:
		return stateEffectProfile{touch: true, syncNudge: true, broadcast: true}, true
	case resolverObservation:
		return stateEffectProfile{syncNudge: true, broadcast: true}, true
	case pluginReport:
		return stateEffectProfile{touch: true, syncNudge: true, broadcast: true}, true
	case startupRecovery:
		return stateEffectProfile{}, true
	case hostExitRecovery:
		return stateEffectProfile{syncNudge: true, broadcast: true}, true
	default:
		return stateEffectProfile{}, false
	}
}

func sessionStateCauseName(cause sessionStateCause) string {
	switch cause.(type) {
	case liveSignal:
		return "live_signal"
	case resolverObservation:
		return "resolver_observation"
	case pluginReport:
		return "plugin_report"
	case startupRecovery:
		return "startup_recovery"
	case hostExitRecovery:
		return "host_exit_recovery"
	default:
		return "unknown"
	}
}

// applyState is the daemon's only persisted session-state transition door.
// Cause-specific guards remain at the caller; once a transition reaches this
// method, it owns the atomic store mutation and every accepted-state effect.
func (d *Daemon) applyState(change sessionStateChange) bool {
	if d.store == nil {
		return false
	}
	profile, ok := stateEffectProfileFor(change.cause)
	if !ok {
		d.logf("state update discarded: session=%s state=%s cause=unknown", change.sessionID, change.state)
		d.traceStateChange(change, statetrace.OutcomeDiscarded, "unknown_cause")
		return false
	}

	if profile.syncNudge {
		d.doorbellMu.Lock()
	}
	// Unconditional: every state write must be ordered against a timer that may
	// be deciding to settle right now. See autoSettleFireMu.
	d.autoSettleFireMu.Lock()
	applied := d.commitSessionState(change)
	d.autoSettleFireMu.Unlock()
	if profile.syncNudge {
		d.doorbellMu.Unlock()
	}
	if !applied {
		d.logf(
			"state update discarded: session=%s state=%s cause=%s",
			change.sessionID,
			change.state,
			sessionStateCauseName(change.cause),
		)
		d.traceStateChange(change, statetrace.OutcomeDiscarded, "store_rejected")
		return false
	}
	d.traceStateChange(change, statetrace.OutcomeApplied, "")

	// The one place a turn ever opens; runs for every cause and the store guards
	// re-opens. A snooze suppresses only this — the state is still committed and
	// broadcast — and a state that breaks through clears the snooze in the check.
	if attention.OpensTurn(protocol.SessionState(change.state)) &&
		!d.snoozeSuppressesTurn(change.sessionID, protocol.SessionState(change.state)) {
		d.store.OpenTurnIfClosed(change.sessionID, time.Now())
		// Reaching a state that wants the user is the moment the activity line
		// matters most, so it breaks through the tier's interval. It does NOT
		// break through `away` — enqueueSessionActivity still refuses there, and
		// that asymmetry is the whole cost model: generating for an empty room
		// would have cost nearly half of always-on.
		d.enqueueSessionActivity(change.sessionID)
	}

	if profile.touch {
		d.store.Touch(change.sessionID)
	}
	if profile.syncNudge {
		d.syncNudgeForState(change.sessionID, change.state)
	}
	// After the turn open above, so a state that both opens a turn and is
	// `working` is never seen half-applied; runs for every cause.
	d.syncAutoSettle(change.sessionID, change.state)
	if profile.broadcast {
		d.broadcastSessionStateChanged(change.sessionID)
	}
	// Last, and only for a target that owes one: a message that could not be
	// typed when it was sent has no other rail back.
	d.drainAgentMessagesAfterStateChange(change.sessionID, change.state)
	return true
}

func (d *Daemon) commitSessionState(change sessionStateChange) bool {
	switch cause := change.cause.(type) {
	case liveSignal, startupRecovery, resolverObservation, hostExitRecovery:
		return d.store.UpdateState(change.sessionID, change.state)
	case pluginReport:
		return d.store.ApplyAgentDriverState(change.sessionID, cause.runID, cause.seq, change.state)
	default:
		return false
	}
}
