package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// Each attach gets its own subscriber id. The PTY session's subscriber map is
// keyed by subscriber id, and a workerStream close emits a Detach RPC for the
// id it registered. Reusing a subID on re-attach lets the dying stream's
// Detach remove the freshly installed subscriber, silently starving the new
// stream of output.
var wsSubscriberCounter atomic.Int64

const maxInitialPromptBytes = 1 << 20

func (d *Daemon) writeInitialPromptFile(sessionID, prompt string) (string, func(), error) {
	if strings.TrimSpace(prompt) == "" {
		return "", func() {}, nil
	}
	if len(prompt) > maxInitialPromptBytes {
		return "", func() {}, fmt.Errorf("initial prompt exceeds %d bytes", maxInitialPromptBytes)
	}
	dataRoot := strings.TrimSpace(d.dataRoot)
	if dataRoot == "" {
		dataRoot = filepath.Dir(d.socketPath)
	}
	dir := filepath.Join(dataRoot, "runtime", "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("create initial prompt directory: %w", err)
	}
	file, err := os.CreateTemp(dir, sessionID+"-*.md")
	if err != nil {
		return "", func() {}, fmt.Errorf("create initial prompt file: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("secure initial prompt file: %w", err)
	}
	if _, err := file.WriteString(prompt); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("write initial prompt file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close initial prompt file: %w", err)
	}
	return path, cleanup, nil
}

func wsSubscriberID(client *wsClient, sessionID string) string {
	n := wsSubscriberCounter.Add(1)
	return fmt.Sprintf("%p:%s:%d", client, sessionID, n)
}

type attachReplayPayload struct {
	// ghosttySnapshot, when non-nil, is a self-contained VT serialization of the
	// worker's parsed terminal (server-authoritative restore). The client resets
	// its model and replays it. Empty when the policy omits restore or no
	// server-authoritative terminal is available (ghostty absent).
	ghosttySnapshot []byte
	ghosttyCols     uint16
	ghosttyRows     uint16
	// ghosttyBlocks are the worker's OSC 133 command blocks resolved atomically
	// with ghosttySnapshot (Phase 3a). Carried only alongside a snapshot.
	ghosttyBlocks       []pty.AttachBlockData
	scrollbackTruncated bool
	decision            string
}

// shouldIncludeAttachReplay reports whether an attach under this policy should
// restore prior terminal state. A fresh spawn has nothing to restore — the live
// stream paints the first frame and the worker answers startup queries itself —
// so it is the only policy that omits the snapshot.
func shouldIncludeAttachReplay(policy protocol.AttachPolicy) bool {
	return policy != protocol.AttachPolicyFreshSpawn
}

// buildAttachReplayPayload selects the server-authoritative restore for an
// attach: the worker's serialized ghostty terminal. The client resets its model
// and replays this stream — no mid-escape hazard, no oracle verification, no
// replay-vs-snapshot decision tree.
func buildAttachReplayPayload(info ptybackend.AttachInfo, policy protocol.AttachPolicy) attachReplayPayload {
	if !shouldIncludeAttachReplay(policy) {
		return attachReplayPayload{decision: "omit_replay_for_policy"}
	}
	if len(info.GhosttySnapshot) == 0 {
		// No server-authoritative terminal to serialize (ghostty construction
		// failed, or a non-macOS build's pure-Go stub). Nothing to restore; the
		// client keeps whatever it has and dedups the live stream against LastSeq.
		return attachReplayPayload{decision: "no_snapshot"}
	}
	return attachReplayPayload{
		ghosttySnapshot:     info.GhosttySnapshot,
		ghosttyCols:         info.Cols,
		ghosttyRows:         info.Rows,
		ghosttyBlocks:       info.GhosttyBlocks,
		scrollbackTruncated: info.GhosttyScrollbackTruncated,
		decision:            "use_ghostty_snapshot",
	}
}

func (d *Daemon) detachSession(client *wsClient, sessionID string) {
	client.attachMu.Lock()
	stream, hasStream := client.attachedStreams[sessionID]
	if hasStream {
		delete(client.attachedStreams, sessionID)
	}
	if client.pendingRemote != nil {
		delete(client.pendingRemote, sessionID)
	}
	if client.attachedRemote != nil {
		delete(client.attachedRemote, sessionID)
	}
	client.attachMu.Unlock()
	if hasStream {
		_ = stream.Close()
	}
}

func (d *Daemon) detachAllSessions(client *wsClient) {
	client.attachMu.Lock()
	streams := make([]ptybackend.Stream, 0, len(client.attachedStreams))
	for _, stream := range client.attachedStreams {
		streams = append(streams, stream)
	}
	client.attachedStreams = make(map[string]ptybackend.Stream)
	client.pendingRemote = make(map[string]struct{})
	client.attachedRemote = make(map[string]struct{})
	client.attachMu.Unlock()
	for _, stream := range streams {
		_ = stream.Close()
	}
}

func normalizeSpawnAgent(raw string) string {
	agent := strings.TrimSpace(strings.ToLower(raw))
	if agent == protocol.AgentShellValue {
		return protocol.AgentShellValue
	}
	if d := agentdriver.Get(agent); d != nil {
		return d.Name()
	}
	return protocol.NormalizeSpawnAgent(raw, string(protocol.SessionAgentCodex))
}

