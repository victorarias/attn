// Command spike-ext is a throwaway viability spike for attn's extension
// platform. It is NOT product code and is deleted after the findings land.
//
// It answers four questions that the platform design hangs on:
//
//  1. Can agent-authored JS subscribe to a daemon event and run as a handler?
//  2. Can a handler BLOCK a core daemon operation on a human verdict, resuming
//     with structured feedback (the "gate")?
//  3. Does blocking for a long time trip the watchdog? (The workflow engine's
//     watchdog is armed around JS segments; a gate parks for minutes.)
//  4. Does a handler that never answers degrade safely (timeout -> policy)?
//
// The host below is a stripped copy of internal/workflow's event-loop +
// watchdog pattern, with workflow-specific machinery (journal, resume,
// determinism bans, agent()) removed and extension host fns installed instead.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// ---------------------------------------------------------------------------
// Host: event loop (copied shape from internal/workflow/loop.go)
// ---------------------------------------------------------------------------

type eventLoop struct {
	jobs      chan func()
	onEnterJS func()
	onLeaveJS func()
}

func newEventLoop() *eventLoop { return &eventLoop{jobs: make(chan func(), 256)} }

func (el *eventLoop) post(fn func()) { el.jobs <- fn }

func (el *eventLoop) runJS(fn func()) {
	if el.onEnterJS != nil {
		el.onEnterJS()
	}
	defer func() {
		if el.onLeaveJS != nil {
			el.onLeaveJS()
		}
	}()
	fn()
}

func (el *eventLoop) safeRunJS(job func()) (caught interface{}) {
	defer func() {
		if r := recover(); r != nil {
			caught = r
		}
	}()
	el.runJS(job)
	return nil
}

// pump drives the VM until the handler's promise settles. While a handler is
// parked on `await ask(...)`, this blocks on a Go channel receive — NOT inside
// goja — which is precisely why the watchdog cannot fire during a gate.
func (el *eventLoop) pump(ctx context.Context, top *goja.Promise) (goja.PromiseState, goja.Value, interface{}) {
	for top.State() == goja.PromiseStatePending {
		select {
		case job := <-el.jobs:
			if caught := el.safeRunJS(job); caught != nil {
				return top.State(), top.Result(), caught
			}
		case <-ctx.Done():
			return top.State(), top.Result(), errors.New("handler cancelled")
		}
	}
	return top.State(), top.Result(), nil
}

// watchdog interrupts the VM if a single JS segment runs too long. Armed on
// entering JS, disarmed on leaving it.
type watchdog struct {
	vm       *goja.Runtime
	timeout  time.Duration
	deadline atomic.Int64
	fired    atomic.Bool
}

func newWatchdog(vm *goja.Runtime, timeout time.Duration) *watchdog {
	return &watchdog{vm: vm, timeout: timeout}
}

func (w *watchdog) arm()    { w.deadline.Store(time.Now().Add(w.timeout).UnixNano()) }
func (w *watchdog) disarm() { w.deadline.Store(0) }

