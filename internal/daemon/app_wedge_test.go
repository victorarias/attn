package daemon

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Who is charged when a dispatch never comes back.
//
// Every app's handler runs on one event loop in one process, so a handler that
// blocks without yielding stops all of them. Before attribution existed, the
// dispatch timeout fired for every app waiting behind it and each one was
// charged, which auto-disables apps whose code never ran — with a notification
// telling their author to fix a handler that was never called.
//
// Two signals answer it. The ping says whether the loop is turning at all, since
// the host serves it off that same loop without touching app code. The host's
// entry announcements say which handler is on the loop, which the daemon cannot
// work out for itself — see attributeWedgedDispatch.

// enterOnceThenBlock is a sidecar whose handlers announce themselves and then
// never return, which is what the daemon sees from a wedged loop.
func enterOnceThenBlock(entered chan<- string, release <-chan struct{}) func(*fakeAppRuntime, appDispatchRequest) error {
	return func(f *fakeAppRuntime, req appDispatchRequest) error {
		entered <- req.App
		<-release
		return nil
	}
}

// The case a live run falsified: charging the earliest dispatch blames the wrong
// app whenever a well-behaved handler got there first.
//
// bystander awaits an attn API — the documented shape — so it enters, yields, and
// is still unanswered when hog enters behind it and spins without yielding. hog
// is what froze the loop, including bystander's own reply. The daemon's dispatch
// order says bystander; only the host's entry order says hog.
func TestFrozenLoopChargesTheHandlerOnTheLoopNotTheEarliestDispatch(t *testing.T) {
	d := newAppDaemon(t)
	// Tripwires the product ships are 60s and 2s. A test that waited them out
	// would take a minute and prove nothing extra.
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "bystander", subscribing("ticket.*"))
	installApp(t, d, "hog", subscribing("ticket.*"))
	installApp(t, d, "latecomer", subscribing("ticket.*"))

	entered := make(chan string, 2)
	release := make(chan struct{})
	defer close(release)
	runtime := startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	// bystander first, hog second: the order that makes earliest-dispatch wrong.
	failures := map[string]chan error{"bystander": make(chan error, 1), "hog": make(chan error, 1), "latecomer": make(chan error, 1)}
	for i, name := range []string{"bystander", "hog"} {
		go func() {
			failures[name] <- d.deliverAppEvent(context.Background(), name, appEvent("ticket.created", "tk", int64(i+1)))
		}()
		if got := <-entered; got != name {
			t.Fatalf("handler %d in was %s, want %s", i, got, name)
		}
	}
	// From here nothing is served: no ping, and no dispatch reaches app code.
	runtime.freezeLoop()

	// latecomer's dispatch lands on a loop that will never read it.
	go func() {
		failures["latecomer"] <- d.deliverAppEvent(context.Background(), "latecomer", appEvent("ticket.created", "tk-3", 3))
	}()

	for name, ch := range failures {
		if err := <-ch; err == nil {
			t.Fatalf("%s's dispatch into a frozen runtime reported success", name)
		} else if name != "hog" && !strings.Contains(err.Error(), "hog") {
			t.Fatalf("%s's failure does not name the culprit: %v", name, err)
		}
	}

	// hog owns it: on the auto-disable clock, and its invocation is its own fault.
	if _, ok := d.appStallSnapshot("hog"); !ok {
		t.Fatal("hog wedged the runtime and is not on the auto-disable clock")
	}
	if rows := invocationsOf(t, d, "hog"); len(rows) != 1 || rows[0].Status != appInvocationStatusError {
		t.Fatalf("hog's invocation = %+v, want one with status %q", rows, appInvocationStatusError)
	}

	// Neither victim is charged. bystander entered but yielded; latecomer never ran
	// a line of its own code at all.
	for _, name := range []string{"bystander", "latecomer"} {
		if stall, ok := d.appStallSnapshot(name); ok {
			t.Fatalf("%s was charged for hog's wedge: %+v", name, stall)
		}
		if rows := invocationsOf(t, d, name); len(rows) != 1 || rows[0].Status != appInvocationStatusRuntimeError {
			t.Fatalf("%s's invocation = %+v, want one with status %q", name, rows, appInvocationStatusRuntimeError)
		}
	}
}

// The culprit's own dispatch times out first — it entered first among the ones
// still running — so by the time a victim gives up, nothing of the culprit's is
// in flight. The entry announcement is what outlives that.
func TestAVictimIsNotChargedAfterTheCulpritsDispatchHasGivenUp(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 200 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "hog", subscribing("ticket.*"))
	installApp(t, d, "latecomer", subscribing("ticket.*"))

	entered := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	runtime := startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	go d.deliverAppEvent(context.Background(), "hog", appEvent("ticket.created", "tk-1", 1))
	if app := <-entered; app != "hog" {
		t.Fatalf("first handler in was %s, want hog", app)
	}
	runtime.freezeLoop()

	// Let hog's own dispatch time out and be released before latecomer starts.
	waitFor(t, "hog's dispatch to be abandoned", func() bool {
		_, ok := d.appStallSnapshot("hog")
		return ok
	})

	err := d.deliverAppEvent(context.Background(), "latecomer", appEvent("ticket.created", "tk-2", 2))
	if err == nil {
		t.Fatal("a dispatch into a frozen runtime reported success")
	}
	if stall, ok := d.appStallSnapshot("latecomer"); ok {
		t.Fatalf("latecomer was charged for a loop hog had already frozen: %+v", stall)
	}
	if !strings.Contains(err.Error(), "hog") {
		t.Fatalf("failure does not name the culprit: %v", err)
	}
}

