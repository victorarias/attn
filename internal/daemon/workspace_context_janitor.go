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
)

const (
	workspaceContextJanitorUpdater          = "attn-janitor"
	defaultWorkspaceContextJanitorThreshold = 12 * 1024
	defaultWorkspaceContextJanitorDebounce  = 10 * time.Minute
	defaultWorkspaceContextJanitorTimeout   = 5 * time.Minute
)

type workspaceContextJanitorConfig struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}

type workspaceContextJanitorExecution struct {
	Candidate          string
	ResolvedExecutable string
	Diagnostics        string
}

type workspaceContextJanitorTimer struct {
	timer *time.Timer
}

type workspaceContextJanitorExecutor func(
	ctx context.Context,
	config workspaceContextJanitorConfig,
	canonical *protocol.WorkspaceContext,
) (workspaceContextJanitorExecution, error)

func parseWorkspaceContextJanitorConfig(raw string) (workspaceContextJanitorConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return workspaceContextJanitorConfig{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config workspaceContextJanitorConfig
	if err := decoder.Decode(&config); err != nil {
		return workspaceContextJanitorConfig{}, fmt.Errorf("invalid workspace context janitor configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return workspaceContextJanitorConfig{}, fmt.Errorf("invalid workspace context janitor configuration: %w", err)
	}
	config.Agent = strings.TrimSpace(strings.ToLower(config.Agent))
	config.Model = strings.TrimSpace(config.Model)
	if config.Agent == "" || config.Model == "" {
		return workspaceContextJanitorConfig{}, errors.New("workspace context janitor requires both agent and model")
	}
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return workspaceContextJanitorConfig{}, fmt.Errorf("workspace context janitor agent is not installed: %s", config.Agent)
	}
	if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
		return workspaceContextJanitorConfig{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	if available, reason := agentdriver.HeadlessTaskAvailability(driver); !available {
		return workspaceContextJanitorConfig{}, fmt.Errorf("agent %s cannot run headless tasks: %s", config.Agent, reason)
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

func (d *Daemon) validateWorkspaceContextJanitorSetting(raw string) error {
	config, err := parseWorkspaceContextJanitorConfig(raw)
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
		return fmt.Errorf("workspace context janitor executable for %s was not found: %w", config.Agent, err)
	}
	return nil
}

func (d *Daemon) workspaceContextJanitorConfig() (workspaceContextJanitorConfig, error) {
	if d.store == nil {
		return workspaceContextJanitorConfig{}, errors.New("workspace context janitor settings unavailable")
	}
	return parseWorkspaceContextJanitorConfig(d.store.GetSetting(SettingWorkspaceContextJanitor))
}

func (d *Daemon) scheduleWorkspaceContextJanitor(canonical *protocol.WorkspaceContext) {
	if canonical == nil || strings.TrimSpace(canonical.WorkspaceID) == "" {
		return
	}
	config, err := d.workspaceContextJanitorConfig()
	if err != nil {
		d.logf("workspace context janitor: configuration: %v", err)
		return
	}
	if config.Agent == "" {
		return
	}
	threshold := d.workspaceContextJanitorSizeThreshold()

	d.workspaceContextJanitorMu.Lock()
	defer d.workspaceContextJanitorMu.Unlock()
	if d.workspaceContextJanitorTimers == nil {
		d.workspaceContextJanitorTimers = make(map[string]*workspaceContextJanitorTimer)
	}
	if scheduled := d.workspaceContextJanitorTimers[canonical.WorkspaceID]; scheduled != nil {
		scheduled.timer.Stop()
		delete(d.workspaceContextJanitorTimers, canonical.WorkspaceID)
	}
	if len([]byte(canonical.Content)) <= threshold {
		return
	}
	workspaceID := canonical.WorkspaceID
	scheduled := &workspaceContextJanitorTimer{}
	scheduled.timer = time.AfterFunc(
		d.workspaceContextJanitorDebounceDuration(),
		func() { d.runScheduledWorkspaceContextJanitor(workspaceID, scheduled) },
	)
	d.workspaceContextJanitorTimers[workspaceID] = scheduled
}

func (d *Daemon) runScheduledWorkspaceContextJanitor(
	workspaceID string,
	scheduled *workspaceContextJanitorTimer,
) {
	d.workspaceContextJanitorMu.Lock()
	if d.workspaceContextJanitorTimers[workspaceID] != scheduled {
		d.workspaceContextJanitorMu.Unlock()
		return
	}
	delete(d.workspaceContextJanitorTimers, workspaceID)
	d.workspaceContextJanitorMu.Unlock()

	canonical, err := d.store.GetWorkspaceContext(workspaceID)
	if err != nil {
		d.logf("workspace context janitor: load %s: %v", workspaceID, err)
		return
	}
	if len([]byte(canonical.Content)) <= d.workspaceContextJanitorSizeThreshold() {
		return
	}
	if _, err := d.runWorkspaceContextJanitor(context.Background(), canonical); err != nil {
		d.logf("workspace context janitor: compact %s: %v", workspaceID, err)
	}
}

func (d *Daemon) runWorkspaceContextJanitor(
	parent context.Context,
	canonical *protocol.WorkspaceContext,
) (*protocol.WorkspaceContextMaintenanceResult, error) {
	if canonical == nil || strings.TrimSpace(canonical.WorkspaceID) == "" {
		return nil, errors.New("workspace context is required")
	}
	config, err := d.workspaceContextJanitorConfig()
	if err != nil {
		return nil, err
	}
	if config.Agent == "" {
		return nil, errors.New("workspace context janitor is disabled")
	}

	ctx, err := d.beginWorkspaceContextJanitorRun(parent, canonical.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer d.finishWorkspaceContextJanitorRun(canonical.WorkspaceID)

	executor := d.workspaceContextJanitorExecutor
	if executor == nil {
		executor = d.executeWorkspaceContextJanitor
	}
	execution, err := executor(ctx, config, canonical)
	if err != nil {
		return nil, err
	}
	candidate := execution.Candidate
	if err := validateWorkspaceContextJanitorCandidate(canonical.Content, candidate); err != nil {
		return nil, err
	}
	if err := d.beginWorkspaceContextJanitorCommit(canonical.WorkspaceID, ctx); err != nil {
		return nil, err
	}
	if d.workspaceContextBeforeJanitorApply != nil {
		d.workspaceContextBeforeJanitorApply()
	}

	updated, changed, err := d.store.ApplyWorkspaceContextJanitorResult(
		canonical.WorkspaceID,
		candidate,
		workspaceContextJanitorUpdater,
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
			"workspace context janitor: workspace=%s agent=%s model=%s executable=%s changed=%t diagnostics=%s",
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

func (d *Daemon) beginWorkspaceContextJanitorRun(
	parent context.Context,
	workspaceID string,
) (context.Context, error) {
	d.workspaceContextJanitorMu.Lock()
	defer d.workspaceContextJanitorMu.Unlock()
	if d.workspaceContextJanitorRunning {
		return nil, errors.New("workspace context janitor is already running")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, d.workspaceContextJanitorTimeoutDuration())
	d.workspaceContextJanitorRunning = true
	d.workspaceContextJanitorActiveWorkspace = workspaceID
	d.workspaceContextJanitorCancel = cancel
	d.workspaceContextJanitorDone = make(chan struct{})
	d.workspaceContextJanitorCanceled = false
	d.workspaceContextJanitorCommitting = false
	return ctx, nil
}

func (d *Daemon) beginWorkspaceContextJanitorCommit(workspaceID string, ctx context.Context) error {
	d.workspaceContextJanitorMu.Lock()
	defer d.workspaceContextJanitorMu.Unlock()
	if !d.workspaceContextJanitorRunning || d.workspaceContextJanitorActiveWorkspace != workspaceID {
		return errors.New("workspace context janitor run is no longer active")
	}
	if d.workspaceContextJanitorCanceled {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.workspaceContextJanitorCommitting = true
	return nil
}

func (d *Daemon) finishWorkspaceContextJanitorRun(workspaceID string) {
	d.workspaceContextJanitorMu.Lock()
	defer d.workspaceContextJanitorMu.Unlock()
	if d.workspaceContextJanitorActiveWorkspace != workspaceID {
		return
	}
	if d.workspaceContextJanitorCancel != nil {
		d.workspaceContextJanitorCancel()
	}
	done := d.workspaceContextJanitorDone
	d.workspaceContextJanitorRunning = false
	d.workspaceContextJanitorActiveWorkspace = ""
	d.workspaceContextJanitorCancel = nil
	d.workspaceContextJanitorDone = nil
	d.workspaceContextJanitorCanceled = false
	d.workspaceContextJanitorCommitting = false
	if done != nil {
		close(done)
	}
}

func (d *Daemon) cancelWorkspaceContextJanitor(workspaceID string) {
	d.workspaceContextJanitorMu.Lock()
	if scheduled := d.workspaceContextJanitorTimers[workspaceID]; scheduled != nil {
		scheduled.timer.Stop()
		delete(d.workspaceContextJanitorTimers, workspaceID)
	}
	var done chan struct{}
	if d.workspaceContextJanitorActiveWorkspace == workspaceID && d.workspaceContextJanitorCancel != nil {
		if !d.workspaceContextJanitorCommitting {
			d.workspaceContextJanitorCanceled = true
			d.workspaceContextJanitorCancel()
		}
		done = d.workspaceContextJanitorDone
	}
	d.workspaceContextJanitorMu.Unlock()
	if done != nil {
		<-done
	}
}

func (d *Daemon) stopWorkspaceContextJanitor() {
	d.workspaceContextJanitorMu.Lock()
	for workspaceID, scheduled := range d.workspaceContextJanitorTimers {
		scheduled.timer.Stop()
		delete(d.workspaceContextJanitorTimers, workspaceID)
	}
	var done chan struct{}
	if d.workspaceContextJanitorCancel != nil {
		if !d.workspaceContextJanitorCommitting {
			d.workspaceContextJanitorCanceled = true
			d.workspaceContextJanitorCancel()
		}
		done = d.workspaceContextJanitorDone
	}
	d.workspaceContextJanitorMu.Unlock()
	if done != nil {
		<-done
	}
}

func (d *Daemon) workspaceContextJanitorSizeThreshold() int {
	if d.workspaceContextJanitorThreshold > 0 {
		return d.workspaceContextJanitorThreshold
	}
	return defaultWorkspaceContextJanitorThreshold
}

func (d *Daemon) workspaceContextJanitorDebounceDuration() time.Duration {
	if d.workspaceContextJanitorDebounce > 0 {
		return d.workspaceContextJanitorDebounce
	}
	return defaultWorkspaceContextJanitorDebounce
}

func (d *Daemon) workspaceContextJanitorTimeoutDuration() time.Duration {
	if d.workspaceContextJanitorTimeout > 0 {
		return d.workspaceContextJanitorTimeout
	}
	return defaultWorkspaceContextJanitorTimeout
}

func (d *Daemon) executeWorkspaceContextJanitor(
	ctx context.Context,
	config workspaceContextJanitorConfig,
	canonical *protocol.WorkspaceContext,
) (workspaceContextJanitorExecution, error) {
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return workspaceContextJanitorExecution{}, fmt.Errorf("workspace context janitor agent not found: %s", config.Agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return workspaceContextJanitorExecution{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	configured := d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	resolvedExecutable := driver.ResolveExecutable(configured)
	executablePath, err := exec.LookPath(resolvedExecutable)
	if err != nil {
		return workspaceContextJanitorExecution{}, fmt.Errorf("resolve %s executable: %w", config.Agent, err)
	}
	attnExecutable, err := os.Executable()
	if err != nil {
		return workspaceContextJanitorExecution{}, fmt.Errorf("resolve attn executable: %w", err)
	}
	tempDir, err := os.MkdirTemp("", "attn-context-janitor-*")
	if err != nil {
		return workspaceContextJanitorExecution{}, fmt.Errorf("create janitor workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	sourcePath := filepath.Join(tempDir, "source.md")
	candidatePath := filepath.Join(tempDir, "candidate.md")
	if err := os.WriteFile(sourcePath, []byte(canonical.Content), 0o600); err != nil {
		return workspaceContextJanitorExecution{}, fmt.Errorf("write janitor source: %w", err)
	}
	request := agentdriver.HeadlessTaskRequest{
		Executable:       executablePath,
		Model:            config.Model,
		Prompt:           workspaceContextJanitorPrompt,
		WorkDir:          tempDir,
		MCPServerName:    "attn_context",
		MCPServerCommand: attnExecutable,
		MCPServerArgs: []string{
			"_workspace-context-janitor-mcp",
			"--source-file", sourcePath,
			"--candidate-file", candidatePath,
		},
	}
	result, err := provider.RunHeadlessTask(ctx, request)
	if err != nil {
		return workspaceContextJanitorExecution{
			ResolvedExecutable: executablePath,
			Diagnostics:        result.Diagnostics,
		}, err
	}
	candidate, err := os.ReadFile(candidatePath)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceContextJanitorExecution{
			ResolvedExecutable: executablePath,
			Diagnostics:        result.Diagnostics,
		}, errors.New("workspace context janitor completed without replacing the context")
	} else if err != nil {
		return workspaceContextJanitorExecution{}, fmt.Errorf("read janitor candidate: %w", err)
	}
	return workspaceContextJanitorExecution{
		Candidate:          string(candidate),
		ResolvedExecutable: executablePath,
		Diagnostics:        result.Diagnostics,
	}, nil
}

const workspaceContextJanitorPrompt = `Compact the supplied workspace context without changing its meaning.

Use read_context first. Then call replace_context exactly once with the complete result.

Preserve:
- Area and all current truths
- unresolved open edges
- decisions and constraints
- source links and useful timeline turning points

You may shorten prose, deduplicate facts, and merge overlapping Threads. Remove stale or superseded material only when the document itself establishes that it is stale or superseded.

Do not add facts, dates, chronology, causality, ownership, thread structure, or conclusions. If uncertain, preserve the content. A byte-identical replacement is valid.

The result must contain exactly one "# Workspace Context" heading, a non-empty "## Area", and a non-empty "## Current Picture".`

func validateWorkspaceContextJanitorCandidate(source, candidate string) error {
	if len([]byte(candidate)) > len([]byte(source)) {
		return fmt.Errorf("workspace context janitor candidate grew from %d to %d bytes", len([]byte(source)), len([]byte(candidate)))
	}
	lines := splitMarkdownLines(candidate)
	if firstNonEmptyLine(lines) != "# Workspace Context" {
		return errors.New(`workspace context janitor candidate must start with "# Workspace Context"`)
	}
	if countExactLine(lines, "# Workspace Context") != 1 {
		return errors.New(`workspace context janitor candidate must contain exactly one "# Workspace Context" heading`)
	}
	if countTopLevelHeadings(lines) != 1 {
		return errors.New("workspace context janitor candidate must contain exactly one top-level heading")
	}
	for _, heading := range []string{"## Area", "## Current Picture"} {
		if countExactLine(lines, heading) != 1 {
			return fmt.Errorf("workspace context janitor candidate must contain exactly one %q heading", heading)
		}
		if strings.TrimSpace(markdownSectionContent(lines, heading)) == "" {
			return fmt.Errorf("workspace context janitor candidate section %q is empty", heading)
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
	d.cancelScheduledWorkspaceContextJanitor(session.WorkspaceID)
	return d.runWorkspaceContextJanitor(ctx, canonical)
}

func (d *Daemon) cancelScheduledWorkspaceContextJanitor(workspaceID string) {
	d.workspaceContextJanitorMu.Lock()
	defer d.workspaceContextJanitorMu.Unlock()
	if scheduled := d.workspaceContextJanitorTimers[workspaceID]; scheduled != nil {
		scheduled.timer.Stop()
		delete(d.workspaceContextJanitorTimers, workspaceID)
	}
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
	updated, err := d.store.RestoreWorkspaceContextJanitorBackup(session.WorkspaceID, session.ID)
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
