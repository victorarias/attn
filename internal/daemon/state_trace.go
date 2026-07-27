package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// The daemon logs the state transitions it accepts and says nothing about the
// ones it drops, which makes a stuck color impossible to diagnose from the log:
// a stuck session is precisely one where nothing is being applied. These helpers
// record every observation the daemon acts on — applied, vetoed before the store
// door, discarded by the store, or skipped by its own source — into a capped
// per-session ring, and mirror each one to the daemon log.
//
// Nothing here arbitrates: no function in this file can change which state a
// session ends up in.

// Source names for state evidence that does not come from the PTY layer (those
// carry pty.Source through pty.Observation). These are deliberately named after
// the mechanism, not the state it reports, because two of them can report the
// same state for unrelated reasons.
const (
	// stateSourceHook is the agent's own state hook reporting through
	// `attn _hook-state`.
	stateSourceHook = "hook_state"
	// stateSourceStopHook is the Stop hook holding a yielded turn open on the
	// facts it reported (background work, a pending scheduled wakeup).
	stateSourceStopHook = "hook_stop"
	// stateSourceClassifier is the stop-time classification pipeline, including
	// the rules that settle a turn without paying for the LLM call.
	stateSourceClassifier = "classifier"
	// stateSourceTranscript is the transcript watcher reading the agent's JSONL.
	stateSourceTranscript = "transcript_watcher"
	// stateSourcePluginDriver is an external agent driver's sequenced report.
	stateSourcePluginDriver = "plugin_driver"
	// stateSourceHookNotify is Claude's Notification hook — the harness saying
	// out loud that it is blocked on the user. It reports a notification_type,
	// not a state.
	stateSourceHookNotify = "hook_notify"
	// stateSourceHookStopFailure is Claude's StopFailure hook — the turn ended on
	// an API error rather than on an answer. It reports an error_type.
	stateSourceHookStopFailure = "hook_stop_failure"
	// stateSourceHookCompaction is Claude's PreCompact/PostCompact pair bracketing
	// the agent rewriting its own context.
	stateSourceHookCompaction = "hook_compaction"
	// stateSourceReviewer is the agent's resolved permission mode, reported as a
	// fact on the state hook. It says who answers an approval request, which is
	// what separates a real approval stall from a guardian's brief round trip.
	stateSourceReviewer = "reviewer"
	// stateSourceResolver is the evidence resolver's own verdict.
	stateSourceResolver = "resolver"
)

// stateTraceRecordGateHook runs inside the recorder's lock, between the liveness
// check and the write. Tests only: it is the seam where a concurrent removal
// would have to interleave for the ring to leak.
var stateTraceRecordGateHook func(sessionID string)

// stateTraceRecorder returns the daemon's trace ring, building it on first use
// so a directly-constructed test daemon traces without a dedicated init site.
func (d *Daemon) stateTraceRecorder() *statetrace.Recorder {
	d.stateTraceOnce.Do(func() {
		d.stateTrace = statetrace.New(statetrace.DefaultCapacity)
	})
	return d.stateTrace
}

// recordStateObservation is the single write path into the trace.
//
// An observation for a session with no store row is logged and not ringed. Such
// an id can never be read back — `attn state explain` needs a store row — and it
// can never be cleaned up either, because the cleanup hangs off session removal.
// Ringing it would leak one map entry per stale id for the daemon's lifetime,
// and stale ids are not rare: a worker event racing a session removal produces
// one every time. The daemon log is the right home for them.
func (d *Daemon) recordStateObservation(sessionID string, obs statetrace.Observation) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	if obs.RecordedAt.IsZero() {
		obs.RecordedAt = time.Now()
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = obs.RecordedAt
	}
	// The liveness check runs inside the recorder's lock (RecordIf), not before
	// it. Checking first and recording after leaves a window where the session is
	// removed and its ring forgotten in between, and the writer then creates a
	// fresh ring for an id that will never be forgotten again.
	d.stateTraceRecorder().RecordIf(sessionID, obs, func() bool {
		live := d.store != nil && d.store.Get(sessionID) != nil
		// The hook runs after the check on purpose: check-then-write is the
		// sequence that leaks when the two are not atomic, so this is where a
		// racing removal has to be injected to falsify the lock.
		if hook := stateTraceRecordGateHook; hook != nil {
			hook(sessionID)
		}
		return live
	})
	d.logf("%s", obs.LogLine(sessionID))
}

// traceStateChange records the fate of a change that reached applyState. The
// origin is optional; without one the cause name stands in as the source, which
// is all a daemon-internal transition can say about where it came from.
func (d *Daemon) traceStateChange(change sessionStateChange, outcome statetrace.Outcome, reason string) {
	source := change.origin.source
	if source == "" {
		source = sessionStateCauseName(change.cause)
	}
	d.recordStateObservation(change.sessionID, statetrace.Observation{
		Source:     source,
		Claim:      change.state,
		Detail:     change.origin.detail,
		Cause:      sessionStateCauseName(change.cause),
		Outcome:    outcome,
		Reason:     reason,
		ObservedAt: change.origin.observedAt,
	})
}

// traceStateVeto records an observation rejected before it reached applyState.
// These are the interesting ones for a stuck color: the state never got near the
// store, so no other log line mentions it at all.
func (d *Daemon) traceStateVeto(sessionID string, origin stateOrigin, claim, reason string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:     origin.source,
		Claim:      claim,
		Detail:     origin.detail,
		Outcome:    statetrace.OutcomeVetoed,
		Reason:     reason,
		ObservedAt: origin.observedAt,
	})
}

