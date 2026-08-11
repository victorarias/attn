package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSupervisedParkSurvivesReopeningTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	parkedAt := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	code := 3
	park := SupervisedPark{
		Child:          "runtime",
		ParkedAt:       parkedAt,
		RestartAttempt: 10,
		ExitAt:         parkedAt.Add(-time.Second),
		ExitCode:       &code,
		ExitError:      "the host died on startup",
	}
	if err := s.SaveSupervisedPark(park); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, ok, err := reopened.GetSupervisedPark("runtime")
	if err != nil || !ok {
		t.Fatalf("get after reopen: ok=%v err=%v", ok, err)
	}
	if !got.ParkedAt.Equal(parkedAt) {
		t.Fatalf("ParkedAt=%s, want %s", got.ParkedAt, parkedAt)
	}
	if got.RestartAttempt != 10 {
		t.Fatalf("RestartAttempt=%d, want 10", got.RestartAttempt)
	}
	if !got.ExitAt.Equal(park.ExitAt) {
		t.Fatalf("ExitAt=%s, want %s", got.ExitAt, park.ExitAt)
	}
	if got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("ExitCode=%v, want 3", got.ExitCode)
	}
	if got.ExitError != park.ExitError {
		t.Fatalf("ExitError=%q, want %q", got.ExitError, park.ExitError)
	}
}

// A child that never exited has no code to record, and one that was never parked
// has no row. Both are ordinary answers, not missing data.
func TestSupervisedParkHandlesAbsenceAndClearing(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, ok, err := s.GetSupervisedPark("runtime"); ok || err != nil {
		t.Fatalf("get before any park: ok=%v err=%v", ok, err)
	}
	if cleared, err := s.ClearSupervisedPark("runtime"); cleared || err != nil {
		t.Fatalf("clear before any park: cleared=%v err=%v", cleared, err)
	}

	if err := s.SaveSupervisedPark(SupervisedPark{
		Child:      "runtime",
		ParkedAt:   time.Now().UTC(),
		ExitSignal: "killed",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := s.GetSupervisedPark("runtime")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ExitCode != nil {
		t.Fatalf("ExitCode=%v for a signalled exit, want nil", got.ExitCode)
	}
	if got.ExitSignal != "killed" || !got.ExitAt.IsZero() {
		t.Fatalf("got=%+v, want signal killed and no exit time", got)
	}

	// A second park replaces the first rather than colliding on the primary key.
	later := time.Now().UTC().Add(time.Hour)
	if err := s.SaveSupervisedPark(SupervisedPark{Child: "runtime", ParkedAt: later, RestartAttempt: 4}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	replaced, _, err := s.GetSupervisedPark("runtime")
	if err != nil {
		t.Fatalf("get after re-save: %v", err)
	}
	if replaced.RestartAttempt != 4 || replaced.ExitSignal != "" {
		t.Fatalf("got=%+v, want the newer park to have replaced the older one whole", replaced)
	}

	if cleared, err := s.ClearSupervisedPark("runtime"); !cleared || err != nil {
		t.Fatalf("clear: cleared=%v err=%v", cleared, err)
	}
	if _, ok, _ := s.GetSupervisedPark("runtime"); ok {
		t.Fatal("the park is still there after clearing it")
	}
}
