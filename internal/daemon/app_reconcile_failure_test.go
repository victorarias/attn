package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// What happens when a rebuild does not succeed: the attempt is visible while it
// runs, it survives a daemon that dies mid-flight, the request stays owed
// through every failure, and attn eventually stops trying and says so.
//
// See docs/plans/2026-08-14-ext-app-reconcile-handler.md, gates 4 and 7.

// reconcilingApp installs a subscribed app that declares reconcile, and puts one
// version_changed request in front of it by applying a second version.
func reconcilingApp(t *testing.T, d *Daemon, name string) store.AppVersion {
	t.Helper()
	installApp(t, d, name, subscribing("ticket.*"))
	second := subscribing("ticket.*")
	second.Description = "the version that owes a rebuild"
	return installApp(t, d, name, second)
}

func appReconcilePreDrain(t *testing.T, d *Daemon, name string) error {
	t.Helper()
	return d.appPreDrain(name)(context.Background(), bus.Consumer{Name: apps.ConsumerName(name)}, nil)
}

// A running attempt is durable, so `attn app status` can report a rebuild in
// flight without inferring one from a daemon's memory — and a daemon that dies
// mid-rebuild leaves a row the next startup closes rather than an attempt that
// looks live forever.
func TestAReconcileInterruptedByARestartIsRepairedAndStaysOwed(t *testing.T) {
	d := newAppDaemon(t)
	version := reconcilingApp(t, d, "greeter")

	claim, err := d.store.AppReconcilePending("greeter")
	if err != nil || len(claim.Requests) != 1 {
		t.Fatalf("claim = %+v, %v", claim, err)
	}
	if _, err := d.store.StartAppInvocation(store.AppInvocation{
		AppName: "greeter", VersionID: version.ID, Kind: store.AppInvocationKindReconcile,
		Handler: "reconcile", Status: store.AppInvocationStatusRunning, StartedAt: d.appNow(),
		ReconcileReason:  `{"causes":["version_changed"]}`,
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	}); err != nil {
		t.Fatalf("start the attempt a dying daemon leaves behind: %v", err)
	}

	running := appStatus(t, d, "greeter").AppStatusResult.Reconcile
	if running.State != appReconcileStateRunning || running.CurrentAttempt == nil {
		t.Fatalf("status while an attempt runs = %+v", running)
	}
	if running.CurrentAttempt.DurationMs != nil {
		t.Fatalf("a running attempt reported a duration: %+v", running.CurrentAttempt)
	}

	if err := d.repairInterruptedAppInvocations(); err != nil {
		t.Fatalf("startup repair: %v", err)
	}

	repaired, ok, err := d.store.LatestOwedAppReconcileInvocation("greeter")
	if err != nil || !ok || repaired.Status != store.AppInvocationStatusInterrupted {
		t.Fatalf("repaired attempt = %+v ok=%t err=%v", repaired, ok, err)
	}
	after, err := d.store.AppReconcilePending("greeter")
	if err != nil || after.ThroughRequestID != claim.ThroughRequestID || len(after.Requests) != 1 {
		t.Fatalf("interruption moved the request: %+v, %v", after, err)
	}
	status := appStatus(t, d, "greeter").AppStatusResult.Reconcile
	if status.State != appReconcileStateOwed || status.CurrentAttempt != nil {
		t.Fatalf("status after repair = %+v", status)
	}
	if status.Reason == nil || status.Reason.ThroughSeq != int(claim.ThroughSeq) {
		t.Fatalf("status after repair carries no fence: %+v", status.Reason)
	}
}

