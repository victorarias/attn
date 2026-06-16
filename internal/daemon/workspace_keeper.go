package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tasks"
)

const (
	// keeperCompactUpdater is the updated_by_session_id sentinel written when the
	// keeper's compaction duty rewrites a workspace context. It is a PERSISTED
	// identifier (stored in workspace_contexts.updated_by_session_id and
	// string-matched in the frontend navigator); migration 51 realigns existing
	// rows from the old "attn-janitor" value to this one.
	keeperCompactUpdater          = "attn-keeper"
	defaultKeeperCompactThreshold = 12 * 1024
	defaultKeeperCompactDebounce  = 10 * time.Minute
	defaultKeeperCompactTimeout   = 5 * time.Minute
)

type keeperCompactConfig struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}

type keeperCompactExecution struct {
	Candidate          string
	ResolvedExecutable string
	Diagnostics        string
}

func parseKeeperCompactConfig(raw string) (keeperCompactConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return keeperCompactConfig{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config keeperCompactConfig
	if err := decoder.Decode(&config); err != nil {
		return keeperCompactConfig{}, fmt.Errorf("invalid keeper compact configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return keeperCompactConfig{}, fmt.Errorf("invalid keeper compact configuration: %w", err)
	}
	config.Agent = strings.TrimSpace(strings.ToLower(config.Agent))
	config.Model = strings.TrimSpace(config.Model)
	if config.Agent == "" || config.Model == "" {
		return keeperCompactConfig{}, errors.New("keeper compact requires both agent and model")
	}
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return keeperCompactConfig{}, fmt.Errorf("keeper compact agent is not installed: %s", config.Agent)
	}
	if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
		return keeperCompactConfig{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	if available, reason := agentdriver.HeadlessTaskAvailability(driver); !available {
		return keeperCompactConfig{}, fmt.Errorf("agent %s cannot run headless tasks: %s", config.Agent, reason)
	}
	return config, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("unexpected trailing JSON")
}

func (d *Daemon) validateKeeperCompactSetting(raw string) error {
	config, err := parseKeeperCompactConfig(raw)
	if err != nil || config.Agent == "" {
		return err
	}
	driver := agentdriver.Get(config.Agent)
	configured := ""
	if d.store != nil {
		configured = d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	}
	executable := driver.ResolveExecutable(configured)
	if _, err := exec.LookPath(executable); err != nil {
		return fmt.Errorf("keeper compact executable for %s was not found: %w", config.Agent, err)
	}
	return nil
}

func (d *Daemon) keeperCompactConfig() (keeperCompactConfig, error) {
	if d.store == nil {
		return keeperCompactConfig{}, errors.New("keeper compact settings unavailable")
	}
	return parseKeeperCompactConfig(d.store.GetSetting(SettingKeeperCompact))
}

// legacyKeeperCompactSettingKey is the pre-rename persisted settings key. It is
// retained ONLY so migrateKeeperCompactSettingKey can copy a user's configured
// agent/model forward to SettingKeeperCompact. Never read it anywhere else.
const legacyKeeperCompactSettingKey = "workspace_context_janitor"

// migrateKeeperCompactSettingKey performs the one-time rename of the persisted
// "workspace_context_janitor" setting to "workspace_keeper_compact"
// (SettingKeeperCompact). It runs at daemon start — and therefore again after
// every app rebuild, since the daemon survives rebuilds — so it MUST be
// idempotent. It copies the legacy value forward only when the legacy key holds
// a non-empty value AND the new key is still empty, so a re-run never clobbers a
// value the user has since set under the new key. After copying it deletes the
// stale legacy row so the migration is a true no-op on the next boot (and the
// dead key never appears in the broadcast settings map). This is a plain
// settings-value copy, NOT a schema migration — it is unrelated to
// schema_migrations.
func (d *Daemon) migrateKeeperCompactSettingKey() {
	if d.store == nil {
		return
	}
	legacy := d.store.GetSetting(legacyKeeperCompactSettingKey)
	if strings.TrimSpace(legacy) == "" {
		return // nothing to migrate (or already migrated + cleaned up)
	}
	if strings.TrimSpace(d.store.GetSetting(SettingKeeperCompact)) == "" {
		// Carry the raw legacy value forward to preserve it exactly.
		d.store.SetSetting(SettingKeeperCompact, legacy)
		d.logf("migrated setting %q -> %q", legacyKeeperCompactSettingKey, SettingKeeperCompact)
	}
	// Drop the stale row so the migration is a true no-op on the next boot.
	d.store.DeleteSetting(legacyKeeperCompactSettingKey)
}

// compactContextKind is the runner task kind for workspace-context compaction.
const compactContextKind = "compact_context"

// forgetWorkspaceContextCompaction drops any in-flight or pending compaction for a
// workspace AND deletes its task record. It is the single nil-safe entry point:
// the runner is constructed late in Start() (startCompactRunner, after the
// websocket server is already accepting connections), so every teardown callsite —
// including the ones reachable over the websocket before the runner exists — must
// route through here rather than dereferencing d.compactRunner directly. Remove
// (not Cancel) is used so a removed workspace leaves no orphan compact_context
// record behind: Cancel alone is a no-op for a queued task and never deletes the
// record.
func (d *Daemon) forgetWorkspaceContextCompaction(workspaceID string) {
	runner := d.compactRunnerRef()
	if runner == nil {
		return
	}
	runner.Remove(tasks.TaskID(compactContextKind, workspaceID))
}

// compactRunnerRef reads the compaction/narration runner pointer under the
// read lock, so a concurrent startCompactRunner pointer swap is race-free. It
// returns the same value the field holds (possibly nil before startCompactRunner
// runs, possibly a disabled runner when no notebook root resolves) — callers keep
// their existing nil/Disabled guards.
func (d *Daemon) compactRunnerRef() *tasks.Runner {
	d.compactRunnerMu.RLock()
	defer d.compactRunnerMu.RUnlock()
	return d.compactRunner
}

// setCompactRunner publishes a freshly built runner under the write lock. Only
// startCompactRunner calls it; everything else reads via compactRunnerRef.
func (d *Daemon) setCompactRunner(runner *tasks.Runner) {
	d.compactRunnerMu.Lock()
	d.compactRunner = runner
	d.compactRunnerMu.Unlock()
}

// startCompactRunner constructs and starts the durable compaction runner. The
// runner root is the notebook root; when it cannot be resolved the runner is
// disabled and the daemon degrades to the inline fallback (see
// enqueueWorkspaceContextCompaction). New always returns a non-nil value, so the
// Cancel/Enqueue callsites can call d.compactRunner unconditionally.
func (d *Daemon) startCompactRunner() {
	root, _ := d.notebookRoot()
	// Build and register on a LOCAL pointer, then publish it once under the write
	// lock. Registering on the local (not the published field) keeps a concurrent
	// reader from ever observing a half-registered runner, and the single
	// setCompactRunner swap is what Stop()/enqueue/forget synchronize against.
	runner := tasks.New(tasks.Options{Root: root, Log: d.logf})
	if !runner.Disabled() {
		if err := runner.RegisterWithTimeout(
			compactContextKind,
			d.compactContextExecutor,
			d.keeperCompactTimeoutDuration(),
		); err != nil {
			d.logf("keeper compact: register compact_context: %v", err)
		}
		// Notebook narration shares the same durable runner (same root, same
		// disabled-when-no-root gate). Both narration executors run native-tools
		// agents and verify a written file rather than committing a read-back.
		if err := runner.RegisterWithTimeout(
			notebookSummarizeSessionKind,
			d.summarizeSessionExecutor,
			notebookSummarizeSessionTimeout,
		); err != nil {
			d.logf("notebook narration: register summarize_session: %v", err)
		}
		if err := runner.RegisterWithTimeout(
			notebookNarrateWorkspaceKind,
			d.narrateWorkspaceExecutor,
			notebookNarrateWorkspaceTimeout,
		); err != nil {
			d.logf("notebook narration: register narrate_workspace: %v", err)
		}
		// Dreaming's nightly harvest folds onto the same runner. The default 5-min
		// timeout is ample for the harvest's file I/O; the cron enqueuer
		// (startNotebookCronEnqueuer) dispatches the harvest_dream task when due.
		if err := runner.Register(harvestDreamKind, d.harvestDreamExecutor); err != nil {
			d.logf("dreaming: register harvest_dream: %v", err)
		}
	}
	// Surface lifecycle transitions to any open task panel. OnChange fires
	// SYNCHRONOUSLY inside the runner's single worker goroutine, so the callback
	// must be cheap and non-blocking: broadcastNotebookTasksChanged ->
	// broadcastMessage -> wsHub.BroadcastValue uses a non-blocking send that drops
	// on a full broadcast channel, so it can never stall the worker.
	runner.OnChange(func() { d.broadcastNotebookTasksChanged() })
	d.setCompactRunner(runner)
	_ = runner.Start()
}

// enqueueWorkspaceContextCompaction is THE trigger callsite. It carries the
// size-threshold gate, the non-empty-workspaceID guard, and the loaded-config
// guard that used to live in the pre-runner scheduler. When the runner is
// enabled it coalesces a debounced compaction onto the per-workspace task;
// otherwise (no notebook root) it runs the compaction inline/synchronously so
// compaction still happens.
func (d *Daemon) enqueueWorkspaceContextCompaction(canonical *protocol.WorkspaceContext) {
	if canonical == nil || strings.TrimSpace(canonical.WorkspaceID) == "" {
		return
	}
	config, err := d.keeperCompactConfig()
	if err != nil {
		d.logf("keeper compact: configuration: %v", err)
		return
	}
	if config.Agent == "" {
		return
	}
	if len([]byte(canonical.Content)) <= d.keeperCompactSizeThreshold() {
		return
	}
	runner := d.compactRunnerRef()
	if runner == nil || runner.Disabled() {
		// Inline fallback: no durable queue, no debounce, no retry. Compaction
		// still happens, synchronously, on the trigger.
		// runWorkspaceContextCompactionInline applies the per-run timeout.
		if _, err := d.runWorkspaceContextCompactionInline(context.Background(), config, canonical); err != nil {
			d.logf("keeper compact: inline compact %s: %v", canonical.WorkspaceID, err)
		}
		return
	}
	if _, err := runner.Enqueue(compactContextKind, canonical.WorkspaceID, tasks.EnqueueOptions{
		Debounce: d.keeperCompactDebounceDuration(),
	}); err != nil {
		d.logf("keeper compact: enqueue %s: %v", canonical.WorkspaceID, err)
	}
}

// compactContextExecutor is the runner-registered ExecutorFunc for
// compact_context. The runner supplies the timeout context and the per-run
// CommitGuard; this body loads the current context, re-checks the size threshold
// (the doc may have shrunk during the debounce window), runs the agentic
// compaction, validates, and commits under the guard.
func (d *Daemon) compactContextExecutor(ctx context.Context, task *tasks.Task) error {
	workspaceID := task.Subject
	config, err := d.keeperCompactConfig()
	if err != nil {
		return err
	}
	if config.Agent == "" {
		return errors.New("keeper compact is disabled")
	}
	canonical, err := d.store.GetWorkspaceContext(workspaceID)
	if err != nil {
		return err
	}
	// Re-check the size gate after the debounce: a doc edited down below the
	// threshold should not burn an LLM pass. No-op success.
	if len([]byte(canonical.Content)) <= d.keeperCompactSizeThreshold() {
		return nil
	}
	_, err = d.applyWorkspaceContextCompaction(ctx, config, canonical, task.CommitGuard)
	return err
}

// runWorkspaceContextCompactionInline runs execute+validate+apply synchronously
// without the durable queue. It is used by the disabled-runner fallback and by
// the manual `attn workspace context compact` command, which must return a
// result synchronously. It uses a throwaway CommitGuard (no concurrent Cancel
// fences an inline run, but the apply path is shared so it must take a guard).
//
// It applies the same per-run timeout the runner-driven path gets from
// RegisterWithTimeout, so a hung/runaway agent cannot block an inline run (the
// disabled-runner fallback) or the synchronous manual-command IPC response
// indefinitely. This is the SOLE timeout boundary for both inline callers.
func (d *Daemon) runWorkspaceContextCompactionInline(
	ctx context.Context,
	config keeperCompactConfig,
	canonical *protocol.WorkspaceContext,
) (*protocol.WorkspaceContextMaintenanceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, d.keeperCompactTimeoutDuration())
	defer cancel()
	return d.applyWorkspaceContextCompaction(ctx, config, canonical, &tasks.CommitGuard{})
}

// applyWorkspaceContextCompaction is the single execute+validate+apply helper
// shared by the runner executor, the inline fallback, and the manual command.
// It runs the agentic compaction, validates the candidate, then commits under
// the supplied CommitGuard so a concurrent Cancel either fences the run cleanly
// before the durable write or waits for it to finish untorn.
func (d *Daemon) applyWorkspaceContextCompaction(
	ctx context.Context,
	config keeperCompactConfig,
	canonical *protocol.WorkspaceContext,
	guard *tasks.CommitGuard,
) (*protocol.WorkspaceContextMaintenanceResult, error) {
	if canonical == nil || strings.TrimSpace(canonical.WorkspaceID) == "" {
		return nil, errors.New("workspace context is required")
	}
	execute := d.executeKeeperCompact
	if d.workspaceContextCompactionExecution != nil {
		execute = d.workspaceContextCompactionExecution
	}
	execution, err := execute(ctx, config, canonical)
	if err != nil {
		return nil, err
	}
	candidate := execution.Candidate
	if err := validateKeeperCompactCandidate(canonical.Content, candidate); err != nil {
		return nil, err
	}

	// Enter the commit fence BEFORE the test hook. Once admitted, a concurrent
	// Cancel must wait for the durable write to finish untorn; the test hook then
	// blocks inside the admitted commit so a test can prove the fence holds. If a
	// Cancel already fired before Enter, skip the durable write entirely.
	if !guard.Enter() {
		return nil, context.Canceled
	}
	defer guard.Leave()
	if d.workspaceContextBeforeKeeperApply != nil {
		d.workspaceContextBeforeKeeperApply()
	}

	updated, changed, err := d.store.ApplyKeeperCompactResult(
		canonical.WorkspaceID,
		candidate,
		keeperCompactUpdater,
		canonical.Revision,
		config.Agent,
		config.Model,
	)
	if err != nil {
		return nil, err
	}
	result := &protocol.WorkspaceContextMaintenanceResult{
		Action:         protocol.WorkspaceContextMaintenanceActionCompact,
		WorkspaceID:    canonical.WorkspaceID,
		SourceRevision: canonical.Revision,
		ResultRevision: updated.Revision,
		Changed:        changed,
		Agent:          protocol.Ptr(config.Agent),
		AgentModel:     protocol.Ptr(config.Model),
	}
	if changed {
		d.refreshCleanWorkspaceContextCheckouts(updated)
		d.broadcastWorkspaceContextChanged(updated)
	}
	if execution.ResolvedExecutable != "" {
		d.logf(
			"keeper compact: workspace=%s agent=%s model=%s executable=%s changed=%t diagnostics=%s",
			canonical.WorkspaceID,
			config.Agent,
			config.Model,
			execution.ResolvedExecutable,
			changed,
			execution.Diagnostics,
		)
	}
	return result, nil
}

func (d *Daemon) keeperCompactSizeThreshold() int {
	if d.keeperCompactThreshold > 0 {
		return d.keeperCompactThreshold
	}
	return defaultKeeperCompactThreshold
}

func (d *Daemon) keeperCompactDebounceDuration() time.Duration {
	if d.keeperCompactDebounce > 0 {
		return d.keeperCompactDebounce
	}
	return defaultKeeperCompactDebounce
}

func (d *Daemon) keeperCompactTimeoutDuration() time.Duration {
	if d.keeperCompactTimeout > 0 {
		return d.keeperCompactTimeout
	}
	return defaultKeeperCompactTimeout
}

func (d *Daemon) executeKeeperCompact(
	ctx context.Context,
	config keeperCompactConfig,
	canonical *protocol.WorkspaceContext,
) (keeperCompactExecution, error) {
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return keeperCompactExecution{}, fmt.Errorf("keeper compact agent not found: %s", config.Agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return keeperCompactExecution{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	configured := d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	resolvedExecutable := driver.ResolveExecutable(configured)
	executablePath, err := exec.LookPath(resolvedExecutable)
	if err != nil {
		return keeperCompactExecution{}, fmt.Errorf("resolve %s executable: %w", config.Agent, err)
	}
	tempDir, err := os.MkdirTemp("", "attn-keeper-compact-*")
	if err != nil {
		return keeperCompactExecution{}, fmt.Errorf("create keeper compact workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	sourcePath := filepath.Join(tempDir, "source.md")
	candidatePath := filepath.Join(tempDir, "candidate.md")
	if err := os.WriteFile(sourcePath, []byte(canonical.Content), 0o600); err != nil {
		return keeperCompactExecution{}, fmt.Errorf("write keeper compact source: %w", err)
	}
	// Native-tools mode: the agent gets its own file tools and a writable scratch
	// dir (WorkDir). It reads the source and writes the candidate itself; the
	// daemon reads the candidate back and owns validation + commit.
	request := agentdriver.HeadlessTaskRequest{
		Executable: executablePath,
		Model:      config.Model,
		Prompt:     fmt.Sprintf(keeperCompactPrompt, sourcePath, candidatePath),
		WorkDir:    tempDir,
	}
	result, err := provider.RunHeadlessTask(ctx, request)
	if err != nil {
		return keeperCompactExecution{
			ResolvedExecutable: executablePath,
			Diagnostics:        result.Diagnostics,
		}, err
	}
	candidate, err := os.ReadFile(candidatePath)
	if errors.Is(err, os.ErrNotExist) {
		return keeperCompactExecution{
			ResolvedExecutable: executablePath,
			Diagnostics:        result.Diagnostics,
		}, errors.New("keeper compact completed without replacing the context")
	} else if err != nil {
		return keeperCompactExecution{}, fmt.Errorf("read keeper compact candidate: %w", err)
	}
	return keeperCompactExecution{
		Candidate:          string(candidate),
		ResolvedExecutable: executablePath,
		Diagnostics:        result.Diagnostics,
	}, nil
}

// keeperCompactPrompt is a format string: the two %s are the absolute
// source path (to read) and candidate path (to write). Absolute paths are robust
// regardless of how the agent resolves cwd, and both providers' file tools accept
// absolute paths inside the writable workspace.
const keeperCompactPrompt = `Compact the workspace context file without changing its meaning.

Read the file at %s. Write the complete compacted result to %s. Do not modify any other file. Write the candidate file exactly once with the full result; do not leave it empty.

Preserve:
- Area and all current truths
- unresolved open edges
- decisions and constraints
- source links and useful timeline turning points

You may shorten prose, deduplicate facts, and merge overlapping Threads. Remove stale or superseded material only when the document itself establishes that it is stale or superseded.

Do not add facts, dates, chronology, causality, ownership, thread structure, or conclusions. If uncertain, preserve the content. A byte-identical copy is valid.

The result must contain exactly one "# Workspace Context" heading, a non-empty "## Area", and a non-empty "## Current Picture".`

func validateKeeperCompactCandidate(source, candidate string) error {
	if len([]byte(candidate)) > len([]byte(source)) {
		return fmt.Errorf("keeper compact candidate grew from %d to %d bytes", len([]byte(source)), len([]byte(candidate)))
	}
	lines := splitMarkdownLines(candidate)
	if firstNonEmptyLine(lines) != "# Workspace Context" {
		return errors.New(`keeper compact candidate must start with "# Workspace Context"`)
	}
	if countExactLine(lines, "# Workspace Context") != 1 {
		return errors.New(`keeper compact candidate must contain exactly one "# Workspace Context" heading`)
	}
	if countTopLevelHeadings(lines) != 1 {
		return errors.New("keeper compact candidate must contain exactly one top-level heading")
	}
	for _, heading := range []string{"## Area", "## Current Picture"} {
		if countExactLine(lines, heading) != 1 {
			return fmt.Errorf("keeper compact candidate must contain exactly one %q heading", heading)
		}
		if strings.TrimSpace(markdownSectionContent(lines, heading)) == "" {
			return fmt.Errorf("keeper compact candidate section %q is empty", heading)
		}
	}
	return nil
}

func splitMarkdownLines(content string) []string {
	raw := strings.Split(content, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, strings.TrimSpace(line))
	}
	return lines
}

func firstNonEmptyLine(lines []string) string {
	for _, line := range lines {
		if line != "" {
			return line
		}
	}
	return ""
}

func countExactLine(lines []string, want string) int {
	count := 0
	for _, line := range lines {
		if line == want {
			count++
		}
	}
	return count
}

func countTopLevelHeadings(lines []string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			count++
		}
	}
	return count
}

func markdownSectionContent(lines []string, heading string) string {
	start := -1
	for index, line := range lines {
		if line == heading {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	var content []string
	for _, line := range lines[start:] {
		if strings.HasPrefix(line, "## ") {
			break
		}
		content = append(content, line)
	}
	return strings.Join(content, "\n")
}

func (d *Daemon) refreshCleanWorkspaceContextCheckouts(canonical *protocol.WorkspaceContext) {
	// Existing checkout files are agent-owned working copies. Replacing one can
	// discard a write from an editor that still holds the old inode open. Leave
	// all checkouts untouched; their prior metadata makes them stale against the
	// new canonical revision, so the normal refresh/conflict workflow preserves
	// both clean and modified local state.
}

func (d *Daemon) broadcastWorkspaceContextChanged(canonical *protocol.WorkspaceContext) {
	d.broadcastMessage(protocol.WorkspaceContextChangedMessage{
		Event:              protocol.EventWorkspaceContextChanged,
		WorkspaceID:        canonical.WorkspaceID,
		Revision:           canonical.Revision,
		UpdatedBySessionID: canonical.UpdatedBySessionID,
		UpdatedAt:          canonical.UpdatedAt,
	})
}

func (d *Daemon) compactWorkspaceContextForSession(
	ctx context.Context,
	sourceSessionID string,
) (*protocol.WorkspaceContextMaintenanceResult, error) {
	session, err := d.resolveWorkspaceContextSource(sourceSessionID)
	if err != nil {
		return nil, err
	}
	canonical, err := d.store.GetWorkspaceContext(session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(canonical.Content) == "" {
		return nil, errors.New("workspace context is empty")
	}
	config, err := d.keeperCompactConfig()
	if err != nil {
		return nil, err
	}
	if config.Agent == "" {
		return nil, errors.New("keeper compact is disabled")
	}
	// Drop any pending/in-flight debounced run so the manual command is
	// authoritative, then run the inline execute+validate+apply path synchronously
	// so the command can return a result to the user. Remove cancels any in-flight
	// run (blocking until it exits) and deletes a pending record, so a queued run
	// cannot fire again after the manual one and double-compact.
	d.forgetWorkspaceContextCompaction(session.WorkspaceID)
	return d.runWorkspaceContextCompactionInline(ctx, config, canonical)
}

func (d *Daemon) rollbackWorkspaceContextForSession(
	sourceSessionID string,
) (*protocol.WorkspaceContextMaintenanceResult, error) {
	session, err := d.resolveWorkspaceContextSource(sourceSessionID)
	if err != nil {
		return nil, err
	}
	current, err := d.store.GetWorkspaceContext(session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	updated, err := d.store.RestoreKeeperCompactBackup(session.WorkspaceID, session.ID)
	if err != nil {
		return nil, err
	}
	d.refreshCleanWorkspaceContextCheckouts(updated)
	d.broadcastWorkspaceContextChanged(updated)
	return &protocol.WorkspaceContextMaintenanceResult{
		Action:         protocol.WorkspaceContextMaintenanceActionRollback,
		WorkspaceID:    session.WorkspaceID,
		SourceRevision: current.Revision,
		ResultRevision: updated.Revision,
		Changed:        true,
	}, nil
}