func legacyExecutableFromSpawnMessage(msg *protocol.SpawnSessionMessage, agent string) string {
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case string(protocol.SessionAgentClaude):
		return strings.TrimSpace(protocol.Deref(msg.ClaudeExecutable))
	case string(protocol.SessionAgentCodex):
		return strings.TrimSpace(protocol.Deref(msg.CodexExecutable))
	case string(protocol.SessionAgentCopilot):
		return strings.TrimSpace(protocol.Deref(msg.CopilotExecutable))
	default:
		return ""
	}
}

func resolveSpawnCWD(cwd string) string {
	trimmed := strings.TrimSpace(cwd)
	switch {
	case trimmed == "~":
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return home
		}
	case strings.HasPrefix(trimmed, "~/"):
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, trimmed[2:])
		}
	}
	return cwd
}

func (d *Daemon) sendSpawnFailure(client *wsClient, sessionID string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if strings.TrimSpace(errMsg) == "" {
		errMsg = "spawn failed"
	}
	d.setWorkspacePaneStatusForSession(sessionID, workspacelayout.PaneStatusFailed, errMsg)
	d.sendToClient(client, protocol.SpawnResultMessage{
		Event:   protocol.EventSpawnResult,
		ID:      sessionID,
		Success: false,
		Error:   protocol.Ptr(errMsg),
	})
}

func buildSpawnSessionRecord(msg *protocol.SpawnSessionMessage, agent, cwd, label string, existing *protocol.Session, isShell, pluginReportsNoState bool) *protocol.Session {
	nowStr := string(protocol.TimestampNow())
	state := protocol.SessionStateLaunching
	if isShell {
		state = protocol.SessionStateIdle
	}
	stateSince, stateUpdatedAt := nowStr, nowStr
	if existing != nil {
		state, stateSince, stateUpdatedAt = existing.State, existing.StateSince, existing.StateUpdatedAt
		if stateSince == "" {
			stateSince = nowStr
		}
		if stateUpdatedAt == "" {
			stateUpdatedAt = nowStr
		}
	}
	if pluginReportsNoState {
		state, stateSince, stateUpdatedAt = protocol.SessionStateWorking, nowStr, nowStr
	}
	session := &protocol.Session{ID: msg.ID, Label: label, Agent: protocol.SessionAgent(agent), Directory: cwd, State: state, StateSince: stateSince, StateUpdatedAt: stateUpdatedAt, LastSeen: nowStr, WorkspaceID: msg.WorkspaceID}
	if branchInfo, _ := git.GetBranchInfo(cwd); branchInfo != nil {
		if branchInfo.Branch != "" {
			session.Branch = protocol.Ptr(branchInfo.Branch)
		}
		if branchInfo.IsWorktree {
			session.IsWorktree = protocol.Ptr(true)
		}
		if branchInfo.MainRepo != "" {
			session.MainRepo = protocol.Ptr(branchInfo.MainRepo)
		}
	}
	return session
}

type internalSpawnPolicy struct {
	unattendedLaunch launchcontract.UnattendedLaunchSpec
}

func (d *Daemon) handleSpawnSession(client *wsClient, msg *protocol.SpawnSessionMessage) {
	d.handleSpawnSessionWithPolicy(client, msg, internalSpawnPolicy{})
}

