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
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	// keeperCompactUpdater is a PERSISTED updated_by_session_id sentinel, string-
	// matched in the frontend; migration 51 realigned rows from "attn-janitor".
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

// notebookTasksEnabled is the default-ON master switch for the keeper's
// background duties; it never gates the manual compaction command.
func (d *Daemon) notebookTasksEnabled() bool {
	if d.store == nil {
		return true
	}
	raw := strings.TrimSpace(d.store.GetSetting(SettingNotebookTasksEnabled))
	if raw == "" {
		return true
	}
	return parseBooleanSetting(raw)
}

// notebookSummariesEnabled is the digest duty's own switch; callers check the
// master switch too.
func (d *Daemon) notebookSummariesEnabled() bool {
	return d.notebookDutyEnabled(SettingNotebookSummarizeSessionEnabled)
}

// notebookWorkspaceNarrationEnabled is the curated-journal duty's own switch.
func (d *Daemon) notebookWorkspaceNarrationEnabled() bool {
	return d.notebookDutyEnabled(SettingNotebookNarrateWorkspaceEnabled)
}

// notebookDutyEnabled resolves a per-duty default-on switch, so every duty
// treats an absent setting identically.
func (d *Daemon) notebookDutyEnabled(settingKey string) bool {
	if d.store == nil {
		return true
	}
	raw := strings.TrimSpace(d.store.GetSetting(settingKey))
	if raw == "" {
		return true
	}
	return parseBooleanSetting(raw)
}

// legacyKeeperCompactSettingKey is retained ONLY for
// migrateKeeperCompactSettingKey. Never read it anywhere else.
const legacyKeeperCompactSettingKey = "workspace_context_janitor"

// renameSettingKey copies a legacy settings VALUE forward only when the current
// key is empty, then deletes the legacy row. Idempotent, because the daemon
// runs these at every start. Not a schema migration.
func (d *Daemon) renameSettingKey(legacy, current string) {
	if d.store == nil {
		return
	}
	value := d.store.GetSetting(legacy)
	if strings.TrimSpace(value) == "" {
		return // nothing to migrate (or already migrated + cleaned up)
	}
	if strings.TrimSpace(d.store.GetSetting(current)) == "" {
		d.store.SetSetting(current, value)
		d.logf("migrated setting %q -> %q", legacy, current)
	}
	d.store.DeleteSetting(legacy)
}

// migrateKeeperCompactSettingKey renames "workspace_context_janitor" to
// SettingKeeperCompact.
func (d *Daemon) migrateKeeperCompactSettingKey() {
	d.renameSettingKey(legacyKeeperCompactSettingKey, SettingKeeperCompact)
}

// compactContextKind is the runner task kind for workspace-context compaction.
const compactContextKind = "compact_context"

// forgetWorkspaceContextCompaction drops any in-flight or pending compaction and
// deletes its record. The single nil-safe entry point: the runner is built late
// in Start(), so no teardown callsite may touch d.jobQueue directly. Remove, not
// Cancel, which is a no-op for a queued task and never deletes the record.
func (d *Daemon) forgetWorkspaceContextCompaction(workspaceID string) {
	runner := d.jobQueueRef()
	if runner == nil {
		return
	}
	runner.RemoveByKey(compactContextKind, workspaceID)
}

// jobQueueRef reads the job-queue pointer under the read lock, so a concurrent
// startJobQueue swap is race-free. May be nil or disabled.
func (d *Daemon) jobQueueRef() *jobs.Runner {
	d.jobQueueMu.RLock()
	defer d.jobQueueMu.RUnlock()
	return d.jobQueue
}

// setJobQueue publishes a runner under the write lock; only startJobQueue calls it.
func (d *Daemon) setJobQueue(runner *jobs.Runner) {
	d.jobQueueMu.Lock()
	d.jobQueue = runner
	d.jobQueueMu.Unlock()
}

