package store

import (
	"fmt"
	"testing"
	"time"
)

// Fact-class compaction: a log of pure invalidations must not grow with how
// often a subject changed, only with how many subjects there are. These pin the
// two halves of that — what it removes, and what it must never remove.

func changeOf(subject string) BusEvent {
	return BusEvent{Name: "document.changed", Subject: subject, Payload: `{}`, Source: "test"}
}

func seqsOnLog(t *testing.T, s *Store) []int64 {
	t.Helper()
	events, err := s.BusEventsSince(0, 100000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	seqs := make([]int64, 0, len(events))
	for _, e := range events {
		seqs = append(seqs, e.Seq)
	}
	return seqs
}

func headOf(t *testing.T, s *Store) int64 {
	t.Helper()
	_, head, err := s.BusBounds()
	if err != nil {
		t.Fatalf("bounds: %v", err)
	}
	return head
}

// The point of the whole mechanism: a document churned N times leaves one fact,
// and it is the newest one — the only one that still says anything, since the
// state itself lives in the table.
func TestChurningOneSubjectLeavesOneFact(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	var last int64
	for i := range 200 {
		seq, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		last = seq
	}

	removed, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s))
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if removed != 199 {
		t.Fatalf("compaction removed %d fact(s), want 199", removed)
	}
	if got := seqsOnLog(t, s); len(got) != 1 || got[0] != last {
		t.Fatalf("log holds %v, want only the newest fact at %d", got, last)
	}
}

