// Package jobs is the durable job queue: short, retryable units of work
// persisted through a pluggable Store so a daemon crash never loses a job.
// MUST NOT import internal/daemon (the daemon imports this package).
//
// One dispatch goroutine launches each eligible job in its own goroutine under
// a per-kind concurrency cap (default 1); every record read-modify-write
// funnels through one store-level lock.
package jobs

import (
	"encoding/json"
	"fmt"
	"time"
)

// LogFunc is the daemon's injected logger shape. Never log.Printf: its stderr
// is discarded when the daemon runs in the background.
type LogFunc func(format string, args ...interface{})

// State is a job's position in the lifecycle.
//
//	queued  -> running                (dispatch picks it up when now >= ScheduledAt)
//	running -> done                   (handler returned nil)
//	running -> failed                 (handler returned an error)
//	failed  -> queued                 (auto-requeue once now >= ScheduledAt and Attempts < max)
//	failed  -> dead                   (no auto-requeue once Attempts >= max)
//	failed|dead -> queued             (manual Retry, ScheduledAt = now)
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
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// UniqueKey is the coalescing identity within a kind: a second Enqueue for
	// the same kind+key targets the SAME record. Empty means a distinct job.
	UniqueKey string `json:"unique_key,omitempty"`
	// Priority orders eligible jobs; higher first, ties by ScheduledAt then CreatedAt.
	Priority    int             `json:"priority,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	State       State           `json:"state"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts,omitempty"`
	// ScheduledAt (UTC) is the earliest dispatch time and the coalesce debounce anchor.
	ScheduledAt time.Time `json:"scheduled_at"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Requeued records a coalescing Enqueue that arrived WHILE this job ran;
	// finish() then returns it to queued so a mid-run trigger is never lost.
	Requeued bool `json:"requeued,omitempty"`

	// CommitGuard is the commit fence for THIS run, injected by the runner and
	// never persisted (nil on records from List/Get/Enqueue). The handler wraps
	// its single durable write in Enter/Leave so a concurrent Cancel either
	// fences before commit or waits for an untorn write.
	CommitGuard *CommitGuard `json:"-"`
}

// DecodePayload unmarshals the job's payload into v; no payload leaves v untouched.
func (j *Job) DecodePayload(v any) error {
	if j == nil || len(j.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(j.Payload, v); err != nil {
		return fmt.Errorf("jobs: decode payload for %s (%s): %w", j.ID, j.Kind, err)
	}
	return nil
}

// clone deep-copies; Payload/Result are slices, so a shallow copy aliases the store.
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