// handleSpawnSessionWithPolicy is reserved for daemon-owned launch paths. The
// public workspace protocol must not be able to grant automatic approval or
// working-directory trust independently of the user's daemon settings.
func (d *Daemon) handleSpawnSessionWithPolicy(client *wsClient, msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) {
	requestedAgent := strings.TrimSpace(strings.ToLower(msg.Agent))
	pluginDriver, hasPluginDriver := d.ensurePluginRegistry().driver(requestedAgent)
	agent := normalizeSpawnAgent(msg.Agent)
	if hasPluginDriver {
		agent = pluginDriver.Agent
	} else if requestedAgent != "" && requestedAgent != protocol.AgentShellValue && agentdriver.Get(requestedAgent) == nil {
		d.sendSpawnFailure(client, msg.ID, fmt.Errorf("agent %q is not available", requestedAgent))
		return
	}
	isShell := agent == protocol.AgentShellValue
	initialPrompt := protocol.Deref(msg.InitialPrompt)
	if isShell && strings.TrimSpace(initialPrompt) != "" {
		d.sendSpawnFailure(client, msg.ID, errors.New("shell sessions do not accept an initial prompt"))
		return
	}
	if strings.TrimSpace(initialPrompt) != "" {
		if hasPluginDriver && !pluginDriver.Capabilities["initial_prompt"] {
			d.sendSpawnFailure(client, msg.ID, fmt.Errorf("agent %q does not support initial prompts", requestedAgent))
			return
		}
		if !hasPluginDriver {
			driver := agentdriver.Get(agent)
			if driver == nil || !agentdriver.EffectiveCapabilities(driver).HasInitialPrompt {
				d.sendSpawnFailure(client, msg.ID, fmt.Errorf("agent %q does not support initial prompts", agent))
				return
			}
		}
	}
	workspaceID := strings.TrimSpace(msg.WorkspaceID)
	if workspaceID == "" {
		d.sendCommandError(client, protocol.CmdSpawnSession, "missing workspace_id")
		return
	}
	if d.store.GetWorkspace(workspaceID) == nil {
		d.setWorkspacePaneStatusForSession(msg.ID, workspacelayout.PaneStatusFailed, "unknown workspace")
		d.sendCommandError(client, protocol.CmdSpawnSession, "unknown workspace")
		return
	}
	spawnStartedAt := time.Now()
	existingSession := d.store.Get(msg.ID)
	cwd := resolveSpawnCWD(msg.Cwd)
	label := protocol.Deref(msg.Label)
	if label == "" {
		label = filepath.Base(cwd)
	}
	// A non-empty stored label is the durable authority — a respawn or reload
	// must not revert a user rename, even if the client sends a stale label.
	if existingSession != nil && strings.TrimSpace(existingSession.Label) != "" {
		label = existingSession.Label
	}
	if msg.Cols <= 0 || msg.Rows <= 0 || msg.Cols > maxPTYDimValue || msg.Rows > maxPTYDimValue {
		d.sendSpawnFailure(client, msg.ID, fmt.Errorf("invalid terminal size cols=%d rows=%d (expected 1..%d)", msg.Cols, msg.Rows, maxPTYDimValue))
		return
	}
	resumeSessionID := protocol.Deref(msg.ResumeSessionID)
	driver := agentdriver.Get(agent)
	if existingSession != nil && !hasPluginDriver {
		resumeSessionID = agentdriver.ResolveSpawnResumeSessionID(
			driver,
			existingSession.ID,
			resumeSessionID,
			d.store.GetResumeSessionID(msg.ID),
		)
	} else if !hasPluginDriver && resumeSessionID == "" && protocol.Deref(msg.ResumePicker) {
		// Ticket "Resume": the bound session's row (and its resume_session_id) was
		// deleted on close, so the session-keyed lookup above is skipped. The ticket
		// persisted the resume key under the same id (its assignee), so resolve it
		// here to resume the prior conversation directly instead of dropping the
		// user into the agent's resume picker. Falls back to the picker (resumeSessionID
		// stays "") when no ticket resume key exists.
		if ticketResumeID := d.store.GetTicketResumeSessionID(msg.ID); ticketResumeID != "" {
			// Only adopt the mirrored id when it is actually resumable. Claude writes
			// its transcript lazily, so a session closed before it ever took a turn has
			// a mirrored id pointing at a transcript that does not exist; `claude -r
			// <dead-id>` would exit non-zero. Leaving resumeSessionID empty falls the
			// ResumePicker back to the cwd-scoped picker instead. Mirrors the
			// fresh-spawn downgrade in buildReloadSpawnOptions (reload.go).
			if agentdriver.ResumeAvailable(driver, ticketResumeID) {
				resumeSessionID = ticketResumeID
			} else {
				d.logf("spawn: ticket resume target %s for session %s is not resumable (no transcript yet); using resume picker", ticketResumeID, msg.ID)
			}
		}
	}

	configuredExecutable := strings.TrimSpace(protocol.Deref(msg.Executable))
	if configuredExecutable == "" {
		configuredExecutable = legacyExecutableFromSpawnMessage(msg, agent)
	}
	initialPromptFile := ""
	cleanupInitialPrompt := func() {}
	cleanupInitialPromptOnReturn := false
	if !hasPluginDriver {
		var promptErr error
		initialPromptFile, cleanupInitialPrompt, promptErr = d.writeInitialPromptFile(msg.ID, initialPrompt)
		if promptErr != nil {
			d.sendSpawnFailure(client, msg.ID, promptErr)
			return
		}
		cleanupInitialPromptOnReturn = initialPromptFile != ""
		defer func() {
			if cleanupInitialPromptOnReturn {
				cleanupInitialPrompt()
			}
		}()
	}
	spawnOpts := ptybackend.SpawnOptions{
		ID:                msg.ID,
		CWD:               cwd,
		Agent:             agent,
		Label:             label,
		Cols:              uint16(msg.Cols),
		Rows:              uint16(msg.Rows),
		ResumeSessionID:   resumeSessionID,
		ResumePicker:      protocol.Deref(msg.ResumePicker),
		YoloMode:          protocol.Deref(msg.YoloMode),
		InitialPromptFile: initialPromptFile,
		Theme:             d.currentTerminalTheme(),
		Executable:        strings.TrimSpace(configuredExecutable),
		ClaudeExecutable:  protocol.Deref(msg.ClaudeExecutable),
		CodexExecutable:   protocol.Deref(msg.CodexExecutable),
		CopilotExecutable: protocol.Deref(msg.CopilotExecutable),
		LoginShellEnv:     d.cachedLoginShellEnv(),

		WorkflowGuidanceEnabled: parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)),
		AutoApprove:             parseBooleanSetting(d.store.GetSetting(SettingAutoApproveEnabled)),
		Model:                   strings.TrimSpace(protocol.Deref(msg.Model)),
		Effort:                  strings.TrimSpace(protocol.Deref(msg.Effort)),
	}
	// The frontend sets chief_of_staff only on initial creation, not on
	// reconnect/resume spawns after a daemon restart. Fall back to the
	// persisted profile-roles table so chief settings survive respawns.
	requestedChief := protocol.Deref(msg.ChiefOfStaff)
	if hasPluginDriver && requestedChief && !pluginDriver.Capabilities["launch_instructions"] {
		d.sendSpawnFailure(client, msg.ID, fmt.Errorf("agent %q cannot be chief of staff without launch_instructions capability", agent))
		return
	}
	if hasPluginDriver && requestedChief && !pluginDriver.Capabilities["resume"] {
		d.sendSpawnFailure(client, msg.ID, fmt.Errorf("agent %q cannot be chief of staff without resume capability", agent))
		return
	}
	chiefAssigned := d.maybeAssignChiefOnSpawn(msg.ID, agent, requestedChief, existingSession)
	chiefAssignmentCommitted := false
	defer func() {
		if chiefAssigned && !chiefAssignmentCommitted {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
	}()
	isChief := d.isChiefOfStaffSession(msg.ID)
	if spawnOpts.Model == "" {
		// No per-spawn pin (delegation); a chief launch falls back to the
		// chief_model_<agent> setting.
		spawnOpts.Model = d.chiefLaunchModel(agent, isChief)
	}
	if spawnOpts.Effort == "" {
		// No per-spawn pin (delegation); a chief launch falls back to the
		// chief_effort_<agent> setting.
		spawnOpts.Effort = d.chiefLaunchEffort(agent, isChief)
	}
	if launch := policy.unattendedLaunch; !launch.IsZero() {
		if err := launch.Validate(); err != nil {
			d.sendSpawnFailure(client, msg.ID, err)
			return
		}
		if !strings.EqualFold(agent, launch.Agent) {
			d.sendSpawnFailure(client, msg.ID, fmt.Errorf("unattended launch agent %q does not match spawn agent %q", launch.Agent, agent))
			return
		}
		if strings.TrimSpace(protocol.Deref(msg.Model)) != strings.TrimSpace(launch.Model) ||
			strings.TrimSpace(protocol.Deref(msg.Effort)) != strings.TrimSpace(launch.Effort) ||
			strings.TrimSpace(configuredExecutable) != strings.TrimSpace(launch.Executable) {
			d.sendSpawnFailure(client, msg.ID, errors.New("spawn message disagrees with unattended launch contract"))
			return
		}
		spawnOpts.AutoApprove = false
		spawnOpts.TrustWorkingDirectory = false
		spawnOpts.Model = ""
		spawnOpts.Effort = ""
		spawnOpts.Executable = ""
		spawnOpts.UnattendedLaunch = launch
	}
	// A chief launch caps its context window (chief_context_window_cap); non-chief
	// launches stay uncapped so delegated interactive agents are never affected.
	spawnOpts.ChiefContextWindowCap = d.chiefContextWindowCap(isChief)
	if existingSession != nil {
		for _, liveID := range d.ptyBackend.SessionIDs(context.Background()) {
			if liveID != msg.ID {
				continue
			}
			d.sendToClient(client, protocol.SpawnResultMessage{
				Event:   protocol.EventSpawnResult,
				ID:      msg.ID,
				Success: true,
			})
			return
		}
	}
	pluginRunID := ""
	instructionsRollback := func() {}
	instructionsCommitted := false
	defer func() {
		if !instructionsCommitted {
			instructionsRollback()
		}
	}()
	if hasPluginDriver {
		pluginRunID = uuid.NewString()
		spawnOpts.LifecycleID = pluginRunID
		d.beginPluginSessionLaunch(msg.ID, pluginDriver.PluginName, pluginRunID)
		params := pluginDriverSpawnParams{
			SessionID:     msg.ID,
			RunID:         pluginRunID,
			CWD:           cwd,
			Label:         label,
			Yolo:          protocol.Deref(msg.YoloMode),
			Model:         spawnOpts.Model,
			Effort:        spawnOpts.Effort,
			InitialPrompt: initialPrompt,
		}
		if metadata := strings.TrimSpace(d.store.GetAgentMetadata(msg.ID)); metadata != "" && json.Valid([]byte(metadata)) {
			params.Metadata = json.RawMessage(metadata)
		}
		if pluginDriver.Capabilities["launch_instructions"] {
			var instructionErr error
			params.Instructions, instructionsRollback, instructionErr = d.preparePluginLaunchInstructions(msg.ID, workspaceID, isChief)
			if instructionErr != nil {
				d.finishPluginSessionLaunch(msg.ID, false)
				d.sendSpawnFailure(client, msg.ID, instructionErr)
				return
			}
		}
		result, err := d.resolvePluginDriverLaunch(pluginDriver, params, existingSession != nil && pluginDriver.Capabilities["resume"])
		if err != nil {
			d.finishPluginSessionLaunch(msg.ID, false)
			d.sendSpawnFailure(client, msg.ID, err)
			return
		}
		commandEnv, err := pluginCommandEnv(result.Env)
		if err != nil {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
			d.sendSpawnFailure(client, msg.ID, err)
			return
		}
		spawnOpts.ExternalCommand = append([]string(nil), result.Argv...)
		spawnOpts.ExternalEnv = commandEnv
		spawnOpts.ExternalCWD = strings.TrimSpace(result.CWD)
	}

	// Persist the complete launch intent before creating the worker. If the daemon
	// dies after Spawn, startup recovery can now associate that worker with its
	// workspace, pane, ticket, and automation run instead of seeing an anonymous
	// process with no durable session row.
	launchSession := buildSpawnSessionRecord(msg, agent, cwd, label, existingSession, isShell, hasPluginDriver && !pluginDriver.Capabilities["state_reporting"])
	if err := d.store.AddChecked(launchSession); err != nil {
		if hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		d.sendSpawnFailure(client, msg.ID, fmt.Errorf("persist session launch intent: %w", err))
		return
	}

	if err := d.ptyBackend.Spawn(context.Background(), spawnOpts); err != nil {
		if existingSession == nil {
			d.store.Remove(msg.ID)
		} else if restoreErr := d.store.AddChecked(existingSession); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore prior session after spawn failure: %w", restoreErr))
		}
		if hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		d.sendSpawnFailure(client, msg.ID, err)
		return
	}
	if initialPromptFile != "" {
		// The spawned wrapper removes the file after reading it. Keep a fallback
		// for failures between PTY spawn and wrapper startup.
		cleanupInitialPromptOnReturn = false
		time.AfterFunc(5*time.Minute, cleanupInitialPrompt)
	}

	{
		d.clearLongRunTracking(msg.ID)
		session := launchSession
		if err := d.store.AddChecked(session); err != nil {
			if hasPluginDriver {
				d.abortPluginSessionLaunch(msg.ID, "launch_failed")
			}
			if chiefAssigned {
				d.clearChiefOfStaffIfSession(msg.ID)
			}
			killErr := d.ptyBackend.Kill(context.Background(), msg.ID, syscall.SIGTERM)
			removeErr := d.ptyBackend.Remove(context.Background(), msg.ID)
			persistErr := fmt.Errorf("persist spawned session: %w", err)
			if killErr != nil {
				persistErr = fmt.Errorf("%w; kill spawned runtime: %v", persistErr, killErr)
			}
			if removeErr != nil {
				persistErr = fmt.Errorf("%w; remove spawned runtime: %v", persistErr, removeErr)
			}
			d.sendSpawnFailure(client, msg.ID, persistErr)
			return
		}
		if hasPluginDriver && !d.store.BeginAgentDriverRun(session.ID, pluginDriver.PluginName, pluginRunID) {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
			if chiefAssigned {
				d.clearChiefOfStaffIfSession(msg.ID)
			}
			killErr := d.ptyBackend.Kill(context.Background(), msg.ID, syscall.SIGTERM)
			removeErr := d.ptyBackend.Remove(context.Background(), msg.ID)
			if existingSession == nil {
				d.store.Remove(session.ID)
			}
			cursorErr := fmt.Errorf("initialize plugin driver run cursor")
			if killErr != nil {
				cursorErr = fmt.Errorf("%w; kill spawned runtime: %v", cursorErr, killErr)
			}
			if removeErr != nil {
				cursorErr = fmt.Errorf("%w; remove spawned runtime: %v", cursorErr, removeErr)
			}
			d.sendSpawnFailure(client, msg.ID, cursorErr)
			return
		}
		if persistResumeID := agentdriver.SpawnResumeSessionID(
			driver,
			session.ID,
			resumeSessionID,
			protocol.Deref(msg.ResumePicker),
		); persistResumeID != "" {
			d.persistResumeSessionID(session.ID, persistResumeID)
		}
		if pendingResumeID := d.consumePendingResumeSessionID(session.ID); pendingResumeID != "" {
			d.persistResumeSessionID(session.ID, pendingResumeID)
		}
		// Re-arm orphaned-ticket reconciliation: the owning session is alive again
		// (a ticket Resume respawns under the same id), so a future death deserves
		// a fresh verdict. No-op when nothing is flagged.
		if err := d.store.ClearTicketReconciliationForAssignee(session.ID); err != nil {
			d.logf("clear ticket reconciliation on spawn for %s: %v", session.ID, err)
		}
		// A crash-stamped ticket whose owner just respawned (dead-pane reload,
		// ticket Resume) is no longer crashed: move it back to Working and put it
		// back on the crash seam's radar (ticket_revive.go).
		d.reviveCrashedTicketsForSession(session.ID)
		if !isShell {
			d.startTranscriptWatcher(session.ID, session.Agent, session.Directory, spawnStartedAt)
		}
		d.store.UpsertRecentLocation(cwd)
		d.associateSessionWithWorkspace(session.ID, workspaceID)
		// Single mechanism: spawn_session guarantees the session has a layout
		// pane. Clients that pre-created one (app, delegate, ticket resume) hit
		// the adopt path; bare spawns (wsctl, scripts) get default placement.
		// A pane failure must not fail the spawn — the session is already live.
		if workspaceID != "" {
			if _, err := d.ensureWorkspaceSessionPane(workspaceID, session.ID, session.Label); err != nil {
				d.logf("ensure workspace pane for session %s: %v", session.ID, err)
			}
		}
		d.setWorkspacePaneStatusForSession(session.ID, workspacelayout.PaneStatusReady, "")
		eventType := protocol.EventSessionRegistered
		if existingSession != nil {
			eventType = protocol.EventSessionStateChanged
		}
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   eventType,
			Session: d.sessionForBroadcast(session),
		})
		d.recomputeAndBroadcastWorkspaceForSession(session.ID)
	}
	if hasPluginDriver {
		if exit := d.finishPluginSessionLaunch(msg.ID, true); exit != nil {
			d.handlePTYExit(*exit)
		}
	}
	chiefAssignmentCommitted = true
	instructionsCommitted = true

	d.sendToClient(client, protocol.SpawnResultMessage{
		Event:   protocol.EventSpawnResult,
		ID:      msg.ID,
		Success: true,
	})
}

