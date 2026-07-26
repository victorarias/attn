package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// sessionStateCause is a package-private sum type. Each variant identifies one
// valid store commit rule and one valid set of post-commit effects.
type sessionStateCause interface {
	isSessionStateCause()
}

// liveSignal is the worker poll reporting the state a worker was spawned into.
// It is what takes a session out of `launching`, and the only claim from outside
// the resolver about what an agent is doing that still commits — see
// pty.Source.AppliesState.
//
// It used to be the vocabulary of every hook and PTY observation. Those are
// evidence now: they record what they saw, and the resolver decides what it
// means. The causes that went with them (a synchronous daemon observation, a
// timestamped classifier verdict, a process exit) went with them.
type liveSignal struct{}

// resolverObservation is the evidence resolver's verdict on its tick. Unlike
// every other cause it is not an edge reported by a source: it is a re-reading
// of all the evidence at once, which is what lets it move a session no source
// spoke about.
//
// It carries no timestamp. A resolution is a statement about now, computed from
// evidence whose own ages the resolver has already weighed, so there is nothing
// for the store to compare it against.
type resolverObservation struct{}

// pluginReport carries the active driver run cursor used for ordered state CAS.
type pluginReport struct {
	runID string
	seq   uint64
}

// startupRecovery rewrites persisted state before clients cross the recovery
// barrier. It deliberately produces no per-session effects or broadcasts.
type startupRecovery struct{}

func (liveSignal) isSessionStateCause()          {}
func (resolverObservation) isSessionStateCause() {}
func (pluginReport) isSessionStateCause()        {}
func (startupRecovery) isSessionStateCause()     {}

type sessionStateChange struct {
	sessionID string
	state     string
	cause     sessionStateCause
	// origin describes the evidence behind the change for the diagnostic trace.
	// It is optional: a caller that leaves it zero is traced under its cause
	// name, which is all a daemon-internal transition has to say about itself.
	// It never affects whether the change commits.
	origin stateOrigin
}

// stateOrigin is where a state claim came from, as distinct from the commit rule
// it travels under. Several sources share one cause — every trusted PTY
// observation is a liveSignal — so the cause alone cannot tell a screen scrape
// from an approval edge when a color turns out wrong.
type stateOrigin struct {
	source     string
	detail     string
	observedAt time.Time
}

// stateEffectProfile is internal policy derived from a closed cause. Callers do
// not assemble these flags themselves.
type stateEffectProfile struct {
	touch     bool
	trackRun  bool
	syncNudge bool
	broadcast bool
}

func stateEffectProfileFor(cause sessionStateCause) (stateEffectProfile, bool) {
	switch cause.(type) {
	case liveSignal:
		return stateEffectProfile{touch: true, trackRun: true, syncNudge: true, broadcast: true}, true
	case resolverObservation:
		return stateEffectProfile{trackRun: true, syncNudge: true, broadcast: true}, true
	case pluginReport:
		return stateEffectProfile{touch: true, trackRun: true, syncNudge: true, broadcast: true}, true
	case startupRecovery:
		return stateEffectProfile{}, true
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
	applied := d.commitSessionState(change)
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

	if profile.touch {
		d.store.Touch(change.sessionID)
	}
	if profile.trackRun {
		switch change.state {
		case protocol.StateWorking:
			d.markRunStartedIfNeeded(change.sessionID)
		case protocol.StateIdle, protocol.StateScheduled:
			d.clearLongRunTracking(change.sessionID)
		}
	}
	if profile.syncNudge {
		d.syncNudgeForState(change.sessionID, change.state)
	}
	if profile.broadcast {
		d.broadcastSessionStateChanged(change.sessionID)
	}
	return true
}

func (d *Daemon) commitSessionState(change sessionStateChange) bool {
	switch cause := change.cause.(type) {
	case liveSignal, startupRecovery, resolverObservation:
		return d.store.UpdateState(change.sessionID, change.state)
	case pluginReport:
		return d.store.ApplyAgentDriverState(change.sessionID, cause.runID, cause.seq, change.state)
	default:
		return false
	}
}