// Interruption is not the app's fault, so it must not move the app closer to
// being disabled — the stall clock belongs to failures the app caused.
func TestAnInterruptedAttemptDoesNotAdvanceTheStallClock(t *testing.T) {
	d := newAppDaemon(t)
	version := reconcilingApp(t, d, "greeter")
	claim, err := d.store.AppReconcilePending("greeter")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.StartAppInvocation(store.AppInvocation{
		AppName: "greeter", VersionID: version.ID, Kind: store.AppInvocationKindReconcile,
		Handler: "reconcile", StartedAt: d.appNow(), ReconcileReason: `{}`,
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.repairInterruptedAppInvocations(); err != nil {
		t.Fatal(err)
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("an interrupted attempt put the app on the auto-disable clock: %+v", stall)
	}
}

// A rebuild that keeps throwing keeps the request owed, keeps every attempt in
// the log, and after the stall window attn stops trying: the app is disabled and
// a durable notification says so, with the fence and the way back.
func TestAReconcileThatKeepsThrowingDisablesTheAppAndSaysSo(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	reconcilingApp(t, d, "greeter")
	runtime := startFakeAppRuntime(t, d, nil)
	runtime.reconcile = func(*fakeAppRuntime, appReconcileRequest) error {
		return errors.New("TypeError: snapshot.sessions is not iterable")
	}
	claim, err := d.store.AppReconcilePending("greeter")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		if err := appReconcilePreDrain(t, d, "greeter"); err == nil {
			t.Fatal("a throwing reconcile reported success")
		}
		clock.advance(3 * time.Minute)
		if !appEnabled(t, d, "greeter") {
			t.Fatalf("greeter was disabled after %d attempt(s) inside the window", i+1)
		}
	}
	status := appStatus(t, d, "greeter").AppStatusResult
	if status.Stall == nil || status.Stall.Kind != appStallKindReconcile ||
		protocol.Deref(status.Stall.ThroughRequestID) != int(claim.ThroughRequestID) {
		t.Fatalf("stall = %+v, want the reconcile claim", status.Stall)
	}
	if status.Reconcile.LastError == nil || !strings.Contains(*status.Reconcile.LastError, "TypeError") {
		t.Fatalf("status does not carry the last failure: %+v", status.Reconcile)
	}

	clock.advance(4 * time.Minute)
	if err := appReconcilePreDrain(t, d, "greeter"); err == nil {
		t.Fatal("a throwing reconcile reported success")
	}

	if appEnabled(t, d, "greeter") {
		t.Fatal("greeter is still enabled after 15 minutes owing the same rebuild")
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 {
		t.Fatalf("auto-disable notifications = %d, want 1", len(notes))
	}
	if !strings.Contains(notes[0].Body, "attn app enable greeter") ||
		!strings.Contains(notes[0].Body, "remains owed") {
		t.Fatalf("the notification does not say what is owed or how to get back: %q", notes[0].Body)
	}
	// Disabling never completes the rebuild: the whole point of keeping it owed
	// is that enabling the app again runs it rather than resuming past it.
	after, err := d.store.AppReconcilePending("greeter")
	if err != nil || after.ThroughRequestID != claim.ThroughRequestID {
		t.Fatalf("the request moved when the app was disabled: %+v, %v", after, err)
	}
	attempts := 0
	for _, inv := range invocationsOf(t, d, "greeter") {
		if inv.Kind == store.AppInvocationKindReconcile {
			attempts++
			if inv.Status != store.AppInvocationStatusError || inv.ThroughRequestID != claim.ThroughRequestID {
				t.Fatalf("attempt = %+v", inv)
			}
		}
	}
	// One row per try, and at least the five this test drove — the daemon's own
	// consumer loop may have retried the same claim alongside them.
	if attempts < 5 {
		t.Fatalf("recorded attempts = %d, want one per try", attempts)
	}
}

// Commands are not bus deliveries, so nothing else would hold them behind the
// fence. Letting one mutate the collections halfway through a rebuild is what
// would make the fence meaningless.
func TestACommandIsRefusedByNameWhileAReconcileIsOwed(t *testing.T) {
	d := newAppDaemon(t)
	manifest := subscribing("ticket.*")
	manifest.Commands = []appbuild.Command{{Name: "refresh"}}
	installApp(t, d, "greeter", manifest)
	second := manifest
	second.Description = "the version that owes a rebuild"
	installApp(t, d, "greeter", second)
	startFakeAppRuntime(t, d, nil)

	result := newAppCommandCaller().invoke(t, d, "greeter", "refresh", "")
	if result.Success {
		t.Fatal("a command ran across the reconcile fence")
	}
	if protocol.Deref(result.ErrorCode) != protocol.ErrorCodeReconcileOwed {
		t.Fatalf("refusal code = %q, want %q", protocol.Deref(result.ErrorCode), protocol.ErrorCodeReconcileOwed)
	}
	if result.Reconcile == nil || len(result.Reconcile.Causes) == 0 {
		t.Fatalf("the refusal carries no structured reason: %+v", result.Reconcile)
	}
	refusal := protocol.Deref(result.Error)
	if !strings.Contains(refusal, "greeter") || !strings.Contains(refusal, "rebuilding") {
		t.Fatalf("the refusal does not name the app and what it is doing: %q", refusal)
	}

	// And it is a refusal, not a failure: nothing ran, so nothing is charged.
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("a refused command put the app on the auto-disable clock: %+v", stall)
	}
}

// Retrying code that does not exist cannot heal, so a gap discovered under a
// version with no reconcile handler is loud immediately rather than after
// fifteen minutes of retries.
func TestAGapWithNoHandlerDisablesTheAppWithoutMovingItsCursor(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribingWithoutReconcile("ticket.*"))
	seedAppConsumer(t, d, "greeter", true, 1)

	gap := &bus.Gap{Cursor: 1, Earliest: 4, Head: 6, Missed: 2}
	err := d.appPreDrain("greeter")(context.Background(),
		bus.Consumer{Name: apps.ConsumerName("greeter")}, gap)
	if err == nil || !strings.Contains(err.Error(), "does not declare reconcile") {
		t.Fatalf("pre-drain error = %v", err)
	}
	if appEnabled(t, d, "greeter") {
		t.Fatal("an app that cannot reconcile a gap was left enabled")
	}
	consumer, _, err := d.store.GetBusConsumer(apps.ConsumerName("greeter"))
	if err != nil || consumer.Cursor != 1 {
		t.Fatalf("cursor = %d, %v; a gap it cannot rebuild must not move it", consumer.Cursor, err)
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "reconcile") {
		t.Fatalf("notifications = %+v", notes)
	}
	var missing []store.AppInvocation
	for _, inv := range invocationsOf(t, d, "greeter") {
		if inv.Handler == "missing_reconcile" {
			missing = append(missing, inv)
		}
	}
	if len(missing) != 1 || missing[0].Kind != store.AppInvocationKindReconcile {
		t.Fatalf("missing_reconcile invocations = %+v, want exactly one", missing)
	}

	// Status names the state rather than leaving a reader to infer it from a
	// disabled app that owes something.
	status := appStatus(t, d, "greeter").AppStatusResult.Reconcile
	if status.State != appReconcileStateUnsupported || status.Reason == nil {
		t.Fatalf("status = %+v", status)
	}
}

