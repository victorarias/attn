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
	"github.com/victorarias/attn/internal/modelcapture"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessioncost"
)

const (
	SettingProjectsDirectory = "projects_directory"
	SettingUIScale           = "uiScale"
	// SettingTicketBoardScale scales ticket-board fonts independently of
	// uiScale; empty => follows uiScale.
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
	// Model capture is an explicit, local-only opt-in: visible terminal text
	// can contain secrets.
	SettingModelCaptureEnabled         = "model_capture.enabled"
	SettingModelCaptureIntervalSeconds = "model_capture.interval_seconds"
	SettingModelCaptureMaxGB           = "model_capture.max_gb"
	// Read-only effective status surfaced to Settings.
	SettingModelCapturePath  = "model_capture.path"
	SettingModelCaptureBytes = "model_capture.bytes"
	// SettingQueueModeEnabled selects the sidebar arrangement (workspace tree
	// alone vs chief slot + "Your turn" band). The daemon stamps turns either way.
	SettingQueueModeEnabled = "queue_mode_enabled"
	// SettingAutoApproveEnabled launches interactive agents in their native
	// auto-approve mode. Off by default; yolo overrides it.
	SettingAutoApproveEnabled = "auto_approve_enabled"
	// SettingOpenSentFilesEnabled gates auto-opening the files an agent hands
	// the user (Claude's SendUserFile) as workspace tiles. Default ON; only an
	// explicit "false" disables.
	SettingOpenSentFilesEnabled = "open_sent_files_enabled"
	// SettingAutoSettleEnabled closes a turn for the user once they have steered
	// the agent back to work. Off by default — it mutates state unasked.
	SettingAutoSettleEnabled = "auto_settle_enabled"
	// SettingAutoSettleArmSeconds: how long a session holds `working` before the
	// countdown starts. Empty => defaultAutoSettleArmSeconds.
	SettingAutoSettleArmSeconds = "auto_settle_arm_seconds"
	// SettingAutoSettleCountdownSeconds: the visible cancel window before the
	// turn settles. Empty => defaultAutoSettleCountdownSeconds.
	SettingAutoSettleCountdownSeconds = "auto_settle_countdown_seconds"
	SettingKeybindingsConfig          = "keybindings_config"
	SettingNewSessionYoloPrefix       = "new_session_yolo_"
	// SettingNewSessionDestinationPrefix + repo scope remembers where that repo's
	// last new session went (DestinationNewWorktree / DestinationMainRepo).
	// Empty => new worktree; opening an existing worktree writes nothing.
	SettingNewSessionDestinationPrefix = "new_session_destination_"
	DestinationNewWorktree             = "new_worktree"
	DestinationMainRepo                = "main_repo"
	// SettingChiefModelPrefix + agent pins the model a chief-of-staff launch
	// uses (--model). Empty => agent default; chief launches only.
	SettingChiefModelPrefix = "chief_model_"
	// SettingChiefEffortPrefix + agent pins a chief launch's reasoning effort
	// via the agent's native mechanism. Empty => agent default.
	SettingChiefEffortPrefix = "chief_effort_"
	// SettingDefaultModelPrefix + agent pins the model EVERY interactive launch
	// uses; per-spawn pins and chief_model_<agent> outrank it (resolveLaunchModel).
	SettingDefaultModelPrefix = "default_model_"
	// SettingDefaultEffortPrefix + agent: same, for reasoning effort
	// (resolveLaunchEffort).
	SettingDefaultEffortPrefix = "default_effort_"
	// SettingNotebookRoot overrides the notebook's filesystem root. Empty =>
	// the profile-derived default (~/attn-notebook[-profile]).
	SettingNotebookRoot = "notebook.root"
	// SettingNotebookRootEffective is READ-ONLY and daemon-computed (never
	// stored, never accepted by set_setting): the folder the notebook resolves to.
	SettingNotebookRootEffective = "notebook.root.effective"
	// SettingNotebookCronFrequency: cron expression for the notebook's nightly
	// maintenance slot. Empty => "0 3 * * *".
	SettingNotebookCronFrequency = "notebook.cron.frequency"
	// SettingNotebookCronTimezone: IANA timezone the frequency is evaluated in.
	// Empty => local time.
	SettingNotebookCronTimezone = "notebook.cron.timezone"
	// SettingNotebookSummarizeSession configures the per-session summarize pass
	// (CHEAP tier). JSON {"agent","model"}; empty => Claude Haiku default.
	SettingNotebookSummarizeSession = "notebook.summarize_session"
	// SettingNotebookSummarizeSessionEnabled gates the summarize pass. Default
	// ON; only an explicit "false" disables. The keeper master switch outranks it.
	SettingNotebookSummarizeSessionEnabled = "notebook.summarize_session.enabled"
	// SettingNotebookNarrateWorkspace configures the curated-journal narrate pass
	// (STRONG tier). JSON {"agent","model"}; empty => Claude Sonnet, whose native
	// Write/Edit enforce read-before-write CAS on the journal.
	SettingNotebookNarrateWorkspace = "notebook.narrate_workspace"
	// SettingNotebookNarrateWorkspaceEnabled gates every narrate path. Default
	// ON; only an explicit "false" disables.
	SettingNotebookNarrateWorkspaceEnabled = "notebook.narrate_workspace.enabled"
	// SettingActivityEnabled gates session activity (one present-tense line per
	// session from its transcript). Off by default: it spends money per session
	// per refresh and sends transcript excerpts to a model.
	SettingActivityEnabled = "activity.enabled"
	// SettingActivityConfig selects the generator. JSON
	// {"agent":"claude"|"codex","model":"<id>","effort":"<effort>"}; empty means
	// UNCONFIGURED — deliberately no default agent (see parseActivityConfig).
	SettingActivityConfig = "activity.config"
	// SettingActivityIntervals is the per-presence-tier cadence, JSON
	// {"watching":120,"present":300} in seconds. No `away` tier by design:
	// stop is not a rate.
	SettingActivityIntervals = "activity.intervals"
	// SettingActivityPresenceIdleSeconds is how long after the last app input
	// the `present` tier survives. Default 90, UNMEASURED; safe because `away`
	// is self-healing.
	SettingActivityPresenceIdleSeconds = "activity.presence_idle_seconds"
	// SettingCrewHeartbeatEnabled gates the cache-warming heartbeat. Default ON
	// (blank means enabled); only an explicit "false" disables it.
	SettingCrewHeartbeatEnabled = "crew.heartbeat_enabled"
	// SettingCrewAutoSleepEnabled gates auto-sleep — the prompted handoff that
	// ends a member's day when the user is truly gone. Default ON.
	SettingCrewAutoSleepEnabled = "crew.autosleep_enabled"
	// SettingCrewCacheTTLSeconds is the assumed prompt-cache lifetime for a
	// harness this daemon has no shipped assumption for. Per-harness override:
	// SettingCrewCacheTTLPrefix + agent.
	SettingCrewCacheTTLSeconds = "crew.cache_ttl_seconds"
	// SettingCrewCacheTTLPrefix + agent overrides the assumption for one harness,
	// e.g. `crew.cache_ttl_seconds.claude`.
	SettingCrewCacheTTLPrefix = "crew.cache_ttl_seconds."
	// SettingCrewHeartbeatLeadSeconds is how far ahead of the estimated expiry
	// attn acts.
	SettingCrewHeartbeatLeadSeconds = "crew.heartbeat_lead_seconds"
	// SettingCrewAwaySeconds is how long an absence must last to count as one.
	SettingCrewAwaySeconds = "crew.away_seconds"
	// SettingCrewWakeLimit caps autonomous wakes per member per window. 0 turns
	// every autonomous wake off, loudly.
	SettingCrewWakeLimit = "crew.wake_limit"
	// SettingCrewWakeLimitWindowSeconds is the window that limit counts over.
	SettingCrewWakeLimitWindowSeconds = "crew.wake_limit_window_seconds"
	// SettingChiefContextWindowCap caps the chief session's effective context
	// window (tokens) so each cache-cold wake re-reads less. Empty =>
	// DefaultContextWindowCap; chief launches only.
	SettingChiefContextWindowCap = "chief_context_window_cap"
	// SettingHeadlessContextWindowCap caps every headless run the same way; a
	// run that grows past it is a bug, not accommodated. Empty =>
	// DefaultContextWindowCap.
	SettingHeadlessContextWindowCap = "headless_context_window_cap"
	// SettingDefaultContextWindowCapPrefix + agent caps EVERY interactive launch of
	// that agent. Empty => uncapped; chief_context_window_cap outranks it.
	SettingDefaultContextWindowCapPrefix = "default_context_window_cap_"
	// SettingNotebookTasksEnabled is the master switch for ALL keeper background
	// duties. Default ON (blank means enabled); only an explicit "false"
	// disables the group.
	SettingNotebookTasksEnabled = "notebook.tasks_enabled"
	// SettingDBLastBackupAt is READ-ONLY and daemon-computed: UTC RFC3339 stamp
	// of the last successful rotating backup this process lifetime.
	SettingDBLastBackupAt = "db.last_backup_at"
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
	if isSessionCostPriceSetting(msg.Key) {
		d.publishSessionCostReprices()
	}
	if msg.Key == SettingTailscaleEnabled {
		d.ensureTailscaleServeFromSettings()
	}
	if msg.Key == SettingHeadlessContextWindowCap {
		d.applyHeadlessContextWindowCap()
	}
	// Off must stop a countdown already on screen; on must reach sessions
	// already working. Durations are re-read per arm and need neither.
	if msg.Key == SettingAutoSettleEnabled {
		if parseBooleanSetting(msg.Value) {
			d.armAutoSettleForRunningSessions()
		} else {
			d.cancelAllAutoSettle()
		}
	}
	// Turning session activity off has to take the lines with it. They are
	// stored on the session and would otherwise keep sitting on home describing
	// work from whenever the feature was last on — a switch that stops the
	// spending but not the claim is the worst of both.
	if msg.Key == SettingActivityEnabled && !parseBooleanSetting(msg.Value) {
		d.clearAllSessionActivity()
	}
	d.publishSettingsFact(FactSettingChanged, msg.Key)
}

