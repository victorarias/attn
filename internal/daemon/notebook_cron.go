package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/notebook"
)

// Notebook cron: a single per-minute, timezone-aware tick that decides what is
// due and enqueues onto the durable queue (internal/jobs), which owns execution.
// The tick itself is a cron entry on that same queue, so its next fire is
// durable and visible rather than living in a goroutine.

const (
	// notebookCronKind is the queue kind for the per-minute notebook tick.
	notebookCronKind = "notebook_cron"

	// defaultNotebookCronFrequency is the default nightly slot (03:00 in the
	// configured timezone — quiet hours).
	defaultNotebookCronFrequency = "0 3 * * *"

	// defaultNotebookCronInterval is how often the cron checks whether work is due.
	defaultNotebookCronInterval = time.Minute

	// notebookCronTickTimeout is a tripwire, not a budget: the tick is
	// sub-millisecond work, so a tick still running here is wedged, and killing it
	// frees the slot for the next minute instead of stalling the kind forever.
	notebookCronTickTimeout = 30 * time.Second
)

// legacyNotebookDreaming*Key exist ONLY for migrateNotebookCronSettingKeys to
// copy forward / reap. Never read them anywhere else.
const (
	legacyNotebookDreamingFrequencyKey = "notebook.dreaming.frequency"
	legacyNotebookDreamingTimezoneKey  = "notebook.dreaming.timezone"
	legacyNotebookDreamingEnabledKey   = "notebook.dreaming.enabled"
)

// migrateNotebookCronSettingKeys renames the persisted notebook.dreaming.*
// schedule settings to notebook.cron.* and reaps the orphaned enabled gate. It
// runs at every daemon start, so it MUST stay idempotent. A plain settings-value
// copy, NOT a schema migration.
func (d *Daemon) migrateNotebookCronSettingKeys() {
	if d.store == nil {
		return
	}
	d.renameSettingKey(legacyNotebookDreamingFrequencyKey, SettingNotebookCronFrequency)
	d.renameSettingKey(legacyNotebookDreamingTimezoneKey, SettingNotebookCronTimezone)
	// The enabled gate has no cron equivalent; DeleteSetting is a no-op when absent.
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

// notebookCronHandler is the queue handler for the per-minute notebook tick. It
// never returns an error: a tick has nothing worth retrying — the next one is a
// minute away and re-decides from the persisted anchor.
func (d *Daemon) notebookCronHandler(_ context.Context, _ *jobs.Job) (any, error) {
	d.notebookCronTick(time.Now())
	return nil, nil
}

// notebookCronTick is the per-tick fan-out of the single notebook cron.
func (d *Daemon) notebookCronTick(now time.Time) {
	d.enqueueDueDailyNarrates(now)
}

// enqueueDueDailyNarrates fires the daily per-workspace narrate backstop for
// long-lived workspaces that saw activity since the last fire; idle workspaces
// never burn a strong-tier pass. Anchor-FIRST ordering: when due, it advances
// the persisted anchor on ONE write BEFORE draining/enqueueing, so a rare
// enqueue failure skips one idempotent day rather than re-firing every tick,
// and missed slots collapse into a single catch-up. A first observation with no
// anchor records "now" and returns, so enabling never fires at startup.
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

	// Resolve the runner BEFORE touching state: a missing/disabled runner must not
	// advance the anchor and silently skip the day.
	runner := d.jobQueueRef()
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
		// First observation (or corrupt anchor): anchor at now, fire at the next slot.
		state.ScheduledFrom = now.UTC().Format(time.RFC3339)
		if err := notebook.SaveNarrateCronState(root, state); err != nil {
			d.logf("daily narrate: anchor schedule: %v", err)
		}
		return
	}

	loc := d.notebookCronLocation()
	next := sched.Next(anchor.In(loc))
	if next.IsZero() {
		// Unsatisfiable schedule (persisted by an older daemon): treat as never-due
		// rather than always-due, which would re-narrate every tick.
		d.logf("daily narrate: frequency %q never occurs; skipping", raw)
		return
	}
	if next.After(now) {
		return // not due yet
	}

	// Due: advance the anchor on ONE write, THEN drain and enqueue.
	state.ScheduledFrom = now.UTC().Format(time.RFC3339)
	if err := notebook.SaveNarrateCronState(root, state); err != nil {
		d.logf("daily narrate: advance anchor: %v", err)
	}

	for _, workspaceID := range d.drainNotebookNarrateActivity() {
		if d.store.GetWorkspace(workspaceID) == nil {
			// Removed since marked active: its final retrospective already ran.
			continue
		}
		d.enqueueDailyNarrateWorkspace(workspaceID)
	}
}

// drainNotebookNarrateActivity atomically snapshots and clears the daily-narrate
// activity set; clearing on drain is what makes a no-activity day enqueue nothing.
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