// A handler that yields is still on the loop, and an answered ping does not say
// otherwise. The daemon must keep naming it if it comes back and spins.
//
// This is the case that decides whether the daemon may infer a handler left. It
// may not: the only thing it could infer from is the loop still turning, and a
// handler that yielded and never settled is on a turning loop. Dropping entries
// on an answered ping would leave this freeze named by nobody.
func TestAHandlerThatYieldedIsStillNamedWhenItComesBackAndSpins(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 200 * time.Millisecond
	d.appPingWait = 100 * time.Millisecond
	installApp(t, d, "sleeper", subscribing("ticket.*"))
	installApp(t, d, "latecomer", subscribing("ticket.*"))

	entered := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	runtime := startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	// sleeper enters and yields — it never settles, but the loop keeps turning, so
	// its own dispatch times out against an *answered* ping.
	err := d.deliverAppEvent(context.Background(), "sleeper", appEvent("ticket.created", "tk-1", 1))
	if err == nil {
		t.Fatal("a handler that never returned reported success")
	}
	if !strings.Contains(err.Error(), "did not return within") {
		t.Fatalf("failure = %v, want the hung-handler message on a turning loop", err)
	}

	// Now it resumes and spins: same handler, same entry, loop stops.
	runtime.freezeLoop()

	failure := d.deliverAppEvent(context.Background(), "latecomer", appEvent("ticket.created", "tk-2", 2))
	if failure == nil {
		t.Fatal("a dispatch into a frozen runtime reported success")
	}
	if !strings.Contains(failure.Error(), "sleeper") {
		t.Fatalf("the handler that yielded and came back is no longer named: %v", failure)
	}
	if stall, ok := d.appStallSnapshot("latecomer"); ok {
		t.Fatalf("latecomer was charged for a loop sleeper froze: %+v", stall)
	}
}

// An app that ran and returned is off the loop, and must not be blamed for a
// freeze it is not part of. When nothing is on the loop the daemon has no culprit
// to name and charges the app whose dispatch timed out, which is all it knows.
func TestAHandlerThatAlreadyReturnedIsNotBlamedForALaterFreeze(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 200 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "quick", subscribing("ticket.*"))
	installApp(t, d, "unlucky", subscribing("ticket.*"))

	runtime := startFakeAppRuntime(t, d, nil)
	if err := d.deliverAppEvent(context.Background(), "quick", appEvent("ticket.created", "tk-1", 1)); err != nil {
		t.Fatalf("a handler that returns normally failed: %v", err)
	}

	// The loop stops with no app's handler on it — a host that wedged in its own
	// code, not an app's.
	runtime.freezeLoop()

	err := d.deliverAppEvent(context.Background(), "unlucky", appEvent("ticket.created", "tk-2", 2))
	if err == nil {
		t.Fatal("a dispatch into a frozen runtime reported success")
	}
	if strings.Contains(err.Error(), "quick") {
		t.Fatalf("an app that had already returned was blamed: %v", err)
	}
	if _, ok := d.appStallSnapshot("quick"); ok {
		t.Fatal("an app that had already returned was put on the auto-disable clock")
	}
	if _, ok := d.appStallSnapshot("unlucky"); !ok {
		t.Fatal("with no culprit to name, the app whose dispatch timed out must still be charged")
	}
}

// The other half of the rule. A loop that is turning means nothing is in this
// app's way, so its own handler is what did not return — charged as before.
func TestATurningLoopChargesTheAppWhoseHandlerHung(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 2 * time.Second
	installApp(t, d, "dawdler", subscribing("ticket.*"))

	entered := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	// Pings stay answered: only this one handler is stuck.
	startFakeAppRuntime(t, d, enterOnceThenBlock(entered, release))

	err := d.deliverAppEvent(context.Background(), "dawdler", appEvent("ticket.created", "tk-1", 1))
	if err == nil {
		t.Fatal("a handler that never returned reported success")
	}
	if !strings.Contains(err.Error(), "did not return within") {
		t.Fatalf("failure = %v, want the hung-handler message", err)
	}
	if _, ok := d.appStallSnapshot("dawdler"); !ok {
		t.Fatal("a hung handler must put its own app on the auto-disable clock")
	}
	if rows := invocationsOf(t, d, "dawdler"); len(rows) != 1 || rows[0].Status != appInvocationStatusError {
		t.Fatalf("invocation = %+v, want one with status %q", rows, appInvocationStatusError)
	}
}

// A ping is the arbiter, so it needs its own budget rather than whatever the
// dispatch had left — which is nothing, by construction.
func TestTheLivenessPingIsBoundedIndependently(t *testing.T) {
	if appRuntimePingWait <= 0 {
		t.Fatal("the liveness ping must be bounded")
	}
	if appRuntimePingWait >= appDispatchTimeout {
		t.Fatalf("ping wait %v is not comfortably inside the dispatch timeout %v", appRuntimePingWait, appDispatchTimeout)
	}
	d := &Daemon{}
	if got := d.appPingBudget(); got != appRuntimePingWait {
		t.Fatalf("appPingBudget() = %v, want the shipped %v", got, appRuntimePingWait)
	}
	d.appPingWait = 5 * time.Millisecond
	if got := d.appPingBudget(); got != 5*time.Millisecond {
		t.Fatalf("appPingBudget() = %v, want the override", got)
	}
}
