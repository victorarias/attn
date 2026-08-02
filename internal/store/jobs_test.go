package store

import (
	"testing"
	"time"
)

func newJobRecord(id, kind string, at time.Time) JobRecord {
	return JobRecord{
		ID:          id,
		Kind:        kind,
		State:       "queued",
		ScheduledAt: at,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
}

func TestJobs_UpsertGetDelete(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	rec := JobRecord{
		ID:          "job-1",
		Kind:        "compact_context",
		UniqueKey:   "ws-1",
		Priority:    7,
		Payload:     `{"workspace_id":"ws-1"}`,
		Result:      `{"bytes":42}`,
		State:       "failed",
		Attempts:    2,
		MaxAttempts: 3,
		ScheduledAt: now.Add(30 * time.Second),
		LastError:   "boom",
		Requeued:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.UpsertJob(rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, ok, err := s.GetJob(rec.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Kind != rec.Kind || got.UniqueKey != rec.UniqueKey || got.State != rec.State {
		t.Fatalf("core fields mismatch: %+v", got)
	}
	if got.Priority != 7 || got.Attempts != 2 || got.MaxAttempts != 3 {
		t.Fatalf("numeric fields mismatch: %+v", got)
	}
	if got.Payload != rec.Payload || got.Result != rec.Result || got.LastError != "boom" || !got.Requeued {
		t.Fatalf("opaque fields mismatch: %+v", got)
	}
	if !got.ScheduledAt.Equal(rec.ScheduledAt) {
		t.Fatalf("scheduled_at not preserved: got %v want %v", got.ScheduledAt, rec.ScheduledAt)
	}

	if err := s.DeleteJob(rec.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := s.GetJob(rec.ID); err != nil || ok {
		t.Fatalf("after delete: ok=%v err=%v", ok, err)
	}
	if err := s.DeleteJob(rec.ID); err != nil {
		t.Fatalf("deleting a missing job should be a no-op, got %v", err)
	}
}

func TestJobs_UniqueKeyIsScopedToItsKind(t *testing.T) {
	s := New()
	now := time.Now().UTC()

	a := newJobRecord("job-a", "compact", now)
	a.UniqueKey = "ws-1"
	b := newJobRecord("job-b", "narrate", now)
	b.UniqueKey = "ws-1"
	for _, rec := range []JobRecord{a, b} {
		if err := s.UpsertJob(rec); err != nil {
			t.Fatalf("upsert %s: %v", rec.ID, err)
		}
	}

	// The same subject under two kinds is two jobs, not a collision.
	got, ok, err := s.GetJobByUniqueKey("compact", "ws-1")
	if err != nil || !ok {
		t.Fatalf("get compact: ok=%v err=%v", ok, err)
	}
	if got.ID != "job-a" {
		t.Errorf("compact/ws-1 resolved to %s, want job-a", got.ID)
	}
	got, ok, err = s.GetJobByUniqueKey("narrate", "ws-1")
	if err != nil || !ok {
		t.Fatalf("get narrate: ok=%v err=%v", ok, err)
	}
	if got.ID != "job-b" {
		t.Errorf("narrate/ws-1 resolved to %s, want job-b", got.ID)
	}

	if _, ok, err := s.GetJobByUniqueKey("compact", "ws-missing"); err != nil || ok {
		t.Errorf("unknown key: ok=%v err=%v", ok, err)
	}
}

func TestJobs_KeylessJobsAreNotCoalesced(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	// Two keyless jobs of one kind must coexist: that is what the partial unique
	// index exists to allow, and what durable activities depend on.
	for _, id := range []string{"job-a", "job-b"} {
		if err := s.UpsertJob(newJobRecord(id, "activity", now)); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
	all, err := s.ListJobs()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("stored %d keyless jobs, want 2", len(all))
	}
	// An empty key must never resolve to one of them.
	if _, ok, err := s.GetJobByUniqueKey("activity", ""); err != nil || ok {
		t.Errorf("empty key matched a job: ok=%v err=%v", ok, err)
	}
}

func TestJobs_EligibleOrdersAndFiltersTheQueue(t *testing.T) {
	s := New()
	now := time.Now().UTC()

	due := newJobRecord("due", "k", now.Add(-time.Minute))
	high := newJobRecord("high", "k", now.Add(-time.Minute))
	high.Priority = 10
	retrying := newJobRecord("retrying", "k", now.Add(-time.Second))
	retrying.State = "failed"
	future := newJobRecord("future", "k", now.Add(time.Hour))
	running := newJobRecord("running", "k", now.Add(-time.Minute))
	running.State = "running"
	finished := newJobRecord("finished", "k", now.Add(-time.Minute))
	finished.State = "done"
	dead := newJobRecord("dead", "k", now.Add(-time.Minute))
	dead.State = "dead"

	for _, rec := range []JobRecord{due, high, retrying, future, running, finished, dead} {
		if err := s.UpsertJob(rec); err != nil {
			t.Fatalf("upsert %s: %v", rec.ID, err)
		}
	}

	got, err := s.EligibleJobs(now, 10)
	if err != nil {
		t.Fatalf("eligible: %v", err)
	}
	var ids []string
	for _, rec := range got {
		ids = append(ids, rec.ID)
	}
	// Priority first, then earliest scheduled. A future, running, done, or dead
	// row is not claimable.
	want := []string{"high", "due", "retrying"}
	if len(ids) != len(want) {
		t.Fatalf("eligible = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("eligible = %v, want %v", ids, want)
		}
	}
}

func TestJobs_EligibleRespectsItsLimit(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	for _, id := range []string{"a", "b", "c"} {
		if err := s.UpsertJob(newJobRecord(id, "k", now.Add(-time.Minute))); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
	got, err := s.EligibleJobs(now, 2)
	if err != nil {
		t.Fatalf("eligible: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d rows, want the requested 2", len(got))
	}
}

func TestJobs_RecoverRunningJobs(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	stuck := newJobRecord("stuck", "k", now.Add(-time.Hour))
	stuck.State = "running"
	stuck.Attempts = 1
	other := newJobRecord("queued", "k", now.Add(time.Hour))
	for _, rec := range []JobRecord{stuck, other} {
		if err := s.UpsertJob(rec); err != nil {
			t.Fatalf("upsert %s: %v", rec.ID, err)
		}
	}

	n, err := s.RecoverRunningJobs(now)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered %d jobs, want 1", n)
	}
	got, _, err := s.GetJob("stuck")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != "queued" {
		t.Errorf("state = %s, want queued", got.State)
	}
	if !got.ScheduledAt.Equal(now.Truncate(time.Nanosecond)) && got.ScheduledAt.Before(now.Add(-time.Second)) {
		t.Errorf("scheduled_at = %v, want it pulled forward to now", got.ScheduledAt)
	}
	if got.Attempts != 1 {
		t.Errorf("attempts = %d, want the spent attempt preserved", got.Attempts)
	}
	// An untouched row keeps its schedule.
	if other, _, _ := s.GetJob("queued"); other.State != "queued" || !other.ScheduledAt.After(now) {
		t.Errorf("a queued job was disturbed by recovery: %+v", other)
	}
}

func TestJobs_TrimDoneKeepsDeadAndFreshJobs(t *testing.T) {
	s := New()
	now := time.Now().UTC()

	oldDone := newJobRecord("old-done", "k", now.Add(-72*time.Hour))
	oldDone.State = "done"
	oldDone.UpdatedAt = now.Add(-72 * time.Hour)
	freshDone := newJobRecord("fresh-done", "k", now)
	freshDone.State = "done"
	oldDead := newJobRecord("old-dead", "k", now.Add(-72*time.Hour))
	oldDead.State = "dead"
	oldDead.UpdatedAt = now.Add(-72 * time.Hour)

	for _, rec := range []JobRecord{oldDone, freshDone, oldDead} {
		if err := s.UpsertJob(rec); err != nil {
			t.Fatalf("upsert %s: %v", rec.ID, err)
		}
	}

	n, err := s.TrimDoneJobs(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if n != 1 {
		t.Fatalf("trimmed %d jobs, want 1", n)
	}
	if _, ok, _ := s.GetJob("old-done"); ok {
		t.Error("the aged completed job survived the trim")
	}
	if _, ok, _ := s.GetJob("fresh-done"); !ok {
		t.Error("a recently completed job was trimmed")
	}
	// A dead job is the record a failure notification points at.
	if _, ok, _ := s.GetJob("old-dead"); !ok {
		t.Error("an aged dead job was trimmed")
	}
}
