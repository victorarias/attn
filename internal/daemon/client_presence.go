package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// PresenceTier is how much of the user's attention the daemon currently has.
// It gates activity generation, which exists to be glanced at: a line nobody
// can see has no value, so generating one is pure waste.
//
// The tiers are ordered, and the order is what makes the reduction across
// clients a max — two windows open, one showing the dashboard, is watching.
//
// This is a different question from presence.go's lastUserActivityAt, which
// answers "did the user act on the daemon" by watching UI-origin commands go
// past. Looking at a screen produces no commands at all, so the command proxy
// cannot see the case that matters most here — the user reading the dashboard
// and touching nothing. Clients report what they can see instead.
type PresenceTier int

const (
	// PresenceAway is nothing generated at all. Not a slow rate: a hard stop.
	// It is safe as a hard stop because leaving it is always an action that
	// restores a higher tier, so the staleness it creates heals itself the
	// moment it would matter. The cost is one generation of latency on return,
	// which the line's own age stamp makes honest.
	PresenceAway PresenceTier = iota
	// PresencePresent is input in the app recently, but the dashboard is not
	// what is on screen — the user is inside a session, and may glance back.
	PresencePresent
	// PresenceWatching is the app visible with the dashboard rendered. This is
	// the only tier where the line is actually being read.
	PresenceWatching
)

func (t PresenceTier) String() string {
	switch t {
	case PresenceWatching:
		return "watching"
	case PresencePresent:
		return "present"
	default:
		return "away"
	}
}

// clientPresence is what one connection last reported, plus when it reported.
//
// ReportedAt is not bookkeeping — it is the expiry. Presence is a heartbeat
// rather than a latch precisely so a client that crashes, is force-quit, or
// loses its socket mid-watching cannot pin generation on forever with nobody
// looking. A stale report is read as `away`, which fails toward off.
type clientPresence struct {
	Visible          bool
	DashboardVisible bool
	// IdleSeconds is how long since the last input anywhere in that client.
	// Negative means the client has observed no input at all this connection.
	IdleSeconds float64
	ReportedAt  time.Time
	// FirstReportAt is when this client first said anything. It is the floor for
	// idleness on a client that has seen no input: a window opened a minute ago
	// and not yet touched is plainly being looked at, while the same window eight
	// hours later is not, and without a floor both look identical.
	FirstReportAt time.Time
}

// idleFor returns how long since the user last touched this client, and whether
// the client knows. Unreported is unknown rather than zero: reading it as "input
// zero seconds ago" would make every open window count as recent input forever.
func (p clientPresence) idleFor() (time.Duration, bool) {
	if p.IdleSeconds < 0 {
		return 0, false
	}
	return time.Duration(p.IdleSeconds * float64(time.Second)), true
}

// watchingIdle is the same question asked for the watching tier, where unknown
// falls back to the age of the connection rather than disqualifying the client.
//
// The two differ because the tiers rest on different evidence. `present` is a
// claim about input — with none reported there is nothing to believe. `watching`
// is a claim about a screen, and the client's report that home is rendered is
// evidence on its own; a window opened a minute ago and not yet touched is
// plainly being looked at. What the fallback buys is the far end of the same
// case: the same untouched window eight hours later, which without a floor is
// indistinguishable from the fresh one and would generate forever.
func (p clientPresence) watchingIdle(now time.Time) time.Duration {
	if idle, ok := p.idleFor(); ok {
		return idle
	}
	if p.FirstReportAt.IsZero() {
		return 0
	}
	return now.Sub(p.FirstReportAt)
}

// presenceHeartbeatGrace is how long a client's last report stays believable.
//
// It is a tripwire, not a schedule: the app heartbeats far more often than
// this, so a healthy client never approaches it, and only a client that has
// genuinely stopped talking crosses it. Set generously because the cost of
// expiring early (a tier drop, then a recovery on the next heartbeat) is worse
// than the cost of expiring late (one extra generation interval).
const presenceHeartbeatGrace = 90 * time.Second

// presenceWatchingIdleLimit is how long `watching` survives with no input at
// all. Home stays on screen when nobody is there, so without this the cheapest
// case to reach is also the most expensive one to leave: the 300s tier expires
// after 90 idle seconds while the 120s tier never expires at all, and an app
// left on home generates for every working session until someone comes back.
//
// Ten minutes because this one must not fire on a user who IS there. Reading
// home is a thing people do without touching anything — scanning what every
// agent is doing is the whole point of the screen — so the limit is set past any
// plausible read rather than at the 90s the `present` tier uses, where the user
// is by definition looking at something else.
const presenceWatchingIdleLimit = 10 * time.Minute

// tier reduces one client's report to a tier, as of `now`.
//
// idleLimit is how long after the last input the `present` tier survives —
// activity.presence_idle_seconds, the daemon's setting, never the client's.
func (p clientPresence) tier(now time.Time, idleLimit time.Duration) PresenceTier {
	if p.ReportedAt.IsZero() || now.Sub(p.ReportedAt) > presenceHeartbeatGrace {
		return PresenceAway
	}
	if !p.Visible {
		// A hidden window can still be the app the user is typing into on
		// another display or space, but attn cannot tell that apart from a
		// minimized window, and `away` is the cheap guess.
		return PresenceAway
	}
	if p.DashboardVisible && p.watchingIdle(now) <= presenceWatchingIdleLimit {
		return PresenceWatching
	}
	if idle, ok := p.idleFor(); ok && idle <= idleLimit {
		return PresencePresent
	}
	return PresenceAway
}

// setPresence records a client's report. The zero ReportedAt a fresh connection
// carries is what makes a client that never reports count as away rather than
// as anything else.
func (c *wsClient) setPresence(msg *protocol.SetClientPresenceMessage, now time.Time) {
	idle := -1.0
	if msg.IdleSeconds != nil {
		idle = *msg.IdleSeconds
	}
	c.presenceMu.Lock()
	defer c.presenceMu.Unlock()
	firstAt := c.presence.FirstReportAt
	if firstAt.IsZero() {
		firstAt = now
	}
	c.presence = clientPresence{
		Visible:          msg.Visible,
		DashboardVisible: msg.DashboardVisible,
		IdleSeconds:      idle,
		ReportedAt:       now,
		FirstReportAt:    firstAt,
	}
}

func (c *wsClient) presenceReport() clientPresence {
	c.presenceMu.RLock()
	defer c.presenceMu.RUnlock()
	return c.presence
}

// handleSetClientPresence records what a client can see. Fire-and-forget: there
// is no result event, because there is nothing the client could do with one —
// the daemon owns the policy and the client owns only the facts.
func (d *Daemon) handleSetClientPresence(client *wsClient, msg *protocol.SetClientPresenceMessage) {
	client.setPresence(msg, time.Now())
}

// PresenceTier is the tier across every connected client: the highest anyone
// reports. Two windows open, one on the dashboard, is watching — the line is
// being read somewhere, and that is the only question the tier answers.
//
// Nothing is persisted. After a daemon restart no client has reported yet, so
// this is `away` until an app connects and says otherwise, which is the correct
// starting point rather than an unfortunate one.
func (d *Daemon) PresenceTier() PresenceTier {
	// No hub is no clients, which is the same answer as no clients: away.
	if d.wsHub == nil {
		return PresenceAway
	}
	now := time.Now()
	idleLimit := d.presenceIdleLimit()
	highest := PresenceAway
	d.wsHub.ForEachClient(func(client *wsClient) {
		if tier := client.presenceReport().tier(now, idleLimit); tier > highest {
			highest = tier
		}
	})
	return highest
}