// traceStateEvidence records an observation whose source does not drive session
// state. It is recorded and goes no further — the observation never reaches
// applyState, so it has no cause and cannot be applied or rejected by the store.
func (d *Daemon) traceStateEvidence(sessionID string, origin stateOrigin, claim string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:     origin.source,
		Claim:      claim,
		Detail:     origin.detail,
		Outcome:    statetrace.OutcomeObserved,
		ObservedAt: origin.observedAt,
	})
}

// traceStateSkip records a source that looked and reported no claim. A skip and
// a missing observation look identical in the store; only the trace separates
// "the classifier ran and had nothing to add" from "the classifier never ran".
func (d *Daemon) traceStateSkip(sessionID, source, reason string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  source,
		Outcome: statetrace.OutcomeSkipped,
		Reason:  reason,
	})
}

// forgetStateTrace drops a session's ring when the session goes away, so the
// recorder does not grow without bound across a long-lived daemon.
func (d *Daemon) forgetStateTrace(sessionID string) {
	d.stateTraceRecorder().Forget(sessionID)
}

func (d *Daemon) handleStateExplain(conn net.Conn, msg *protocol.StateExplainMessage) {
	session := d.store.Get(strings.TrimSpace(msg.TargetSessionID))
	if session == nil {
		d.sendError(conn, "session_not_found")
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                 true,
		StateExplainResult: d.stateExplainResult(session),
	})
}

// stateExplainResult renders the current trace for one session.
func (d *Daemon) stateExplainResult(session *protocol.Session) *protocol.StateExplainResult {
	recorded := d.stateTraceRecorder().Observations(session.ID)
	observations := make([]protocol.StateExplainEntry, 0, len(recorded))
	for _, obs := range recorded {
		entry := protocol.StateExplainEntry{
			Source:     obs.Source,
			Claim:      obs.Claim,
			Outcome:    string(obs.Outcome),
			ObservedAt: obs.ObservedAt.Format(time.RFC3339Nano),
			RecordedAt: obs.RecordedAt.Format(time.RFC3339Nano),
		}
		if obs.Detail != "" {
			entry.Detail = protocol.Ptr(obs.Detail)
		}
		if obs.Cause != "" {
			entry.Cause = protocol.Ptr(obs.Cause)
		}
		if obs.Reason != "" {
			entry.Reason = protocol.Ptr(obs.Reason)
		}
		if obs.Repeats > 0 {
			entry.Repeats = protocol.Ptr(obs.Repeats)
		}
		observations = append(observations, entry)
	}
	result := &protocol.StateExplainResult{
		SessionID:    session.ID,
		Agent:        string(session.Agent),
		State:        string(session.State),
		Observations: observations,
		Capacity:     d.stateTraceRecorder().Capacity(),
	}
	if strings.TrimSpace(session.StateSince) != "" {
		result.StateSince = protocol.Ptr(session.StateSince)
	}
	return result
}

// handleHookNotification records Claude's Notification hook. The hook is
// evidence, not a command: it lands ~6s after the event it describes, so acting
// on it directly would paint a state the session may already have left. The
// resolver weighs it against fresher sources.
func (d *Daemon) handleHookNotification(conn net.Conn, msg *protocol.HookNotificationMessage) {
	kind := strings.TrimSpace(msg.NotificationType)
	if kind == "" {
		d.sendError(conn, "missing notification_type")
		return
	}
	message := strings.TrimSpace(protocol.Deref(msg.Message))
	d.traceStateEvidence(msg.ID, stateOrigin{
		source: stateSourceHookNotify,
		detail: message,
	}, kind)
	d.recordNotificationEvidence(msg.ID, kind, message)
	d.sendOK(conn)
}

// handleHookStopFailure records Claude's StopFailure hook, which replaces Stop
// when a turn ends on an API error. None of the end-of-turn work applies: there
// is no finished turn to classify, narrate, or resume from.
func (d *Daemon) handleHookStopFailure(conn net.Conn, msg *protocol.HookStopFailureMessage) {
	errorType := strings.TrimSpace(msg.ErrorType)
	if errorType == "" {
		d.sendError(conn, "missing error_type")
		return
	}
	message := strings.TrimSpace(protocol.Deref(msg.ErrorMessage))
	d.traceStateEvidence(msg.ID, stateOrigin{
		source: stateSourceHookStopFailure,
		detail: message,
	}, errorType)
	d.recordStopFailureEvidence(msg.ID, errorType, message)
	d.sendOK(conn)
}

// handleHookCompaction records Claude's PreCompact/PostCompact pair.
func (d *Daemon) handleHookCompaction(conn net.Conn, msg *protocol.HookCompactionMessage) {
	phase := "finished"
	if msg.Active {
		phase = "started"
	}
	d.traceStateEvidence(msg.ID, stateOrigin{
		source: stateSourceHookCompaction,
		detail: strings.TrimSpace(protocol.Deref(msg.Trigger)),
	}, phase)
	d.recordCompactionEvidence(msg.ID, msg.Active)
	d.sendOK(conn)
}

// tracePermissionMode records the agent's resolved approval mode as a level.
// It rides along on the state hook rather than being read from attn's launch
// flags, which are not authoritative: a user's global agent settings can put a
// guardian in the loop for a session attn launched without asking for one, and
// the mode can change mid-session.
func (d *Daemon) tracePermissionMode(sessionID, mode string) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return
	}
	d.traceStateEvidence(sessionID, stateOrigin{source: stateSourceReviewer}, mode)
}
