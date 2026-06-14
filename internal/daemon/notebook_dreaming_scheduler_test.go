package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/notebook"
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

// A disabled scheduler tick is fully inert: it neither anchors the schedule nor
// harvests, so turning dreaming off leaves no machine state behind.
func TestDreamSchedulerTickDisabledIsInert(t *testing.T) {
	d := newNotebookDaemon(t)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact that would harvest if enabled.")

	d.dreamSchedulerTick(mustTime(t, "2026-06-14T12:00:00Z"))

	root := dreamRoot(t, d)
	state, _ := notebook.LoadDreamRunState(root)
	if state.ScheduledFrom != "" || state.LastRunAt != "" {
		t.Fatalf("disabled tick wrote run state: %+v", state)
	}
	if cands, _ := notebook.LoadDreamCandidates(root); len(cands) != 0 {
		t.Fatalf("disabled tick persisted %d candidates", len(cands))
	}
}

// The first tick after enabling anchors the schedule at "now" and does NOT run —
// so enabling dreaming never triggers an immediate harvest on daemon startup; the
// first real run lands at the next scheduled slot.
func TestDreamSchedulerTickFirstEnableAnchorsWithoutRunning(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")

	d.dreamSchedulerTick(mustTime(t, "2026-06-14T12:00:00Z"))

	root := dreamRoot(t, d)
	state, _ := notebook.LoadDreamRunState(root)
	if state.ScheduledFrom == "" {
		t.Fatal("first enable should anchor the schedule")
	}
	if state.LastRunAt != "" {
		t.Fatalf("first enable must not run; got LastRunAt=%q", state.LastRunAt)
	}
	if cands, _ := notebook.LoadDreamCandidates(root); len(cands) != 0 {
		t.Fatalf("first enable persisted %d candidates", len(cands))
	}
}

// An anchored schedule whose next slot is still in the future does not run, and
// leaves the anchor untouched.
func TestDreamSchedulerTickNotDue(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	root := dreamRoot(t, d)

	// Anchor at 04:00 UTC; the next "0 3 * * *" slot is the following day 03:00.
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-14T04:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	d.dreamSchedulerTick(mustTime(t, "2026-06-14T05:00:00Z"))

	state, _ := notebook.LoadDreamRunState(root)
	if state.LastRunAt != "" {
		t.Fatalf("not-due tick ran: %+v", state)
	}
	if state.ScheduledFrom != "2026-06-14T04:00:00Z" {
		t.Fatalf("not-due tick mutated the anchor: %q", state.ScheduledFrom)
	}
}

// A due schedule runs once, persisting the harvested candidates and advancing the
// anchor; a second tick the same day does NOT run again — proving a laptop that
// slept past several nightly slots catches up with exactly one run, not one per
// missed slot.
func TestDreamSchedulerTickDueRunsOnceWithCatchUp(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact worth consolidating.")
	root := dreamRoot(t, d)

	// Anchor several days back: many 03:00 slots were missed (laptop asleep).
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-10T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	now := mustTime(t, "2026-06-14T12:00:00Z")
	d.dreamSchedulerTick(now)

	state, _ := notebook.LoadDreamRunState(root)
	if state.LastRunAt == "" {
		t.Fatal("due tick should have run")
	}
	firstRun := state.LastRunAt
	cands, _ := notebook.LoadDreamCandidates(root)
	if len(cands) == 0 {
		t.Fatal("due run should persist harvested candidates")
	}
	if state.LastRunCandidateCount != len(cands) {
		t.Fatalf("run state count %d != persisted %d", state.LastRunCandidateCount, len(cands))
	}

	// One minute later the next slot is tomorrow's 03:00 → not due → no second run.
	d.dreamSchedulerTick(now.Add(time.Minute))
	state2, _ := notebook.LoadDreamRunState(root)
	if state2.LastRunAt != firstRun {
		t.Fatalf("catch-up fired twice: first=%q second=%q", firstRun, state2.LastRunAt)
	}
}

