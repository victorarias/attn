package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/jobs"
)

// newTestJobStore builds the queue's persistence over the test daemon's own
// SQLite store, with the single-instance lock in a per-test temp dir.
//
// The lock dir is NOT config.DataDir() (what production uses): a test binary
// builds many daemons in one process against one scoped data dir, so a shared
// lock would let the first started runner block every later one.
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
