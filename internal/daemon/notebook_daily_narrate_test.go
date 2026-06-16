package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tasks"
)

// pinUTCSlot configures the daily-narrate cron to use the shared nightly slot in a
// fixed UTC timezone, so schedule math in tests is independent of the machine's local
// time. The frequency default ("0 3 * * *") is used; only the timezone is pinned.
func pinUTCSlot(t *testing.T, d *Daemon) {
	t.Helper()
	d.store.SetSetting(SettingNotebookDreamingTimezone, "UTC")
}

// --- enqueueDueDailyNarrates cron due-math ---

// The first tick anchors the schedule at "now" and does NOT enqueue, so daemon
// startup never fires an immediate daily narrate; the first real pass lands at the
// next scheduled slot. NOTE: the daily narrate has no enabled gate and uses its own
// state file.
func TestEnqueueDueDailyNarratesFirstObservationAnchorsWithoutFiring(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	pinUTCSlot(t, d)
	d.markNotebookWorkspaceActivity("ws-A")

	d.enqueueDueDailyNarrates(mustTime(t, "2026-06-14T12:00:00Z"))

	state, _ := notebook.LoadNarrateCronState(root)
	if state.ScheduledFrom == "" {
		t.Fatal("first observation should anchor the schedule")
	}
	if taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
		t.Fatal("first observation enqueued a narrate before the first scheduled slot")
	}
	// The activity set must NOT be drained when the cron only anchors (no fire).
	if got := d.drainNotebookNarrateActivity(); len(got) != 1 || got[0] != "ws-A" {
		t.Fatalf("first observation drained the activity set: %v", got)
	}
}

// An anchored schedule whose next slot is still in the future does not enqueue, and
// leaves the anchor untouched.
func TestEnqueueDueDailyNarratesNotDueLeavesAnchor(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	pinUTCSlot(t, d)
	d.markNotebookWorkspaceActivity("ws-A")

	// Anchor at 04:00 UTC; the next "0 3 * * *" slot is the following day 03:00.
	if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-14T04:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	d.enqueueDueDailyNarrates(mustTime(t, "2026-06-14T05:00:00Z"))

	state, _ := notebook.LoadNarrateCronState(root)
	if state.ScheduledFrom != "2026-06-14T04:00:00Z" {
		t.Fatalf("not-due tick mutated the anchor: %q", state.ScheduledFrom)
	}
	if taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
		t.Fatal("not-due tick enqueued a narrate")
	}
}

// A due schedule fires once: it enqueues narrate_workspace for the active workspace
// (with the daily Meta flag set) and advances the anchor; a second tick the same day
// does NOT re-fire, and — because the set was cleared on the first fire — a later due
// day with no new activity enqueues nothing.
func TestEnqueueDueDailyNarratesDueFiresOnceAndClearsActivity(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	pinUTCSlot(t, d)
	// Live workspace row so the drain does not skip it as removed.
	d.store.AddWorkspace(&protocol.Workspace{ID: "ws-A", Title: "ws-A", Directory: t.TempDir()})
	d.markNotebookWorkspaceActivity("ws-A")
	// Block the narrate so the enqueued record stays observable.
	d.narrateWorkspaceExecution = blockingExecution(t)

	// Anchor a day back so 2026-06-14T03:00 is the due slot.
	if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-13T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	now := mustTime(t, "2026-06-14T12:00:00Z")
	d.enqueueDueDailyNarrates(now)

	state, _ := notebook.LoadNarrateCronState(root)
	if state.ScheduledFrom != now.UTC().Format(time.RFC3339) {
		t.Fatalf("due fire did not advance the anchor to now: %q", state.ScheduledFrom)
	}
	if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
		t.Fatal("due fire did not enqueue narrate_workspace for the active workspace")
	}
	task, err := d.compactRunner.Get(tasks.TaskID(notebookNarrateWorkspaceKind, "ws-A"))
	if err != nil || task == nil {
		t.Fatalf("get narrate task: %v", err)
	}
	if task.Meta[notebookNarrateMetaDailyPass] != "1" {
		t.Fatalf("daily narrate task missing the daily-pass Meta flag: %+v", task.Meta)
	}

	// The activity set was cleared on the fire, so a later due day with no new
	// activity advances the anchor but enqueues nothing new.
	d.store.AddWorkspace(&protocol.Workspace{ID: "ws-B", Title: "ws-B", Directory: t.TempDir()})
	d.enqueueDueDailyNarrates(mustTime(t, "2026-06-15T12:00:00Z"))
	if taskExists(t, d, notebookNarrateWorkspaceKind, "ws-B") {
		t.Fatal("a later due day with no new activity enqueued a narrate")
	}
}

