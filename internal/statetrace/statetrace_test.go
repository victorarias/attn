package statetrace

import (
	"strings"
	"testing"
	"time"
)

func claims(t *testing.T, got []Observation) []string {
	t.Helper()
	out := make([]string, 0, len(got))
	for _, obs := range got {
		out = append(out, obs.Claim)
	}
	return out
}

func TestRecorderKeepsOrderPerSession(t *testing.T) {
	r := New(8)
	r.Record("a", Observation{Claim: "working"})
	r.Record("b", Observation{Claim: "idle"})
	r.Record("a", Observation{Claim: "waiting_input"})

	if got := claims(t, r.Observations("a")); strings.Join(got, ",") != "working,waiting_input" {
		t.Fatalf("session a: got %v", got)
	}
	if got := claims(t, r.Observations("b")); strings.Join(got, ",") != "idle" {
		t.Fatalf("session b: got %v", got)
	}
	if got := r.Observations("missing"); got != nil {
		t.Fatalf("unknown session: got %v, want nil", got)
	}
}

// The ring exists to bound memory on a long-lived daemon, so overflow must drop
// the oldest and keep the newest — the newest is what explains the color now.
func TestRecorderEvictsOldestOnOverflow(t *testing.T) {
	r := New(3)
	for _, claim := range []string{"one", "two", "three", "four", "five"} {
		r.Record("s", Observation{Claim: claim})
	}
	if got := strings.Join(claims(t, r.Observations("s")), ","); got != "three,four,five" {
		t.Fatalf("got %q", got)
	}
}

func TestRecorderWrapsRepeatedly(t *testing.T) {
	r := New(2)
	for i := range 7 {
		r.Record("s", Observation{Claim: string(rune('a' + i))})
	}
	if got := strings.Join(claims(t, r.Observations("s")), ","); got != "f,g" {
		t.Fatalf("got %q", got)
	}
}

func TestRecorderSnapshotIsACopy(t *testing.T) {
	r := New(4)
	r.Record("s", Observation{Claim: "working"})
	snapshot := r.Observations("s")
	snapshot[0].Claim = "mutated"
	if got := r.Observations("s")[0].Claim; got != "working" {
		t.Fatalf("recorder state leaked through snapshot: got %q", got)
	}
}

func TestRecorderForgetDropsSession(t *testing.T) {
	r := New(4)
	r.Record("s", Observation{Claim: "working"})
	r.Forget("s")
	if got := r.Observations("s"); got != nil {
		t.Fatalf("got %v after Forget, want nil", got)
	}
}

// A nil recorder is the zero-configuration path (tests, a daemon built without
// tracing); every call must be a no-op rather than a panic, so call sites need
// no guard.
func TestNilRecorderIsInert(t *testing.T) {
	var r *Recorder
	r.Record("s", Observation{Claim: "working"})
	r.Forget("s")
	if got := r.Observations("s"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := r.Capacity(); got != 0 {
		t.Fatalf("capacity %d, want 0", got)
	}
}

func TestNewClampsNonPositiveCapacity(t *testing.T) {
	if got := New(0).Capacity(); got != DefaultCapacity {
		t.Fatalf("capacity %d, want %d", got, DefaultCapacity)
	}
	if got := New(-5).Capacity(); got != DefaultCapacity {
		t.Fatalf("capacity %d, want %d", got, DefaultCapacity)
	}
}

// A source that reports only ObservedAt must still get a RecordedAt, because the
// gap between the two is the diagnostic for a delayed observation.
func TestRecordDefaultsTimestamps(t *testing.T) {
	r := New(4)
	observed := time.Now().Add(-2 * time.Second)
	r.Record("s", Observation{Claim: "working", ObservedAt: observed})
	r.Record("s", Observation{Claim: "idle"})

	got := r.Observations("s")
	if !got[0].ObservedAt.Equal(observed) {
		t.Fatalf("ObservedAt overwritten: %s", got[0].ObservedAt)
	}
	if got[0].RecordedAt.IsZero() {
		t.Fatal("RecordedAt not defaulted")
	}
	if !got[1].ObservedAt.Equal(got[1].RecordedAt) {
		t.Fatalf("ObservedAt %s should default to RecordedAt %s", got[1].ObservedAt, got[1].RecordedAt)
	}
}

func TestLogLineCarriesTheWholeObservation(t *testing.T) {
	observed := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	obs := Observation{
		Source:     "screen",
		Claim:      "pending_approval",
		Detail:     "screen scrape",
		Cause:      "live_signal",
		Outcome:    OutcomeVetoed,
		Reason:     "driver_veto",
		ObservedAt: observed,
		RecordedAt: observed.Add(150 * time.Millisecond),
	}
	line := obs.LogLine("sess-1")
	for _, want := range []string{
		"session=sess-1",
		"source=screen",
		"claim=pending_approval",
		"outcome=vetoed",
		"cause=live_signal",
		"reason=driver_veto",
		`detail="screen scrape"`,
		"lag=150ms",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

// A skip carries no claim; the line must stay parseable rather than emitting an
// empty key.
func TestLogLineRendersMissingFieldsAsDashes(t *testing.T) {
	line := Observation{Outcome: OutcomeSkipped}.LogLine("sess-1")
	if !strings.Contains(line, "source=- claim=- outcome=skipped") {
		t.Fatalf("got %q", line)
	}
	if strings.Contains(line, "cause=") || strings.Contains(line, "reason=") || strings.Contains(line, "detail=") {
		t.Fatalf("empty optional fields should be omitted: %q", line)
	}
}
