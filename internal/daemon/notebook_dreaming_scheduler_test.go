package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/tasks"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return parsed
}

// enableDreaming turns the gate on and pins the timezone to UTC so schedule math
// in tests is independent of the machine's local time.
func enableDreaming(t *testing.T, d *Daemon) {
	t.Helper()
	d.store.SetSetting(SettingNotebookDreamingEnabled, "true")
	d.store.SetSetting(SettingNotebookDreamingTimezone, "UTC")
}

func dreamRoot(t *testing.T, d *Daemon) string {
	t.Helper()
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatalf("notebook root: %v", err)
	}
	return root
}

// installDreamHarvestRunner wires a started durable runner with the harvest_dream
// executor onto the daemon (the enqueuer's required dispatch target). The runner's
// OWN tasks dir is a throwaway temp dir; it deliberately does NOT touch
// SettingNotebookRoot, which newNotebookDaemon already set and the test's seeded
// journals/DreamRunState depend on (both the executor and the enqueuer resolve the
// notebook root via d.notebookRoot()). A fast poll interval avoids real-time waits.
func installDreamHarvestRunner(t *testing.T, d *Daemon) *tasks.Runner {
	t.Helper()
	runner := tasks.New(tasks.Options{
		Root:         filepath.Join(t.TempDir(), "tasks"),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
	})
	if err := runner.Register(harvestDreamKind, d.harvestDreamExecutor); err != nil {
		t.Fatalf("register harvest_dream: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.compactRunner = runner
	return runner
}

// dreamTaskEnqueued reports whether the harvest_dream:<root> task exists on the
// runner (i.e. the enqueuer dispatched it on a due fire).
func dreamTaskEnqueued(t *testing.T, runner *tasks.Runner, root string) bool {
	t.Helper()
	task, err := runner.Get(tasks.TaskID(harvestDreamKind, root))
	if err != nil {
		t.Fatalf("get harvest_dream task: %v", err)
	}
	return task != nil
}

// A disabled enqueuer tick is fully inert: it neither anchors the schedule nor
// enqueues, so turning dreaming off leaves no machine state and no task behind.
func TestEnqueueDueDreamHarvestDisabledIsInert(t *testing.T) {
	d := newNotebookDaemon(t)
	runner := installDreamHarvestRunner(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact that would harvest if enabled.")
	root := dreamRoot(t, d)

	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T12:00:00Z"))

	state, _ := notebook.LoadDreamRunState(root)
	if state.ScheduledFrom != "" || state.LastRunAt != "" {
		t.Fatalf("disabled tick wrote run state: %+v", state)
	}
	if dreamTaskEnqueued(t, runner, root) {
		t.Fatal("disabled tick enqueued a harvest_dream task")
	}
}

// The first tick after enabling anchors the schedule at "now" and does NOT
// enqueue — so enabling dreaming never triggers an immediate harvest on daemon
// startup; the first real dispatch lands at the next scheduled slot.
func TestEnqueueDueDreamHarvestFirstEnableAnchorsWithoutRunning(t *testing.T) {
	d := newNotebookDaemon(t)
	runner := installDreamHarvestRunner(t, d)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	root := dreamRoot(t, d)

	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T12:00:00Z"))

	state, _ := notebook.LoadDreamRunState(root)
	if state.ScheduledFrom == "" {
		t.Fatal("first enable should anchor the schedule")
	}
	if state.LastRunAt != "" {
		t.Fatalf("first enable must not dispatch; got LastRunAt=%q", state.LastRunAt)
	}
	if dreamTaskEnqueued(t, runner, root) {
		t.Fatal("first enable enqueued a harvest_dream task")
	}
}

// An anchored schedule whose next slot is still in the future does not enqueue,
// and leaves the anchor untouched.
func TestEnqueueDueDreamHarvestNotDue(t *testing.T) {
	d := newNotebookDaemon(t)
	runner := installDreamHarvestRunner(t, d)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	root := dreamRoot(t, d)

	// Anchor at 04:00 UTC; the next "0 3 * * *" slot is the following day 03:00.
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-14T04:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T05:00:00Z"))

	state, _ := notebook.LoadDreamRunState(root)
	if state.LastRunAt != "" {
		t.Fatalf("not-due tick dispatched: %+v", state)
	}
	if state.ScheduledFrom != "2026-06-14T04:00:00Z" {
		t.Fatalf("not-due tick mutated the anchor: %q", state.ScheduledFrom)
	}
	if dreamTaskEnqueued(t, runner, root) {
		t.Fatal("not-due tick enqueued a harvest_dream task")
	}
}

// A due schedule dispatches once, enqueuing the harvest_dream task and advancing
// the anchor; a second tick the same day does NOT enqueue again — proving a laptop
// that slept past several nightly slots catches up with exactly one dispatch, not
// one per missed slot.
func TestEnqueueDueDreamHarvestDueRunsOnceWithCatchUp(t *testing.T) {
	d := newNotebookDaemon(t)
	runner := installDreamHarvestRunner(t, d)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact worth consolidating.")
	root := dreamRoot(t, d)

	// Anchor several days back: many 03:00 slots were missed (laptop asleep).
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-10T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	now := mustTime(t, "2026-06-14T12:00:00Z")
	d.enqueueDueDreamHarvest(now)

	state, _ := notebook.LoadDreamRunState(root)
	if state.LastRunAt == "" {
		t.Fatal("due tick should have dispatched (LastRunAt set)")
	}
	firstRun := state.LastRunAt
	if !dreamTaskEnqueued(t, runner, root) {
		t.Fatal("due tick should enqueue the harvest_dream task")
	}

	// One minute later the next slot is tomorrow's 03:00 → not due → no second
	// dispatch (LastRunAt unchanged).
	d.enqueueDueDreamHarvest(now.Add(time.Minute))
	state2, _ := notebook.LoadDreamRunState(root)
	if state2.LastRunAt != firstRun {
		t.Fatalf("catch-up fired twice: first=%q second=%q", firstRun, state2.LastRunAt)
	}
}

// harvestDreamExecutor merges a fresh harvest into the persisted set: a re-run
// picks up only genuinely new facts (idempotent on already-seen sources). Running
// the executor twice with one new journal fact between runs grows the persisted
// candidate set by exactly that one fact.
func TestHarvestDreamExecutorMergesPersisted(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "First durable fact about the harvest.")
	root := dreamRoot(t, d)

	if err := d.harvestDreamExecutor(t.Context(), nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := notebook.LoadDreamCandidates(root)
	if err != nil {
		t.Fatalf("load after first run: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("first run should persist harvested candidates")
	}

	// Add one genuinely new fact, then re-run: only the new one is added, and the
	// total grows by exactly one (the prior fact is not double-counted).
	appendDreamJournal(t, d, "2026-06-11", "Second durable fact, distinct from the first.")
	if err := d.harvestDreamExecutor(t.Context(), nil); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := notebook.LoadDreamCandidates(root)
	if err != nil {
		t.Fatalf("load after second run: %v", err)
	}
	if len(second) != len(first)+1 {
		t.Fatalf("second run total = %d, want %d (one new fact merged)", len(second), len(first)+1)
	}
}

// dreamStatus surfaces the schedule, timezone, last/next run, and persisted count
// once a harvest has persisted candidates and the enqueuer has recorded a fire.
func TestDreamStatusSurfacesSchedule(t *testing.T) {
	d := newNotebookDaemon(t)
	installDreamHarvestRunner(t, d)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact worth consolidating.")
	root := dreamRoot(t, d)

	// Record a fire at the 03:00 anchor (the enqueuer's state write) and persist the
	// harvested candidates (the executor's write) — the two halves of one due fire.
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{
		ScheduledFrom: "2026-06-14T03:00:00Z",
		LastRunAt:     "2026-06-14T03:00:00Z",
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if err := d.harvestDreamExecutor(t.Context(), nil); err != nil {
		t.Fatalf("harvest: %v", err)
	}

	res, err := d.dreamStatus()
	if err != nil {
		t.Fatalf("dreamStatus: %v", err)
	}
	if res.Schedule == nil || *res.Schedule != defaultDreamingFrequency {
		t.Fatalf("schedule = %v, want %q", res.Schedule, defaultDreamingFrequency)
	}
	if res.Timezone == nil || *res.Timezone != "UTC" {
		t.Fatalf("timezone = %v, want UTC", res.Timezone)
	}
	if res.LastRunAt == nil || res.NextRunAt == nil {
		t.Fatalf("expected last/next run set: last=%v next=%v", res.LastRunAt, res.NextRunAt)
	}
	// The fire anchored at 2026-06-14T03:00:00Z; the next "0 3 * * *" UTC slot is the
	// following day. Asserting the exact value pins that next_run is computed from
	// the persisted anchor (and timezone), not merely non-nil.
	if *res.NextRunAt != "2026-06-15T03:00:00Z" {
		t.Fatalf("next_run = %q, want 2026-06-15T03:00:00Z", *res.NextRunAt)
	}
	if res.PersistedCount == 0 {
		t.Fatal("expected persisted candidates after a harvest")
	}
}

// Before any run/anchor, status still computes a next_run (from "now") rather than
// panicking or leaving it unset — covering fillDreamSchedule's never-anchored path.
func TestDreamStatusNextRunBeforeFirstRun(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")

	res, err := d.dreamStatus()
	if err != nil {
		t.Fatalf("dreamStatus: %v", err)
	}
	if res.LastRunAt != nil {
		t.Fatalf("no run has happened, last_run should be unset: %v", res.LastRunAt)
	}
	if res.NextRunAt == nil {
		t.Fatal("next_run should be computed even before the first run")
	}
	next, err := time.Parse(time.RFC3339, *res.NextRunAt)
	if err != nil {
		t.Fatalf("next_run not a timestamp: %q", *res.NextRunAt)
	}
	if !next.After(time.Now()) {
		t.Fatalf("next_run %s should be in the future", next)
	}
}

// The configured timezone genuinely flips the due decision: the same anchor and
// "now" produce opposite verdicts under different zones. An enqueuer that dropped
// the timezone conversion (computing everything in one fixed zone) would fail at
// least one direction of this table.
func TestEnqueueDueDreamHarvestTimezoneAffectsDueDecision(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tz      string
		anchor  string
		now     string
		wantRun bool
	}{
		// 04:00Z = 00:00 EDT; next 03:00 EDT slot is 07:00Z, before now 08:30Z → due
		// in New York, but the next 03:00 UTC slot is the following day → not due.
		{"NewYork due", "America/New_York", "2026-06-14T04:00:00Z", "2026-06-14T08:30:00Z", true},
		{"UTC not due (same instants)", "UTC", "2026-06-14T04:00:00Z", "2026-06-14T08:30:00Z", false},
		// Mirror: 03:00 UTC slot is at 03:00Z ≤ now 04:00Z → due in UTC, but the next
		// 03:00 EDT slot is 07:00Z → not due in New York.
		{"UTC due", "UTC", "2026-06-14T01:00:00Z", "2026-06-14T04:00:00Z", true},
		{"NewYork not due (same instants)", "America/New_York", "2026-06-14T01:00:00Z", "2026-06-14T04:00:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newNotebookDaemon(t)
			runner := installDreamHarvestRunner(t, d)
			d.store.SetSetting(SettingNotebookDreamingEnabled, "true")
			d.store.SetSetting(SettingNotebookDreamingTimezone, tc.tz)
			appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
			root := dreamRoot(t, d)
			if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: tc.anchor}); err != nil {
				t.Fatalf("seed state: %v", err)
			}

			d.enqueueDueDreamHarvest(mustTime(t, tc.now))

			state, _ := notebook.LoadDreamRunState(root)
			ran := state.LastRunAt != ""
			if ran != tc.wantRun {
				t.Fatalf("tz=%s anchor=%s now=%s: ran=%v want=%v", tc.tz, tc.anchor, tc.now, ran, tc.wantRun)
			}
			if got := dreamTaskEnqueued(t, runner, root); got != tc.wantRun {
				t.Fatalf("tz=%s: task enqueued=%v want=%v", tc.tz, got, tc.wantRun)
			}
		})
	}
}

// A frequency that never occurs (validation normally rejects it, but an older
// daemon could have persisted one) must be treated as never-due, not always-due —
// otherwise every tick would re-enqueue. Set the raw setting directly to bypass
// validation, then tick twice and assert nothing fired.
func TestEnqueueDueDreamHarvestUnsatisfiableFrequencyDoesNotRun(t *testing.T) {
	d := newNotebookDaemon(t)
	runner := installDreamHarvestRunner(t, d)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	d.store.SetSetting(SettingNotebookDreamingFrequency, "0 0 30 2 *") // Feb 30 — never occurs
	root := dreamRoot(t, d)
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-10T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T12:00:00Z"))
	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T12:01:00Z"))

	if state, _ := notebook.LoadDreamRunState(root); state.LastRunAt != "" {
		t.Fatalf("an unsatisfiable frequency must never be due, but a fire happened: %+v", state)
	}
	if dreamTaskEnqueued(t, runner, root) {
		t.Fatal("an unsatisfiable frequency must never enqueue a harvest_dream task")
	}
}

// Documented catch-up nuance: when the daemon wakes shortly BEFORE the day's slot
// after missing several nights, the missed slots collapse into one catch-up fire
// now, and the day's own slot then fires when it arrives. Two fires, by design —
// this pins that behavior so it cannot regress silently in either direction.
func TestEnqueueDueDreamHarvestCatchUpBeforeSlotAlsoRunsTheSlot(t *testing.T) {
	d := newNotebookDaemon(t)
	installDreamHarvestRunner(t, d)
	enableDreaming(t, d) // UTC
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	root := dreamRoot(t, d)
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-10T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	// 02:00, before today's 03:00 slot: one catch-up fire for the missed nights.
	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T02:00:00Z"))
	s1, _ := notebook.LoadDreamRunState(root)
	if s1.LastRunAt == "" {
		t.Fatal("missed nights should trigger a catch-up fire")
	}
	catchUp := s1.LastRunAt

	// 02:30, still before the slot: anchor advanced to 02:00 → not due again.
	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T02:30:00Z"))
	s2, _ := notebook.LoadDreamRunState(root)
	if s2.LastRunAt != catchUp {
		t.Fatalf("catch-up must not repeat before the slot: %q -> %q", catchUp, s2.LastRunAt)
	}

	// 03:00:30, today's scheduled slot arrives → the documented second fire.
	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T03:00:30Z"))
	s3, _ := notebook.LoadDreamRunState(root)
	if s3.LastRunAt == catchUp {
		t.Fatal("today's scheduled slot should fire after the pre-slot catch-up")
	}

	// 03:01:30, no further fires today.
	d.enqueueDueDreamHarvest(mustTime(t, "2026-06-14T03:01:30Z"))
	s4, _ := notebook.LoadDreamRunState(root)
	if s4.LastRunAt != s3.LastRunAt {
		t.Fatalf("the slot fire must not repeat: %q -> %q", s3.LastRunAt, s4.LastRunAt)
	}
}

// The cron enqueuer goroutine actually ticks at its configured interval
// (exercising dreamSchedulerInterval) and returns promptly when its done channel
// closes; a due tick records a fire (LastRunAt set).
func TestNotebookCronEnqueuerGoroutineRunsAndStops(t *testing.T) {
	d := newNotebookDaemon(t)
	installDreamHarvestRunner(t, d)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	root := dreamRoot(t, d)
	// Anchor in the past relative to the real clock so the first tick is due
	// regardless of when the test runs.
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{
		ScheduledFrom: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	d.dreamSchedulerInterval = 5 * time.Millisecond

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() { d.startNotebookCronEnqueuer(done); close(stopped) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if state, _ := notebook.LoadDreamRunState(root); state.LastRunAt != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("enqueuer goroutine did not fire within timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(done)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueuer goroutine did not stop after done closed")
	}
}

func TestDreamingLocation(t *testing.T) {
	d := newNotebookDaemon(t)
	if got := d.dreamingLocation(); got != time.Local {
		t.Fatalf("default location = %v, want local", got)
	}
	d.store.SetSetting(SettingNotebookDreamingTimezone, "America/New_York")
	if got := d.dreamingLocation(); got.String() != "America/New_York" {
		t.Fatalf("configured location = %v, want America/New_York", got)
	}
	d.store.SetSetting(SettingNotebookDreamingTimezone, "Not/ARealZone")
	if got := d.dreamingLocation(); got != time.Local {
		t.Fatalf("invalid location should fall back to local, got %v", got)
	}
}

func TestValidateDreamingSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty frequency ok", "", false},
		{"valid cron", "0 3 * * *", false},
		{"valid every-minute cron", "* * * * *", false},
		{"descriptor ok", "@daily", false},
		{"garbage cron", "not a cron", true},
		{"too few fields", "0 3 * *", true},
		{"impossible date (Feb 30) rejected", "0 0 30 2 *", true},
		{"embedded CRON_TZ rejected", "CRON_TZ=Asia/Tokyo 0 3 * * *", true},
		{"embedded TZ rejected", "TZ=Asia/Tokyo 0 3 * * *", true},
	} {
		t.Run("frequency/"+tc.name, func(t *testing.T) {
			if err := validateNotebookDreamingFrequency(tc.value); (err != nil) != tc.wantErr {
				t.Fatalf("validate %q err=%v wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty tz ok", "", false},
		{"valid IANA", "America/New_York", false},
		{"utc", "UTC", false},
		{"garbage tz", "Not/ARealZone", true},
	} {
		t.Run("timezone/"+tc.name, func(t *testing.T) {
			if err := validateNotebookDreamingTimezone(tc.value); (err != nil) != tc.wantErr {
				t.Fatalf("validate %q err=%v wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
}
