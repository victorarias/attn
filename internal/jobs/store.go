package jobs

import "time"

// Store is the persistence + single-instance-lock seam the Runner sits on. The
// daemon injects a SQLite-backed implementation so jobs live in the profile DB;
// keeping the seam here (rather than importing internal/store) preserves this
// package as a leaf.
//
// The Runner funnels every read-modify-write through one lock, so an
// implementation does not need its own concurrency control — only durability.
type Store interface {
	// Init prepares the store. A DB-backed store is a no-op: migrations create
	// the table when the database opens.
	Init() error

	// AcquireLock takes exclusive single-instance ownership, returning an opaque
	// token for ReleaseLock. Returns ErrAlreadyRunning if another live process
	// already holds it.
	AcquireLock() (string, error)
	// ReleaseLock releases a token from AcquireLock. Best-effort; never blocks Stop.
	ReleaseLock(token string)

	// RecoverOrphans resets any job left in StateRunning back to StateQueued
	// (ScheduledAt = now) and returns how many were recovered. A running row at
	// startup means a crash interrupted that job mid-run.
	RecoverOrphans(now time.Time) (int, error)

	// Load returns the record for id, or (nil, nil) when there is none.
	Load(id string) (*Job, error)
	// LoadByKey returns the record for a kind's coalescing key, or (nil, nil) when
	// there is none. Only jobs enqueued with a UniqueKey are reachable this way.
	LoadByKey(kind, uniqueKey string) (*Job, error)
	// Save persists a record (create or overwrite by id).
	Save(j *Job) error
	// Delete removes a record by id; a missing record is not an error.
	Delete(id string) error
	// List returns every persisted record.
	List() ([]*Job, error)

	// Eligible returns at most limit jobs that may run at now, ordered the way
	// dispatch wants to claim them: highest Priority first, then earliest
	// ScheduledAt, then oldest CreatedAt. "May run" means a queued job whose
	// ScheduledAt has arrived, or a failed job whose backoff has elapsed —
	// the attempt cap is the Runner's to enforce, since it knows the default.
	Eligible(now time.Time, limit int) ([]*Job, error)

	// TrimDone deletes jobs that completed successfully before cutoff and reports
	// how many were removed. Only StateDone is trimmed: see Runner retention.
	TrimDone(cutoff time.Time) (int, error)
}
