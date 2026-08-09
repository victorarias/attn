package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/jobs"
)

// newTestJobStore builds the queue's persistence over the test daemon's own
// SQLite store, with the single-instance lock in a per-test temp dir. A runner
// this helper builds is not the daemon's own, so it takes a lock dir of its own
// rather than contending with startJobQueue's over the daemon's data root.
func newTestJobStore(t *testing.T, d *Daemon) jobs.Store {
	t.Helper()
	return &sqlJobStore{store: d.store, lockDir: t.TempDir(), log: func(string, ...any) {}}
}

// jobIDForKey resolves the queue id of the job a kind coalesces onto for key.
// The id-taking calls (Cancel, Retry) need it, and tests address jobs by the
// entity they act on.
func jobIDForKey(t *testing.T, runner *jobs.Runner, kind, key string) string {
	t.Helper()
	job, err := runner.GetByKey(kind, key)
	if err != nil {
		t.Fatalf("look up job %s/%s: %v", kind, key, err)
	}
	if job == nil {
		t.Fatalf("no job for %s/%s", kind, key)
	}
	return job.ID
}
