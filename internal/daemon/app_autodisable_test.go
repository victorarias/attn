package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The auto-disable clock: one rule, one number, and everything that must NOT
// trip it.
//
// The clock measures fifteen minutes of wall time stuck on the same event, so
// every test here drives an injected clock. A test that waited out the real
// window would take a quarter of an hour and prove less, and one that shortened
// the constant would be testing a number the product does not ship.

// appTestClock is the daemon's clock for these tests, moved by hand.
type appTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newAppTestClock(d *Daemon) *appTestClock {
	clock := &appTestClock{now: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)}
	d.appClock = clock.Now
	return clock
}

func (c *appTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *appTestClock) advance(by time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(by)
	c.mu.Unlock()
}

func appEnabled(t *testing.T, d *Daemon, name string) bool {
	t.Helper()
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(name))
	if err != nil || !ok {
		t.Fatalf("consumer for %s: %v ok=%t", name, err, ok)
	}
	return consumer.Enabled
}

func appNotifications(t *testing.T, d *Daemon, kind string) []store.NotificationRecord {
	t.Helper()
	all, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	var out []store.NotificationRecord
	for _, record := range all {
		if record.Kind == kind {
			out = append(out, record)
		}
	}
	return out
}

// failEvery makes a sidecar whose handler always throws.
func failEvery(message string) func(*fakeAppRuntime, appDispatchRequest) error {
	return func(*fakeAppRuntime, appDispatchRequest) error { return errors.New(message) }
}

// The rule itself, and all three of its effects. Asserting only the enabled bit
// would let a silent auto-disable ship: the bit stops delivery, the fact reaches
// the rest of the daemon, and the notification is the only one a person sees.
func TestAppStuckOnOneEventIsDisabledAndSaysSoThreeWays(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, failEvery("TypeError: undefined is not a function"))
	stuck := appEvent("ticket.created", "tk-1", 12)

	// Four failures inside the window change nothing: the rule is time stalled,
	// not attempts burned.
	for i := 0; i < 4; i++ {
		if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err == nil {
			t.Fatal("a throwing handler reported success")
		}
		clock.advance(3 * time.Minute)
		if !appEnabled(t, d, "greeter") {
			t.Fatalf("greeter was disabled after %d failure(s) inside the window", i+1)
		}
	}

	// The one that crosses fifteen minutes: four failures put the clock at
	// twelve, so this is the first delivery past the window.
	clock.advance(4 * time.Minute)
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err == nil {
		t.Fatal("a throwing handler reported success")
	}

	if appEnabled(t, d, "greeter") {
		t.Fatal("greeter is still enabled after 15 minutes stuck on one event")
	}
	facts := appFacts(t, d, FactAppEnabledChanged)
	if len(facts) != 1 || facts[0].Subject != "greeter" {
		t.Fatalf("app.enabled.changed facts = %+v, want one for greeter", facts)
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 {
		t.Fatalf("auto-disable notifications = %d, want 1", len(notes))
	}
	// The notification has to carry the way back, or the app is off forever as
	// far as its owner knows.
	if !strings.Contains(notes[0].Body, "attn app enable greeter") {
		t.Fatalf("the notification does not name the way back: %q", notes[0].Body)
	}
	if !strings.Contains(notes[0].Body, "ticket.created") || !strings.Contains(notes[0].Detail, "TypeError") {
		t.Fatalf("the notification does not say what failed: body=%q detail=%q", notes[0].Body, notes[0].Detail)
	}
	// One app is off; everything else still runs. That is a warning, and it
	// reaches the app now rather than at whatever re-pushes the feed next.
	if notes[0].Severity != store.NotificationWarning {
		t.Fatalf("severity = %q, want warning", notes[0].Severity)
	}
	if created := appFacts(t, d, FactNotificationCreated); len(created) != 1 || created[0].Subject != notes[0].ID {
		t.Fatalf("notification.created facts = %+v, want one for %s", created, notes[0].ID)
	}
	// And the clock is cleared, so re-enabling does not disable it again on the
	// very next failure.
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("the disabled app is still on the clock: %+v", stall)
	}
}

// Slow is not stuck. A handler that takes minutes but succeeds never touches
// the clock, however long the app has been running.
func TestSlowButSucceedingAppIsNeverDisabled(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "slowpoke", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, func(*fakeAppRuntime, appDispatchRequest) error {
		// Each run "takes" ten minutes on the daemon's clock.
		clock.advance(10 * time.Minute)
		return nil
	})

	for seq := int64(1); seq <= 6; seq++ {
		if err := d.deliverAppEvent(context.Background(), "slowpoke", appEvent("ticket.created", "tk", seq)); err != nil {
			t.Fatalf("a succeeding handler failed: %v", err)
		}
	}
	if !appEnabled(t, d, "slowpoke") {
		t.Fatal("an app that succeeded on every event, slowly, was disabled")
	}
	if _, ok := d.appStallSnapshot("slowpoke"); ok {
		t.Fatal("a succeeding app is on the auto-disable clock")
	}
	rows := invocationsOf(t, d, "slowpoke")
	if len(rows) != 6 {
		t.Fatalf("recorded %d invocation(s), want 6", len(rows))
	}
	if rows[0].Duration < 10*time.Minute {
		t.Fatalf("recorded duration %s, want the ten minutes the handler took", rows[0].Duration)
	}
}

