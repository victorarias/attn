package tasks

import "testing"

// TestTaskFilenamesAreInjective is a regression test for a storage-identity bug:
// the original filename scheme replaced "/", ":", and ".." with "_", so two
// distinct task ids that differed only in those runes addressed the SAME file and
// clobbered each other's record. Every pair below collided under that lossy
// scheme; each id must now persist and load back as its own distinct record.
func TestTaskFilenamesAreInjective(t *testing.T) {
	root := t.TempDir()
	s := newStore(root)

	pairs := [][2]string{
		{"k:a/b", "k:a_b"},   // "/" and "_" were both "_"
		{"k:a:b", "k:a__b"},  // ":" became "__"
		{"k:a..b", "k:a__b"}, // ".." became "__"
		{"compact_context:ws/1", "compact_context:ws_1"}, // realistic kind:subject
	}
	for _, p := range pairs {
		id1, id2 := p[0], p[1]
		if id1 == id2 {
			t.Fatalf("test bug: pair ids are equal %q", id1)
		}
		if taskFilename(id1) == taskFilename(id2) {
			t.Fatalf("filename collision: %q and %q both encode to %q", id1, id2, taskFilename(id1))
		}
		if err := s.save(&Task{ID: id1, Kind: "k", Subject: "s1"}); err != nil {
			t.Fatalf("save %q: %v", id1, err)
		}
		if err := s.save(&Task{ID: id2, Kind: "k", Subject: "s2"}); err != nil {
			t.Fatalf("save %q: %v", id2, err)
		}
		got1, err := s.load(id1)
		if err != nil || got1 == nil {
			t.Fatalf("load %q: got (%+v, %v), want a record", id1, got1, err)
		}
		got2, err := s.load(id2)
		if err != nil || got2 == nil {
			t.Fatalf("load %q: got (%+v, %v), want a record", id2, got2, err)
		}
		if got1.ID != id1 || got1.Subject != "s1" {
			t.Errorf("load %q returned the wrong record: %+v", id1, got1)
		}
		if got2.ID != id2 || got2.Subject != "s2" {
			t.Errorf("load %q returned the wrong record: %+v", id2, got2)
		}
	}
}

// TestLoadRejectsMismatchedRecord covers the defensive load-time check: a file
// whose stored id does not match the requested id is reported as "no record",
// never returned as the wrong task.
func TestLoadRejectsMismatchedRecord(t *testing.T) {
	root := t.TempDir()
	s := newStore(root)

	// Seed the on-disk path for "wanted" with a record carrying a different id,
	// simulating a hand-edit or a collision under some other encoding.
	if err := writeAtomic(taskPath(root, "wanted"), []byte(`{"id":"other","kind":"k","subject":"s"}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := s.load("wanted")
	if err != nil {
		t.Fatalf("load: unexpected error %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for a mismatched record, got %+v", got)
	}
}
