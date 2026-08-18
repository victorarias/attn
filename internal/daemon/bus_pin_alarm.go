package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

// An enabled consumer that is not consuming makes the log grow for as long as
// the condition lasts. Until now the only way to notice was to go and look —
// `attn bus status`, or the event bus settings page. Disabled installed-app
// lanes are retained deliberately and wait for a measured per-lane tripwire.
//
// This is the half that does not wait to be looked at. `internal/bus` decides
// what counts as a pin worth reporting (DefaultPinAlarmAge carries the receipt);
// this file decides when to say it out loud, and what to tell the user to do.

const (
	// busPinAlarmKind is the recurring check on the job queue, where every
	// periodic duty in attn runs.
	busPinAlarmKind    = "bus_pin_alarm"
	busPinAlarmTimeout = 30 * time.Second

	// notificationKindBusPinned marks the notification a crossing writes.
	notificationKindBusPinned = "bus_retention_pinned"

	// busPinAlarmMinInterval floors the derived check interval. Below a minute the
	// queue would be doing work far faster than a pin can meaningfully change.
	busPinAlarmMinInterval = time.Minute
)

// busPinAlarmInterval derives how often to look from the window being watched.
// A check less frequent than a quarter of its own tripwire reports an outage
// long after the tripwire named it; more frequent than that buys nothing, since
// the condition it watches is measured in hours. At the default hour this is a
// quarter-hour tick doing four indexed reads.
func busPinAlarmInterval(age time.Duration) time.Duration {
	if interval := age / 4; interval > busPinAlarmMinInterval {
		return interval
	}
	return busPinAlarmMinInterval
}

// busPinAlarmAge is the tripwire this daemon watches, resolved the same way
// every other process that renders the finding resolves it.
//
// Resolved once: the bus and the recurring check that watches it are wired at
// different moments and must agree, and a value read twice is also announced
// twice in a log someone reads to find out what the limit is. The resolver never
// returns zero — off is negative — so zero means "not resolved yet".
func (d *Daemon) busPinAlarmAge() time.Duration {
	d.busPinMu.Lock()
	defer d.busPinMu.Unlock()
	if d.busPinAge == 0 {
		d.busPinAge = bus.PinAlarmAgeFromEnv(d.logf)
	}
	return d.busPinAge
}

// busPinEpisode is one consumer's position against the alarm, held for as long
// as the pin lasts.
//
// It is in memory, for the same reason the app stall clock is (see appStall): it
// tracks a condition that is happening now. A daemon restart genuinely does start
// the observation over — the consumer gets a fresh window against a daemon that
// has also just restarted — and a daemon restarting more often than the check
// interval never reaches the second observation, so a crash loop cannot turn into
// a stream of notifications.
type busPinEpisode struct {
	// cursor is the pinned position, and is what tells one episode from the next.
	// While the consumer is stuck its cursor cannot move; when it moves, whatever
	// was wrong is over and the next pin is a new episode that may notify again.
	cursor int64
	// notified is set once this episode has been announced. One notification per
	// episode: a durable row per tick, for a condition that can last days, is
	// noise that would teach the user to ignore the feed.
	notified bool
}

// busPinAlarmHandler is the recurring check. It reports what it saw so a run
// doing nothing and a run that is not happening look different in the task list.
func (d *Daemon) busPinAlarmHandler(_ context.Context, _ *jobs.Job) (any, error) {
	if d.eventBus == nil {
		return map[string]any{"pinned": 0}, nil
	}
	pins, err := d.eventBus.PinAlarms()
	if err != nil {
		return nil, fmt.Errorf("checking the event log's retention floor: %w", err)
	}
	notified := d.recordBusPins(pins)
	return map[string]any{"pinned": len(pins), "notified": notified}, nil
}

// recordBusPins advances the episode state and announces the crossings, and is
// the whole one-notification-per-episode rule.
//
// A pin is announced on the SECOND consecutive check that finds the same
// consumer stuck at the same cursor. The confirmation is what makes the alarm
// trustworthy across a suspend: on wake, a consumer's oldest unread event is as
// old as the sleep, and the delivery loop has not polled yet — one look cannot
// tell that from an outage, but two looks a whole interval apart can, because by
// then the cursor has moved.
func (d *Daemon) recordBusPins(pins []bus.Pin) int {
	d.busPinMu.Lock()
	defer d.busPinMu.Unlock()

	if d.busPinEpisodes == nil {
		d.busPinEpisodes = map[string]*busPinEpisode{}
	}
	// A consumer that is no longer pinning has no episode: the condition cleared,
	// and a later one is allowed to speak again.
	seen := make(map[string]bool, len(pins))
	for _, p := range pins {
		seen[p.Consumer] = true
	}
	for name := range d.busPinEpisodes {
		if !seen[name] {
			delete(d.busPinEpisodes, name)
		}
	}

	notified := 0
	for _, p := range pins {
		episode := d.busPinEpisodes[p.Consumer]
		if episode == nil || episode.cursor != p.Cursor {
			d.busPinEpisodes[p.Consumer] = &busPinEpisode{cursor: p.Cursor}
			continue
		}
		if episode.notified {
			continue
		}
		episode.notified = true
		d.notifyBusPin(p)
		notified++
	}
	return notified
}

// notifyBusPin writes the durable warning and puts it on the bus so an open app
// updates without waiting for something else to re-push the feed.
func (d *Daemon) notifyBusPin(p bus.Pin) {
	d.logf("bus: %s", bus.PinMessage(p))
	if d.store == nil {
		return
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind: notificationKindBusPinned,
		// The log keeps every pinned event and nothing is lost, so this is not an
		// emergency. It also never ends on its own, which is why it is said at all.
		Severity:   store.NotificationWarning,
		Title:      fmt.Sprintf("Event log held open by %s", p.Consumer),
		Body:       busPinNotificationBody(p),
		Detail:     fmt.Sprintf("oldest unread event: seq %d, %s", p.Cursor+1, p.OldestUnreadAt.UTC().Format(time.RFC3339)),
		SourceKind: "bus_consumer",
		SourceID:   p.Consumer,
	}, time.Now())
	if err != nil {
		d.logf("notifications: add bus-pin notification for %s: %v", p.Consumer, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

// busPinNotificationBody is the bus's own sentence about the pin, followed by
// the way out. Agents read errors and users read notifications; this has to let
// either one act without opening the code.
//
// The finding is not restated here. It is the same string `attn bus status` and
// the settings page print, so the notification a user gets and the page they open
// to check it can never describe one pin differently.
//
// The advice is chosen from the consumer's NAME, not from any runtime state: a
// name is true whatever is wrong behind it, and the check must keep working when
// the reason a consumer stopped is something nobody has thought of yet.
func busPinNotificationBody(p bus.Pin) string {
	way := fmt.Sprintf("`attn bus status` shows why it stopped; `attn bus disable %s` releases the log if you do not need it to catch up.", p.Consumer)
	if name, ok := strings.CutPrefix(p.Consumer, apps.ConsumerPrefix); ok {
		way = fmt.Sprintf("This is the %s app. `attn app status %s` and `attn app runtime status` show why it stopped — a parked runtime is the usual cause, and `attn app runtime restart` starts it again. `attn bus disable %s` releases the log instead.",
			name, name, p.Consumer)
	}
	return bus.PinMessage(p) + ". " + way
}
