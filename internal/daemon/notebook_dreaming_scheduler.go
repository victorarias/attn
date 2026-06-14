package daemon

import (
	"errors"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/victorarias/attn/internal/notebook"
)

// Dreaming scheduler — phase B1 (deterministic, no LLM).
//
// This file makes the dreaming pass RUN on a schedule and remember what it saw
// across daemon restarts. A timezone-aware cron drives a nightly harvest that
// merges into the persisted candidate set under .attn/dreams/; the LLM promote
// pass that turns candidates into durable memory arrives in the follow-up PR.
//
// Lifecycle mirrors the workspace-context janitor: a single-flight guard so two
// runs never overlap, a filesystem lock for crash auditability, and startup
// recovery that clears a lock orphaned by a crashed run. The scheduler is a
// ticker loop that asks, each tick, whether a run is due — granularity of a
// minute is ample for a daily janitor and keeps catch-up logic trivial.

const (
	// defaultDreamingFrequency runs the pass nightly at 03:00 in the configured
	// timezone — quiet hours, after a day's journals and dispatches have landed.
	defaultDreamingFrequency = "0 3 * * *"

	// defaultDreamSchedulerInterval is how often the scheduler checks whether a
	// run is due. A daily janitor does not need finer granularity.
	defaultDreamSchedulerInterval = time.Minute
)

// errDreamRunning signals that a dreaming run is already in progress; the
// scheduler treats it as a benign skip rather than an error to log loudly.
var errDreamRunning = errors.New("dreaming pass already running")

// dreamingEnabled reports whether the notebook.dreaming.enabled gate is on.
func (d *Daemon) dreamingEnabled() bool {
	if d.store == nil {
		return false
	}
	return parseBooleanSetting(d.store.GetSetting(SettingNotebookDreamingEnabled))
}

// dreamingFrequency returns the configured cron frequency or the default.
func (d *Daemon) dreamingFrequency() string {
	if d.store != nil {
		if f := strings.TrimSpace(d.store.GetSetting(SettingNotebookDreamingFrequency)); f != "" {
			return f
		}
	}
	return defaultDreamingFrequency
}

// dreamingSchedule parses the configured frequency into a cron schedule, also
// returning the raw expression for display/logging.
func (d *Daemon) dreamingSchedule() (cron.Schedule, string, error) {
	raw := d.dreamingFrequency()
	sched, err := cron.ParseStandard(raw)
	if err != nil {
		return nil, raw, err
	}
	return sched, raw, nil
}

// dreamingLocation returns the configured IANA timezone, falling back to the
// machine's local time when unset or unparseable (so a bad setting degrades to a
// sensible default rather than disabling the scheduler).
func (d *Daemon) dreamingLocation() *time.Location {
	if d.store == nil {
		return time.Local
	}
	tz := strings.TrimSpace(d.store.GetSetting(SettingNotebookDreamingTimezone))
	if tz == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	d.logf("dreaming scheduler: invalid timezone %q, using local time", tz)
	return time.Local
}

// parseDreamTime parses a persisted RFC3339 timestamp.
func parseDreamTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// startDreamingScheduler clears any orphaned lock from a crashed run, then blocks
// running the scheduler tick loop until done is closed. Intended to be launched in
// its own goroutine from Start.
//
// Shutdown is by done alone (close(d.done) in Stop), like the daemon's other
// d.done-driven loops; there is no explicit join. A harvest in flight when done
// closes is not waited for, which is safe: state writes are atomic (temp+rename)
// and a lock left by an abrupt stop is reclaimed by ClearOrphanDreamLocks on the
// next start — the same crash path the lock is designed around.
func (d *Daemon) startDreamingScheduler(done <-chan struct{}) {
	if root, err := d.notebookRoot(); err == nil {
		if n, err := notebook.ClearOrphanDreamLocks(root); err != nil {
			d.logf("dreaming scheduler: clear orphan locks: %v", err)
		} else if n > 0 {
			d.logf("dreaming scheduler: cleared %d orphan dream lock(s)", n)
		}
	}

	interval := d.dreamSchedulerInterval
	if interval <= 0 {
		interval = defaultDreamSchedulerInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			d.dreamSchedulerTick(time.Now())
		}
	}
}

