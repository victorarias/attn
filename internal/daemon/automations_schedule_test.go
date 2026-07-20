package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// scheduledDefinitionYAML builds a minimal valid scheduled-trigger definition,
// mirroring the fixture shape in automation_test.go's
// TestScheduledDefinitionValidation.
func scheduledDefinitionYAML(dir, cron, continuity, catchUp, prompt string) string {
	return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly
enabled: true
trigger:
  type: scheduled
  schedule: {cron: %q, time_zone: UTC}
prompt: %s
launch: {driver: codex}
location: {type: directory, path: %s}
policy: {continuity: %s, catch_up: %s}
`, cron, prompt, dir, continuity, catchUp)
}

// setupScheduledDaemon parses and persists one scheduled automation
// definition and returns a Daemon wired to an in-memory store, ready for
// observeDueSchedules.
func setupScheduledDaemon(t *testing.T, cron, continuity, catchUp string) (*Daemon, *store.Store, *store.AutomationDefinition, string) {
	t.Helper()
	dir := t.TempDir()
	spec, canonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, cron, continuity, catchUp, "Sweep.")))
	if err != nil {
		t.Fatalf("parse definition: %v", err)
	}
	s := store.New()
	def, err := s.UpsertAutomationDefinition(spec.ID, spec.Name, string(canonical), true, time.Now())
	if err != nil {
		t.Fatalf("upsert definition: %v", err)
	}
	d := &Daemon{store: s, wsHub: newWSHub()}
	return d, s, def, dir
}

func TestObserveDueSchedulesFirstObservationAnchorsCursor(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "*/5 * * * *", "fresh", "latest")
	delivered := 0
	d.automationDeliveryHook = func(*store.AutomationRun) error { delivered++; return nil }

	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now)

	if delivered != 0 {
		t.Fatalf("first observation delivered %d runs, want 0", delivered)
	}
	cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(now) {
		t.Fatalf("cursor=%v ok=%v err=%v, want anchored at %v", cursor, ok, err, now)
	}
}

func TestObserveDueSchedulesFiresWithinGraceForBothCatchUpPolicies(t *testing.T) {
	for _, catchUp := range []string{"skip", "latest"} {
		t.Run(catchUp, func(t *testing.T) {
			d, s, _, _ := setupScheduledDaemon(t, "* * * * *", "fresh", catchUp)
			var delivered []*store.AutomationRun
			d.automationDeliveryHook = func(run *store.AutomationRun) error {
				delivered = append(delivered, run)
				return s.MarkAutomationRunDelivered(run.ID, "{}", time.Now())
			}

			now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
			d.observeDueSchedules(now0) // anchor
			due := now0.Add(60 * time.Second)
			d.observeDueSchedules(now0.Add(70 * time.Second)) // 10s past due, well within grace

			if len(delivered) != 1 {
				t.Fatalf("delivered=%d, want 1", len(delivered))
			}
			occurrence, err := s.GetAutomationOccurrence(delivered[0].OccurrenceID)
			if err != nil || occurrence == nil {
				t.Fatalf("occurrence missing: %v", err)
			}
			wantKey := automation.ScheduledOccurrenceKey(due)
			if occurrence.OccurrenceKey != wantKey {
				t.Fatalf("occurrence key=%s want=%s", occurrence.OccurrenceKey, wantKey)
			}
			input, err := automation.ParseScheduledInput(json.RawMessage(occurrence.PayloadJSON))
			if err != nil {
				t.Fatalf("payload round-trip: %v", err)
			}
			if input.IntendedAt != due.UTC().Format(time.RFC3339) {
				t.Fatalf("intended_at=%s want=%s", input.IntendedAt, due.UTC().Format(time.RFC3339))
			}
		})
	}
}

// TestObserveDueSchedulesSkipGraceBoundary is the regression check for Fix
// 4a: catch_up=skip's grace window is inclusive of exactly scheduleSkipGrace
// but excludes anything past it, matching observeDueSchedule's `<=` compare.
// The cursor still advances in both cases either way.
func TestObserveDueSchedulesSkipGraceBoundary(t *testing.T) {
	for name, tc := range map[string]struct {
		offset   time.Duration
		delivers bool
	}{
		"exactly at grace fires":      {scheduleSkipGrace, true},
		"one second past grace skips": {scheduleSkipGrace + time.Second, false},
	} {
		t.Run(name, func(t *testing.T) {
			d, s, def, _ := setupScheduledDaemon(t, "0 * * * *", "fresh", "skip")
			var delivered []*store.AutomationRun
			d.automationDeliveryHook = func(run *store.AutomationRun) error {
				delivered = append(delivered, run)
				return s.MarkAutomationRunDelivered(run.ID, "{}", time.Now())
			}

			anchor := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
			d.observeDueSchedules(anchor)
			intended := time.Date(2026, 7, 20, 4, 0, 0, 0, time.UTC)
			observedAt := intended.Add(tc.offset)
			d.observeDueSchedules(observedAt)

			if tc.delivers {
				if len(delivered) != 1 {
					t.Fatalf("delivered=%d, want 1", len(delivered))
				}
				occurrence, err := s.GetAutomationOccurrence(delivered[0].OccurrenceID)
				if err != nil || occurrence == nil {
					t.Fatalf("occurrence missing: %v", err)
				}
				if wantKey := automation.ScheduledOccurrenceKey(intended); occurrence.OccurrenceKey != wantKey {
					t.Fatalf("occurrence key=%s want=%s", occurrence.OccurrenceKey, wantKey)
				}
			} else if len(delivered) != 0 {
				t.Fatalf("delivered=%d, want 0", len(delivered))
			}

			cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
			if err != nil || !ok || !cursor.Equal(observedAt) {
				t.Fatalf("cursor=%v ok=%v err=%v, want advanced to %v regardless of fire/skip", cursor, ok, err, observedAt)
			}
		})
	}
}

// TestObserveDueSchedulesSkipsObservationWhileRecovering is the regression
// check for Fix 4c: a brand-new scheduled definition observed while startup
// recovery hasn't settled prior state must not anchor a cursor; once
// recovery clears, the next call anchors normally.
func TestObserveDueSchedulesSkipsObservationWhileRecovering(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	d.setRecovering(true)

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)

	if _, ok, err := s.GetAutomationScheduleCursor(def.ID); err != nil || ok {
		t.Fatalf("recovering observation anchored a cursor: ok=%v err=%v", ok, err)
	}

	d.setRecovering(false)
	d.observeDueSchedules(now0)
	cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(now0) {
		t.Fatalf("cursor=%v ok=%v err=%v, want anchored at %v once recovery clears", cursor, ok, err, now0)
	}
}

func TestObserveDueSchedulesIdempotentAcrossRepeatedTicks(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	delivered := 0
	d.automationDeliveryHook = func(run *store.AutomationRun) error {
		delivered++
		return s.MarkAutomationRunDelivered(run.ID, "{}", time.Now())
	}

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)
	due := now0.Add(70 * time.Second)
	d.observeDueSchedules(due)
	d.observeDueSchedules(due)                      // same tick repeated
	d.observeDueSchedules(due.Add(5 * time.Second)) // a later tick still within the same due instant

	if delivered != 1 {
		t.Fatalf("delivered=%d, want 1", delivered)
	}
	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs=%d, want 1", len(runs))
	}
}

func TestObserveDueSchedulesDowntimeCatchUpPolicies(t *testing.T) {
	// Hourly schedule, two hours of missed instants, and the daemon only ticks
	// once it is back up: catch_up latest fires the newest missed instant;
	// catch_up skip fires nothing because that instant is well past the grace
	// window. Both still advance the cursor to now.
	for name, want := range map[string]struct {
		catchUp  string
		delivers bool
	}{
		"latest": {"latest", true},
		"skip":   {"skip", false},
	} {
		t.Run(name, func(t *testing.T) {
			d, s, def, _ := setupScheduledDaemon(t, "0 * * * *", "fresh", want.catchUp)
			var delivered []*store.AutomationRun
			d.automationDeliveryHook = func(run *store.AutomationRun) error {
				delivered = append(delivered, run)
				return s.MarkAutomationRunDelivered(run.ID, "{}", time.Now())
			}

			anchor := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
			d.observeDueSchedules(anchor)
			resumed := time.Date(2026, 7, 20, 5, 35, 0, 0, time.UTC) // two missed hourly ticks
			d.observeDueSchedules(resumed)

			if want.delivers {
				if len(delivered) != 1 {
					t.Fatalf("delivered=%d, want 1", len(delivered))
				}
				newestMissed := time.Date(2026, 7, 20, 5, 0, 0, 0, time.UTC)
				occurrence, err := s.GetAutomationOccurrence(delivered[0].OccurrenceID)
				if err != nil || occurrence == nil {
					t.Fatalf("occurrence missing: %v", err)
				}
				if wantKey := automation.ScheduledOccurrenceKey(newestMissed); occurrence.OccurrenceKey != wantKey {
					t.Fatalf("occurrence key=%s want=%s", occurrence.OccurrenceKey, wantKey)
				}
			} else if len(delivered) != 0 {
				t.Fatalf("delivered=%d, want 0", len(delivered))
			}

			cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
			if err != nil || !ok || !cursor.Equal(resumed) {
				t.Fatalf("cursor=%v ok=%v err=%v, want advanced to %v", cursor, ok, err, resumed)
			}
		})
	}
}

// TestObserveDueScheduleClaimRejectionLeavesCursorForRetry is the regression
// check for Fix 1: a claim rejected by ClaimScheduledAutomationRun's revision
// guard must not advance the cursor past the intended instant, or the next
// tick would never re-decide that occurrence and it would be dropped
// permanently. Simulates the observation race directly (definition edited
// between ListAutomationDefinitions' read and the claim) by calling
// observeDueSchedule with a stale `definition` copy while the store already
// holds a newer revision.
func TestObserveDueScheduleClaimRejectionLeavesCursorForRetry(t *testing.T) {
	d, s, def, dir := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	staleDefinition := *def // Revision predates the edit below.

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0) // anchor

	// Edit the definition (bumping its revision), racing an in-flight tick
	// that already read the pre-edit definition.
	editedSpec, editedCanonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, "* * * * *", "fresh", "latest", "Different sweep.")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(editedSpec.ID, editedSpec.Name, string(editedCanonical), true, now0); err != nil {
		t.Fatal(err)
	}

	delivered := 0
	d.automationDeliveryHook = func(*store.AutomationRun) error { delivered++; return nil }
	due := now0.Add(60 * time.Second)
	d.observeDueSchedule(staleDefinition, spec, now0.Add(70*time.Second))

	if delivered != 0 {
		t.Fatalf("delivered=%d, want 0 on claim rejection", delivered)
	}
	cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(now0) {
		t.Fatalf("cursor=%v ok=%v err=%v, want unchanged at %v after rejected claim", cursor, ok, err, now0)
	}
	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs=%d, want 0 after rejected claim", len(runs))
	}

	// A later tick re-reads the now-fresh definition and claims the same
	// intended instant successfully.
	freshDefinition, err := s.GetAutomationDefinition(def.ID)
	if err != nil || freshDefinition == nil {
		t.Fatalf("fresh definition: %v", err)
	}
	var freshSpec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(freshDefinition.SpecJSON), &freshSpec); err != nil {
		t.Fatal(err)
	}
	retryAt := now0.Add(75 * time.Second)
	d.observeDueSchedule(*freshDefinition, freshSpec, retryAt)

	if delivered != 1 {
		t.Fatalf("delivered=%d, want 1 after retry with fresh definition", delivered)
	}
	runs, err = s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs=%d, want 1", len(runs))
	}
	wantKey := automation.ScheduledOccurrenceKey(due)
	occurrence, err := s.GetAutomationOccurrence(runs[0].OccurrenceID)
	if err != nil || occurrence == nil || occurrence.OccurrenceKey != wantKey {
		t.Fatalf("occurrence=%#v err=%v, want key %s for the same intended instant", occurrence, err, wantKey)
	}
	cursor, ok, err = s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(retryAt) {
		t.Fatalf("cursor=%v ok=%v err=%v, want advanced to %v after the successful retry", cursor, ok, err, retryAt)
	}
}

// A claim rejected at 03:01:30 on a minutely schedule retried a full minute
// later must fire exactly one run for the NEWEST due instant (03:02), not the
// failed 03:01: the held-back cursor keeps the definition's appointment
// eligible, and the normal newest-due-wins rule — not a stale-instant replay —
// decides what fires on retry. This pins the production cadence where a new
// cron instant becomes due before the retry.
func TestObserveDueScheduleClaimRejectionRetryFiresNewestDueInstant(t *testing.T) {
	d, s, def, dir := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	staleDefinition := *def // Revision predates the edit below.

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0) // anchor

	editedSpec, editedCanonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, "* * * * *", "fresh", "latest", "Different sweep.")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(editedSpec.ID, editedSpec.Name, string(editedCanonical), true, now0); err != nil {
		t.Fatal(err)
	}

	delivered := 0
	d.automationDeliveryHook = func(*store.AutomationRun) error { delivered++; return nil }
	// 03:01:30 tick with the stale definition: intended 03:01 claim rejected.
	d.observeDueSchedule(staleDefinition, spec, now0.Add(90*time.Second))
	if delivered != 0 {
		t.Fatalf("delivered=%d, want 0 on claim rejection", delivered)
	}

	// 03:02:30 tick with the fresh definition: both 03:01 and 03:02 are due;
	// newest-due-wins fires 03:02 exactly once.
	freshDefinition, err := s.GetAutomationDefinition(def.ID)
	if err != nil || freshDefinition == nil {
		t.Fatalf("fresh definition: %v", err)
	}
	var freshSpec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(freshDefinition.SpecJSON), &freshSpec); err != nil {
		t.Fatal(err)
	}
	retryAt := now0.Add(150 * time.Second)
	d.observeDueSchedule(*freshDefinition, freshSpec, retryAt)

	if delivered != 1 {
		t.Fatalf("delivered=%d, want exactly 1 after the one-minute-later retry", delivered)
	}
	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs=%d, want 1: the failed instant must be superseded, not replayed alongside", len(runs))
	}
	wantKey := automation.ScheduledOccurrenceKey(now0.Add(120 * time.Second))
	occurrence, err := s.GetAutomationOccurrence(runs[0].OccurrenceID)
	if err != nil || occurrence == nil || occurrence.OccurrenceKey != wantKey {
		t.Fatalf("occurrence=%#v err=%v, want key %s for the newest due instant", occurrence, err, wantKey)
	}
	cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(retryAt) {
		t.Fatalf("cursor=%v ok=%v err=%v, want advanced to %v after the successful retry", cursor, ok, err, retryAt)
	}
}

func TestObserveDueSchedulesReplayStormGuardAdvancesCursorWithoutClaiming(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0) // anchor

	orig := scheduleDueInstantCap
	scheduleDueInstantCap = 5
	defer func() { scheduleDueInstantCap = orig }()

	delivered := 0
	d.automationDeliveryHook = func(*store.AutomationRun) error { delivered++; return nil }
	later := now0.Add(100 * time.Minute) // far more missed minutely instants than the cap
	d.observeDueSchedules(later)

	if delivered != 0 {
		t.Fatalf("replay storm guard delivered %d runs, want 0", delivered)
	}
	cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(later) {
		t.Fatalf("cursor=%v ok=%v err=%v, want jumped to %v", cursor, ok, err, later)
	}
}

// claimPendingScheduledRun claims a scheduled occurrence at the store layer
// without delivering it, modeling a daemon crash between the durable claim
// and the delivery attempt.
func claimPendingScheduledRun(t *testing.T, s *store.Store, def *store.AutomationDefinition, intended, observed time.Time) *store.AutomationRun {
	t.Helper()
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	effective, err := automation.Effective(spec, def.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(effective)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(automation.NewScheduledInput(intended, observed))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimScheduledAutomationRun(def.ID, automation.ScheduledOccurrenceKey(intended), "", def.Revision, string(payload), string(snapshotJSON), observed, newAutomationRunReservation())
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// TestScheduledPendingRunRecoversOnRestart verifies item C: recoverAutomations
// does not skip provider "schedule" pending runs the way it skips provider
// "github" ones (see TestAutomationRecoveryLeavesGitHubRunsForFreshProviderObservation).
// A real delivery attempt reaches deliverAutomationRun (which recoverAutomations
// calls directly, not through automationDeliveryHook, so a full spawn is out of
// reach for a unit test); disabling the definition after the claim gives a
// deterministic, PTY-free failure that still proves the attempt was made. That
// same real deliverAutomationRun -> failAutomationRun path is also where a
// scheduled run's delivered/failed transition broadcasts automations_changed,
// so this is extended to assert the broadcast fires for a scheduled run too
// (not just the manual/WS paths covered in automations_test.go).
func TestScheduledPendingRunRecoversOnRestart(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	intended := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	run := claimPendingScheduledRun(t, s, def, intended, intended.Add(time.Second))
	if _, err := s.UpsertAutomationDefinition(def.ID, def.Name, def.SpecJSON, false, intended.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var broadcastIDs []string
	d.automationsBroadcastHook = func(msg *protocol.AutomationsChangedMessage) {
		mu.Lock()
		broadcastIDs = append(broadcastIDs, msg.DefinitionIds...)
		mu.Unlock()
	}

	d.recoverAutomations()

	got, err := s.GetAutomationRun(run.ID)
	if err != nil || got == nil || got.State != "failed" || !strings.Contains(got.LastError, "definition is disabled") {
		t.Fatalf("recovery did not attempt scheduled run: run=%#v err=%v", got, err)
	}
	mu.Lock()
	ids := append([]string(nil), broadcastIDs...)
	mu.Unlock()
	found := false
	for _, id := range ids {
		if id == def.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("scheduled run's failed transition did not broadcast automations_changed: broadcasts=%v", ids)
	}
}

// TestScheduledPendingRunDeliversImmutableSnapshotAfterDefinitionEdit drives
// delivery through deliverObservedAutomationRun (the hookable entry point
// also used by the observation path) rather than recoverAutomations, so the
// test can inspect what would be delivered without a real PTY spawn.
func TestScheduledPendingRunDeliversImmutableSnapshotAfterDefinitionEdit(t *testing.T) {
	d, s, def, dir := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	intended := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	run := claimPendingScheduledRun(t, s, def, intended, intended.Add(time.Second))

	// Edit the definition after the claim: prompt changes and revision bumps.
	editedSpec, editedCanonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, "* * * * *", "fresh", "latest", "Different sweep.")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(editedSpec.ID, editedSpec.Name, string(editedCanonical), true, intended.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var capturedPrompt string
	d.automationDeliveryHook = func(r *store.AutomationRun) error {
		var snap automation.Snapshot
		if err := json.Unmarshal([]byte(r.SnapshotJSON), &snap); err != nil {
			return err
		}
		capturedPrompt = snap.Prompt
		return s.MarkAutomationRunDelivered(r.ID, "{}", time.Now())
	}
	if err := d.deliverObservedAutomationRun(run); err != nil {
		t.Fatal(err)
	}

	if capturedPrompt != "Sweep." {
		t.Fatalf("delivered prompt=%q, want original %q", capturedPrompt, "Sweep.")
	}
}

func TestObserveDueSchedulesSkipsDisabledDefinition(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	if _, err := s.UpsertAutomationDefinition(def.ID, def.Name, def.SpecJSON, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	delivered := 0
	d.automationDeliveryHook = func(*store.AutomationRun) error { delivered++; return nil }

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)
	d.observeDueSchedules(now0.Add(70 * time.Second))

	if delivered != 0 {
		t.Fatalf("disabled definition delivered %d runs, want 0", delivered)
	}
}

func TestObserveDueSchedulesFreshContinuityCreatesDistinctRuns(t *testing.T) {
	d, s, _, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	var delivered []*store.AutomationRun
	d.automationDeliveryHook = func(run *store.AutomationRun) error {
		delivered = append(delivered, run)
		return s.MarkAutomationRunDelivered(run.ID, "{}", time.Now())
	}

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)
	d.observeDueSchedules(now0.Add(70 * time.Second))
	d.observeDueSchedules(now0.Add(130 * time.Second))

	if len(delivered) != 2 {
		t.Fatalf("delivered=%d, want 2", len(delivered))
	}
	if delivered[0].TicketID == delivered[1].TicketID || delivered[0].SessionID == delivered[1].SessionID {
		t.Fatalf("fresh continuity reused reservation ids: %#v vs %#v", delivered[0], delivered[1])
	}
}

// TestScheduledSingletonContinuationSkipsPullRequestParsing is the regression
// check for item B of the brief: validateAutomationContinuation must not
// attempt to parse req.Context as a pull request for a schedule-provider
// continuation, even when it reuses a singleton-bound ticket/session against
// a live session. Binding reuse itself is covered at the store layer by
// TestScheduledAutomationSingletonContinuityReusesBinding.
func TestScheduledSingletonContinuationSkipsPullRequestParsing(t *testing.T) {
	s := store.New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{}`, true, now)
	if err != nil {
		t.Fatal(err)
	}
	intended1 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	payload1, err := json.Marshal(automation.NewScheduledInput(intended1, now))
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.ClaimScheduledAutomationRun(def.ID, automation.ScheduledOccurrenceKey(intended1), "singleton", def.Revision, string(payload1), `{}`, now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: first.TicketID, Title: "Nightly", Status: store.TicketStatusWorking, Assignee: first.SessionID, AutomationRunID: first.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}

	intended2 := intended1.Add(time.Minute)
	payload2, err := json.Marshal(automation.NewScheduledInput(intended2, now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.ClaimScheduledAutomationRun(def.ID, automation.ScheduledOccurrenceKey(intended2), "singleton", def.Revision, string(payload2), `{}`, now.Add(time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil {
		t.Fatal(err)
	}
	if second.TicketID != first.TicketID || second.SessionID != first.SessionID {
		t.Fatalf("singleton reservation not reused: first=%#v second=%#v", first, second)
	}

	d := &Daemon{store: s, ptyBackend: &fakeSpawnBackend{sessionIDs: []string{first.SessionID}}}
	req := automation.WorkRequest{
		RunID: second.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Provider: "schedule",
		Context: json.RawMessage(payload2),
		IDs:     automation.DeliveryIDs{TicketID: second.TicketID, SessionID: second.SessionID},
	}
	// Pre-fix, this would fail trying to parse a ScheduledInput payload as a
	// pull request. With the provider gate, it instead falls through to the
	// live-session short circuit and succeeds.
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("singleton continuation with a live session rejected: %v", err)
	}
}

// TestScheduledSingletonMissingContinuityTicketFailsBeforeReusingBoundArtifacts
// is the scheduled-provider mirror of
// TestMissingContinuityTicketFailsBeforeReusingBoundArtifacts in
// automations_test.go. Fix 2 makes ClaimScheduledAutomationRun record
// subject_key=continuityKey instead of always "": this is what lets
// HasPriorAutomationContinuityRun ever match a scheduled singleton's prior
// run, so EnsureTicket's deleted-ticket safety guard actually fires instead
// of silently recreating the ticket.
func TestScheduledSingletonMissingContinuityTicketFailsBeforeReusingBoundArtifacts(t *testing.T) {
	s := store.New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{}`, true, now)
	if err != nil {
		t.Fatal(err)
	}
	intended1 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	payload1, err := json.Marshal(automation.NewScheduledInput(intended1, now))
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.ClaimScheduledAutomationRun(def.ID, automation.ScheduledOccurrenceKey(intended1), "singleton", def.Revision, string(payload1), `{}`, now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: first.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: first.SessionID, AutomationRunID: first.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.SweepExpiredTickets(now.Add(2*time.Hour), time.Hour); err != nil || removed != 1 {
		t.Fatalf("sweep removed=%d err=%v", removed, err)
	}

	intended2 := intended1.Add(time.Minute)
	payload2, err := json.Marshal(automation.NewScheduledInput(intended2, now.Add(4*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.ClaimScheduledAutomationRun(def.ID, automation.ScheduledOccurrenceKey(intended2), "singleton", def.Revision, string(payload2), `{}`, now.Add(4*time.Hour), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil {
		t.Fatal(err)
	}

	d := &Daemon{store: s, wsHub: newWSHub()}
	err = d.EnsureTicket(context.Background(), automation.WorkRequest{RunID: second.ID, DefinitionID: def.ID, ContinuityKey: "singleton", IDs: automation.DeliveryIDs{TicketID: second.TicketID, SessionID: second.SessionID}})
	if err == nil || !strings.Contains(err.Error(), "continuity ticket is missing") {
		t.Fatalf("missing continuity ticket err=%v", err)
	}
	if ticket, err := s.GetTicket(first.TicketID); err != nil || ticket != nil {
		t.Fatalf("swept ticket was recreated: ticket=%#v err=%v", ticket, err)
	}
}

// TestObserveDueScheduleBroadcastsAtClaimTimeEvenWhenDeliveryFailsRetryably
// pins Fix F1: a claimed scheduled run must be visible to an open WS panel
// immediately, not only after delivery settles. A retryable delivery error
// leaves the run pending (recovery/next-tick retries it), so without a
// claim-time broadcast the panel would have no signal the run exists at all
// until some later transition.
func TestObserveDueScheduleBroadcastsAtClaimTimeEvenWhenDeliveryFailsRetryably(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	var broadcasts []string
	d.automationsBroadcastHook = func(msg *protocol.AutomationsChangedMessage) {
		broadcasts = append(broadcasts, msg.DefinitionIds...)
	}
	d.automationDeliveryHook = func(run *store.AutomationRun) error {
		return &retryableAutomationDeliveryError{cause: fmt.Errorf("session not ready yet")}
	}

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0) // anchor
	d.observeDueSchedules(now0.Add(70 * time.Second))

	if len(broadcasts) == 0 {
		t.Fatal("no automations_changed broadcast fired at claim time")
	}
	for _, id := range broadcasts {
		if id != def.ID {
			t.Fatalf("broadcast for unexpected definition %q, want %q", id, def.ID)
		}
	}
	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil || len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("runs=%#v err=%v, want exactly one pending run despite the retryable delivery failure", runs, err)
	}
}
