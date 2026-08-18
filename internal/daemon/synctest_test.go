package daemon

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// Support for running daemon tests inside a `testing/synctest` bubble, where
// time.Now is a fake clock that advances only when every goroutine in the bubble
// is durably blocked. Four rules make a daemon fit inside one, and all are
// consequences of the same thing — the bubble is the boundary.
//
// 1. Build the daemon OUTSIDE the bubble. store.New opens a database/sql handle,
//    whose connectionOpener goroutine lives as long as the DB; opened inside, it
//    joins the bubble and never exits, and synctest reports a deadlock even
//    though the test passed.
//
// 2. Stop the daemon's background subsystems from INSIDE it. Watchers, timers
//    and countdowns started by the code under test are bubble goroutines; a
//    watcher still parked on its select when the test body returns is reported
//    as "blocked goroutines remain".
//
// 3. Seed anything time-stamped INSIDE the bubble. The bubble's clock starts at
//    2000-01-01, so a row a fixture stamps with time.Now outside it is dated
//    decades in the future. A turn opened outside and settled inside settles
//    BEFORE it opened, and reads as still owed.
//
// 4. Let every goroutine the body started finish before the body returns. The
//    bubble ends only when the last goroutine in it exits, so one still parked
//    when the test body returns is reported as "blocked goroutines remain".
//    Something the code under test spawned and left sleeping counts — see
//    settleStopClassification.
//
// What cannot come inside at all: a real socket, a real PTY, a child process, an
// fsnotify watcher. A goroutine blocked on a real file descriptor is not
// durably blocked, so the fake clock never advances and the bubble hangs. Those
// tests keep their poll helpers.
//
// Nor can a goroutine with no exit path: `go d.wsHub.run()` loops forever over
// its channels, so a test that starts the hub inside a bubble never finishes
// (measured: the bubble hangs until the test binary's own timeout). Giving the
// hub a quit channel is a production change, so those tests stay boundary-bound.

// newBubbleDaemon builds a daemon for a synctest bubble. Call it ABOVE
// synctest.Test with the outer T — see rule 1.
func newBubbleDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

// stopDaemonBackground registers a cleanup that stops every daemon subsystem
// owning a goroutine or a timer. Call it INSIDE the bubble with the bubble's T,
// whose cleanups run inside the bubble — see rule 2. It mirrors the subsystem
// list in Daemon.Stop, minus everything that only a started daemon owns
// (listener, HTTP server, PID lock).
func stopDaemonBackground(t *testing.T, d *Daemon) {
	t.Helper()
	t.Cleanup(func() {
		d.stopAllTranscriptWatchers()
		d.stopNudgeCountdowns()
		d.stopAutoSettleTimers()
		d.stopSnoozeTimers()
		d.stopNotebookWatcher()
		d.stopFsWatchers()
		d.pluginDriverSilence().stop()
	})
}

// requireDone asserts a goroutine's done channel has been closed. Inside a bubble
// there is nothing to wait on with a deadline: synctest.Wait() returns once every
// other goroutine is durably blocked, so a channel still open at that point is
// open because the goroutine never finished, not because the read was early.
func requireDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	synctest.Wait()
	select {
	case <-done:
	default:
		t.Fatal(what)
	}
}

// requireNoOutbound asserts a WebSocket test client was sent nothing. Outside a
// bubble this claim costs a tolerance window and is only ever "nothing yet";
// after synctest.Wait there is nothing left that could still send.
func requireNoOutbound(t *testing.T, client *wsClient, what string) {
	t.Helper()
	synctest.Wait()
	select {
	case outbound := <-client.send:
		t.Fatalf("%s: %s", what, string(outbound.payload))
	default:
	}
}

// requireOutbound is requireNoOutbound's positive: the message the daemon owes
// this client is on its queue once the bubble has settled, or it is never coming.
func requireOutbound(t *testing.T, client *wsClient, what string) outboundMessage {
	t.Helper()
	synctest.Wait()
	select {
	case outbound := <-client.send:
		return outbound
	default:
		t.Fatal(what)
		return outboundMessage{}
	}
}

// settleStopClassification lets the goroutine handleStop spawns run out. That
// goroutine re-reads the stop transcript on a retry loop (internal/agent's
// claudeTranscriptRetryWindow, 2s, every 100ms) and outlives the assertions of a
// test that only cares about handleStop's synchronous side effects. Outside a
// bubble it is invisible; inside one it is a bubble goroutine still sleeping when
// the body returns, which is rule 4. Sleeping past its window retires it.
func settleStopClassification(t *testing.T) {
	t.Helper()
	time.Sleep(4 * time.Second)
	synctest.Wait()
}