func (w *watchdog) start(ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				d := w.deadline.Load()
				if d != 0 && time.Now().UnixNano() > d {
					w.fired.Store(true)
					w.vm.Interrupt("handler exceeded its time budget")
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// ---------------------------------------------------------------------------
// Host: the extension surface
// ---------------------------------------------------------------------------

// Event is what the daemon hands an extension.
type Event struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

// Verdict is what a gate handler returns to the daemon.
type Verdict struct {
	Decision string         `json:"decision"` // allow | deny
	Fields   map[string]any `json:"fields,omitempty"`
}

// AskRequest is the UI payload a handler puts up. In the real platform this
// becomes a view tree validated against a component registry; the spike only
// needs it to survive the JS->Go boundary intact.
type AskRequest struct {
	Title   string           `json:"title"`
	View    []map[string]any `json:"view"`
	Actions []map[string]any `json:"actions"`
}

// AskResponse is the human's answer.
type AskResponse struct {
	Action string            `json:"action"`
	Fields map[string]string `json:"fields"`
}

// UI is the seam the daemon implements: put a view up, block, return an answer.
type UI interface {
	Ask(ctx context.Context, req AskRequest) (AskResponse, error)
}

type extHost struct {
	ui UI
	// asks counts how many times the handler actually interrupted the human.
	asks atomic.Int32
}

// dispatch runs one handler invocation to completion. One goja VM per event —
// handlers are one-shot, so no long-lived VM and no cross-event state in JS.
func (h *extHost) dispatch(ctx context.Context, script string, ev Event, budget time.Duration) (Verdict, error) {
	vm := goja.New()
	el := newEventLoop()

	wd := newWatchdog(vm, budget)
	el.onEnterJS = wd.arm
	el.onLeaveJS = wd.disarm
	stop := wd.start(ctx)
	defer stop()

	// --- host fns ---

	// ask(spec) -> Promise<AskResponse>. The handler parks here; the loop
	// goroutine drops out of goja and blocks on el.jobs.
	if err := vm.Set("ask", func(call goja.FunctionCall) goja.Value {
		var req AskRequest
		raw, _ := json.Marshal(call.Argument(0).Export())
		_ = json.Unmarshal(raw, &req)

		p, resolve, reject := vm.NewPromise()
		h.asks.Add(1)
		go func() {
			resp, err := h.ui.Ask(ctx, req)
			el.post(func() {
				if err != nil {
					mustSettle(reject, vm.ToValue(err.Error()))
					return
				}
				mustSettle(resolve, vm.ToValue(map[string]any{
					"action": resp.Action,
					"fields": resp.Fields,
				}))
			})
		}()
		return vm.ToValue(p)
	}); err != nil {
		return Verdict{}, err
	}

	if err := vm.Set("allow", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(map[string]any{"decision": "allow"})
	}); err != nil {
		return Verdict{}, err
	}
	if err := vm.Set("deny", func(call goja.FunctionCall) goja.Value {
		fields := map[string]any{}
		if len(call.Arguments) > 0 {
			if m, ok := call.Argument(0).Export().(map[string]any); ok {
				fields = m
			}
		}
		return vm.ToValue(map[string]any{"decision": "deny", "fields": fields})
	}); err != nil {
		return Verdict{}, err
	}
	if err := vm.Set("log", func(call goja.FunctionCall) goja.Value {
		fmt.Printf("      [ext log] %v\n", call.Argument(0))
		return goja.Undefined()
	}); err != nil {
		return Verdict{}, err
	}

	// The handler registry: `on(name, fn)` records handlers; the host then
	// invokes the one matching the event.
	handlers := map[string]goja.Callable{}
	if err := vm.Set("on", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if fn, ok := goja.AssertFunction(call.Argument(1)); ok {
			handlers[name] = fn
		}
		return goja.Undefined()
	}); err != nil {
		return Verdict{}, err
	}

	// --- load the extension (registration pass; must be fast + non-blocking) ---
	var loadPanic interface{}
	loadPanic = el.safeRunJS(func() {
		if _, err := vm.RunScript("extension.js", script); err != nil {
			panic(err)
		}
	})
	if loadPanic != nil {
		return Verdict{}, fmt.Errorf("load: %v", loadPanic)
	}

	fn, ok := handlers[ev.Name]
	if !ok {
		return Verdict{}, fmt.Errorf("no handler for %s", ev.Name)
	}

	// --- invoke the handler; it returns a promise (async fn) ---
	var top *goja.Promise
	invokePanic := el.safeRunJS(func() {
		v, err := fn(goja.Undefined(), vm.ToValue(ev.Payload))
		if err != nil {
			panic(err)
		}
		p, isPromise := v.Export().(*goja.Promise)
		if !isPromise {
			// A synchronous handler (the cheap-policy path): wrap its value.
			pp, resolve, _ := vm.NewPromise()
			mustSettle(resolve, v)
			top = pp
			return
		}
		top = p
	})
	if invokePanic != nil {
		return Verdict{}, fmt.Errorf("invoke: %v", invokePanic)
	}

	state, result, panicVal := el.pump(ctx, top)
	if panicVal != nil {
		return Verdict{}, fmt.Errorf("run: %v", panicVal)
	}
	if state == goja.PromiseStateRejected {
		return Verdict{}, fmt.Errorf("handler rejected: %v", result)
	}

	var verdict Verdict
	raw, _ := json.Marshal(result.Export())
	if err := json.Unmarshal(raw, &verdict); err != nil {
		return Verdict{}, fmt.Errorf("bad verdict: %w", err)
	}
	return verdict, nil
}

func mustSettle(settle func(interface{}) error, v goja.Value) {
	if err := settle(v); err != nil {
		panic(err)
	}
}

// ---------------------------------------------------------------------------
// Fake UIs standing in for the app
// ---------------------------------------------------------------------------

// promptUI answers after `think`, as a human would.
type promptUI struct {
	think  time.Duration
	action string
	fields map[string]string
	seen   *AskRequest
}

func (u *promptUI) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	u.seen = &req
	select {
	case <-time.After(u.think):
	case <-ctx.Done():
		return AskResponse{}, ctx.Err()
	}
	return AskResponse{Action: u.action, Fields: u.fields}, nil
}

// silentUI never answers — the human walked away.
type silentUI struct{}

func (silentUI) Ask(ctx context.Context, _ AskRequest) (AskResponse, error) {
	<-ctx.Done()
	return AskResponse{}, ctx.Err()
}

// ---------------------------------------------------------------------------
// The extension an agent would author
// ---------------------------------------------------------------------------

const delegationGateExtension = `
on("delegation.before_start", async (ev) => {
  // Cheap policy first: short, low-stakes briefs never interrupt the human.
  if (ev.brief.length < 120) {
    log("brief is short, auto-allowing without a gate");
    return allow();
  }

  const answer = await ask({
    title: "Approve delegation to " + ev.agent + "?",
    view: [
      { kind: "markdown", text: "**Ticket:** " + ev.ticket_id },
      { kind: "code", lang: "text", text: ev.brief },
      { kind: "textarea", id: "feedback", label: "What should change?" },
    ],
    actions: [
      { id: "approve", label: "Approve", primary: true },
      { id: "reject",  label: "Send back" },
    ],
  });

  if (answer.action === "approve") return allow();
  return deny({ feedback: answer.fields.feedback });
});
`

