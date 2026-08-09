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

// notebookTasksEnabled reports whether the keeper's async background duties
// (summarize_session, narrate_workspace, compact_context) are enabled. This is
// the single master switch behind SettingNotebookTasksEnabled. Default ON: a
// nil store or a blank/unset value means enabled, so existing installs keep
// running the keeper without an opt-in; only an explicit "false" disables the
// whole group. It gates the BACKGROUND enqueue and executor paths only — the
// user-triggered inline/manual compaction command is an explicit action and is
// never gated by this toggle.
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

// notebookSummariesEnabled reports whether the keeper's per-session digest duty
// is enabled. It is an independent, default-on opt-out beneath the keeper master
// switch: callers check both so the master can still stop every duty at once.
func (d *Daemon) notebookSummariesEnabled() bool {
	return d.notebookDutyEnabled(SettingNotebookSummarizeSessionEnabled)
}

// notebookWorkspaceNarrationEnabled reports whether the keeper's curated-journal
// duty is enabled. Like summaries, it is an independent, default-on opt-out beneath
// the keeper master switch.
func (d *Daemon) notebookWorkspaceNarrationEnabled() bool {
	return d.notebookDutyEnabled(SettingNotebookNarrateWorkspaceEnabled)
}

// notebookDutyEnabled resolves a per-duty default-on switch. Keeping the default
// policy here makes every keeper duty treat absent settings identically.
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

// legacyKeeperCompactSettingKey is the pre-rename persisted settings key. It is
// retained ONLY so migrateKeeperCompactSettingKey can copy a user's configured
// agent/model forward to SettingKeeperCompact. Never read it anywhere else.
const legacyKeeperCompactSettingKey = "workspace_context_janitor"

// renameSettingKey performs a one-time settings-value rename: it copies the legacy
// key's value forward to current ONLY when legacy is non-empty AND current is still
// empty (so a re-run never clobbers a value the user has since set under the new
// key), then deletes the stale legacy row so the migration is a true no-op on the
// next boot (and the dead key never appears in the broadcast settings map). It is
// nil-store-safe and idempotent — required because the daemon runs these renames at
// every start, including after each app rebuild. This is a plain settings-value
// copy, NOT a schema migration; it is unrelated to schema_migrations.
func (d *Daemon) renameSettingKey(legacy, current string) {
	if d.store == nil {
		return
	}
	value := d.store.GetSetting(legacy)
	if strings.TrimSpace(value) == "" {
		return // nothing to migrate (or already migrated + cleaned up)
	}
	if strings.TrimSpace(d.store.GetSetting(current)) == "" {
		// Carry the raw legacy value forward to preserve it exactly.
		d.store.SetSetting(current, value)
		d.logf("migrated setting %q -> %q", legacy, current)
	}
	d.store.DeleteSetting(legacy)
}

// migrateKeeperCompactSettingKey performs the one-time rename of the persisted
// "workspace_context_janitor" setting to SettingKeeperCompact. See renameSettingKey
// for the idempotent copy-forward-then-reap contract.
func (d *Daemon) migrateKeeperCompactSettingKey() {
	d.renameSettingKey(legacyKeeperCompactSettingKey, SettingKeeperCompact)
}

// compactContextKind is the runner task kind for workspace-context compaction.
const compactContextKind = "compact_context"

// forgetWorkspaceContextCompaction drops any in-flight or pending compaction for a
// workspace AND deletes its task record. It is the single nil-safe entry point:
// the runner is constructed late in Start() (startJobQueue, after the
// websocket server is already accepting connections), so every teardown callsite —
// including the ones reachable over the websocket before the runner exists — must
// route through here rather than dereferencing d.jobQueue directly. Remove
// (not Cancel) is used so a removed workspace leaves no orphan compact_context
// record behind: Cancel alone is a no-op for a queued task and never deletes the
// record.
func (d *Daemon) forgetWorkspaceContextCompaction(workspaceID string) {
	runner := d.jobQueueRef()
	if runner == nil {
		return
	}
	runner.RemoveByKey(compactContextKind, workspaceID)
}

