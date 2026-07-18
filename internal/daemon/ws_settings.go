package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	SettingProjectsDirectory = "projects_directory"
	SettingUIScale           = "uiScale"
	// SettingTicketBoardScale scales fonts on the ticket board and ticket
	// detail surfaces independently of the app-wide uiScale. Empty/unset =>
	// the board follows uiScale.
	SettingTicketBoardScale  = "ticketBoardScale"
	SettingClaudeExecutable  = "claude_executable"
	SettingCodexExecutable   = "codex_executable"
	SettingCopilotExecutable = "copilot_executable"
	SettingEditorExecutable  = "editor_executable"
	SettingNewSessionAgent   = "new_session_agent"
	SettingClaudeAvailable   = "claude_available"
	SettingCodexAvailable    = "codex_available"
	SettingCopilotAvailable  = "copilot_available"
	SettingPTYBackendMode    = "pty_backend_mode"
	SettingTheme             = "theme"
	SettingReviewerModel     = "reviewer_model"
	SettingKeeperCompact     = "workspace_keeper_compact"
	SettingTailscaleEnabled  = "tailscale_enabled"
	SettingWorkflowsEnabled  = "workflows_enabled"
	// SettingAutoApproveEnabled, when true, launches interactive agents in their
	// native auto-approve mode (Claude `--permission-mode auto`, Codex
	// `approvals_reviewer=auto_review`) so they can run unattended without
	// stalling on permission gates. Off by default. Yolo overrides it.
	SettingAutoApproveEnabled   = "auto_approve_enabled"
	SettingKeybindingsConfig    = "keybindings_config"
	SettingNewSessionYoloPrefix = "new_session_yolo_"
	// SettingChiefModelPrefix + agent (e.g. "chief_model_claude") pins the model a
	// chief-of-staff launch uses, passed through as --model. Empty/unset => the
	// agent's own default model. Only consulted for chief launches.
	SettingChiefModelPrefix = "chief_model_"
	// SettingChiefEffortPrefix + agent (e.g. "chief_effort_claude") pins the
	// reasoning effort a chief-of-staff launch uses, passed through as the
	// agent's native effort mechanism (Claude --effort, Codex
	// model_reasoning_effort). Empty/unset => the agent's own default. Only
	// consulted for chief launches.
	SettingChiefEffortPrefix = "chief_effort_"
	// SettingNotebookRoot overrides the notebook's filesystem root. Empty =>
	// the profile-derived default (~/attn-notebook[-profile]).
	SettingNotebookRoot = "notebook.root"
	// SettingNotebookRootEffective is a READ-ONLY, daemon-computed key surfaced in
	// the settings payload (never stored, never accepted by set_setting): the
	// absolute folder the notebook currently resolves to, so the UI can show where
	// the notebook lives even when SettingNotebookRoot is blank (the default).
	SettingNotebookRootEffective = "notebook.root.effective"
	// SettingNotebookCronFrequency is the 5-field cron expression for the
	// notebook's nightly maintenance slot (currently the daily-narrate backstop).
	// Empty => the default ("0 3 * * *").
	SettingNotebookCronFrequency = "notebook.cron.frequency"
	// SettingNotebookCronTimezone is the IANA timezone the frequency is
	// evaluated in. Empty => the machine's local time.
	SettingNotebookCronTimezone = "notebook.cron.timezone"
	// SettingNotebookSummarizeSession configures the per-session summarize pass
	// (the CHEAP tier). JSON {"agent":"claude"|"codex","model":"<id>"}; empty =>
	// the built-in cheap default (Claude Haiku). See parseNotebookNarrationConfig.
	SettingNotebookSummarizeSession = "notebook.summarize_session"
	// SettingNotebookNarrateWorkspace configures the curated-journal narrate pass (the
	// STRONG tier). JSON {"agent":"claude"|"codex","model":"<id>"}; empty => the
	// built-in strong default (Claude Sonnet). Claude is the default narrate agent
	// because its native Write/Edit enforce read-before-write CAS on the shared
	// journal; see parseNotebookNarrationConfig.
	SettingNotebookNarrateWorkspace = "notebook.narrate_workspace"
	// SettingChiefContextWindowCap caps the chief-of-staff session's effective
	// context window (in tokens): auto-compaction triggers at this threshold
	// instead of at the model's full window, so each cache-cold chief wake
	// re-reads less context. Empty/unset => DefaultContextWindowCap. Applied only
	// to chief launches; delegated interactive agents are never capped.
	SettingChiefContextWindowCap = "chief_context_window_cap"
	// SettingHeadlessContextWindowCap caps every headless run (keeper narration,
	// ticket reconciliation, workflow subagents) the same way. Headless runs are
	// one-shot and cache-cold by construction; one that grows past this is treated
	// as a bug, not accommodated. Empty/unset => DefaultContextWindowCap.
	SettingHeadlessContextWindowCap = "headless_context_window_cap"
	// SettingNotebookTasksEnabled is the master switch for ALL keeper async
	// background duties (per-session summarize, workspace narrate, context
	// compaction). Default ON: a blank/unset value means enabled, so existing
	// installs keep running the keeper without an opt-in. Only an explicit "false"
	// disables the whole group; the per-duty agent/model settings stay configurable
	// but produce no background work while off. See notebookTasksEnabled and the
	// enqueue/executor gates that honor it.
	SettingNotebookTasksEnabled = "notebook.tasks_enabled"
)

