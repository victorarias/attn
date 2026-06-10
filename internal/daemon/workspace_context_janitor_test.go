package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	janitorSource = `# Workspace Context

## Area

Workspace context product work and operational use.

## Current Picture

The current document is longer than it needs to be, but its facts remain useful.

## Threads

### Context model
- Now: The area-map format is being implemented.
`
	janitorCandidate = `# Workspace Context

## Area

Workspace context product work and use.

## Current Picture

The area-map format is being implemented.
`
)

func TestParseWorkspaceContextJanitorConfig(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		config, err := parseWorkspaceContextJanitorConfig("")
		if err != nil || config.Agent != "" || config.Model != "" {
			t.Fatalf("config = %+v, err = %v", config, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		config, err := parseWorkspaceContextJanitorConfig(`{"agent":"CODEX","model":"gpt-test"}`)
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
			if _, err := parseWorkspaceContextJanitorConfig(raw); err == nil {
				t.Fatalf("parseWorkspaceContextJanitorConfig(%q) succeeded", raw)
			}
		})
	}
}

func TestValidateWorkspaceContextJanitorCandidate(t *testing.T) {
	if err := validateWorkspaceContextJanitorCandidate(janitorSource, janitorCandidate); err != nil {
		t.Fatalf("valid candidate rejected: %v", err)
	}
	if err := validateWorkspaceContextJanitorCandidate(janitorSource, janitorSource); err != nil {
		t.Fatalf("identical candidate rejected: %v", err)
	}
	legacy := "# Goal\n\nDo the work.\n"
	if err := validateWorkspaceContextJanitorCandidate(legacy, legacy); err == nil {
		t.Fatal("identical legacy candidate unexpectedly accepted")
	}

	for name, candidate := range map[string]string{
		"growth": janitorSource + "\nMore content that makes the result larger.\n",
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
			if err := validateWorkspaceContextJanitorCandidate(janitorSource, candidate); err == nil {
				t.Fatalf("candidate unexpectedly accepted:\n%s", candidate)
			}
		})
	}
}

func TestWorkspaceContextJanitorCompactsAndLeavesExistingCheckoutsStale(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-clean", "workspace-1")
	setupWorkspaceContextSession(t, d, "session-modified", "workspace-1")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)

	canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", janitorSource, "session-clean", 0)
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
	localEdit := janitorSource + "\nLocal unsaved fact.\n"
	if err := os.WriteFile(modified.Path, []byte(localEdit), 0o600); err != nil {
		t.Fatalf("edit modified checkout: %v", err)
	}
	d.workspaceContextJanitorExecutor = func(
		context.Context,
		workspaceContextJanitorConfig,
		*protocol.WorkspaceContext,
	) (workspaceContextJanitorExecution, error) {
		return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
	}
	result, err := d.runWorkspaceContextJanitor(context.Background(), canonical)
	if err != nil {
		t.Fatalf("run janitor: %v", err)
	}
	if !result.Changed || result.SourceRevision != 1 || result.ResultRevision != 2 ||
		protocol.Deref(result.Agent) != "codex" || protocol.Deref(result.AgentModel) != "gpt-test" {
		t.Fatalf("result = %+v", result)
	}
	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get compacted context: %v", err)
	}
	if current.Content != janitorCandidate || current.UpdatedBySessionID != workspaceContextJanitorUpdater {
		t.Fatalf("current = %+v", current)
	}
	cleanContent, err := os.ReadFile(clean.Path)
	if err != nil {
		t.Fatalf("read clean checkout: %v", err)
	}
	if string(cleanContent) != janitorSource {
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
	backup, err := d.store.GetWorkspaceContextJanitorBackup("workspace-1")
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}
	if backup.SourceContent != janitorSource || backup.SourceRevision != 1 || backup.ResultRevision != 2 {
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
	if current.Content != janitorSource || current.UpdatedBySessionID != "session-clean" {
		t.Fatalf("restored context = %+v", current)
	}
}

func TestManualWorkspaceContextCompactionCancelsScheduledRun(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
	d.workspaceContextJanitorThreshold = 1
	d.workspaceContextJanitorDebounce = time.Hour
	canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", janitorSource, "session-1", 0)
	if err != nil {
		t.Fatalf("seed context: %v", err)
	}
	d.scheduleWorkspaceContextJanitor(canonical)
	d.workspaceContextJanitorExecutor = func(
		context.Context,
		workspaceContextJanitorConfig,
		*protocol.WorkspaceContext,
	) (workspaceContextJanitorExecution, error) {
		return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
	}

	if _, err := d.compactWorkspaceContextForSession(context.Background(), "session-1"); err != nil {
		t.Fatalf("manual compaction: %v", err)
	}
	d.workspaceContextJanitorMu.Lock()
	defer d.workspaceContextJanitorMu.Unlock()
	if scheduled := d.workspaceContextJanitorTimers["workspace-1"]; scheduled != nil {
		t.Fatal("manual compaction left the automatic timer scheduled")
	}
}