// publishSettingsFact refreshes the tailscale serve state, then publishes.
// Every settings-re-pushing fact goes through here so none forgets the refresh.
func (d *Daemon) publishSettingsFact(name, subject string) {
	d.refreshTailscaleServeState()
	d.publishFact(name, subject, nil)
}

// projectSettingsUpdated pushes the settings snapshot; changedKey is empty when
// what moved was not a user-set setting.
func (d *Daemon) projectSettingsUpdated(changedKey string) {
	d.projectSnapshot(snapshotSettings, func() {
		event := &protocol.SettingsUpdatedMessage{
			Event:    protocol.EventSettingsUpdated,
			Settings: d.settingsWithAgentAvailability(),
		}
		if strings.TrimSpace(changedKey) != "" {
			event.ChangedKey = protocol.Ptr(changedKey)
		}
		d.broadcastMessage(event)
	})
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
				if err := agentdriver.EnsureAgentsSkillInstalled(); err != nil {
					d.logf("failed to ensure ~/.agents attn skill: %v", err)
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
	d.lastBackupMu.Lock()
	lastBackupAt := d.lastBackupAt
	d.lastBackupMu.Unlock()
	if !lastBackupAt.IsZero() {
		settings[SettingDBLastBackupAt] = lastBackupAt.Format(time.RFC3339)
	}
	settings[SettingTailscaleEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingTailscaleEnabled]))
	settings[SettingWorkflowsEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingWorkflowsEnabled]))
	settings[SettingModelCaptureEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingModelCaptureEnabled]))
	settings[SettingModelCaptureIntervalSeconds] = strconv.Itoa(int(d.modelCaptureInterval() / time.Second))
	settings[SettingModelCaptureMaxGB] = strconv.FormatInt(d.modelCaptureMaxBytes()>>30, 10)
	settings[SettingModelCapturePath] = d.modelCaptureDir()
	if bytes, err := modelcapture.SizeBytes(d.modelCaptureDir()); err == nil {
		settings[SettingModelCaptureBytes] = strconv.FormatInt(bytes, 10)
	} else {
		d.logf("model capture size failed: %v", err)
		settings[SettingModelCaptureBytes] = "0"
	}
	settings[SettingQueueModeEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingQueueModeEnabled]))
	settings[SettingAutoApproveEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingAutoApproveEnabled]))
	// Surface EFFECTIVE auto-settle policy, not absent keys.
	settings[SettingAutoSettleEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingAutoSettleEnabled]))
	settings[SettingAutoSettleArmSeconds] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[SettingAutoSettleArmSeconds], defaultAutoSettleArmSeconds) / time.Second))
	settings[SettingAutoSettleCountdownSeconds] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[SettingAutoSettleCountdownSeconds], defaultAutoSettleCountdownSeconds) / time.Second))
	// These are default-ON: send EFFECTIVE values so the app never mistakes an
	// absent key for off.
	settings[SettingNotebookTasksEnabled] = strconv.FormatBool(d.notebookTasksEnabled())
	settings[SettingNotebookSummarizeSessionEnabled] = strconv.FormatBool(d.notebookSummariesEnabled())
	settings[SettingNotebookNarrateWorkspaceEnabled] = strconv.FormatBool(d.notebookWorkspaceNarrationEnabled())
	settings[SettingOpenSentFilesEnabled] = strconv.FormatBool(d.openSentFilesEnabled())
	// EFFECTIVE token caps too, so the UI shows the concrete default.
	settings[SettingChiefContextWindowCap] = strconv.Itoa(resolveContextWindowCap(stored[SettingChiefContextWindowCap]))
	settings[SettingHeadlessContextWindowCap] = strconv.Itoa(resolveContextWindowCap(stored[SettingHeadlessContextWindowCap]))
	// Session activity. The toggle and the intervals are surfaced EFFECTIVE, so
	// the pane shows the concrete defaults rather than absent keys. activity.config
	// is deliberately NOT normalized: blank means no agent has been chosen, and
	// substituting one here would be the app quietly choosing how the user's money
	// gets spent. The pane must require the choice.
	settings[SettingActivityEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingActivityEnabled]))
	settings[SettingActivityPresenceIdleSeconds] = strconv.Itoa(int(d.presenceIdleLimit() / time.Second))
	if intervals, err := parseActivityIntervals(stored[SettingActivityIntervals]); err == nil {
		if encoded, err := json.Marshal(intervals); err == nil {
			settings[SettingActivityIntervals] = string(encoded)
		}
	}
	// The crew lifecycle, surfaced EFFECTIVE for the same reason: a member's
	// daily rhythm is set by these numbers, and an absent key reads as "off"
	// when every one of them is on by default. Per-harness cache-TTL overrides
	// (crew.cache_ttl_seconds.<agent>) are not surfaced — they are exceptions,
	// and listing every harness attn might ever launch would be noise.
	settings[SettingCrewHeartbeatEnabled] = strconv.FormatBool(d.crewBoolSetting(SettingCrewHeartbeatEnabled))
	settings[SettingCrewAutoSleepEnabled] = strconv.FormatBool(d.crewBoolSetting(SettingCrewAutoSleepEnabled))
	settings[SettingCrewCacheTTLSeconds] = strconv.Itoa(int(d.crewCacheTTL("") / time.Second))
	settings[SettingCrewHeartbeatLeadSeconds] = strconv.Itoa(int(d.crewHeartbeatLead() / time.Second))
	settings[SettingCrewAwaySeconds] = strconv.Itoa(int(d.crewAwayLimit() / time.Second))
	crewWakes := d.crewWakeLedger()
	settings[SettingCrewWakeLimit] = strconv.Itoa(crewWakes.Limit)
	settings[SettingCrewWakeLimitWindowSeconds] = strconv.Itoa(int(crewWakes.Window / time.Second))
	// The presence tier is deliberately NOT here. It is live state, and settings
	// are only re-pushed when a setting changes, so a copy parked in this
	// snapshot goes stale within seconds of the user moving. `attn activity`
	// computes it per request, which is the only way to read it honestly.

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

