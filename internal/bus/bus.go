// Package bus is attn's durable event bus, carrying domain facts (name,
// subject, small payload — never byte streams like PTY output or attach
// traffic). Durable consumers get strict seq order, at-least-once delivery from
// a persisted cursor; ephemeral subscribers start at head with no cursor.
// MUST NOT import internal/daemon (the daemon imports this package and adapts
// internal/store to the Store seam).
// See docs/plans/2026-08-01-ext-a1-event-bus.md.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultRetention is the trim age window, floored by enabled-consumer cursors.
	DefaultRetention = 30 * 24 * time.Hour
	// DefaultTrimInterval is how often retention runs.
	DefaultTrimInterval = time.Hour
	// DefaultBatchSize bounds one read of the forward stream.
	DefaultBatchSize = 200
	// DefaultPollInterval bounds how long a missed publish notification, or an
	// externally flipped enabled bit, can go unnoticed.
	DefaultPollInterval = 5 * time.Second
	// DefaultRetryBase / DefaultRetryCap bound the capped-exponential retry of a
	// failing handler.
	DefaultRetryBase = time.Second
	DefaultRetryCap  = 2 * time.Minute
)

// ErrAlreadyStarted is returned by Register once Start has run: the set of
// durable consumers is fixed at startup.
var ErrAlreadyStarted = errors.New("bus: consumers must be registered before Start")

// LogFunc is the daemon's injected logger shape. All runtime logging goes
// through it — never log.Printf, whose stderr is discarded in the background.
type LogFunc func(format string, args ...interface{})

// Event is one fact on the log.
type Event struct {
	Seq       int64
	Name      string
	Subject   string
	Payload   json.RawMessage
	Source    string
	CreatedAt time.Time
}

// Decode unmarshals the payload into v. A fact with no payload leaves v untouched.
func (e Event) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

// Consumer is a durable consumer's persisted registration and position.
type Consumer struct {
	Name      string
	Cursor    int64
	Filter    string
	Enabled   bool
	UpdatedAt time.Time
}

// Store is the persistence seam; the daemon adapts internal/store to it so
// neither package imports the other.
type Store interface {
	Append(e Event, now time.Time) (int64, error)
	Since(cursor int64, limit int) ([]Event, error)
	Bounds() (earliest, head int64, err error)
	GetConsumer(name string) (Consumer, bool, error)
	SaveConsumer(c Consumer, now time.Time) error
	SetCursor(name string, cursor int64, now time.Time) error
	ListConsumers() ([]Consumer, error)
	Trim(cutoff time.Time) (int, error)
	// Compact keeps only the newest fact per subject among the named ones, at or
	// below floor.
	Compact(names []string, floor int64) (int, error)
	// Producers reports every fact class with its totals and its counts at or
	// after each cutoff, loudest first. It also carries the log's row count and
	// bytes, summed across classes — one pass answers both questions.
	Producers(cutoffs []time.Time) ([]ProducerRow, error)
	// EventTimeAt stamps the first event at or above seq, for age questions.
	EventTimeAt(seq int64) (time.Time, bool, error)
}

// Handler receives one event. An error stalls the consumer with backoff and
// redelivers the event; handlers must tolerate redelivery.
type Handler func(ctx context.Context, ev Event) error

// Options configures a Bus; zero durations fall back to the package defaults.
type Options struct {
	Store        Store
	Log          LogFunc
	Now          func() time.Time
	Retention    time.Duration
	TrimInterval time.Duration
	BatchSize    int
	PollInterval time.Duration
	RetryBase    time.Duration
	RetryCap     time.Duration
	// Compactable names fact classes that are pure invalidations, so retention
	// may keep only the newest per subject (see compact for the cost).
	Compactable []string
}

