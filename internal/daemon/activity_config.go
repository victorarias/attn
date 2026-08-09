package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// sessionActivityKind is the job kind the activity executor is registered under.
const sessionActivityKind = "session_activity"

// Per-agent defaults, applied once the user has picked an agent. There is
// deliberately no default AGENT — see parseActivityConfig.
//
// Measured on the same corpus through the same seam (receipts in
// docs/plans/2026-08-07-session-activity.md): Codex on Luna answers in 4.8s p50
// for $0.0027 a run, Claude on Haiku in 11.7s for $0.011–0.017. The gap is not
// the models, it is what each CLI does around them — Claude Code bills a
// ~47K-token prefix and emits ~700–990 output tokens of unsuppressible thinking
// to deliver a 20-token answer, where Codex bills 13K and emits 15–66.
//
// Effort is left unset on Claude because it measured inert there (none, low,
// medium and high all land within 862–1,047 output tokens on identical input).
// On Codex it is live, and `low` produced the tightest latency tail.
const (
	activityClaudeDefaultModel = "claude-haiku-4-5"
	activityCodexDefaultModel  = "gpt-5.6-luna"
	activityCodexDefaultEffort = "low"
)

// Generation intervals per presence tier, in seconds. `away` has none by
// design: stop is not a rate.
const (
	defaultActivityWatchingSeconds = 120
	defaultActivityPresentSeconds  = 300
	activityIntervalMinSeconds     = 30
	activityIntervalMaxSeconds     = 3600
)

// defaultActivityPresenceIdleSeconds is how long after the last input in the app
// the `present` tier survives.
//
// UNMEASURED — a guess, and flagged as one. It is a safe guess because `away` is
// self-healing: leaving it is always an action that restores a higher tier, so
// erring short costs a few seconds of latency when the user comes back and
// erring long costs runs nobody sees. Replace it with a receipt if it ever
// matters.
const (
	defaultActivityPresenceIdleSeconds = 90
	activityPresenceIdleMinSeconds     = 10
	activityPresenceIdleMaxSeconds     = 3600
)

// activityConfig is the resolved {agent, model, effort} for the generator.
//
// Unlike notebookNarrationConfig, a blank setting does NOT resolve to a default
// agent. Claude and Codex differ enough in speed, price, and which account pays
// that picking one for the user would be choosing how their money is spent. So
// an enabled feature with no agent selected is a reported error, and the UI must
// require the choice before the toggle takes.
type activityConfig struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

// errActivityAgentUnset is what an enabled-but-unconfigured feature reports. It
// is a distinct error because it is the one misconfiguration that is a normal
// step in setting the feature up rather than a mistake, and the UI says
// something different about it.
var errActivityAgentUnset = errors.New("session activity has no agent selected: choose claude or codex")

// parseActivityConfig resolves the activity.config setting. A blank agent is an
// error, never a fallback; a blank model or effort takes that agent's default.
func parseActivityConfig(raw string) (activityConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return activityConfig{}, errActivityAgentUnset
	}

	var config activityConfig
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return activityConfig{}, fmt.Errorf("invalid session activity configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return activityConfig{}, fmt.Errorf("invalid session activity configuration: %w", err)
	}

	config.Agent = strings.TrimSpace(strings.ToLower(config.Agent))
	config.Model = strings.TrimSpace(config.Model)
	config.Effort = strings.TrimSpace(strings.ToLower(config.Effort))
	if config.Agent == "" {
		return activityConfig{}, errActivityAgentUnset
	}

	if config.Model == "" {
		switch config.Agent {
		case "claude":
			config.Model = activityClaudeDefaultModel
		case "codex":
			config.Model = activityCodexDefaultModel
		default:
			return activityConfig{}, fmt.Errorf("session activity requires a model for agent %s", config.Agent)
		}
	}
	if config.Effort == "" && config.Agent == "codex" {
		config.Effort = activityCodexDefaultEffort
	}

	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return activityConfig{}, fmt.Errorf("session activity agent is not installed: %s", config.Agent)
	}
	if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
		return activityConfig{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	if available, reason := agentdriver.HeadlessTaskAvailability(driver); !available {
		return activityConfig{}, fmt.Errorf("agent %s cannot run headless tasks: %s", config.Agent, reason)
	}
	return config, nil
}

