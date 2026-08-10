package bus

import (
	"fmt"
	"sort"
	"time"
)

// The bus's operator picture: what the log holds, who writes to it, who reads
// it, and what is wrong. Computed once here and rendered twice — `attn bus
// status` runs it against the database directly, the daemon serves the same
// snapshot to the app. Neither surface derives anything of its own, so the two
// can never disagree about whether something is healthy.

const (
	// RecentWindow and BaselineWindow are the two rates a producer is reported
	// at: what it is doing now, and what it has been doing.
	RecentWindow   = time.Hour
	BaselineWindow = 24 * time.Hour

	// SurgeWindow and SurgeRatePerHour are the loud-producer tripwire.
	//
	// It is an absolute ceiling, not a multiple of the producer's own history,
	// because a relative baseline cannot be set on this data: measured over 8
	// days of production, an hour compared against the mean of its preceding 24
	// reaches 35x on healthy classes (an idle night drags the baseline to near
	// zero) and would fire 48 times; day-over-day still swings 4-6x on the loud
	// classes. Any threshold that catches a real flap also cries wolf.
	//
	// Sustained rate separates cleanly, because hours of smoothing absorb the
	// burstiness that defeats the relative comparison. Measured over the same 8
	// days of production, against the loudest healthy class (pr.updated):
	//
	//	window  healthy max  flapping class     headroom at 1000/h
	//	6h        480/h      5763/h peak          2.08x
	//	24h       357/h      1075-2265/h          2.80x
	//
	// The ceiling is checked on BOTH windows because they catch different
	// things. The 6h window catches a flap starting, within hours. The 24h
	// window catches one that is simply always loud — the state this bus was
	// already in, where an evening lull drops the 6h rate below the line while
	// the producer still writes two thirds of the log.
	//
	// The headroom is deliberately thinner than a tripwire usually gets, because
	// the two errors do not cost the same: a false positive is one WARN line in
	// the daemon log, and a false negative is what already happened — a producer
	// writing two thirds of the log for a week, found by accident.
	SurgeWindow      = 6 * time.Hour
	SurgeRatePerHour = 1000.0

	// StallAge is how long an enabled consumer may hold unread events without
	// its cursor moving before it is called stalled. Delivery polls every
	// DefaultPollInterval (5s) and a failing handler retries at most
	// DefaultRetryCap (2m) apart, so a healthy consumer — even one retrying —
	// advances well inside this. 5 minutes is 2.5x past the retry cap.
	StallAge = 5 * time.Minute
)

// ProducerRow is what the Store seam hands back for one fact class: the raw
// counts, with no rate or share derived. The policy — which windows, what
// counts as loud — lives on this side of the seam.
type ProducerRow struct {
	Name     string
	Events   int64
	Bytes    int64
	Subjects int64
	// Recent is aligned with the cutoffs Producers was given, one count each.
	Recent []int64
}

// Producer is one fact class's contribution to the log.
type Producer struct {
	Name     string
	Events   int64
	Bytes    int64
	Subjects int64
	// Share is this class's fraction of the log's rows, 0..1.
	Share float64
	// RecentPerHour and BaselinePerHour are mean rates over RecentWindow and
	// BaselineWindow. SustainedPerHour is the rate over SurgeWindow.
	RecentPerHour    float64
	BaselinePerHour  float64
	SustainedPerHour float64
	// Surging is set when either sustained window crossed SurgeRatePerHour;
	// SurgeWindow names the one that did, and SurgePerHour is its rate.
	Surging      bool
	SurgeWindow  time.Duration
	SurgePerHour float64
}

// surge decides whether a producer trips the ceiling, and on which window. The
// loudest crossing wins, so the message reports the worst of the two rather
// than whichever happened to be checked first.
func (p *Producer) surge() {
	windows := []struct {
		d    time.Duration
		rate float64
	}{
		{SurgeWindow, p.SustainedPerHour},
		{BaselineWindow, p.BaselinePerHour},
	}
	for _, w := range windows {
		if w.rate >= SurgeRatePerHour && w.rate > p.SurgePerHour {
			p.Surging = true
			p.SurgeWindow = w.d
			p.SurgePerHour = w.rate
		}
	}
}

// ConsumerStatus is one durable consumer's registration, position, and lag.
type ConsumerStatus struct {
	Name      string
	Cursor    int64
	Lag       int64
	Filter    string
	Enabled   bool
	UpdatedAt time.Time
	// Live is true when this process has a delivery loop for the consumer, and
	// is meaningful only when Status.Delivering is set.
	Live bool
	// Stalled carries the current failure message when a handler is failing.
	Stalled string
	// OldestUnreadAt stamps the oldest event this consumer has not read; zero
	// when it is caught up. Lag counts events, this says how long they waited.
	OldestUnreadAt time.Time
	// HoldsRetentionFloor marks the enabled consumer whose cursor is the floor
	// retention stops at — the one pinning the log.
	HoldsRetentionFloor bool
}

