package workflow

import (
	"context"

	"github.com/dop251/goja"
)

// eventLoop is a minimal single-goroutine JS event loop. The goja *Runtime is
// created and touched ONLY by the goroutine running run(); every host fn,
// resolve(), and Set() executes there. Worker goroutines never touch the runtime;
// they post a closure on `jobs`, and the loop goroutine runs it (which is where
// resolve() is legally called).
//
// This is the load-bearing thread-safety invariant: vm.Interrupt is the only goja
// call permitted off the loop goroutine.
type eventLoop struct {
	runtime *goja.Runtime
	jobs    chan func() // cross-goroutine mailbox: closures to run ON the loop goroutine

	// onEnterJS / onLeaveJS arm/disarm the watchdog around any segment that
	// re-enters the bytecode interpreter (initial script run and each resolve()).
	onEnterJS func()
	onLeaveJS func()
}

func newEventLoop(rt *goja.Runtime) *eventLoop {
	return &eventLoop{
		runtime: rt,
		// Buffered generously so a burst of worker completions never blocks a
		// worker goroutine trying to hand back a result.
		jobs: make(chan func(), 4096),
	}
}

// post enqueues a closure to run on the loop goroutine. Safe to call from any
// goroutine. The closure is the only place a worker's result re-enters the runtime.
func (el *eventLoop) post(fn func()) {
	el.jobs <- fn
}

// runJS executes fn (which re-enters goja) with the watchdog armed around it, then
// disarmed. A panic from goja (e.g. an *InterruptedError surfaced via resolve()'s
// recursive RunProgram) propagates after disarming.
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

// pump drives the runtime until the top-level promise settles. Each iteration
// blocks for the next worker-delivered closure, then runs it on the loop
// goroutine. resolve() inside that closure re-enters goja, runs await
// continuations, and leave() drains the synchronous microtask tail. When all
// async work is done the top-level promise settles and we stop.
//
// The select also wakes on ctx cancellation. This is load-bearing for cancel:
// while the script is parked on `await agent(...)`, the loop is blocked HERE on a
// Go channel receive, not in goja, so the watchdog's vm.Interrupt cannot reach it
// (vm.Interrupt only interrupts bytecode execution). Returning an *ErrInterrupted
// on ctx.Done() makes `attn workflow cancel` actually settle a run that is waiting
// on a live agent() — in-flight subagents are torn down separately via the run ctx
// threaded into AgentStub.Run.
//
// A recovered panic (interrupt) is returned via the panicVal so the caller can map
// it to RunStatus interrupted.
func (el *eventLoop) pump(ctx context.Context, topLevel *goja.Promise) (state goja.PromiseState, result goja.Value, panicVal interface{}) {
	for topLevel.State() == goja.PromiseStatePending {
		select {
		case job := <-el.jobs:
			caught := el.safeRunJS(job)
			if caught != nil {
				return topLevel.State(), topLevel.Result(), caught
			}
		case <-ctx.Done():
			return topLevel.State(), topLevel.Result(), &ErrInterrupted{Reason: "workflow cancelled"}
		}
	}
	return topLevel.State(), topLevel.Result(), nil
}

// safeRunJS runs a job with watchdog arming and recovers any panic so the loop can
// surface an interrupt cleanly instead of crashing the goroutine.
func (el *eventLoop) safeRunJS(job func()) (caught interface{}) {
	defer func() {
		if r := recover(); r != nil {
			caught = r
		}
	}()
	el.runJS(job)
	return nil
}
