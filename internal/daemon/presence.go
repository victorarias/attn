package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// isUserPresenceCommand reports whether cmd is a UI-origin websocket command
// that indicates the user is actively at the app right now (as opposed to an
// agent driving the daemon over the unix socket, which never reaches this
// path). Keep this list in sync with the design in the ticket-inbox presence
// feature: session/workspace selection, PR/file views, and terminal
// input/resize are the actions a human takes while looking at the app.
func isUserPresenceCommand(cmd string) bool {
	switch cmd {
	case protocol.CmdSessionSelected,
		protocol.CmdWorkspaceSelected,
		protocol.CmdSessionVisualized,
		protocol.CmdPRVisited,
		protocol.CmdMarkFileViewed,
		protocol.CmdPtyInput,
		protocol.CmdPtyResize:
		return true
	default:
		return false
	}
}

// recordUserActivity stamps the current time as the most recent observed
// user-presence signal. Called from the websocket pre-dispatch path only;
// unix-socket (CLI/agent) commands never call this.
func (d *Daemon) recordUserActivity(now time.Time) {
	d.lastUserActivityAtNano.Store(now.UnixNano())
}

// lastUserActivityAt returns the most recent user-presence timestamp in UTC,
// or the zero time if no user activity has been observed since the daemon
// started.
func (d *Daemon) lastUserActivityAt() time.Time {
	nano := d.lastUserActivityAtNano.Load()
	if nano == 0 {
		return time.Time{}
	}
	return time.Unix(0, nano).UTC()
}
