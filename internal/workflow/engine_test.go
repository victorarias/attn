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
func promptEcho(call AgentCall) (json.RawMessage, error) {
	b, _ := json.Marshal(call.Prompt)
	return b, nil
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

// TestAgentTerminalFailureResolvesNull: an agent() whose stub returns an error
// resolves to null (never rejects the script); the run completes.
func TestAgentTerminalFailureResolvesNull(t *testing.T) {
	failOnSecond := StubFunc(func(call AgentCall) (json.RawMessage, error) {
		if strings.Contains(call.Prompt, "boom") {
			return nil, errors.New("subagent crashed")
		}
		b, _ := json.Marshal("ok:" + call.Prompt)
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