// Bus is the event bus. The zero value is not usable; construct with New.
type Bus struct {
	store Store
	log   LogFunc
	now   func() time.Time

	retention    time.Duration
	trimInterval time.Duration
	batchSize    int
	pollInterval time.Duration
	retryBase    time.Duration
	retryCap     time.Duration

	compactable []string

	// publishMu serializes append + ephemeral fan-out so ephemeral subscribers
	// observe events in seq order.
	publishMu sync.Mutex
	// marked says the announce mark was placed from a real log head. An unset
	// mark reads as "announce the whole log", so announcing is held back until it
	// is placed.
	marked bool
	// announced is the highest seq fanned out to ephemeral subscribers, guarded
	// by publishMu; both Publish and Announce read the log forward from it.
	announced int64

	mu        sync.Mutex
	durables  []*durable
	ephemeral map[int]*ephemeralSub
	nextSubID int
	started   bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type durable struct {
	name    string
	filter  Filter
	handler Handler

	wake chan struct{}

	mu       sync.Mutex
	cursor   int64
	enabled  bool
	stalled  string
	failures int
}

type ephemeralSub struct {
	filter Filter
	fn     func(Event)
}

// New builds a Bus. A nil Store yields a bus that fans out publishes without
// durability, mirroring the store's nil-db convention.
func New(opts Options) *Bus {
	b := &Bus{
		store:        opts.Store,
		log:          opts.Log,
		now:          opts.Now,
		retention:    nonZeroDuration(opts.Retention, DefaultRetention),
		trimInterval: nonZeroDuration(opts.TrimInterval, DefaultTrimInterval),
		batchSize:    nonZeroInt(opts.BatchSize, DefaultBatchSize),
		pollInterval: nonZeroDuration(opts.PollInterval, DefaultPollInterval),
		retryBase:    nonZeroDuration(opts.RetryBase, DefaultRetryBase),
		retryCap:     nonZeroDuration(opts.RetryCap, DefaultRetryCap),
		compactable:  append([]string(nil), opts.Compactable...),
		ephemeral:    map[int]*ephemeralSub{},
	}
	if b.now == nil {
		b.now = time.Now
	}
	if b.log == nil {
		b.log = func(string, ...interface{}) {}
	}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	// Mark at construction, not Start: a never-started Bus still publishes and
	// announces, and an unplaced mark would replay the whole log on first write.
	if b.store != nil {
		b.markHead()
	}
	return b
}

// Publish appends a fact and returns its seq. Payload may be nil or any
// JSON-marshalable value. The write is synchronous: the caller learns the fact
// is durable.
func (b *Bus) Publish(name, subject string, payload any) (int64, error) {
	return b.publish(Event{Name: name, Subject: subject}, payload)
}

// PublishFrom is Publish with an explicit source label for diagnosis.
func (b *Bus) PublishFrom(source, name, subject string, payload any) (int64, error) {
	return b.publish(Event{Name: name, Subject: subject, Source: source}, payload)
}

func (b *Bus) publish(ev Event, payload any) (int64, error) {
	if strings.TrimSpace(ev.Name) == "" {
		return 0, errors.New("bus: publish requires an event name")
	}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("bus: marshaling payload for %s: %w", ev.Name, err)
		}
		ev.Payload = raw
	}
	b.publishMu.Lock()
	defer b.publishMu.Unlock()

	now := b.now()
	ev.CreatedAt = now

	// No store: degrade to fan-out rather than silence.
	if b.store == nil {
		b.fanoutEphemeral(ev)
		return 0, nil
	}

	seq, err := b.store.Append(ev, now)
	if err != nil {
		// A failed append must not silence the wire; the caller still learns
		// durability was lost.
		b.fanoutEphemeral(ev)
		return 0, fmt.Errorf("bus: appending %s: %w", ev.Name, err)
	}
	ev.Seq = seq

	// Fan out by reading the log forward, not by delivering the event in hand: a
	// fact appended by somebody else's transaction may sit below this seq still
	// unannounced, and delivering this one first would break seq order.
	b.announceLocked(&ev)
	b.wakeDurables()
	return seq, nil
}

// Announce fans out facts appended to the log outside Publish (store
// transactions that commit a change and its fact together). Idempotent and
// order-correct: callers just call it after their commit, and a missed announce
// is repaired by the next one.
func (b *Bus) Announce() {
	if b.store == nil {
		return
	}
	b.publishMu.Lock()
	defer b.publishMu.Unlock()
	b.announceLocked(nil)
	b.wakeDurables()
}

// markHead sets the announce mark to the log's head and reports whether it
// succeeded. Until it does the bus must not announce at all — an unset mark
// would replay the whole log. announceLocked retries it after a failed
// construction-time call.
func (b *Bus) markHead() bool {
	_, head, err := b.store.Bounds()
	if err != nil {
		b.log("bus: reading log bounds to place the announce mark: %v", err)
		return false
	}
	b.announced = head
	b.marked = true
	return true
}

