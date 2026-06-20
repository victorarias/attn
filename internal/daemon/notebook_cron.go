package daemon

import (
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/victorarias/attn/internal/notebook"
)

// Notebook cron enqueuer.
//
// A single per-minute, timezone-aware cron tick drives the notebook's scheduled
// background work. The tick is a thin schedule-and-dispatch loop: it decides what
// is due and enqueues onto the durable runner (internal/tasks), which owns
// execution, single-flight, retry/backoff, and crash recovery. Today it dispatches
// the daily per-workspace narrate backstop (enqueueDueDailyNarrates). The minute
// granularity is ample for a nightly pass and keeps catch-up logic trivial.

const (
	// defaultNotebookCronFrequency is the nightly slot (03:00 in the configured
	// timezone — quiet hours, after a day's journals and dispatches have landed)
	// the notebook cron fires on by default.
	defaultNotebookCronFrequency = "0 3 * * *"

	// defaultNotebookCronInterval is how often the cron checks whether work is
	// due. A daily pass does not need finer granularity.
	defaultNotebookCronInterval = time.Minute
)

// legacyNotebookDreaming*Key are the pre-rename persisted settings keys.
// frequency/timezone are retained ONLY so migrateNotebookCronSettingKeys can copy a
// user's configured schedule forward to the notebook.cron.* keys; the enabled gate
// has no cron successor (it died with the dreaming feature) and is only reaped.
// Never read any of these anywhere else.
const (
	legacyNotebookDreamingFrequencyKey = "notebook.dreaming.frequency"
	legacyNotebookDreamingTimezoneKey  = "notebook.dreaming.timezone"
	legacyNotebookDreamingEnabledKey   = "notebook.dreaming.enabled"
)

// migrateNotebookCronSettingKeys performs the one-time rename of the persisted
// notebook.dreaming.{frequency,timezone} settings to notebook.cron.* (the schedule
// outlived the removed dreaming feature) and reaps the orphaned
// notebook.dreaming.enabled gate (which has no successor). Each rename uses
// renameSettingKey for the idempotent copy-forward-then-reap contract; it runs at
// daemon start (and thus again after every app rebuild, since the daemon survives
// them), so it MUST stay idempotent. This is a plain settings-value copy, NOT a
// schema migration.
func (d *Daemon) migrateNotebookCronSettingKeys() {
	if d.store == nil {
		return
	}
	d.renameSettingKey(legacyNotebookDreamingFrequencyKey, SettingNotebookCronFrequency)
	d.renameSettingKey(legacyNotebookDreamingTimezoneKey, SettingNotebookCronTimezone)
	// The enabled gate has no cron equivalent — just drop any stale row so it stops
	// being broadcast in the settings map. DeleteSetting is a no-op when absent.
	d.store.DeleteSetting(legacyNotebookDreamingEnabledKey)
}

// notebookCronFrequency returns the configured cron frequency or the default.
func (d *Daemon) notebookCronFrequency() string {
	if d.store != nil {
		if f := strings.TrimSpace(d.store.GetSetting(SettingNotebookCronFrequency)); f != "" {
			return f
		}
	}
	return defaultNotebookCronFrequency
}

// notebookCronSchedule parses the configured frequency into a cron schedule, also
// returning the raw expression for display/logging.
func (d *Daemon) notebookCronSchedule() (cron.Schedule, string, error) {
	raw := d.notebookCronFrequency()
	sched, err := cron.ParseStandard(raw)
	if err != nil {
		return nil, raw, err
	}
	return sched, raw, nil
}

// notebookCronLocation returns the configured IANA timezone, falling back to the
// machine's local time when unset or unparseable (so a bad setting degrades to a
// sensible default rather than disabling the scheduler).
func (d *Daemon) notebookCronLocation() *time.Location {
	if d.store == nil {
		return time.Local
	}
	tz := strings.TrimSpace(d.store.GetSetting(SettingNotebookCronTimezone))
	if tz == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	d.logf("notebook cron: invalid timezone %q, using local time", tz)
	return time.Local
}

