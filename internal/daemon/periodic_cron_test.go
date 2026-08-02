package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/jobs"
)

// The daemon's two periodic duties — the notebook tick and the scheduled-automation
// observation — used to be ticker goroutines whose next fire lived only in memory.
// They are cron entries on the job queue now, so startJobQueue is what arms
// them: if this breaks, nothing ticks and nothing says so.
func TestStartJobQueueArmsThePeriodicTicks(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	d.startJobQueue()
	runner := d.jobQueueRef()
	t.Cleanup(runner.Stop)

	for _, kind := range []string{notebookCronKind, automationScheduleKind} {
		entry, err := runner.CronEntry(kind)
		if err != nil {
			t.Fatalf("cron entry for %s: %v", kind, err)
		}
		if entry == nil {
			t.Fatalf("%s is not armed", kind)
		}
		if entry.State != jobs.StateQueued {
			t.Fatalf("%s entry state = %s, want queued", kind, entry.State)
		}
	}

	// The panel lists work the daemon owes, so the two heartbeats must not be in it.
	list, err := runner.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, job := range list {
		if job.Kind == notebookCronKind || job.Kind == automationScheduleKind {
			t.Fatalf("the work list included the %s heartbeat: %+v", job.Kind, job)
		}
	}
}