// announceLocked delivers everything above the announce mark. fallback is the
// event the caller just appended, delivered directly if the log cannot be read
// — losing durability must not also silence the wire.
func (b *Bus) announceLocked(fallback *Event) {
	if !b.marked && !b.markHead() {
		if fallback != nil {
			b.fanoutEphemeral(*fallback)
		}
		return
	}
	for {
		events, err := b.store.Since(b.announced, b.batchSize)
		if err != nil {
			b.log("bus: reading the log forward from seq %d to announce: %v", b.announced, err)
			if fallback != nil && fallback.Seq > b.announced {
				b.fanoutEphemeral(*fallback)
				b.announced = fallback.Seq
			}
			return
		}
		if len(events) == 0 {
			return
		}
		for _, ev := range events {
			b.fanoutEphemeral(ev)
			b.announced = ev.Seq
		}
		if len(events) < b.batchSize {
			return
		}
	}
}

func (b *Bus) fanoutEphemeral(ev Event) {
	b.mu.Lock()
	subs := make([]*ephemeralSub, 0, len(b.ephemeral))
	for _, s := range b.ephemeral {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	for _, s := range subs {
		if s.filter.Matches(ev.Name) {
			s.fn(ev)
		}
	}
}

func (b *Bus) wakeDurables() {
	b.mu.Lock()
	ds := append([]*durable(nil), b.durables...)
	b.mu.Unlock()

	for _, d := range ds {
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
}

// Register adds a durable consumer; must be called before Start. A new
// consumer begins at head; an existing one keeps its cursor and enabled bit —
// a restart must neither rewind a consumer nor resurrect a killed one.
func (b *Bus) Register(name string, filter Filter, h Handler) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("bus: consumer name is required")
	}
	if h == nil {
		return fmt.Errorf("bus: consumer %s needs a handler", name)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return ErrAlreadyStarted
	}
	for _, d := range b.durables {
		if d.name == name {
			return fmt.Errorf("bus: consumer %s already registered", name)
		}
	}
	b.durables = append(b.durables, &durable{
		name:    name,
		filter:  filter,
		handler: h,
		wake:    make(chan struct{}, 1),
		enabled: true,
	})
	return nil
}

// Subscribe adds an ephemeral subscriber and returns its cancel function. The
// function runs inline on the publishing goroutine — it must be cheap and must
// not publish back onto the bus (publishMu is held: deadlock). Seq is 0 when
// the fact could not be made durable; a subscriber that cares must check.
func (b *Bus) Subscribe(filter Filter, fn func(Event)) func() {
	if fn == nil {
		return func() {}
	}
	b.mu.Lock()
	id := b.nextSubID
	b.nextSubID++
	b.ephemeral[id] = &ephemeralSub{filter: filter, fn: fn}
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		delete(b.ephemeral, id)
		b.mu.Unlock()
	}
}

// Start persists every registration and launches one delivery loop per durable
// consumer, plus the retention loop.
func (b *Bus) Start() error {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return nil
	}
	b.started = true
	ds := append([]*durable(nil), b.durables...)
	b.mu.Unlock()

	if b.store == nil {
		return nil
	}

	_, head, err := b.store.Bounds()
	if err != nil {
		return fmt.Errorf("bus: reading log bounds: %w", err)
	}

	for _, d := range ds {
		if err := b.initConsumer(d, head); err != nil {
			return err
		}
		b.wg.Add(1)
		go func(d *durable) {
			defer b.wg.Done()
			b.deliver(d)
		}(d)
	}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.retain()
	}()
	return nil
}

// initConsumer creates or reconciles the persisted registration for d.
func (b *Bus) initConsumer(d *durable, head int64) error {
	existing, ok, err := b.store.GetConsumer(d.name)
	if err != nil {
		return fmt.Errorf("bus: loading consumer %s: %w", d.name, err)
	}
	now := b.now()
	if !ok {
		if err := b.store.SaveConsumer(Consumer{
			Name:    d.name,
			Cursor:  head,
			Filter:  d.filter.String(),
			Enabled: true,
		}, now); err != nil {
			return fmt.Errorf("bus: registering consumer %s: %w", d.name, err)
		}
		d.setPosition(head, true)
		return nil
	}
	// SaveConsumer refreshes the filter but preserves cursor and enabled.
	if err := b.store.SaveConsumer(Consumer{
		Name:    d.name,
		Cursor:  existing.Cursor,
		Filter:  d.filter.String(),
		Enabled: existing.Enabled,
	}, now); err != nil {
		return fmt.Errorf("bus: updating consumer %s: %w", d.name, err)
	}
	d.setPosition(existing.Cursor, existing.Enabled)
	return nil
}