func TestWorkspaceContextJanitorRejectsStaleRevision(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
	canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", janitorSource, "session-1", 0)
	if err != nil {
		t.Fatalf("seed context: %v", err)
	}
	later := janitorSource + "\nA later verified edit.\n"
	d.workspaceContextJanitorExecutor = func(
		context.Context,
		workspaceContextJanitorConfig,
		*protocol.WorkspaceContext,
	) (workspaceContextJanitorExecution, error) {
		if _, _, updateErr := d.store.UpdateWorkspaceContext("workspace-1", later, "session-1", 1); updateErr != nil {
			return workspaceContextJanitorExecution{}, updateErr
		}
		return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
	}

	if _, err := d.runWorkspaceContextJanitor(context.Background(), canonical); !errors.Is(err, store.ErrWorkspaceContextConflict) {
		t.Fatalf("run error = %v, want revision conflict", err)
	}
	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if current.Content != later || current.Revision != 2 {
		t.Fatalf("current context = %+v", current)
	}
	if _, err := d.store.GetWorkspaceContextJanitorBackup("workspace-1"); !errors.Is(err, store.ErrWorkspaceContextJanitorBackupNotFound) {
		t.Fatalf("backup error = %v, want not found", err)
	}
}

func TestWorkspaceContextJanitorTimeoutAndCancellation(t *testing.T) {
	for name, stop := range map[string]func(*Daemon){
		"timeout":                func(d *Daemon) {},
		"workspace cancellation": func(d *Daemon) { d.cancelWorkspaceContextJanitor("workspace-1") },
	} {
		t.Run(name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
			d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
			canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", janitorSource, "session-1", 0)
			if err != nil {
				t.Fatalf("seed context: %v", err)
			}
			if name == "timeout" {
				d.workspaceContextJanitorTimeout = 20 * time.Millisecond
			} else {
				d.workspaceContextJanitorTimeout = time.Second
			}
			started := make(chan struct{})
			d.workspaceContextJanitorExecutor = func(
				ctx context.Context,
				_ workspaceContextJanitorConfig,
				_ *protocol.WorkspaceContext,
			) (workspaceContextJanitorExecution, error) {
				close(started)
				<-ctx.Done()
				return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
			}
			done := make(chan error, 1)
			go func() {
				_, runErr := d.runWorkspaceContextJanitor(context.Background(), canonical)
				done <- runErr
			}()
			<-started
			stop(d)
			select {
			case runErr := <-done:
				if !errors.Is(runErr, context.DeadlineExceeded) && !errors.Is(runErr, context.Canceled) {
					t.Fatalf("run error = %v", runErr)
				}
			case <-time.After(time.Second):
				t.Fatal("janitor run did not stop")
			}
			current, getErr := d.store.GetWorkspaceContext("workspace-1")
			if getErr != nil {
				t.Fatalf("get current context: %v", getErr)
			}
			if current.Content != janitorSource || current.Revision != 1 {
				t.Fatalf("context changed after canceled run: %+v", current)
			}
		})
	}
}

func TestWorkspaceContextJanitorCancellationWaitsForAdmittedCommit(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
	canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", janitorSource, "session-1", 0)
	if err != nil {
		t.Fatalf("seed context: %v", err)
	}
	d.workspaceContextJanitorExecutor = func(
		context.Context,
		workspaceContextJanitorConfig,
		*protocol.WorkspaceContext,
	) (workspaceContextJanitorExecution, error) {
		return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
	}
	commitStarted := make(chan struct{})
	releaseCommit := make(chan struct{})
	d.workspaceContextBeforeJanitorApply = func() {
		close(commitStarted)
		<-releaseCommit
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := d.runWorkspaceContextJanitor(context.Background(), canonical)
		runDone <- runErr
	}()
	<-commitStarted

	cancelDone := make(chan struct{})
	go func() {
		d.cancelWorkspaceContextJanitor("workspace-1")
		close(cancelDone)
	}()
	select {
	case <-cancelDone:
		t.Fatal("cancellation returned before the admitted commit finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-runDone; err != nil {
		t.Fatalf("admitted commit failed: %v", err)
	}
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not return after commit completion")
	}
	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if current.Content != janitorCandidate || current.Revision != 2 {
		t.Fatalf("admitted commit was not applied: %+v", current)
	}
}

