package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// Narration task kinds (runner executor selectors). They live on d.compactRunner
// alongside compactContextKind ("compact_context").
const (
	notebookSummarizeSessionKind = "summarize_session"
	notebookNarrateWorkspaceKind = "narrate_workspace"
)

// Tier-default model ids. Narration ALWAYS runs (unlike the janitor, which
// disables on a blank setting): when a setting is unset/blank we fall back to a
// built-in default so session-end and removal-boundary narration work out of the
// box. Claude is the default narrator for BOTH tiers because its native
// Write/Edit enforce read-before-write CAS, which the shared-journal concurrency
// story depends on (Codex apply-patch CAS is unverified for the installed
// version — see notebook_narration.go).
//
//   - cheap (summarize_session): Claude Haiku — per-session, high-frequency, only
//     produces raw input the narrator re-reads.
//   - strong (narrate_workspace): Claude Sonnet — writes the curated journal, the
//     load-bearing product surface where quality is the point.
const (
	notebookSummarizeDefaultAgent = "claude"
	notebookSummarizeDefaultModel = "claude-haiku-4-5"
	notebookNarrateDefaultAgent   = "claude"
	notebookNarrateDefaultModel   = "claude-sonnet-4-6"
)

// notebookNarrationConfig is the resolved {agent, model} for a narration kind. It
// is never "disabled": parseNotebookNarrationConfig substitutes the tier default
// for a blank setting, so Agent/Model are always populated for a valid config.
type notebookNarrationConfig struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}

// narrationTierDefault returns the built-in {agent, model} for a narration kind.
func narrationTierDefault(kind string) (agent, model string) {
	switch kind {
	case notebookSummarizeSessionKind:
		return notebookSummarizeDefaultAgent, notebookSummarizeDefaultModel
	case notebookNarrateWorkspaceKind:
		return notebookNarrateDefaultAgent, notebookNarrateDefaultModel
	default:
		return notebookNarrateDefaultAgent, notebookNarrateDefaultModel
	}
}

// parseNotebookNarrationConfig parses a narration setting value (the same JSON
// shape the janitor validates: {"agent":"claude"|"codex","model":"<id>"}) and
// resolves the provider, validating at config/enqueue time so a misconfigured
// agent/model fails fast into failed->dead with a surfaced last_error rather than
// hanging an executor mid-run. Unlike parseWorkspaceContextJanitorConfig, a BLANK
// value yields the tier DEFAULT (narration is always-on), not a disabled config.
// A non-blank value must specify both agent and model.
func parseNotebookNarrationConfig(kind, raw string) (notebookNarrationConfig, error) {
	raw = strings.TrimSpace(raw)
	var config notebookNarrationConfig
	if raw == "" {
		config.Agent, config.Model = narrationTierDefault(kind)
	} else {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return notebookNarrationConfig{}, fmt.Errorf("invalid %s configuration: %w", kind, err)
		}
		if err := ensureJSONEOF(decoder); err != nil {
			return notebookNarrationConfig{}, fmt.Errorf("invalid %s configuration: %w", kind, err)
		}
		config.Agent = strings.TrimSpace(strings.ToLower(config.Agent))
		config.Model = strings.TrimSpace(config.Model)
		if config.Agent == "" || config.Model == "" {
			return notebookNarrationConfig{}, fmt.Errorf("%s requires both agent and model", kind)
		}
	}

	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return notebookNarrationConfig{}, fmt.Errorf("%s agent is not installed: %s", kind, config.Agent)
	}
	if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
		return notebookNarrationConfig{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	if available, reason := agentdriver.HeadlessTaskAvailability(driver); !available {
		return notebookNarrationConfig{}, fmt.Errorf("agent %s cannot run headless tasks: %s", config.Agent, reason)
	}
	return config, nil
}

// validateNotebookNarrationSetting is the set-setting validator: it parses the
// value and additionally resolves the executable on PATH, so a bad agent/model/
// executable is rejected at config time rather than failing an enqueued task.
func (d *Daemon) validateNotebookNarrationSetting(kind, raw string) error {
	config, err := parseNotebookNarrationConfig(kind, raw)
	if err != nil {
		return err
	}
	driver := agentdriver.Get(config.Agent)
	configured := ""
	if d.store != nil {
		configured = d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	}
	executable := driver.ResolveExecutable(configured)
	if _, err := exec.LookPath(executable); err != nil {
		return fmt.Errorf("%s executable for %s was not found: %w", kind, config.Agent, err)
	}
	return nil
}

// notebookNarrationConfigFor loads the resolved config for a narration kind from
// settings, applying the tier default when unset. Returns an error (and never a
// blank config) when the configured agent/model is invalid — the caller fails the
// task so the misconfiguration surfaces.
func (d *Daemon) notebookNarrationConfigFor(kind string) (notebookNarrationConfig, error) {
	if d.store == nil {
		return notebookNarrationConfig{}, errors.New("notebook narration settings unavailable")
	}
	settingKey := ""
	switch kind {
	case notebookSummarizeSessionKind:
		settingKey = SettingNotebookSummarizeSession
	case notebookNarrateWorkspaceKind:
		settingKey = SettingNotebookNarrateWorkspace
	default:
		return notebookNarrationConfig{}, fmt.Errorf("unknown narration kind: %s", kind)
	}
	return parseNotebookNarrationConfig(kind, d.store.GetSetting(settingKey))
}

// resolveNotebookNarrationExecutable resolves the agent's executable path on PATH
// for a parsed narration config, mirroring executeWorkspaceContextJanitor's
// provider resolution. Returns the HeadlessTaskProvider and the absolute path.
func (d *Daemon) resolveNotebookNarrationExecutable(
	config notebookNarrationConfig,
) (agentdriver.HeadlessTaskProvider, string, error) {
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return nil, "", fmt.Errorf("narration agent not found: %s", config.Agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return nil, "", fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	configured := d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	resolvedExecutable := driver.ResolveExecutable(configured)
	executablePath, err := exec.LookPath(resolvedExecutable)
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s executable: %w", config.Agent, err)
	}
	return provider, executablePath, nil
}