func (d *Daemon) handleGetSettingsWS(client *wsClient) {
	d.logf("Getting settings")
	d.refreshTailscaleServeState()
	d.sendToClient(client, &protocol.SettingsUpdatedMessage{
		Event:    protocol.EventSettingsUpdated,
		Settings: d.settingsWithAgentAvailability(),
	})
}

func (d *Daemon) handleSetSettingWS(client *wsClient, msg *protocol.SetSettingMessage) {
	d.logf("Setting %s = %s", msg.Key, msg.Value)
	if err := d.validateSetting(msg.Key, msg.Value); err != nil {
		d.logf("Setting validation failed: %v", err)
		d.sendToClient(client, &protocol.SettingsUpdatedMessage{
			Event:      protocol.EventSettingsUpdated,
			Settings:   d.settingsWithAgentAvailability(),
			ChangedKey: protocol.Ptr(msg.Key),
			Error:      protocol.Ptr(err.Error()),
			Success:    protocol.Ptr(false),
		})
		return
	}

	d.store.SetSetting(msg.Key, msg.Value)
	if msg.Key == SettingTailscaleEnabled {
		d.ensureTailscaleServeFromSettings()
	}
	if msg.Key == SettingHeadlessContextWindowCap {
		d.applyHeadlessContextWindowCap()
	}
	d.broadcastSettings(msg.Key)
}

func (d *Daemon) broadcastSettings(changedKey string) {
	d.refreshTailscaleServeState()
	d.broadcastCurrentSettings(changedKey)
}

func (d *Daemon) broadcastCurrentSettings(changedKey string) {
	event := &protocol.SettingsUpdatedMessage{
		Event:    protocol.EventSettingsUpdated,
		Settings: d.settingsWithAgentAvailability(),
	}
	if strings.TrimSpace(changedKey) != "" {
		event.ChangedKey = protocol.Ptr(changedKey)
	}
	d.wsHub.BroadcastValue(event)
}

func executableSettingKey(agent string) string {
	return strings.TrimSpace(strings.ToLower(agent)) + "_executable"
}

func availabilitySettingKey(agent string) string {
	return strings.TrimSpace(strings.ToLower(agent)) + "_available"
}

func capabilitySettingKey(agent, capability string) string {
	return strings.TrimSpace(strings.ToLower(agent)) + "_cap_" + strings.TrimSpace(strings.ToLower(capability))
}

func isAgentExecutableSettingKey(key string) (agent string, ok bool) {
	lower := strings.TrimSpace(strings.ToLower(key))
	if !strings.HasSuffix(lower, "_executable") {
		return "", false
	}
	agent = strings.TrimSuffix(lower, "_executable")
	if agent == "" {
		return "", false
	}
	if agentdriver.Get(agent) == nil {
		return "", false
	}
	return agent, true
}

func canonicalExecutableSettingKey(agent string) string {
	return executableSettingKey(agent)
}

