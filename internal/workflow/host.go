package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

// nowRFC3339Nano stamps a display-only wall-clock time for a call's StartedAt/
// CompletedAt. It matches the protocol's RFC3339Nano format so the CLI and UI can
// parse it. These timestamps are NEVER part of the resume cache identity.
func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// runState holds everything mutated during a single Run/Resume. It lives on the
// loop goroutine; only the agent worker goroutines touch the parts explicitly
// documented as concurrent (the semaphore + the counters, which are mutated
// back on the loop goroutine inside posted closures).
type runState struct {
	vm    *goja.Runtime
	el    *eventLoop
	stub  AgentStub
	jour  Journal
	stack *pathStack

	// ctx is the run's cancellation context, passed to every live agent() dispatch
	// so canceling the run (attn workflow cancel / watchdog) tears the subagent down.
	ctx context.Context

	agentLifetimeCap int
	maxItemsPerCall  int

	// Loop-goroutine-only counters (mutated inside posted closures or synchronously).
	liveAgentCount int
	cachedCalls    int
	liveCalls      int

	// diverged latches true the moment any agent() misses the cache. Once set,
	// every structurally-later agent() (i.e. every subsequent invocation in the
	// deterministic control flow) runs live too — enforcing the R-spec's
	// prefix-and-suffix invalidation: no cached call may have a live-run ancestor,
	// even if its own prompt/schema happens to still match.
	diverged bool

	// Concurrency cap for live agent dispatch (correctness, not throughput).
	sem chan struct{}

	// thenCatchNull and stageWrap are cached JS helpers (compiled once) used to
	// give parallel/pipeline their never-reject / null-on-throw semantics in JS,
	// where promise semantics are exact.
	resolveThen goja.Callable // (p, onFulfilled, onRejected) => p.then(onFulfilled, onRejected)
	promiseAll  goja.Callable // (arr) => Promise.all(arr)

	// nullValue is goja null, cached.
	nullValue goja.Value
}

// callsiteKey returns a stable lexical id for the agent() call site = the
// immediate user frame's source Position. Captured synchronously while the host
// fn runs on the VM goroutine (the call stack is intact).
func (rs *runState) callsiteKey() string {
	var frames [4]goja.StackFrame
	captured := rs.vm.CaptureCallStack(4, frames[:0])
	// frames[0] is the host fn's own frame (agent); the first frame whose Position
	// has a filename is the user call site.
	for _, f := range captured {
		pos := f.Position()
		if pos.Filename != "" && pos.Line > 0 {
			return fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column)
		}
	}
	// Fallback: a single bucket. Loop counter still disambiguates repeats.
	return "<unknown>"
}

// installHostFns registers args/log/phase/agent/parallel/pipeline/workflow.
func installHostFns(rs *runState, args any) error {
	vm := rs.vm
	rs.nullValue = goja.Null()

	// Cache JS helpers for promise composition.
	thenV, err := vm.RunString(`(function(p, onF, onR){ return Promise.resolve(p).then(onF, onR); })`)
	if err != nil {
		return err
	}
	if c, ok := goja.AssertFunction(thenV); ok {
		rs.resolveThen = c
	} else {
		return fmt.Errorf("internal: then helper is not callable")
	}
	allV, err := vm.RunString(`(function(arr){ return Promise.all(arr); })`)
	if err != nil {
		return err
	}
	if c, ok := goja.AssertFunction(allV); ok {
		rs.promiseAll = c
	} else {
		return fmt.Errorf("internal: all helper is not callable")
	}

	if err := vm.Set("args", vm.ToValue(args)); err != nil {
		return err
	}
	if err := vm.Set("log", func(goja.FunctionCall) goja.Value { return goja.Undefined() }); err != nil {
		return err
	}
	if err := vm.Set("phase", func(call goja.FunctionCall) goja.Value {
		title := ""
		if len(call.Arguments) > 0 {
			title = call.Argument(0).String()
		}
		rs.stack.setPhase(title)
		return goja.Undefined()
	}); err != nil {
		return err
	}
	if err := vm.Set("workflow", func(goja.FunctionCall) goja.Value {
		panic(vm.ToValue((&ErrWorkflowNotImpl{}).Error()))
	}); err != nil {
		return err
	}
	if err := vm.Set("agent", rs.makeAgentFn()); err != nil {
		return err
	}
	if err := vm.Set("parallel", rs.makeParallelFn()); err != nil {
		return err
	}
	if err := vm.Set("pipeline", rs.makePipelineFn()); err != nil {
		return err
	}
	return nil
}

