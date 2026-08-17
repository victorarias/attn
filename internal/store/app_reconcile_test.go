package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedReconcileApp(t *testing.T, s *Store, now time.Time) AppVersion {
	t.Helper()
	v, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:first",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle.js",
	}, now)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if err := s.SaveBusConsumer(BusConsumer{
		Name: "app:approval-gate", Filter: "ticket.*", Enabled: true,
	}, now); err != nil {
		t.Fatalf("seed consumer: %v", err)
	}
	return v
}

func TestAppReconcileTriggersCoalesceAtOneClaimBoundary(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	first := seedReconcileApp(t, s, now)
	if _, err := s.AppendBusEvent(BusEvent{Name: "ticket.created"}, now); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := s.RequestAppReconcileGap("approval-gate", 0, 3, now)
	if err != nil || !inserted {
		t.Fatalf("gap insert = %t, %v", inserted, err)
	}
	inserted, err = s.RequestAppReconcileGap("approval-gate", 0, 3, now)
	if err != nil || inserted {
		t.Fatalf("duplicate gap insert = %t, %v", inserted, err)
	}

	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Requests) != 2 {
		t.Fatalf("pending = %+v, want version and one gap", claim.Requests)
	}
	if claim.Requests[0].Reason != AppReconcileVersionChange || claim.Requests[1].Reason != AppReconcileGap {
		t.Fatalf("reason order = %+v", claim.Requests)
	}
	if claim.Requests[0].PreviousVersionID != first.ID || claim.Requests[0].VersionID != second.ID {
		t.Fatalf("version request = %+v", claim.Requests[0])
	}
	if claim.ThroughSeq != 2 || claim.ThroughRequestID != claim.Requests[1].ID {
		t.Fatalf("claim boundary = %+v", claim)
	}
}