func (d *Daemon) handleAttachSession(client *wsClient, msg *protocol.AttachSessionMessage) {
	subID := wsSubscriberID(client, msg.ID)

	info, stream, err := d.ptyBackend.Attach(context.Background(), msg.ID, subID)
	if err != nil {
		d.sendToClient(client, protocol.AttachResultMessage{
			Event:   protocol.EventAttachResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(err.Error()),
		})
		return
	}
	replay := buildAttachReplayPayload(info, protocol.Deref(msg.AttachPolicy))
	d.logf(
		"PTY attach result: id=%s policy=%s running=%v last_seq=%d ghostty_snapshot_bytes=%d scrollback_truncated=%v replay_decision=%s size=%dx%d",
		msg.ID,
		protocol.Deref(msg.AttachPolicy),
		info.Running,
		info.LastSeq,
		len(replay.ghosttySnapshot),
		replay.scrollbackTruncated,
		replay.decision,
		info.Cols,
		info.Rows,
	)

	client.attachMu.Lock()
	previous := client.attachedStreams[msg.ID]
	client.attachedStreams[msg.ID] = stream
	client.attachMu.Unlock()
	if previous != nil && previous != stream {
		_ = previous.Close()
	}
	go d.forwardPTYStreamEvents(client, msg.ID, stream)

	result := protocol.AttachResultMessage{
		Event:   protocol.EventAttachResult,
		ID:      msg.ID,
		Success: true,
		LastSeq: protocol.Ptr(int(info.LastSeq)),
		Cols:    protocol.Ptr(int(info.Cols)),
		Rows:    protocol.Ptr(int(info.Rows)),
		Pid:     protocol.Ptr(info.PID),
		Running: protocol.Ptr(info.Running),
	}
	if len(replay.ghosttySnapshot) > 0 {
		result.Snapshot = &protocol.AttachSnapshot{
			Cols:                int(replay.ghosttyCols),
			Rows:                int(replay.ghosttyRows),
			VtDumpB64:           base64.StdEncoding.EncodeToString(replay.ghosttySnapshot),
			Blocks:              attachBlocksToProtocol(replay.ghosttyBlocks),
			ScrollbackTruncated: replay.scrollbackTruncated,
		}
	}
	d.sendToClient(client, result)
}

