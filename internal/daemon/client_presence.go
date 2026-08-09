package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// PresenceTier is how much of the user's attention the daemon currently has; it
// gates activity generation. Tiers are ordered so reducing across clients is a
// max. Distinct from presence.go's lastUserActivityAt, which sees commands but
// not a user reading the dashboard and touching nothing.
type PresenceTier int

const (
	// PresenceAway generates nothing at all — a hard stop, not a slow rate.
	PresenceAway PresenceTier = iota
	// PresencePresent is recent input in the app with the dashboard off screen.
	PresencePresent
	// PresenceWatching is the app visible with the dashboard rendered.
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

// clientPresence is what one connection last reported. ReportedAt is the
// expiry: presence is a heartbeat, not a latch, so a crashed client cannot pin
// generation on forever. A stale report reads as `away`.
type clientPresence struct {
	Visible          bool
	DashboardVisible bool
	// IdleSeconds is time since the last input in that client; negative means
	// the client has observed no input at all this connection.
	IdleSeconds float64
	ReportedAt  time.Time
	// FirstReportAt is the idleness floor for a client that has seen no input.
	FirstReportAt time.Time
}

// idleFor returns time since the last input in this client and whether the
// client knows. Unreported is unknown, not zero, which would read as fresh
// input forever.
func (p clientPresence) idleFor() (time.Duration, bool) {
	if p.IdleSeconds < 0 {
		return 0, false
	}
	return time.Duration(p.IdleSeconds * float64(time.Second)), true
}

// watchingIdle is idleFor for the watching tier, where unknown falls back to
// the age of the connection instead of disqualifying the client: a rendered
// dashboard is evidence on its own, but an untouched one must still expire.
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
// A tripwire: the app heartbeats far more often, so only a client that stopped
// talking crosses it.
const presenceHeartbeatGrace = 90 * time.Second

// presenceWatchingIdleLimit is how long `watching` survives with no input at
// all; without it an app left on home generates forever. Ten minutes is set
// past any plausible read, since reading home takes no input.
const presenceWatchingIdleLimit = 10 * time.Minute

// tier reduces one client's report to a tier as of `now`. idleLimit is
// activity.presence_idle_seconds, the daemon's setting, never the client's.
func (p clientPresence) tier(now time.Time, idleLimit time.Duration) PresenceTier {
	if p.ReportedAt.IsZero() || now.Sub(p.ReportedAt) > presenceHeartbeatGrace {
		return PresenceAway
	}
	if !p.Visible {
		// Indistinguishable from minimized; `away` is the cheap guess.
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

// setPresence records a client's report. A fresh connection's zero ReportedAt
// is what makes a client that never reports count as away.
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

// handleSetClientPresence records what a client can see. Fire-and-forget: the
// daemon owns the policy, the client owns only the facts.
func (d *Daemon) handleSetClientPresence(client *wsClient, msg *protocol.SetClientPresenceMessage) {
	client.setPresence(msg, time.Now())
}

// PresenceTier is the highest tier any connected client reports. Nothing is
// persisted, so a restarted daemon is `away` until an app says otherwise.
func (d *Daemon) PresenceTier() PresenceTier {
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