// makeAgentFn returns the agent(prompt, opts?) host function. It reads the
// structural ordinal SYNCHRONOUSLY (before any async boundary), checks the
// journal (cache hit -> resolve immediately), or dispatches the fake stub on a
// worker goroutine and resolves when it posts the result back.
func (rs *runState) makeAgentFn() func(goja.FunctionCall) goja.Value {
	vm := rs.vm
	return func(call goja.FunctionCall) goja.Value {
		prompt := ""
		if len(call.Arguments) > 0 {
			prompt = call.Arguments[0].String()
		}
		// opts (call.Argument(1)) may carry a schema (and label/phase). The schema
		// is part of the cache identity (hashSchema) AND is threaded to the stub so
		// the real driver can advertise it through the return_result sink. E1
		// callers pass no opts => schema nil => schemaHash "none" => behavior
		// unchanged.
		schema := extractAgentSchema(call.Argument(1))
		// isolation/model/agentType select the live call's execution context. They
		// are threaded to the stub but NOT folded into the cache identity: the
		// predicate stays ordinal+prompt_hash+schema_hash (§6). isolation is
		// validated to "" | "worktree" (unknown => "") so a typo can't silently run
		// in an unexpected mode.
		isolation := validateIsolation(extractAgentString(call.Argument(1), "isolation"))
		model := extractAgentString(call.Argument(1), "model")
		agentType := extractAgentString(call.Argument(1), "agentType")
		// label is DISPLAY metadata (shown in `workflow show` and the UI). Like
		// model it is NOT part of the cache identity.
		label := extractAgentString(call.Argument(1), "label")

		// --- fix the ordinal synchronously, before anything async ---
		site := rs.callsiteKey()
		ordinal := rs.stack.ordinalFor(site)
		ordKey := ordinal.String()
		promptHash := hashPrompt(prompt)
		schemaHash := hashSchema(schema)
		// Capture the current phase title synchronously, on the loop goroutine, so
		// it reflects THIS call's structural position (display only, not identity).
		phaseTitle := rs.stack.currentPhase()

		p, resolve, _ := vm.NewPromise()

		// Cache check: a hit resolves immediately on the loop goroutine — but ONLY
		// while we are still in the unchanged prefix. Once any earlier call has
		// diverged, this and every later call run live (prefix-and-suffix rule).
		if !rs.diverged {
			if entry, ok := rs.jour.Lookup(ordKey); ok && IsCacheHit(entry, ordKey, promptHash, schemaHash) {
				rs.cachedCalls++
				val := rs.resultToValue(entry.Result)
				mustResolve(resolve, val)
				return vm.ToValue(p)
			}
			// First miss/mismatch: latch divergence so everything after runs live.
			rs.diverged = true
		}

		// Live call. Enforce the lifetime cap before spawning.
		rs.liveAgentCount++
		if rs.liveAgentCount > rs.agentLifetimeCap {
			panic(vm.ToValue((&ErrAgentCap{Cap: rs.agentLifetimeCap}).Error()))
		}

		// Emit an in-flight "running" record at DISPATCH (on the loop goroutine,
		// before the worker spawns) so `workflow show` and the UI can see the call
		// that is currently executing — without this the run looks frozen while a
		// multi-minute call is in flight. IsCacheHit rejects non-terminal entries,
		// so this row reaches the daemon store but never serves a resume cache hit;
		// the terminal Upsert below overwrites it in place at the same ordinal.
		startedAt := nowRFC3339Nano()
		rs.jour.Upsert(JournalEntry{
			Ordinal: ordKey, PromptHash: promptHash, SchemaHash: schemaHash,
			Status: "running", Label: label, Phase: phaseTitle, Model: model,
			StartedAt: startedAt,
		})

		ordSnapshot := ordinal.clone()
		go func() {
			// Concurrency cap (correctness semaphore).
			rs.sem <- struct{}{}
			res, runErr := rs.stub.Run(rs.ctx, AgentCall{
				Ordinal:   ordSnapshot,
				Prompt:    prompt,
				Schema:    schema,
				Isolation: isolation,
				Model:     model,
				AgentType: agentType,
			})
			<-rs.sem

			// Hop back to the loop goroutine to journal + resolve. This is the ONLY
			// place a worker's value re-enters the runtime — proving cross-goroutine
			// results marshal safely onto the single runtime goroutine.
			rs.el.post(func() {
				rs.liveCalls++
				if runErr != nil {
					// Terminal failure: resolve null (never reject), journal errored.
					// Carry the same display fields + StartedAt so the terminal row
					// keeps them after overwriting the "running" record at this ordinal.
					rs.jour.Upsert(JournalEntry{
						Ordinal: ordKey, PromptHash: promptHash, SchemaHash: schemaHash,
						Result: nil, Status: "errored", Err: runErr.Error(),
						Label: label, Phase: phaseTitle, Model: model,
						StartedAt: startedAt, CompletedAt: nowRFC3339Nano(),
					})
					mustResolve(resolve, rs.nullValue)
					return
				}
				rs.jour.Upsert(JournalEntry{
					Ordinal: ordKey, PromptHash: promptHash, SchemaHash: schemaHash,
					Result: res, Status: "ok",
					Label: label, Phase: phaseTitle, Model: model,
					StartedAt: startedAt, CompletedAt: nowRFC3339Nano(),
				})
				mustResolve(resolve, rs.resultToValue(res))
			})
		}()

		return vm.ToValue(p)
	}
}

