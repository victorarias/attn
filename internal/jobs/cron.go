package jobs

import (
	"errors"
	"fmt"
	"time"
)

// Recurring work runs on the queue as an ordinary job that re-arms itself.
//
// The alternative — a ticker goroutine per periodic duty that enqueues onto the
// queue — is what this replaces. A ticker's next fire lives only in memory, so a
// restart silently resets it, nothing shows the schedule, and every new duty
// brings its own goroutine and its own shutdown wiring. A cron entry is a
// durable record: it survives a restart with its next fire intact, it appears in
// the background-work panel like everything else, and it is one registration.

// CronKey is the coalescing key every cron entry uses. A kind has exactly one
// recurring record, so the key only has to be stable, and a fixed one keeps the
// entry addressable (GetByKey) without the caller knowing how arming works.
const CronKey = "cron"

// RegisterCron wires a handler for a kind the queue fires every interval,
// forever. The first fire is one interval after Start.
//
// A cron entry NEVER dies. An ordinary job that keeps failing is eventually
// abandoned, because nobody wants a broken job retried until the end of time;
// but a heartbeat that stops is a silent outage — no tick, no log, no panel
// entry moving — so a failing fire is logged, recorded on the record, and
// re-armed for the next interval instead of counting toward the attempt cap.
// Handlers for recurring work should therefore treat their own errors as
// reportable, not fatal.
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

// cronInterval returns the recurrence for a kind, or 0 when the kind is not a
// cron entry.
func (r *Runner) cronInterval(kind string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handlers[kind].interval
}

// armCron creates the recurring record for every registered cron kind that does
// not already have one. It runs once, from Start.
//
// An EXISTING record is left exactly as it is. Arming it again would push its
// ScheduledAt an interval into the future on every boot, so a daemon restarted
// more often than the interval would never tick at all — the failure mode a
// durable schedule exists to rule out.
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
		if existing != nil {
			continue
		}
		if _, err := r.Enqueue(kind, EnqueueOptions{UniqueKey: CronKey, Delay: interval}); err != nil {
			r.log("jobs: arm cron entry for %s: %v", kind, err)
		}
	}
}

// rearmCronLocked returns a finished cron job to queued, scheduled one interval
// out. The caller holds ioMu and has already confirmed the kind recurs. runErr
// is the fire's outcome: it is recorded so the panel and the log show a failing
// heartbeat, but it never stops the recurrence (see RegisterCron).
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

// ErrNotCron is returned when a cron-only operation names a kind that does not
// recur.
var ErrNotCron = errors.New("jobs: kind is not a cron entry")

// CronEntry returns the recurring record for kind — what the next fire is
// scheduled for, and how the last one went. It is the read behind an operator
// asking "is this heartbeat still beating?".
func (r *Runner) CronEntry(kind string) (*Job, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	if r.cronInterval(kind) <= 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotCron, kind)
	}
	return r.GetByKey(kind, CronKey)
}
