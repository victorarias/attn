package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// Apps as bus consumers: registration, delivery, the invocation log, and the
// clock that disables an app stuck on one event.
//
// An app is an ordinary durable consumer — `app:<name>`, a persisted cursor,
// at-least-once, stall-don't-skip, cursor-after-handler. None of that is
// reimplemented here. What this file adds is what happens between "the bus
// handed us a fact" and "the cursor moved": resolve the version and the handler,
// dispatch into the shared sidecar, record what happened, and decide whose fault
// it was.
//
// The last of those is the part worth reading twice. A handler that threw is the
// app's fault and advances its stall clock. A sidecar that died, or was never
// there, is the runtime's — it stalls the delivery exactly the same way, but it
// must never move an app closer to being disabled. Nothing else in attn can tell
// those apart, so this file has to.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.

const (
	appInvocationStatusOK = "ok"
	// appInvocationStatusError is a handler that threw: the app's own fault.
	appInvocationStatusError = "error"
	// appInvocationStatusRuntimeError is a dispatch that never reached a handler,
	// or whose answer never came back. It is recorded — a reader looking at an app
	// that is doing nothing deserves to see why — but it is not held against the
	// app.
	appInvocationStatusRuntimeError = "runtime_error"
)

// appRuntimeConnectWait is how long a delivery waits for the sidecar to come up
// before treating its absence as a runtime failure.
//
// The runtime starts lazily, on the first fact an app is due, so the very first
// dispatch after a daemon start pays a cold start. Spawn, connect, hello, import
// the bundle and run the handler was measured end to end at 77ms (receipt in the
// plan doc); ten seconds is ~130× that, and a delivery that hits it stalls and
// retries rather than failing anything permanently.
const appRuntimeConnectWait = 10 * time.Second

// appConnectWait is that tripwire, overridable so a test that dispatches into a
// runtime which will never connect does not cost ten real seconds per delivery.
func (d *Daemon) appConnectWait() time.Duration {
	if d.appRuntimeWait > 0 {
		return d.appRuntimeWait
	}
	return appRuntimeConnectWait
}

// appAutoDisableStall is the whole auto-disable rule: an app that has been stuck
// on the SAME event for this long is disabled.
//
// One clock, no failure-count clause. The thing that has to be prevented is one
// broken app pinning the durable log's retention floor open for everybody, and
// that is a function of wall time stalled, not of how many times the retry
// happened to fire. A count would also punish a fast-failing app more than a
// slow one for the same amount of harm.
//
// Fifteen minutes is roughly five rounds at the bus's two-minute retry cap —
// long enough that a transient dependency (a git remote, a rebooting service)
// recovers untouched, short enough that a genuinely broken app does not hold the
// log for an afternoon.
const appAutoDisableStall = 15 * time.Minute

// appCrashStrikes is the second half of the auto-disable rule: an app the
// runtime host named as the cause of a sidecar crash this many times inside
// appCrashWindow is disabled.
//
// The stall clock above cannot express this one, which is why there are two. A
// crash kills the process, so the delivery is retried against a fresh runtime
// and the next crash may land on a different event entirely — an app whose
// promise rejects after its handler already returned takes the sidecar down
// while some other app's event is in flight. A clock keyed on "stuck on the same
// event" never accrues for the one app that hurts everybody.
//
// Three, because supervise parks the whole sidecar after DefaultGiveUpAfter (10)
// restarts with no stability window — roughly two to three minutes of
// crash-looping — and every app losing its runtime is exactly the harm this
// prevents, so the culprit has to go first. Above one, because a single crash
// can be a machine event (an OOM kill, a signal) whose stack merely passes
// through an app's bundle.
const appCrashStrikes = 3

// appCrashWindow is appAutoDisableStall so the auto-disable rule has one
// duration rather than two: an app broken for a quarter of an hour is disabled,
// whichever way it is broken.
const appCrashWindow = appAutoDisableStall

// notificationKindAppAutoDisabled marks the notification an auto-disable writes.
const notificationKindAppAutoDisabled = "app_auto_disabled"

// appStall is one app's position against the auto-disable clock. There is an
// entry only while an app is failing; a success deletes it.
//
// It is in memory on purpose. The clock measures a stall that is happening now,
// and a daemon restart genuinely does reset it: the app gets its window again
// against a runtime that has also just restarted, which is the generous reading
// and the right one.
type appStall struct {
	seq       int64
	eventName string
	since     time.Time
	attempts  int
	lastError string
}

