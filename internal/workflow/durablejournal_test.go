package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// journalFactory drives a resume scenario over one Journal implementation. The
// abstraction lets the SAME R-spec scripts run over both MemJournal and the
// SQLite-backed DurableJournal so the two can never silently diverge.
//
//   - fresh() returns the journal used for the first Run.
//   - reopen() returns the journal handed to Resume. For MemJournal this is the
//     same in-memory object (today's behavior). For DurableJournal this is a FRESH
//     adapter rebuilt from SQLite alone — discarding the prior in-memory mirror —
//     which is what proves durable persistence and append-order reconstruction.
//   - seedPrefix(entries, n) returns a resume journal containing only the first n
//     entries, for the kill@k scenario. For MemJournal it appends a prefix into a
//     new journal; for DurableJournal it wipes the store and rewrites the prefix,
//     then rebuilds a fresh adapter from the persisted prefix.
type journalFactory struct {
	name       string
	fresh      func() Journal
	reopen     func() Journal
	seedPrefix func(entries []JournalEntry, n int) Journal
}

func memFactory() *journalFactory {
	var jour *MemJournal
	return &journalFactory{
		name: "mem",
		fresh: func() Journal {
			jour = NewMemJournal()
			return jour
		},
		reopen: func() Journal {
			return jour
		},
		seedPrefix: func(entries []JournalEntry, n int) Journal {
			seeded := NewMemJournal()
			for _, e := range entries[:n] {
				_ = seeded.Append(e)
			}
			return seeded
		},
	}
}

func durableFactory(t *testing.T) *journalFactory {
	t.Helper()
	s := store.New()
	const runID = "run-parity"
	// The run row is not strictly required (no enforced FK), but persisting one
	// mirrors real usage where a run owns its calls.
	if err := s.UpsertWorkflowRun(&store.WorkflowRunRow{
		RunID:      runID,
		ScriptPath: "/parity.js",
		ScriptHash: "h",
		Status:     "running",
		CreatedAt:  "2026-06-14T10:00:00Z",
		UpdatedAt:  "2026-06-14T10:00:00Z",
	}); err != nil {
		t.Fatalf("seed run row: %v", err)
	}
	return &journalFactory{
		name: "durable",
		fresh: func() Journal {
			return NewDurableJournal(s, runID)
		},
		reopen: func() Journal {
			// Fresh adapter, rebuilt from SQLite only — proves persistence.
			return NewDurableJournal(s, runID)
		},
		seedPrefix: func(entries []JournalEntry, n int) Journal {
			// Wipe persisted calls and rewrite only the prefix, then rebuild a
			// fresh adapter from the persisted prefix.
			if err := s.DeleteWorkflowRun(runID); err != nil {
				t.Fatalf("wipe calls: %v", err)
			}
			if err := s.UpsertWorkflowRun(&store.WorkflowRunRow{
				RunID:      runID,
				ScriptPath: "/parity.js",
				ScriptHash: "h",
				Status:     "running",
				CreatedAt:  "2026-06-14T10:00:00Z",
				UpdatedAt:  "2026-06-14T10:00:00Z",
			}); err != nil {
				t.Fatalf("re-seed run row: %v", err)
			}
			prefix := NewDurableJournal(s, runID)
			for _, e := range entries[:n] {
				if err := prefix.Append(e); err != nil {
					t.Fatalf("seedPrefix append: %v", err)
				}
			}
			return NewDurableJournal(s, runID)
		},
	}
}

func factories(t *testing.T) []*journalFactory {
	return []*journalFactory{memFactory(), durableFactory(t)}
}

