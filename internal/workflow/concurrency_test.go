package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// blockingStub is the E5 controllable AgentStub. Unlike DefaultStub (which
// returns instantly and therefore never actually exercises the concurrency
// semaphore) and ScriptedStub (which gates by ordinal, in a test-chosen ORDER),
// blockingStub holds every Run() goroutine inside the critical section — past the
// semaphore acquire in host.go's dispatch — until the test releases it. Because
// the engine acquires the semaphore BEFORE calling Run and releases it AFTER,
// every goroutine parked inside Run is holding a semaphore slot. The number of
// goroutines simultaneously inside Run is therefore the live in-flight count, and
// its high-water-mark is the observed max concurrency.
//
// It records:
//   - inFlight: goroutines currently inside Run (atomic, live).
//   - maxInFlight: high-water-mark of inFlight (atomic).
//   - calls: total Run invocations seen (atomic).
//
// resultFor must be a pure function of the prompt so replay/result assertions
// hold; the schema is ignored (these tests do not vary by schema).
type blockingStub struct {
	resultFor func(prompt string) json.RawMessage

	release chan struct{} // closed by the test to let every parked Run proceed

	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	calls       atomic.Int64
}

func newBlockingStub(resultFor func(prompt string) json.RawMessage) *blockingStub {
	return &blockingStub{
		resultFor: resultFor,
		release:   make(chan struct{}),
	}
}