func (d *Daemon) settingsWithAgentAvailability() map[string]interface{} {
	stored := d.store.GetAllSettings()
	settings := make(map[string]interface{}, len(stored)+8)
	for k, v := range stored {
		settings[k] = v
	}

	for _, name := range agentdriver.List() {
		driver := agentdriver.Get(name)
		if driver == nil {
			continue
		}
		execKey := canonicalExecutableSettingKey(name)
		configured := strings.TrimSpace(stored[execKey])
		if configured == "" {
			configured = strings.TrimSpace(stored[executableSettingKey(name)])
		}
		available := isAgentExecutableAvailable(configured, driver.DefaultExecutable())
		settings[availabilitySettingKey(name)] = strconv.FormatBool(available)
		if available {
			switch name {
			case string(protocol.SessionAgentClaude):
				if err := agentdriver.EnsureClaudeSkillInstalled(); err != nil {
					d.logf("failed to ensure Claude attn skill: %v", err)
				}
			case string(protocol.SessionAgentCodex):
				if err := agentdriver.EnsureCodexSkillInstalled(); err != nil {
					d.logf("failed to ensure Codex attn skill: %v", err)
				}
			case string(protocol.SessionAgentCopilot):
				if err := agentdriver.EnsureCopilotSkillInstalled(); err != nil {
					d.logf("failed to ensure Copilot attn skill: %v", err)
				}
			}
		}

		caps := agentdriver.EffectiveCapabilities(driver)
		settings[capabilitySettingKey(name, "hooks")] = strconv.FormatBool(caps.HasHooks)
		settings[capabilitySettingKey(name, "transcript")] = strconv.FormatBool(caps.HasTranscript)
		settings[capabilitySettingKey(name, "transcript_watcher")] = strconv.FormatBool(caps.HasTranscriptWatcher)
		settings[capabilitySettingKey(name, "classifier")] = strconv.FormatBool(caps.HasClassifier)
		settings[capabilitySettingKey(name, "initial_prompt")] = strconv.FormatBool(caps.HasInitialPrompt)
		settings[capabilitySettingKey(name, "state_detector")] = strconv.FormatBool(caps.HasStateDetector)
		settings[capabilitySettingKey(name, "resume")] = strconv.FormatBool(caps.HasResume)
		settings[capabilitySettingKey(name, "yolo")] = strconv.FormatBool(caps.HasYolo)
		hasHeadlessTask, _ := agentdriver.HeadlessTaskAvailability(driver)
		settings[capabilitySettingKey(name, "headless_task")] = strconv.FormatBool(hasHeadlessTask)
	}
	for _, driver := range d.ensurePluginRegistry().registeredDrivers() {
		settings[availabilitySettingKey(driver.Agent)] = "true"
		for capability, enabled := range driver.Capabilities {
			settings[capabilitySettingKey(driver.Agent, capability)] = strconv.FormatBool(enabled)
		}
	}

	if _, ok := settings[SettingClaudeAvailable]; !ok {
		settings[SettingClaudeAvailable] = settings[availabilitySettingKey(string(protocol.SessionAgentClaude))]
	}
	if _, ok := settings[SettingCodexAvailable]; !ok {
		settings[SettingCodexAvailable] = settings[availabilitySettingKey(string(protocol.SessionAgentCodex))]
	}
	if _, ok := settings[SettingCopilotAvailable]; !ok {
		settings[SettingCopilotAvailable] = settings[availabilitySettingKey(string(protocol.SessionAgentCopilot))]
	}
	settings[SettingPTYBackendMode] = d.ptyBackendMode()
	if root, err := d.notebookRoot(); err == nil {
		settings[SettingNotebookRootEffective] = root
	}
	settings[SettingTailscaleEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingTailscaleEnabled]))
	settings[SettingWorkflowsEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingWorkflowsEnabled]))
	settings[SettingAutoApproveEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingAutoApproveEnabled]))
	// Normalize the keeper master switch to its EFFECTIVE value so the UI toggle
	// reflects the default-ON semantics (blank/unset => "true") rather than an
	// absent key the frontend would read as off.
	settings[SettingNotebookTasksEnabled] = strconv.FormatBool(d.notebookTasksEnabled())
	// Surface the EFFECTIVE token caps so the UI shows the concrete default
	// (128000) rather than an absent key when the operator has not set one.
	settings[SettingChiefContextWindowCap] = strconv.Itoa(resolveContextWindowCap(stored[SettingChiefContextWindowCap]))
	settings[SettingHeadlessContextWindowCap] = strconv.Itoa(resolveContextWindowCap(stored[SettingHeadlessContextWindowCap]))

	tailscale := d.tailscaleStateSnapshot()
	if tailscale.status != "" {
		settings["tailscale_status"] = tailscale.status
	}
	if tailscale.domain != "" {
		settings["tailscale_domain"] = tailscale.domain
		settings["tailscale_url"] = "https://" + tailscale.domain + "/"
	}
	if tailscale.authURL != "" {
		settings["tailscale_auth_url"] = tailscale.authURL
	}
	if tailscale.lastError != "" {
		settings["tailscale_error"] = tailscale.lastError
	}
	return settings
}