// Health levels, ordered by how much they want attention.
const (
	HealthWarn  = "warn"
	HealthError = "error"
)

// Health kinds. Machine-readable so a surface can style or filter by them; the
// Message is the whole sentence and needs no assembly.
const (
	HealthConsumerDisabled = "consumer_disabled"
	HealthConsumerStalled  = "consumer_stalled"
	HealthConsumerNotLive  = "consumer_not_live"
	HealthConsumerLagging  = "consumer_lagging"
	HealthRetentionPinned  = "retention_pinned"
	HealthProducerSurging  = "producer_surging"
)

// Health is one thing wrong, said plainly. A surface renders Message; it does
// not re-derive the finding from the numbers.
type Health struct {
	Level   string
	Kind    string
	Subject string
	Message string
}

// Status is the whole operator picture.
type Status struct {
	Head     int64
	Earliest int64
	Rows     int64
	Bytes    int64
	// OldestAt and NewestAt bound the log in time; zero on an empty log.
	OldestAt time.Time
	NewestAt time.Time
	// Delivering says this snapshot came from a process that owns the delivery
	// loops. Only then do Live and Stalled mean anything, and only then can a
	// registered-but-not-running consumer be reported — `attn bus status` reads
	// the database from outside the daemon and knows neither.
	Delivering bool
	// RetentionWindow is the age past which retention drops events.
	RetentionWindow time.Duration

	Producers []Producer
	Consumers []ConsumerStatus
	Health    []Health
}

// Status snapshots the bus. Everything both surfaces show is computed here.
func (b *Bus) Status() (Status, error) {
	if b.store == nil {
		return Status{}, nil
	}
	now := b.now()

	earliest, head, err := b.store.Bounds()
	if err != nil {
		return Status{}, err
	}
	consumers, err := b.store.ListConsumers()
	if err != nil {
		return Status{}, err
	}
	// Cutoffs are positional; the reads below must match this order.
	cutoffs := []time.Time{
		now.Add(-RecentWindow),
		now.Add(-SurgeWindow),
		now.Add(-BaselineWindow),
	}
	producers, err := b.store.Producers(cutoffs)
	if err != nil {
		return Status{}, err
	}

	out := Status{
		Head:            head,
		Earliest:        earliest,
		Delivering:      b.delivering(),
		RetentionWindow: b.retention,
	}
	for _, p := range producers {
		out.Rows += p.Events
		out.Bytes += p.Bytes
	}
	if out.Rows > 0 {
		if t, ok, err := b.store.EventTimeAt(earliest); err == nil && ok {
			out.OldestAt = t
		}
		if t, ok, err := b.store.EventTimeAt(head); err == nil && ok {
			out.NewestAt = t
		}
	}

	for _, p := range producers {
		entry := Producer{
			Name:             p.Name,
			Events:           p.Events,
			Bytes:            p.Bytes,
			Subjects:         p.Subjects,
			RecentPerHour:    perHour(p.Recent[0], RecentWindow),
			SustainedPerHour: perHour(p.Recent[1], SurgeWindow),
			BaselinePerHour:  perHour(p.Recent[2], BaselineWindow),
		}
		if out.Rows > 0 {
			entry.Share = float64(p.Events) / float64(out.Rows)
		}
		entry.surge()
		out.Producers = append(out.Producers, entry)
	}

	live := b.liveDurables()
	floor := retentionFloorName(consumers)
	for _, c := range consumers {
		entry := ConsumerStatus{
			Name:                c.Name,
			Cursor:              c.Cursor,
			Lag:                 max64(head-c.Cursor, 0),
			Filter:              c.Filter,
			Enabled:             c.Enabled,
			UpdatedAt:           c.UpdatedAt,
			HoldsRetentionFloor: c.Name == floor,
		}
		if entry.Lag > 0 {
			if t, ok, err := b.store.EventTimeAt(c.Cursor + 1); err == nil && ok {
				entry.OldestUnreadAt = t
			}
		}
		if d, ok := live[c.Name]; ok {
			entry.Live = true
			entry.Stalled = d.stallReason()
		}
		out.Consumers = append(out.Consumers, entry)
	}

	out.Health = health(out, now)
	return out, nil
}

