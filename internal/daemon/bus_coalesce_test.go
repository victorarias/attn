package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// A bulk operation publishes one fact per entity — that is what makes the facts
// useful — but the wire has always seen one whole-list push per operation.
// coalesceSnapshots is what keeps both true, so these pin the collapse.

func TestCoalesceSnapshotsCollapsesRepeatedPushes(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	d.coalesceSnapshots(func() {
		for _, id := range []string{"pr-1", "pr-2", "pr-3"} {
			d.publishFact(FactPRUpdated, id, nil)
		}
	})

	if got := trace.EventNames(); len(got) != 1 || got[0] != string(protocol.EventPRsUpdated) {
		t.Fatalf("three PR facts should collapse to one prs_updated, got %v", got)
	}
}

func TestCoalesceSnapshotsKeepsDistinctSnapshotsSeparate(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	// The order the wire sees is the order the snapshots were first touched, not
	// the order the facts arrived: the repo mute below is published last but its
	// list is the second one pushed only because a PR fact came first.
	d.coalesceSnapshots(func() {
		d.publishFact(FactPRHeatChanged, "pr-1", nil)
		d.publishFact(FactPRHeatChanged, "pr-2", nil)
		d.publishFact(FactRepoMuteChanged, "owner/repo", nil)
	})

	want := []string{string(protocol.EventPRsUpdated), string(protocol.EventReposUpdated)}
	got := trace.EventNames()
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestUncoalescedFactsPushOncePerFact(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	// Outside a coalesce block each fact pushes immediately. This is the shape
	// every single-entity mutation has, and it is why the collapse has to be
	// opt-in at the bulk call site rather than always on.
	d.publishFact(FactPRUpdated, "pr-1", nil)
	d.publishFact(FactPRUpdated, "pr-2", nil)

	if got := trace.Count(); got != 2 {
		t.Fatalf("want 2 pushes, got %d (%v)", got, trace.EventNames())
	}
}

func TestNestedCoalesceFlushesOnlyAtTheOuterBoundary(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	// Bulk producers call each other — a repo mute walks its PRs, each of which
	// is itself a mutation — so the depth counter, not the innermost block, has
	// to decide when to flush.
	d.coalesceSnapshots(func() {
		d.publishFact(FactPRUpdated, "pr-1", nil)
		d.coalesceSnapshots(func() {
			d.publishFact(FactPRUpdated, "pr-2", nil)
		})
		if got := trace.Count(); got != 0 {
			t.Fatalf("inner block flushed early: %v", trace.EventNames())
		}
	})

	if got := trace.Count(); got != 1 {
		t.Fatalf("want one push after the outer block, got %d (%v)", got, trace.EventNames())
	}
}
