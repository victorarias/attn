package daemon

import (
	"path/filepath"
	"testing"
)

// Support for running daemon tests inside a `testing/synctest` bubble, where
// time.Now is a fake clock that advances only when every goroutine in the bubble
// is durably blocked. Two rules make a daemon fit inside one, and both are
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
// What cannot come inside at all: a real socket, a real PTY, a child process, an
// fsnotify watcher. A goroutine blocked on a real file descriptor is not
// durably blocked, so the fake clock never advances and the bubble hangs. Those
// tests keep their poll helpers.

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
	})
}