// parseNotebookCronTime parses a persisted RFC3339 timestamp.
func parseNotebookCronTime(s string) (time.Time, bool) {
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
// startCompactRunner so the narrate executor is registered before the first tick
// can enqueue.
//
// Shutdown is by done alone (close(d.done) in Stop), like the daemon's other
// d.done-driven loops; there is no explicit join. The enqueuer holds no run
// machinery — it only dispatches onto the durable runner, which owns single-flight
// and crash recovery — so there is nothing to drain on stop.
func (d *Daemon) startNotebookCronEnqueuer(done <-chan struct{}) {
	ticker := time.NewTicker(defaultNotebookCronInterval)
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

// notebookCronTick is the per-tick fan-out of the single notebook cron. It enqueues
// a per-active-workspace narrate for long-lived (never-removed) workspaces on the
// nightly slot.
func (d *Daemon) notebookCronTick(now time.Time) {
	d.enqueueDueDailyNarrates(now)
}

// enqueueDueDailyNarrates decides, from the timezone-aware cron and the persisted
// NarrateCronState anchor, whether the daily per-workspace narrate pass is due now;
// when it is, it advances the anchor on a SINGLE state write, then drains the
// activity set and enqueues a coalesced narrate_workspace for each STILL-LIVE
// workspace that saw activity since the last fire. This is the backstop for the
// never-removed long-lived workspace, which gets no session-end narrate on a day it
// had no session stop.
//
// The daily narrate fires on the shared notebook-maintenance nightly slot defined by
// the notebook cron schedule/timezone SETTINGS; a dedicated split can come later.
// Narration is always on (no enabled gate), so this fires on its own anchor; that is
// why it uses a SEPARATE state file (NarrateCronState).
//
// Activity gate: only workspaces that saw a session end or a content-changing context
// write since the last fire are in the set, so idle workspaces are skipped and never
// burn a strong-tier pass. A removed workspace in the set is skipped too — its
// removal-boundary final retrospective already ran. An empty set still advances the
// anchor (consumes the day's slot) and enqueues nothing.
//
// Anchoring + catch-up: a first observation with no anchor records "now" and returns
// (so enabling never fires immediately at startup); a fire advances the anchor to
// "now" on one write BEFORE draining, so a rare enqueue failure skips one idempotent
// day rather than re-firing every tick, and missed slots collapse into a single
// catch-up.
func (d *Daemon) enqueueDueDailyNarrates(now time.Time) {
	sched, raw, err := d.notebookCronSchedule()
	if err != nil {
		d.logf("daily narrate: invalid frequency %q: %v", raw, err)
		return
	}
	root, err := d.notebookRoot()
	if err != nil {
		d.logf("daily narrate: resolve root: %v", err)
		return
	}

	// Resolve the runner BEFORE touching state, so a missing/disabled runner never
	// advances the anchor (which would silently skip the day with no work done).
	runner := d.compactRunnerRef()
	if runner == nil || runner.Disabled() {
		return
	}

	state, err := notebook.LoadNarrateCronState(root)
	if err != nil {
		d.logf("daily narrate: load state: %v", err)
		return
	}

	anchor, ok := parseNotebookCronTime(state.ScheduledFrom)
	if !ok {
		// First observation (or a corrupt anchor): anchor at now so the first pass
		// lands at the next scheduled slot instead of immediately at startup.
		state.ScheduledFrom = now.UTC().Format(time.RFC3339)
		if err := notebook.SaveNarrateCronState(root, state); err != nil {
			d.logf("daily narrate: anchor schedule: %v", err)
		}
		return
	}

	loc := d.notebookCronLocation()
	next := sched.Next(anchor.In(loc))
	if next.IsZero() {
		// An unsatisfiable schedule has no next occurrence. Validation rejects these,
		// but a value persisted by an older daemon could slip through — treat it as
		// never-due rather than always-due (which would re-narrate every tick).
		d.logf("daily narrate: frequency %q never occurs; skipping", raw)
		return
	}
	if next.After(now) {
		return // not due yet
	}

	// Due: anchor-FIRST ordering. Advance the anchor on ONE state write, THEN drain
	// and enqueue. If a rare enqueue fails, the advanced anchor skips one day rather
	// than re-firing every tick; the daily narrate is idempotent (the next trigger
	// re-narrates), so a skipped day is benign.
	state.ScheduledFrom = now.UTC().Format(time.RFC3339)
	if err := notebook.SaveNarrateCronState(root, state); err != nil {
		d.logf("daily narrate: advance anchor: %v", err)
	}

	for _, workspaceID := range d.drainNotebookNarrateActivity() {
		if d.store.GetWorkspace(workspaceID) == nil {
			// Removed since it was marked active: its removal-boundary final
			// retrospective already ran. Skip it.
			continue
		}
		d.enqueueDailyNarrateWorkspace(workspaceID)
	}
}

// drainNotebookNarrateActivity atomically snapshots and clears the daily-narrate
// activity set (swapping in a fresh map under the mutex), returning the workspace ids
// that saw activity since the last fire. Clearing on drain is what makes a later
// no-activity day enqueue nothing.
func (d *Daemon) drainNotebookNarrateActivity() []string {
	d.notebookNarrateActivityMu.Lock()
	defer d.notebookNarrateActivityMu.Unlock()
	if len(d.notebookNarrateActivity) == 0 {
		return nil
	}
	ids := make([]string, 0, len(d.notebookNarrateActivity))
	for id := range d.notebookNarrateActivity {
		ids = append(ids, id)
	}
	d.notebookNarrateActivity = make(map[string]struct{})
	return ids
}
