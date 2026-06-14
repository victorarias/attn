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
	SettingProjectsDirectory        = "projects_directory"
	SettingUIScale                  = "uiScale"
	SettingClaudeExecutable         = "claude_executable"
	SettingCodexExecutable          = "codex_executable"
	SettingCopilotExecutable        = "copilot_executable"
	SettingEditorExecutable         = "editor_executable"
	SettingNewSessionAgent          = "new_session_agent"
	SettingClaudeAvailable          = "claude_available"
	SettingCodexAvailable           = "codex_available"
	SettingCopilotAvailable         = "copilot_available"
	SettingPTYBackendMode           = "pty_backend_mode"
	SettingTheme                    = "theme"
	SettingReviewLoopPresets        = "review_loop_prompt_presets"
	SettingReviewLoopLastPreset     = "review_loop_last_preset"
	SettingReviewLoopLastPrompt     = "review_loop_last_prompt"
	SettingReviewLoopLastIterations = "review_loop_last_iterations"
	SettingReviewLoopModel          = "review_loop_model"
	SettingReviewerModel            = "reviewer_model"
	SettingWorkspaceContextJanitor  = "workspace_context_janitor"
	SettingTailscaleEnabled         = "tailscale_enabled"
	SettingKeybindingsConfig        = "keybindings_config"
	SettingNewSessionYoloPrefix     = "new_session_yolo_"
	// SettingNotebookRoot overrides the notebook's filesystem root. Empty =>
	// the profile-derived default (~/attn-notebook[-profile]).
	SettingNotebookRoot = "notebook.root"
	// SettingNotebookDreamingEnabled gates the nightly dreaming consolidation
	// pass. Default false; `attn notebook dream status`/`--dry-run` inspect the
	// harvest regardless (the gate only governs autonomous runs).
	SettingNotebookDreamingEnabled = "notebook.dreaming.enabled"
	// SettingNotebookDreamingFrequency is the 5-field cron expression for the
	// nightly pass. Empty => the default ("0 3 * * *").
	SettingNotebookDreamingFrequency = "notebook.dreaming.frequency"
	// SettingNotebookDreamingTimezone is the IANA timezone the frequency is
	// evaluated in. Empty => the machine's local time.
	SettingNotebookDreamingTimezone = "notebook.dreaming.timezone"
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
	settings[SettingTailscaleEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingTailscaleEnabled]))

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

func (d *Daemon) validateSetting(key, value string) error {
	switch key {
	case SettingProjectsDirectory:
		return validateProjectsDirectory(value)
	case SettingUIScale:
		return validateUIScale(value)
	case SettingClaudeExecutable, SettingCodexExecutable, SettingCopilotExecutable:
		return validateExecutableSetting(value)
	case SettingEditorExecutable:
		return validateEditorSetting(value)
	case SettingNewSessionAgent:
		return d.validateNewSessionAgent(value)
	case SettingTheme:
		return validateTheme(value)
	case SettingTailscaleEnabled:
		return validateBooleanSetting(value)
	case SettingWorkspaceContextJanitor:
		return d.validateWorkspaceContextJanitorSetting(value)
	case SettingNotebookRoot:
		return validateNotebookRoot(value)
	case SettingNotebookDreamingEnabled:
		return validateBooleanSetting(value)
	case SettingNotebookDreamingFrequency:
		return validateNotebookDreamingFrequency(value)
	case SettingNotebookDreamingTimezone:
		return validateNotebookDreamingTimezone(value)
	case SettingKeybindingsConfig:
		return validateKeybindingsConfig(value)
	case SettingReviewLoopPresets, SettingReviewLoopLastPreset, SettingReviewLoopLastPrompt, SettingReviewLoopLastIterations, SettingReviewLoopModel, SettingReviewerModel:
		return nil
	default:
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingNewSessionYoloPrefix) {
			return validateBooleanSetting(value)
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

// validateNotebookRoot accepts an empty value (meaning the profile-derived
// default) or an absolute path (a leading ~/ is expanded). It refuses a path
// inside the attn data dir: the notebook must live OUTSIDE ~/.attn[-profile] so
// it stays a plain, externally-syncable directory a dotfile-skipping scanner
// won't miss.
func validateNotebookRoot(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	path := value
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("notebook.root must be an absolute path")
	}
	dataDir := config.DataDir()
	clean := filepath.Clean(path)
	if clean == dataDir || strings.HasPrefix(clean, dataDir+string(filepath.Separator)) {
		return fmt.Errorf("notebook.root must be outside the attn data dir (%s)", dataDir)
	}
	return nil
}

// validateNotebookDreamingFrequency accepts an empty value (use the default) or a
// cron expression the scheduler can fire. It rejects two parseable-but-wrong
// forms: an embedded CRON_TZ=/TZ= prefix (a second timezone source that would
// silently compete with notebook.dreaming.timezone) and a schedule whose date can
// never occur (e.g. "0 0 30 2 *", Feb 30) — robfig cron returns the zero time for
// those, which the scheduler would treat as perpetually due and re-harvest in a
// tight loop.
func validateNotebookDreamingFrequency(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if hasCronTZPrefix(trimmed) {
		return fmt.Errorf("notebook.dreaming.frequency must not embed a CRON_TZ=/TZ= prefix; set notebook.dreaming.timezone instead")
	}
	sched, err := cron.ParseStandard(trimmed)
	if err != nil {
		return fmt.Errorf("notebook.dreaming.frequency must be a cron expression (5 fields, or a descriptor like @daily): %w", err)
	}
	if sched.Next(time.Now()).IsZero() {
		return fmt.Errorf("notebook.dreaming.frequency %q describes a time that never occurs", trimmed)
	}
	return nil
}

// hasCronTZPrefix reports whether a cron string carries a leading TZ=/CRON_TZ=
// timezone prefix (the form robfig/cron's ParseStandard honors).
func hasCronTZPrefix(expr string) bool {
	return strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=")
}

// validateNotebookDreamingTimezone accepts an empty value (local time) or an IANA
// timezone name loadable on this machine.
func validateNotebookDreamingTimezone(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := time.LoadLocation(strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("notebook.dreaming.timezone must be an IANA timezone: %w", err)
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
