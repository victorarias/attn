package bus

import (
	"sync"
	"testing"
	"time"
)

// Announce is the entry point for facts the bus did not append itself — a store
// transaction that committed a change together with the fact describing it. The
// contract it has to hold is ordering: whoever wrote to the log, subscribers see
// it in seq order, exactly once.

// A subscriber that watches the same names.
func watchAll(b *Bus) (func() []Event, func()) {
	var (
		mu   sync.Mutex
		seen []Event
	)
	stop := b.Subscribe(Filter{}, func(ev Event) {
		mu.Lock()
		seen = append(seen, ev)
		mu.Unlock()
	})
	snapshot := func() []Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]Event(nil), seen...)
	}
	return snapshot, stop
}

func seqsOf(events []Event) []int64 {
	out := make([]int64, 0, len(events))
	for _, e := range events {
		out = append(out, e.Seq)
	}
	return out
}

func inOrder(seqs []int64) bool {
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			return false
		}
	}
	return true
}

// The plain case: a fact that arrived on the log without going through Publish
// still reaches subscribers, carrying the seq it was appended at.
func TestAnnounceDeliversWhatTheLogGainedWithoutPublish(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	now := time.Now()
	first := s.appendOutOfBand("document.changed", "ext/x/requests/a", now)
	second := s.appendOutOfBand("document.changed", "ext/x/requests/b", now)

	b.Announce()

	got := seqsOf(seen())
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("announce delivered %v, want [%d %d]", got, first, second)
	}
}

// Announcing is reading forward from a mark, so announcing twice delivers once.
// This is what lets a caller announce unconditionally after a commit without
// knowing whether anyone else already did.
func TestAnnouncingTwiceDeliversOnce(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	s.appendOutOfBand("document.changed", "ext/x/requests/a", time.Now())
	b.Announce()
	b.Announce()
	b.Announce()

	if got := seen(); len(got) != 1 {
		t.Fatalf("three announces of one fact delivered %d time(s)", len(got))
	}
}

// A bus attached to a log that already has history announces what happens next,
// not the history — otherwise every daemon start would replay the log at every
// live subscriber.
func TestAnnounceDoesNotReplayWhatWasThereBeforeTheBus(t *testing.T) {
	s := newMemStore()
	now := time.Now()
	s.appendOutOfBand("document.changed", "ext/x/requests/old", now)
	s.appendOutOfBand("document.changed", "ext/x/requests/older", now)

	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	b.Announce()
	if got := seen(); len(got) != 0 {
		t.Fatalf("a fresh bus replayed %d historical fact(s)", len(got))
	}

	fresh := s.appendOutOfBand("document.changed", "ext/x/requests/new", now)
	b.Announce()
	got := seqsOf(seen())
	if len(got) != 1 || got[0] != fresh {
		t.Fatalf("announce delivered %v, want only the new fact at %d", got, fresh)
	}
}

// The ordering property, under the race it exists for: writers commit their
// facts and then race to announce, and Publish runs against the same mark. Every
// subscriber must see the log's own order, and see each fact once — never a
// later seq before an earlier one because its announcer won the race.
func TestRacingWritersDeliverTheLogsOrderExactlyOnce(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	const writers = 8
	const each = 25

	var (
		start sync.WaitGroup
		done  sync.WaitGroup
	)
	start.Add(1)
	for w := range writers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			for i := range each {
				// Commit, then announce — the shape of a composite write. The
				// gap between the two is where the ordering race lives.
				if w%2 == 0 {
					s.appendOutOfBand("document.changed", "ext/x/requests/a", time.Now())
					b.Announce()
					continue
				}
				// Publishes interleave with committed writes on the same mark.
				if _, err := b.Publish("session.state.changed", "s", map[string]int{"i": i}); err != nil {
					t.Errorf("publish: %v", err)
					return
				}
			}
		}()
	}
	start.Done()
	done.Wait()
	// A commit whose announce lost every race is repaired by the next announce;
	// one final call stands in for the retention tick that would do it anyway.
	b.Announce()

	got := seqsOf(seen())
	if want := writers * each; len(got) != want {
		t.Fatalf("delivered %d fact(s), want %d — a fact was dropped or doubled", len(got), want)
	}
	if !inOrder(got) {
		t.Fatalf("delivered out of the log's order: %v", got)
	}
}

// A bus with no store has no log to read forward from, so Announce is a no-op
// rather than a panic: the same degradation Publish already makes, and the
// shape every test daemon runs in.
func TestAnnounceWithoutAStoreDoesNothing(t *testing.T) {
	b := New(Options{Log: func(string, ...interface{}) {}})
	seen, stop := watchAll(b)
	defer stop()

	b.Announce()
	if got := seen(); len(got) != 0 {
		t.Fatalf("a store-less bus announced %d fact(s)", len(got))
	}
}

// Compaction is driven from here — the bus decides which names are compactable
// and where the floor is; the store only runs the SQL. Trim reports both.
func TestTrimCompactsTheNamesTheBusDeclared(t *testing.T) {
	s := newMemStore()
	b := New(Options{
		Store:       s,
		Log:         func(string, ...interface{}) {},
		Compactable: []string{"document.changed"},
		Retention:   time.Hour,
	})

	now := time.Now()
	for range 10 {
		s.appendOutOfBand("document.changed", "ext/x/requests/a", now)
	}
	for range 3 {
		s.appendOutOfBand("session.state.changed", "sess-1", now)
	}

	removed := b.Trim()
	if removed != 9 {
		t.Fatalf("trim removed %d fact(s), want the 9 redundant ones", removed)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Rows != 4 {
		t.Fatalf("log holds %d row(s) after compaction, want 4", status.Rows)
	}
	if status.Bytes <= 0 {
		t.Fatalf("a non-empty log reports %d byte(s)", status.Bytes)
	}
}

// A bus that declared nothing compactable compacts nothing, however much churn
// the log holds. Compaction is opt-in per fact class because it is only sound
// for facts that are pure invalidations.
func TestTrimCompactsNothingWhenNoNameWasDeclared(t *testing.T) {
	s := newMemStore()
	b := New(Options{Store: s, Log: func(string, ...interface{}) {}, Retention: time.Hour})

	now := time.Now()
	for range 10 {
		s.appendOutOfBand("document.changed", "ext/x/requests/a", now)
	}

	if removed := b.Trim(); removed != 0 {
		t.Fatalf("a bus with no compactable names removed %d fact(s)", removed)
	}
	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Rows != 10 {
		t.Fatalf("log holds %d row(s), want all 10", status.Rows)
	}
}