// A version move under a handler-less subscribed version is refused before the
// pointer moves: installing a version attn already knows it cannot serve safely
// would be choosing the broken state on purpose.
func TestAVersionMoveIsRefusedWhenTheSubscribedVersionCannotReconcile(t *testing.T) {
	d := appApplyDaemon(t)
	legacy := `{"name":"greeter","attn_app_api":1,"entrypoint":"src/index.ts","subscribe":[{"events":["ticket.*"]}]}`
	firstHash := stageArtifact(t, d, "greeter", legacy, "export default {}")
	if resp := appApply(t, d, "greeter", firstHash, legacy); !resp.Ok {
		t.Fatalf("first apply: %v", protocol.Deref(resp.Error))
	}
	first, _, err := d.store.GetApp("greeter")
	if err != nil {
		t.Fatal(err)
	}

	secondHash := stageArtifact(t, d, "greeter", legacy, "export default {} // edited")
	resp := appApply(t, d, "greeter", secondHash, legacy)
	if resp.Ok {
		t.Fatal("a subscribed version with no reconcile handler was applied over an existing one")
	}
	message := protocol.Deref(resp.Error)
	for _, want := range []string{"greeter", "reconcile = true", "version 1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("refusal %q does not contain %q", message, want)
		}
	}
	app, _, err := d.store.GetApp("greeter")
	if err != nil || app.CurrentVersionID != first.CurrentVersionID {
		t.Fatalf("the pointer moved despite the refusal: %+v, %v", app, err)
	}
	if claim, err := d.store.AppReconcilePending("greeter"); err != nil || len(claim.Requests) != 0 {
		t.Fatalf("a refused move left a request behind: %+v, %v", claim, err)
	}
}
