package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/tasks"
)

// Dreaming cron enqueuer — phase B1 (deterministic, no LLM).
//
// This file makes the dreaming harvest RUN on a schedule and remember what it saw
// across daemon restarts. A timezone-aware cron decides, each tick, whether a
// nightly harvest is due; when it is, the enqueuer enqueues a single
// harvest_dream task onto the durable runner (internal/tasks). The runner owns
// execution, single-flight (its single worker), retry/backoff, and crash
// recovery (orphan-running reset on start) — so the enqueuer is a thin
// schedule-and-dispatch loop with no run machinery of its own.
//
// The harvest merges into the persisted candidate set under .attn/dreams/; the
// LLM promote pass that turns candidates into durable memory arrives in the
// follow-up PR. The cron tick granularity of a minute is ample for a daily
// janitor and keeps catch-up logic trivial.

// harvestDreamKind is the runner task kind for the nightly dream harvest. Its
// subject is the notebook root string (harvest_dream:<root>); the task store's
// sanitizeID turns the slashed root into a safe single filename component.
const harvestDreamKind = "harvest_dream"

const (
	// defaultDreamingFrequency runs the pass nightly at 03:00 in the configured
	// timezone — quiet hours, after a day's journals and dispatches have landed.
	defaultDreamingFrequency = "0 3 * * *"

	// defaultDreamSchedulerInterval is how often the enqueuer checks whether a
	// harvest is due. A daily janitor does not need finer granularity.
	defaultDreamSchedulerInterval = time.Minute
)

// harvestDreamExecutor merges a fresh harvest into the persisted candidate set
// and saves it. It runs on the durable runner (single worker = single-flight, so
// no bespoke guard or lock is needed) and is the SOLE writer of candidates.json;
// it deliberately does NOT touch DreamRunState (the cron enqueuer owns that, so
// state.json has exactly one writer). This phase writes only machine state under
// .attn/dreams/ — no durable memory and no LLM (that is the promote phase).
//
// No CommitGuard: SaveDreamCandidates is an atomic temp+rename, ctx-free write,
// so a Cancel/timeout cannot tear it (the same property the janitor's ctx-free
// commit relies on). The ctx is therefore unused.
func (d *Daemon) harvestDreamExecutor(_ context.Context, _ *tasks.Task) error {
	root, err := d.notebookRoot()
	if err != nil {
		return fmt.Errorf("dreaming harvest: resolve root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("dreaming harvest: empty notebook root")
	}
	set, persisted, err := d.dreamHarvestUnion()
	if err != nil {
		return fmt.Errorf("dreaming harvest: union: %w", err)
	}
	candidates := set.Candidates()
	if err := notebook.SaveDreamCandidates(root, candidates); err != nil {
		return fmt.Errorf("dreaming harvest: save candidates: %w", err)
	}
	d.logf("dreaming: harvested %d candidates (%d new) into %s", len(candidates), len(candidates)-persisted, root)
	return nil
}

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

// startNotebookCronEnqueuer blocks running the notebook cron tick loop until done
// is closed. Intended to be launched in its own goroutine from Start, AFTER
// startCompactRunner so the harvest_dream executor is registered before the first
// tick can enqueue.
//
// Shutdown is by done alone (close(d.done) in Stop), like the daemon's other
// d.done-driven loops; there is no explicit join. The enqueuer holds no run
// machinery — it only dispatches onto the durable runner, which owns single-flight
// and crash recovery — so there is nothing to drain on stop.
func (d *Daemon) startNotebookCronEnqueuer(done <-chan struct{}) {
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
			d.notebookCronTick(time.Now())
		}
	}
}

// notebookCronTick is the per-tick fan-out of the single notebook cron. Today it
// enqueues only the due nightly dream harvest; the daily per-workspace narrate
// lands here in the follow-up PR.
func (d *Daemon) notebookCronTick(now time.Time) {
	d.enqueueDueDreamHarvest(now)
	// TODO(daily-narrate): also enqueue per-active-workspace narrate here.
}

// enqueueDueDreamHarvest decides, from the timezone-aware cron and the persisted
// anchor, whether a harvest is due now; when it is, it advances the anchor and
// records the dispatch time on a SINGLE state write, then enqueues the
// harvest_dream task. The runner owns the actual harvest (execution, single-flight,
// retry, crash recovery).
//
// Anchoring: the very FIRST observation with no persisted anchor records "now" and
// returns, so enabling dreaming never fires an immediate harvest on daemon startup
// — the first real dispatch lands at the next scheduled slot. After that a fire is
// due once schedule.Next(anchor) has passed; the dispatch advances the anchor to
// "now".
//
// Catch-up semantics: several scheduled slots missed while the daemon was down (a
// sleeping laptop) collapse into a SINGLE catch-up enqueue on the next tick — not
// one per missed slot — because the anchor jumps forward to the dispatch time. Two
// nuances follow from anchoring on wall-clock time, both deliberate and benign for
// this harvest-only phase (it writes idempotent machine state, no LLM): a wake
// shortly BEFORE the day's slot enqueues the catch-up and then that slot when it
// arrives; and re-enabling after a long disable performs one catch-up enqueue (the
// persisted anchor is stale), unlike a first-ever enable. The promote phase will
// revisit whether either warrants suppression before attaching an LLM pass.
func (d *Daemon) enqueueDueDreamHarvest(now time.Time) {
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

	// Resolve the runner BEFORE touching state, so a missing/disabled runner never
	// advances the anchor (which would silently skip the day with no work done).
	runner := d.compactRunnerRef()
	if runner == nil || runner.Disabled() {
		return
	}

	state, err := notebook.LoadDreamRunState(root)
	if err != nil {
		d.logf("dreaming scheduler: load state: %v", err)
		return
	}

	anchor, ok := parseDreamTime(state.ScheduledFrom)
	if !ok {
		// First enable (or a corrupt anchor): anchor at now so the first dispatch
		// lands at the next scheduled slot instead of immediately.
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

	// Due: anchor-FIRST ordering. Advance the anchor and record the dispatch time on
	// ONE state write, THEN enqueue. If the rare Enqueue fails, the advanced anchor
	// skips one day rather than re-firing every tick; the idempotent harvest union
	// makes a skipped day benign (tomorrow's run picks up everything).
	state.ScheduledFrom = now.UTC().Format(time.RFC3339)
	state.LastRunAt = now.UTC().Format(time.RFC3339)
	if err := notebook.SaveDreamRunState(root, state); err != nil {
		d.logf("dreaming scheduler: advance anchor: %v", err)
	}
	if _, err := runner.Enqueue(harvestDreamKind, root, tasks.EnqueueOptions{ZeroDebounce: true}); err != nil {
		d.logf("dreaming: enqueue harvest: %v", err)
	}
}
