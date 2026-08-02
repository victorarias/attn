package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// The Stop hook reports facts — the statuses of the agent's background tasks, how
// many scheduled wakeups are pending — and the rules below decide what they mean.
// The decision lives here rather than in the hook binary so the facts reach the
// daemon, which is where state from every other source is arbitrated.

// runningBackgroundTaskCount is how many of the stop's background tasks are
// still in flight. Status comparison is case-insensitive because it is the agent
// harness's string, not ours.
func runningBackgroundTaskCount(msg *protocol.StopMessage) int {
	running := 0
	for _, status := range msg.BackgroundTaskStatuses {
		if strings.EqualFold(strings.TrimSpace(status), "running") {
			running++
		}
	}
	return running
}

// hasActiveBackgroundTask reports whether the stop still has background work in
// flight.
func hasActiveBackgroundTask(msg *protocol.StopMessage) bool {
	return runningBackgroundTaskCount(msg) > 0
}

// hasPendingSessionCron reports whether the stop parked on a scheduled wakeup.
// Detection is presence-only — session_crons carries no per-item status, and a
// fired or deleted cron leaves the list entirely.
func hasPendingSessionCron(msg *protocol.StopMessage) bool {
	return protocol.Deref(msg.PendingSessionCrons) > 0
}

// stopIsNonTerminal reports whether this Stop leaves the turn able to resume on
// its own. The resume id and narration belong to a turn that has actually ended,
// so a non-terminal stop skips them; it is still judged, with the yield in view,
// because the payload cannot say whether the running work is what the turn waits
// on or a leftover it finished around. It says nothing about what color the
// session should be — that follows from the facts and verdict recorded alongside
// it.
//
// A parked cron is deliberately not one of these. The transcript is flushed and
// the wakeup may be hours away, so treating it as a yield only meant a cron-parked
// session was never classified and a question it asked was never discovered.
//
// relaxBackgroundWork drops the background-work rule for the chief of staff: a
// chief that has merely armed a Monitor to watch its delegations is async-waiting,
// not working, and pegging it green makes the "is the chief working?" glance
// meaningless.
func stopIsNonTerminal(msg *protocol.StopMessage, relaxBackgroundWork bool) bool {
	return !relaxBackgroundWork && hasActiveBackgroundTask(msg)
}
