// Package bus is attn's durable event bus: the internal spine that carries
// domain facts from whatever produced them to everything that reacts.
//
// A fact is a name, a subject, and a small payload — "this session changed
// state", "this ticket was commented on". It is NOT a WebSocket message. Most of
// what the daemon currently pushes to clients is a whole-list snapshot, and a log
// of snapshots would be a fat stream of UI invalidations that tells a subscriber
// only that something changed. Facts stay small, stay durable, and are worth
// subscribing to. Turning a fact into whatever the wire needs — frequently a
// snapshot re-push — is the consumer's job, not the publisher's.
//
// Two kinds of consumer:
//
//   - Durable consumers register by name and hold a persisted cursor. Delivery is
//     strict seq order, one event in flight, cursor advanced after the handler
//     returns: at-least-once. A consumer that was down catches up from its
//     bookmark. A handler that keeps failing stalls its own consumer loudly
//     rather than skipping the event.
//   - Ephemeral subscribers hold no cursor and start at head. The WebSocket hub
//     is one: clients already refetch on reconnect, so replaying history into a
//     hub would duplicate work the frontend already does.
//
// What does NOT belong here: byte streams. PTY output, attach traffic, and tile
// content keep their direct paths. They are high volume, and attach routing is
// per-client predicate matching, which pub/sub cannot express.
//
// The package accepts a Store and a LogFunc at construction and MUST NOT import
// internal/daemon (the daemon imports this package and adapts internal/store to
// the Store seam, exactly as it does for internal/jobs).
//
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
	// DefaultRetention is the age window for the durable log. Events older than
	// this are eligible for trimming, subject to the enabled-consumer cursor floor.
	DefaultRetention = 30 * 24 * time.Hour
	// DefaultTrimInterval is how often retention runs.
	DefaultTrimInterval = time.Hour
	// DefaultBatchSize bounds one read of the forward stream.
	DefaultBatchSize = 200
	// DefaultPollInterval is the safety-net wake for a durable consumer. Delivery
	// is normally driven by publish notifications; this bounds how long a missed
	// notification, or an externally flipped enabled bit, can go unnoticed.
	DefaultPollInterval = 5 * time.Second
	// DefaultRetryBase / DefaultRetryCap bound the capped-exponential retry of a
	// failing handler.
	DefaultRetryBase = time.Second
	DefaultRetryCap  = 2 * time.Minute
)

// ErrAlreadyStarted is returned by Register once Start has run: the set of
// durable consumers is fixed at startup, so a delivery loop always exists for
// every registration.
var ErrAlreadyStarted = errors.New("bus: consumers must be registered before Start")

// LogFunc matches the daemon's injected logger shape (see internal/jobs,
// internal/pty). Runtime logging goes through it — never log.Printf, whose stderr
// is discarded when the daemon runs in the background.
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

// Store is the persistence seam. internal/store implements the equivalent
// methods; the daemon adapts them so neither package imports the other.
type Store interface {
	Append(e Event, now time.Time) (int64, error)
	Since(cursor int64, limit int) ([]Event, error)
	Bounds() (earliest, head int64, err error)
	GetConsumer(name string) (Consumer, bool, error)
	SaveConsumer(c Consumer, now time.Time) error
	SetCursor(name string, cursor int64, now time.Time) error
	ListConsumers() ([]Consumer, error)
	Trim(cutoff time.Time) (int, error)
	// Compact keeps only the newest fact per subject among the named ones, and
	// only at or below floor. Which names those are is decided here (Options.
	// Compactable); the SQL lives in the store, the same split Trim uses.
	Compact(names []string, floor int64) (int, error)
	// Size reports how many facts the log holds and how many bytes of event text
	// they carry — the receipt that says whether the log is outgrowing the data
	// it describes.
	Size() (rows, bytes int64, err error)
}

// Handler receives one event. Returning an error stalls the consumer on that
// event and retries with backoff; the cursor does not advance, so the event is
// redelivered. Handlers must therefore tolerate redelivery.
type Handler func(ctx context.Context, ev Event) error

// Options configures a Bus. Every duration is optional and falls back to the
// package defaults.
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
	// Compactable names the fact classes whose log rows are pure invalidations,
	// so the retention pass may keep only the newest one per subject. See
	// Bus.Trim for what that costs a consumer and why it is sound.
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
	// observe events in seq order. Durable consumers get ordering from their
	// cursor and do not need it.
	publishMu sync.Mutex
	// marked says the announce mark has been placed from a real log head. Until
	// it is, the mark's zero value would mean "announce the whole log", so
	// announcing is held back rather than replaying history at every client.
	marked bool
	// announced is the highest seq already fanned out to ephemeral subscribers,
	// guarded by publishMu. Both entry points — Publish and Announce — read
	// forward from it, which is what keeps events appended by somebody else's
	// transaction in seq order with events this bus appended itself.
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

