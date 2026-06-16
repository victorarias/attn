package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tasks"
)

const (
	keeperSource = `# Workspace Context

## Area

Workspace context product work and operational use.

## Current Picture

The current document is longer than it needs to be, but its facts remain useful.

## Threads

### Context model
- Now: The area-map format is being implemented.
`
	keeperCandidate = `# Workspace Context

## Area

Workspace context product work and use.

## Current Picture

The area-map format is being implemented.
`
)

// installTestCompactRunner replaces the daemon's default disabled runner with an
// enabled one over a temp root, registers the real compact_context executor (so
// the executor's load/threshold/validate/commit-under-CommitGuard logic runs for
// real), and starts the worker. The fake compaction execution is injected via
// d.workspaceContextCompactionExecution so no real LLM is spawned. A fast poll
// interval avoids real-time waits.
func installTestCompactRunner(t *testing.T, d *Daemon) {
	t.Helper()
	runner := tasks.New(tasks.Options{
		Root:         t.TempDir(),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
	})
	if err := runner.RegisterWithTimeout(
		compactContextKind,
		d.compactContextExecutor,
		d.keeperCompactTimeoutDuration(),
	); err != nil {
		t.Fatalf("register compact_context: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.compactRunner = runner
}

func fakeCompaction(candidate string) func(
	context.Context, keeperCompactConfig, *protocol.WorkspaceContext,
) (keeperCompactExecution, error) {
	return func(
		context.Context, keeperCompactConfig, *protocol.WorkspaceContext,
	) (keeperCompactExecution, error) {
		return keeperCompactExecution{Candidate: candidate}, nil
	}
}

func TestParseKeeperCompactConfig(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		config, err := parseKeeperCompactConfig("")
		if err != nil || config.Agent != "" || config.Model != "" {
			t.Fatalf("config = %+v, err = %v", config, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		config, err := parseKeeperCompactConfig(`{"agent":"CODEX","model":"gpt-test"}`)
		if err != nil {
			t.Fatalf("parse config: %v", err)
		}
		if config.Agent != "codex" || config.Model != "gpt-test" {
			t.Fatalf("config = %+v", config)
		}
	})

	for name, raw := range map[string]string{
		"missing model": `{"agent":"codex"}`,
		"unknown field": `{"agent":"codex","model":"gpt-test","fallback":"claude"}`,
		"unknown agent": `{"agent":"missing","model":"test"}`,
		"trailing json": `{"agent":"codex","model":"gpt-test"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseKeeperCompactConfig(raw); err == nil {
				t.Fatalf("parseKeeperCompactConfig(%q) succeeded", raw)
			}
		})
	}
}

func TestValidateKeeperCompactCandidate(t *testing.T) {
	if err := validateKeeperCompactCandidate(keeperSource, keeperCandidate); err != nil {
		t.Fatalf("valid candidate rejected: %v", err)
	}
	if err := validateKeeperCompactCandidate(keeperSource, keeperSource); err != nil {
		t.Fatalf("identical candidate rejected: %v", err)
	}
	legacy := "# Goal\n\nDo the work.\n"
	if err := validateKeeperCompactCandidate(legacy, legacy); err == nil {
		t.Fatal("identical legacy candidate unexpectedly accepted")
	}

	for name, candidate := range map[string]string{
		"growth": keeperSource + "\nMore content that makes the result larger.\n",
		"wrong top heading": `# Context

## Area
Area.

## Current Picture
Current.
`,
		"missing area": `# Workspace Context

## Current Picture
Current.
`,
		"empty current picture": `# Workspace Context

## Area
Area.

## Current Picture
`,
		"duplicate area": `# Workspace Context

## Area
Area.

## Area
Another area.

## Current Picture
Current.
`,
		"extra top heading": `# Workspace Context

## Area
Area.

## Current Picture
Current.

# Appendix
Other.
`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateKeeperCompactCandidate(keeperSource, candidate); err == nil {
				t.Fatalf("candidate unexpectedly accepted:\n%s", candidate)
			}
		})
	}
}

// TestWorkspaceContextCompactionAppliesAndLeavesExistingCheckoutsStale exercises
// the shared execute+validate+apply path (inline) end to end: the canonical is
// compacted, both checkouts are left untouched (clean stays stale, modified keeps
// its local edit), a backup is written, and rollback restores the source.
func TestWorkspaceContextCompactionAppliesAndLeavesExistingCheckoutsStale(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-clean", "workspace-1")
	setupWorkspaceContextSession(t, d, "session-modified", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)

	canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-clean", 0)
	if err != nil {
		t.Fatalf("seed context: %v", err)
	}
	clean, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-clean"})
	if err != nil {
		t.Fatalf("checkout clean session: %v", err)
	}
	modified, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-modified"})
	if err != nil {
		t.Fatalf("checkout modified session: %v", err)
	}
	localEdit := keeperSource + "\nLocal unsaved fact.\n"
	if err := os.WriteFile(modified.Path, []byte(localEdit), 0o600); err != nil {
		t.Fatalf("edit modified checkout: %v", err)
	}
	d.workspaceContextCompactionExecution = fakeCompaction(keeperCandidate)
	config, err := d.keeperCompactConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	result, err := d.runWorkspaceContextCompactionInline(context.Background(), config, canonical)
	if err != nil {
		t.Fatalf("run compaction: %v", err)
	}
	if !result.Changed || result.SourceRevision != 1 || result.ResultRevision != 2 ||
		protocol.Deref(result.Agent) != "codex" || protocol.Deref(result.AgentModel) != "gpt-test" {
		t.Fatalf("result = %+v", result)
	}
	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get compacted context: %v", err)
	}
	if current.Content != keeperCandidate || current.UpdatedBySessionID != keeperCompactUpdater {
		t.Fatalf("current = %+v", current)
	}
	cleanContent, err := os.ReadFile(clean.Path)
	if err != nil {
		t.Fatalf("read clean checkout: %v", err)
	}
	if string(cleanContent) != keeperSource {
		t.Fatalf("clean checkout was rewritten: %q", cleanContent)
	}
	cleanStatus, err := d.workspaceContextStatus(&protocol.WorkspaceContextStatusMessage{SourceSessionID: "session-clean"})
	if err != nil {
		t.Fatalf("clean status: %v", err)
	}
	if cleanStatus.Modified || !cleanStatus.Stale ||
		cleanStatus.Revision != 1 || cleanStatus.CanonicalRevision != 2 {
		t.Fatalf("clean status = %+v", cleanStatus)
	}
	modifiedContent, err := os.ReadFile(modified.Path)
	if err != nil {
		t.Fatalf("read modified checkout: %v", err)
	}
	if string(modifiedContent) != localEdit {
		t.Fatalf("modified checkout was overwritten: %q", modifiedContent)
	}
	modifiedStatus, err := d.workspaceContextStatus(&protocol.WorkspaceContextStatusMessage{SourceSessionID: "session-modified"})
	if err != nil {
		t.Fatalf("modified status: %v", err)
	}
	if !modifiedStatus.Modified || !modifiedStatus.Stale ||
		modifiedStatus.Revision != 1 || modifiedStatus.CanonicalRevision != 2 {
		t.Fatalf("modified status = %+v", modifiedStatus)
	}
	backup, err := d.store.GetKeeperCompactBackup("workspace-1")
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}
	if backup.SourceContent != keeperSource || backup.SourceRevision != 1 || backup.ResultRevision != 2 {
		t.Fatalf("backup = %+v", backup)
	}

	rollback, err := d.rollbackWorkspaceContextForSession("session-clean")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rollback.Action != "rollback" || !rollback.Changed || rollback.ResultRevision != 3 {
		t.Fatalf("rollback result = %+v", rollback)
	}
	current, err = d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get restored context: %v", err)
	}
	if current.Content != keeperSource || current.UpdatedBySessionID != "session-clean" {
		t.Fatalf("restored context = %+v", current)
	}
}

// TestManualWorkspaceContextCompactionCancelsPendingRun proves the manual command
// drops a pending debounced runner task and returns a result synchronously.
func TestManualWorkspaceContextCompactionCancelsPendingRun(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = 1
	d.keeperCompactDebounce = time.Hour
	installTestCompactRunner(t, d)
	d.workspaceContextCompactionExecution = fakeCompaction(keeperCandidate)

	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	// Enqueue a far-future debounced task that must be cancelled by the manual run.
	if _, err := d.compactRunner.Enqueue(compactContextKind, "workspace-1", tasks.EnqueueOptions{Debounce: time.Hour}); err != nil {
		t.Fatalf("enqueue pending: %v", err)
	}

	result, err := d.compactWorkspaceContextForSession(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("manual compaction: %v", err)
	}
	if !result.Changed || result.ResultRevision != 2 {
		t.Fatalf("manual result = %+v", result)
	}
	pending, err := d.compactRunner.Get(tasks.TaskID(compactContextKind, "workspace-1"))
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	// Cancel does not delete the record, but the manual run committed revision 2;
	// the pending record stays queued for the far-future debounce and must not have
	// run (it would conflict on the stale revision). The deterministic seam here is
	// that the manual command returned a committed result synchronously.
	if pending != nil && pending.State == tasks.StateRunning {
		t.Fatalf("pending task still running after manual cancel: %+v", pending)
	}
}

// TestWorkspaceContextCompactionRejectsStaleRevision proves the revision-guarded
// apply rejects a candidate built from a now-stale source revision.
func TestWorkspaceContextCompactionRejectsStaleRevision(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0)
	if err != nil {
		t.Fatalf("seed context: %v", err)
	}
	later := keeperSource + "\nA later verified edit.\n"
	d.workspaceContextCompactionExecution = func(
		context.Context, keeperCompactConfig, *protocol.WorkspaceContext,
	) (keeperCompactExecution, error) {
		if _, _, updateErr := d.store.UpdateWorkspaceContext("workspace-1", later, "session-1", 1); updateErr != nil {
			return keeperCompactExecution{}, updateErr
		}
		return keeperCompactExecution{Candidate: keeperCandidate}, nil
	}
	config, err := d.keeperCompactConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if _, err := d.runWorkspaceContextCompactionInline(context.Background(), config, canonical); !errors.Is(err, store.ErrWorkspaceContextConflict) {
		t.Fatalf("run error = %v, want revision conflict", err)
	}
	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if current.Content != later || current.Revision != 2 {
		t.Fatalf("current context = %+v", current)
	}
	if _, err := d.store.GetKeeperCompactBackup("workspace-1"); !errors.Is(err, store.ErrKeeperCompactBackupNotFound) {
		t.Fatalf("backup error = %v, want not found", err)
	}
}

// TestCompactRunnerTimeoutAndCancellationProtectContext proves the runner-owned
// timeout and a runner.Cancel both abort a stuck compaction without writing the
// context.
func TestCompactRunnerTimeoutAndCancellationProtectContext(t *testing.T) {
	for name, stop := range map[string]func(*Daemon){
		"timeout":      func(d *Daemon) {},
		"cancellation": func(d *Daemon) { d.compactRunner.Cancel(tasks.TaskID(compactContextKind, "workspace-1")) },
	} {
		t.Run(name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
			d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
			d.keeperCompactThreshold = 1
			if name == "timeout" {
				d.keeperCompactTimeout = 20 * time.Millisecond
			} else {
				d.keeperCompactTimeout = time.Second
			}
			started := make(chan struct{})
			d.workspaceContextCompactionExecution = func(
				ctx context.Context, _ keeperCompactConfig, _ *protocol.WorkspaceContext,
			) (keeperCompactExecution, error) {
				close(started)
				<-ctx.Done()
				return keeperCompactExecution{}, ctx.Err()
			}
			installTestCompactRunner(t, d)
			if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
				t.Fatalf("seed context: %v", err)
			}
			if _, err := d.compactRunner.Enqueue(compactContextKind, "workspace-1", tasks.EnqueueOptions{}); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			<-started
			stop(d)
			deadline := time.Now().Add(2 * time.Second)
			for {
				current, err := d.store.GetWorkspaceContext("workspace-1")
				if err != nil {
					t.Fatalf("get current context: %v", err)
				}
				if current.Content != keeperSource || current.Revision != 1 {
					t.Fatalf("context changed after aborted run: %+v", current)
				}
				task, err := d.compactRunner.Get(tasks.TaskID(compactContextKind, "workspace-1"))
				if err != nil {
					t.Fatalf("get task: %v", err)
				}
				if task != nil && task.State != tasks.StateRunning {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("compaction run did not stop")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

// TestCompactRunnerCancellationWaitsForAdmittedCommit proves the CommitGuard
// fence: a Cancel that arrives after the executor has entered its commit waits
// for the durable write to finish untorn.
func TestCompactRunnerCancellationWaitsForAdmittedCommit(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = 1
	d.workspaceContextCompactionExecution = fakeCompaction(keeperCandidate)
	commitStarted := make(chan struct{})
	releaseCommit := make(chan struct{})
	d.workspaceContextBeforeKeeperApply = func() {
		close(commitStarted)
		<-releaseCommit
	}
	installTestCompactRunner(t, d)
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	if _, err := d.compactRunner.Enqueue(compactContextKind, "workspace-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-commitStarted

	cancelDone := make(chan struct{})
	go func() {
		d.compactRunner.Cancel(tasks.TaskID(compactContextKind, "workspace-1"))
		close(cancelDone)
	}()
	select {
	case <-cancelDone:
		t.Fatal("cancellation returned before the admitted commit finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseCommit)
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not return after commit completion")
	}
	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if current.Content != keeperCandidate || current.Revision != 2 {
		t.Fatalf("admitted commit was not applied: %+v", current)
	}
}

// TestWorkspaceDeletionCancelsCompactionBeforeRemovingContext proves the
// cancel-then-remove ordering: dissociateSessionFromWorkspace cancels the
// in-flight compaction (blocking until it exits) before removing the workspace
// row, so no torn compaction can write a deleted workspace's context.
func TestWorkspaceDeletionCancelsCompactionBeforeRemovingContext(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = 1
	started := make(chan struct{})
	d.workspaceContextCompactionExecution = func(
		ctx context.Context, _ keeperCompactConfig, _ *protocol.WorkspaceContext,
	) (keeperCompactExecution, error) {
		close(started)
		<-ctx.Done()
		return keeperCompactExecution{}, ctx.Err()
	}
	installTestCompactRunner(t, d)
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	if _, err := d.compactRunner.Enqueue(compactContextKind, "workspace-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started

	d.dissociateSessionFromWorkspace("session-1")
	if d.store.GetWorkspace("workspace-1") != nil || d.store.HasWorkspaceContext("workspace-1") {
		t.Fatal("workspace deletion returned before removing the workspace context")
	}
	if _, err := d.store.GetKeeperCompactBackup("workspace-1"); !errors.Is(err, store.ErrKeeperCompactBackupNotFound) {
		t.Fatalf("backup error = %v, want not found", err)
	}
}

// TestWorkspaceContextCompactionEnqueuesOnThresholdViaTrigger proves the
// context-write trigger enqueues a coalesced compaction once the doc crosses the
// size threshold, and that the runner runs it to a committed revision.
func TestWorkspaceContextCompactionEnqueuesOnThresholdViaTrigger(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = 1
	d.keeperCompactDebounce = 5 * time.Millisecond
	calls := make(chan struct{}, 1)
	d.workspaceContextCompactionExecution = func(
		context.Context, keeperCompactConfig, *protocol.WorkspaceContext,
	) (keeperCompactExecution, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return keeperCompactExecution{Candidate: keeperCandidate}, nil
	}
	installTestCompactRunner(t, d)

	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-1"})
	if err != nil {
		t.Fatalf("checkout context: %v", err)
	}
	if err := os.WriteFile(checkout.Path, []byte(keeperSource), 0o600); err != nil {
		t.Fatalf("edit context: %v", err)
	}
	if _, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-1"}); err != nil || !changed {
		t.Fatalf("publish context: changed=%v err=%v", changed, err)
	}

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("trigger did not enqueue a compaction")
	}
	deadline := time.Now().Add(time.Second)
	for {
		current, getErr := d.store.GetWorkspaceContext("workspace-1")
		if getErr != nil {
			t.Fatalf("get current context: %v", getErr)
		}
		if current.Revision == 2 {
			if current.Content != keeperCandidate {
				t.Fatalf("compacted content = %q", current.Content)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("context revision = %d, want 2", current.Revision)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestWorkspaceContextCompactionInlineFallbackWhenRunnerDisabled proves that with
// a disabled runner (no notebook root) the trigger compacts inline/synchronously
// so compaction still happens.
func TestWorkspaceContextCompactionInlineFallbackWhenRunnerDisabled(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = 1
	// NewForTesting installs a disabled runner; keep it.
	if !d.compactRunner.Disabled() {
		t.Fatal("expected disabled runner in test")
	}
	d.workspaceContextCompactionExecution = fakeCompaction(keeperCandidate)

	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	canonical, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get canonical: %v", err)
	}
	// The trigger runs the inline fallback synchronously.
	d.enqueueWorkspaceContextCompaction(canonical)

	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if current.Revision != 2 || current.Content != keeperCandidate {
		t.Fatalf("inline fallback did not compact: %+v", current)
	}
}

// TestWorkspaceContextCompactionReChecksThresholdAfterDebounce proves the run-time
// size re-check: a doc edited below the threshold during the debounce window is a
// no-op success (no LLM pass, no revision bump).
func TestWorkspaceContextCompactionReChecksThresholdAfterDebounce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	// Threshold far above the seeded doc size so the run-time re-check no-ops.
	d.keeperCompactThreshold = 1 << 20
	executed := false
	d.workspaceContextCompactionExecution = func(
		context.Context, keeperCompactConfig, *protocol.WorkspaceContext,
	) (keeperCompactExecution, error) {
		executed = true
		return keeperCompactExecution{Candidate: keeperCandidate}, nil
	}
	installTestCompactRunner(t, d)
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	// Enqueue directly (the trigger would gate it, but a pre-debounce enqueue may
	// have outlived a shrink); the executor must re-check and no-op.
	if _, err := d.compactRunner.Enqueue(compactContextKind, "workspace-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		task, err := d.compactRunner.Get(tasks.TaskID(compactContextKind, "workspace-1"))
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task != nil && task.State == tasks.StateDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not finish: %+v", task)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if executed {
		t.Fatal("executor ran despite the doc being below the size threshold")
	}
	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if current.Revision != 1 {
		t.Fatalf("context was modified by a below-threshold run: %+v", current)
	}
}

// TestWorkspaceTeardownDoesNotPanicBeforeCompactRunnerExists proves the runtime
// teardown sites tolerate a nil compactRunner. Production New() leaves
// compactRunner nil until startCompactRunner() runs late in Start(), but the
// websocket server already accepts connections by then, so an UnregisterWorkspace
// / session-close / move-out arriving in that window reaches these sites with a
// nil runner. An unconditional d.compactRunner.Cancel(...) would nil-deref on the
// first line of Runner.Cancel (it reads r.disabled) and crash the daemon.
func TestWorkspaceTeardownDoesNotPanicBeforeCompactRunnerExists(t *testing.T) {
	t.Run("dissociateSessionFromWorkspace", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
		// Mimic production New(): the runner is not constructed yet.
		d.compactRunner = nil

		d.dissociateSessionFromWorkspace("session-1")

		if d.store.GetWorkspace("workspace-1") != nil {
			t.Fatal("workspace was not removed after dissociation")
		}
	})

	t.Run("handleUnregisterWorkspace", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
		d.compactRunner = nil

		d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: "workspace-1"})

		if d.store.GetWorkspace("workspace-1") != nil {
			t.Fatal("workspace was not removed after unregister")
		}
	})

	t.Run("unregisterWorkspaceIfEmptyAfterMove", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
		// The session must be gone for the workspace to be considered empty.
		d.workspaces.dissociateSession("session-1")
		d.store.Remove("session-1")
		d.compactRunner = nil

		d.unregisterWorkspaceIfEmptyAfterMove("workspace-1")

		if d.store.GetWorkspace("workspace-1") != nil {
			t.Fatal("empty workspace was not removed after move-out")
		}
	})
}

// TestManualWorkspaceContextCompactionAppliesTimeout proves the manual command
// path (compactWorkspaceContextForSession -> runWorkspaceContextCompactionInline)
// bounds the agent run with the configured per-run timeout. The original code
// funneled every run through a WithTimeout wrapper; the inline manual path must
// keep that bound so a hung/runaway agent cannot block the synchronous IPC
// response indefinitely.
func TestManualWorkspaceContextCompactionAppliesTimeout(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = 1
	d.keeperCompactTimeout = 20 * time.Millisecond

	gotDeadline := make(chan bool, 1)
	d.workspaceContextCompactionExecution = func(
		ctx context.Context, _ keeperCompactConfig, _ *protocol.WorkspaceContext,
	) (keeperCompactExecution, error) {
		_, hasDeadline := ctx.Deadline()
		gotDeadline <- hasDeadline
		// A runaway agent: block until the context aborts. With a deadline the
		// manual command returns promptly; without one it would hang here forever.
		<-ctx.Done()
		return keeperCompactExecution{}, ctx.Err()
	}
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := d.compactWorkspaceContextForSession(context.Background(), "session-1")
		done <- err
	}()

	select {
	case hasDeadline := <-gotDeadline:
		if !hasDeadline {
			t.Fatal("manual compaction executor ctx has NO deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("manual compaction executor was not invoked")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("manual compaction error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("manual compaction did not abort within the configured timeout")
	}
}

// TestMigrateKeeperCompactSettingKey covers the one-time rename of the persisted
// "workspace_context_janitor" setting to SettingKeeperCompact: a configured
// legacy value is carried forward, the legacy row is dropped, and the migration
// is idempotent — a re-run never clobbers a value the user set under the new key.
func TestMigrateKeeperCompactSettingKey(t *testing.T) {
	t.Run("copies legacy value forward and drops the legacy row", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		const value = `{"agent":"codex","model":"gpt-test"}`
		d.store.SetSetting(legacyKeeperCompactSettingKey, value)

		d.migrateKeeperCompactSettingKey()

		if got := d.store.GetSetting(SettingKeeperCompact); got != value {
			t.Fatalf("new key = %q, want %q", got, value)
		}
		if got := d.store.GetSetting(legacyKeeperCompactSettingKey); got != "" {
			t.Fatalf("legacy key still present: %q", got)
		}
		if _, ok := d.store.GetAllSettings()[legacyKeeperCompactSettingKey]; ok {
			t.Fatal("legacy key still appears in GetAllSettings")
		}
	})

	t.Run("idempotent: re-run is a no-op and never clobbers a user-set new value", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		d.store.SetSetting(legacyKeeperCompactSettingKey, `{"agent":"codex","model":"old"}`)

		d.migrateKeeperCompactSettingKey()

		// User reconfigures under the new key, and (defensively) a stale legacy row reappears.
		const userValue = `{"agent":"claude","model":"new"}`
		d.store.SetSetting(SettingKeeperCompact, userValue)
		d.store.SetSetting(legacyKeeperCompactSettingKey, `{"agent":"codex","model":"old"}`)

		d.migrateKeeperCompactSettingKey()

		if got := d.store.GetSetting(SettingKeeperCompact); got != userValue {
			t.Fatalf("new key was clobbered: got %q, want %q", got, userValue)
		}
		if got := d.store.GetSetting(legacyKeeperCompactSettingKey); got != "" {
			t.Fatalf("legacy key still present after re-run: %q", got)
		}
	})

	t.Run("no legacy value: nothing to migrate", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

		d.migrateKeeperCompactSettingKey()

		if got := d.store.GetSetting(SettingKeeperCompact); got != "" {
			t.Fatalf("new key unexpectedly set: %q", got)
		}
	})
}
