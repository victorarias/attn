package workflow

// pathContextTracker is a goja AsyncContextTracker that carries the structural
// path context (the engine's pathStack state) across every await / .then boundary.
//
// Why this exists (the load-bearing E1 invariant): an async stage/slot callback
// that issues an agent() AFTER its own internal `await` resumes on a continuation
// the engine does not synchronously bracket. Without help, rs.stack would have
// already unwound to whatever the loop goroutine touched last, so the post-await
// agent() would read an empty path and a globally-counted ordinal assigned by
// promise-RESOLUTION order — a temporal ordinal, the exact failure §6 forbids.
//
// goja invokes this tracker around the promise machinery:
//   - Grab() runs when a reaction is REGISTERED (at the await point / .then call).
//     It snapshots the current structural path and binds it to that pending
//     continuation.
//   - Resumed(ctx) runs right BEFORE the continuation executes; we install the
//     snapshot so any agent() in the continuation reads its structural ordinal.
//   - Exited() runs after the continuation finishes; we restore the prior state.
//
// Resumed/Exited never nest (guaranteed by goja's contract), but reaction jobs do
// run interleaved with the synchronous descent, so we save the pre-Resumed state
// and restore it on Exited rather than merely clearing — that keeps the
// synchronous pipeline/parallel construction path intact.
type pathContextTracker struct {
	stack *pathStack

	// saved holds the stack state captured at Resumed time, restored at Exited.
	// A non-nil saved means a reaction job is currently in flight.
	saved *stackState
}

func newPathContextTracker(stack *pathStack) *pathContextTracker {
	return &pathContextTracker{stack: stack}
}

// Grab snapshots the current structural path to bind to a freshly-registered
// continuation. Returning a value-type stackState (not a pointer into the live
// stack) is what makes the bound context immune to later mutation.
func (t *pathContextTracker) Grab() interface{} {
	return t.stack.captureState()
}

// Resumed installs the path snapshot bound to this continuation before it runs.
// It saves the current state first so Exited can restore it. A nil/typeless ctx
// (a reaction registered before the tracker existed, or a non-path job) leaves the
// stack untouched.
func (t *pathContextTracker) Resumed(ctx interface{}) {
	prev := t.stack.captureState()
	t.saved = &prev
	if s, ok := ctx.(stackState); ok {
		t.stack.restoreState(s)
	}
}

// Exited restores the pre-Resumed stack state so the synchronous event-loop
// context the reaction interrupted is left exactly as it was.
func (t *pathContextTracker) Exited() {
	if t.saved != nil {
		t.stack.restoreState(*t.saved)
		t.saved = nil
	}
}