// New builds a Bus. A nil Store yields a bus that accepts publishes and drops
// them, mirroring the store's own nil-db convention so a daemon without a
// database still runs.
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
	// The announce mark starts at head, so a bus attached to an existing log
	// announces what happens next rather than replaying its history. It is set
	// here rather than in Start because a Bus that is never started still
	// publishes and announces — that is the shape of every test daemon — and one
	// that replayed a month of facts into the wire on its first write would be a
	// worse failure than a missed one.
	if b.store != nil {
		b.markHead()
	}
	return b
}

// Publish appends a fact and returns its seq. Payload may be nil (a fact whose
// subject says everything) or any JSON-marshalable value.
//
// The write is synchronous: the caller learns the fact is durable, and the seq is
// assigned under the same lock that orders ephemeral delivery. This is affordable
// because the bus carries facts, not streams.
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

	// Without a store the fact still HAPPENED — only its durability is missing.
	// Live subscribers are delivered to either way, so a bus configured without a
	// database degrades to fan-out rather than to silence.
	if b.store == nil {
		b.fanoutEphemeral(ev)
		return 0, nil
	}

	seq, err := b.store.Append(ev, now)
	if err != nil {
		// Same degradation as the store-less case above, and for the same reason.
		// These projections were direct broadcasts before the bus existed; a client
		// must not miss a state change because the durable log had a bad night. The
		// caller still learns durability was lost.
		b.fanoutEphemeral(ev)
		return 0, fmt.Errorf("bus: appending %s: %w", ev.Name, err)
	}
	ev.Seq = seq

	// Fan out by reading the log forward rather than by handing subscribers the
	// event in hand. The two are the same message whenever this publish is the
	// only writer, and they are not the same ORDER when they are not: a fact
	// appended inside somebody else's transaction (see the document store's
	// composite write) may sit below this seq and still be unannounced, and
	// delivering this one first would show subscribers the log out of order.
	// Reading forward makes the log the ordering authority, whoever wrote to it.
	b.announceLocked(&ev)
	b.wakeDurables()
	return seq, nil
}

// Announce fans out facts appended to the log outside Publish — by a store
// transaction that committed a change and its fact together — so ephemeral
// subscribers see them in seq order beside everything else.
//
// It reads forward from the announce mark under the same lock Publish holds,
// which makes it idempotent and order-correct no matter who calls it when: two
// writers that commit and then race to announce cannot deliver out of order,
// and an announce that never happens is repaired by the next one. Callers
// therefore need no coordination beyond calling it after their commit.
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
// learned where the log stands. A mark that was never set is 0, which means
// "announce everything" — so until this succeeds the bus must not announce at
// all, or the first write would replay the whole log into every live client.
// Construction calls it once; announceLocked retries it, because a database
// that could not be read at construction is usually readable a moment later.
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
	// Without a mark there is no "everything after here" to deliver, only the
	// whole log. Deliver the caller's own event, which is what it would have
	// gotten before reading forward existed, and try again next time.
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

// Register adds a durable consumer. It must be called before Start.
//
// A consumer new to the store begins at head: registering a consumer is not a
// request to replay history. An existing consumer keeps its cursor and its
// enabled bit — restarting the daemon must neither rewind a consumer nor
// resurrect one the operator killed.
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
// subscriber holds no cursor, starts at head, and is invoked inline on the
// publishing goroutine in seq order — so its function must be cheap and must not
// publish back onto the bus.
//
// Seq is 0 when the fact could not be made durable (no store, or a failed
// append). Ephemeral delivery is deliberately not conditional on durability:
// these subscribers project onto the wire, and the wire must not go quiet
// because the log did. A subscriber that cares must check Seq.
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
		// From now: a fresh consumer is not asking for the backlog.
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
	// The enabled bit is the kill switch and lives only in the database, so it is
	// re-read here rather than cached for the process lifetime.
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

	// A consumer that is behind a busy producer never leaves the loop below, so
	// re-reading the kill switch once per drain would leave it unreachable for as
	// long as the burst lasts. Re-read it on the poll interval instead, which is
	// the bound DefaultPollInterval documents.
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
				// Unwanted events still advance the cursor, batched into one write
				// at the next matching event or at the end of the batch.
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
			// Backoff escalates for an event the handler cannot get past, not over
			// a consumer's lifetime: a delivery that succeeds ends the streak, so a
			// consumer that is merely lagging does not ratchet to the retry cap on
			// occasional transient failures.
			d.clearFailure()
		}
		if skipped != 0 {
			if err := b.advance(d, skipped); err != nil {
				return err
			}
		}
	}
}

