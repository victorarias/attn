package daemon

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

// sqlJobStore satisfies the jobs.Store seam.
var _ jobs.Store = (*sqlJobStore)(nil)

// sqlJobStore adapts the profile SQLite store to the jobs.Store seam so the
// durable job queue persists in ~/.attn[-profile]/attn.db. It lives in the daemon
// (which imports both internal/jobs and internal/store) so neither of those
// packages depends on the other.
//
// The single-instance lock is a file lock in the daemon's own data root, beside
// the attn.pid that already marks one daemon per root — one queue per daemon.
// config.ValidateDaemonIsolation refuses to start a daemon whose data root is not
// the profile data dir unless its database is separately isolated, so a root of
// its own always means a store of its own.
type sqlJobStore struct {
	store   *store.Store
	lockDir string
	log     jobs.LogFunc
}

// newSQLJobStore builds the adapter over the daemon's store, locking under the
// daemon's data root.
func (d *Daemon) newSQLJobStore() *sqlJobStore {
	lockDir := d.dataRoot
	if lockDir == "" {
		lockDir = config.DataDir()
	}
	return &sqlJobStore{store: d.store, lockDir: lockDir, log: d.logf}
}

// jobSubject names the entity a background job acts on — the workspace, session,
// or ticket id its handler operates against. Every one of the daemon's kinds
// coalesces per subject, so the coalescing key IS the subject, and this is the
// one place that equivalence is written down: a handler asks for the subject and
// does not need to know it is reading a queue key.
func jobSubject(job *jobs.Job) string {
	if job == nil {
		return ""
	}
	return job.UniqueKey
}

// Init is a no-op: migration {86} creates the jobs table when the DB opens.
func (a *sqlJobStore) Init() error { return nil }

func (a *sqlJobStore) AcquireLock() (string, error) { return jobs.AcquireDirLock(a.lockDir, a.log) }
func (a *sqlJobStore) ReleaseLock(token string)     { jobs.ReleaseDirLock(token, a.log) }

func (a *sqlJobStore) RecoverOrphans(now time.Time) (int, error) {
	return a.store.RecoverRunningJobs(now)
}

func (a *sqlJobStore) Load(id string) (*jobs.Job, error) {
	rec, ok, err := a.store.GetJob(id)
	if err != nil || !ok {
		return nil, err
	}
	return recordToJob(*rec), nil
}

func (a *sqlJobStore) LoadByKey(kind, uniqueKey string) (*jobs.Job, error) {
	rec, ok, err := a.store.GetJobByUniqueKey(kind, uniqueKey)
	if err != nil || !ok {
		return nil, err
	}
	return recordToJob(*rec), nil
}

func (a *sqlJobStore) Save(j *jobs.Job) error { return a.store.UpsertJob(jobToRecord(j)) }

func (a *sqlJobStore) Delete(id string) error { return a.store.DeleteJob(id) }

func (a *sqlJobStore) List() ([]*jobs.Job, error) {
	recs, err := a.store.ListJobs()
	if err != nil {
		return nil, err
	}
	return recordsToJobs(recs), nil
}

func (a *sqlJobStore) Eligible(now time.Time, limit int) ([]*jobs.Job, error) {
	recs, err := a.store.EligibleJobs(now, limit)
	if err != nil {
		return nil, err
	}
	return recordsToJobs(recs), nil
}

func (a *sqlJobStore) TrimDone(cutoff time.Time) (int, error) {
	return a.store.TrimDoneJobs(cutoff)
}

func recordsToJobs(recs []store.JobRecord) []*jobs.Job {
	out := make([]*jobs.Job, 0, len(recs))
	for _, rec := range recs {
		out = append(out, recordToJob(rec))
	}
	return out
}

// jobToRecord maps a queue job to its store row. Payload and Result are carried
// as opaque JSON text; the store never interprets them. CommitGuard is per-run
// and never persisted.
func jobToRecord(j *jobs.Job) store.JobRecord {
	return store.JobRecord{
		ID:          j.ID,
		Kind:        j.Kind,
		UniqueKey:   j.UniqueKey,
		Priority:    j.Priority,
		Payload:     string(j.Payload),
		Result:      string(j.Result),
		State:       string(j.State),
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		ScheduledAt: j.ScheduledAt,
		LastError:   j.LastError,
		Requeued:    j.Requeued,
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
	}
}

func recordToJob(rec store.JobRecord) *jobs.Job {
	return &jobs.Job{
		ID:          rec.ID,
		Kind:        rec.Kind,
		UniqueKey:   rec.UniqueKey,
		Priority:    rec.Priority,
		Payload:     rawJSON(rec.Payload),
		Result:      rawJSON(rec.Result),
		State:       jobs.State(rec.State),
		Attempts:    rec.Attempts,
		MaxAttempts: rec.MaxAttempts,
		ScheduledAt: rec.ScheduledAt,
		LastError:   rec.LastError,
		Requeued:    rec.Requeued,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
}

// rawJSON turns a stored column into a payload, treating blank as absent so a
// job that carries nothing round-trips as nil rather than as an empty document.
func rawJSON(s string) json.RawMessage {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return json.RawMessage(s)
}
