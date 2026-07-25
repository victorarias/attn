package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// A Stop is not always terminal. The turn may yield with background work still
// in flight, in which case the agent auto-resumes when the work completes, or
// park on a pending scheduled wakeup, in which case it resumes when a cron
// fires. Classifying such a stop reads a not-yet-flushed transcript and
// mis-detects the session as idle/unknown.
//
// The Stop hook reports the facts (the statuses of the agent's background tasks
// and how many scheduled wakeups are pending); the rules below decide what they
// mean. The decision lives here rather than in the hook binary because the hook
// had to ask the daemon back for the chief-of-staff role to make it, and because
// a hook that collapses facts into a state name hides them from the daemon —
// which is where state from every other source is arbitrated.

// hasActiveBackgroundTask reports whether the stop still has background work in
// flight. Status comparison is case-insensitive because it is the agent
// harness's string, not ours.
func hasActiveBackgroundTask(msg *protocol.StopMessage) bool {
	for _, status := range msg.BackgroundTaskStatuses {
		if strings.EqualFold(strings.TrimSpace(status), "running") {
			return true
		}
	}
	return false
}

// hasPendingSessionCron reports whether the stop parked on a scheduled wakeup.
// Detection is presence-only — session_crons carries no per-item status, and a
// fired or deleted cron leaves the list entirely.
func hasPendingSessionCron(msg *protocol.StopMessage) bool {
	return protocol.Deref(msg.PendingSessionCrons) > 0
}

// nonTerminalStopState returns the runtime state to hold the session in for a
// Stop that is not terminal, or "" when the stop should fall through to normal
// classification. Running background work outranks a parked schedule, so a stop
// with both stays "working"; once both drain, the next stop classifies normally.
//
// relaxBackgroundWork drops the background-work -> "working" rule. It is set for
// the chief of staff: a chief that has merely armed a Monitor to watch its
// delegations (or a poll loop) is async-waiting, not working, and pegging it
// green makes the at-a-glance "is the chief actually working?" signal
// meaningless. With it set, background work no longer forces "working" (the stop
// falls through to normal classification, settling idle/waiting), while a pending
// scheduled wakeup still parks "scheduled" (quiet/blue, not green).
func nonTerminalStopState(msg *protocol.StopMessage, relaxBackgroundWork bool) string {
	switch {
	case !relaxBackgroundWork && hasActiveBackgroundTask(msg):
		return protocol.StateWorking
	case hasPendingSessionCron(msg):
		return protocol.StateScheduled
	default:
		return ""
	}
}