// attachBlocksToProtocol converts the worker's resolved command blocks to their
// wire form. nil in → nil out (the field is omitted when there are no blocks).
func attachBlocksToProtocol(blocks []pty.AttachBlockData) []protocol.AttachBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]protocol.AttachBlock, len(blocks))
	for i, b := range blocks {
		out[i] = protocol.AttachBlock{
			ID:             int(b.ID),
			Pending:        b.Pending,
			PromptRow:      int(b.PromptRow),
			InputRow:       int32PtrToInt(b.InputRow),
			InputCol:       int32PtrToInt(b.InputCol),
			OutputStartRow: int32PtrToInt(b.OutputStartRow),
			EndRow:         int32PtrToInt(b.EndRow),
			Command:        b.Command,
			ExitCode:       int32PtrToInt(b.ExitCode),
		}
	}
	return out
}

func int32PtrToInt(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

// snapshotSeedScreen resolves the visible frame from the worker's fresh
// Ghostty snapshot (Manager.Snapshot) to seed an observer. The second result is
// ok.
func snapshotSeedScreen(info ptybackend.AttachInfo) (pty.ReplayScreenSnapshot, bool) {
	if info.ScreenSnapshotFresh && len(info.ScreenSnapshot) > 0 {
		return pty.ReplayScreenSnapshot{
			Payload:       info.ScreenSnapshot,
			Cols:          info.ScreenCols,
			Rows:          info.ScreenRows,
			CursorX:       info.ScreenCursorX,
			CursorY:       info.ScreenCursorY,
			CursorVisible: info.ScreenCursorVisible,
		}, true
	}
	return pty.ReplayScreenSnapshot{}, false
}

// handleGetScreenSnapshot serves a read-only snapshot of a session's current
// screen. It registers no subscriber and starts no stream — purely a seed for
// observers (grid tiles) that then dedup the live firehose against last_seq.
func (d *Daemon) handleGetScreenSnapshot(client *wsClient, msg *protocol.GetScreenSnapshotMessage) {
	provider, ok := d.ptyBackend.(ptybackend.SnapshotProvider)
	if !ok {
		d.sendToClient(client, protocol.GetScreenSnapshotResultMessage{
			Event:   protocol.EventGetScreenSnapshotResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr("screen snapshot not supported"),
		})
		return
	}

	info, err := provider.Snapshot(context.Background(), msg.ID)
	if err != nil {
		// Graceful: a worker built before MethodSnapshot answers "unknown
		// method"; the observer stays unseeded rather than erroring loudly.
		d.sendToClient(client, protocol.GetScreenSnapshotResultMessage{
			Event:   protocol.EventGetScreenSnapshotResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(err.Error()),
		})
		return
	}

	result := protocol.GetScreenSnapshotResultMessage{
		Event:   protocol.EventGetScreenSnapshotResult,
		ID:      msg.ID,
		Success: true,
		LastSeq: protocol.Ptr(int(info.LastSeq)),
		Cols:    protocol.Ptr(int(info.Cols)),
		Rows:    protocol.Ptr(int(info.Rows)),
		Running: protocol.Ptr(info.Running),
	}
	screen, haveScreen := snapshotSeedScreen(info)
	if haveScreen {
		result.ScreenSnapshot = protocol.Ptr(base64.StdEncoding.EncodeToString(screen.Payload))
		result.ScreenRows = protocol.Ptr(int(screen.Rows))
		result.ScreenCols = protocol.Ptr(int(screen.Cols))
		result.ScreenCursorX = protocol.Ptr(int(screen.CursorX))
		result.ScreenCursorY = protocol.Ptr(int(screen.CursorY))
		result.ScreenCursorVisible = protocol.Ptr(screen.CursorVisible)
		result.ScreenSnapshotFresh = protocol.Ptr(true)
	}
	d.logf(
		"PTY screen snapshot: id=%s running=%v last_seq=%d snapshot_bytes=%d screen=%dx%d have_screen=%v",
		msg.ID, info.Running, info.LastSeq, len(screen.Payload),
		screen.Cols, screen.Rows, haveScreen,
	)
	d.sendToClient(client, result)
}

func (d *Daemon) handleDetachSessionWS(client *wsClient, msg *protocol.DetachSessionMessage) {
	d.detachSession(client, msg.ID)
}

// encodePtyOutputMessage builds the outbound frame for one PTY output chunk.
// Clients that advertised CapabilityBinaryPtyOutput get a compact binary
// frame; everyone else (daemon-to-daemon relays, automation clients) keeps the
// base64-in-JSON pty_output event. The capability is re-read per chunk so a
// re-sent client_hello takes effect immediately.
func encodePtyOutputMessage(client *wsClient, sessionID string, event ptybackend.OutputEvent) (outboundMessage, error) {
	if client.HasCapability(protocol.CapabilityBinaryPtyOutput) {
		frame, err := protocol.EncodePtyOutputFrame(sessionID, event.Seq, event.Data)
		if err != nil {
			return outboundMessage{}, err
		}
		return outboundMessage{kind: messageKindBinary, payload: frame}, nil
	}
	encoded := base64.StdEncoding.EncodeToString(event.Data)
	wsEvent := &protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr(sessionID),
		Data:  protocol.Ptr(encoded),
		Seq:   protocol.Ptr(int(event.Seq)),
	}
	payload, err := json.Marshal(wsEvent)
	if err != nil {
		return outboundMessage{}, err
	}
	return outboundMessage{kind: messageKindText, payload: payload}, nil
}

