package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// A park has to survive the daemon that made it.
//
// Restarts are ordinary — an upgrade, a crash, a reboot — and while the park
// lived only in the supervisor's memory each one lazy-started the same broken
// host, spent the whole backoff schedule on it again, and raised a second
// critical notification for one outage the user had already been told about.
func TestParkedRuntimeSurvivesADaemonRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	launches := filepath.Join(t.TempDir(), "launches")
	t.Setenv(appRuntimeHostOverride, countingHostStub(t, launches))

	first := newAppDaemonOn(t, dbPath)
	first.appRuntimeSupervise = supervise.Options{GiveUpAfter: 1}
	installApp(t, first, "greeter", subscribing("ticket.*"))
	if err := first.ensureAppRuntime(); err != nil {
		t.Fatalf("ensure runtime: %v", err)
	}
	waitFor(t, "the crash-looping runtime to be parked", func() bool {
		snapshot, ok := first.appRuntimeSnapshot()
		return ok && snapshot.Phase == supervise.PhaseParked
	})
	parked, _ := first.appRuntimeSnapshot()
	if parked.ParkedAt.IsZero() {
		t.Fatal("a parked runtime carries no park time")
	}
	launchesBefore := launchCount(t, launches)

	// The daemon goes away — an upgrade, a crash, a reboot. Its database does not.
	first.stopAppRuntime()
	stopAppDaemon(t, first)

	second := newAppDaemonOn(t, dbPath)
	second.appRuntimeSupervise = supervise.Options{GiveUpAfter: 1}
	t.Cleanup(second.stopAppRuntime)
	second.restoreAppRuntimePark()

	status := appRuntimeStatus(t, second)
	if status.Runtime == nil {
		t.Fatal("the restarted daemon reports no runtime at all, so nothing tells a reader why apps are dead")
	}
	if status.Runtime.Phase != string(supervise.PhaseParked) {
		t.Fatalf("phase after restart = %q, want parked", status.Runtime.Phase)
	}
	if status.Runtime.ParkedAt == nil {
		t.Fatal("the restored park carries no timestamp")
	}
	if got, want := *status.Runtime.ParkedAt, stampForWire(parked.ParkedAt); got != want {
		t.Fatalf("parked_at = %q after restart, want the original %q", got, want)
	}
	if status.Runtime.RestartAttempt != parked.RestartAttempt {
		t.Fatalf("restart_attempt = %d after restart, want the original %d",
			status.Runtime.RestartAttempt, parked.RestartAttempt)
	}
	if status.Runtime.LastExit == nil || !strings.Contains(*status.Runtime.LastExit, "3") {
		t.Fatalf("last_exit after restart = %v, want the recorded exit code 3", status.Runtime.LastExit)
	}

	// The point of all of it: the broken host is never launched again.
	if got := launchCount(t, launches); got != launchesBefore {
		t.Fatalf("the restarted daemon launched the host %d more time(s); a restored park must spawn nothing",
			got-launchesBefore)
	}
	// One outage, one critical notification. A second one for the same park is
	// the surface that cannot be ignored repeating itself.
	if notes := appNotifications(t, second, notificationKindAppRuntimeParked); len(notes) != 1 {
		t.Fatalf("app-runtime-parked notifications after a restart = %d, want the original 1", len(notes))
	}
}

// The restore has to be in place before anything can lazy-start the runtime. A
// dispatch is the caller that would: it arrives on the first fact an app is due,
// which on a fresh daemon is immediately.
func TestRestoredParkIsInPlaceBeforeTheFirstDispatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	launches := filepath.Join(t.TempDir(), "launches")
	t.Setenv(appRuntimeHostOverride, countingHostStub(t, launches))

	seedParkedRuntime(t, dbPath, 10)

	d := newAppDaemonOn(t, dbPath)
	d.appRuntimeSupervise = supervise.Options{GiveUpAfter: 1}
	installApp(t, d, "greeter", subscribing("ticket.*"))
	t.Cleanup(d.stopAppRuntime)

	// No restoreAppRuntimePark call: the dispatch path builds the supervisor
	// itself, and it must find the park already applied. This is the race the
	// persisted park exists to close, so it is asserted rather than commented.
	err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1))
	if !isRuntimeFailure(err) {
		t.Fatalf("dispatch into a restored park returned %v, want a runtime failure", err)
	}
	if !strings.Contains(err.Error(), "parked") || !strings.Contains(err.Error(), "attn app runtime restart") {
		t.Fatalf("the dispatch error does not say what happened or how to fix it: %q", err)
	}
	if got := launchCount(t, launches); got != 0 {
		t.Fatalf("a dispatch launched the host %d time(s) despite the persisted park", got)
	}
	snapshot, ok := d.appRuntimeSnapshot()
	if !ok || snapshot.Phase != supervise.PhaseParked {
		t.Fatalf("snapshot after a dispatch = %+v, want parked", snapshot)
	}
	// Not the app's fault, so it must not be on the auto-disable clock.
	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 1 || rows[0].Status != appInvocationStatusRuntimeError {
		t.Fatalf("invocations = %+v, want one runtime_error", rows)
	}
	if _, stalled := d.appStallSnapshot("greeter"); stalled {
		t.Fatal("the app is on the auto-disable clock for the runtime being parked")
	}
}

