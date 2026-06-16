// Package tasks is a general, file-backed, durable task runner.
//
// It runs short, retryable units of work (compaction, summarization, narration)
// out of a single worker goroutine, persisting one atomic-JSON file per task so a
// daemon crash never loses a pending or in-flight task. It is intentionally
// daemon-agnostic: it accepts a notebook root dir and a LogFunc at construction
// and MUST NOT import internal/daemon (the daemon imports this package, so the
// reverse would be an import cycle).
//
// The persistence, atomic temp+rename, and orphan-recovery idioms are ported from
// internal/notebook/dreams_state.go; the cancel-blocks-until-exit + commit-fence
// contract is ported from internal/daemon/workspace_keeper.go.
//
// What this package is NOT: there are no priorities, no DAG, no cron generality,
// no worker pool, no SQLite, no per-task lock file, and no heartbeat beyond the
// runner-owned context.WithTimeout around each executor invocation. The single
// worker serializes everything, so a per-task lock is unnecessary.
package tasks

import (
	"fmt"
	"time"
)

// LogFunc matches the daemon's injected logger shape (see internal/pty,
// internal/classifier). Runtime logging goes through it — never log.Printf, whose
// stderr is discarded when the daemon runs in the background.
type LogFunc func(format string, args ...interface{})

// State is a task's position in the lifecycle.
//
//	queued  -> running                (worker picks it up when now >= NextAttemptAt)
//	running -> done                   (executor returned nil)
//	running -> failed                 (executor returned an error)
//	failed  -> queued                 (auto-requeue once now >= NextAttemptAt and Attempts < max)
//	failed  -> dead                   (no auto-requeue once Attempts >= max)
//	failed|dead -> queued             (manual Retry, NextAttemptAt = now)
//
// State is serialized as a plain string (not a Go enum) so the on-disk record
// stays self-describing and forward-compatible.
type State string

const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateFailed  State = "failed"
	StateDone    State = "done"
	StateDead    State = "dead"
)

// Task is the durable record persisted as <root>/.attn/tasks/<id>.json.
//
// There is deliberately no Payload or DedupeMarker field: every kind derives what
// it needs from Subject (a workspace id, a session id, a notebook root), and
// idempotency lives in the kind's target file, not in this record.
type Task struct {
	// ID is derived from Kind+Subject (see TaskID) so re-enqueueing the same
	// logical work overwrites the same file instead of creating duplicates.
	ID string `json:"id"`
	// Kind selects the executor (registered via Runner.Register).
	Kind string `json:"kind"`
	// Subject is the kind-specific target identity (workspace id, session id, …).
	Subject string `json:"subject"`
	// State is the lifecycle position; see State.
	State State `json:"state"`
	// Attempts counts how many times an executor has run for this record. It is
	// incremented when a run starts, so a record in failed/dead reflects the
	// number of executor invocations already spent.
	Attempts int `json:"attempts"`
	// NextAttemptAt is both the earliest time the worker may run this task and the
	// coalesce debounce anchor (RFC3339, UTC). A queued task is eligible once now
	// has reached it; a failed task auto-requeues once now has reached it.
	NextAttemptAt time.Time `json:"next_attempt_at"`
	// LastError is the most recent executor failure message (display + diagnosis).
	LastError string `json:"last_error,omitempty"`
	// CreatedAt is when the record was first persisted (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the record was last persisted (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
	// Requeued records that a re-enqueue arrived WHILE this task was running. The
	// in-flight run cannot be overwritten without tearing its bookkeeping, so the
	// re-enqueue sets this flag and pushes NextAttemptAt; when the run finishes the
	// worker honors the flag by transitioning to queued (re-run) instead of done,
	// so a coalesced trigger that landed mid-run is never lost.
	Requeued bool `json:"requeued,omitempty"`

	// Meta is a small, kind-specific bag of inputs carried on the durable record.
	// MOST kinds need none of this: they re-derive everything from Subject at run
	// time (a workspace id, a session id), which is the deliberate "no Payload"
	// design above. The single exception is summarize_session: by the time its
	// debounced run fires AFTER a single-session-workspace teardown, both the
	// session row and the workspace row are gone from the store, so the executor
	// can no longer re-derive the transcript path or the workspace bucket from a
	// live row. The transcript FILE itself survives on disk (under ~/.claude /
	// ~/.codex), so carrying its path plus the workspace id here lets the digest
	// still be written to the correct per-workspace bucket post-removal. Kept as a
	// generic string map (not a typed struct) so the durable record stays
	// self-describing and forward-compatible like Subject/State.
	Meta map[string]string `json:"meta,omitempty"`

	// CommitGuard is the commit-fence latch for THIS run, injected by the runner
	// before it invokes the executor. It is never persisted. The executor calls
	// CommitGuard.Enter immediately before its single durable write and Leave
	// after, so a concurrent Cancel either fences the run cleanly before commit or
	// waits for the write to finish untorn. It is nil on records returned by
	// List/Get/Enqueue (those are not live runs).
	CommitGuard *CommitGuard `json:"-"`
}

// TaskID derives the stable, subject-coalescing id for a kind+subject pair, e.g.
// "compact_context:<wsID>" or "narrate_workspace:<wsID>". Re-enqueueing the same
// kind+subject targets the same file, which is the whole coalescing mechanism.
func TaskID(kind, subject string) string {
	return fmt.Sprintf("%s:%s", kind, subject)
}

// clone returns a deep copy so callers (the worker, Cancel, status reads) can
// hand out records without sharing the runner's mutable internal pointer. The
// shallow `cp := *t` copies the Meta map HEADER, so a clone handed to a caller
// would otherwise share — and race — the runner's underlying map; deep-copy it so
// a caller mutating the clone's Meta never touches the stored record's map.
func (t *Task) clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	cp.Meta = cloneStringMap(t.Meta)
	return &cp
}

// cloneStringMap returns an independent copy of m (nil for a nil/empty map). It
// is the deep-copy primitive for Task.Meta: both clone() and Enqueue use it so a
// carried Meta is never aliased between the stored record and a caller's copy.
func cloneStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
