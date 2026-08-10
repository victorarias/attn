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
	// DeleteConsumer removes a registration. Deleting one that is not there is
	// success: Unregister is an uninstall path and must be re-runnable.
	DeleteConsumer(name string) error
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

	// mu guards the consumer sets and the lifecycle bits below. Runtime
	// registration does its two store reads under it: registering is a rare
	// lifecycle event, and the alternative is a window where a consumer is in the
	// set with no row behind it, or has a row with nobody serving it.
	mu        sync.Mutex
	durables  []*durable
	ephemeral map[int]*ephemeralSub
	nextSubID int
	started   bool
	// stopped is set before Stop cancels, so a registration racing shutdown does
	// not add to wg while Stop is already waiting on it.
	stopped bool
	// retiring holds the names Unregister is between removing and deleting the row
	// for. See Register for what taking one of them back early would cost.
	retiring map[string]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type durable struct {
	name    string
	handler Handler

	wake chan struct{}

	// ctx is a child of the bus context, so Stop cancels every consumer and
	// Unregister cancels exactly one. Both of the delivery loop's waits — the
	// retry sleep and the idle select — watch it, which is what lets an
	// unregister interrupt a loop parked behind the two-minute retry cap instead
	// of waiting it out.
	ctx    context.Context
	cancel context.CancelFunc
	// done is closed when this consumer's delivery loop returns; Unregister waits
	// on it before deleting the row. launched says a loop was ever started, and
	// is guarded by the bus mutex — a consumer registered before Start and
	// unregistered before it has no loop to wait for.
	done     chan struct{}
	launched bool

	mu sync.Mutex
	// filter is under the same lock as the position because SetFilter changes it
	// while the delivery loop is reading it — an app's subscriptions move when a
	// new version is applied, and the loop must not be racing the change.
	filter   Filter
	cursor   int64
	enabled  bool
	stalled  string
	failures int
	// retired is set by Unregister before the row goes. It drops a late result
	// from a handler that was in flight — a cursor advance or a failure record
	// against a registration that no longer exists is a no-op, not an error.
	retired bool
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
		retiring:     map[string]struct{}{},
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

// Register adds a durable consumer, before or after Start: called after, it
// persists the registration and starts the delivery loop immediately — an app
// installed while the daemon runs must not wait for a restart. A new consumer
// begins at head; an existing one keeps its cursor and enabled bit — a restart
// must neither rewind a consumer nor resurrect a killed one. A registration
// that fails to persist leaves nothing behind, so the caller can retry.
func (b *Bus) Register(name string, filter Filter, h Handler) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("bus: consumer name is required")
	}
	if h == nil {
		return fmt.Errorf("bus: consumer %s needs a handler", name)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.durables {
		if d.name == name {
			return fmt.Errorf("bus: consumer %s already registered", name)
		}
	}
	// A name being unregistered is claimed until its row is gone. Taking it back
	// inside that window would resume the outgoing consumer's cursor from a row
	// that is about to be deleted under the new loop — which then drains against a
	// registration that disappeared and retries that error forever. Same zombie the
	// delete-last ordering exists to prevent, through the side door.
	if _, retiring := b.retiring[name]; retiring {
		return fmt.Errorf("bus: consumer %s is being unregistered; retry once it is gone", name)
	}
	d := b.newDurable(name, filter, h)

	// Before Start, the registration is all there is to do: Start persists every
	// consumer and launches its loop. The same is true for a bus with no store,
	// which never delivers durably at all.
	if !b.started || b.store == nil {
		b.durables = append(b.durables, d)
		return nil
	}

	_, head, err := b.store.Bounds()
	if err != nil {
		d.cancel()
		return fmt.Errorf("bus: reading log bounds to register %s: %w", name, err)
	}
	if err := b.initConsumer(d, head); err != nil {
		d.cancel()
		return err
	}
	b.durables = append(b.durables, d)
	b.launchLocked(d)
	return nil
}