// jobQueueRef reads the job-queue pointer under the read lock, so a concurrent
// startJobQueue pointer swap is race-free. It returns the same value the field
// holds (possibly nil before startJobQueue runs, possibly a disabled queue when
// there is no store) — callers keep their existing nil/Disabled guards.
func (d *Daemon) jobQueueRef() *jobs.Runner {
	d.jobQueueMu.RLock()
	defer d.jobQueueMu.RUnlock()
	return d.jobQueue
}

// setJobQueue publishes a freshly built runner under the write lock. Only
// startJobQueue calls it; everything else reads via jobQueueRef.
func (d *Daemon) setJobQueue(runner *jobs.Runner) {
	d.jobQueueMu.Lock()
	d.jobQueue = runner
	d.jobQueueMu.Unlock()
}

// startJobQueue constructs and starts the daemon's durable job queue: the four
// background kinds (compact_context, summarize_session, narrate_workspace,
// reconcile) plus the two periodic cron entries. It is disabled when there is no
// store to persist to, in which case compaction degrades to the inline fallback
// (see enqueueWorkspaceContextCompaction). New always returns a non-nil value,
// so the Cancel/Enqueue callsites can call d.jobQueue unconditionally.
func (d *Daemon) startJobQueue() {
	// Carry anything the retired task runner still owed onto the queue. It empties
	// the old table, so this is a no-op on every boot after the first.
	d.importLegacyTasks()
	// Jobs persist in the profile SQLite DB via the injected store, NOT under the
	// notebook root — so the queue's enable gate is keyed only on the store, not on
	// a resolvable notebook root. That gate-drop is what lets the reconcile kind
	// (which must run for any dead session, notebook or not) be an ordinary job
	// (docs/plans/2026-07-02-bg-task-notifications.md). The notebook-scoped
	// handlers (compact_context, summarize_session, narrate_workspace) keep their
	// own no-op-when-no-notebook guards.
	opts := jobs.Options{Log: d.logf}
	if d.store != nil {
		opts.Store = d.newSQLJobStore()
	}
	// Build and register on a LOCAL pointer, then publish it once under the write
	// lock. Registering on the local (not the published field) keeps a concurrent
	// reader from ever observing a half-registered runner, and the single
	// setJobQueue swap is what Stop()/enqueue/forget synchronize against.
	runner := jobs.New(opts)
	if !runner.Disabled() {
		if err := runner.RegisterWith(
			compactContextKind,
			d.compactContextHandler,
			jobs.HandlerConfig{Timeout: d.keeperCompactTimeoutDuration()},
		); err != nil {
			d.logf("keeper compact: register compact_context: %v", err)
		}
		// Notebook narration shares the same durable queue (same root, same
		// disabled-when-no-root gate). Both narration handlers run native-tools
		// agents and verify a written file rather than committing a read-back.
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
		// Session activity joins the same queue. It is gated on its own setting
		// rather than the notebook's, and it is coalesced per session, so a burst
		// of transcript movement collapses into one run.
		if err := runner.RegisterWith(
			sessionActivityKind,
			d.sessionActivityHandler,
			jobs.HandlerConfig{
				Timeout: sessionActivityTimeout,
				// Several sessions can move at once while the dashboard is open, and
				// a line the user is looking at is worth generating promptly. The cap
				// is what keeps that from becoming an unbounded fan of subprocesses.
				MaxConcurrent: sessionActivityConcurrency,
			},
		); err != nil {
			d.logf("session activity: register session_activity: %v", err)
		}
		// Orphaned-ticket reconciliation joins the same durable queue as a job
		// kind. It runs regardless of notebook config and is the one kind that wants
		// real concurrency: a workspace teardown can kill several delegated sessions
		// at once, so cap it at ticketReconcileConcurrency classifier subprocesses
		// (the per-kind bound the queue owns, replacing the old semaphore).
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
		// The daemon's two periodic duties are cron entries on this same queue, so
		// every recurring thing the daemon does has one mechanism, one durable
		// record of when it next fires, and one place to look when it stops.
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
		if err := runner.RegisterCron(
			automationScheduleKind,
			automationScheduleInterval,
			d.automationScheduleHandler,
			jobs.HandlerConfig{Timeout: automationScheduleTickTimeout},
		); err != nil {
			d.logf("automation schedule: register tick: %v", err)
		}
	}
	// Surface lifecycle transitions to any open task panel. OnChange may fire
	// CONCURRENTLY from the runner's dispatch goroutine and its in-flight runs, so
	// the callback must be cheap and concurrency-safe. Publishing appends one row
	// to the bus and then projects to a non-blocking broadcast that drops on a
	// full channel, so it can never stall a run — but it IS a durable write on
	// the runner's goroutine, of the same order as the store.Save it follows.
	runner.OnChange(func(jobID string) { d.publishFact(FactTaskChanged, jobID, nil) })
	// Surface a durable notification when a job exhausts its retries (reaches the
	// terminal dead state). OnTerminalFailure fires exactly once per job, on the
	// queue's goroutine with a cloned record; notifyTaskTerminalFailure persists a
	// notification and broadcasts the new unread count, both non-blocking.
	runner.OnTerminalFailure(func(j *jobs.Job) { d.notifyTaskTerminalFailure(j) })
	d.setJobQueue(runner)
	if err := runner.Start(); err != nil {
		// A queue that failed to start is not an inert object: it still accepts
		// Enqueue and still writes rows, but no dispatch loop reads them and no cron
		// entry is armed. Every background duty and both periodic ticks are gone,
		// and the only evidence is work that never happens. Say so loudly — this
		// line is the whole diagnosis for a daemon that has quietly stopped doing
		// anything in the background.
		d.logf("jobs: THE JOB QUEUE DID NOT START: %v — no background work and no periodic ticks will run until the daemon is restarted", err)
	}
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
		// Inline fallback: no durable queue, no debounce, no retry. Compaction
		// still happens, synchronously, on the trigger.
		// runWorkspaceContextCompactionInline applies the per-run timeout.
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

// compactContextHandler is the queue-registered HandlerFunc for compact_context.
// The queue supplies the timeout context and the per-run CommitGuard; this body
// loads the current context, re-checks the size threshold (the doc may have
// shrunk during the debounce window), runs the agentic compaction, validates,
// and commits under the guard.
func (d *Daemon) compactContextHandler(ctx context.Context, job *jobs.Job) (any, error) {
	// Master switch: a run queued before the user disabled the keeper must not fire
	// background work after the toggle is off. No-op success so the stale record is
	// retired rather than retried. The user-triggered inline/manual compaction path
	// does not route through here and stays ungated.
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
	// Re-check the size gate after the debounce: a doc edited down below the
	// threshold should not burn an LLM pass. No-op success.
	if len([]byte(canonical.Content)) <= d.keeperCompactSizeThreshold() {
		return nil, nil
	}
	_, err = d.applyWorkspaceContextCompaction(ctx, config, canonical, job.CommitGuard)
	return nil, err
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
	return d.applyWorkspaceContextCompaction(ctx, config, canonical, &jobs.CommitGuard{})
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
		// Existing checkout files are agent-owned working copies; we deliberately do
		// NOT rewrite them. Replacing one could discard a write from an editor still
		// holding the old inode open. Their prior metadata makes them stale against
		// the new canonical revision, so the normal refresh/conflict workflow
		// preserves both clean and modified local state.
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
	// Checkouts are agent-owned; we deliberately leave them untouched (see the note
	// in the compact path) so the normal refresh/conflict workflow reconciles them
	// against the restored revision rather than risking a lost local write.
	d.broadcastWorkspaceContextChanged(updated)
	return &protocol.WorkspaceContextMaintenanceResult{
		Action:         protocol.WorkspaceContextMaintenanceActionRollback,
		WorkspaceID:    session.WorkspaceID,
		SourceRevision: current.Revision,
		ResultRevision: updated.Revision,
		Changed:        true,
	}, nil
}