func (d *Daemon) ptyBackendMode() string {
	switch d.ptyBackend.(type) {
	case *ptybackend.WorkerBackend:
		return "worker"
	case *ptybackend.EmbeddedBackend:
		return "embedded"
	default:
		return "unknown"
	}
}

func isAgentExecutableAvailable(configuredExecutable, defaultExecutable string) bool {
	executable := strings.TrimSpace(configuredExecutable)
	if executable == "" {
		executable = defaultExecutable
	}
	_, err := exec.LookPath(executable)
	return err == nil
}

// chiefLaunchModel returns the configured model for a chief-of-staff launch of
// the given agent (from chief_model_<agent>), or "" — the agent's own default —
// when this is not a chief launch or no model is configured.
func (d *Daemon) chiefLaunchModel(agent string, chief bool) string {
	if !chief {
		return ""
	}
	return strings.TrimSpace(d.store.GetSetting(SettingChiefModelPrefix + strings.ToLower(strings.TrimSpace(agent))))
}

// chiefLaunchEffort returns the configured reasoning effort for a
// chief-of-staff launch of the given agent (from chief_effort_<agent>), or
// "" — the agent's own default — when this is not a chief launch or no
// effort is configured.
func (d *Daemon) chiefLaunchEffort(agent string, chief bool) string {
	if !chief {
		return ""
	}
	return strings.TrimSpace(d.store.GetSetting(SettingChiefEffortPrefix + strings.ToLower(strings.TrimSpace(agent))))
}

// chiefContextWindowCap returns the effective context-window token cap for a
// chief-of-staff launch, or 0 (no cap) when this is not a chief launch. Mirrors
// chiefLaunchModel: the policy (what the cap is, and that only the chief gets
// one) lives here; the driver decides how to apply it.
func (d *Daemon) chiefContextWindowCap(chief bool) int {
	if !chief {
		return 0
	}
	return resolveContextWindowCap(d.store.GetSetting(SettingChiefContextWindowCap))
}

// applyHeadlessContextWindowCap pushes the headless_context_window_cap setting
// into the process-global that the headless spawn seam reads. Called at startup
// and on every settings change so headless runs always use the current value.
func (d *Daemon) applyHeadlessContextWindowCap() {
	if d.store == nil {
		return
	}
	agentdriver.SetHeadlessContextWindowCap(resolveContextWindowCap(d.store.GetSetting(SettingHeadlessContextWindowCap)))
}