func (d *Daemon) forwardPTYStreamEvents(client *wsClient, sessionID string, stream ptybackend.Stream) {
	d.logf("pty stream forward start: id=%s", sessionID)
	defer func() {
		client.attachMu.Lock()
		current, ok := client.attachedStreams[sessionID]
		if ok && current == stream {
			delete(client.attachedStreams, sessionID)
		}
		client.attachMu.Unlock()
		d.logf("pty stream forward stop: id=%s", sessionID)
	}()

	for event := range stream.Events() {
		switch event.Kind {
		case ptybackend.OutputEventKindOutput:
			// Hot path: one event per output chunk per attached client. Gate the
			// verbose log (and its preview allocation) on debug so DEBUG-off runs
			// don't take the global log mutex + a synchronous disk write per chunk.
			if d.debugLogging {
				d.logf(
					"pty_output forward: id=%s seq=%d bytes=%d preview=%q",
					sessionID,
					event.Seq,
					len(event.Data),
					previewBinaryForLog(event.Data),
				)
			}
			outbound, err := encodePtyOutputMessage(client, sessionID, event)
			if err != nil {
				d.logf("pty_output marshal failed: id=%s seq=%d err=%v", sessionID, event.Seq, err)
				continue
			}
			if !d.sendOutboundBlocking(client, outbound, ptyOutputSendWait) {
				d.logf("pty_output send failed, closing stream: id=%s seq=%d", sessionID, event.Seq)
				_ = stream.Close()
				return
			}
		case ptybackend.OutputEventKindDesync:
			if d.debugLogging {
				d.logf("pty_desync forward: id=%s reason=%s", sessionID, event.Reason)
			}
			wsEvent := &protocol.WebSocketEvent{
				Event:  protocol.EventPtyDesync,
				ID:     protocol.Ptr(sessionID),
				Reason: protocol.Ptr(event.Reason),
			}
			payload, err := json.Marshal(wsEvent)
			if err != nil {
				continue
			}
			if !d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: payload}) {
				_ = stream.Close()
				return
			}
		}
	}

	d.logf("pty stream events closed: id=%s", sessionID)
}