// runDreamHarvest merges a fresh harvest into the persisted set: a re-run picks up
// only genuinely new facts (idempotent on already-seen sources) and reports the
// new-candidate delta.
func TestRunDreamHarvestMergesPersisted(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "First durable fact about the harvest.")

	first, err := d.runDreamHarvest(mustTime(t, "2026-06-14T03:00:00Z"))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.LastRunNewCandidates == 0 || first.LastRunCandidateCount != first.LastRunNewCandidates {
		t.Fatalf("first run should be all-new: %+v", first)
	}

	// Add one genuinely new fact, then re-run: only the new one counts as new, and
	// the total grows by exactly that (the prior fact is not double-counted).
	appendDreamJournal(t, d, "2026-06-11", "Second durable fact, distinct from the first.")
	second, err := d.runDreamHarvest(mustTime(t, "2026-06-15T03:00:00Z"))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.LastRunCandidateCount != first.LastRunCandidateCount+1 {
		t.Fatalf("second run total = %d, want %d (one new fact merged)", second.LastRunCandidateCount, first.LastRunCandidateCount+1)
	}
	if second.LastRunNewCandidates != 1 {
		t.Fatalf("second run new = %d, want exactly 1", second.LastRunNewCandidates)
	}
}

// A run in progress (the in-memory single-flight guard) refuses a concurrent run.
func TestRunDreamHarvestSingleFlight(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")

	d.dreamMu.Lock()
	d.dreamRunning = true
	d.dreamMu.Unlock()

	if _, err := d.runDreamHarvest(mustTime(t, "2026-06-14T03:00:00Z")); !errors.Is(err, errDreamRunning) {
		t.Fatalf("expected errDreamRunning, got %v", err)
	}
}

// The filesystem lock blocks a run while held; clearing an orphaned lock (startup
// recovery) unblocks the next run.
func TestRunDreamHarvestFileLockAndRecovery(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	root := dreamRoot(t, d)

	// Simulate a crashed run that left its lock behind (never released).
	if _, err := notebook.AcquireDreamLock(root); err != nil {
		t.Fatalf("seed orphan lock: %v", err)
	}
	if _, err := d.runDreamHarvest(mustTime(t, "2026-06-14T03:00:00Z")); err == nil {
		t.Fatal("run should fail while the lock is held")
	}

	cleared, err := notebook.ClearOrphanDreamLocks(root)
	if err != nil || cleared != 1 {
		t.Fatalf("orphan recovery: cleared=%d err=%v", cleared, err)
	}
	if _, err := d.runDreamHarvest(mustTime(t, "2026-06-14T03:05:00Z")); err != nil {
		t.Fatalf("run should succeed after recovery: %v", err)
	}
}

// dreamStatus surfaces the schedule, timezone, last/next run, and persisted count
// once a run has happened.
func TestDreamStatusSurfacesSchedule(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact worth consolidating.")

	if _, err := d.runDreamHarvest(mustTime(t, "2026-06-14T03:00:00Z")); err != nil {
		t.Fatalf("run: %v", err)
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
	// The run anchored at 2026-06-14T03:00:00Z; the next "0 3 * * *" UTC slot is the
	// following day. Asserting the exact value pins that next_run is computed from
	// the persisted anchor (and timezone), not merely non-nil.
	if *res.NextRunAt != "2026-06-15T03:00:00Z" {
		t.Fatalf("next_run = %q, want 2026-06-15T03:00:00Z", *res.NextRunAt)
	}
	if res.PersistedCount == 0 {
		t.Fatal("expected persisted candidates after a run")
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
// "now" produce opposite verdicts under different zones. A scheduler that dropped
// the timezone conversion (computing everything in one fixed zone) would fail at
// least one direction of this table.
func TestDreamSchedulerTickTimezoneAffectsDueDecision(t *testing.T) {
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
			d.store.SetSetting(SettingNotebookDreamingEnabled, "true")
			d.store.SetSetting(SettingNotebookDreamingTimezone, tc.tz)
			appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
			root := dreamRoot(t, d)
			if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: tc.anchor}); err != nil {
				t.Fatalf("seed state: %v", err)
			}

			d.dreamSchedulerTick(mustTime(t, tc.now))

			state, _ := notebook.LoadDreamRunState(root)
			if ran := state.LastRunAt != ""; ran != tc.wantRun {
				t.Fatalf("tz=%s anchor=%s now=%s: ran=%v want=%v", tc.tz, tc.anchor, tc.now, ran, tc.wantRun)
			}
		})
	}
}