// Unreliable is not stuck either. An app failing on a different fact every time
// is making progress: its cursor keeps moving, so it is not holding the log.
func TestAppFailingOnDistinctEventsIsNeverDisabled(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "flaky", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, failEvery("intermittent"))

	for seq := int64(1); seq <= 10; seq++ {
		if err := d.deliverAppEvent(context.Background(), "flaky", appEvent("ticket.created", "tk", seq)); err == nil {
			t.Fatal("a throwing handler reported success")
		}
		clock.advance(5 * time.Minute)
		if !appEnabled(t, d, "flaky") {
			t.Fatalf("flaky was disabled after failing on %d distinct events", seq)
		}
	}
	// Its clock restarted at every new event, so it never accumulated a stall.
	stall, ok := d.appStallSnapshot("flaky")
	if !ok || stall.seq != 10 || stall.attempts != 1 {
		t.Fatalf("stall = %+v (present=%t), want a fresh clock on the last event", stall, ok)
	}
}

// Rule 2 again, at the clock rather than at the classification: a sidecar that
// is down for longer than the window disables nobody.
func TestARuntimeOutageLongerThanTheWindowDisablesNothing(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	// A runtime binary that starts and dies without ever connecting: the outage.
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "exit 0"))
	d.appRuntimeSupervise.GiveUpAfter = 1
	// The connect wait is the one thing here that is real time rather than the
	// injected clock, and the runtime is never going to connect.
	d.appRuntimeWait = 20 * time.Millisecond

	stuck := appEvent("ticket.created", "tk-1", 4)
	// Fail once, then let the daemon's clock run past the window entirely.
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); !isRuntimeFailure(err) {
		t.Fatalf("error %v was not classified as the runtime's", err)
	}
	clock.advance(2 * appAutoDisableStall)
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); !isRuntimeFailure(err) {
		t.Fatalf("error %v was not classified as the runtime's", err)
	}

	if !appEnabled(t, d, "greeter") {
		t.Fatal("an app was disabled because the runtime was down")
	}
	if notes := appNotifications(t, d, notificationKindAppAutoDisabled); len(notes) != 0 {
		t.Fatalf("a runtime outage wrote %d auto-disable notification(s)", len(notes))
	}
}

// Enabling is the way back, and it has to clear the streak too — otherwise a
// re-enabled app is disabled again by its very next failure.
func TestEnablingClearsTheStreakAndResumesDelivery(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	var throw = true
	startFakeAppRuntime(t, d, func(*fakeAppRuntime, appDispatchRequest) error {
		if throw {
			return errors.New("boom")
		}
		return nil
	})
	stuck := appEvent("ticket.created", "tk-1", 3)

	_ = d.deliverAppEvent(context.Background(), "greeter", stuck)
	clock.advance(appAutoDisableStall + time.Minute)
	_ = d.deliverAppEvent(context.Background(), "greeter", stuck)
	if appEnabled(t, d, "greeter") {
		t.Fatal("the app was not disabled")
	}

	resp := appSetEnabled(t, d, "greeter", true)
	if !resp.Ok {
		t.Fatalf("enable: %v", protocol.Deref(resp.Error))
	}
	if !appEnabled(t, d, "greeter") {
		t.Fatal("enable did not flip the consumer bit back")
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("enable left the old stall clock running: %+v", stall)
	}

	// The next failure starts a fresh window rather than tripping the old one.
	_ = d.deliverAppEvent(context.Background(), "greeter", stuck)
	if !appEnabled(t, d, "greeter") {
		t.Fatal("a re-enabled app was disabled again by its first failure")
	}
	stall, ok := d.appStallSnapshot("greeter")
	if !ok || !stall.since.Equal(clock.Now()) {
		t.Fatalf("stall = %+v (present=%t), want a window starting now", stall, ok)
	}

	// And it really does deliver again.
	throw = false
	if err := d.deliverAppEvent(context.Background(), "greeter", stuck); err != nil {
		t.Fatalf("a re-enabled app did not deliver: %v", err)
	}
}

// Disabling releases the retention floor. This is the whole reason auto-disable
// exists: a stalled consumer holds the durable log open for everybody, and the
// observable consequence is that nothing can be compacted past it.
func TestADisabledAppNoLongerHoldsTheRetentionFloor(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "greeter", subscribing(FactDocumentChanged))
	startFakeAppRuntime(t, d, failEvery("boom"))

	// Several invalidations about one subject. Compaction reduces those to the
	// newest — but only at or below the floor every enabled consumer has passed.
	for i := 0; i < 4; i++ {
		d.publishFact(FactDocumentChanged, "app/greeter/seen/tk-1", nil)
	}
	waitFor(t, "the app to stall on the first fact", func() bool {
		_, ok := d.appStallSnapshot("greeter")
		return ok
	})

	removed, err := d.eventBus.Trim()
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if removed != 0 {
		t.Fatalf("compaction removed %d event(s) while a stalled enabled consumer sat at the bottom of the log", removed)
	}

	clock.advance(appAutoDisableStall + time.Minute)
	waitFor(t, "the stalled app to be disabled", func() bool { return !appEnabled(t, d, "greeter") })

	// Nothing else moved. The only difference is that a disabled consumer does
	// not pin the log.
	removed, err = d.eventBus.Trim()
	if err != nil {
		t.Fatalf("trim after the auto-disable: %v", err)
	}
	if removed == 0 {
		t.Fatal("the disabled app is still holding the retention floor down")
	}
}
