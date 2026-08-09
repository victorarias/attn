package daemon

import (
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/jobs"
)

// The daemon's two periodic duties — the notebook tick and the scheduled-automation
// observation — used to be ticker goroutines whose next fire lived only in memory.
// They are cron entries on the job queue now, so startJobQueue is what arms
// them: if this breaks, nothing ticks and nothing says so.
func TestStartJobQueueArmsThePeriodicTicks(t *testing.T) {
	d := newBubbleDaemon(t)
	notebookRoot := t.TempDir()
	// Arming is synchronous inside startJobQueue, so a missing entry here is never
	// an early read: either the runner never started or the store refused the
	// write. Both used to be logged into a test daemon's nil logger and dropped,
	// which is how this failing under full-suite load survived a triage as a race.
	// CronEntry carries the reason now, so the fatal below prints it.
	synctest.Test(t, func(t *testing.T) {
		d.store.SetSetting(SettingNotebookRoot, notebookRoot)
		d.startJobQueue()
		runner := d.jobQueueRef()
		t.Cleanup(runner.Stop)
		synctest.Wait()

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
	})
}