// chiefLaunchModel returns chief_model_<agent>, or "" when not a chief launch
// or unconfigured.
func (d *Daemon) chiefLaunchModel(agent string, chief bool) string {
	if !chief {
		return ""
	}
	return strings.TrimSpace(d.store.GetSetting(SettingChiefModelPrefix + strings.ToLower(strings.TrimSpace(agent))))
}

// chiefLaunchEffort returns chief_effort_<agent>, or "" when not a chief launch
// or unconfigured.
func (d *Daemon) chiefLaunchEffort(agent string, chief bool) string {
	if !chief {
		return ""
	}
	return strings.TrimSpace(d.store.GetSetting(SettingChiefEffortPrefix + strings.ToLower(strings.TrimSpace(agent))))
}

// defaultLaunchModel returns default_model_<agent>, or "" when unconfigured.
func (d *Daemon) defaultLaunchModel(agent string) string {
	return strings.TrimSpace(d.store.GetSetting(SettingDefaultModelPrefix + strings.ToLower(strings.TrimSpace(agent))))
}

// defaultLaunchEffort returns default_effort_<agent>, or "" when unconfigured.
func (d *Daemon) defaultLaunchEffort(agent string) string {
	return strings.TrimSpace(d.store.GetSetting(SettingDefaultEffortPrefix + strings.ToLower(strings.TrimSpace(agent))))
}

