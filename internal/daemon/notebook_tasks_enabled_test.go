package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
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

// TestNotebookSummariesEnabledDefaultsOn proves the per-duty switch is also
// opt-out and that its effective value is present in the settings payload even
// when no row has been persisted yet.
func TestNotebookSummariesEnabledDefaultsOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	if !d.notebookSummariesEnabled() {
		t.Fatal("unset notebook.summarize_session.enabled must default to ON")
	}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookSummarizeSessionEnabled]; got != "true" {
		t.Fatalf("effective summary setting = %#v, want true", got)
	}

	d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "false")
	if d.notebookSummariesEnabled() {
		t.Fatal("explicit false must disable session summaries")
	}
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookSummarizeSessionEnabled]; got != "false" {
		t.Fatalf("effective summary setting = %#v, want false", got)
	}
}

// TestNotebookWorkspaceNarrationEnabledDefaultsOn proves the curated-journal
// switch is opt-out and appears as its effective value in every settings snapshot.
func TestNotebookWorkspaceNarrationEnabledDefaultsOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	if !d.notebookWorkspaceNarrationEnabled() {
		t.Fatal("unset notebook.narrate_workspace.enabled must default to ON")
	}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookNarrateWorkspaceEnabled]; got != "true" {
		t.Fatalf("effective narration setting = %#v, want true", got)
	}

	d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "false")
	if d.notebookWorkspaceNarrationEnabled() {
		t.Fatal("explicit false must disable workspace narration")
	}
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookNarrateWorkspaceEnabled]; got != "false" {
		t.Fatalf("effective narration setting = %#v, want false", got)
	}
}

// TestNotebookSummariesDisabledOnlySkipsSummaries proves the per-duty switch
// leaves journal narration alone while preventing summary records from being
// created. Re-enabling restores the enqueue path without changing its model.
func TestNotebookSummariesDisabledOnlySkipsSummaries(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)

		d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "false")
		d.enqueueSummarizeSession("session-off", "", "")
		d.enqueueNarrateWorkspace("ws-on")
		assertNoTask(t, d, notebookSummarizeSessionKind, "session-off")
		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-on") {
			t.Fatal("summary switch must not disable journal narration")
		}

		d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "true")
		d.enqueueSummarizeSession("session-on", "", "")
		if !taskExists(t, d, notebookSummarizeSessionKind, "session-on") {
			t.Fatal("summarize must enqueue once its duty switch is on")
		}
	})
}

// TestNotebookNarrationDisabledOnlySkipsNarration proves the switch gates every
// narration enqueue path without disabling session summaries. Re-enabling restores
// routine narration without changing its agent/model configuration.
func TestNotebookNarrationDisabledOnlySkipsNarration(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)

		d.store.SetSetting(SettingNotebookNarrateWorkspace, `{"agent":"claude","model":"claude-custom"}`)
		d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "false")
		d.enqueueNarrateWorkspace("ws-routine-off")
		d.enqueueDailyNarrateWorkspace("ws-daily-off")
		d.enqueueFinalNarrateWorkspace("ws-final-off")
		d.enqueueSummarizeSession("session-on", "", "")
		assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-routine-off")
		assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-daily-off")
		assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-final-off")
		if !taskExists(t, d, notebookSummarizeSessionKind, "session-on") {
			t.Fatal("narration switch must not disable session summaries")
		}

		d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "true")
		d.enqueueNarrateWorkspace("ws-routine-on")
		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-routine-on") {
			t.Fatal("narration must enqueue once its duty switch is on")
		}
		if got := d.store.GetSetting(SettingNotebookNarrateWorkspace); got != `{"agent":"claude","model":"claude-custom"}` {
			t.Fatalf("narration model config changed across toggle: %s", got)
		}
	})
}

// TestNotebookTasksDisabledSkipsEnqueue proves the master switch gates the
// BACKGROUND enqueue chokepoints: with the toggle off, a session-stop summarize and
// a workspace narrate create no durable record at all; flipping it back on (here via
// the default-ON unset) restores enqueueing.
func TestNotebookTasksDisabledSkipsEnqueue(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
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
	})
}

// TestNotebookTasksDisabledExecutorNoOps proves the master switch is also honored at
// RUN time: a record queued before the user disabled the keeper (here injected
// directly past the enqueue gate) is retired as a no-op success without invoking the
// agent, so a stale queued run cannot fire background work after the toggle is off.
func TestNotebookTasksDisabledExecutorNoOps(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)
		d.store.SetSetting(SettingNotebookTasksEnabled, "false")

		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			t.Fatal("summarize executor ran the agent while the master switch was off")
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "session-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)
	})
}

// TestNotebookSummariesDisabledExecutorNoOps covers the queued-before-toggle
// case: the durable record completes without spawning a headless agent.
func TestNotebookSummariesDisabledExecutorNoOps(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)
		d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "false")

		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			t.Fatal("summarize executor ran the agent while session summaries were off")
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "session-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)
	})
}

// TestNotebookNarrationDisabledExecutorNoOps covers the queued-before-toggle
// case: the durable record completes without spawning a headless agent.
func TestNotebookNarrationDisabledExecutorNoOps(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)
		d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "false")

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			t.Fatal("narrate executor ran the agent while journal narration was off")
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", jobs.StateDone)
	})
}

// assertNoTask fails if a record exists for the given kind/subject. Unlike
// taskExists it does not poll: it asserts the record is absent right now, used after
// a synchronous gate that must never have reached the runner.
func assertNoTask(t *testing.T, d *Daemon, kind, subject string) {
	t.Helper()
	task, err := d.jobQueue.GetByKey(kind, subject)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task != nil {
		t.Fatalf("expected no %s task for %q, got %+v", kind, subject, task)
	}
}