// A frequency that never occurs (validation normally rejects it, but an older
// daemon could have persisted one) must be treated as never-due, not always-due —
// otherwise every tick would re-harvest. Set the raw setting directly to bypass
// validation, then tick twice and assert nothing ran.
func TestDreamSchedulerUnsatisfiableFrequencyDoesNotRun(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d)
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	d.store.SetSetting(SettingNotebookDreamingFrequency, "0 0 30 2 *") // Feb 30 — never occurs
	root := dreamRoot(t, d)
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-10T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	d.dreamSchedulerTick(mustTime(t, "2026-06-14T12:00:00Z"))
	d.dreamSchedulerTick(mustTime(t, "2026-06-14T12:01:00Z"))

	if state, _ := notebook.LoadDreamRunState(root); state.LastRunAt != "" {
		t.Fatalf("an unsatisfiable frequency must never be due, but a run happened: %+v", state)
	}
}

// Documented catch-up nuance: when the daemon wakes shortly BEFORE the day's slot
// after missing several nights, the missed slots collapse into one catch-up run
// now, and the day's own slot then runs when it arrives. Two runs, by design — this
// pins that behavior so it cannot regress silently in either direction.
func TestDreamSchedulerCatchUpBeforeSlotAlsoRunsTheSlot(t *testing.T) {
	d := newNotebookDaemon(t)
	enableDreaming(t, d) // UTC
	appendDreamJournal(t, d, "2026-06-10", "A durable fact.")
	root := dreamRoot(t, d)
	if err := notebook.SaveDreamRunState(root, notebook.DreamRunState{ScheduledFrom: "2026-06-10T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	// 02:00, before today's 03:00 slot: one catch-up run for the missed nights.
	d.dreamSchedulerTick(mustTime(t, "2026-06-14T02:00:00Z"))
	s1, _ := notebook.LoadDreamRunState(root)
	if s1.LastRunAt == "" {
		t.Fatal("missed nights should trigger a catch-up run")
	}
	catchUp := s1.LastRunAt

	// 02:30, still before the slot: anchor advanced to 02:00 → not due again.
	d.dreamSchedulerTick(mustTime(t, "2026-06-14T02:30:00Z"))
	s2, _ := notebook.LoadDreamRunState(root)
	if s2.LastRunAt != catchUp {
		t.Fatalf("catch-up must not repeat before the slot: %q -> %q", catchUp, s2.LastRunAt)
	}

	// 03:00:30, today's scheduled slot arrives → the documented second run.
	d.dreamSchedulerTick(mustTime(t, "2026-06-14T03:00:30Z"))
	s3, _ := notebook.LoadDreamRunState(root)
	if s3.LastRunAt == catchUp {
		t.Fatal("today's scheduled slot should run after the pre-slot catch-up")
	}

	// 03:01:30, no further runs today.
	d.dreamSchedulerTick(mustTime(t, "2026-06-14T03:01:30Z"))
	s4, _ := notebook.LoadDreamRunState(root)
	if s4.LastRunAt != s3.LastRunAt {
		t.Fatalf("the slot run must not repeat: %q -> %q", s3.LastRunAt, s4.LastRunAt)
	}
}

// The scheduler goroutine actually ticks at its configured interval (exercising
// dreamSchedulerInterval) and returns promptly when its done channel closes.
func TestDreamSchedulerGoroutineRunsAndStops(t *testing.T) {
	d := newNotebookDaemon(t)
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
	go func() { d.startDreamingScheduler(done); close(stopped) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if state, _ := notebook.LoadDreamRunState(root); state.LastRunAt != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler goroutine did not run within timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(done)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler goroutine did not stop after done closed")
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