// resolveLaunchModel: per-spawn pin, then chief_model_<agent> for chief
// launches, then default_model_<agent>, then "" (the agent's own default).
func (d *Daemon) resolveLaunchModel(agent string, chief bool, requested string) string {
	if requested != "" {
		return requested
	}
	if model := d.chiefLaunchModel(agent, chief); model != "" {
		return model
	}
	return d.defaultLaunchModel(agent)
}

// resolveLaunchEffort mirrors resolveLaunchModel for reasoning effort.
func (d *Daemon) resolveLaunchEffort(agent string, chief bool, requested string) string {
	if requested != "" {
		return requested
	}
	if effort := d.chiefLaunchEffort(agent, chief); effort != "" {
		return effort
	}
	return d.defaultLaunchEffort(agent)
}

// launchContextWindowCap returns the effective token cap for an interactive
// launch, or 0. Precedence: per-session pin, then chief_context_window_cap for
// chief launches, then default_context_window_cap_<agent> (unset => uncapped).
func (d *Daemon) launchContextWindowCap(sessionID, agent string, chief bool) int {
	if session := d.store.Get(strings.TrimSpace(sessionID)); session != nil {
		if cap := protocol.Deref(session.ContextWindowCap); cap > 0 {
			return cap
		}
	}
	if chief {
		return resolveContextWindowCap(d.store.GetSetting(SettingChiefContextWindowCap))
	}
	key := SettingDefaultContextWindowCapPrefix + strings.ToLower(strings.TrimSpace(agent))
	if v := strings.TrimSpace(d.store.GetSetting(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// applyHeadlessContextWindowCap pushes headless_context_window_cap into the
// process-global the headless spawn seam reads; called at startup and on change.
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
	case SettingTailscaleEnabled, SettingWorkflowsEnabled, SettingAutoApproveEnabled, SettingNotebookTasksEnabled, SettingNotebookSummarizeSessionEnabled, SettingNotebookNarrateWorkspaceEnabled, SettingQueueModeEnabled, SettingAutoSettleEnabled, SettingModelCaptureEnabled, SettingActivityEnabled, SettingOpenSentFilesEnabled:
		return validateBooleanSetting(value)
	case SettingModelCaptureIntervalSeconds:
		return validateModelCaptureInterval(value)
	case SettingModelCaptureMaxGB:
		return validateModelCaptureMaxGB(value)
	case SettingAutoSettleArmSeconds:
		return validateAutoSettleSeconds("auto-settle delay", value, autoSettleArmMinSeconds, autoSettleArmMaxSeconds)
	case SettingAutoSettleCountdownSeconds:
		return validateAutoSettleSeconds("auto-settle countdown", value, autoSettleCountdownMinSeconds, autoSettleCountdownMaxSeconds)
	case SettingChiefContextWindowCap, SettingHeadlessContextWindowCap:
		return validateContextWindowCap(value)
	case SettingKeeperCompact:
		return d.validateKeeperCompactSetting(value)
	case SettingNotebookSummarizeSession:
		return d.validateNotebookNarrationSetting(notebookSummarizeSessionKind, value)
	case SettingNotebookNarrateWorkspace:
		return d.validateNotebookNarrationSetting(notebookNarrateWorkspaceKind, value)
	case SettingActivityConfig:
		return d.validateActivitySetting(value)
	case SettingActivityIntervals:
		_, err := parseActivityIntervals(value)
		return err
	case SettingActivityPresenceIdleSeconds:
		return validateBoundedIntSetting(
			"session activity presence idle",
			value,
			activityPresenceIdleMinSeconds,
			activityPresenceIdleMaxSeconds,
		)
	case SettingCrewHeartbeatEnabled, SettingCrewAutoSleepEnabled:
		return validateBooleanSetting(value)
	case SettingCrewCacheTTLSeconds:
		return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
	case SettingCrewHeartbeatLeadSeconds:
		return validateBoundedIntSetting("crew heartbeat lead", value, crewHeartbeatLeadMinSeconds, crewHeartbeatLeadMaxSeconds)
	case SettingCrewAwaySeconds:
		return validateBoundedIntSetting("crew away threshold", value, crewAwayMinSeconds, crewAwayMaxSeconds)
	case SettingCrewWakeLimit:
		// Zero is meaningful — it turns autonomous wakes off — so the floor is 0,
		// not 1.
		return validateBoundedIntSetting("crew wake limit", value, 0, crewWakeLimitMax)
	case SettingCrewWakeLimitWindowSeconds:
		return validateBoundedIntSetting("crew wake limit window", value, crewWakeLimitWindowMinSecs, crewWakeLimitWindowMaxSecs)
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
		if isSessionCostPriceSetting(key) {
			_, err := sessioncost.ParseOverrides(map[string]string{key: value})
			return err
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingCrewCacheTTLPrefix) {
			return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingNewSessionYoloPrefix) {
			return validateBooleanSetting(value)
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingNewSessionDestinationPrefix) {
			return validateNewSessionDestination(value)
		}
		// Model names and effort levels are free-form / agent-native: accept
		// anything and let the agent reject bad ones.
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingChiefModelPrefix) {
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingChiefEffortPrefix) {
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingDefaultModelPrefix) {
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingDefaultEffortPrefix) {
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(key)), SettingDefaultContextWindowCapPrefix) {
			// Same bounds as the chief/headless caps; blank means uncapped here.
			return validateContextWindowCap(value)
		}
		if _, ok := isAgentExecutableSettingKey(key); ok {
			return validateExecutableSetting(value)
		}
		return fmt.Errorf("unknown setting: %s", key)
	}
}

