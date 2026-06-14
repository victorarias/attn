package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// promptEcho is a stub whose result is the literal prompt, so downstream prompts
// that embed an upstream result change deterministically when the upstream changes.
func promptEcho(_ OrdinalPath, prompt string, _ json.RawMessage) (json.RawMessage, error) {
	b, _ := json.Marshal(prompt)
	return b, nil
}

// TestResumeR1IdenticalIsFullCacheHit: Run then Resume with identical script+args
// yields 100% cache hits and zero live calls (the guaranteed case).
func TestResumeR1IdenticalIsFullCacheHit(t *testing.T) {
	script := `
		const a = await agent("alpha");
		const b = await agent("beta:" + a);
		const c = await agent("gamma:" + b);
		return [a, b, c];
	`
	jour := NewMemJournal()
	eng := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r1, err := eng.Run(context.Background(), script, map[string]any{"k": 1})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if r1.LiveCalls != 3 || r1.CachedCalls != 0 {
		t.Fatalf("fresh run: live=%d cached=%d, want 3/0", r1.LiveCalls, r1.CachedCalls)
	}

	eng2 := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), script, map[string]any{"k": 1})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r2.LiveCalls != 0 || r2.CachedCalls != 3 {
		t.Fatalf("resume identical: live=%d cached=%d, want 0/3", r2.LiveCalls, r2.CachedCalls)
	}
	// Replayed result equals the original.
	if !sameJSON(r1.Value, r2.Value) {
		t.Errorf("replayed value differs: %v vs %v", r1.Value, r2.Value)
	}
}

// TestResumeR1PostAwaitPipelineIsFullCacheHit guards R-spec R1 for the load-bearing
// post-await case: a pipeline whose stage callback issues an agent() AFTER its own
// internal await. With temporal ordinals, resume would assign a logical call a
// different ordinal than the original run whenever subagent timing differed, miss
// the journal, trip the divergence latch, and re-run live (paid) calls despite an
// identical script. Run several trials so a scheduling-dependent collision surfaces.
func TestResumeR1PostAwaitPipelineIsFullCacheHit(t *testing.T) {
	script := `
		const out = await pipeline(["X", "Y", "Z"], async (v, item, i) => {
			const r1 = await agent("a:" + item);
			return await agent("b:" + r1);
		});
		return out;
	`
	for trial := 0; trial < 10; trial++ {
		jour := NewMemJournal()
		eng := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
		r1, err := eng.Run(context.Background(), script, nil)
		if err != nil {
			t.Fatalf("trial %d run: %v", trial, err)
		}
		if r1.LiveCalls != 6 || r1.CachedCalls != 0 {
			t.Fatalf("trial %d fresh run: live=%d cached=%d, want 6/0", trial, r1.LiveCalls, r1.CachedCalls)
		}

		eng2 := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
		r2, err := eng2.Resume(context.Background(), script, nil)
		if err != nil {
			t.Fatalf("trial %d resume: %v", trial, err)
		}
		if r2.LiveCalls != 0 || r2.CachedCalls != 6 {
			t.Fatalf("trial %d resume identical post-await: live=%d cached=%d, want 0/6 (R1 violated by a temporal ordinal)", trial, r2.LiveCalls, r2.CachedCalls)
		}
		if !sameJSON(r1.Value, r2.Value) {
			t.Fatalf("trial %d replayed value differs: %v vs %v", trial, r1.Value, r2.Value)
		}
	}
}

// TestResumeR3DownstreamEditCachesUpstream: editing only a LATER call's prompt
// keeps the upstream prefix cached; only the edited call onward runs live.
func TestResumeR3DownstreamEditCachesUpstream(t *testing.T) {
	orig := `
		const a = await agent("alpha");
		const b = await agent("beta");
		const c = await agent("gamma");
		return [a, b, c];
	`
	// Edit only the LAST call's prompt. a and b ordinals + prompts unchanged.
	edited := `
		const a = await agent("alpha");
		const b = await agent("beta");
		const c = await agent("gamma-EDITED");
		return [a, b, c];
	`
	jour := NewMemJournal()
	eng := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	if _, err := eng.Run(context.Background(), orig, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	eng2 := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), edited, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// a, b cached; only c live.
	if r2.CachedCalls != 2 || r2.LiveCalls != 1 {
		t.Fatalf("downstream edit: cached=%d live=%d, want 2/1", r2.CachedCalls, r2.LiveCalls)
	}
}