// startJobQueue constructs and starts the durable job queue. Disabled without a
// store, in which case compaction degrades to the inline fallback.
func (d *Daemon) startJobQueue() {
	// Empties the old table, so this is a no-op on every boot after the first.
	d.importLegacyTasks()
	// Jobs persist in the profile SQLite DB, so the enable gate is keyed on the
	// store alone — which lets reconcile (needed for any dead session) be an
	// ordinary job. The notebook-scoped handlers keep their own guards.
	opts := jobs.Options{Log: d.logf}
	if d.store != nil {
		opts.Store = d.newSQLJobStore()
	}
	// Register on a LOCAL pointer and publish once, so no concurrent reader ever
	// observes a half-registered runner.
	runner := jobs.New(opts)
	if !runner.Disabled() {
		if err := runner.RegisterWith(
			compactContextKind,
			d.compactContextHandler,
			jobs.HandlerConfig{Timeout: d.keeperCompactTimeoutDuration()},
		); err != nil {
			d.logf("keeper compact: register compact_context: %v", err)
		}
		// Both narration handlers run native-tools agents and verify a written file
		// rather than committing a read-back.
		if err := runner.RegisterWith(
			notebookSummarizeSessionKind,
			d.summarizeSessionHandler,
			jobs.HandlerConfig{Timeout: notebookSummarizeSessionTimeout},
		); err != nil {
			d.logf("notebook narration: register summarize_session: %v", err)
		}
		if err := runner.RegisterWith(
			notebookNarrateWorkspaceKind,
			d.narrateWorkspaceHandler,
			jobs.HandlerConfig{Timeout: notebookNarrateWorkspaceTimeout},
		); err != nil {
			d.logf("notebook narration: register narrate_workspace: %v", err)
		}
		// Session activity is gated on its own setting, not the notebook's, and is
		// coalesced per session.
		if err := runner.RegisterWith(
			sessionActivityKind,
			d.sessionActivityHandler,
			jobs.HandlerConfig{
				Timeout: sessionActivityTimeout,
				// Keeps concurrent sessions from fanning into unbounded subprocesses.
				MaxConcurrent: sessionActivityConcurrency,
			},
		); err != nil {
			d.logf("session activity: register session_activity: %v", err)
		}
		// Reconciliation runs regardless of notebook config and is the one kind that
		// wants real concurrency: a teardown can kill several sessions at once.
		if err := runner.RegisterWith(
			reconcileKind,
			d.reconcileJobHandler,
			jobs.HandlerConfig{
				Timeout:       ticketReconcileTimeout(),
				MaxConcurrent: ticketReconcileConcurrency,
			},
		); err != nil {
			d.logf("ticket reconcile: register reconcile: %v", err)
		}
		// Every recurring duty is a cron entry on this queue: one mechanism, one
		// durable record of when it next fires.
		if err := runner.RegisterCron(
			notebookCronKind,
			defaultNotebookCronInterval,
			d.notebookCronHandler,
			jobs.HandlerConfig{Timeout: notebookCronTickTimeout},
		); err != nil {
			d.logf("notebook cron: register tick: %v", err)
		}
		if err := runner.RegisterCron(
			sessionActivityScanKind,
			sessionActivityScanInterval,
			d.sessionActivityScanHandler,
			jobs.HandlerConfig{Timeout: sessionActivityScanTimeout},
		); err != nil {
			d.logf("session activity: register scan tick: %v", err)
		}
		// The app invocation log is the third periodic duty. It trims on the same
		// queue as the others so there is one place to look when it stops running.
		if err := runner.RegisterCron(
			appInvocationRetentionKind,
			appInvocationRetentionInterval,
			d.appInvocationRetentionHandler,
			jobs.HandlerConfig{Timeout: appInvocationRetentionTimeout},
		); err != nil {
			d.logf("apps: register invocation retention tick: %v", err)
		}
		// The event log's retention floor is the fourth. A consumer that stops
		// consuming grows the log for as long as it lasts and nothing else ever
		// says so, so the check is only skipped when it is deliberately turned off.
		if age := d.busPinAlarmAge(); age > 0 {
			if err := runner.RegisterCron(
				busPinAlarmKind,
				busPinAlarmInterval(age),
				d.busPinAlarmHandler,
				jobs.HandlerConfig{Timeout: busPinAlarmTimeout},
			); err != nil {
				d.logf("bus: register retention-pin alarm tick: %v", err)
			}
		}
		// The crew lifecycle is the fifth: it watches awake members' context
		// caches and the user's absence, and does nothing at all until one of
		// those is close to mattering.
		d.registerCrewLifecycleCron(runner)
		if err := runner.RegisterCron(
			automationScheduleKind,
			automationScheduleInterval,
			d.automationScheduleHandler,
			jobs.HandlerConfig{Timeout: automationScheduleTickTimeout},
		); err != nil {
			d.logf("automation schedule: register tick: %v", err)
		}
	}
	// OnChange may fire CONCURRENTLY from the dispatch goroutine and in-flight
	// runs, so the callback must be cheap, concurrency-safe, and non-blocking.
	runner.OnChange(func(jobID string) { d.publishFact(FactTaskChanged, jobID, nil) })
	// OnTerminalFailure fires exactly once per dead job, on the queue's goroutine
	// with a cloned record; the handler must stay non-blocking.
	runner.OnTerminalFailure(func(j *jobs.Job) { d.notifyTaskTerminalFailure(j) })
	d.setJobQueue(runner)
	if err := runner.Start(); err != nil {
		// A queue that failed to start still accepts Enqueue and writes rows, but
		// nothing dispatches them. This line is the whole diagnosis.
		d.logf("jobs: THE JOB QUEUE DID NOT START: %v — no background work and no periodic ticks will run until the daemon is restarted", err)
	}
}

