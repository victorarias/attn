package store

import (
	"testing"
	"time"
)

func TestTasks_UpsertGetDelete(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	rec := TaskRecord{
		ID:            "compact_context:ws-1",
		Kind:          "compact_context",
		Subject:       "ws-1",
		State:         "queued",
		Attempts:      2,
		NextAttemptAt: now.Add(30 * time.Second),
		LastError:     "boom",
		MetaJSON:      `{"transcript":"/tmp/x.jsonl"}`,
		Requeued:      true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.UpsertTask(rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, ok, err := s.GetTask(rec.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Kind != rec.Kind || got.Subject != rec.Subject || got.State != rec.State {
		t.Fatalf("core fields mismatch: %+v", got)
	}
	if got.Attempts != 2 || got.LastError != "boom" || got.MetaJSON != rec.MetaJSON || !got.Requeued {
		t.Fatalf("scalar fields mismatch: %+v", got)
	}
	if !got.NextAttemptAt.Equal(rec.NextAttemptAt) {
		t.Fatalf("next_attempt_at not preserved: got %v want %v", got.NextAttemptAt, rec.NextAttemptAt)
	}

	// Upsert again with new state — same id must overwrite, not duplicate.
	rec.State = "running"
	rec.Attempts = 3
	if err := s.UpsertTask(rec); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, ok, _ = s.GetTask(rec.ID)
	if !ok || got.State != "running" || got.Attempts != 3 {
		t.Fatalf("overwrite failed: %+v", got)
	}
	all, err := s.ListTasks()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 row after re-upsert, got %d", len(all))
	}

	if err := s.DeleteTask(rec.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := s.GetTask(rec.ID); ok {
		t.Fatalf("row still present after delete")
	}
	// Deleting a missing row is not an error.
	if err := s.DeleteTask("nope"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestTasks_GetMissing(t *testing.T) {
	s := New()
	rec, ok, err := s.GetTask("absent")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok || rec != nil {
		t.Fatalf("expected miss, got ok=%v rec=%v", ok, rec)
	}
}

func TestTasks_ListNewestUpdatedFirst(t *testing.T) {
	s := New()
	base := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(id string, updated time.Time) TaskRecord {
		return TaskRecord{ID: id, Kind: "k", Subject: id, State: "queued",
			NextAttemptAt: base, CreatedAt: base, UpdatedAt: updated}
	}
	if err := s.UpsertTask(mk("a", base.Add(1*time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTask(mk("b", base.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTask(mk("c", base.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListTasks()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	order := []string{all[0].ID, all[1].ID, all[2].ID}
	want := []string{"b", "c", "a"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestTasks_RecoverRunningTasks(t *testing.T) {
	s := New()
	base := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(id, state string) TaskRecord {
		return TaskRecord{ID: id, Kind: "k", Subject: id, State: state,
			NextAttemptAt: base.Add(time.Hour), CreatedAt: base, UpdatedAt: base}
	}
	for _, r := range []TaskRecord{mk("run-1", "running"), mk("run-2", "running"), mk("q", "queued"), mk("d", "done")} {
		if err := s.UpsertTask(r); err != nil {
			t.Fatal(err)
		}
	}
	recoverAt := base.Add(5 * time.Minute)
	n, err := s.RecoverRunningTasks(recoverAt)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 2 {
		t.Fatalf("recovered %d, want 2", n)
	}
	for _, id := range []string{"run-1", "run-2"} {
		got, _, _ := s.GetTask(id)
		if got.State != "queued" {
			t.Fatalf("%s state=%s, want queued", id, got.State)
		}
		if !got.NextAttemptAt.Equal(recoverAt) {
			t.Fatalf("%s next_attempt_at=%v, want %v", id, got.NextAttemptAt, recoverAt)
		}
	}
	// Non-running rows are untouched.
	if got, _, _ := s.GetTask("q"); got.State != "queued" || !got.NextAttemptAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("queued row was disturbed: %+v", got)
	}
	if got, _, _ := s.GetTask("d"); got.State != "done" {
		t.Fatalf("done row was disturbed: %+v", got)
	}
}