// Compaction is per subject, not per name: two documents churning at the same
// time keep one fact each, and a fact of an uncompactable name is untouched
// however many of them share a subject.
func TestCompactionKeepsTheNewestOfEverySubjectItTouches(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	newest := map[string]int64{}
	for i := range 30 {
		for _, subject := range []string{"app/x/requests/a", "app/x/requests/b"} {
			seq, err := s.AppendBusEvent(changeOf(subject), now.Add(time.Duration(i)*time.Second))
			if err != nil {
				t.Fatalf("append: %v", err)
			}
			newest[subject] = seq
		}
		// A fact nobody declared compactable, on a subject that also carries
		// compactable ones — the name is what decides, not the subject.
		if _, err := s.AppendBusEvent(BusEvent{
			Name: "session.state.changed", Subject: "app/x/requests/a", Source: "test",
		}, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	if _, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s)); err != nil {
		t.Fatalf("compact: %v", err)
	}

	events, err := s.BusEventsSince(0, 100000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	changes, others := map[string]int64{}, 0
	for _, e := range events {
		if e.Name == "document.changed" {
			if _, dup := changes[e.Subject]; dup {
				t.Fatalf("subject %s kept more than one fact", e.Subject)
			}
			changes[e.Subject] = e.Seq
			continue
		}
		others++
	}
	if len(changes) != 2 {
		t.Fatalf("compaction left facts for %d subject(s), want 2", len(changes))
	}
	for subject, seq := range changes {
		if seq != newest[subject] {
			t.Fatalf("subject %s kept seq %d, want the newest at %d", subject, seq, newest[subject])
		}
	}
	if others != 30 {
		t.Fatalf("compaction touched an unnamed fact class: %d survived, want 30", others)
	}
}

// The floor is the safety property. A consumer parked below it has not read what
// sits above it, so nothing above the floor may go — even facts that compaction
// would otherwise consider redundant. Without this a lagging consumer would
// wake to a log missing the very changes it was behind on.
func TestAConsumerParkedBelowTheFloorPinsEveryFactItHasNotRead(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	var seqs []int64
	for i := range 10 {
		seq, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		seqs = append(seqs, seq)
	}

	// Read the first three, then stop — a consumer that is behind, not gone.
	floor := seqs[2]
	if err := s.SaveBusConsumer(BusConsumer{Name: "slowpoke", Cursor: floor, Enabled: true}, now); err != nil {
		t.Fatalf("registering the consumer: %v", err)
	}

	if _, err := s.CompactBusEvents([]string{"document.changed"}, floor); err != nil {
		t.Fatalf("compact: %v", err)
	}

	got := seqsOnLog(t, s)
	// Everything it has not read survives, in order. What it already read goes
	// entirely rather than collapsing to one, because the surviving newest fact
	// about the subject sits above the floor — a consumer reading forward from
	// here still sees every change it was behind on.
	want := seqs[3:]
	if len(got) != len(want) {
		t.Fatalf("log holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("log holds %v, want %v", got, want)
		}
	}
}

// A caller that named no compactable class asked for nothing, which is not the
// same as asking for everything. Getting this backwards would empty the log.
func TestCompactingNoNamesRemovesNothing(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	for range 5 {
		if _, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	removed, err := s.CompactBusEvents(nil, headOf(t, s))
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if removed != 0 || len(seqsOnLog(t, s)) != 5 {
		t.Fatalf("compacting no names removed %d fact(s), leaving %d", removed, len(seqsOnLog(t, s)))
	}
}

// The property that says the mechanism actually bounds growth, stated the way it
// has to hold rather than as a count the fixture happens to produce: after a
// churning workload the log is no larger than the live documents plus the
// removals still in the window. It is an inequality because a document may be
// created and deleted with both facts collapsing to the tombstone, and because
// nothing forces every live document to have been written at all.
func TestACompactedLogIsNoLargerThanWhatItDescribes(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	const documents = 20
	live := map[string]bool{}
	tombstones := map[string]bool{}
	step := 0
	appendChange := func(id string, deleted bool) {
		step++
		if _, err := s.AppendBusEvent(changeOf("app/x/requests/"+id), now.Add(time.Duration(step)*time.Second)); err != nil {
			t.Fatalf("append: %v", err)
		}
		if deleted {
			delete(live, id)
			tombstones[id] = true
			return
		}
		live[id] = true
		delete(tombstones, id)
	}

	// Create everything, rewrite each a few times, then delete a third: the
	// mixed workload, not a single-shaped one.
	for i := range documents {
		appendChange(fmt.Sprintf("d%02d", i), false)
	}
	for range 8 {
		for i := range documents {
			appendChange(fmt.Sprintf("d%02d", i), false)
		}
	}
	for i := 0; i < documents; i += 3 {
		appendChange(fmt.Sprintf("d%02d", i), true)
	}

	before := len(seqsOnLog(t, s))
	if _, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	after := len(seqsOnLog(t, s))

	if bound := len(live) + len(tombstones); after > bound {
		t.Fatalf("compacted log holds %d fact(s) for %d live document(s) and %d tombstone(s)",
			after, len(live), len(tombstones))
	}
	if after >= before {
		t.Fatalf("compaction of %d fact(s) about %d document(s) removed nothing", before, documents)
	}
}

// `attn bus status` reports the log's weight, so the numbers have to move with
// the log rather than being a constant nobody notices is wrong.
func TestTheLogReportsItsOwnWeight(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	rows, bytes, err := s.BusLogSize()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if rows != 0 || bytes != 0 {
		t.Fatalf("an empty log weighs %d row(s), %d byte(s)", rows, bytes)
	}

	for range 4 {
		if _, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	rows, bytes, err = s.BusLogSize()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if rows != 4 {
		t.Fatalf("log holds %d row(s), want 4", rows)
	}
	one := changeOf("app/x/requests/a")
	if want := int64(4 * (len(one.Name) + len(one.Subject) + len(one.Payload) + len(one.Source))); bytes <= want {
		t.Fatalf("4 facts weigh %d byte(s), want more than the %d of their own text", bytes, want)
	}

	if _, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	compacted, compactedBytes, err := s.BusLogSize()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if compacted != 1 || compactedBytes >= bytes {
		t.Fatalf("after compaction the log reports %d row(s), %d byte(s); was %d/%d",
			compacted, compactedBytes, rows, bytes)
	}
}