// health turns the snapshot into the warnings a user should read, so no surface
// has to infer "this is bad" from a number. Ordered worst first.
func health(s Status, now time.Time) []Health {
	var out []Health
	for _, c := range s.Consumers {
		switch {
		case c.Stalled != "":
			out = append(out, Health{
				Level: HealthError, Kind: HealthConsumerStalled, Subject: c.Name,
				Message: fmt.Sprintf("consumer %s is stalled at seq %d and is retrying: %s",
					c.Name, c.Cursor+1, c.Stalled),
			})
		case c.Enabled && c.Lag > 0 && stale(c.UpdatedAt, now):
			msg := fmt.Sprintf("consumer %s is %s behind and not advancing; its cursor has not moved for %s",
				c.Name, events(c.Lag), roundDuration(now.Sub(c.UpdatedAt)))
			if !c.OldestUnreadAt.IsZero() {
				msg += fmt.Sprintf(", and its oldest unread event has waited %s",
					roundDuration(now.Sub(c.OldestUnreadAt)))
			}
			out = append(out, Health{
				Level: HealthError, Kind: HealthConsumerLagging, Subject: c.Name, Message: msg,
			})
		case s.Delivering && c.Enabled && !c.Live:
			out = append(out, Health{
				Level: HealthWarn, Kind: HealthConsumerNotLive, Subject: c.Name,
				Message: fmt.Sprintf("consumer %s is registered and enabled but has no delivery loop in this daemon, so nothing is reading its %s backlog",
					c.Name, events(c.Lag)),
			})
		case !c.Enabled:
			out = append(out, Health{
				Level: HealthWarn, Kind: HealthConsumerDisabled, Subject: c.Name,
				Message: fmt.Sprintf("consumer %s is disabled: it is not being delivered to, and it does not hold retention open — once trimming passes its cursor at seq %d, enabling it resumes at head with a logged gap",
					c.Name, c.Cursor),
			})
		}
	}
	for _, c := range s.Consumers {
		// A consumer legitimately holding the floor a little back is the system
		// working. It is worth saying only when it is also the reason old events
		// survive: it has fallen behind the retention window itself.
		if !c.HoldsRetentionFloor || c.OldestUnreadAt.IsZero() {
			continue
		}
		age := now.Sub(c.OldestUnreadAt)
		if age <= s.RetentionWindow {
			continue
		}
		out = append(out, Health{
			Level: HealthWarn, Kind: HealthRetentionPinned, Subject: c.Name,
			Message: fmt.Sprintf("consumer %s pins the retention floor at seq %d: its oldest unread event is %s old, past the %s window, so nothing below it can be trimmed",
				c.Name, c.Cursor, roundDuration(age), roundDuration(s.RetentionWindow)),
		})
	}
	for _, p := range s.Producers {
		if !p.Surging {
			continue
		}
		out = append(out, Health{
			Level: HealthWarn, Kind: HealthProducerSurging, Subject: p.Name,
			Message: surgeMessage(p),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Level == HealthError && out[j].Level != HealthError
	})
	return out
}

// surgeMessage names the number, the tripwire it crossed, and the window — the
// three things an agent needs to act on it without reading this code.
func surgeMessage(p Producer) string {
	return fmt.Sprintf("producer %s is publishing %.0f events/hour sustained over the last %s, past the %.0f/hour tripwire; it holds %s (%.0f%% of the log) across %d subject(s)",
		p.Name, p.SurgePerHour, roundDuration(p.SurgeWindow), SurgeRatePerHour,
		events(p.Events), p.Share*100, p.Subjects)
}

// retentionFloorName is the enabled consumer whose cursor retention stops at.
// Disabled consumers are excluded exactly as the trim does — they do not pin.
func retentionFloorName(consumers []Consumer) string {
	name := ""
	floor := int64(-1)
	for _, c := range consumers {
		if !c.Enabled {
			continue
		}
		if floor < 0 || c.Cursor < floor {
			floor = c.Cursor
			name = c.Name
		}
	}
	return name
}

func (b *Bus) liveDurables() map[string]*durable {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]*durable, len(b.durables))
	for _, d := range b.durables {
		out[d.name] = d
	}
	return out
}

// delivering reports whether this process owns delivery loops, which is what
// makes Live and Stalled trustworthy. A Bus built to read the database — `attn
// bus status` — registers nothing and never starts.
func (b *Bus) delivering() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started && len(b.durables) > 0
}

func perHour(count int64, window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	return float64(count) / window.Hours()
}

func stale(updatedAt, now time.Time) bool {
	// A registration that never recorded a write cannot be called stalled.
	return !updatedAt.IsZero() && now.Sub(updatedAt) >= StallAge
}

func events(n int64) string {
	if n == 1 {
		return "1 event"
	}
	return fmt.Sprintf("%s events", humanCount(n))
}

// humanCount groups thousands so a six-figure backlog reads as one at a glance.
func humanCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 || len(s) <= 3 {
		return s
	}
	head := len(s) % 3
	if head == 0 {
		head = 3
	}
	out := s[:head]
	for i := head; i < len(s); i += 3 {
		out += "," + s[i:i+3]
	}
	return out
}

// roundDuration renders a span the way someone says it out loud, so a message
// reads "24h" and "3d" rather than "24h0m0s" and "72h0m0s".
func roundDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0fd", d.Hours()/24)
	case d >= time.Hour:
		if m := int(d.Minutes()) % 60; m != 0 {
			return fmt.Sprintf("%dh%dm", int(d.Hours()), m)
		}
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	default:
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