func TestWorkspaceDeletionCancelsJanitorBeforeRemovingContext(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
	canonical, _, err := d.store.UpdateWorkspaceContext("workspace-1", janitorSource, "session-1", 0)
	if err != nil {
		t.Fatalf("seed context: %v", err)
	}
	started := make(chan struct{})
	d.workspaceContextJanitorExecutor = func(
		ctx context.Context,
		_ workspaceContextJanitorConfig,
		_ *protocol.WorkspaceContext,
	) (workspaceContextJanitorExecution, error) {
		close(started)
		<-ctx.Done()
		return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := d.runWorkspaceContextJanitor(context.Background(), canonical)
		done <- runErr
	}()
	<-started

	d.dissociateSessionFromWorkspace("session-1")
	if d.store.GetWorkspace("workspace-1") != nil || d.store.HasWorkspaceContext("workspace-1") {
		t.Fatal("workspace deletion returned before removing the workspace context")
	}
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("run error = %v, want canceled", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("janitor did not stop before workspace deletion returned")
	}
	if _, err := d.store.GetWorkspaceContextJanitorBackup("workspace-1"); !errors.Is(err, store.ErrWorkspaceContextJanitorBackupNotFound) {
		t.Fatalf("backup error = %v, want not found", err)
	}
}

func TestWorkspaceContextJanitorStaleDebounceCallbackDoesNotReplaceNewTimer(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
	d.workspaceContextJanitorThreshold = 1
	d.workspaceContextJanitorDebounce = time.Hour
	canonical := &protocol.WorkspaceContext{
		WorkspaceID: "workspace-1",
		Content:     janitorSource,
		Revision:    1,
	}

	d.scheduleWorkspaceContextJanitor(canonical)
	d.workspaceContextJanitorMu.Lock()
	first := d.workspaceContextJanitorTimers["workspace-1"]
	d.workspaceContextJanitorMu.Unlock()
	if first == nil {
		t.Fatal("first timer was not scheduled")
	}

	d.scheduleWorkspaceContextJanitor(canonical)
	d.workspaceContextJanitorMu.Lock()
	second := d.workspaceContextJanitorTimers["workspace-1"]
	d.workspaceContextJanitorMu.Unlock()
	if second == nil || second == first {
		t.Fatal("second publish did not replace the debounce timer")
	}

	d.runScheduledWorkspaceContextJanitor("workspace-1", first)
	d.workspaceContextJanitorMu.Lock()
	current := d.workspaceContextJanitorTimers["workspace-1"]
	d.workspaceContextJanitorMu.Unlock()
	if current != second {
		t.Fatal("stale debounce callback removed the current timer")
	}
	d.cancelWorkspaceContextJanitor("workspace-1")
}

func TestWorkspaceContextJanitorAllowsOnlyOneInvocation(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	setupWorkspaceContextSession(t, d, "session-2", "workspace-2")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
	first, _, err := d.store.UpdateWorkspaceContext("workspace-1", janitorSource, "session-1", 0)
	if err != nil {
		t.Fatalf("seed first context: %v", err)
	}
	second, _, err := d.store.UpdateWorkspaceContext("workspace-2", janitorSource, "session-2", 0)
	if err != nil {
		t.Fatalf("seed second context: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	d.workspaceContextJanitorExecutor = func(
		ctx context.Context,
		_ workspaceContextJanitorConfig,
		_ *protocol.WorkspaceContext,
	) (workspaceContextJanitorExecution, error) {
		close(started)
		select {
		case <-release:
			return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
		case <-ctx.Done():
			return workspaceContextJanitorExecution{}, ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := d.runWorkspaceContextJanitor(context.Background(), first)
		done <- runErr
	}()
	<-started
	if _, err := d.runWorkspaceContextJanitor(context.Background(), second); err == nil ||
		!strings.Contains(err.Error(), "already running") {
		t.Fatalf("second run error = %v, want already running", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first run: %v", err)
	}
}

func TestWorkspaceContextJanitorDebouncesAgentPublish(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingWorkspaceContextJanitor, `{"agent":"codex","model":"gpt-test"}`)
	d.workspaceContextJanitorThreshold = 1
	d.workspaceContextJanitorDebounce = 20 * time.Millisecond
	calls := make(chan struct{}, 1)
	d.workspaceContextJanitorExecutor = func(
		context.Context,
		workspaceContextJanitorConfig,
		*protocol.WorkspaceContext,
	) (workspaceContextJanitorExecution, error) {
		calls <- struct{}{}
		return workspaceContextJanitorExecution{Candidate: janitorCandidate}, nil
	}

	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-1"})
	if err != nil {
		t.Fatalf("checkout context: %v", err)
	}
	if err := os.WriteFile(checkout.Path, []byte(janitorSource), 0o600); err != nil {
		t.Fatalf("edit context: %v", err)
	}
	if _, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-1"}); err != nil || !changed {
		t.Fatalf("publish context: changed=%v err=%v", changed, err)
	}

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("debounced janitor did not run")
	}
	deadline := time.Now().Add(time.Second)
	for {
		current, getErr := d.store.GetWorkspaceContext("workspace-1")
		if getErr != nil {
			t.Fatalf("get current context: %v", getErr)
		}
		if current.Revision == 2 {
			if current.Content != janitorCandidate {
				t.Fatalf("compacted content = %q", current.Content)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("context revision = %d, want 2", current.Revision)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		d.workspaceContextJanitorMu.Lock()
		running := d.workspaceContextJanitorRunning
		d.workspaceContextJanitorMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("janitor run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