func (d *Daemon) validateSetting(key, value string) error {
	switch key {
	case SettingProjectsDirectory:
		return validateProjectsDirectory(value)
	case SettingUIScale:
		return validateUIScale(value)
	case SettingTicketBoardScale:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return validateUIScale(value)
	case SettingClaudeExecutable, SettingCodexExecutable, SettingCopilotExecutable:
		return validateExecutableSetting(value)
	case SettingEditorExecutable:
		return validateEditorSetting(value)
	case SettingNewSessionAgent:
		return d.validateNewSessionAgent(value)
	case SettingTheme:
		return validateTheme(value)
	case SettingTailscaleEnabled, SettingWorkflowsEnabled, SettingAutoApproveEnabled, SettingNotebookTasksEnabled:
		return validateBooleanSetting(value)
	case SettingChiefContextWindowCap, SettingHeadlessContextWindowCap:
		return validateContextWindowCap(value)
	case SettingKeeperCompact:
		return d.validateKeeperCompactSetting(value)
	case SettingNotebookSummarizeSession:
		return d.validateNotebookNarrationSetting(notebookSummarizeSessionKind, value)
	case SettingNotebookNarrateWorkspace:
		return d.validateNotebookNarrationSetting(notebookNarrateWorkspaceKind, value)
	case SettingNotebookRoot:
		return validateNotebookRoot(value)
	case SettingNotebookCronFrequency:
		return validateNotebookCronFrequency(value)
	case SettingNotebookCronTimezone:
		return validateNotebookCronTimezone(value)
	case SettingKeybindingsConfig:
		return validateKeybindingsConfig(value)
	case SettingReviewerModel:
		return nil
	default:
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingNewSessionYoloPrefix) {
			return validateBooleanSetting(value)
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingChiefModelPrefix) {
			// Model names/aliases are free-form (like the reviewer_model
			// setting); accept any value and let the agent reject bad ones.
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingChiefEffortPrefix) {
			// Effort levels are agent-native (claude: low/medium/high/xhigh/max,
			// codex: minimal/low/medium/high/xhigh); accept any value and let
			// the agent reject bad ones. The UI constrains input.
			return nil
		}
		if _, ok := isAgentExecutableSettingKey(key); ok {
			return validateExecutableSetting(value)
		}
		return fmt.Errorf("unknown setting: %s", key)
	}
}

func validateBooleanSetting(value string) error {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "true", "false":
		return nil
	default:
		return fmt.Errorf("invalid boolean value: %s", value)
	}
}

func parseBooleanSetting(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func validateUIScale(value string) error {
	scale, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid scale value: %s", value)
	}
	if scale < 0.5 || scale > 2.0 {
		return fmt.Errorf("scale must be between 0.5 and 2.0")
	}
	return nil
}

// contextWindowCap bounds. The knob can only REDUCE the effective window (a value
// above the model's real limit is clamped/ignored by the agent), so the ceiling
// is a fat-finger guard rather than a hard limit; the floor keeps compaction from
// thrashing on a pathologically small window.
const (
	contextWindowCapMin = 10000
	contextWindowCapMax = 2000000
)

// validateContextWindowCap accepts an empty value (meaning DefaultContextWindowCap)
// or a whole number of tokens within [contextWindowCapMin, contextWindowCapMax].
func validateContextWindowCap(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return fmt.Errorf("context window cap must be a whole number of tokens: %s", value)
	}
	if n < contextWindowCapMin || n > contextWindowCapMax {
		return fmt.Errorf("context window cap must be between %d and %d tokens", contextWindowCapMin, contextWindowCapMax)
	}
	return nil
}

// resolveContextWindowCap turns a stored setting value into an effective token
// cap, applying DefaultContextWindowCap when unset/blank/unparseable.
func resolveContextWindowCap(stored string) int {
	if trimmed := strings.TrimSpace(stored); trimmed != "" {
		if n, err := strconv.Atoi(trimmed); err == nil && n > 0 {
			return n
		}
	}
	return agentdriver.DefaultContextWindowCap
}

// validateNotebookRoot accepts an empty value (meaning the profile-derived
// default) or an absolute path (a leading ~/ is expanded). It refuses a path
// inside the attn data dir: the notebook must live OUTSIDE ~/.attn[-profile] so
// it stays a plain, externally-syncable directory a dotfile-skipping scanner
// won't miss.
func validateNotebookRoot(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, err := normalizeExternalRoot(value)
	if err != nil {
		return fmt.Errorf("notebook.root %w", err)
	}
	return nil
}

