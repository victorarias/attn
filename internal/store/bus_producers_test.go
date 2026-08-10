package store

import (
	"fmt"
	"testing"
	"time"
)

// The producer aggregate is the surface that would have caught a producer
// writing most of the log. These tests seed a log shaped like the real one —
// one class dominating, a long tail behind it — because the counts, the share,
// and the window boundaries are all things a wrong query gets subtly wrong on
// realistic data and exactly right on three rows.

// seedBusLog writes a log in the shape production had: one loud class
// republishing a handful of subjects, a moderate one, and a quiet tail.
func seedBusLog(t *testing.T, s *Store, now time.Time) {
	t.Helper()
	// Loud: 40 inside the last hour, 60 more inside the last 24, 100 older.
	for i := 0; i < 40; i++ {
		appendBus(t, s, "session.state.changed", fmt.Sprintf("sess_%d", i%4), now.Add(-30*time.Minute))
	}
	for i := 0; i < 60; i++ {
		appendBus(t, s, "session.state.changed", fmt.Sprintf("sess_%d", i%4), now.Add(-6*time.Hour))
	}
	for i := 0; i < 100; i++ {
		appendBus(t, s, "session.state.changed", fmt.Sprintf("sess_%d", i%4), now.Add(-72*time.Hour))
	}
	// Moderate: 10 in the last 24, nothing in the last hour.
	for i := 0; i < 10; i++ {
		appendBus(t, s, "pr.updated", fmt.Sprintf("pr_%d", i), now.Add(-3*time.Hour))
	}
	// Quiet tail.
	appendBus(t, s, "ticket.created", "tk_1", now.Add(-2*time.Hour))
}

func producerByName(t *testing.T, rows []BusProducer, name string) BusProducer {
	t.Helper()
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no producer %q in %d row(s)", name, len(rows))
	return BusProducer{}
}

func TestBusProducersCountsEachWindowAndRanksLoudestFirst(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := busBase.Add(96 * time.Hour)
	seedBusLog(t, s, now)

	rows, err := s.BusProducers([]time.Time{
		now.Add(-time.Hour),
		now.Add(-6 * time.Hour),
		now.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("BusProducers: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 fact classes, got %d: %+v", len(rows), rows)
	}
	// Loudest first is what makes the table answer "who is writing all this?"
	// without the reader sorting it themselves.
	if rows[0].Name != "session.state.changed" {
		t.Fatalf("want the loudest class first, got %q", rows[0].Name)
	}

	loud := producerByName(t, rows, "session.state.changed")
	if loud.Events != 200 {
		t.Errorf("events = %d, want 200", loud.Events)
	}
	// Four subjects across 200 events is the shape that says "republishing",
	// and it is the column that separates a busy producer from a flapping one.
	if loud.Subjects != 4 {
		t.Errorf("subjects = %d, want 4", loud.Subjects)
	}
	if got := loud.Recent; got[0] != 40 || got[1] != 100 || got[2] != 100 {
		t.Errorf("window counts = %v, want [40 100 100]", got)
	}
	if loud.Bytes <= 0 {
		t.Errorf("bytes = %d, want the event text to be measured", loud.Bytes)
	}

	moderate := producerByName(t, rows, "pr.updated")
	if got := moderate.Recent; got[0] != 0 || got[1] != 10 || got[2] != 10 {
		t.Errorf("pr.updated window counts = %v, want [0 10 10]", got)
	}
}

// The aggregate replaced a separate BusLogSize scan, so it has to agree with it
// exactly — otherwise the page and the log's own weight would tell two stories.
func TestBusProducersTotalsMatchBusLogSize(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := busBase.Add(96 * time.Hour)
	seedBusLog(t, s, now)

	rows, err := s.BusProducers([]time.Time{now.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("BusProducers: %v", err)
	}
	var events, bytes int64
	for _, r := range rows {
		events += r.Events
		bytes += r.Bytes
	}
	wantRows, wantBytes, err := s.BusLogSize()
	if err != nil {
		t.Fatalf("BusLogSize: %v", err)
	}
	if events != wantRows {
		t.Errorf("summed events = %d, BusLogSize rows = %d", events, wantRows)
	}
	if bytes != wantBytes {
		t.Errorf("summed bytes = %d, BusLogSize bytes = %d", bytes, wantBytes)
	}
}

// An empty log must answer "nothing", not fail. It is the state a fresh
// install is in, and the page opens on it.
func TestBusProducersOnAnEmptyLog(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	rows, err := s.BusProducers([]time.Time{time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("BusProducers: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows on an empty log, got %+v", rows)
	}
}

// Asking for no windows still has to answer the totals: the cutoff list is
// built from policy that could legitimately shrink to nothing.
func TestBusProducersWithoutCutoffs(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := busBase.Add(96 * time.Hour)
	seedBusLog(t, s, now)

	rows, err := s.BusProducers(nil)
	if err != nil {
		t.Fatalf("BusProducers: %v", err)
	}
	loud := producerByName(t, rows, "session.state.changed")
	if loud.Events != 200 || len(loud.Recent) != 0 {
		t.Fatalf("events = %d, recent = %v; want 200 and no window counts", loud.Events, loud.Recent)
	}
}

// EventTimeAt answers both age questions: how old the log's tail is, and how
// long a consumer's oldest unread event has waited. A cursor past the head has
// no such event, and saying so is different from saying "the epoch".
func TestBusEventTimeAt(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	first := appendBus(t, s, "session.state.changed", "sess_1", busBase)
	appendBus(t, s, "pr.updated", "pr_1", busBase.Add(2*time.Hour))
	last := appendBus(t, s, "ticket.created", "tk_1", busBase.Add(5*time.Hour))

	at, ok, err := s.BusEventTimeAt(first)
	if err != nil || !ok {
		t.Fatalf("BusEventTimeAt(%d) = %v, %v, %v", first, at, ok, err)
	}
	if !at.Equal(busBase) {
		t.Errorf("oldest = %s, want %s", at, busBase)
	}

	// The first event at or ABOVE the seq, so a cursor sitting on a trimmed
	// row still reports the oldest thing that consumer has left to read.
	at, ok, err = s.BusEventTimeAt(first + 1)
	if err != nil || !ok {
		t.Fatalf("BusEventTimeAt(%d) = %v, %v, %v", first+1, at, ok, err)
	}
	if !at.Equal(busBase.Add(2 * time.Hour)) {
		t.Errorf("next = %s, want %s", at, busBase.Add(2*time.Hour))
	}

	if _, ok, err := s.BusEventTimeAt(last + 1); err != nil || ok {
		t.Errorf("past the head: ok = %v, err = %v; want false, nil", ok, err)
	}
}
