package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// sessionStateCause is a package-private sum type. Each variant identifies one
// valid store commit rule and one valid set of post-commit effects.
type sessionStateCause interface {
	isSessionStateCause()
}

// liveSignal is an authoritative hook or trusted PTY observation.
type liveSignal struct{}

// daemonObservation is a synchronous daemon-derived observation, such as the
// transcript watcher or the immediate long-run-review handoff.
type daemonObservation struct{}

// classifierObservation is captured before slow classification work so its
// eventual result cannot overwrite a newer state transition.
type classifierObservation struct {
	observedAt time.Time
}

// pluginReport carries the active driver run cursor used for ordered state CAS.
type pluginReport struct {
	runID string
	seq   uint64
}

// startupRecovery rewrites persisted state before clients cross the recovery
// barrier. It deliberately produces no per-session effects or broadcasts.
type startupRecovery struct{}

// processExit records the terminal idle state after exit-specific teardown and
// pre-clobber ticket reconciliation have already run.
type processExit struct{}

func (liveSignal) isSessionStateCause()            {}
func (daemonObservation) isSessionStateCause()     {}
func (classifierObservation) isSessionStateCause() {}
func (pluginReport) isSessionStateCause()          {}
func (startupRecovery) isSessionStateCause()       {}
func (processExit) isSessionStateCause()           {}

type sessionStateChange struct {
	sessionID string
	state     string
	cause     sessionStateCause
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
	case daemonObservation, classifierObservation:
		return stateEffectProfile{trackRun: true, syncNudge: true, broadcast: true}, true
	case pluginReport:
		return stateEffectProfile{touch: true, trackRun: true, syncNudge: true, broadcast: true}, true
	case startupRecovery:
		return stateEffectProfile{}, true
	case processExit:
		return stateEffectProfile{touch: true, broadcast: true}, true
	default:
		return stateEffectProfile{}, false
	}
}

func sessionStateCauseName(cause sessionStateCause) string {
	switch cause.(type) {
	case liveSignal:
		return "live_signal"
	case daemonObservation:
		return "daemon_observation"
	case classifierObservation:
		return "classifier_observation"
	case pluginReport:
		return "plugin_report"
	case startupRecovery:
		return "startup_recovery"
	case processExit:
		return "process_exit"
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
		return false
	}

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
	case liveSignal, daemonObservation, startupRecovery, processExit:
		return d.store.UpdateState(change.sessionID, change.state)
	case classifierObservation:
		return d.store.UpdateStateWithTimestamp(change.sessionID, change.state, cause.observedAt)
	case pluginReport:
		return d.store.ApplyAgentDriverState(change.sessionID, cause.runID, cause.seq, change.state)
	default:
		return false
	}
}
