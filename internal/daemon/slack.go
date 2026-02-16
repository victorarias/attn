package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/slack"
)

// slackMonitor manages the Slack Socket Mode connection lifecycle
type slackMonitor struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	seen    map[string]time.Time // dedup: message ts → first seen time
	seenMu  sync.Mutex
}

// ensureSlackMonitor starts the Slack monitor if not already running.
// Called when a subscription is created.
func (d *Daemon) ensureSlackMonitor() {
	if d.slackMon == nil {
		d.slackMon = &slackMonitor{}
	}

	d.slackMon.mu.Lock()
	defer d.slackMon.mu.Unlock()

	if d.slackMon.running {
		return
	}

	auth, err := slack.LoadAuth()
	if err != nil {
		d.logf("[slack] auth not available: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.slackMon.cancel = cancel
	d.slackMon.running = true

	client := slack.NewClient(*auth, d.logf)

	go func() {
		d.logf("[slack] monitor starting")
		err := client.Listen(ctx, func(event slack.MessageEvent) {
			d.handleSlackMessage(event)
		})
		d.logf("[slack] monitor stopped: %v", err)

		d.slackMon.mu.Lock()
		d.slackMon.running = false
		d.slackMon.mu.Unlock()
	}()
}

// stopSlackMonitor stops the Slack monitor if running.
func (d *Daemon) stopSlackMonitor() {
	if d.slackMon == nil {
		return
	}

	d.slackMon.mu.Lock()
	defer d.slackMon.mu.Unlock()

	if !d.slackMon.running {
		return
	}

	if d.slackMon.cancel != nil {
		d.slackMon.cancel()
	}
	d.slackMon.running = false
	d.logf("[slack] monitor stopped")
}

// handleSlackMessage processes a thread reply from Slack and delivers to subscribed sessions.
func (d *Daemon) handleSlackMessage(event slack.MessageEvent) {
	// Dedup: Socket Mode can redeliver events on reconnect
	if d.slackMon != nil {
		d.slackMon.seenMu.Lock()
		if d.slackMon.seen == nil {
			d.slackMon.seen = make(map[string]time.Time)
		}
		if _, dup := d.slackMon.seen[event.TS]; dup {
			d.slackMon.seenMu.Unlock()
			d.logf("[slack] dedup: skipping already-seen message %s", event.TS)
			return
		}
		d.slackMon.seen[event.TS] = time.Now()
		// Prune entries older than 5 minutes
		for ts, t := range d.slackMon.seen {
			if time.Since(t) > 5*time.Minute {
				delete(d.slackMon.seen, ts)
			}
		}
		d.slackMon.seenMu.Unlock()
	}

	keys, err := d.store.GetSubscribedThreadKeys()
	if err != nil {
		d.logf("[slack] error getting subscriptions: %v", err)
		return
	}

	// Collect target sessions from exact thread match and channel-wide wildcard
	seen := make(map[string]bool)
	var targetSessions []string

	if event.ThreadTS != "" {
		key := event.Platform + ":" + event.ChannelID + ":" + event.ThreadTS
		for _, sid := range keys[key] {
			if !seen[sid] {
				seen[sid] = true
				targetSessions = append(targetSessions, sid)
			}
		}
	}
	// Channel-wide subscriptions (thread_ts = "*")
	channelKey := event.Platform + ":" + event.ChannelID + ":*"
	for _, sid := range keys[channelKey] {
		if !seen[sid] {
			seen[sid] = true
			targetSessions = append(targetSessions, sid)
		}
	}

	if len(targetSessions) == 0 {
		return
	}

	// Truncate long messages
	text := event.Text
	if len(text) > 300 {
		text = text[:297] + "..."
	}

	label := "thread reply"
	if event.ThreadTS == "" {
		label = "message"
	}
	message := fmt.Sprintf("Slack %s from @%s: %s", label, event.Username, text)

	for _, sessionID := range targetSessions {
		// Skip messages from this session (match [xxxx] prefix from slack-post)
		shortID := sessionID[:4]
		if strings.HasPrefix(event.Text, "["+shortID+"]") {
			d.logf("[slack] skipping self-message for session %s", shortID)
			continue
		}

		d.logf("[slack] delivering to session %s: %s", sessionID[:8], message[:min(len(message), 80)])
		go func(sid string) {
			cmd := exec.Command("cc-send", sid, message)
			if output, err := cmd.CombinedOutput(); err != nil {
				d.logf("[slack] cc-send to %s failed: %v (output: %s)", sid[:8], err, string(output))
			}
		}(sessionID)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