// Unregister stops a consumer's delivery loop and deletes its persisted row. It
// is the way out of Register, and the reason it exists at all: an abandoned
// enabled row holds the retention floor down forever, against a consumer nobody
// serves.
//
// It is idempotent. A name this process never registered — an orphan row left by
// an earlier daemon — is still deleted, and a name that does not exist at all is
// success. The caller is an uninstall path, and an uninstall that fails the
// second time it runs is a worse surface than one that says nothing.
//
// The order is cancel, wait for the loop to exit, then delete the row, and it is
// load-bearing in both directions. Deleting first would leave the live loop's
// next drain reading a registration that disappeared — an error path that records
// a failure and retries forever, a zombie. Returning before the loop exits would
// hand the caller a consumer that is still delivering. The wait is unbounded for
// the same reason Stop's is: a handler that ignores its cancelled context is a
// bug in the handler, and a retired consumer's late cursor advance is already
// dropped, so waiting costs nothing a correct handler will notice.
//
// The name stays claimed for the whole of it, so a Register that arrives while the
// loop is winding down is refused rather than served from a row about to be
// deleted underneath it.
func (b *Bus) Unregister(name string) error {
	b.mu.Lock()
	var (
		found *durable
		wait  bool
	)
	for i, d := range b.durables {
		if d.name != name {
			continue
		}
		found = d
		wait = d.launched
		b.durables = append(b.durables[:i], b.durables[i+1:]...)
		break
	}
	b.retiring[name] = struct{}{}
	b.mu.Unlock()
	// Released whatever happens, including a failed delete: a surviving row is the
	// ordinary resume-from-cursor case, and the caller already has the error.
	defer func() {
		b.mu.Lock()
		delete(b.retiring, name)
		b.mu.Unlock()
	}()

	if found != nil {
		found.retire()
		found.cancel()
		if wait {
			<-found.done
		}
	}

	if b.store == nil {
		return nil
	}
	if err := b.store.DeleteConsumer(name); err != nil {
		return fmt.Errorf("bus: deleting consumer %s: %w", name, err)
	}
	return nil
}

// SetFilter changes what a registered consumer receives, keeping its cursor and
// its enabled bit.
//
// It exists because a consumer's subscriptions can outlive neither the consumer
// nor its position: an app declares its event patterns in its manifest, and
// applying a new version can change them. Unregister-then-Register would express
// the same intent and delete the cursor on the way through — the app would resume
// at head and silently skip everything published while it was being updated.
// Register refuses a name it already serves, so this is the only way to say "same
// consumer, different subscriptions".
//
// A name that is not registered here is an error rather than a silent no-op: the
// caller believes it is changing a live consumer's behavior, and learning
// otherwise from later missing deliveries is the worst way to find out.
func (b *Bus) SetFilter(name string, filter Filter) error {
	b.mu.Lock()
	var found *durable
	for _, d := range b.durables {
		if d.name == name {
			found = d
			break
		}
	}
	started := b.started
	b.mu.Unlock()
	if found == nil {
		return fmt.Errorf("bus: consumer %s is not registered, so its filter cannot be changed", name)
	}
	// Persist first, then swap what the loop reads. The other order would have the
	// loop filtering by a rule no restart would reproduce if the write failed.
	//
	// Before Start there is nothing persisted to correct: Register only holds the
	// consumer in memory, and Start writes every one of them with the filter it
	// carries at that moment — which is this one.
	if b.store != nil && started {
		existing, ok, err := b.store.GetConsumer(name)
		if err != nil {
			return fmt.Errorf("bus: reading consumer %s to change its filter: %w", name, err)
		}
		if !ok {
			return fmt.Errorf("bus: consumer %s has no registration to change", name)
		}
		if err := b.store.SaveConsumer(Consumer{
			Name:    name,
			Cursor:  existing.Cursor,
			Filter:  filter.String(),
			Enabled: existing.Enabled,
		}, b.now()); err != nil {
			return fmt.Errorf("bus: saving the filter of consumer %s: %w", name, err)
		}
	}
	found.setFilter(filter)
	return nil
}

// Registered reports whether this process serves a consumer under name.
//
// It is how a caller chooses between Register and SetFilter without guessing
// from an error string: those two are not interchangeable — one mints a cursor,
// the other preserves it — and picking the wrong one is silent.
func (b *Bus) Registered(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.durables {
		if d.name == name {
			return true
		}
	}
	return false
}

// newDurable builds a consumer and its cancel scope. Callers that also touch the
// consumer set hold b.mu; the bus context it reads is fixed at construction.
func (b *Bus) newDurable(name string, filter Filter, h Handler) *durable {
	ctx, cancel := context.WithCancel(b.ctx)
	return &durable{
		name:    name,
		filter:  filter,
		handler: h,
		wake:    make(chan struct{}, 1),
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		enabled: true,
	}
}