// appDispatchPlan is everything one delivery needs, read once per event.
type appDispatchPlan struct {
	app         string
	namespace   string
	versionID   int64
	artifact    string
	handler     string
	collections []string
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// registerAppConsumers gives every registered app its durable consumer. It runs
// once at daemon start, after the bus is up.
//
// A registration failure for one app is logged and the others still start: one
// app with a corrupt declaration must not take the rest of them down.
func (d *Daemon) registerAppConsumers() {
	if d.store == nil || d.eventBus == nil {
		return
	}
	rows, err := d.store.ListApps()
	if err != nil {
		d.logf("apps: listing apps to register their consumers: %v", err)
		return
	}
	for _, row := range rows {
		if err := d.registerAppConsumer(row.Name); err != nil {
			d.logf("apps: %v", err)
		}
	}
}

// registerAppConsumer registers, or re-points, one app's consumer.
//
// Registering is what mints the cursor, and a fresh consumer starts at head:
// an app installed today is not asking to be handed a month of backlog. An app
// that already has a cursor keeps it, which is what makes a daemon restart
// invisible to a running app.
//
// A live consumer whose app has been re-applied with different subscriptions
// gets SetFilter rather than a re-registration: unregister-then-register would
// delete the cursor on the way through, and the app would silently skip every
// fact published while it was being updated.
func (d *Daemon) registerAppConsumer(name string) error {
	filter, err := d.appFilter(name)
	if err != nil {
		return err
	}
	consumer := apps.ConsumerName(name)
	if d.eventBus.Registered(consumer) {
		if err := d.eventBus.SetFilter(consumer, filter); err != nil {
			return fmt.Errorf("re-pointing the bus consumer for app %q at its new subscriptions: %w", name, err)
		}
		return nil
	}
	if err := d.eventBus.Register(consumer, filter, d.appEventHandler(name)); err != nil {
		return fmt.Errorf("registering the bus consumer for app %q: %w", name, err)
	}
	return nil
}

// appFilter reads an app's declared subscriptions off its current version.
//
// An app with no version yet subscribes to nothing rather than to everything:
// bus.ParseFilter reads an empty expression as All, and an app whose code has
// never been built must not be woken by every fact in the system.
func (d *Daemon) appFilter(name string) (bus.Filter, error) {
	manifest, _, err := d.appDeclaration(name)
	if err != nil {
		return nil, err
	}
	patterns := manifest.EventPatterns()
	if len(patterns) == 0 {
		return bus.Filter{appNoSubscriptionsPattern}, nil
	}
	return bus.Filter(patterns), nil
}

// appNoSubscriptionsPattern is a fact name nothing publishes, used as the filter
// of an app that declared no subscriptions. A filter has to be *something*, and
// every other candidate — empty, "*" — means "everything" somewhere in the bus.
const appNoSubscriptionsPattern = "app.subscribes.to.nothing"

// appDeclaration loads an app's current version and the manifest frozen into it.
// The zero AppVersion with a nil error means the app exists but has no version.
func (d *Daemon) appDeclaration(name string) (appbuild.Manifest, store.AppVersion, error) {
	row, ok, err := d.store.GetApp(name)
	if err != nil {
		return appbuild.Manifest{}, store.AppVersion{}, fmt.Errorf("reading app %q: %w", name, err)
	}
	if !ok || row.CurrentVersionID == 0 {
		return appbuild.Manifest{}, store.AppVersion{}, nil
	}
	version, ok, err := d.store.GetAppVersion(row.CurrentVersionID)
	if err != nil {
		return appbuild.Manifest{}, store.AppVersion{}, fmt.Errorf("reading version %d of app %q: %w", row.CurrentVersionID, name, err)
	}
	if !ok {
		return appbuild.Manifest{}, store.AppVersion{}, nil
	}
	var manifest appbuild.Manifest
	if err := json.Unmarshal([]byte(version.Declaration), &manifest); err != nil {
		return appbuild.Manifest{}, store.AppVersion{}, fmt.Errorf(
			"the declaration frozen into version %d of app %q is not readable (%v); that snapshot is written at apply time and never edited, so this version cannot be run — `attn app rollback %s` moves off it",
			version.ID, name, err, name)
	}
	return manifest, version, nil
}

// declareAppCollections creates the document collections a version declared, in
// the app's own namespace.
//
// It runs from syncAppRuntimeForVersion, which is every apply and every
// rollback, and it is idempotent: declaring a collection that already exists
// with the same fields is a no-op in the store.
// A collection an older version declared and this one dropped is left alone —
// the documents in it are the user's data, and a version bump is not consent to
// delete them.
func (d *Daemon) declareAppCollections(name string, manifest appbuild.Manifest) {
	namespace := apps.Namespace(name)
	for _, collection := range manifest.Collections {
		schema := docstore.CollectionSchema{Namespace: namespace, Collection: collection.Name}
		for _, field := range collection.Fields {
			schema.Fields = append(schema.Fields, docstore.FieldSpec{Name: field, Type: docstore.FieldString})
		}
		if err := schema.Validate(); err != nil {
			d.logf("apps: app %s declares collection %q, which the document store refuses: %v", name, collection.Name, err)
			continue
		}
		redeclared, err := d.store.DefineDocumentCollection(schema, d.appNow())
		if err != nil {
			d.logf("apps: declaring collection %s/%s for app %s: %v", namespace, collection.Name, name, err)
			continue
		}
		if redeclared {
			d.publishCollectionRedeclared(namespace, collection.Name)
		}
	}
}

// syncAppRuntimeForVersion re-points an app's consumer and collections after its
// version moved. Called from apply and rollback, whose subscriptions and
// collections can both differ from the version before.
func (d *Daemon) syncAppRuntimeForVersion(name string) {
	if d.store == nil {
		return
	}
	manifest, _, err := d.appDeclaration(name)
	if err != nil {
		d.logf("apps: %v", err)
		return
	}
	d.declareAppCollections(name, manifest)
	if d.eventBus == nil {
		return
	}
	if err := d.registerAppConsumer(name); err != nil {
		d.logf("apps: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Delivery
// ---------------------------------------------------------------------------

func (d *Daemon) appEventHandler(name string) bus.Handler {
	return func(ctx context.Context, ev bus.Event) error {
		return d.deliverAppEvent(ctx, name, ev)
	}
}

// deliverAppEvent runs one fact through one app.
//
// Returning an error stalls this app's consumer on this event and has the bus
// redeliver it with backoff — never skip it. That is true for both failure
// classes; what differs is what gets recorded and whether the stall clock moves.
func (d *Daemon) deliverAppEvent(ctx context.Context, name string, ev bus.Event) error {
	plan, err := d.planAppDispatch(name, ev)
	if err != nil {
		return err
	}
	if plan == nil {
		// Nothing to run: no version, or a fact that matches the app's filter but
		// no declared subscription. Advance rather than stall — there is no code
		// here to succeed on a retry, and a permanent stall would pin retention.
		return nil
	}

	started := d.appNow()
	result, dispatchErr := d.dispatchToAppRuntime(ctx, plan, ev)
	took := d.appNow().Sub(started)

	// The consumer is going away (daemon stop, `attn app remove`). Record nothing
	// and let the bus put the event back: an interrupted delivery did not happen.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	invocation := store.AppInvocation{
		AppName:      name,
		VersionID:    plan.versionID,
		EventSeq:     ev.Seq,
		EventName:    ev.Name,
		EventSubject: ev.Subject,
		Handler:      plan.handler,
		Duration:     took,
		StartedAt:    started,
	}

	switch {
	case dispatchErr != nil && isRuntimeFailure(dispatchErr):
		invocation.Status = appInvocationStatusRuntimeError
		invocation.Error = dispatchErr.Error()
		d.recordAppInvocation(invocation)
		// Rule 2: the runtime died, so no app is closer to being disabled than it
		// was. Clearing rather than merely not-advancing is deliberate — leaving a
		// clock running through an outage would charge the app for the minutes the
		// sidecar was down.
		d.clearAppStall(name)
		return dispatchErr

	case dispatchErr != nil && errors.Is(dispatchErr, context.DeadlineExceeded):
		// A handler that never returned, and attributeWedgedDispatch has already
		// established that this app is the one that did not return — either the
		// loop is turning, or it is frozen and this app is what froze it.
		invocation.Status = appInvocationStatusError
		invocation.Error = fmt.Sprintf(
			"the handler for %s did not return within %s; attn abandoned the dispatch. A handler awaits attn's own APIs, which always settle — an await on something else needs its own timeout.",
			ev.Name, d.appDispatchBudget())
		d.recordAppInvocation(invocation)
		d.noteAppFailure(name, ev, invocation.Error)
		return errors.New(invocation.Error)

	case dispatchErr != nil:
		invocation.Status = appInvocationStatusRuntimeError
		invocation.Error = dispatchErr.Error()
		d.recordAppInvocation(invocation)
		d.clearAppStall(name)
		return dispatchErr

	case !result.OK:
		invocation.Status = appInvocationStatusError
		invocation.Error = result.Error
		d.recordAppInvocation(invocation)
		d.noteAppFailure(name, ev, result.Error)
		return fmt.Errorf("app %s handler %s threw: %s", name, plan.handler, firstLine(result.Error))

	default:
		invocation.Status = appInvocationStatusOK
		d.recordAppInvocation(invocation)
		d.clearAppStall(name)
		return nil
	}
}

// planAppDispatch resolves what should run. A nil plan with a nil error means
// there is nothing to run for this fact.
func (d *Daemon) planAppDispatch(name string, ev bus.Event) (*appDispatchPlan, error) {
	manifest, version, err := d.appDeclaration(name)
	if err != nil {
		return nil, err
	}
	if version.ID == 0 {
		return nil, nil
	}
	handler := resolveAppHandler(manifest.EventPatterns(), ev.Name)
	if handler == "" {
		return nil, nil
	}
	plan := &appDispatchPlan{
		app:       name,
		namespace: apps.Namespace(name),
		versionID: version.ID,
		artifact:  version.ArtifactPath,
		handler:   handler,
	}
	for _, collection := range manifest.Collections {
		plan.collections = append(plan.collections, collection.Name)
	}
	return plan, nil
}

// resolveAppHandler picks which declared subscription a fact arrived under.
//
// Exact wins over a wildcard, and among wildcards the longest prefix wins, so an
// app that declares both `session.*` and `session.state.changed` gets the
// specific handler for the specific fact. The matching itself is the bus's, not
// a copy of it.
func resolveAppHandler(patterns []string, eventName string) string {
	best := ""
	for _, pattern := range patterns {
		if !bus.MatchPattern(pattern, eventName) {
			continue
		}
		if pattern == eventName {
			return pattern
		}
		if len(pattern) > len(best) {
			best = pattern
		}
	}
	return best
}

// dispatchToAppRuntime sends one handler run to the sidecar and waits for it, or
// for the delivery to be cancelled.
func (d *Daemon) dispatchToAppRuntime(ctx context.Context, plan *appDispatchPlan, ev bus.Event) (appDispatchResult, error) {
	runtime, err := d.awaitAppRuntime(ctx)
	if err != nil {
		return appDispatchResult{}, err
	}

	dispatch := &appDispatch{
		app:         plan.app,
		namespace:   plan.namespace,
		versionID:   plan.versionID,
		collections: make(map[string]struct{}, len(plan.collections)),
	}
	for _, collection := range plan.collections {
		dispatch.collections[collection] = struct{}{}
	}
	d.registerAppDispatch(dispatch)
	// Released whatever happens, including the abandoned-timeout path: an id left
	// behind would let a handler that finally woke up write documents from outside
	// any delivery.
	defer d.releaseAppDispatch(dispatch.id)

	var payload any
	if len(ev.Payload) > 0 {
		payload = json.RawMessage(ev.Payload)
	}
	request := appDispatchRequest{
		Dispatch:    dispatch.id,
		App:         plan.app,
		VersionID:   plan.versionID,
		Artifact:    plan.artifact,
		Handler:     plan.handler,
		Collections: plan.collections,
		Event: appDispatchEvent{
			Name:        ev.Name,
			Subject:     ev.Subject,
			Seq:         ev.Seq,
			Payload:     payload,
			PublishedAt: stampForWire(ev.CreatedAt),
		},
	}
	if request.Collections == nil {
		request.Collections = []string{}
	}

	callCtx, cancel := context.WithTimeout(ctx, d.appDispatchBudget())
	defer cancel()
	result, err := runtime.dispatch(callCtx, request)
	if err != nil {
		if ctx.Err() == nil && callCtx.Err() != nil {
			// Our own deadline, not the caller's: something in the sidecar is stuck.
			// Attributed here rather than by the caller because this dispatch must
			// still be in the in-flight set for the answer to be right.
			return appDispatchResult{}, d.attributeWedgedDispatch(ctx, runtime, plan.app)
		}
		if ctx.Err() != nil {
			return appDispatchResult{}, ctx.Err()
		}
		// The transport failed — the socket died mid-call, or the process did.
		return appDispatchResult{}, runtimeFailure("%v", err)
	}
	// The host answered, so this handler is no longer on the loop. Only an answer
	// proves that: a dispatch the daemon gave up on is still running in there, and
	// forgetting it here is what let a victim be charged for it.
	d.forgetEnteredHandler(dispatch.id)
	return result, nil
}

// attributeWedgedDispatch decides who is charged for a dispatch that never came
// back.
//
// Every app shares one event loop, so a handler that blocks without yielding
// blocks all of them: every other app's dispatch times out too, and charging
// each of them auto-disables apps whose code never ran. The ping tells the two
// cases apart. The host serves it off that same loop without touching app code,
// so an answer means the loop is turning and this app's own handler is what is
// stuck, and silence means the loop is frozen — in which case one specific
// handler is executing without yielding, and everyone else suffered a runtime
// failure rather than a fault of their own.
//
// Which handler that is, is the host's to say, not the daemon's to guess. The
// daemon knows only the order it *sent* dispatches, and that order is not the
// order handlers hold the loop: a handler that awaits an attn API — the ordinary,
// documented shape — yields, and a spinner dispatched after it is what freezes
// everything, including the first handler's own reply. Blaming the earliest
// dispatch charges the well-behaved app and lets the spinner walk. So the host
// announces each entry before it calls the handler (appRuntimeEnteredMethod), and
// the culprit is the most recent entry with no answer yet: whoever entered last
// and never came back is the one on the loop right now.
//
// An entry is forgotten when its dispatch is answered, when a ping answers, or
// when the process dies. Only the first and last of those prove that handler is
// off the loop: an answered ping proves nothing is *holding* the loop, and a
// handler that yielded and never settled is still on it. Wiping on a ping is
// still right, because attribution is moot while the loop turns.
//
// Known residue, both cheap. If one app freezes the loop, finishes, and a
// *different* app freezes it again with no ping in between, both entries are
// present and the second freezer entered later, so it is still the one charged.
// And if a handler yields, has its entry wiped by a ping, and only then resumes
// and spins, nothing names it: attribution falls back to charging whoever timed
// out, until the next dispatch enters and is announced.
func (d *Daemon) attributeWedgedDispatch(ctx context.Context, runtime *appRuntimeConnection, name string) error {
	pingCtx, cancel := context.WithTimeout(ctx, d.appPingBudget())
	defer cancel()

	asked := d.appNow()
	err := runtime.ping(pingCtx)
	// Logged because this is the only place that says who wedged the runtime, and
	// it costs nothing in a healthy system: a dispatch has to burn its whole
	// timeout to get here.
	// Microseconds, not milliseconds: an answered ping is the healthy case and it
	// costs well under a millisecond, which rounds to "0s" and tells the reader
	// nothing about the margin the timeout is holding.
	d.logf("apps: %s hit the dispatch timeout; the app runtime %s a liveness ping after %s",
		name, pingOutcome(err), d.appNow().Sub(asked).Round(time.Microsecond))

	if err == nil {
		// The loop is turning, so nothing is holding it and there is nothing to
		// attribute — see the residue note above for what this wipe costs.
		d.forgetEnteredHandlers()
		return context.DeadlineExceeded
	}

	culprit, ok := d.wedgedAppCulprit()
	if !ok || culprit == name {
		return context.DeadlineExceeded
	}
	return runtimeFailure(
		"the app runtime stopped answering while %s held its event loop, so this handler never ran; %s is what attn charged for the stall, and `attn app status %s` shows it",
		culprit, culprit, culprit)
}

func pingOutcome(err error) string {
	if err == nil {
		return "answered"
	}
	return "did not answer"
}

// noteEnteredHandler records that the host has called an app's handler and has
// not answered for it yet. A generation that does not match wipes the map: those
// handlers were running in a process that is gone.
//
// Ordering is the point, so this runs inline on the connection's read loop rather
// than in a goroutine per frame — entries arrive in the order the host made them
// and must be stamped in that order.
func (d *Daemon) noteEnteredHandler(generation uint64, dispatchID, name string) {
	d.appEnteredMu.Lock()
	defer d.appEnteredMu.Unlock()
	if d.appEntered == nil || d.appEnteredGen != generation {
		d.appEntered = make(map[string]enteredHandler)
		d.appEnteredGen = generation
	}
	d.appEnteredSeq++
	d.appEntered[dispatchID] = enteredHandler{app: name, order: d.appEnteredSeq}
}

func (d *Daemon) forgetEnteredHandler(dispatchID string) {
	d.appEnteredMu.Lock()
	delete(d.appEntered, dispatchID)
	d.appEnteredMu.Unlock()
}

func (d *Daemon) forgetEnteredHandlers() {
	d.appEnteredMu.Lock()
	d.appEntered = nil
	d.appEnteredGen = 0
	d.appEnteredMu.Unlock()
}

// wedgedAppCulprit names the app whose handler is on the frozen loop: the last
// one the host entered and has not answered for.
func (d *Daemon) wedgedAppCulprit() (string, bool) {
	d.appEnteredMu.Lock()
	defer d.appEnteredMu.Unlock()
	var latest enteredHandler
	for _, entry := range d.appEntered {
		if entry.order > latest.order {
			latest = entry
		}
	}
	return latest.app, latest.order > 0
}

// appRuntimePingWait is how long the sidecar gets to answer a liveness ping.
//
// A tripwire past a localhost round trip, not a fit: the host answers off its
// read loop with a constant, and answered pings measured on a live daemon cost
// 344µs and 416µs. Two seconds is ~5,000× that, so only a loop that is genuinely
// not turning reaches it. It is spent only on a dispatch that already burned
// appDispatchTimeout, so being generous costs nothing that was not already lost.
const appRuntimePingWait = 2 * time.Second

// appPingBudget is that tripwire, overridable so a test about a frozen loop does
// not cost two real seconds.
func (d *Daemon) appPingBudget() time.Duration {
	if d.appPingWait > 0 {
		return d.appPingWait
	}
	return appRuntimePingWait
}

// appDispatchBudget is appDispatchTimeout, overridable so a test about a handler
// that never returns does not cost a real minute.
func (d *Daemon) appDispatchBudget() time.Duration {
	if d.appDispatchWait > 0 {
		return d.appDispatchWait
	}
	return appDispatchTimeout
}

// awaitAppRuntime returns the live sidecar, starting it if it is not running and
// waiting a bounded time for it to connect.
func (d *Daemon) awaitAppRuntime(ctx context.Context) (*appRuntimeConnection, error) {
	runtime, ready := d.appRuntimeOrReady()
	if runtime != nil {
		return runtime, nil
	}
	if err := d.startAppRuntimeForDispatch(); err != nil {
		if errors.Is(err, supervise.ErrParked) {
			return nil, runtimeFailure(
				"the app runtime is parked after repeated crashes and is not being restarted; `attn app runtime status` shows why it exited and `attn app runtime restart` tries again")
		}
		return nil, runtimeFailure("%v", err)
	}
	wait := d.appConnectWait()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ready:
		if runtime := d.appRuntimeConnected(); runtime != nil {
			return runtime, nil
		}
		return nil, runtimeFailure("the app runtime connected and went away again before this handler could run")
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, runtimeFailure(
			"the app runtime did not connect within %s; `attn app runtime status` shows what it is doing and `attn app logs runtime` shows what it printed",
			wait)
	}
}

// appRuntimeOrReady hands back the live connection, or the channel that closes
// when one arrives. Both under the same lock: fetching them separately leaves a
// window where the connection lands between the check and the wait, and the
// waiter sleeps out its whole timeout beside a healthy runtime.
func (d *Daemon) appRuntimeOrReady() (*appRuntimeConnection, chan struct{}) {
	d.appRuntimeMu.Lock()
	defer d.appRuntimeMu.Unlock()
	if d.appRuntimeConn != nil {
		return d.appRuntimeConn, nil
	}
	if d.appRuntimeReady == nil {
		d.appRuntimeReady = make(chan struct{})
	}
	return nil, d.appRuntimeReady
}

func (d *Daemon) recordAppInvocation(invocation store.AppInvocation) {
	if d.store == nil {
		return
	}
	id, err := d.store.AppendAppInvocation(invocation)
	if err != nil {
		d.logf("apps: recording an invocation of app %s: %v", invocation.AppName, err)
		return
	}
	d.notifyAppWatchers(appInvocationForWire(id, invocation), invocation.AppName)
}

// firstLine trims a stack trace down to its message, for a log line and a bus
// error that already have the whole text recorded beside them.
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

// ---------------------------------------------------------------------------
// The auto-disable clock
// ---------------------------------------------------------------------------

// noteAppFailure advances the stall clock for an app-attributed failure, and
// disables the app if it has been stuck on this event past the window.
//
// The clock is per event, not per app: an app failing on a different fact every
// time is not stuck, it is unreliable, and unreliable does not pin the retention
// floor. Only the same seq failing over and over does.
func (d *Daemon) noteAppFailure(name string, ev bus.Event, message string) {
	now := d.appNow()

	d.appStallMu.Lock()
	if d.appStalls == nil {
		d.appStalls = make(map[string]*appStall)
	}
	stall := d.appStalls[name]
	if stall == nil || stall.seq != ev.Seq {
		stall = &appStall{seq: ev.Seq, eventName: ev.Name, since: now}
		d.appStalls[name] = stall
	}
	stall.attempts++
	stall.lastError = message
	stalled := now.Sub(stall.since)
	attempts := stall.attempts
	d.appStallMu.Unlock()

	if stalled < appAutoDisableStall {
		return
	}
	d.autoDisableApp(name, ev, stalled, attempts, message)
}

func (d *Daemon) clearAppStall(name string) {
	d.appStallMu.Lock()
	delete(d.appStalls, name)
	d.appStallMu.Unlock()
}

// noteAppRuntimeCrash charges an app for taking the sidecar down.
//
// The host names the culprit from the stack of the error that killed it, which
// is the only thing in the process that still knows whose code it was: the
// rejection may surface long after that app's dispatch returned, so "who was
// running" would name an innocent.
func (d *Daemon) noteAppRuntimeCrash(name, kind, message string) {
	now := d.appNow()

	d.appCrashMu.Lock()
	if d.appCrashes == nil {
		d.appCrashes = make(map[string][]time.Time)
	}
	kept := d.appCrashes[name][:0]
	for _, at := range d.appCrashes[name] {
		if now.Sub(at) < appCrashWindow {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	d.appCrashes[name] = kept
	strikes := len(kept)
	d.appCrashMu.Unlock()

	d.logf("apps: %s crashed the app runtime (%s), strike %d of %d: %s",
		name, kind, strikes, appCrashStrikes, firstLine(message))
	if strikes < appCrashStrikes {
		return
	}
	d.disableAppAutomatically(name, message,
		fmt.Sprintf("apps: disabled %s — crashed the app runtime %d times within %s: %s",
			name, strikes, appCrashWindow, firstLine(message)),
		fmt.Sprintf(
			"%s crashed the shared app runtime %d times in %s, so attn disabled it — one app taking the runtime down stops every other app with it. The failure was an unhandled %s; a handler must catch what it starts, including work it does not await. Fix it and `attn app enable %s`; `attn app logs runtime` shows what it printed.",
			name, strikes, appCrashWindow, kind, name))
}

func (d *Daemon) clearAppCrashes(name string) {
	d.appCrashMu.Lock()
	delete(d.appCrashes, name)
	d.appCrashMu.Unlock()
}

// appStallSnapshot reports what an app is stuck on, for `attn app status`.
func (d *Daemon) appStallSnapshot(name string) (appStall, bool) {
	d.appStallMu.Lock()
	defer d.appStallMu.Unlock()
	stall, ok := d.appStalls[name]
	if !ok {
		return appStall{}, false
	}
	return *stall, true
}

// autoDisableApp flips the app off, says so on the bus, and tells the user.
//
// All three, because each answers a different question: the consumer bit stops
// delivery and releases the retention floor, the fact reaches anything in the
// daemon watching the app, and the notification is the only one of the three a
// person ever sees. An auto-disable nobody is told about is a feature that
// silently stops working.
func (d *Daemon) autoDisableApp(name string, ev bus.Event, stalled time.Duration, attempts int, message string) {
	d.disableAppAutomatically(name, message,
		fmt.Sprintf("apps: disabled %s — stuck on %s (seq %d) for %s across %d attempts: %s",
			name, ev.Name, ev.Seq, stalled.Round(time.Second), attempts, firstLine(message)),
		fmt.Sprintf(
			"%s failed on the same event (%s, seq %d) for %s across %d attempts, so attn disabled it — a stalled app holds the event log open for every other consumer. Fix the handler and `attn app enable %s`; `attn app status %s` shows the failures.",
			name, ev.Name, ev.Seq, stalled.Round(time.Minute), attempts, name, name))
}

// disableAppAutomatically flips the app off, says so on the bus, and tells the
// user.
//
// All three, because each answers a different question: the consumer bit stops
// delivery and releases the retention floor, the fact reaches anything in the
// daemon watching the app, and the notification is the only one of the three a
// person ever sees. An auto-disable nobody is told about is a feature that
// silently stops working.
func (d *Daemon) disableAppAutomatically(name, detail, logLine, body string) {
	consumer := apps.ConsumerName(name)
	flipped, err := d.store.SetBusConsumerEnabled(consumer, false, d.appNow())
	if err != nil {
		d.logf("apps: disabling app %s: %v", name, err)
		return
	}
	if !flipped {
		// Already off, or removed while this delivery was failing.
		return
	}
	// The windows restart from here. Enabling the app again is the supported way
	// back, and it clears these too; leaving the old clocks in place would disable
	// a re-enabled app on its very next failure.
	d.clearAppStall(name)
	d.clearAppCrashes(name)

	d.logf("%s", logLine)
	d.publishFact(FactAppEnabledChanged, name, appEnabledChanged{
		Name: name, Consumer: consumer, Enabled: false,
	})
	if d.store == nil {
		return
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindAppAutoDisabled,
		Severity:   store.NotificationWarning,
		Title:      fmt.Sprintf("App disabled: %s", name),
		Body:       body,
		Detail:     detail,
		SourceKind: "app",
		SourceID:   name,
	}, d.appNow())
	if err != nil {
		d.logf("notifications: add app-auto-disabled notification for %s: %v", name, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

// ---------------------------------------------------------------------------
// Invocation retention
// ---------------------------------------------------------------------------

const (
	// appInvocationRetentionKind is the cron entry that trims the invocation log.
	appInvocationRetentionKind = "app_invocation_retention"
	// appInvocationRetentionInterval is how often it runs. Hourly, like the bus's
	// own retention pass: the log grows continuously and a daily sweep would let a
	// busy day's worth accumulate before anything looked at it.
	appInvocationRetentionInterval = time.Hour
	appInvocationRetentionTimeout  = 30 * time.Second

	// AppInvocationRetention is the age window for the invocation log. It matches
	// the bus's own DefaultRetention, which is the useful property: an invocation
	// whose event has been trimmed off the durable log cannot be re-read against
	// it, so the row has nothing left to tell anyone.
	AppInvocationRetention = 30 * 24 * time.Hour

	// AppInvocationsPerApp is how many invocations one app keeps, whatever their
	// age. The age window alone does not bound this table, because how many rows
	// thirty days holds is entirely a property of what the app subscribed to.
	//
	// The receipt, measured over 7.5 days of Victor's production log (275,845
	// facts): the loudest fact is `session.state.changed` at 1,141/hour, and it is
	// what a scaffolded app subscribes to out of the box. Thirty days of that is
	// ~820,000 rows — well over a hundred megabytes for one app, on a database
	// that is 51MB today. The quietest domain an app would realistically watch
	// (`ticket.*`) runs at 27/day, three orders of magnitude below.
	//
	// 20,000 rows is ~17 hours of the loudest possible app — a whole working day
	// of "what did it do this morning" — and about 4MB. For anything quieter the
	// age window trims first and the cap is never felt: at the ticket rate, 20,000
	// rows is two years.
	AppInvocationsPerApp = 20_000
)

// appInvocationRetentionHandler is the cron entry. It reports how many rows went
// so a run that is doing nothing and a run that is not happening look different
// in the task list.
func (d *Daemon) appInvocationRetentionHandler(_ context.Context, _ *jobs.Job) (any, error) {
	if d.store == nil {
		return map[string]any{"removed": 0}, nil
	}
	removed, err := d.store.TrimAppInvocations(d.appNow().Add(-AppInvocationRetention), AppInvocationsPerApp)
	if err != nil {
		return nil, fmt.Errorf("trimming the app invocation log: %w", err)
	}
	if removed > 0 {
		d.logf("apps: trimmed %d invocation(s) — older than %s, or past the newest %d of an app",
			removed, AppInvocationRetention, AppInvocationsPerApp)
	}
	return map[string]any{"removed": removed}, nil
}