// validateActivitySetting is the set-setting validator. It additionally resolves
// the executable on PATH, so a bad agent or a missing CLI is rejected while the
// user is looking at the settings pane rather than silently failing a job later.
//
// Clearing the setting is allowed: that is how the user un-picks an agent, and
// the enabled toggle is what decides whether an unconfigured feature matters.
func (d *Daemon) validateActivitySetting(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	config, err := parseActivityConfig(raw)
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
		return fmt.Errorf("session activity executable for %s was not found: %w", config.Agent, err)
	}
	return nil
}

// activityIntervals is the per-tier generation cadence.
type activityIntervals struct {
	Watching int `json:"watching"`
	Present  int `json:"present"`
}

// parseActivityIntervals resolves the activity.intervals setting. Both fields
// are clamped rather than rejected: a value outside the range is a settings-pane
// typo, and stopping generation over one helps nobody.
func parseActivityIntervals(raw string) (activityIntervals, error) {
	intervals := activityIntervals{
		Watching: defaultActivityWatchingSeconds,
		Present:  defaultActivityPresentSeconds,
	}
	if strings.TrimSpace(raw) == "" {
		return intervals, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intervals); err != nil {
		return activityIntervals{}, fmt.Errorf("invalid session activity intervals: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return activityIntervals{}, fmt.Errorf("invalid session activity intervals: %w", err)
	}
	intervals.Watching = clampInterval(intervals.Watching, defaultActivityWatchingSeconds)
	intervals.Present = clampInterval(intervals.Present, defaultActivityPresentSeconds)
	return intervals, nil
}

func clampInterval(seconds, fallback int) int {
	if seconds <= 0 {
		return fallback
	}
	if seconds < activityIntervalMinSeconds {
		return activityIntervalMinSeconds
	}
	if seconds > activityIntervalMaxSeconds {
		return activityIntervalMaxSeconds
	}
	return seconds
}

// activityEnabled reports the runtime switch. Off by default: the feature spends
// money per session per refresh and sends transcript excerpts to a model.
func (d *Daemon) activityEnabled() bool {
	if d.store == nil {
		return false
	}
	return parseBooleanSetting(d.store.GetSetting(SettingActivityEnabled))
}

// activityConfigured loads the resolved config, or reports why there is none.
func (d *Daemon) activityConfigured() (activityConfig, error) {
	if d.store == nil {
		return activityConfig{}, errors.New("session activity settings unavailable")
	}
	return parseActivityConfig(d.store.GetSetting(SettingActivityConfig))
}

// activityInterval is the generation cadence for a tier. `away` returns zero,
// which every caller reads as "generate nothing" rather than "generate now" —
// the one place that distinction has to be right.
func (d *Daemon) activityInterval(tier PresenceTier) time.Duration {
	if tier == PresenceAway {
		return 0
	}
	raw := ""
	if d.store != nil {
		raw = d.store.GetSetting(SettingActivityIntervals)
	}
	intervals, err := parseActivityIntervals(raw)
	if err != nil {
		// A stored value that no longer parses falls back to the defaults rather
		// than to zero: zero would silently stop the feature, which is exactly
		// the failure a user cannot diagnose from the dashboard.
		d.logf("activity: intervals setting is invalid (%v); using defaults", err)
		intervals = activityIntervals{
			Watching: defaultActivityWatchingSeconds,
			Present:  defaultActivityPresentSeconds,
		}
	}
	if tier == PresenceWatching {
		return time.Duration(intervals.Watching) * time.Second
	}
	return time.Duration(intervals.Present) * time.Second
}

// presenceIdleLimit is how long after the last input in the app the `present`
// tier survives. Daemon-owned: the client reports when input happened, never
// how long that should count for.
func (d *Daemon) presenceIdleLimit() time.Duration {
	seconds := defaultActivityPresenceIdleSeconds
	if d.store != nil {
		seconds = resolveBoundedIntSetting(
			d.store.GetSetting(SettingActivityPresenceIdleSeconds),
			defaultActivityPresenceIdleSeconds,
			activityPresenceIdleMinSeconds,
			activityPresenceIdleMaxSeconds,
		)
	}
	return time.Duration(seconds) * time.Second
}

// resolveActivityExecutable resolves the provider and absolute executable path
// for a parsed config, the same way narration does.
func (d *Daemon) resolveActivityExecutable(config activityConfig) (agentdriver.HeadlessTaskProvider, string, error) {
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return nil, "", fmt.Errorf("session activity agent not found: %s", config.Agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return nil, "", fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	configured := ""
	if d.store != nil {
		configured = d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	}
	executablePath, err := exec.LookPath(driver.ResolveExecutable(configured))
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s executable: %w", config.Agent, err)
	}
	return provider, executablePath, nil
}
