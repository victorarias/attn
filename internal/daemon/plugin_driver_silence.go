package daemon

import (
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// A driver-declared session's state is only as current as its driver. When the
// driver's connection is gone — the runtime crashed, or a replacement started
// and never adopted the run — nothing else moves that session's state, and
// before this it stayed frozen for as long as the daemon lived. Session
// 8360d126 sat at `idle` for six hours while its pane worked.
//
// So a driver going away arms an alarm per live run, and any word from a driver
// about that run disarms it. Word, not registration: a driver that registers,
// declines to adopt the run and says nothing is exactly as blind as one that
// never came back. A healthy reconnect always speaks — the suite's hello makes
// the driver report metadata — so the witness is free.
//
// What the alarm applies is `unknown`, which is the daemon admitting it cannot
// tell rather than a guess: `attention.OpensTurn` treats it as wanting the user
// and `BreaksSnooze` lets it through, which is what those six hours needed. The
// next plugin report outranks it, so recovery needs no special path.

// pluginDriverSilenceGrace is a tripwire, not a deadline: nothing healthy is
// this slow. A replacement runtime is normally connected inside a second, and
// the slowest legitimate path adds up to ~65s — supervise.DisconnectGrace (5s)
// plus its restart backoff (capped at 30s, supervise.RestartBackoff) plus the
// suite's own reconnect backoff (capped at 30s, plugins/attn-pi/suite/core.ts).
// Measured on a live pi session across a 60s daemon outage: the replacement
// driver reported 8s after the daemon came back.
const pluginDriverSilenceGrace = 2 * time.Minute

// pluginDriverSilenceWatch holds one pending alarm per session. A session is in
// here only while its driver is missing, so a healthy daemon holds none.
type pluginDriverSilenceWatch struct {
	mu     sync.Mutex
	armed  map[string]*time.Timer
	closed bool
}

func newPluginDriverSilenceWatch() *pluginDriverSilenceWatch {
	return &pluginDriverSilenceWatch{armed: map[string]*time.Timer{}}
}

// arm replaces any pending alarm for the session. Replacing rather than keeping
// the older one is deliberate: the newest disconnect is the one the grace is
// owed from.
func (w *pluginDriverSilenceWatch) arm(sessionID string, grace time.Duration, fire func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if timer, ok := w.armed[sessionID]; ok {
		timer.Stop()
	}
	w.armed[sessionID] = time.AfterFunc(grace, fire)
}

// disarm cancels a session's alarm and reports whether one was pending, so the
// hot report path can skip the log line when nothing was waiting.
func (w *pluginDriverSilenceWatch) disarm(sessionID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	timer, ok := w.armed[sessionID]
	if !ok {
		return false
	}
	timer.Stop()
	delete(w.armed, sessionID)
	return true
}

// stop cancels every pending alarm and refuses new ones, for daemon shutdown: a
// timer that fires into a stopped daemon writes to a closed store.
func (w *pluginDriverSilenceWatch) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	for id, timer := range w.armed {
		timer.Stop()
		delete(w.armed, id)
	}
}

func (d *Daemon) pluginDriverSilence() *pluginDriverSilenceWatch {
	d.pluginDriverSilenceOnce.Do(func() {
		d.pluginDriverSilenceWatch = newPluginDriverSilenceWatch()
	})
	return d.pluginDriverSilenceWatch
}

// armPluginDriverSilenceWatch starts the grace for every run a plugin owns,
// for a plugin whose connection just dropped.
func (d *Daemon) armPluginDriverSilenceWatch(pluginName string) {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListAgentDriverRuns(pluginName) {
		d.armPluginDriverSilenceWatchForRun(run.SessionID, run.RunID, pluginName)
	}
}

// armPluginDriverSilenceWatchForEveryRun starts the grace for every live run on
// the daemon, whichever plugin owns it. This is daemon startup: nothing is
// connected yet, and a run whose plugin no longer appears in the catalog at all
// would otherwise be armed by nobody.
func (d *Daemon) armPluginDriverSilenceWatchForEveryRun() {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListActiveAgentDriverRuns() {
		d.armPluginDriverSilenceWatchForRun(run.SessionID, run.RunID, run.PluginName)
	}
}

func (d *Daemon) armPluginDriverSilenceWatchForRun(sessionID, runID, pluginName string) {
	grace := d.pluginDriverSilenceGrace()
	if grace <= 0 {
		return
	}
	d.pluginDriverSilence().arm(sessionID, grace, func() {
		d.declarePluginDriverSilent(sessionID, runID, pluginName, grace)
	})
}

// notePluginDriverReport records that a driver still speaks for this session.
func (d *Daemon) notePluginDriverReport(sessionID string) {
	if d.pluginDriverSilence().disarm(sessionID) {
		d.logf("plugin driver silence cleared: session=%s", sessionID)
	}
}

// forgetPluginDriverSilenceWatch drops a session's alarm because the session
// itself is gone; there is nothing left to declare unknown.
func (d *Daemon) forgetPluginDriverSilenceWatch(sessionID string) {
	d.pluginDriverSilence().disarm(sessionID)
}

// pluginDriverSilenceGrace is the tripwire, overridable in tests.
func (d *Daemon) pluginDriverSilenceGrace() time.Duration {
	if d.pluginDriverSilenceGraceOverride > 0 {
		return d.pluginDriverSilenceGraceOverride
	}
	return pluginDriverSilenceGrace
}

// declarePluginDriverSilent applies `unknown` to a session whose driver never
// came back. It re-reads everything it needs: the alarm was armed minutes ago
// and the session may have been closed, relaunched, or moved on since.
func (d *Daemon) declarePluginDriverSilent(sessionID, runID, pluginName string, grace time.Duration) {
	if d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID != runID {
		// A relaunch minted a new run; whatever it declares is current.
		return
	}
	if !pluginDriverDeclaredStates[session.State] {
		// Only a live declaration can go stale. `recoverable` and `exited` are
		// lifecycle facts nobody is waiting on a driver to refresh.
		return
	}
	d.logf(
		"plugin driver silent: session=%s plugin=%s run=%s state=%s no report for %s; declaring unknown",
		sessionID, pluginName, runID, session.State, grace,
	)
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     protocol.StateUnknown,
		cause:     pluginDriverSilent{},
		origin:    stateOrigin{source: stateSourcePluginDriver, detail: "driver silent"},
	})
}

// pluginDriverDeclaredStates are the states a driver declares and therefore the
// only ones its silence can invalidate.
var pluginDriverDeclaredStates = map[protocol.SessionState]bool{
	protocol.SessionStateWorking:         true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStatePendingApproval: true,
}
