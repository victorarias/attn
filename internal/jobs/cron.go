package jobs

import (
	"errors"
	"fmt"
	"time"
)

// CronKey is the coalescing key every cron entry uses: one recurring record per kind.
const CronKey = "cron"

// RegisterCron wires a handler the queue fires every interval, first fire one
// interval after Start. A cron entry NEVER dies: a failing fire is logged and
// re-armed instead of counting toward the attempt cap.
func (r *Runner) RegisterCron(kind string, interval time.Duration, fn HandlerFunc, cfg HandlerConfig) error {
	if interval <= 0 {
		return fmt.Errorf("jobs: cron interval for %s must be positive, got %s", kind, interval)
	}
	if err := r.RegisterWith(kind, fn, cfg); err != nil {
		return err
	}
	r.mu.Lock()
	entry := r.handlers[kind]
	entry.interval = interval
	r.handlers[kind] = entry
	r.mu.Unlock()
	return nil
}

// cronInterval returns the recurrence for a kind, or 0 when it is not a cron entry.
func (r *Runner) cronInterval(kind string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handlers[kind].interval
}

// armCron gives every registered cron kind a recurring record, once, from Start.
// An on-schedule record is left alone: re-arming every boot would starve a daemon
// restarted more often than the interval. A terminal record is revived — a build
// that missed the registration killed it as unknown-kind, and nothing else selects it.
func (r *Runner) armCron() {
	r.mu.Lock()
	kinds := make(map[string]time.Duration, len(r.handlers))
	for kind, entry := range r.handlers {
		if entry.interval > 0 {
			kinds[kind] = entry.interval
		}
	}
	r.mu.Unlock()

	for kind, interval := range kinds {
		existing, err := r.GetByKey(kind, CronKey)
		if err != nil {
			r.log("jobs: read cron entry for %s: %v", kind, err)
			continue
		}
		if existing != nil && !existing.State.Terminal() {
			continue
		}
		if existing != nil {
			r.log("jobs: cron entry for %s was %s (%s); reviving it", kind, existing.State, existing.LastError)
		}
		// Enqueue coalesces, so a revive resets that row rather than minting a second.
		if _, err := r.Enqueue(kind, EnqueueOptions{UniqueKey: CronKey, Delay: interval}); err != nil {
			r.log("jobs: arm cron entry for %s: %v", kind, err)
		}
	}
}

// rearmCronLocked returns a finished cron job to queued one interval out.
// Caller holds ioMu. runErr is recorded but never stops the recurrence.
func (r *Runner) rearmCronLocked(j *Job, interval time.Duration, runErr error) {
	now := r.now()
	j.State = StateQueued
	j.Attempts = 0
	j.Requeued = false
	j.ScheduledAt = now.Add(interval)
	j.UpdatedAt = now
	if runErr != nil {
		j.LastError = runErr.Error()
		r.log("jobs: cron %s failed, next fire at %s: %v",
			j.Kind, j.ScheduledAt.Format(time.RFC3339), runErr)
	} else {
		j.LastError = ""
	}
	if err := r.store.Save(j); err != nil {
		r.log("jobs: re-arm cron entry for %s: %v", j.Kind, err)
	}
}

// ErrNotCron is returned when a cron-only operation names a kind that does not recur.
var ErrNotCron = errors.New("jobs: kind is not a cron entry")

// ErrCronKind is returned by Enqueue for ordinary work aimed at a recurring kind.
var ErrCronKind = errors.New("jobs: kind is a cron entry and cannot be enqueued directly")

// CronEntry returns the recurring record for kind: next fire and last outcome.
func (r *Runner) CronEntry(kind string) (*Job, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	if r.cronInterval(kind) <= 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotCron, kind)
	}
	return r.GetByKey(kind, CronKey)
}
