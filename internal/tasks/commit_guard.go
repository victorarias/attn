package tasks

import "sync"

// CommitGuard is the executor-owned commit-fence latch. It is the load-bearing
// primitive that makes Cancel safe: it draws the line between work the runner may
// cancel mid-flight and a durable write the runner must never tear.
//
// Contract (ported in spirit from the keeper compaction commit fence in
// internal/daemon/workspace_keeper.go):
//
//   - The executor does its cancellable work (LLM call, file reads) honoring
//     ctx.Done() up to the moment it is about to perform its single durable write.
//   - Immediately before that write it calls Enter(). From that point the runner's
//     Cancel will NOT cancel the run's context — it instead waits for the
//     goroutine to exit. So the executor can complete its atomic write and persist
//     a coherent result even though a Cancel arrived.
//   - After the write the executor calls Leave() (always via defer once Enter
//     returned true).
//
// Enter reports whether the commit may proceed. If a Cancel already fired before
// the executor reached Enter, Enter returns false: the run was cancelled cleanly
// before committing, so the executor must skip the write. This is the (a) branch
// of the contract ("cancel a not-yet-committing run cleanly"); a true return is
// the (b) branch ("an already-committing run finishes its write untorn").
//
// The runner drives the coordination via tryFence / committed; an executor only
// ever touches Enter/Leave.
type CommitGuard struct {
	mu         sync.Mutex
	cancelled  bool // a Cancel fired before commit; the executor must not write
	committing bool // the executor is inside its durable write
}

// Enter is called by the executor immediately before its durable write. It
// returns true if the executor may proceed with the write (and MUST then call
// Leave when done), or false if a cancellation already fenced the run before it
// reached commit (the executor must abandon the write).
func (g *CommitGuard) Enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancelled {
		return false
	}
	g.committing = true
	return true
}

// Leave is called by the executor after its durable write completes. It is only
// valid to call after Enter returned true.
func (g *CommitGuard) Leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.committing = false
}

// tryFence is called by the runner's Cancel under the latch. If the run has not
// yet entered its commit, it marks the run cancelled (so a later Enter returns
// false) and reports true — meaning "you may cancel the context now". If the run
// is already committing, it reports false — meaning "do not cancel; wait for the
// goroutine to exit so the durable write is not torn".
func (g *CommitGuard) tryFence() (mayCancel bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.committing {
		return false
	}
	g.cancelled = true
	return true
}