// launchLocked starts d's delivery loop. Caller holds b.mu, which is what keeps
// the WaitGroup increment ordered against Stop: Stop sets stopped under the same
// lock before it waits, so a registration racing shutdown gets no loop rather
// than adding to a WaitGroup somebody is already waiting on.
func (b *Bus) launchLocked(d *durable) {
	if b.stopped || b.ctx.Err() != nil {
		close(d.done)
		return
	}
	d.launched = true
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer close(d.done)
		b.deliver(d)
	}()
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
	defer b.mu.Unlock()
	if b.started {
		return nil
	}
	b.started = true

	if b.store == nil {
		return nil
	}

	_, head, err := b.store.Bounds()
	if err != nil {
		return fmt.Errorf("bus: reading log bounds: %w", err)
	}

	for _, d := range b.durables {
		if err := b.initConsumer(d, head); err != nil {
			return err
		}
		b.launchLocked(d)
	}

	if b.stopped {
		return nil
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
			Filter:  d.filterExpr(),
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
		Filter:  d.filterExpr(),
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
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()

	b.cancel()
	b.wg.Wait()
}

// deliver is one durable consumer's loop. Every wait watches the consumer's own
// context, a child of the bus context: Stop reaches all of them, Unregister
// reaches this one, and neither has to wait out a retry sleep to be noticed.
func (b *Bus) deliver(d *durable) {
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	retry := time.Duration(0)
	for {
		if retry > 0 {
			timer := time.NewTimer(retry)
			select {
			case <-d.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			retry = 0
		} else {
			select {
			case <-d.ctx.Done():
				return
			case <-d.wake:
			case <-ticker.C:
			}
		}

		failures := d.drainFailures()
		if err := b.drain(d); err != nil {
			if d.ctx.Err() != nil {
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
		if d.ctx.Err() != nil {
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
			if d.ctx.Err() != nil {
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
			if !d.matches(ev.Name) {
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
			if err := d.handler(d.ctx, ev); err != nil {
				if d.ctx.Err() != nil {
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
	// A handler that was in flight when Unregister landed may still be returning
	// here. Its registration is gone, so there is no cursor to move: dropping the
	// write is the correct outcome, and reporting it as an error would turn a
	// clean uninstall into a stall on a consumer nobody serves.
	if d.isRetired() {
		return nil
	}
	if err := b.store.SetCursor(d.name, seq, b.now()); err != nil {
		return fmt.Errorf("persisting cursor: %w", err)
	}
	d.setCursor(seq)
	return nil
}

// retain runs the bus's own periodic duties: reclaim what retention allows,
// then say so when a producer is writing far more than any healthy one does.
func (b *Bus) retain() {
	ticker := time.NewTicker(b.trimInterval)
	defer ticker.Stop()
	// Report before the first tick, not an hour into the run: a producer already
	// past the ceiling is past it at startup, and a daemon restarted more often
	// than trimInterval would otherwise never say so.
	b.ReportLoudProducers()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			_, _ = b.Trim()
			b.ReportLoudProducers()
		}
	}
}

// ReportLoudProducers writes one loud log line per fact class over the tripwire,
// and nothing at all otherwise.
//
// This is the half of bus observability that does not wait to be looked at. The
// producer bug this exists for wrote two thirds of the log for a week and was
// found by accident during unrelated work; a page nobody opens would not have
// caught it either. Each line names the fact, the rate, the ceiling it crossed,
// and the window — everything needed to act on it without reading this code.
func (b *Bus) ReportLoudProducers() {
	if b.store == nil {
		return
	}
	status, err := b.Status()
	if err != nil {
		b.log("bus: reading the log to check producer rates: %v", err)
		return
	}
	for _, h := range status.Health {
		if h.Kind == HealthProducerSurging {
			b.log("bus: %s", h.Message)
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

func (d *durable) matches(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.filter.Matches(name)
}

func (d *durable) filterExpr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.filter.String()
}

func (d *durable) setFilter(f Filter) {
	d.mu.Lock()
	d.filter = f
	d.mu.Unlock()
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

// retire marks the consumer unregistered, which is what makes a late result from
// an in-flight handler a no-op rather than an error or a failure streak against a
// registration that no longer exists.
func (d *durable) retire() {
	d.mu.Lock()
	d.retired = true
	d.mu.Unlock()
}

func (d *durable) isRetired() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.retired
}

func (d *durable) recordFailure(reason string, count int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.retired {
		return
	}
	d.stalled = reason
	d.failures = count
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