// runResume runs script1 from scratch, then resumes with script2 (same engine
// config), returning both results. The journal under test seeds Run via fresh()
// and Resume via reopen().
func runResume(t *testing.T, f *journalFactory, script1, script2 string, args1, args2 any) (RunResult, RunResult) {
	t.Helper()
	eng := New(Config{Journal: f.fresh(), Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r1, err := eng.Run(context.Background(), script1, args1)
	if err != nil {
		t.Fatalf("[%s] run: %v", f.name, err)
	}
	eng2 := New(Config{Journal: f.reopen(), Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), script2, args2)
	if err != nil {
		t.Fatalf("[%s] resume: %v", f.name, err)
	}
	return r1, r2
}

// TestJournalParityR1Identical: R1 over both impls — identical script+args yields
// 100% cache hits and zero live calls.
func TestJournalParityR1Identical(t *testing.T) {
	script := `
		const a = await agent("alpha");
		const b = await agent("beta:" + a);
		const c = await agent("gamma:" + b);
		return [a, b, c];
	`
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			r1, r2 := runResume(t, f, script, script, map[string]any{"k": 1}, map[string]any{"k": 1})
			if r1.LiveCalls != 3 || r1.CachedCalls != 0 {
				t.Fatalf("fresh run: live=%d cached=%d, want 3/0", r1.LiveCalls, r1.CachedCalls)
			}
			if r2.LiveCalls != 0 || r2.CachedCalls != 3 {
				t.Fatalf("resume identical: live=%d cached=%d, want 0/3", r2.LiveCalls, r2.CachedCalls)
			}
			if !sameJSON(r1.Value, r2.Value) {
				t.Fatalf("replayed value differs: %v vs %v", r1.Value, r2.Value)
			}
		})
	}
}

// TestJournalParityR2UpstreamEdit: editing an upstream prompt invalidates it and
// everything structurally after; the prefix before stays cached.
func TestJournalParityR2UpstreamEdit(t *testing.T) {
	orig := `
		const x = await agent("x-input");
		const a = await agent("a-prompt");
		const b = await agent("b:" + a);
		return [x, a, b];
	`
	edited := `
		const x = await agent("x-input");
		const a = await agent("a-prompt-EDITED");
		const b = await agent("b:" + a);
		return [x, a, b];
	`
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			_, r2 := runResume(t, f, orig, edited, nil, nil)
			if r2.CachedCalls != 1 || r2.LiveCalls != 2 {
				t.Fatalf("upstream edit: cached=%d live=%d, want 1/2", r2.CachedCalls, r2.LiveCalls)
			}
		})
	}
}

// TestJournalParityR3DownstreamEdit: editing only a later prompt keeps the upstream
// prefix cached; only the edited call onward runs live.
func TestJournalParityR3DownstreamEdit(t *testing.T) {
	orig := `
		const a = await agent("alpha");
		const b = await agent("beta");
		const c = await agent("gamma");
		return [a, b, c];
	`
	edited := `
		const a = await agent("alpha");
		const b = await agent("beta");
		const c = await agent("gamma-EDITED");
		return [a, b, c];
	`
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			_, r2 := runResume(t, f, orig, edited, nil, nil)
			if r2.CachedCalls != 2 || r2.LiveCalls != 1 {
				t.Fatalf("downstream edit: cached=%d live=%d, want 2/1", r2.CachedCalls, r2.LiveCalls)
			}
		})
	}
}

// TestJournalParityR4ArgsChange: a call that embeds args runs live when args change;
// an args-independent call stays cached.
func TestJournalParityR4ArgsChange(t *testing.T) {
	script := `
		const a = await agent("static-prompt");
		const b = await agent("uses-args:" + args.tag);
		return [a, b];
	`
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			_, r2 := runResume(t, f, script, script, map[string]any{"tag": "v1"}, map[string]any{"tag": "v2"})
			if r2.CachedCalls != 1 || r2.LiveCalls != 1 {
				t.Fatalf("args change: cached=%d live=%d, want 1/1", r2.CachedCalls, r2.LiveCalls)
			}
		})
	}
}

