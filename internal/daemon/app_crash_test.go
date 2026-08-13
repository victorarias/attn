package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/supervise"
)

// The crash rule: an app whose error escapes every handler takes the shared
// sidecar down with it, and is charged for doing so.
//
// It is the second auto-disable clock because the first one cannot reach this
// case. A crash kills the process before the delivery finishes, the event is
// redelivered against a fresh runtime, and the next crash may land on a
// different event entirely — so "stuck on the same event for fifteen minutes"
// never accrues for the one app that stops every other app.
//
// The host names the culprit from the stack of the error that killed it, which
// is why these tests drive the wire method rather than a Go helper: the
// attribution is a claim the host makes and the daemon acts on.

func reportCrash(t *testing.T, d *Daemon, app, kind, message string) {
	t.Helper()
	params, err := json.Marshal(appRuntimeCrashParams{App: app, Kind: kind, Error: message})
	if err != nil {
		t.Fatalf("marshal crash params: %v", err)
	}
	if _, err := d.appRuntimeMethod(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"crash"`),
		Method:  appRuntimeCrashedMethod,
		Params:  params,
	}); err != nil {
		t.Fatalf("%s: %v", appRuntimeCrashedMethod, err)
	}
}

func TestAppThatKeepsCrashingTheRuntimeIsDisabledAndSaysSoThreeWays(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "leaker", subscribing("ticket.*"))

	for i := 1; i < appCrashStrikes; i++ {
		reportCrash(t, d, "leaker", "unhandledRejection", "TypeError: fetch failed")
		clock.advance(time.Minute)
		if !appEnabled(t, d, "leaker") {
			t.Fatalf("leaker was disabled on strike %d of %d", i, appCrashStrikes)
		}
	}

	reportCrash(t, d, "leaker", "unhandledRejection", "TypeError: fetch failed")

	if appEnabled(t, d, "leaker") {
		t.Fatalf("leaker is still enabled after %d crashes", appCrashStrikes)
	}
	facts := appFacts(t, d, FactAppEnabledChanged)
	if len(facts) != 1 || facts[0].Subject != "leaker" {
		t.Fatalf("app.enabled.changed facts = %+v, want one for leaker", facts)
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 {
		t.Fatalf("auto-disable notifications = %d, want 1", len(notes))
	}
	// It has to say what happened and how to come back, or the author is left
	// with an app that stopped for no reason they can see.
	if !strings.Contains(notes[0].Body, "attn app enable leaker") {
		t.Fatalf("the notification does not name the way back: %q", notes[0].Body)
	}
	if !strings.Contains(notes[0].Body, "unhandledRejection") {
		t.Fatalf("the notification does not say what failed: %q", notes[0].Body)
	}
	if !strings.Contains(notes[0].Detail, "TypeError") {
		t.Fatalf("the notification detail does not carry the error: %q", notes[0].Detail)
	}
	if created := appFacts(t, d, FactNotificationCreated); len(created) != 1 {
		t.Fatalf("notification.created facts = %+v, want one", created)
	}
}

// Crashes far apart are not a crash loop. An app that took the runtime down
// once a month is not the thing this rule exists to stop.
func TestCrashesOutsideTheWindowDoNotAccumulate(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "occasional", subscribing("ticket.*"))

	for i := 0; i < appCrashStrikes*3; i++ {
		reportCrash(t, d, "occasional", "uncaughtException", "Error: transient")
		clock.advance(appCrashWindow + time.Minute)
	}

	if !appEnabled(t, d, "occasional") {
		t.Fatalf("an app crashing once per %s window was disabled", appCrashWindow)
	}
}

// The host sends an empty name when the error carried no stack naming a loaded
// bundle. Nothing is charged: guessing which app was running is exactly how an
// innocent app gets disabled, which is why the host reads the stack at all.
func TestACrashThatNamesNoAppChargesNobody(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "innocent", subscribing("ticket.*"))

	for i := 0; i < appCrashStrikes*2; i++ {
		reportCrash(t, d, "", "unhandledRejection", "Error: something in the host")
	}

	if !appEnabled(t, d, "innocent") {
		t.Fatal("an unattributed crash disabled an app")
	}
	if notes := appNotifications(t, d, notificationKindAppAutoDisabled); len(notes) != 0 {
		t.Fatalf("auto-disable notifications = %d, want 0", len(notes))
	}
}

// Enabling is the way back, so it clears the streak. The case that depends on
// it is an app switched off and on by hand while carrying strikes: without the
// clear it is disabled again on its very next crash, against a clock it never
// got to restart.
func TestEnablingAnAppClearsItsCrashStreak(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "leaker", subscribing("ticket.*"))

	for i := 0; i < appCrashStrikes-1; i++ {
		reportCrash(t, d, "leaker", "unhandledRejection", "TypeError: fetch failed")
	}
	if !appEnabled(t, d, "leaker") {
		t.Fatalf("leaker was disabled before its %dth strike", appCrashStrikes)
	}

	if resp := appSetEnabled(t, d, "leaker", false); !resp.Ok {
		t.Fatalf("disable leaker: %+v", resp)
	}
	if resp := appSetEnabled(t, d, "leaker", true); !resp.Ok {
		t.Fatalf("re-enable leaker: %+v", resp)
	}

	reportCrash(t, d, "leaker", "unhandledRejection", "TypeError: fetch failed")
	if !appEnabled(t, d, "leaker") {
		t.Fatal("a re-enabled app was disabled again on its very next crash")
	}
}

// The strike count must stay under what supervise gives the sidecar before it
// parks it: parking stops every app, so the culprit has to be gone first.
func TestCrashStrikesFireBeforeTheSupervisorParksTheRuntime(t *testing.T) {
	if appCrashStrikes < 2 {
		t.Fatalf("appCrashStrikes = %d; one crash can be a machine event, not a broken app", appCrashStrikes)
	}
	if appCrashStrikes >= supervise.DefaultGiveUpAfter {
		t.Fatalf("appCrashStrikes = %d but the sidecar is parked after %d restarts, so every app loses its runtime before the culprit is disabled",
			appCrashStrikes, supervise.DefaultGiveUpAfter)
	}
}