func (s *blockingStub) Run(ctx context.Context, call AgentCall) (json.RawMessage, error) {
	s.calls.Add(1)
	cur := s.inFlight.Add(1)
	// Raise the high-water-mark to cur (monotonic CAS loop).
	for {
		hw := s.maxInFlight.Load()
		if cur <= hw || s.maxInFlight.CompareAndSwap(hw, cur) {
			break
		}
	}
	defer s.inFlight.Add(-1)
	select {
	case <-s.release:
		return s.resultFor(call.Prompt), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// releaseAll lets every currently-parked and future Run proceed.
func (s *blockingStub) releaseAll() { close(s.release) }

// waitForInFlight blocks until inFlight reaches want (the cap is fully saturated)
// or the deadline elapses. Returning true proves the cap was REACHED; the caller
// separately asserts it was never EXCEEDED via maxInFlight. Polling an atomic is
// the deterministic substitute for sleeping: we make a positive observation that
// exactly `want` goroutines are simultaneously inside Run, rather than guessing.
func (s *blockingStub) waitForInFlight(want int64, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.inFlight.Load() == want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func echoPrompt(prompt string) json.RawMessage {
	b, _ := json.Marshal("R:" + prompt)
	return b
}

// assertCapSaturatedAndBounded runs script (which must dispatch N live agents
// concurrently, N strictly greater than cap), proves the cap is both REACHED and
// never EXCEEDED, and returns the completed result for further assertions.
//
// Determinism: the run goroutine launches N agents whose Run() all park inside
// the stub. With a cap of `cap`, the semaphore admits exactly `cap` of them; the
// rest block on `rs.sem <- struct{}{}` and never enter Run. We poll until exactly
// `cap` are in flight (a positive observation, not a sleep), then release. As each
// finishes it frees a slot, admitting the next, so the high-water-mark can never
// exceed `cap`. If the semaphore were broken (e.g. unbounded), inFlight would
// blow past `cap` and maxInFlight would record it — failing the test.
func assertCapSaturatedAndBounded(t *testing.T, script string, capN, wantLive int) RunResult {
	t.Helper()
	stub := newBlockingStub(echoPrompt)
	eng := New(Config{
		Stub:            stub,
		ConcurrencyCap:  capN,
		WatchdogTimeout: 10 * time.Second,
	})

	done := make(chan RunResult, 1)
	go func() {
		r, _ := eng.Run(context.Background(), script, nil)
		done <- r
	}()

	// Wait until the cap is fully saturated: exactly `capN` goroutines inside Run.
	if !stub.waitForInFlight(int64(capN), 5*time.Second) {
		// Release so the run goroutine can unwind before we fail.
		stub.releaseAll()
		<-done
		t.Fatalf("cap never saturated: inFlight reached %d, want %d (semaphore not admitting up to the cap?)",
			stub.inFlight.Load(), capN)
	}

	// The cap is saturated. Now release everything and let the run finish.
	stub.releaseAll()
	r := <-done

	if r.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", r.Status, r.Err)
	}
	if got := stub.maxInFlight.Load(); got != int64(capN) {
		t.Fatalf("max in-flight = %d, want exactly the cap %d (cap was %s)",
			got, capN, capVerdict(got, int64(capN)))
	}
	if int(stub.calls.Load()) != wantLive {
		t.Fatalf("total live Run calls = %d, want %d (some agents never dispatched)", stub.calls.Load(), wantLive)
	}
	if r.LiveCalls != wantLive {
		t.Fatalf("LiveCalls = %d, want %d", r.LiveCalls, wantLive)
	}
	return r
}

func capVerdict(got, capN int64) string {
	if got > capN {
		return "EXCEEDED (semaphore failed to bound dispatch)"
	}
	return "not reached (cap under-utilized)"
}

// TestParallelConcurrencyReachesCapNeverExceeds dispatches N=12 parallel agents
// with the concurrency cap pinned to 3. It proves, under genuine goroutine
// concurrency with a blocking stub, that the live in-flight count REACHES exactly
// 3 and NEVER exceeds it, and that all 12 still complete with correct results.
// This closes E1's open issue: with DefaultStub returning instantly the semaphore
// was wired but its throughput bound was never observed under real contention.
func TestParallelConcurrencyReachesCapNeverExceeds(t *testing.T) {
	const n, capN = 12, 3
	script := fmt.Sprintf(`
		const thunks = [];
		for (let i = 0; i < %d; i++) {
			const k = i;
			thunks.push(() => agent("p" + k));
		}
		return await parallel(thunks);
	`, n)

	r := assertCapSaturatedAndBounded(t, script, capN, n)

	// All N slots resolved with their per-slot result; none null, none lost.
	out, ok := r.Value.([]interface{})
	if !ok || len(out) != n {
		t.Fatalf("parallel result = %#v, want %d-element slice", r.Value, n)
	}
	for i, v := range out {
		want := "R:p" + fmt.Sprint(i)
		if v != want {
			t.Errorf("slot %d = %v, want %q", i, v, want)
		}
	}
}

// TestPipelineConcurrencyReachesCapNeverExceeds is the pipeline analogue. A
// pipeline of N=12 items over a single stage dispatches all 12 stage-0 agents
// concurrently; with the cap at 3 the in-flight count reaches exactly 3 and never
// exceeds it, and all 12 items complete.
func TestPipelineConcurrencyReachesCapNeverExceeds(t *testing.T) {
	const n, capN = 12, 3
	script := fmt.Sprintf(`
		const items = [];
		for (let i = 0; i < %d; i++) items.push(i);
		return await pipeline(items, (prev, item, i) => agent("s" + item));
	`, n)

	r := assertCapSaturatedAndBounded(t, script, capN, n)

	out, ok := r.Value.([]interface{})
	if !ok || len(out) != n {
		t.Fatalf("pipeline result = %#v, want %d-element slice", r.Value, n)
	}
	for i, v := range out {
		want := "R:s" + fmt.Sprint(i)
		if v != want {
			t.Errorf("item %d = %v, want %q", i, v, want)
		}
	}
}

// raceStub resolves every call with a pure function of the prompt but with a
// nondeterministic real delay (a tiny randomized-by-the-runtime goroutine yield),
// so across runs the agents resolve in genuinely different real orders. Unlike
// ScriptedStub (which the test releases in a SCRIPTED order), nothing here
// dictates ordering — the Go scheduler does. This is the true-concurrency
// strengthening of E1's injected-reorder ordinal tests.
type raceStub struct {
	resultFor func(prompt string) json.RawMessage
}

func (s *raceStub) Run(_ context.Context, call AgentCall) (json.RawMessage, error) {
	// Yield to the scheduler so resolution order is genuinely nondeterministic
	// across runs and across goroutines. No sleep duration is prescribed; runtime
	// scheduling decides who finishes first.
	runtime.Gosched()
	return s.resultFor(call.Prompt), nil
}

// ordinalMapUnderRace runs script under a raceStub with a large concurrency cap
// (so dispatch is genuinely parallel) and returns the ordinal->result mapping.
func ordinalMapUnderRace(t *testing.T, script string, capN int) map[string]string {
	t.Helper()
	stub := &raceStub{resultFor: echoPrompt}
	eng := New(Config{Stub: stub, ConcurrencyCap: capN, WatchdogTimeout: 10 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("run error: %v (status=%s)", err, res.Status)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	out := map[string]string{}
	for _, e := range res.Journal.Entries() {
		var s string
		_ = json.Unmarshal(e.Result, &s)
		out[e.Ordinal] = s
	}
	return out
}

// TestOrdinalStabilityUnderGenuineConcurrency runs the same parallel+pipeline
// workflow many times under REAL goroutine concurrency (no scripted release
// order; the scheduler decides resolution order) and asserts the ordinal->result
// mapping — i.e. the journal cache map — is byte-identical across every run.
//
// This strengthens E1's TestParallelOrdinalStabilityUnderReorder /
// TestPipelinePostAwaitOrdinalStability, which inject a chosen resolution order
// via ScriptedStub. Here NOTHING scripts the order: with a wide concurrency cap
// and a yielding stub, agents resolve in whatever order the Go runtime picks, so
// each repetition is a fresh real race. If structural ordinals leaked any
// timing dependence, the maps would diverge between runs (and -race would also
// flag the shared-state access). Run under -race.
func TestOrdinalStabilityUnderGenuineConcurrency(t *testing.T) {
	// A mix that exercises every fan-out path concurrently: a parallel barrier of
	// post-await thunks, plus a pipeline whose stage callbacks issue post-await
	// agents. Post-await is the timing-sensitive case (the path stack is unwound at
	// the await), so it is the strongest probe for a temporal leak.
	script := `
		const mk = async (n) => {
			const r = await agent("x:" + n);
			return await agent("y:" + r);
		};
		const p = await parallel([ () => mk("0"), () => mk("1"), () => mk("2"), () => mk("3") ]);
		const q = await pipeline(["A", "B", "C"], async (v, item, i) => {
			const r1 = await agent("a:" + item);
			return await agent("b:" + r1);
		});
		return [p, q];
	`

	const capN = 8 // wide enough to dispatch the whole fan-out concurrently
	baseline := ordinalMapUnderRace(t, script, capN)
	if len(baseline) == 0 {
		t.Fatalf("baseline produced no journaled calls")
	}

	const repeats = 40
	for i := 0; i < repeats; i++ {
		got := ordinalMapUnderRace(t, script, capN)
		if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("ordinal->result map diverged on run %d under genuine concurrency:\n baseline=%s\n got=%s",
				i, dumpSorted(baseline), dumpSorted(got))
		}
	}
}

// dumpSorted renders an ordinal->result map deterministically for diffs.
func dumpSorted(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []byte
	for _, k := range keys {
		b = append(b, fmt.Sprintf("  %s -> %s\n", k, m[k])...)
	}
	return string(b)
}

// TestParallelNeverRejectsNullSlotUnderConcurrency proves the parallel
// never-reject / null-slot contract holds under GENUINE concurrent dispatch (not
// just the instant-stub path covered by TestParallelThrowingThunkNullSlot): a mix
// of throwing thunks, stub-error thunks, and good thunks all run concurrently; the
// barrier resolves, throwing/errored slots are null, good slots carry their value.
func TestParallelNeverRejectsNullSlotUnderConcurrency(t *testing.T) {
	// boomStub errors on prompts containing "boom" (terminal subagent failure ->
	// null slot, never reject); otherwise echoes. It yields so the good and bad
	// slots truly race.
	boomStub := StubFunc(func(call AgentCall) (json.RawMessage, error) {
		runtime.Gosched()
		if strings.Contains(call.Prompt, "boom") {
			return nil, fmt.Errorf("subagent crashed on %q", call.Prompt)
		}
		return echoPrompt(call.Prompt), nil
	})
	script := `
		const out = await parallel([
			() => agent("ok0"),
			() => { throw new Error("thunk threw"); },
			() => agent("boom2"),
			() => agent("ok3"),
			() => agent("boom4"),
			() => agent("ok5"),
		]);
		return out;
	`
	eng := New(Config{Stub: boomStub, ConcurrencyCap: 8, WatchdogTimeout: 10 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("parallel rejected under concurrency (must never reject): %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	out, ok := res.Value.([]interface{})
	if !ok || len(out) != 6 {
		t.Fatalf("result = %#v, want 6-element slice", res.Value)
	}
	want := []interface{}{"R:ok0", nil, nil, "R:ok3", nil, "R:ok5"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("slots = %#v, want %#v (throwing thunk + errored agents must be null slots)", out, want)
	}
}

// TestPipelineNeverRejectsNullItemUnderConcurrency is the pipeline analogue: under
// concurrent dispatch, a throwing stage and a stub-errored stage each drop their
// item to null for the rest of the pipeline, surviving items flow through both
// stages, and the pipeline never rejects.
func TestPipelineNeverRejectsNullItemUnderConcurrency(t *testing.T) {
	boomStub := StubFunc(func(call AgentCall) (json.RawMessage, error) {
		runtime.Gosched()
		if strings.Contains(call.Prompt, "boom") {
			return nil, fmt.Errorf("subagent crashed on %q", call.Prompt)
		}
		return echoPrompt(call.Prompt), nil
	})
	script := `
		const out = await pipeline(["keep", "throw", "boom"],
			(prev, item, i) => {
				if (item === "throw") throw new Error("stage threw for " + item);
				return agent("s0:" + item);
			},
			(prev, item, i) => agent("s1:" + prev));
		return out;
	`
	eng := New(Config{Stub: boomStub, ConcurrencyCap: 8, WatchdogTimeout: 10 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("pipeline rejected under concurrency (must never reject): %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	out, ok := res.Value.([]interface{})
	if !ok || len(out) != 3 {
		t.Fatalf("result = %#v, want 3-element slice", res.Value)
	}
	// keep flows through both stages; throw is dropped at stage 0; boom errors at
	// the stub in stage 0 (null) and stays null through stage 1.
	want := []interface{}{"R:s1:R:s0:keep", nil, nil}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("items = %#v, want %#v", out, want)
	}
}
