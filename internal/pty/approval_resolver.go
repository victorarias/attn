package pty

import "time"

// approvalResolver detects the user resolving an approval prompt by watching the
// rendered screen, and emits a single "working" transition once the prompt is
// gone. It exists because neither Claude nor Codex fire a hook at the moment a
// permission request is approved: the only signal back to "working" before the
// approved (possibly long-running) tool finishes is the approval UI disappearing
// from the terminal.
//
// State machine (driven by rendered-screen samples):
//   - on the first sample where the approval prompt is visible, arm and emit
//     statePendingApproval. The onset itself is hook-driven, but re-emitting it
//     here syncs the PTY layer's own state view: the worker only forwards a state
//     to the daemon when it differs from the last PTY-emitted state, and the
//     onset hook bypasses the worker. Without this, a working clear that follows
//     a hook-set pending (e.g. a chained second approval) would look unchanged to
//     the worker and never be forwarded. (Agents may emit no output while
//     blocked, so we arm on first sight rather than waiting for a dwell.)
//   - once armed and the prompt is no longer visible, require the absence to hold
//     for approvalClearDebounce before emitting "working", so a prompt repaint or
//     a chained multi-step approval does not flap to working.
//
// The debounce must NOT depend on further PTY output: a quiet approved command
// (one that produces no output while it runs) would otherwise stay stuck in
// pending_approval. observe reports approvalClearStarted the moment the prompt
// first disappears so the session can schedule an independent recheck.
const approvalClearDebounce = 750 * time.Millisecond

// approvalSignal is the action the session should take after observe consumes one
// rendered-screen sample.
type approvalSignal int

const (
	// approvalNone: nothing to do for this sample.
	approvalNone approvalSignal = iota
	// approvalArmedPending: the prompt just appeared. Emit statePendingApproval
	// to (re)sync the PTY layer's state view (see the type comment).
	approvalArmedPending
	// approvalClearStarted: the prompt just left the screen. The session should
	// schedule a recheck after approvalClearDebounce so the clear completes even
	// if no further PTY output arrives.
	approvalClearStarted
	// approvalCleared: the prompt has stayed gone for the debounce. Emit
	// stateWorking.
	approvalCleared
)

type approvalResolver struct {
	armed        bool
	clearedSince time.Time
}

// observe consumes one rendered-screen sample and reports the transition to act
// on: approvalArmedPending once when the prompt first appears, approvalClearStarted
// once when it first disappears, then approvalCleared once the prompt has been
// gone for approvalClearDebounce.
func (r *approvalResolver) observe(screenText string, now time.Time) approvalSignal {
	if isPendingApproval(screenText) {
		r.clearedSince = time.Time{}
		if !r.armed {
			r.armed = true
			return approvalArmedPending
		}
		return approvalNone
	}

	if !r.armed {
		return approvalNone
	}

	if r.clearedSince.IsZero() {
		r.clearedSince = now
		return approvalClearStarted
	}

	if now.Sub(r.clearedSince) >= approvalClearDebounce {
		r.armed = false
		r.clearedSince = time.Time{}
		return approvalCleared
	}

	return approvalNone
}
