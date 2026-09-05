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
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	// PERSISTED updated_by_session_id sentinel, string-matched in the frontend;
	// migration 51 realigned rows from "attn-janitor".
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
	if !agentdriver.HeadlessTasksSupportTools(driver) {
		return keeperCompactConfig{}, fmt.Errorf("agent %s supports only tool-free headless tasks", config.Agent)
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

func (d *Daemon) notebookSummariesEnabled() bool {
	return d.notebookDutyEnabled(SettingNotebookSummarizeSessionEnabled)
}

func (d *Daemon) notebookWorkspaceNarrationEnabled() bool {
	return d.notebookDutyEnabled(SettingNotebookNarrateWorkspaceEnabled)
}

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

// Retained ONLY for migrateKeeperCompactSettingKey; never read elsewhere.
const legacyKeeperCompactSettingKey = "workspace_context_janitor"

func (d *Daemon) renameSettingKey(legacy, current string) {
	if d.store == nil {
		return
	}
	value := d.store.GetSetting(legacy)
	if strings.TrimSpace(value) == "" {
		return
	}
	if strings.TrimSpace(d.store.GetSetting(current)) == "" {
		d.store.SetSetting(current, value)
		d.logf("migrated setting %q -> %q", legacy, current)
	}
	d.store.DeleteSetting(legacy)
}

func (d *Daemon) migrateKeeperCompactSettingKey() {
	d.renameSettingKey(legacyKeeperCompactSettingKey, SettingKeeperCompact)
}

const compactContextKind = "compact_context"

// The runner is built late in Start(), so no teardown callsite may touch
// d.jobQueue directly. Remove, not Cancel — Cancel is a no-op for a queued task.
func (d *Daemon) forgetWorkspaceContextCompaction(workspaceID string) {
	runner := d.jobQueueRef()
	if runner == nil {
		return
	}
	runner.RemoveByKey(compactContextKind, workspaceID)
}

func (d *Daemon) jobQueueRef() *jobs.Runner {
	d.jobQueueMu.RLock()
	defer d.jobQueueMu.RUnlock()
	return d.jobQueue
}

func (d *Daemon) setJobQueue(runner *jobs.Runner) {
	d.jobQueueMu.Lock()
	d.jobQueue = runner
	d.jobQueueMu.Unlock()
}

func (d *Daemon) startJobQueue() {
	var queueStore jobs.Store
	if d.store != nil {
		queueStore = d.newSQLJobStore()
	}
	d.startJobQueueWithStore(queueStore)
}

func (d *Daemon) startJobQueueWithStore(queueStore jobs.Store) {
	d.importLegacyTasks()
	opts := jobs.Options{Log: d.logf, Store: queueStore}
	runner := jobs.New(opts)
	if !runner.Disabled() {
		if err := d.registerSnoozeWakeHandler(runner); err != nil {
			d.logf("snooze wake: register session_snooze_wake: %v", err)
		}
		if err := runner.RegisterWith(
			compactContextKind,
			d.compactContextHandler,
			jobs.HandlerConfig{Timeout: d.keeperCompactTimeoutDuration()},
		); err != nil {
			d.logf("keeper compact: register compact_context: %v", err)
		}
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
		if err := runner.RegisterWith(
			sessionActivityKind,
			d.sessionActivityHandler,
			jobs.HandlerConfig{
				Timeout:       sessionActivityTimeout,
				MaxConcurrent: sessionActivityConcurrency,
			},
		); err != nil {
			d.logf("session activity: register session_activity: %v", err)
		}
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
		if err := runner.RegisterWith(
			legacyTicketRecoveryKind,
			d.legacyTicketRecoveryHandler,
			jobs.HandlerConfig{Timeout: legacyTicketRecoveryTimeout},
		); err != nil {
			d.logf("legacy ticket recovery: register: %v", err)
		}
		if err := runner.RegisterWith(
			sessionTitleKind,
			d.sessionTitleHandler,
			jobs.HandlerConfig{Timeout: sessionTitleTimeout},
		); err != nil {
			d.logf("session title: register session_title: %v", err)
		}
		if err := runner.RegisterWith(
			gardenReviewClassifyKind,
			d.gardenReviewClassifyHandler,
			jobs.HandlerConfig{
				Timeout:       gardenReviewClassifyTimeout,
				MaxConcurrent: gardenReviewClassifyConcurrency,
			},
		); err != nil {
			d.logf("garden review: register classification: %v", err)
		}
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
		if err := runner.RegisterCron(
			appInvocationRetentionKind,
			appInvocationRetentionInterval,
			d.appInvocationRetentionHandler,
			jobs.HandlerConfig{Timeout: appInvocationRetentionTimeout},
		); err != nil {
			d.logf("apps: register invocation retention tick: %v", err)
		}
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
		d.registerCrewLifecycleCron(runner)
		d.registerSessionPullRequestRefreshCron(runner)
		d.registerWorktreeSweepCron(runner)
		if err := runner.RegisterCron(
			automationScheduleKind,
			automationScheduleInterval(),
			d.automationScheduleHandler,
			jobs.HandlerConfig{Timeout: automationScheduleTickTimeout},
		); err != nil {
			d.logf("automation schedule: register tick: %v", err)
		}
	}
	// OnChange may fire CONCURRENTLY from dispatch and in-flight runs; the handler
	// must be cheap, concurrency-safe and non-blocking.
	runner.OnChange(func(jobID string) { d.publishFact(FactTaskChanged, jobID, nil) })
	// OnTerminalFailure fires on the queue's goroutine; it must stay non-blocking.
	runner.OnTerminalFailure(func(j *jobs.Job) {
		d.notifyTaskTerminalFailure(j)
		go d.failGardenReviewJob(j)
	})
	if err := runner.Start(); err != nil {
		d.logf("jobs: THE JOB QUEUE DID NOT START: %v — no background work and no periodic ticks will run until the daemon is restarted", err)
		return
	}
	d.setJobQueue(runner)
	d.reconcileSnoozeWakeJobs()
	d.resumeGardenReviews()
	d.settleHarvestConditions()
}

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
	// Ahead of the queue lookup: a down queue compacts inline, which also spawns.
	if d.headlessTaskRefused(compactContextKind) {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
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

func (d *Daemon) compactContextHandler(ctx context.Context, job *jobs.Job) (any, error) {
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
	if len([]byte(canonical.Content)) <= d.keeperCompactSizeThreshold() {
		return nil, nil
	}
	_, err = d.applyWorkspaceContextCompaction(ctx, config, canonical, job.CommitGuard)
	return nil, err
}

func (d *Daemon) runWorkspaceContextCompactionInline(
	ctx context.Context,
	config keeperCompactConfig,
	canonical *protocol.WorkspaceContext,
) (*protocol.WorkspaceContextMaintenanceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, d.keeperCompactTimeoutDuration())
	defer cancel()
	return d.applyWorkspaceContextCompaction(ctx, config, canonical, &jobs.CommitGuard{})
}

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
	// admitted commit.
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
		// Checkout files are agent-owned: replacing one could discard a write from an
		// editor holding the old inode.
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
	if d.headlessTaskRefused(compactContextKind) {
		return keeperCompactExecution{}, headless.Refusal(compactContextKind)
	}
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
	request := agentdriver.HeadlessTaskRequest{
		Executable: executablePath,
		Model:      config.Model,
		Prompt:     prompts.RenderText("keeper", "compact", prompts.Values{"source_path": sourcePath, "candidate_path": candidatePath}),
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

// Format string: the two %s are the absolute source path (read) and the
// candidate path (write).

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
	// Remove first, so a queued run cannot double-compact after the manual one.
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
	d.broadcastWorkspaceContextChanged(updated)
	return &protocol.WorkspaceContextMaintenanceResult{
		Action:         protocol.WorkspaceContextMaintenanceActionRollback,
		WorkspaceID:    session.WorkspaceID,
		SourceRevision: current.Revision,
		ResultRevision: updated.Revision,
		Changed:        true,
	}, nil
}