// enqueueWorkspaceContextCompaction is THE trigger callsite. With a runner it
// coalesces a debounced per-workspace job; without one it compacts inline.
func (d *Daemon) enqueueWorkspaceContextCompaction(canonical *protocol.WorkspaceContext) {
	if canonical == nil || strings.TrimSpace(canonical.WorkspaceID) == "" {
		return
	}
	if !d.notebookTasksEnabled() {
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
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		// No durable queue, no debounce, no retry; the inline path owns the timeout.
		if _, err := d.runWorkspaceContextCompactionInline(context.Background(), config, canonical); err != nil {
			d.logf("keeper compact: inline compact %s: %v", canonical.WorkspaceID, err)
		}
		return
	}
	if _, err := runner.Enqueue(compactContextKind, jobs.EnqueueOptions{
		UniqueKey: canonical.WorkspaceID,
		Delay:     d.keeperCompactDebounceDuration(),
	}); err != nil {
		d.logf("keeper compact: enqueue %s: %v", canonical.WorkspaceID, err)
	}
}

// compactContextHandler is the compact_context job handler; the queue supplies
// the timeout context and the per-run CommitGuard.
func (d *Daemon) compactContextHandler(ctx context.Context, job *jobs.Job) (any, error) {
	// A run queued before the keeper was disabled must not fire after it. No-op
	// success, so the stale record retires rather than retrying.
	if !d.notebookTasksEnabled() {
		return nil, nil
	}
	workspaceID := jobSubject(job)
	config, err := d.keeperCompactConfig()
	if err != nil {
		return nil, err
	}
	if config.Agent == "" {
		return nil, errors.New("keeper compact is disabled")
	}
	canonical, err := d.store.GetWorkspaceContext(workspaceID)
	if err != nil {
		return nil, err
	}
	// Re-checked after the debounce: a doc edited back under the threshold must
	// not burn an LLM pass.
	if len([]byte(canonical.Content)) <= d.keeperCompactSizeThreshold() {
		return nil, nil
	}
	_, err = d.applyWorkspaceContextCompaction(ctx, config, canonical, job.CommitGuard)
	return nil, err
}

// runWorkspaceContextCompactionInline runs execute+validate+apply synchronously
// for the disabled-runner fallback and the manual command, on a throwaway
// CommitGuard. It is the SOLE timeout boundary for both inline callers.
func (d *Daemon) runWorkspaceContextCompactionInline(
	ctx context.Context,
	config keeperCompactConfig,
	canonical *protocol.WorkspaceContext,
) (*protocol.WorkspaceContextMaintenanceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, d.keeperCompactTimeoutDuration())
	defer cancel()
	return d.applyWorkspaceContextCompaction(ctx, config, canonical, &jobs.CommitGuard{})
}

// applyWorkspaceContextCompaction is the execute+validate+apply helper shared by
// all three callers. It commits under the CommitGuard, so a concurrent Cancel
// either fences the run before the durable write or waits for it untorn.
func (d *Daemon) applyWorkspaceContextCompaction(
	ctx context.Context,
	config keeperCompactConfig,
	canonical *protocol.WorkspaceContext,
	guard *jobs.CommitGuard,
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

	// The fence is entered BEFORE the test hook, so the hook blocks inside the
	// admitted commit and a test can prove the fence holds.
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
		// Checkout files are agent-owned and deliberately not rewritten: replacing one
		// could discard a write from an editor holding the old inode.
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
	// Native-tools mode: the agent reads the source and writes the candidate in a
	// writable scratch dir; the daemon owns validation and commit.
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

// keeperCompactPrompt is a format string: the two %s are the absolute source
// path (to read) and candidate path (to write).
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

func (d *Daemon) broadcastWorkspaceContextChanged(canonical *protocol.WorkspaceContext) {
	if canonical == nil {
		return
	}
	d.publishFact(FactWorkspaceContextChanged, canonical.WorkspaceID, protocol.WorkspaceContextChangedMessage{
		Event:              protocol.EventWorkspaceContextChanged,
		WorkspaceID:        canonical.WorkspaceID,
		Revision:           canonical.Revision,
		UpdatedBySessionID: canonical.UpdatedBySessionID,
		UpdatedAt:          canonical.UpdatedAt,
	})
}

func (d *Daemon) projectWorkspaceContextChanged(ev bus.Event) {
	msg, ok := decodeFact[protocol.WorkspaceContextChangedMessage](d, ev)
	if !ok {
		return
	}
	d.broadcastMessage(msg)
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
	// Remove first, so a queued run cannot fire after the manual one and
	// double-compact; it blocks until any in-flight run exits.
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
	// Checkouts are agent-owned and left untouched; see the compact path.
	d.broadcastWorkspaceContextChanged(updated)
	return &protocol.WorkspaceContextMaintenanceResult{
		Action:         protocol.WorkspaceContextMaintenanceActionRollback,
		WorkspaceID:    session.WorkspaceID,
		SourceRevision: current.Revision,
		ResultRevision: updated.Revision,
		Changed:        true,
	}, nil
}