// extractAgentSchema pulls the `schema` property off the agent() opts object (the
// second argument) and marshals it to canonical JSON. Returns nil when opts is
// absent, not an object, or carries no schema — keeping the no-schema path
// (schemaHash "none") exact. The schema is canonicalized via Go's json.Marshal
// so the same logical schema hashes identically across runs.
func extractAgentSchema(optsVal goja.Value) json.RawMessage {
	if optsVal == nil || goja.IsUndefined(optsVal) || goja.IsNull(optsVal) {
		return nil
	}
	obj, ok := optsVal.(*goja.Object)
	if !ok {
		return nil
	}
	schemaVal := obj.Get("schema")
	if schemaVal == nil || goja.IsUndefined(schemaVal) || goja.IsNull(schemaVal) {
		return nil
	}
	exported := schemaVal.Export()
	if exported == nil {
		return nil
	}
	raw, err := json.Marshal(exported)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

// extractAgentString pulls a string-valued property (e.g. "isolation", "model",
// "agentType") off the agent() opts object. It mirrors extractAgentSchema: an
// absent opts object, a missing key, or a non-string value yields "". Numbers and
// other non-strings are deliberately ignored rather than coerced, so a malformed
// opt never silently becomes a meaningful value.
func extractAgentString(optsVal goja.Value, key string) string {
	if optsVal == nil || goja.IsUndefined(optsVal) || goja.IsNull(optsVal) {
		return ""
	}
	obj, ok := optsVal.(*goja.Object)
	if !ok {
		return ""
	}
	v := obj.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	s, ok := v.Export().(string)
	if !ok {
		return ""
	}
	return s
}

// validateIsolation normalizes the isolation opt to the supported set: "" (none,
// share the writable working tree) or "worktree". Any unknown value falls back to
// "" so a typo can't silently change WHERE the call runs.
func validateIsolation(s string) string {
	if s == "worktree" {
		return "worktree"
	}
	return ""
}

// mustResolve calls a NewPromise resolve func and propagates an uncatchable error
// (e.g. an *InterruptedError from a watchdog fire during the await continuation).
// goja returns such errors rather than panicking; we must re-panic so the loop's
// recover surfaces it as RunStatus interrupted instead of silently stalling.
func mustResolve(resolve func(interface{}) error, v goja.Value) {
	if err := resolve(v); err != nil {
		panic(err)
	}
}

// resultToValue converts a journaled JSON result into a goja Value (null on empty).
func (rs *runState) resultToValue(raw json.RawMessage) goja.Value {
	if len(raw) == 0 {
		return rs.nullValue
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		// Fall back to the raw string if it is not valid JSON.
		return rs.vm.ToValue(string(raw))
	}
	return rs.vm.ToValue(v)
}

// makeParallelFn returns parallel(thunks): a barrier that never rejects. Each
// thunk i is invoked under a parallelSlot(i) marker (synchronously, during the
// structural descent), and a throwing thunk yields a null slot. Returns a Promise
// of [results].
func (rs *runState) makeParallelFn() func(goja.FunctionCall) goja.Value {
	vm := rs.vm
	return func(call goja.FunctionCall) goja.Value {
		thunks := toSlice(vm, call.Argument(0))
		if len(thunks) > rs.maxItemsPerCall {
			panic(vm.ToValue((&ErrTooManyItems{Construct: "parallel", Count: len(thunks), Max: rs.maxItemsPerCall}).Error()))
		}

		childPromises := make([]goja.Value, len(thunks))
		for i, thunkV := range thunks {
			thunk, ok := goja.AssertFunction(thunkV)
			if !ok {
				// Non-callable slot -> null.
				childPromises[i] = rs.settledNull()
				continue
			}
			// Capture this slot's path snapshot so any agent() inside reads the
			// positional ordinal, independent of resolution timing.
			pop := rs.stack.push(segParallelSlot, i)
			slotPath := rs.stack.snapshot()
			childPromises[i] = rs.invokeNullable(thunk, slotPath)
			pop()
		}

		arr := vm.NewArray(toIfaceSlice(childPromises)...)
		out, err := rs.promiseAll(goja.Undefined(), arr)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return out
	}
}

// makePipelineFn returns pipeline(items, ...stages): no barrier; each item flows
// through all stages independently. Stage cb signature is (prevResult,
// originalItem, index). A throwing stage drops that item to null for the rest of
// its stages. Returns a Promise of [perItemFinalResult].
func (rs *runState) makePipelineFn() func(goja.FunctionCall) goja.Value {
	vm := rs.vm
	return func(call goja.FunctionCall) goja.Value {
		items := toSlice(vm, call.Argument(0))
		if len(items) > rs.maxItemsPerCall {
			panic(vm.ToValue((&ErrTooManyItems{Construct: "pipeline", Count: len(items), Max: rs.maxItemsPerCall}).Error()))
		}
		var stages []goja.Callable
		for _, a := range call.Arguments[1:] {
			if fn, ok := goja.AssertFunction(a); ok {
				stages = append(stages, fn)
			} else {
				stages = append(stages, nil)
			}
		}

		itemResults := make([]goja.Value, len(items))
		for j, item := range items {
			itemResults[j] = rs.buildPipelineItem(j, item, stages)
		}

		arr := vm.NewArray(toIfaceSlice(itemResults)...)
		out, err := rs.promiseAll(goja.Undefined(), arr)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return out
	}
}

// buildPipelineItem constructs the .then chain for one item across all stages.
// The chain is CONSTRUCTED synchronously (markers pushed/popped during descent),
// but each stage's agent() calls fire at RESOLUTION time. We defeat the resulting
// timing nondeterminism by capturing each stage's path snapshot into the closure
// and re-establishing it before the callback runs (§3.5 of the design).
func (rs *runState) buildPipelineItem(j int, item goja.Value, stages []goja.Callable) goja.Value {
	vm := rs.vm
	popItem := rs.stack.push(segPipelineItem, j)
	defer popItem()

	// prev starts as the original item, already-resolved.
	prev := rs.settledValue(item)
	for s, stage := range stages {
		popStage := rs.stack.push(segStage, s)
		stagePath := rs.stack.snapshot() // captured at construction; bound to (j, s)

		if stage == nil {
			// Non-callable stage: drop the item to null for the rest.
			prev = rs.settledNull()
			popStage()
			continue
		}

		stageFn := stage
		idxVal := vm.ToValue(j)
		origItem := item

		// onFulfilled(prevResult): re-establish the captured path, then run the
		// stage cb with (prevResult, originalItem, index). If prevResult is null
		// (a prior stage failed/dropped), short-circuit to null per native.
		onFulfilled := func(fcall goja.FunctionCall) goja.Value {
			prevResult := fcall.Argument(0)
			if goja.IsNull(prevResult) || goja.IsUndefined(prevResult) {
				return rs.nullValue
			}
			restore := rs.stack.replace(stagePath)
			defer restore()
			v, err := stageFn(goja.Undefined(), prevResult, origItem, idxVal)
			if err != nil {
				// Throwing stage -> null for this item.
				return rs.nullValue
			}
			return v
		}
		// onRejected: a rejected upstream promise also drops the item to null.
		onRejected := func(goja.FunctionCall) goja.Value { return rs.nullValue }

		next, err := rs.resolveThen(goja.Undefined(), prev, vm.ToValue(onFulfilled), vm.ToValue(onRejected))
		if err != nil {
			next = rs.settledNull()
		}
		prev = next
		popStage()
	}
	return prev
}

// invokeNullable invokes a thunk under the given captured path, wrapping the
// result so a throw/rejection becomes a null slot (parallel never rejects). The
// parallel thunk takes no args.
func (rs *runState) invokeNullable(fn goja.Callable, capturedPath []segment) goja.Value {
	vm := rs.vm
	// Re-establish the captured path so any synchronous agent() inside the thunk
	// reads the positional ordinal. (Synchronous calls already see it via the live
	// push; the capture matters for agent() issued after an await inside the thunk.)
	restore := rs.stack.replace(capturedPath)
	var result goja.Value
	var thrown bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				thrown = true
			}
		}()
		v, err := fn(goja.Undefined())
		if err != nil {
			thrown = true
			return
		}
		result = v
	}()
	restore()
	if thrown {
		return rs.settledNull()
	}
	// Wrap the (possibly pending) result so a later rejection becomes null and so
	// any agent() the continuation issues re-establishes the captured path.
	onRejected := func(goja.FunctionCall) goja.Value { return rs.nullValue }
	onFulfilled := func(fcall goja.FunctionCall) goja.Value {
		// Re-establish the captured path for any continuation work.
		restore := rs.stack.replace(capturedPath)
		defer restore()
		return fcall.Argument(0)
	}
	wrapped, err := rs.resolveThen(goja.Undefined(), result, vm.ToValue(onFulfilled), vm.ToValue(onRejected))
	if err != nil {
		return rs.settledNull()
	}
	return wrapped
}

// settledValue returns an already-fulfilled promise carrying v.
func (rs *runState) settledValue(v goja.Value) goja.Value {
	p, resolve, _ := rs.vm.NewPromise()
	mustResolve(resolve, v)
	return rs.vm.ToValue(p)
}

// settledNull returns an already-fulfilled promise carrying null.
func (rs *runState) settledNull() goja.Value {
	return rs.settledValue(rs.nullValue)
}

// toSlice flattens a goja array-like value into a Go slice of element Values.
func toSlice(vm *goja.Runtime, v goja.Value) []goja.Value {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	obj, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	lenV := obj.Get("length")
	if lenV == nil {
		return nil
	}
	n := int(lenV.ToInteger())
	out := make([]goja.Value, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, obj.Get(fmt.Sprintf("%d", i)))
	}
	return out
}

// toIfaceSlice adapts a []goja.Value to the variadic []interface{} NewArray wants.
func toIfaceSlice(vs []goja.Value) []interface{} {
	out := make([]interface{}, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}