// The gate is exact: only workspaces in the activity set are narrated. A due fire
// narrates the active ws-A and NOT the idle ws-B.
func TestEnqueueDueDailyNarratesGatesOnActivity(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	pinUTCSlot(t, d)
	d.store.AddWorkspace(&protocol.Workspace{ID: "ws-A", Title: "ws-A", Directory: t.TempDir()})
	d.store.AddWorkspace(&protocol.Workspace{ID: "ws-B", Title: "ws-B", Directory: t.TempDir()})
	d.narrateWorkspaceExecution = blockingExecution(t)

	// Only ws-A saw activity. ws-B is idle.
	d.markNotebookWorkspaceActivity("ws-A")

	if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-13T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	d.enqueueDueDailyNarrates(mustTime(t, "2026-06-14T12:00:00Z"))

	if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
		t.Fatal("active workspace ws-A was not narrated")
	}
	// Give the worker a beat; ws-B must never get a task.
	time.Sleep(20 * time.Millisecond)
	task, err := d.compactRunner.Get(tasks.TaskID(notebookNarrateWorkspaceKind, "ws-B"))
	if err != nil {
		t.Fatalf("get ws-B narrate: %v", err)
	}
	if task != nil {
		t.Fatalf("idle workspace ws-B was unexpectedly narrated: %+v", task)
	}
}

// A workspace in the activity set whose row was removed before the fire is skipped:
// its removal-boundary final retrospective already ran. The anchor still advances.
func TestEnqueueDueDailyNarratesSkipsRemovedWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	pinUTCSlot(t, d)
	d.narrateWorkspaceExecution = blockingExecution(t)
	// ws-gone was active but its row is gone (no AddWorkspace).
	d.markNotebookWorkspaceActivity("ws-gone")

	if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-13T03:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	now := mustTime(t, "2026-06-14T12:00:00Z")
	d.enqueueDueDailyNarrates(now)

	state, _ := notebook.LoadNarrateCronState(root)
	if state.ScheduledFrom != now.UTC().Format(time.RFC3339) {
		t.Fatalf("anchor did not advance on a fire that skipped a removed workspace: %q", state.ScheduledFrom)
	}
	time.Sleep(20 * time.Millisecond)
	task, err := d.compactRunner.Get(tasks.TaskID(notebookNarrateWorkspaceKind, "ws-gone"))
	if err != nil {
		t.Fatalf("get ws-gone narrate: %v", err)
	}
	if task != nil {
		t.Fatalf("removed workspace was narrated by the daily cron: %+v", task)
	}
}

// --- activity hooks ---

// A session Stop marks its workspace active (the handleStop path), so the daily cron
// will narrate it.
func TestHandleStopMarksWorkspaceActivity(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	installNotebookNarrationRunner(t, d)
	d.summarizeSessionExecution = blockingExecution(t)
	d.narrateWorkspaceExecution = blockingExecution(t)

	d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1"})

	got := d.drainNotebookNarrateActivity()
	if len(got) != 1 || got[0] != "ws-1" {
		t.Fatalf("stop did not mark ws-1 active: %v", got)
	}
}