func (d *Daemon) handlePtyInput(client *wsClient, msg *protocol.PtyInputMessage) {
	source := strings.TrimSpace(protocol.Deref(msg.Source))
	// Record genuine user keystrokes for the nudge splice guard. A doorbell that fires
	// within userInputGuardWindow of a keystroke would splice onto the half-typed line.
	d.noteUserInput(msg.ID, source)
	if d.debugLogging {
		d.logf(
			"pty_input: id=%s bytes=%d preview=%q source=%s",
			msg.ID,
			len(msg.Data),
			previewBinaryForLog([]byte(msg.Data)),
			strings.TrimSpace(protocol.Deref(msg.Source)),
		)
	}
	if err := d.ptyBackend.Input(context.Background(), msg.ID, []byte(msg.Data)); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("pty_input failed for %s: %v", msg.ID, err)
		}
	} else if d.debugLogging {
		d.logf("pty_input ok: id=%s bytes=%d", msg.ID, len(msg.Data))
	}
}

func (d *Daemon) handlePtyResize(client *wsClient, msg *protocol.PtyResizeMessage) {
	if msg.Cols <= 0 || msg.Rows <= 0 || msg.Cols > maxPTYDimValue || msg.Rows > maxPTYDimValue {
		d.sendCommandError(client, protocol.CmdPtyResize, fmt.Sprintf("invalid terminal size cols=%d rows=%d (expected 1..%d)", msg.Cols, msg.Rows, maxPTYDimValue))
		return
	}
	d.logf("pty_resize: id=%s cols=%d rows=%d", msg.ID, msg.Cols, msg.Rows)
	if err := d.ptyBackend.Resize(context.Background(), msg.ID, uint16(msg.Cols), uint16(msg.Rows)); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("pty_resize failed for %s: %v", msg.ID, err)
		}
		return
	}
	// Broadcast the new geometry to all other attached clients so they can
	// keep their local terminal models in sync.
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPtyResized,
		ID:    protocol.Ptr(msg.ID),
		Cols:  protocol.Ptr(msg.Cols),
		Rows:  protocol.Ptr(msg.Rows),
	})
}