func TestAppReconcileTriggerDuringRunRemainsOwedAndCompletionFencesCursor(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedReconcileApp(t, s, now)
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	first, err := s.AppReconcilePending("approval-gate")
	if err != nil || len(first.Requests) != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	if _, err := s.AppendBusEvent(BusEvent{Name: "ticket.updated"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestAppReconcileGap("approval-gate", 0, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBusConsumerCursor("app:approval-gate", 9, now); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAppReconcile("approval-gate", first.ThroughRequestID, first.ThroughSeq, now); err != nil {
		t.Fatal(err)
	}
	consumer, _, err := s.GetBusConsumer("app:approval-gate")
	if err != nil || consumer.Cursor != 9 {
		t.Fatalf("completion rewound cursor: %+v, %v", consumer, err)
	}
	later, err := s.AppReconcilePending("approval-gate")
	if err != nil || len(later.Requests) != 1 || later.Requests[0].Reason != AppReconcileGap {
		t.Fatalf("later claim = %+v, %v", later, err)
	}
}

func TestAppReconcileRequestSurvivesRestartUntilCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconcile.db")
	now := time.Now().UTC().Truncate(time.Millisecond)
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	seedReconcileApp(t, s, now)
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	before, err := s.AppReconcilePending("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	after, err := s.AppReconcilePending("approval-gate")
	if err != nil || after.ThroughRequestID != before.ThroughRequestID || len(after.Requests) != 1 {
		t.Fatalf("after restart = %+v, %v; before = %+v", after, err, before)
	}
	if err := s.CompleteAppReconcile("approval-gate", after.ThroughRequestID, after.ThroughSeq, now); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.AppReconcilePending("approval-gate"); err != nil || len(pending.Requests) != 0 {
		t.Fatalf("pending after completion = %+v, %v", pending, err)
	}
}

func TestAppReconcileInvocationStartsSettlesOnceAndKeepsItsClaimIdentity(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	version := seedReconcileApp(t, s, now)
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil || len(claim.Requests) != 1 {
		t.Fatalf("claim = %+v, %v", claim, err)
	}
	reason := `{"causes":["version_changed"],"version":2,"throughSeq":0,"previousVersions":[1]}`
	id, err := s.StartAppInvocation(AppInvocation{
		AppName: "approval-gate", VersionID: version.ID,
		Kind: AppInvocationKindReconcile, Handler: "reconcile", StartedAt: now,
		ReconcileReason: reason, ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	})
	if err != nil {
		t.Fatalf("start invocation: %v", err)
	}
	rows, err := s.ListAppInvocations("approval-gate", 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("running rows = %+v, %v", rows, err)
	}
	running := rows[0]
	if running.ID != id || running.Status != AppInvocationStatusRunning || running.Kind != AppInvocationKindReconcile ||
		running.ReconcileReason != reason || running.ThroughRequestID != claim.ThroughRequestID || !running.FinishedAt.IsZero() {
		t.Fatalf("running invocation = %+v", running)
	}

	finished := now.Add(2300 * time.Millisecond)
	settled, err := s.SettleAppInvocation(id, AppInvocationStatusError, "rebuild failed", finished)
	if err != nil || !settled {
		t.Fatalf("settle = %t, %v", settled, err)
	}
	if settled, err := s.SettleAppInvocation(id, AppInvocationStatusOK, "", finished.Add(time.Second)); err != nil || settled {
		t.Fatalf("second settle rewrote terminal row: settled=%t err=%v", settled, err)
	}
	attempt, ok, err := s.LatestOwedAppReconcileInvocation("approval-gate")
	if err != nil || !ok {
		t.Fatalf("latest owed attempt: ok=%t err=%v", ok, err)
	}
	if attempt.ID != id || attempt.Status != AppInvocationStatusError || attempt.Error != "rebuild failed" ||
		attempt.Duration != 2300*time.Millisecond || !attempt.FinishedAt.Equal(finished) {
		t.Fatalf("settled attempt = %+v", attempt)
	}
	if err := s.CompleteAppReconcile("approval-gate", claim.ThroughRequestID, claim.ThroughSeq, finished); err != nil {
		t.Fatal(err)
	}
	if attempt, ok, err := s.LatestOwedAppReconcileInvocation("approval-gate"); err != nil || ok {
		t.Fatalf("completed attempt still owed: %+v ok=%t err=%v", attempt, ok, err)
	}
}

func TestAppReconcileInvocationStartupRepairInterruptsRunningAttemptAndLeavesRequestOwed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconcile-invocation.db")
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	version := seedReconcileApp(t, s, now)
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAppInvocation(AppInvocation{
		AppName: "approval-gate", VersionID: version.ID, Kind: AppInvocationKindReconcile,
		Handler: "reconcile", StartedAt: now, ReconcileReason: `{"causes":["version_changed"]}`,
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	repairedAt := now.Add(7 * time.Second)
	if count, err := s.InterruptRunningAppInvocations(repairedAt); err != nil || count != 1 {
		t.Fatalf("interrupt count = %d, %v", count, err)
	}
	if count, err := s.InterruptRunningAppInvocations(repairedAt.Add(time.Second)); err != nil || count != 0 {
		t.Fatalf("second repair count = %d, %v", count, err)
	}
	attempt, ok, err := s.LatestOwedAppReconcileInvocation("approval-gate")
	if err != nil || !ok || attempt.Status != AppInvocationStatusInterrupted || attempt.Duration != 7*time.Second || !attempt.FinishedAt.Equal(repairedAt) {
		t.Fatalf("repaired attempt = %+v ok=%t err=%v", attempt, ok, err)
	}
	pending, err := s.AppReconcilePending("approval-gate")
	if err != nil || pending.ThroughRequestID != claim.ThroughRequestID || len(pending.Requests) != 1 {
		t.Fatalf("request after repair = %+v, %v", pending, err)
	}
}

func TestCompleteAppReconcileInvocationSettlesAttemptAndCrossesFenceAtomically(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	seedReconcileApp(t, s, now)
	version, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.StartAppInvocation(AppInvocation{
		AppName: "approval-gate", VersionID: version.ID, Kind: AppInvocationKindReconcile,
		Handler: "reconcile", StartedAt: now, ReconcileReason: `{"causes":["version_changed"]}`,
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := now.Add(4 * time.Second)
	if err := s.CompleteAppReconcileInvocation("approval-gate", id, claim.ThroughRequestID, claim.ThroughSeq, finished); err != nil {
		t.Fatalf("complete invocation: %v", err)
	}
	rows, err := s.ListAppInvocations("approval-gate", 10)
	if err != nil || len(rows) != 1 || rows[0].Status != AppInvocationStatusOK ||
		rows[0].Duration != 4*time.Second || !rows[0].FinishedAt.Equal(finished) {
		t.Fatalf("completed invocation = %+v, %v", rows, err)
	}
	if pending, err := s.AppReconcilePending("approval-gate"); err != nil || len(pending.Requests) != 0 {
		t.Fatalf("pending after success = %+v, %v", pending, err)
	}
	consumer, ok, err := s.GetBusConsumer("app:approval-gate")
	if err != nil || !ok || consumer.Cursor != claim.ThroughSeq {
		t.Fatalf("consumer after success = %+v ok=%t err=%v", consumer, ok, err)
	}
}

func TestCompleteAppReconcileInvocationRollsBackSettlementWhenFenceCannotCross(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	seedReconcileApp(t, s, now)
	version, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.StartAppInvocation(AppInvocation{
		AppName: "approval-gate", VersionID: version.ID, Kind: AppInvocationKindReconcile,
		Handler: "reconcile", StartedAt: now, ReconcileReason: `{"causes":["version_changed"]}`,
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBusConsumer("app:approval-gate"); err != nil {
		t.Fatal(err)
	}
	err = s.CompleteAppReconcileInvocation("approval-gate", id, claim.ThroughRequestID, claim.ThroughSeq, now.Add(time.Second))
	if err == nil || !strings.Contains(err.Error(), "no bus consumer") {
		t.Fatalf("completion error = %v", err)
	}
	rows, err := s.ListAppInvocations("approval-gate", 10)
	if err != nil || len(rows) != 1 || rows[0].Status != AppInvocationStatusRunning || !rows[0].FinishedAt.IsZero() {
		t.Fatalf("settlement survived rolled-back fence: %+v, %v", rows, err)
	}
	pending, err := s.AppReconcilePending("approval-gate")
	if err != nil || pending.ThroughRequestID != claim.ThroughRequestID || len(pending.Requests) != 1 {
		t.Fatalf("request moved despite rollback = %+v, %v", pending, err)
	}
}

func TestCompleteAppReconcileInvocationRefusesASettledOrDifferentClaim(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	seedReconcileApp(t, s, now)
	version, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.StartAppInvocation(AppInvocation{
		AppName: "approval-gate", VersionID: version.ID, Kind: AppInvocationKindReconcile,
		StartedAt: now, ReconcileReason: `{}`, ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAppReconcileInvocation("approval-gate", id, claim.ThroughRequestID+1, claim.ThroughSeq, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "owns claim") {
		t.Fatalf("different claim error = %v", err)
	}
	if settled, err := s.SettleAppInvocation(id, AppInvocationStatusError, "boom", now.Add(time.Second)); err != nil || !settled {
		t.Fatalf("settle failure = %t, %v", settled, err)
	}
	if err := s.CompleteAppReconcileInvocation("approval-gate", id, claim.ThroughRequestID, claim.ThroughSeq, now.Add(2*time.Second)); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("settled claim error = %v", err)
	}
	if pending, err := s.AppReconcilePending("approval-gate"); err != nil || len(pending.Requests) != 1 {
		t.Fatalf("refusal completed request = %+v, %v", pending, err)
	}
}

func TestLatestOwedAppReconcileInvocationKeepsAnOkBoundaryAboveProgress(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	seedReconcileApp(t, s, now)
	version, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.StartAppInvocation(AppInvocation{
		AppName: "approval-gate", VersionID: version.ID, Kind: AppInvocationKindReconcile,
		StartedAt: now, ReconcileReason: `{}`, ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	// This is the state a non-atomic caller could leave if its handler settlement
	// committed but the request/cursor transaction failed. The boundary must stay
	// recoverable even though the attempt itself says ok.
	if settled, err := s.SettleAppInvocation(id, AppInvocationStatusOK, "", now.Add(time.Second)); err != nil || !settled {
		t.Fatalf("settle = %t, %v", settled, err)
	}
	attempt, ok, err := s.LatestOwedAppReconcileInvocation("approval-gate")
	if err != nil || !ok || attempt.ID != id || attempt.Status != AppInvocationStatusOK {
		t.Fatalf("owed ok boundary = %+v ok=%t err=%v", attempt, ok, err)
	}
}

func TestStartAppReconcileInvocationRefusesAnUnusableClaim(t *testing.T) {
	s := New()
	now := time.Now()
	for _, tc := range []struct {
		name   string
		reason string
		claim  int64
		want   string
	}{
		{name: "no claim", reason: `{}`, want: "through_request_id"},
		{name: "invalid reason", reason: `{`, claim: 1, want: "valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.StartAppInvocation(AppInvocation{
				AppName: "approval-gate", VersionID: 1, Kind: AppInvocationKindReconcile,
				StartedAt: now, ReconcileReason: tc.reason, ThroughRequestID: tc.claim,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAppReEnableCreatesNoReconcileRequest(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedReconcileApp(t, s, now)
	if exists, changed, err := s.SetAppBusConsumerEnabled("approval-gate", false, now); err != nil || !exists || !changed {
		t.Fatalf("disable = exists:%t changed:%t err:%v", exists, changed, err)
	}
	if exists, changed, err := s.SetAppBusConsumerEnabled("approval-gate", true, now); err != nil || !exists || !changed {
		t.Fatalf("re-enable = exists:%t changed:%t err:%v", exists, changed, err)
	}
	if pending, err := s.AppReconcilePending("approval-gate"); err != nil || len(pending.Requests) != 0 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}

func TestAppVersionChangeFenceStopsAtTheConsumerCursor(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedReconcileApp(t, s, now)
	first, err := s.AppendBusEvent(BusEvent{Name: "ticket.created"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendBusEvent(BusEvent{Name: "ticket.updated"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBusConsumerCursor("app:approval-gate", first, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil || len(claim.Requests) != 1 {
		t.Fatalf("pending = %+v, %v", claim, err)
	}
	if got := claim.Requests[0].ThroughSeq; got != first {
		t.Fatalf("version fence = %d, want consumer cursor %d; the retained fact above it must still be delivered", got, first)
	}
}

func TestAppGapFenceStopsBeforeTheEarliestRetainedFact(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedReconcileApp(t, s, now)
	inserted, err := s.RequestAppReconcileGap("approval-gate", 1, 4, now)
	if err != nil || !inserted {
		t.Fatalf("gap insert = %t, %v", inserted, err)
	}
	claim, err := s.AppReconcilePending("approval-gate")
	if err != nil || len(claim.Requests) != 1 {
		t.Fatalf("pending = %+v, %v", claim, err)
	}
	if got := claim.Requests[0].ThroughSeq; got != 3 {
		t.Fatalf("gap fence = %d, want earliest-1 = 3", got)
	}
}

func TestAppReconcileReEnableBeforeFirstVersionCreatesNoRequest(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.SaveApp("view-only", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBusConsumer(BusConsumer{
		Name: "app:view-only", Filter: "app.subscribes.to.nothing", Enabled: false,
	}, now); err != nil {
		t.Fatal(err)
	}
	if exists, changed, err := s.SetAppBusConsumerEnabled("view-only", true, now); err != nil || !exists || !changed {
		t.Fatalf("enable = exists:%t changed:%t err:%v", exists, changed, err)
	}
	if pending, err := s.AppReconcilePending("view-only"); err != nil || len(pending.Requests) != 0 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}