// reconcileGap handles a consumer whose cursor sits below the oldest surviving
// event: retention trimmed past it while it was disabled or dead. It resumes at
// head — replaying a month of backlog into a just-revived consumer would be worse
// than the gap — and says so.
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
			// A failed pass is already logged; the next tick retries it.
			_, _ = b.Trim()
		}
	}
}

// Trim runs one retention pass and reports how many events it removed, by both
// halves: the age window, and compaction of the fact classes that carry no
// history. It is exported so the daemon and tests can force a pass without
// waiting for a tick.
// It reports what it removed and whether either half failed. The daemon's tick
// logs the failure and carries on — a pass that could not run is retried an hour
// later — but a caller that ASKED for a pass has to be able to tell a pass that
// removed nothing from one that never happened, or a script reads "removed 0"
// and concludes the log is already clean.
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

// compact keeps at most one fact per subject for every compactable name, which
// is what bounds the log by the size of the data it describes rather than by
// how often that data is written.
//
// A compactable fact is an invalidation: it says a subject changed, and the
// state itself lives in the store, so five of them about one subject carry no
// more than the newest. The consequence, stated rather than hidden: for these
// names durable delivery is at-least-once PER CHANGED SUBJECT, not per write. A
// consumer that was behind while a document changed five times learns once that
// it changed, and reads current state — which is what a consumer of this bus is
// told to do anyway. A workload that needs every intermediate change as a
// business event is doing event sourcing, and those events are data: they
// belong in a collection the workload writes, where their growth is its own
// visible cost.
//
// The cursor floor is the same one trimming honors, for two load-bearing
// reasons. An enabled consumer must never lose a fact it has not read, and
// below the floor every enabled consumer has read everything, so their delivery
// is bit-for-bit what it is today. And reconcileGap reads "cursor below the
// earliest surviving seq" as "everything missing was trimmed"; compacting above
// the floor would punch holes that assumption misreads and skip a revived
// consumer past facts that still exist. A stalled enabled consumer therefore
// pins compaction exactly as it pins trimming — a loud condition already, with
// `attn bus status` showing the stall and the kill switch to unpin it.
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

// consumerFloor is the lowest position every enabled consumer has passed. With
// no enabled consumer registered it is the log head: nobody is owed anything, so
// nothing is pinned. Disabled consumers are excluded for the same reason they
// are excluded from trimming — a killed consumer must not pin the log.
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

// ConsumerStatus is one row of Status.
type ConsumerStatus struct {
	Name    string
	Cursor  int64
	Lag     int64
	Filter  string
	Enabled bool
	// Live is true when this process has a delivery loop for the consumer. A
	// registered-but-not-live consumer is one whose owner is gone.
	Live bool
	// Stalled carries the current failure message when a handler is failing.
	Stalled string
}

// Status reports the log head and every registration, for operator inspection.
//
// Rows and Bytes are the log's actual weight rather than head-minus-earliest,
// which only ever described the seq space. They are the receipt for the
// invariant compaction upholds — the log stays proportional to the data it
// describes, never to how often that data is written — so a workload that
// stresses it shows up as a measurement instead of a suspicion.
type Status struct {
	Head      int64
	Earliest  int64
	Rows      int64
	Bytes     int64
	Consumers []ConsumerStatus
}

// Status snapshots the bus for `attn bus status`.
func (b *Bus) Status() (Status, error) {
	if b.store == nil {
		return Status{}, nil
	}
	earliest, head, err := b.store.Bounds()
	if err != nil {
		return Status{}, err
	}
	rows, err := b.store.ListConsumers()
	if err != nil {
		return Status{}, err
	}
	logRows, logBytes, err := b.store.Size()
	if err != nil {
		return Status{}, err
	}

	b.mu.Lock()
	live := make(map[string]*durable, len(b.durables))
	for _, d := range b.durables {
		live[d.name] = d
	}
	b.mu.Unlock()

	out := Status{Head: head, Earliest: earliest, Rows: logRows, Bytes: logBytes}
	for _, r := range rows {
		cs := ConsumerStatus{
			Name:    r.Name,
			Cursor:  r.Cursor,
			Lag:     head - r.Cursor,
			Filter:  r.Filter,
			Enabled: r.Enabled,
		}
		if cs.Lag < 0 {
			cs.Lag = 0
		}
		if d, ok := live[r.Name]; ok {
			cs.Live = true
			cs.Stalled = d.stallReason()
		}
		out.Consumers = append(out.Consumers, cs)
	}
	return out, nil
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