var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// sanitizeThemeColor blanks a field that isn't a valid "#rrggbb" hex color; an
// empty field makes pty fall back to its built-in default for that channel.
func sanitizeThemeColor(value string) string {
	if hexColorPattern.MatchString(value) {
		return value
	}
	return ""
}

// handleSetTerminalTheme stores the daemon-global terminal theme and fans it
// out best-effort to every live session so already-running agents answer OSC
// 10/11/12 color queries with the new colors immediately. Fire-and-forget, no
// result event — mirrors pty_resize.
func (d *Daemon) handleSetTerminalTheme(client *wsClient, msg *protocol.SetTerminalThemeMessage) {
	theme := pty.TerminalTheme{
		Foreground: sanitizeThemeColor(msg.Foreground),
		Background: sanitizeThemeColor(msg.Background),
		Cursor:     sanitizeThemeColor(msg.Cursor),
	}
	if theme.Foreground != msg.Foreground || theme.Background != msg.Background || theme.Cursor != msg.Cursor {
		d.logf("set_terminal_theme: invalid color field(s) blanked, got fg=%q bg=%q cursor=%q", msg.Foreground, msg.Background, msg.Cursor)
	}
	d.setCurrentTerminalTheme(theme)

	ctx := context.Background()
	for _, sessionID := range d.ptyBackend.SessionIDs(ctx) {
		if err := d.ptyBackend.SetTheme(ctx, sessionID, theme); err != nil {
			d.logf("set_terminal_theme: SetTheme failed for %s: %v", sessionID, err)
		}
	}
}

func parseSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "SIGTERM", "TERM":
		return syscall.SIGTERM
	case "SIGINT", "INT":
		return syscall.SIGINT
	case "SIGHUP", "HUP":
		return syscall.SIGHUP
	case "SIGKILL", "KILL":
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}

func (d *Daemon) handleKillSession(client *wsClient, msg *protocol.KillSessionMessage) {
	d.detachSession(client, msg.ID)
	sig := parseSignal(protocol.Deref(msg.Signal))
	if protocol.Deref(msg.Reload) {
		// This kill is the first half of a client reload (same id respawns right
		// after). Mark before Kill so the exit event — which can fire the instant
		// Kill returns — reads as a lifecycle transition, not a crash.
		d.markReloadKill(msg.ID)
	}
	err := d.ptyBackend.Kill(context.Background(), msg.ID, sig)
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) {
		// Production backends return from Kill only once the child has exited.
		// Close here because worker lifecycle delivery can trail that return.
		d.closePluginDriverSession(msg.ID, "killed", nil, signalName(sig))
	}
	if err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("kill_session failed for %s: %v", msg.ID, err)
		}
	}
}

func shouldLogPtyCommandError(err error) bool {
	return !errors.Is(err, pty.ErrSessionNotFound)
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGKILL:
		return "SIGKILL"
	default:
		return "SIGTERM"
	}
}
