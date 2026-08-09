package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

// nowRFC3339Nano stamps a display-only time, NEVER part of the cache identity.
func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// runState holds everything mutated during a Run/Resume. It lives on the loop
// goroutine; workers touch only the semaphore and mutate counters back on the
// loop goroutine inside posted closures.
type runState struct {
	vm    *goja.Runtime
	el    *eventLoop
	stub  AgentStub
	jour  Journal
	stack *pathStack

	// ctx reaches every live agent() dispatch, so cancel tears the subagent down.
	ctx context.Context

	agentLifetimeCap int
	maxItemsPerCall  int

	// Loop-goroutine-only counters (mutated inside posted closures or synchronously).
	liveAgentCount int
	cachedCalls    int
	liveCalls      int

	// diverged latches on the first cache miss; every structurally-later agent()
	// then runs live too, so no cached call ever has a live-run ancestor.
	diverged bool

	// Concurrency cap for live agent dispatch: correctness, not throughput.
	sem chan struct{}

	// Cached JS helpers giving parallel/pipeline their never-reject semantics in
	// JS, where promise semantics are exact.
	resolveThen goja.Callable // (p, onFulfilled, onRejected) => p.then(onFulfilled, onRejected)
	promiseAll  goja.Callable // (arr) => Promise.all(arr)

	nullValue goja.Value
}

// callsiteKey is a stable lexical id for the agent() call site. Must be called
// synchronously on the VM goroutine, while the call stack is intact.
func (rs *runState) callsiteKey() string {
	var frames [4]goja.StackFrame
	captured := rs.vm.CaptureCallStack(4, frames[:0])
	// frames[0] is the host fn's own frame; the first with a filename is the caller.
	for _, f := range captured {
		pos := f.Position()
		if pos.Filename != "" && pos.Line > 0 {
			return fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column)
		}
	}
	// A single bucket; the loop counter still disambiguates repeats.
	return "<unknown>"
}

// installHostFns registers args/log/phase/agent/parallel/pipeline/workflow.
func installHostFns(rs *runState, args any) error {
	vm := rs.vm
	rs.nullValue = goja.Null()

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

// makeAgentFn returns the agent(prompt, opts?) host function. It must read the
// structural ordinal SYNCHRONOUSLY, before any async boundary.
func (rs *runState) makeAgentFn() func(goja.FunctionCall) goja.Value {
	vm := rs.vm
	return func(call goja.FunctionCall) goja.Value {
		prompt := ""
		if len(call.Arguments) > 0 {
			prompt = call.Arguments[0].String()
		}
		// The schema is part of the cache identity (hashSchema).
		schema := extractAgentSchema(call.Argument(1))
		// Threaded to the stub but NOT part of the cache identity, which stays
		// ordinal+prompt_hash+schema_hash.
		isolation := validateIsolation(extractAgentString(call.Argument(1), "isolation"))
		model := extractAgentString(call.Argument(1), "model")
		agentType := extractAgentString(call.Argument(1), "agentType")
		// label is display metadata, not part of the cache identity.
		label := extractAgentString(call.Argument(1), "label")

		// Fix the ordinal synchronously, before anything async.
		site := rs.callsiteKey()
		ordinal := rs.stack.ordinalFor(site)
		ordKey := ordinal.String()
		promptHash := hashPrompt(prompt)
		schemaHash := hashSchema(schema)
		// Captured here so it reflects THIS call's position. Display only.
		phaseTitle := rs.stack.currentPhase()

		p, resolve, _ := vm.NewPromise()

		// A hit resolves immediately, but only inside the unchanged prefix.
		if !rs.diverged {
			if entry, ok := rs.jour.Lookup(ordKey); ok && IsCacheHit(entry, ordKey, promptHash, schemaHash) {
				rs.cachedCalls++
				val := rs.resultToValue(entry.Result)
				mustResolve(resolve, val)
				return vm.ToValue(p)
			}
			// Latch divergence so everything after runs live.
			rs.diverged = true
		}

		rs.liveAgentCount++
		if rs.liveAgentCount > rs.agentLifetimeCap {
			panic(vm.ToValue((&ErrAgentCap{Cap: rs.agentLifetimeCap}).Error()))
		}

		// An in-flight record, so a multi-minute call is visible. IsCacheHit rejects
		// non-terminal entries, and the terminal Upsert overwrites this in place.
		startedAt := nowRFC3339Nano()
		rs.jour.Upsert(JournalEntry{
			Ordinal: ordKey, PromptHash: promptHash, SchemaHash: schemaHash,
			Status: "running", Label: label, Phase: phaseTitle, Model: model,
			StartedAt: startedAt,
		})

		ordSnapshot := ordinal.clone()
		go func() {
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

			// The ONLY place a worker's value re-enters the runtime.
			rs.el.post(func() {
				rs.liveCalls++
				if runErr != nil {
					// Resolves null, never rejects; the display fields are carried so
					// overwriting the "running" row keeps them.
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

// extractAgentSchema pulls `schema` off the agent() opts object as canonical
// JSON, so the same logical schema hashes identically across runs.
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

// extractAgentString pulls a string property off the agent() opts object;
// non-strings are ignored rather than coerced.
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

// validateIsolation normalizes the isolation opt to "" (share the working tree)
// or "worktree". Unknown falls back to "" so a typo can't change where a call runs.
func validateIsolation(s string) string {
	if s == "worktree" {
		return "worktree"
	}
	return ""
}

// mustResolve re-panics on goja's uncatchable errors (a watchdog
// *InterruptedError), which it returns; without that the loop silently stalls.
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
		return rs.vm.ToValue(string(raw))
	}
	return rs.vm.ToValue(v)
}

// makeParallelFn returns parallel(thunks): a barrier that never rejects; a
// throwing thunk yields a null slot.
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
			// So agent() inside reads the positional ordinal, whatever the timing.
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

// makePipelineFn returns pipeline(items, ...stages): no barrier, each item flows
// through all stages independently; a throwing stage drops that item to null.
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

// buildPipelineItem constructs one item's .then chain synchronously, but each
// stage's agent() fires at resolution time — hence the captured path snapshot
// re-established before each callback.
func (rs *runState) buildPipelineItem(j int, item goja.Value, stages []goja.Callable) goja.Value {
	vm := rs.vm
	popItem := rs.stack.push(segPipelineItem, j)
	defer popItem()

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

		// A null prevResult (a prior stage dropped the item) short-circuits.
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

// invokeNullable runs a thunk under a captured path, wrapping the result so a
// throw or rejection becomes a null slot.
func (rs *runState) invokeNullable(fn goja.Callable, capturedPath []segment) goja.Value {
	vm := rs.vm
	// Matters for agent() issued after an await; synchronous calls see the push.
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
	// So a later rejection becomes null and continuations keep the captured path.
	onRejected := func(goja.FunctionCall) goja.Value { return rs.nullValue }
	onFulfilled := func(fcall goja.FunctionCall) goja.Value {
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