// validateAutoSettleSeconds accepts empty (the built-in default) or whole
// seconds inside the bounds; label names which of the two windows failed.
func validateAutoSettleSeconds(label, value string, minSeconds, maxSeconds int) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return fmt.Errorf("%s must be a whole number of seconds: %s", label, value)
	}
	if n < minSeconds || n > maxSeconds {
		return fmt.Errorf("%s must be between %d and %d seconds", label, minSeconds, maxSeconds)
	}
	return nil
}

// validateNewSessionDestination accepts empty (no remembered choice) or one of
// the two destinations the picker can record.
func validateNewSessionDestination(value string) error {
	switch strings.TrimSpace(value) {
	case "", DestinationNewWorktree, DestinationMainRepo:
		return nil
	default:
		return fmt.Errorf("new session destination must be %q or %q: %s", DestinationNewWorktree, DestinationMainRepo, value)
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

// contextWindowCap bounds. The knob can only REDUCE the window, so the ceiling
// is a fat-finger guard; the floor keeps compaction from thrashing.
const (
	contextWindowCapMin = 10000
	contextWindowCapMax = 2000000
)

// validateContextWindowCap accepts empty or whole tokens inside the bounds.
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

// resolveContextWindowCap turns a stored value into an effective token cap,
// defaulting when unset/blank/unparseable.
func resolveContextWindowCap(stored string) int {
	if trimmed := strings.TrimSpace(stored); trimmed != "" {
		if n, err := strconv.Atoi(trimmed); err == nil && n > 0 {
			return n
		}
	}
	return agentdriver.DefaultContextWindowCap
}

// validateNotebookRoot accepts empty (the profile-derived default) or an
// absolute path outside the attn data dir; see normalizeExternalRoot.
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

// normalizeExternalRoot expands ~/, requires an absolute path, and rejects a path
// at or inside the attn data dir. Empty returns ("", nil); errors are unprefixed
// so each caller adds its own vocabulary.
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
	// Symlinked roots are permitted, so a symlink can defeat the lexical check
	// above. The canonical form is used ONLY for comparison — the cleaned path is
	// returned, so legitimate symlinked roots keep their spelling.
	canonRoot := canonicalizeForComparison(clean)
	canonData := canonicalizeForComparison(dataDir)
	if canonRoot == canonData || strings.HasPrefix(canonRoot, canonData+string(filepath.Separator)) {
		return "", fmt.Errorf("must be outside the attn data dir (%s)", dataDir)
	}
	return clean, nil
}

// canonicalizeForComparison resolves symlinks in the deepest existing ancestor
// and re-joins the rest lexically. Used ONLY for containment comparison; never
// returned to callers as the root.
func canonicalizeForComparison(path string) string {
	clean := filepath.Clean(path)
	ancestor := clean
	var remainder []string
	for {
		if _, err := os.Stat(ancestor); err == nil {
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return clean
		}
		remainder = append([]string{filepath.Base(ancestor)}, remainder...)
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return clean
	}
	return filepath.Join(append([]string{resolved}, remainder...)...)
}

// validateNotebookCronFrequency rejects an embedded CRON_TZ=/TZ= prefix (competes
// with notebook.cron.timezone) and a never-occurring date like Feb 30 — robfig
// returns the zero time for those, which the scheduler re-fires in a loop.
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

// hasCronTZPrefix reports a leading TZ=/CRON_TZ= prefix (the form
// robfig/cron's ParseStandard honors).
func hasCronTZPrefix(expr string) bool {
	return strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=")
}

// validateNotebookCronTimezone accepts empty (local time) or a loadable IANA name.
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

// validateKeybindingsConfig only guarantees parseable JSON (or empty); the
// frontend owns the shortcut schema.
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