// TestResumeR2UpstreamEditInvalidatesDownstream: editing an UPSTREAM call's prompt
// invalidates that call and everything structurally after it — transitively, even
// though the downstream prompts embed the upstream result and therefore also
// change. We assert the upstream prefix before the edit is still cached.
func TestResumeR2UpstreamEditInvalidatesDownstream(t *testing.T) {
	orig := `
		const x = await agent("x-input");
		const a = await agent("a-prompt");
		const b = await agent("b:" + a);
		return [x, a, b];
	`
	// Edit the SECOND call ("a-prompt"). The first call ("x-input") is untouched
	// and must stay cached; a and b (b's prompt embeds a) run live.
	edited := `
		const x = await agent("x-input");
		const a = await agent("a-prompt-EDITED");
		const b = await agent("b:" + a);
		return [x, a, b];
	`
	jour := NewMemJournal()
	eng := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	if _, err := eng.Run(context.Background(), orig, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	eng2 := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), edited, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// x cached (1); a edited + b downstream live (2).
	if r2.CachedCalls != 1 || r2.LiveCalls != 2 {
		t.Fatalf("upstream edit: cached=%d live=%d, want 1/2", r2.CachedCalls, r2.LiveCalls)
	}
}

// TestResumeR5SchemaChangeInvalidates: changing a call's schema (even with an
// identical prompt) flips schemaHash and re-runs that call onward. We simulate a
// schema change by directly tampering with the journal entry's SchemaHash to a
// non-"none" value, then resuming the unchanged (schema-less) script — the call's
// recomputed schemaHash ("none") won't match, so it must run live.
func TestResumeR5SchemaChangeInvalidates(t *testing.T) {
	script := `
		const a = await agent("first");
		const b = await agent("second");
		return [a, b];
	`
	jour := NewMemJournal()
	eng := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	if _, err := eng.Run(context.Background(), script, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	entries := jour.Entries()
	if len(entries) != 2 {
		t.Fatalf("want 2 journaled, got %d", len(entries))
	}
	// Tamper: give the FIRST call a non-none schemaHash, as if it originally had a
	// schema that the (schema-less) resume script no longer matches.
	first := entries[0]
	first.SchemaHash = hashSchema(json.RawMessage(`{"type":"object"}`))
	jour.Upsert(first)

	eng2 := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// The first call's schemaHash mismatches -> it is the divergence boundary ->
	// it and the second call run live; nothing cached.
	if r2.CachedCalls != 0 || r2.LiveCalls != 2 {
		t.Fatalf("schema change at first call: cached=%d live=%d, want 0/2", r2.CachedCalls, r2.LiveCalls)
	}
}

// TestResumeR4ArgsChange: a call that embeds args changes its prompt when args
// change and runs live; a call that ignores args stays cached up to that boundary.
func TestResumeR4ArgsChange(t *testing.T) {
	script := `
		const a = await agent("static-prompt");
		const b = await agent("uses-args:" + args.tag);
		return [a, b];
	`
	jour := NewMemJournal()
	eng := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	if _, err := eng.Run(context.Background(), script, map[string]any{"tag": "v1"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	eng2 := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), script, map[string]any{"tag": "v2"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// a ignores args -> cached; b embeds args.tag -> prompt changed -> live.
	if r2.CachedCalls != 1 || r2.LiveCalls != 1 {
		t.Fatalf("args change: cached=%d live=%d, want 1/1", r2.CachedCalls, r2.LiveCalls)
	}
}

// TestKillAtKThenResume: a stub that errors on the (k+1)-th call interrupts the
// run after k journaled calls; Resume replays the k cached calls and runs the rest
// live, producing the full result.
func TestKillAtKThenResume(t *testing.T) {
	script := `
		const a = await agent("one");
		const b = await agent("two");
		const c = await agent("three");
		const d = await agent("four");
		return [a, b, c, d];
	`
	// Run 1: a stub that returns an error on the 3rd prompt (and beyond) so the
	// run journals 'one' and 'two' as ok, then the third resolves null (errored).
	// We then *truncate* the journal to only the first two ok entries to simulate a
	// kill@2 (the prior run died before journaling 'three').
	jour := NewMemJournal()
	eng := New(Config{Journal: jour, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r1, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if r1.LiveCalls != 4 {
		t.Fatalf("fresh run live=%d want 4", r1.LiveCalls)
	}

	// Simulate kill@2: keep only the first two journal entries.
	killed := NewMemJournal()
	full := jour.Entries()
	for _, e := range full[:2] {
		_ = killed.Append(e)
	}

	eng2 := New(Config{Journal: killed, Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r2.CachedCalls != 2 || r2.LiveCalls != 2 {
		t.Fatalf("kill@2 resume: cached=%d live=%d, want 2/2", r2.CachedCalls, r2.LiveCalls)
	}
	// Full result reconstructed: identical to the original.
	if !sameJSON(r1.Value, r2.Value) {
		t.Errorf("resumed value %v differs from original %v", r2.Value, r1.Value)
	}
	if r2.Status != StatusCompleted {
		t.Fatalf("resume status=%s err=%v", r2.Status, r2.Err)
	}
}

// TestAgentTerminalFailureResolvesNull: an agent() whose stub returns an error
// resolves to null (never rejects the script); the run completes.
func TestAgentTerminalFailureResolvesNull(t *testing.T) {
	failOnSecond := StubFunc(func(_ OrdinalPath, prompt string, _ json.RawMessage) (json.RawMessage, error) {
		if strings.Contains(prompt, "boom") {
			return nil, errors.New("subagent crashed")
		}
		b, _ := json.Marshal("ok:" + prompt)
		return b, nil
	})
	script := `
		const a = await agent("fine");
		const b = await agent("boom");
		return { a: a, bIsNull: b === null };
	`
	eng := New(Config{Stub: failOnSecond, WatchdogTimeout: 5 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("run errored unexpectedly: %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v, want completed (agent failure must not reject)", res.Status, res.Err)
	}
	obj, ok := res.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("value not an object: %#v", res.Value)
	}
	if obj["bIsNull"] != true {
		t.Errorf("failed agent should resolve null, got bIsNull=%v", obj["bIsNull"])
	}
	// The errored call is journaled with status "errored" and a null result.
	var erroredSeen bool
	for _, e := range res.Journal.Entries() {
		if e.Status == "errored" {
			erroredSeen = true
			if len(e.Result) != 0 {
				t.Errorf("errored entry should have null result, got %s", string(e.Result))
			}
		}
	}
	if !erroredSeen {
		t.Errorf("expected an errored journal entry")
	}
}

// TestParallelThrowingThunkNullSlot: a throwing thunk yields a null slot and the
// parallel call resolves (never rejects).
func TestParallelThrowingThunkNullSlot(t *testing.T) {
	script := `
		const out = await parallel([
			() => agent("ok0"),
			() => { throw new Error("thunk blew up"); },
			() => agent("ok2"),
		]);
		return { len: out.length, slot1Null: out[1] === null, slot0: out[0], slot2: out[2] };
	`
	eng := New(Config{Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("parallel rejected (should never reject): %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	obj := res.Value.(map[string]interface{})
	if obj["slot1Null"] != true {
		t.Errorf("throwing thunk should give null slot, got %v", obj["slot1Null"])
	}
	if obj["slot0"] != "ok0" || obj["slot2"] != "ok2" {
		t.Errorf("non-throwing slots wrong: %v %v", obj["slot0"], obj["slot2"])
	}
}

// TestPipelineThrowingStageDropsItem: a throwing stage drops that item to null for
// the remaining stages; other items flow through; pipeline never rejects.
func TestPipelineThrowingStageDropsItem(t *testing.T) {
	script := `
		const out = await pipeline(["keep", "drop"],
			(v, item, i) => {
				if (item === "drop") throw new Error("stage failed for drop");
				return agent("s0:" + item);
			},
			(v, item, i) => agent("s1:" + v));
		return { item0: out[0], item1IsNull: out[1] === null };
	`
	eng := New(Config{Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("pipeline rejected (should never reject): %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	obj := res.Value.(map[string]interface{})
	if obj["item1IsNull"] != true {
		t.Errorf("dropped item should be null, got %v", obj["item1IsNull"])
	}
	if obj["item0"] != "s1:s0:keep" {
		t.Errorf("kept item wrong: %v, want s1:s0:keep", obj["item0"])
	}
}

// TestPipelineStageSignature: the stage cb receives (prevResult, originalItem, index).
func TestPipelineStageSignature(t *testing.T) {
	script := `
		const out = await pipeline(["A", "B"],
			(prev, item, i) => agent("first|prev=" + prev + "|item=" + item + "|i=" + i),
			(prev, item, i) => agent("second|item=" + item + "|i=" + i));
		return out;
	`
	eng := New(Config{Stub: StubFunc(promptEcho), WatchdogTimeout: 5 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Stage 0 prev is the original item; index threads through; item is preserved.
	entries := res.Journal.Entries()
	var sawFirstA, sawSecondB bool
	for _, e := range entries {
		var s string
		_ = json.Unmarshal(e.Result, &s)
		if s == "first|prev=A|item=A|i=0" {
			sawFirstA = true
		}
		if s == "second|item=B|i=1" {
			sawSecondB = true
		}
	}
	if !sawFirstA {
		t.Errorf("stage0 did not receive (originalItem A, index 0); entries=%v", entries)
	}
	if !sawSecondB {
		t.Errorf("stage1 did not receive (originalItem B, index 1); entries=%v", entries)
	}
}

// sameJSON compares two exported Go values by their JSON encoding.
func sameJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
