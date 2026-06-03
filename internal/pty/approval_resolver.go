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
//     for clearDebounce of continued output before emitting "working", so a
//     prompt repaint or a chained multi-step approval does not flap to working
const approvalClearDebounce = 750 * time.Millisecond

type approvalResolver struct {
	armed        bool
	clearedSince time.Time
}

// observe consumes one rendered-screen sample and returns a state transition to
// emit: statePendingApproval once when the prompt first appears, then
// stateWorking once when the prompt has been gone for clearDebounce.
func (r *approvalResolver) observe(screenText string, now time.Time) (string, bool) {
	if isPendingApproval(screenText) {
		r.clearedSince = time.Time{}
		if !r.armed {
			r.armed = true
			return statePendingApproval, true
		}
		return "", false
	}

	if !r.armed {
		return "", false
	}

	if r.clearedSince.IsZero() {
		r.clearedSince = now
		return "", false
	}

	if now.Sub(r.clearedSince) >= approvalClearDebounce {
		r.armed = false
		r.clearedSince = time.Time{}
		return stateWorking, true
	}

	return "", false
}
