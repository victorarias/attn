package store

import (
	"path/filepath"
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