// A content-CHANGING context update marks the workspace active; a no-op update
// (changed == false) does NOT — driven through the real updateWorkspaceContext
// handler so the hook is exercised at the genuine chokepoint.
func TestContextWriteMarksActivityOnlyWhenChanged(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")

	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-1"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	// A no-op update (nothing edited) does NOT mark activity.
	if _, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-1"}); err != nil || changed {
		t.Fatalf("expected no-op update (changed=false), got changed=%v err=%v", changed, err)
	}
	if got := d.drainNotebookNarrateActivity(); len(got) != 0 {
		t.Fatalf("a no-op context update marked activity: %v", got)
	}

	// A real edit -> changed -> marks activity.
	if err := os.WriteFile(checkout.Path, []byte("# Real shared goal\n"), 0o600); err != nil {
		t.Fatalf("edit checkout: %v", err)
	}
	if _, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-1"}); err != nil || !changed {
		t.Fatalf("expected changing update (changed=true), got changed=%v err=%v", changed, err)
	}
	got := d.drainNotebookNarrateActivity()
	if len(got) != 1 || got[0] != "workspace-1" {
		t.Fatalf("a content-changing context update did not mark workspace-1 active: %v", got)
	}
}

// --- executor relaxation (daily pass success semantics) ---

// A daily-flagged narrate whose agent leaves the block UNCHANGED goes DONE (not
// failed): a daily refresh that finds nothing new is a clean no-op.
func TestNarrateWorkspaceDailyPassUnchangedIsDone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatalf("mkdir journal: %v", err)
	}
	prior := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nalready narrated today\n\nsource: workspace:ws-1\n"
	if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed prior entry: %v", err)
	}

	// Daily-pass agent no-ops: leaves the block exactly as-is.
	d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "nothing new"}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{
		Meta: map[string]string{notebookNarrateMetaDailyPass: "1"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", tasks.StateDone)
}

// A daily-flagged narrate whose agent writes NO entry at all goes DONE (not failed):
// an absent block on a daily refresh is also a clean no-op.
func TestNarrateWorkspaceDailyPassAbsentIsDone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	// Daily-pass agent writes nothing.
	d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "no entry"}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{
		Meta: map[string]string{notebookNarrateMetaDailyPass: "1"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", tasks.StateDone)
}

// A daily-flagged REMOVAL pass keeps STRICT gating: an unchanged/absent block still
// FAILS, because the removal retrospective must actually be written.
func TestNarrateWorkspaceDailyFlagRemovalPassStillStrict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	// No workspace row for ws-removed -> IS_REMOVAL_PASS derived true; the daily flag
	// must not relax a removal pass.
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatalf("mkdir journal: %v", err)
	}
	prior := "## ws-removed — 2026-06-15\n<!-- attn:wsnarr:ws-removed -->\n\nprior\n\nsource: workspace:ws-removed\n"
	if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed prior entry: %v", err)
	}

	d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-removed", tasks.EnqueueOptions{
		Meta: map[string]string{notebookNarrateMetaDailyPass: "1"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookNarrateWorkspaceKind, "ws-removed")
	if task.State == tasks.StateDone {
		t.Fatal("daily flag wrongly relaxed a removal pass to done")
	}
}

// A NON-flagged routine (session-end) pass with an unchanged block still FAILS —
// unchanged behavior, preserving the retry-until-the-digest-lands property.
func TestNarrateWorkspaceRoutinePassUnchangedStillFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatalf("mkdir journal: %v", err)
	}
	prior := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nactive-day entry\n\nsource: workspace:ws-1\n"
	if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed prior entry: %v", err)
	}

	d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
	}

	// No daily Meta flag -> strict gating.
	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookNarrateWorkspaceKind, "ws-1")
	if !strings.Contains(task.LastError, "unchanged") {
		t.Fatalf("routine pass last error = %q, want unchanged rejection", task.LastError)
	}
}