// The way out has to erase the way in. A restart that revived the runtime but
// left the row behind would re-park it on the next daemon start — the door back
// from parked opening exactly once.
func TestRestartClearsThePersistedPark(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	seedParkedRuntime(t, dbPath, 10)
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "sleep 60"))

	d := newAppDaemonOn(t, dbPath)
	t.Cleanup(d.stopAppRuntime)
	d.restoreAppRuntimePark()

	revived := appRuntimeRestart(t, d)
	if revived.Was != string(supervise.PhaseParked) {
		t.Fatalf("was = %q, want parked", revived.Was)
	}
	if revived.Runtime.Phase == string(supervise.PhaseParked) || revived.Runtime.ParkedAt != nil {
		t.Fatalf("restart left the runtime parked: %+v", revived.Runtime)
	}
	if _, ok, err := d.store.GetSupervisedPark(appRuntimeChildName); err != nil || ok {
		t.Fatalf("the persisted park outlived the restart: ok=%v err=%v", ok, err)
	}
}

// A daemon that has never run an app must still say so. Reading the park back is
// not a reason to build a supervisor, and "not started" is a different answer
// from "stopped" — the second sends a reader looking for a fault.
func TestRestoreLeavesAnUnparkedDaemonUntouched(t *testing.T) {
	d := newAppDaemonOn(t, filepath.Join(t.TempDir(), "attn.db"))
	installApp(t, d, "greeter", subscribing("ticket.*"))
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "sleep 60"))

	d.restoreAppRuntimePark()

	if status := appRuntimeStatus(t, d); status.Runtime != nil {
		t.Fatalf("a daemon with no persisted park reported a runtime: %+v", status.Runtime)
	}
}

// ---------------------------------------------------------------------------

// newAppDaemonOn is newAppDaemon against a database the test owns, so one test
// can park a runtime, drop the daemon, and bring a second one up over the same
// state — which is all a daemon restart is.
func newAppDaemonOn(t *testing.T, dbPath string) *Daemon {
	t.Helper()
	persistent, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	// The bus is built around the store it was constructed with, so the two move
	// together or the daemon publishes into a database nobody reads.
	d.stopEventBus()
	_ = d.store.Close()
	d.store = persistent
	d.eventBus = nil
	d.ensureEventBus()
	if err := d.eventBus.Start(); err != nil {
		t.Fatalf("start the event bus: %v", err)
	}
	t.Cleanup(func() { stopAppDaemon(t, d) })
	return d
}

// stopAppDaemon takes a daemon down the way a process exit does, database and
// all. Idempotent, so a test may end one early and still have its cleanup run.
func stopAppDaemon(t *testing.T, d *Daemon) {
	t.Helper()
	d.stopEventBus()
	_ = d.store.Close()
}

// seedParkedRuntime writes the row a previous daemon's give-up would have left.
func seedParkedRuntime(t *testing.T, dbPath string, attempts int) time.Time {
	t.Helper()
	s, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	defer func() { _ = s.Close() }()
	parkedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	code := 3
	if err := s.SaveSupervisedPark(store.SupervisedPark{
		Child:          appRuntimeChildName,
		ParkedAt:       parkedAt,
		RestartAttempt: attempts,
		ExitAt:         parkedAt,
		ExitCode:       &code,
	}); err != nil {
		t.Fatalf("seed park: %v", err)
	}
	return parkedAt
}

// countingHostStub is a runtime host that records every launch and dies, so a
// test can assert a spawn that must not happen rather than only its aftermath.
func countingHostStub(t *testing.T, marks string) string {
	t.Helper()
	return writeExecutableStub(t, "echo started >> "+marks+"\nexit 3")
}

func launchCount(t *testing.T, marks string) int {
	t.Helper()
	data, err := os.ReadFile(marks)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read launch marks: %v", err)
	}
	return strings.Count(string(data), "started")
}
