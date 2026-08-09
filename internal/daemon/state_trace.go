package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// Per-session trace ring recording every state observation — applied, vetoed,
// store-discarded, or skipped. Nothing here arbitrates: no function in this
// file can change a session's state.

// Source names for non-PTY state evidence, named after the mechanism rather
// than the state it reports.
const (
	stateSourceHook         = "hook_state"
	stateSourceStopHook     = "hook_stop"
	stateSourceClassifier   = "classifier"
	stateSourceTranscript   = "transcript_watcher"
	stateSourcePluginDriver = "plugin_driver"
	// Reports a notification_type, not a state.
	stateSourceHookNotify = "hook_notify"
	// Claude's StopFailure hook (turn ended on an API error); reports an error_type.
	stateSourceHookStopFailure = "hook_stop_failure"
	stateSourceHookCompaction  = "hook_compaction"
	// The agent's resolved permission mode — who answers an approval request.
	stateSourceReviewer = "reviewer"
	stateSourceResolver = "resolver"
)

// stateTraceRecordGateHook runs inside the recorder's lock, between the liveness
// check and the write. Tests only: the seam a concurrent removal must hit.
var stateTraceRecordGateHook func(sessionID string)

// stateTraceRecorder returns the daemon's trace ring, building it on first use
// so a directly-constructed test daemon traces without a dedicated init site.
func (d *Daemon) stateTraceRecorder() *statetrace.Recorder {
	d.stateTraceOnce.Do(func() {
		d.stateTrace = statetrace.New(statetrace.DefaultCapacity)
	})
	return d.stateTrace
}

// recordStateObservation is the single write path into the trace. An observation
// for a session with no store row is logged, never ringed — it would leak one
// map entry per stale id.
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
	// The liveness check must stay inside the recorder's lock (RecordIf): checked
	// before it, a racing removal mints a ring that is never forgotten again.
	d.stateTraceRecorder().RecordIf(sessionID, obs, func() bool {
		live := d.store != nil && d.store.Get(sessionID) != nil
		if hook := stateTraceRecordGateHook; hook != nil {
			hook(sessionID)
		}
		return live
	})
	d.logf("%s", obs.LogLine(sessionID))
}

// traceStateChange records the fate of a change that reached applyState.
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

// traceStateVeto records an observation rejected before it reached applyState —
// the ones no other log line mentions at all.
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
// state; it never reaches applyState.
func (d *Daemon) traceStateEvidence(sessionID string, origin stateOrigin, claim string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:     origin.source,
		Claim:      claim,
		Detail:     origin.detail,
		Outcome:    statetrace.OutcomeObserved,
		ObservedAt: origin.observedAt,
	})
}

// traceStateSkip records a source that looked and reported no claim — only the
// trace separates "ran and had nothing to add" from "never ran".
func (d *Daemon) traceStateSkip(sessionID, source, reason string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  source,
		Outcome: statetrace.OutcomeSkipped,
		Reason:  reason,
	})
}

// forgetStateTrace drops a session's ring when the session goes away.
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

// handleHookNotification records Claude's Notification hook. Evidence, not a
// command: it lands ~6s after the event, so the resolver weighs it.
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

// handleHookStopFailure records Claude's StopFailure hook (replaces Stop on an
// API error); no end-of-turn work applies — there is no finished turn.
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

// tracePermissionMode records the agent's resolved approval mode. It rides on
// the state hook, not attn's launch flags: the mode can change mid-session.
func (d *Daemon) tracePermissionMode(sessionID, mode string) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return
	}
	d.traceStateEvidence(sessionID, stateOrigin{source: stateSourceReviewer}, mode)
}
