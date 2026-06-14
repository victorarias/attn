package workflow

import (
	"encoding/json"
	"testing"
)

func TestIsCacheHitTruthTable(t *testing.T) {
	base := JournalEntry{Ordinal: "ord", PromptHash: "ph", SchemaHash: "sh", Status: "ok"}
	cases := []struct {
		name             string
		ord, prom, schem string
		want             bool
	}{
		{"all match", "ord", "ph", "sh", true},
		{"ordinal mismatch", "other", "ph", "sh", false},
		{"prompt mismatch", "ord", "other", "sh", false},
		{"schema mismatch", "ord", "ph", "other", false},
		{"ordinal+prompt mismatch", "other", "other", "sh", false},
		{"all mismatch", "x", "y", "z", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsCacheHit(base, tc.ord, tc.prom, tc.schem)
			if got != tc.want {
				t.Errorf("IsCacheHit=%v want %v", got, tc.want)
			}
		})
	}
}

func TestHashSchemaSentinel(t *testing.T) {
	// Absent and empty schema both hash to the stable "none" sentinel.
	if got := hashSchema(nil); got != schemaNoneSentinel {
		t.Errorf("nil schema hash = %q, want %q", got, schemaNoneSentinel)
	}
	if got := hashSchema(json.RawMessage{}); got != schemaNoneSentinel {
		t.Errorf("empty schema hash = %q, want %q", got, schemaNoneSentinel)
	}
	// A present schema hashes to something other than the sentinel (so add/remove
	// schema flips the hash — R5).
	present := hashSchema(json.RawMessage(`{"type":"object"}`))
	if present == schemaNoneSentinel {
		t.Errorf("present schema hashed to the none sentinel")
	}
	// Same schema bytes hash equal; different bytes hash different.
	if hashSchema(json.RawMessage(`{"a":1}`)) != hashSchema(json.RawMessage(`{"a":1}`)) {
		t.Errorf("identical schema produced different hashes")
	}
	if hashSchema(json.RawMessage(`{"a":1}`)) == hashSchema(json.RawMessage(`{"a":2}`)) {
		t.Errorf("different schemas produced the same hash")
	}
}

func TestHashPromptStable(t *testing.T) {
	if hashPrompt("hello") != hashPrompt("hello") {
		t.Errorf("identical prompt produced different hashes")
	}
	if hashPrompt("hello") == hashPrompt("world") {
		t.Errorf("different prompts produced the same hash")
	}
}

func TestMemJournalAppendUpsert(t *testing.T) {
	j := NewMemJournal()
	if err := j.Append(JournalEntry{Ordinal: "a", Status: "ok"}); err != nil {
		t.Fatalf("append a: %v", err)
	}
	if err := j.Append(JournalEntry{Ordinal: "b", Status: "ok"}); err != nil {
		t.Fatalf("append b: %v", err)
	}
	// Duplicate ordinal via Append must error (one entry per ordinal invariant).
	if err := j.Append(JournalEntry{Ordinal: "a", Status: "ok"}); err == nil {
		t.Errorf("Append of duplicate ordinal should error")
	}
	// Upsert overwrites in place, preserving order.
	j.Upsert(JournalEntry{Ordinal: "a", Status: "errored", Err: "boom"})
	got, ok := j.Lookup("a")
	if !ok || got.Status != "errored" {
		t.Errorf("upsert did not overwrite: %+v ok=%v", got, ok)
	}
	entries := j.Entries()
	if len(entries) != 2 || entries[0].Ordinal != "a" || entries[1].Ordinal != "b" {
		t.Errorf("order not preserved after upsert: %+v", entries)
	}
	// Upsert of a new ordinal appends.
	j.Upsert(JournalEntry{Ordinal: "c", Status: "ok"})
	if len(j.Entries()) != 3 {
		t.Errorf("upsert of new ordinal should append")
	}
}

func TestMemJournalClone(t *testing.T) {
	j := NewMemJournal()
	_ = j.Append(JournalEntry{Ordinal: "a", PromptHash: "p", Status: "ok"})
	clone := j.Clone()
	// Mutating the clone must not touch the original.
	clone.Upsert(JournalEntry{Ordinal: "a", PromptHash: "CHANGED", Status: "ok"})
	orig, _ := j.Lookup("a")
	if orig.PromptHash != "p" {
		t.Errorf("clone mutation leaked into original: %q", orig.PromptHash)
	}
}