// ---------------------------------------------------------------------------

func main() {
	longBrief := "Refactor the entire authentication subsystem, rewrite the " +
		"session store, migrate every caller, and delete the legacy shim in one pass."
	shortBrief := "Fix the typo in the README."

	failures := 0
	check := func(name string, ok bool, detail string) {
		status := "PASS"
		if !ok {
			status = "FAIL"
			failures++
		}
		fmt.Printf("  [%s] %s\n        %s\n", status, name, detail)
	}

	fmt.Println("\n=== Q1/Q2: event -> handler -> blocking gate -> structured verdict ===")
	{
		ui := &promptUI{
			think:  1500 * time.Millisecond,
			action: "reject",
			fields: map[string]string{"feedback": "Too broad. Split the store migration out first."},
		}
		host := &extHost{ui: ui}
		// Budget is 200ms — far shorter than the 1.5s the human takes to answer.
		// If parking on `await ask()` kept the watchdog armed, this would be
		// interrupted rather than returning a verdict.
		start := time.Now()
		v, err := host.dispatch(context.Background(), delegationGateExtension, Event{
			Name: "delegation.before_start",
			Payload: map[string]any{
				"agent": "claude", "ticket_id": "tkt-4812", "brief": longBrief,
			},
		}, 200*time.Millisecond)
		elapsed := time.Since(start)

		check("handler ran and returned a verdict", err == nil, fmt.Sprintf("err=%v", err))
		check("verdict is deny", v.Decision == "deny", fmt.Sprintf("decision=%q", v.Decision))
		check("structured feedback crossed back to Go",
			v.Fields["feedback"] == "Too broad. Split the store migration out first.",
			fmt.Sprintf("feedback=%q", v.Fields["feedback"]))
		check("the operation really blocked on the human",
			elapsed >= 1500*time.Millisecond,
			fmt.Sprintf("elapsed=%s (human took 1.5s)", elapsed.Round(10*time.Millisecond)))
		if ui.seen != nil {
			check("view tree survived the JS->Go boundary",
				len(ui.seen.View) == 3 && len(ui.seen.Actions) == 2 && ui.seen.Title != "",
				fmt.Sprintf("title=%q view=%d actions=%d", ui.seen.Title, len(ui.seen.View), len(ui.seen.Actions)))
		}
	}

	fmt.Println("\n=== Q3: does a long block trip the watchdog? ===")
	{
		ui := &promptUI{think: 1200 * time.Millisecond, action: "approve"}
		host := &extHost{ui: ui}
		v, err := host.dispatch(context.Background(), delegationGateExtension, Event{
			Name: "delegation.before_start",
			Payload: map[string]any{
				"agent": "codex", "ticket_id": "tkt-4813", "brief": longBrief,
			},
		}, 50*time.Millisecond) // brutal 50ms JS budget
		check("50ms JS budget survives a 1.2s human pause",
			err == nil && v.Decision == "allow",
			fmt.Sprintf("decision=%q err=%v", v.Decision, err))
	}

	fmt.Println("\n=== Q3b: the budget still catches a runaway handler ===")
	{
		host := &extHost{ui: &promptUI{}}
		_, err := host.dispatch(context.Background(),
			`on("e", () => { while (true) {} });`,
			Event{Name: "e", Payload: map[string]any{}}, 100*time.Millisecond)
		check("infinite loop is interrupted, not hung",
			err != nil, fmt.Sprintf("err=%v", err))
	}

	fmt.Println("\n=== Q4: nobody answers -> timeout -> default policy ===")
	{
		host := &extHost{ui: silentUI{}}
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := host.dispatch(ctx, delegationGateExtension, Event{
			Name: "delegation.before_start",
			Payload: map[string]any{
				"agent": "claude", "ticket_id": "tkt-4814", "brief": longBrief,
			},
		}, time.Second)
		check("unanswered gate ends in bounded time",
			err != nil && time.Since(start) < time.Second,
			fmt.Sprintf("err=%v after %s", err, time.Since(start).Round(10*time.Millisecond)))
	}

	fmt.Println("\n=== Q5: cheap policy path costs the human nothing ===")
	{
		host := &extHost{ui: &promptUI{think: 5 * time.Second, action: "reject"}}
		start := time.Now()
		v, err := host.dispatch(context.Background(), delegationGateExtension, Event{
			Name: "delegation.before_start",
			Payload: map[string]any{
				"agent": "claude", "ticket_id": "tkt-4815", "brief": shortBrief,
			},
		}, 200*time.Millisecond)
		check("short brief auto-allows with no UI at all",
			err == nil && v.Decision == "allow" && host.asks.Load() == 0 && time.Since(start) < time.Second,
			fmt.Sprintf("decision=%q asks=%d elapsed=%s", v.Decision, host.asks.Load(), time.Since(start).Round(time.Millisecond)))
	}

	fmt.Printf("\n%d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