// normalizeExternalRoot expands a leading "~/" against the user's home
// directory, requires the result to be an absolute path, cleans it, and
// rejects a path that is (or is inside) the attn data dir — an external root
// must live OUTSIDE ~/.attn[-profile] so it stays a plain, externally-syncable
// directory a dotfile-skipping scanner won't miss. Empty input is the
// caller's concern: it returns ("", nil) unchanged.
//
// Errors are unprefixed (e.g. "must be an absolute path") so each caller can
// prefix them with its own vocabulary (notebook.root vs fs root).
func normalizeExternalRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	path := trimmed
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("must be an absolute path")
	}
	dataDir := config.DataDir()
	clean := filepath.Clean(path)
	if clean == dataDir || strings.HasPrefix(clean, dataDir+string(filepath.Separator)) {
		return "", fmt.Errorf("must be outside the attn data dir (%s)", dataDir)
	}
	return clean, nil
}

// validateNotebookCronFrequency accepts an empty value (use the default) or a
// cron expression the scheduler can fire. It rejects two parseable-but-wrong
// forms: an embedded CRON_TZ=/TZ= prefix (a second timezone source that would
// silently compete with notebook.cron.timezone) and a schedule whose date can
// never occur (e.g. "0 0 30 2 *", Feb 30) — robfig cron returns the zero time for
// those, which the scheduler would treat as perpetually due and re-fire in a
// tight loop.
func validateNotebookCronFrequency(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if hasCronTZPrefix(trimmed) {
		return fmt.Errorf("notebook.cron.frequency must not embed a CRON_TZ=/TZ= prefix; set notebook.cron.timezone instead")
	}
	sched, err := cron.ParseStandard(trimmed)
	if err != nil {
		return fmt.Errorf("notebook.cron.frequency must be a cron expression (5 fields, or a descriptor like @daily): %w", err)
	}
	if sched.Next(time.Now()).IsZero() {
		return fmt.Errorf("notebook.cron.frequency %q describes a time that never occurs", trimmed)
	}
	return nil
}

// hasCronTZPrefix reports whether a cron string carries a leading TZ=/CRON_TZ=
// timezone prefix (the form robfig/cron's ParseStandard honors).
func hasCronTZPrefix(expr string) bool {
	return strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=")
}

// validateNotebookCronTimezone accepts an empty value (local time) or an IANA
// timezone name loadable on this machine.
func validateNotebookCronTimezone(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := time.LoadLocation(strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("notebook.cron.timezone must be an IANA timezone: %w", err)
	}
	return nil
}

func validateProjectsDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("projects directory cannot be empty")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("projects directory must be an absolute path")
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("cannot create directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot access directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path exists but is not a directory")
	}

	return nil
}

func validateExecutableSetting(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	path, err := exec.LookPath(value)
	if err != nil {
		return fmt.Errorf("executable not found: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable path points to a directory")
	}

	return nil
}

func validateEditorSetting(value string) error {
	editor := strings.TrimSpace(value)
	if editor == "" {
		return nil
	}

	binary := extractCommandBinary(editor)
	if binary == "" {
		return fmt.Errorf("invalid editor command")
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("executable not found: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable path points to a directory")
	}

	return nil
}

func (d *Daemon) validateNewSessionAgent(value string) error {
	agent := strings.TrimSpace(strings.ToLower(value))
	if agent == "" {
		return nil
	}
	if agentdriver.Get(agent) == nil {
		if d.plugins != nil {
			if _, ok := d.plugins.driver(agent); ok {
				return nil
			}
		}
		return fmt.Errorf("unknown agent: %s", value)
	}
	return nil
}

// validateKeybindingsConfig keeps daemon validation light: the frontend owns the
// shortcut schema and tolerates anything unrecognized, so the daemon only
// guarantees the stored blob is parseable JSON (or empty).
func validateKeybindingsConfig(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("keybindings config must be valid JSON")
	}
	return nil
}

func validateTheme(value string) error {
	if value != "dark" && value != "light" && value != "system" {
		return fmt.Errorf("invalid theme: %s (must be dark, light, or system)", value)
	}
	return nil
}

func extractCommandBinary(command string) string {
	if command == "" {
		return ""
	}
	if command[0] == '"' || command[0] == '\'' {
		quote := command[0]
		for i := 1; i < len(command); i++ {
			if command[i] == quote {
				return command[1:i]
			}
		}
		return ""
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