// dreamSchedulerTick runs at most one harvest per tick, when the schedule says a
// run is due. It is the unit-testable core of the scheduler: tests drive it with
// synthetic times instead of waiting on a real ticker.
//
// Anchoring: the very FIRST observation with no persisted anchor records "now" and
// returns, so enabling dreaming never fires an immediate harvest on daemon startup
// — the first real run lands at the next scheduled slot. After that a run is due
// once schedule.Next(anchor) has passed; the run advances the anchor to its
// completion time.
//
// Catch-up semantics: several scheduled slots missed while the daemon was down (a
// sleeping laptop) collapse into a SINGLE catch-up run on the next tick — not one
// run per missed slot — because the anchor jumps forward to the run time. Two
// nuances follow from anchoring on wall-clock time, both deliberate and benign for
// this harvest-only phase (it writes idempotent machine state, no LLM): a wake
// shortly BEFORE the day's slot runs the catch-up and then that slot when it
// arrives; and re-enabling after a long disable performs one catch-up run (the
// persisted anchor is stale), unlike a first-ever enable. The promote phase will
// revisit whether either warrants suppression before attaching an LLM pass.
func (d *Daemon) dreamSchedulerTick(now time.Time) {
	if !d.dreamingEnabled() {
		return
	}
	sched, raw, err := d.dreamingSchedule()
	if err != nil {
		d.logf("dreaming scheduler: invalid frequency %q: %v", raw, err)
		return
	}
	root, err := d.notebookRoot()
	if err != nil {
		d.logf("dreaming scheduler: resolve root: %v", err)
		return
	}
	state, err := notebook.LoadDreamRunState(root)
	if err != nil {
		d.logf("dreaming scheduler: load state: %v", err)
		return
	}

	anchor, ok := parseDreamTime(state.ScheduledFrom)
	if !ok {
		// First enable (or a corrupt anchor): anchor at now so the first run lands
		// at the next scheduled slot instead of immediately.
		state.ScheduledFrom = now.UTC().Format(time.RFC3339)
		if err := notebook.SaveDreamRunState(root, state); err != nil {
			d.logf("dreaming scheduler: anchor schedule: %v", err)
		}
		return
	}

	loc := d.dreamingLocation()
	next := sched.Next(anchor.In(loc))
	if next.IsZero() {
		// An unsatisfiable schedule has no next occurrence. Validation rejects these,
		// but a value persisted by an older daemon could slip through — treat it as
		// never-due rather than always-due (which would re-harvest every tick).
		d.logf("dreaming scheduler: frequency %q never occurs; skipping", raw)
		return
	}
	if next.After(now) {
		return // not due yet
	}
	if _, err := d.runDreamHarvest(now); err != nil && !errors.Is(err, errDreamRunning) {
		d.logf("dreaming scheduler: run: %v", err)
	}
}

// runDreamHarvest performs one scheduled harvest: under a single-writer guard and
// filesystem lock, it merges a fresh harvest into the persisted candidate set,
// saves it, and advances the schedule anchor. This phase writes only machine state
// under .attn/dreams/ — no durable memory and no LLM (that is the promote phase).
func (d *Daemon) runDreamHarvest(now time.Time) (*notebook.DreamRunState, error) {
	d.dreamMu.Lock()
	if d.dreamRunning {
		d.dreamMu.Unlock()
		return nil, errDreamRunning
	}
	d.dreamRunning = true
	d.dreamMu.Unlock()
	defer func() {
		d.dreamMu.Lock()
		d.dreamRunning = false
		d.dreamMu.Unlock()
	}()

	root, err := d.notebookRoot()
	if err != nil {
		return nil, err
	}
	release, err := notebook.AcquireDreamLock(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()

	set, persisted, err := d.dreamHarvestUnion()
	if err != nil {
		return nil, err
	}
	candidates := set.Candidates()
	if err := notebook.SaveDreamCandidates(root, candidates); err != nil {
		return nil, err
	}
	state := notebook.DreamRunState{
		ScheduledFrom:         now.UTC().Format(time.RFC3339),
		LastRunAt:             now.UTC().Format(time.RFC3339),
		LastRunNewCandidates:  len(candidates) - persisted,
		LastRunCandidateCount: len(candidates),
	}
	if err := notebook.SaveDreamRunState(root, state); err != nil {
		return nil, err
	}
	d.logf("dreaming: harvested %d candidates (%d new) into %s", len(candidates), len(candidates)-persisted, root)
	return &state, nil
}