// TestJournalParityR5SchemaChange: tampering the first entry's schemaHash (as if it
// originally had a schema the resume script no longer matches) makes it the
// divergence boundary. Over the durable arm this also proves the schemaHash column
// round-trips, since the tampered hash must survive the store write/read.
func TestJournalParityR5SchemaChange(t *testing.T) {
	script := `
		const a = await agent("first");
		const b = await agent("second");
		return [a, b];
	`
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			eng := New(Config{Journal: f.fresh(), Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
			r1, err := eng.Run(context.Background(), script, nil)
			if err != nil {
				t.Fatalf("[%s] run: %v", f.name, err)
			}
			entries := r1.Journal.Entries()
			if len(entries) != 2 {
				t.Fatalf("want 2 journaled, got %d", len(entries))
			}
			// Tamper the FIRST entry's schemaHash via Upsert on the SAME run journal,
			// then reopen for resume. For durable, Upsert writes through to SQLite so
			// reopen() reads the tampered hash back.
			first := entries[0]
			first.SchemaHash = hashSchema(json.RawMessage(`{"type":"object"}`))
			r1.Journal.Upsert(first)

			eng2 := New(Config{Journal: f.reopen(), Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
			r2, err := eng2.Resume(context.Background(), script, nil)
			if err != nil {
				t.Fatalf("[%s] resume: %v", f.name, err)
			}
			if r2.CachedCalls != 0 || r2.LiveCalls != 2 {
				t.Fatalf("schema change at first call: cached=%d live=%d, want 0/2", r2.CachedCalls, r2.LiveCalls)
			}
		})
	}
}

// TestJournalParityKillAtK: a kill after k journaled calls (here k=2) replays the
// persisted prefix and runs the divergent tail live. For the durable arm the
// surviving rows come from SQLite via a fresh adapter — proving the partial run's
// write-through survives a process restart.
func TestJournalParityKillAtK(t *testing.T) {
	script := `
		const a = await agent("one");
		const b = await agent("two");
		const c = await agent("three");
		const d = await agent("four");
		return [a, b, c, d];
	`
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			eng := New(Config{Journal: f.fresh(), Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
			r1, err := eng.Run(context.Background(), script, nil)
			if err != nil {
				t.Fatalf("[%s] run: %v", f.name, err)
			}
			if r1.LiveCalls != 4 {
				t.Fatalf("fresh run live=%d want 4", r1.LiveCalls)
			}

			// Kill@2: resume from only the first two persisted entries.
			killed := f.seedPrefix(r1.Journal.Entries(), 2)
			eng2 := New(Config{Journal: killed, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
			r2, err := eng2.Resume(context.Background(), script, nil)
			if err != nil {
				t.Fatalf("[%s] resume: %v", f.name, err)
			}
			if r2.CachedCalls != 2 || r2.LiveCalls != 2 {
				t.Fatalf("kill@2 resume: cached=%d live=%d, want 2/2", r2.CachedCalls, r2.LiveCalls)
			}
			if !sameJSON(r1.Value, r2.Value) {
				t.Fatalf("resumed value %v differs from original %v", r2.Value, r1.Value)
			}
			if r2.Status != StatusCompleted {
				t.Fatalf("resume status=%s err=%v", r2.Status, r2.Err)
			}
		})
	}
}

// TestDurableJournalRoundTripLossless asserts the JournalEntry<->row mapping is
// field-for-field lossless for the six JournalEntry fields, including a null Result
// (errored entry) and the "none" schema sentinel.
func TestDurableJournalRoundTripLossless(t *testing.T) {
	cases := []JournalEntry{
		{Ordinal: "0", PromptHash: "ph0", SchemaHash: "none", Result: json.RawMessage(`"v0"`), Status: "ok"},
		{Ordinal: "1.2", PromptHash: "ph1", SchemaHash: "deadbeef", Result: nil, Status: "errored", Err: "subagent crashed"},
		{Ordinal: "2", PromptHash: "ph2", SchemaHash: "none", Result: nil, Status: "skipped"},
	}
	for _, in := range cases {
		out := entryFromRow(rowFromEntry("run-rt", in))
		if out.Ordinal != in.Ordinal || out.PromptHash != in.PromptHash ||
			out.SchemaHash != in.SchemaHash || out.Status != in.Status || out.Err != in.Err {
			t.Fatalf("scalar mismatch: in=%+v out=%+v", in, out)
		}
		if string(out.Result) != string(in.Result) {
			t.Fatalf("result mismatch: in=%q out=%q", string(in.Result), string(out.Result))
		}
		// Null result must round-trip to a nil RawMessage, not an empty non-nil one.
		if len(in.Result) == 0 && out.Result != nil {
			t.Fatalf("null result became non-nil: %v", out.Result)
		}
	}
}

// TestDurableJournalAppendRejectsDuplicate proves the one-entry-per-ordinal
// invariant matches MemJournal exactly (same error path).
func TestDurableJournalAppendRejectsDuplicate(t *testing.T) {
	s := store.New()
	const runID = "run-dup"
	dj := NewDurableJournal(s, runID)
	e := JournalEntry{Ordinal: "0", PromptHash: "p", SchemaHash: "none", Status: "ok", Result: json.RawMessage(`1`)}
	if err := dj.Append(e); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := dj.Append(e); err == nil {
		t.Fatal("duplicate append should error, got nil")
	}
}
