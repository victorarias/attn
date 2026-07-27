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

// stopIsNonTerminal reports whether this Stop leaves the turn able to resume on
// its own, in which case none of the end-of-turn work applies: classifying reads
// a transcript the agent has not finished writing, and the resume id and
// narration belong to a turn that has not ended.
//
// It says nothing about what color the session should be. That used to be its
// return value, and the state it named was applied here — the second writer this
// phase removed. What the session looks like while it waits follows from the
// facts recorded alongside this call.
//
// A parked cron is deliberately not one of these. It reads like one — something
// will resume this session later — but the reason above does not hold for it:
// the turn is over, the transcript is flushed, and the wakeup may be hours away.
// Treating it as a yield meant a cron-parked session was never classified at
// all, so a turn that ended by asking the user something was never discovered to
// have asked. The schedule is recorded as a fact either way, and what it earns
// is a name for the *outcome* of the settle — see sessionstate.parked — not a
// reason to skip finding out what the outcome was.
//
// relaxBackgroundWork drops the background-work rule. It is set for the chief of
// staff: a chief that has merely armed a Monitor to watch its delegations (or a
// poll loop) is async-waiting, not working, and pegging it green makes the
// at-a-glance "is the chief actually working?" signal meaningless.
func stopIsNonTerminal(msg *protocol.StopMessage, relaxBackgroundWork bool) bool {
	return !relaxBackgroundWork && hasActiveBackgroundTask(msg)
}
