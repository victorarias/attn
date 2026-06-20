package daemon

import (
	"context"
	"path/filepath"
	"testing"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/tasks"
)

// TestNotebookTasksEnabledDefaultsOn proves the master switch is opt-OUT: a blank
// or unset value reads as enabled (so existing installs keep running the keeper),
// the documented truthy spellings enable it, and only an explicit falsey value
// disables the whole async-duty group.
func TestNotebookTasksEnabledDefaultsOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	if !d.notebookTasksEnabled() {
		t.Fatal("unset notebook.tasks_enabled must default to ON")
	}
	for _, on := range []string{"true", "on", "1", "yes", "  TRUE  "} {
		d.store.SetSetting(SettingNotebookTasksEnabled, on)
		if !d.notebookTasksEnabled() {
			t.Fatalf("value %q must enable keeper tasks", on)
		}
	}
	for _, off := range []string{"false", "off", "0", "no"} {
		d.store.SetSetting(SettingNotebookTasksEnabled, off)
		if d.notebookTasksEnabled() {
			t.Fatalf("value %q must disable keeper tasks", off)
		}
	}
}

// TestNotebookTasksDisabledSkipsEnqueue proves the master switch gates the
// BACKGROUND enqueue chokepoints: with the toggle off, a session-stop summarize and
// a workspace narrate create no durable record at all; flipping it back on (here via
// the default-ON unset) restores enqueueing.
func TestNotebookTasksDisabledSkipsEnqueue(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	installNotebookNarrationRunner(t, d)

	d.store.SetSetting(SettingNotebookTasksEnabled, "false")
	d.enqueueSummarizeSession("session-off", "", "")
	d.enqueueNarrateWorkspace("ws-off")
	// The gate returns synchronously without touching the runner, so an immediate
	// Get is authoritative — no record was created.
	assertNoTask(t, d, notebookSummarizeSessionKind, "session-off")
	assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-off")

	d.store.SetSetting(SettingNotebookTasksEnabled, "true")
	d.enqueueSummarizeSession("session-on", "", "")
	d.enqueueNarrateWorkspace("ws-on")
	if !taskExists(t, d, notebookSummarizeSessionKind, "session-on") {
		t.Fatal("summarize must enqueue once the master switch is on")
	}
	if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-on") {
		t.Fatal("narrate must enqueue once the master switch is on")
	}
}

// TestNotebookTasksDisabledExecutorNoOps proves the master switch is also honored at
// RUN time: a record queued before the user disabled the keeper (here injected
// directly past the enqueue gate) is retired as a no-op success without invoking the
// agent, so a stale queued run cannot fire background work after the toggle is off.
func TestNotebookTasksDisabledExecutorNoOps(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	installNotebookNarrationRunner(t, d)
	d.store.SetSetting(SettingNotebookTasksEnabled, "false")

	d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		t.Fatal("summarize executor ran the agent while the master switch was off")
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "session-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookSummarizeSessionKind, "session-1", tasks.StateDone)
}

// assertNoTask fails if a record exists for the given kind/subject. Unlike
// taskExists it does not poll: it asserts the record is absent right now, used after
// a synchronous gate that must never have reached the runner.
func assertNoTask(t *testing.T, d *Daemon, kind, subject string) {
	t.Helper()
	task, err := d.compactRunner.Get(tasks.TaskID(kind, subject))
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task != nil {
		t.Fatalf("expected no %s task for %q, got %+v", kind, subject, task)
	}
}
