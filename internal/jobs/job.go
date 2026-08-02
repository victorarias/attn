// Package jobs is the durable job queue.
//
// It runs short, retryable units of work, persisting each job through a
// pluggable Store (the daemon injects a SQLite-backed one) so a daemon crash
// never loses a pending or in-flight job. It is deliberately daemon-agnostic:
// it accepts a Store and a LogFunc at construction and MUST NOT import
// internal/daemon (the daemon imports this package, so the reverse would be an
// import cycle).
//
// A single dispatch goroutine selects eligible jobs and launches each as its own
// goroutine, bounded by a per-kind concurrency cap (default 1): a kind is
// serialized with itself while different kinds run in parallel. Every record
// read-modify-write funnels through one store-level lock, so persistence stays
// serialized even though handlers run concurrently.
//
// It replaces internal/tasks, carrying over the mechanics that package earned —
// the commit fence, cancel-blocks-until-exit, capped exponential backoff with a
// dead terminal state, per-kind concurrency caps, and the requeue-during-run
// flag — on a generalized record. What is new: a job carries a payload and
// returns a result, and coalescing is opt-in through UniqueKey rather than a law
// of the record's identity. That is what lets two jobs of the same kind run at
// once, which durable workflow activities require.
package jobs

import (
	"encoding/json"
	"fmt"
	"time"
)

// LogFunc matches the daemon's injected logger shape (see internal/pty,
// internal/classifier). Runtime logging goes through it — never log.Printf, whose
// stderr is discarded when the daemon runs in the background.
type LogFunc func(format string, args ...interface{})

// State is a job's position in the lifecycle.
//
//	queued  -> running                (dispatch picks it up when now >= ScheduledAt)
//	running -> done                   (handler returned nil)
//	running -> failed                 (handler returned an error)
//	failed  -> queued                 (auto-requeue once now >= ScheduledAt and Attempts < max)
//	failed  -> dead                   (no auto-requeue once Attempts >= max)
//	failed|dead -> queued             (manual Retry, ScheduledAt = now)
//
// State is serialized as a plain string (not a Go enum) so the stored record
// stays self-describing and forward-compatible.
type State string

const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateFailed  State = "failed"
	StateDone    State = "done"
	StateDead    State = "dead"
)

// Terminal reports whether a state is an end state (no further run without an
// explicit Retry).
func (s State) Terminal() bool { return s == StateDone || s == StateDead }

// Job is one durable unit of work.
type Job struct {
	// ID is opaque and generated at enqueue. Nothing parses it: coalescing
	// identity lives in UniqueKey, so callers that want to address a job by what
	// it is about use the kind+key surface rather than reconstructing an id.
	ID string `json:"id"`
	// Kind selects the handler (registered via Runner.Register).
	Kind string `json:"kind"`
	// UniqueKey is the optional coalescing identity within a kind. When set, a
	// second Enqueue for the same kind+key targets the SAME record — the debounce
	// collapses a burst of triggers into one run, and a trigger arriving mid-run
	// re-runs the job rather than starting a second one. When empty the job is
	// distinct: every Enqueue creates a new record, which is what lets two runs of
	// the same kind carry different arguments and be in flight together.
	UniqueKey string `json:"unique_key,omitempty"`
	// Priority orders selection among jobs that are all eligible; higher runs
	// first. Ties fall through to ScheduledAt, then CreatedAt, so the
	// longest-waiting job of a priority claims a freed slot first.
	Priority int `json:"priority,omitempty"`
	// Payload carries the run's inputs. It is opaque to the queue — only the
	// handler that was enqueued with it interprets it — and it is the reason a job
	// survives the disappearance of whatever it describes: a run that fires after
	// its session and workspace rows are gone still holds everything it needs.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Result is what the handler returned on success, persisted so a caller that
	// was not waiting can still read the outcome. Nil for a job that has not
	// succeeded.
	Result json.RawMessage `json:"result,omitempty"`
	// State is the lifecycle position; see State.
	State State `json:"state"`
	// Attempts counts how many times a handler has run for this record. It is
	// incremented when a run starts, so a record in failed/dead reflects the
	// number of handler invocations already spent.
	Attempts int `json:"attempts"`
	// MaxAttempts overrides the runner's attempt cap for this job. Zero means
	// "use the runner default".
	MaxAttempts int `json:"max_attempts,omitempty"`
	// ScheduledAt is both the earliest time dispatch may run this job and the
	// coalesce debounce anchor (UTC). A queued job is eligible once now has
	// reached it; a failed job auto-requeues once now has reached it.
	ScheduledAt time.Time `json:"scheduled_at"`
	// LastError is the most recent handler failure message (display + diagnosis).
	LastError string `json:"last_error,omitempty"`
	// CreatedAt is when the record was first persisted (UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the record was last persisted (UTC).
	UpdatedAt time.Time `json:"updated_at"`
	// Requeued records that a coalescing Enqueue arrived WHILE this job was
	// running. The in-flight run cannot be overwritten without tearing its
	// bookkeeping, so the re-enqueue sets this flag and pushes ScheduledAt; when
	// the run finishes the runner honors the flag by returning to queued (re-run)
	// instead of done, so a coalesced trigger that landed mid-run is never lost.
	Requeued bool `json:"requeued,omitempty"`

	// CommitGuard is the commit-fence latch for THIS run, injected by the runner
	// before it invokes the handler. It is never persisted. The handler calls
	// CommitGuard.Enter immediately before its single durable write and Leave
	// after, so a concurrent Cancel either fences the run cleanly before commit or
	// waits for the write to finish untorn. It is nil on records returned by
	// List/Get/Enqueue (those are not live runs).
	CommitGuard *CommitGuard `json:"-"`
}

// DecodePayload unmarshals the job's payload into v. A job with no payload
// leaves v untouched, so a handler whose payload type is all-optional does not
// have to special-case the empty case.
func (j *Job) DecodePayload(v any) error {
	if j == nil || len(j.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(j.Payload, v); err != nil {
		return fmt.Errorf("jobs: decode payload for %s (%s): %w", j.ID, j.Kind, err)
	}
	return nil
}

// clone returns a deep copy so callers (dispatch, Cancel, status reads) can hand
// out records without sharing the runner's mutable pointer. Payload and Result
// are byte slices, so a shallow copy would alias them; copy them so a caller
// mutating a clone never reaches the stored record.
func (j *Job) clone() *Job {
	if j == nil {
		return nil
	}
	cp := *j
	cp.Payload = cloneBytes(j.Payload)
	cp.Result = cloneBytes(j.Result)
	return &cp
}

func cloneBytes(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