// Stop cancels delivery and waits for the loops to exit. In-flight handlers see
// a cancelled context; their cursor is not advanced, so an interrupted event is
// redelivered on the next start.
func (b *Bus) Stop() {
	b.cancel()
	b.wg.Wait()
}

// deliver is one durable consumer's loop.
func (b *Bus) deliver(d *durable) {
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	retry := time.Duration(0)
	for {
		if retry > 0 {
			timer := time.NewTimer(retry)
			select {
			case <-b.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			retry = 0
		} else {
			select {
			case <-b.ctx.Done():
				return
			case <-d.wake:
			case <-ticker.C:
			}
		}

		failures := d.drainFailures()
		if err := b.drain(d); err != nil {
			if b.ctx.Err() != nil {
				return
			}
			retry = backoff(b.retryBase, b.retryCap, failures+1)
			d.recordFailure(err.Error(), failures+1)
			b.log("bus: consumer %s stalled at seq %d (attempt %d, retry in %s): %v",
				d.name, d.position()+1, failures+1, retry, err)
		}
	}
}

// drain reads forward from the consumer's cursor until the log is exhausted.
func (b *Bus) drain(d *durable) error {
	// The enabled bit is the kill switch and lives only in the database; re-read
	// it, never cache it for the process lifetime.
	rec, ok, err := b.store.GetConsumer(d.name)
	if err != nil {
		return fmt.Errorf("reading registration: %w", err)
	}
	if !ok {
		return fmt.Errorf("registration for %s disappeared", d.name)
	}
	d.setPosition(rec.Cursor, rec.Enabled)
	if !rec.Enabled {
		return nil
	}

	if err := b.reconcileGap(d); err != nil {
		return err
	}

	// A lagging consumer never leaves the loop below, so the kill switch is
	// re-read on the poll interval, not once per drain.
	lastCheck := b.now()
	killed := func() (bool, error) {
		if b.now().Sub(lastCheck) < b.pollInterval {
			return false, nil
		}
		lastCheck = b.now()
		rec, ok, err := b.store.GetConsumer(d.name)
		if err != nil {
			return false, fmt.Errorf("reading registration: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("registration for %s disappeared", d.name)
		}
		d.setEnabled(rec.Enabled)
		return !rec.Enabled, nil
	}

	for {
		if b.ctx.Err() != nil {
			return nil
		}
		events, err := b.store.Since(d.position(), b.batchSize)
		if err != nil {
			return fmt.Errorf("reading log: %w", err)
		}
		if len(events) == 0 {
			d.clearFailure()
			return nil
		}

		var skipped int64
		for _, ev := range events {
			if b.ctx.Err() != nil {
				return nil
			}
			stop, err := killed()
			if err != nil {
				return err
			}
			if stop {
				if skipped != 0 {
					return b.advance(d, skipped)
				}
				return nil
			}
			if !d.filter.Matches(ev.Name) {
				// Unwanted events still advance the cursor, batched into one write.
				skipped = ev.Seq
				continue
			}
			if skipped != 0 {
				if err := b.advance(d, skipped); err != nil {
					return err
				}
				skipped = 0
			}
			if err := d.handler(b.ctx, ev); err != nil {
				if b.ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("handling %s (seq %d): %w", ev.Name, ev.Seq, err)
			}
			if err := b.advance(d, ev.Seq); err != nil {
				return err
			}
			// A successful delivery ends the failure streak, so a lagging consumer
			// does not ratchet to the retry cap on transient failures.
			d.clearFailure()
		}
		if skipped != 0 {
			if err := b.advance(d, skipped); err != nil {
				return err
			}
		}
	}
}

// reconcileGap handles a cursor below the oldest surviving event (retention
// trimmed past it while disabled or dead): resume at head, with a logged gap.
func (b *Bus) reconcileGap(d *durable) error {
	earliest, head, err := b.store.Bounds()
	if err != nil {
		return fmt.Errorf("reading log bounds: %w", err)
	}
	if earliest == 0 || d.position() >= earliest-1 {
		return nil
	}
	missed := earliest - 1 - d.position()
	b.log("bus: consumer %s resumed at head %d; %d event(s) were trimmed before its cursor %d",
		d.name, head, missed, d.position())
	return b.advance(d, head)
}

func (b *Bus) advance(d *durable, seq int64) error {
	if err := b.store.SetCursor(d.name, seq, b.now()); err != nil {
		return fmt.Errorf("persisting cursor: %w", err)
	}
	d.setCursor(seq)
	return nil
}

// retain runs the retention window.
func (b *Bus) retain() {
	ticker := time.NewTicker(b.trimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			_, _ = b.Trim()
		}
	}
}

// Trim runs one retention pass (age window plus compaction) and reports how
// many events it removed and whether either half failed — a caller must be able
// to tell "removed 0" from "never ran".
func (b *Bus) Trim() (int, error) {
	if b.store == nil {
		return 0, nil
	}
	var (
		removed int
		failed  error
	)
	n, err := b.store.Trim(b.now().Add(-b.retention))
	if err != nil {
		failed = fmt.Errorf("retention pass: %w", err)
		b.log("bus: retention pass failed: %v", err)
	} else {
		removed += n
		if n > 0 {
			b.log("bus: trimmed %d event(s) older than %s", n, b.retention)
		}
	}
	compacted, err := b.compact()
	if err != nil && failed == nil {
		failed = err
	}
	return removed + compacted, failed
}

// compact keeps at most one fact per subject for every compactable name,
// bounding the log by the data it describes rather than by write frequency.
// For these names durable delivery is at-least-once PER CHANGED SUBJECT, not
// per write. Compaction honors the same cursor floor as trimming: an enabled
// consumer must never lose an unread fact, and compacting above the floor would
// punch holes that reconcileGap misreads as trimmed history. A stalled enabled
// consumer pins compaction exactly as it pins trimming.
func (b *Bus) compact() (int, error) {
	if len(b.compactable) == 0 {
		return 0, nil
	}
	floor, err := b.consumerFloor()
	if err != nil {
		b.log("bus: compaction pass failed: %v", err)
		return 0, fmt.Errorf("compaction pass: %w", err)
	}
	n, err := b.store.Compact(b.compactable, floor)
	if err != nil {
		b.log("bus: compaction pass failed: %v", err)
		return 0, fmt.Errorf("compaction pass: %w", err)
	}
	if n > 0 {
		b.log("bus: compacted %d superseded event(s) at or below seq %d", n, floor)
	}
	return n, nil
}

// consumerFloor is the lowest position every enabled consumer has passed; with
// none enabled it is the log head. A killed consumer must not pin the log.
func (b *Bus) consumerFloor() (int64, error) {
	rows, err := b.store.ListConsumers()
	if err != nil {
		return 0, fmt.Errorf("listing consumers: %w", err)
	}
	floor := int64(-1)
	for _, c := range rows {
		if !c.Enabled {
			continue
		}
		if floor < 0 || c.Cursor < floor {
			floor = c.Cursor
		}
	}
	if floor >= 0 {
		return floor, nil
	}
	_, head, err := b.store.Bounds()
	if err != nil {
		return 0, fmt.Errorf("reading log bounds: %w", err)
	}
	return head, nil
}

func (d *durable) position() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cursor
}

func (d *durable) setCursor(seq int64) {
	d.mu.Lock()
	d.cursor = seq
	d.mu.Unlock()
}

func (d *durable) setPosition(cursor int64, enabled bool) {
	d.mu.Lock()
	d.cursor = cursor
	d.enabled = enabled
	d.mu.Unlock()
}

func (d *durable) setEnabled(enabled bool) {
	d.mu.Lock()
	d.enabled = enabled
	d.mu.Unlock()
}

func (d *durable) recordFailure(reason string, count int) {
	d.mu.Lock()
	d.stalled = reason
	d.failures = count
	d.mu.Unlock()
}

func (d *durable) clearFailure() {
	d.mu.Lock()
	d.stalled = ""
	d.failures = 0
	d.mu.Unlock()
}

func (d *durable) drainFailures() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.failures
}

func (d *durable) stallReason() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stalled
}

// backoff is capped exponential, matching internal/jobs' schedule.
func backoff(base, ceiling time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= ceiling {
			return ceiling
		}
	}
	if d > ceiling {
		return ceiling
	}
	return d
}

func nonZeroDuration(v, fallback time.Duration) time.Duration {
	if v <= 0 {
		return fallback
	}
	return v
}

func nonZeroInt(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
