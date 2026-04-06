package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	SettingProjectsDirectory        = "projects_directory"
	SettingUIScale                  = "uiScale"
	SettingClaudeExecutable         = "claude_executable"
	SettingCodexExecutable          = "codex_executable"
	SettingCopilotExecutable        = "copilot_executable"
	SettingPiExecutable             = "pi_executable"
	SettingEditorExecutable         = "editor_executable"
	SettingNewSessionAgent          = "new_session_agent"
	SettingClaudeAvailable          = "claude_available"
	SettingCodexAvailable           = "codex_available"
	SettingCopilotAvailable         = "copilot_available"
	SettingPiAvailable              = "pi_available"
	SettingPTYBackendMode           = "pty_backend_mode"
	SettingTheme                    = "theme"
	SettingReviewLoopPresets        = "review_loop_prompt_presets"
	SettingReviewLoopLastPreset     = "review_loop_last_preset"
	SettingReviewLoopLastPrompt     = "review_loop_last_prompt"
	SettingReviewLoopLastIterations = "review_loop_last_iterations"
	SettingReviewLoopModel          = "review_loop_model"
	SettingReviewerModel            = "reviewer_model"
	SettingTailscaleEnabled         = "tailscale_enabled"
	SettingNewSessionYoloPrefix     = "new_session_yolo_"
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
		if available && name == string(protocol.SessionAgentClaude) {
			if err := agentdriver.EnsureClaudeSkillInstalled(); err != nil {
				d.logf("failed to ensure Claude attn skill: %v", err)
			}
		}

		caps := agentdriver.EffectiveCapabilities(driver)
		settings[capabilitySettingKey(name, "hooks")] = strconv.FormatBool(caps.HasHooks)
		settings[capabilitySettingKey(name, "transcript")] = strconv.FormatBool(caps.HasTranscript)
		settings[capabilitySettingKey(name, "transcript_watcher")] = strconv.FormatBool(caps.HasTranscriptWatcher)
		settings[capabilitySettingKey(name, "classifier")] = strconv.FormatBool(caps.HasClassifier)
		settings[capabilitySettingKey(name, "state_detector")] = strconv.FormatBool(caps.HasStateDetector)
		settings[capabilitySettingKey(name, "resume")] = strconv.FormatBool(caps.HasResume)
		settings[capabilitySettingKey(name, "fork")] = strconv.FormatBool(caps.HasFork)
		settings[capabilitySettingKey(name, "yolo")] = strconv.FormatBool(caps.HasYolo)
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
	if _, ok := settings[SettingPiAvailable]; !ok {
		settings[SettingPiAvailable] = settings[availabilitySettingKey("pi")]
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
	case SettingClaudeExecutable, SettingCodexExecutable, SettingCopilotExecutable, SettingPiExecutable:
		return validateExecutableSetting(value)
	case SettingEditorExecutable:
		return validateEditorSetting(value)
	case SettingNewSessionAgent:
		return validateNewSessionAgent(value)
	case SettingTheme:
		return validateTheme(value)
	case SettingTailscaleEnabled:
		return validateBooleanSetting(value)
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

func validateNewSessionAgent(value string) error {
	agent := strings.TrimSpace(strings.ToLower(value))
	if agent == "" {
		return nil
	}
	if agentdriver.Get(agent) == nil {
		return fmt.Errorf("unknown agent: %s", value)
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
