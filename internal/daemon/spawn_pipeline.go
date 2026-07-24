package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type internalSpawnPolicy struct {
	unattendedLaunch launchcontract.UnattendedLaunchSpec
}

type spawnRequest struct {
	msg             *protocol.SpawnSessionMessage
	policy          internalSpawnPolicy
	agent           string
	pluginDriver    pluginDriverRegistration
	hasPluginDriver bool
	isShell         bool
	initialPrompt   string
	workspaceID     string
	existingSession *protocol.Session
	cwd             string
	label           string
	spawnStartedAt  time.Time
	driver          agentdriver.Driver
	resumeSessionID string
}

type spawnPlan struct {
	spawnOpts                    ptybackend.SpawnOptions
	launchSession                *protocol.Session
	pluginRunID                  string
	cleanupInitialPrompt         func()
	cleanupInitialPromptOnReturn bool
	chiefAssigned                bool
	isChief                      bool
	chiefAssignmentCommitted     bool
	instructionsRollback         func()
	instructionsCommitted        bool
	priorIntent                  store.LaunchIntent
	hadPriorIntent               bool
}

type spawnRejection struct {
	commandError string
	err          error
}

type spawnOutcome struct {
	alreadyLive bool
	err         error
}

func (plan *spawnPlan) rollback(d *Daemon, sessionID string) {
	if plan.cleanupInitialPromptOnReturn {
		plan.cleanupInitialPrompt()
	}
	if plan.chiefAssigned && !plan.chiefAssignmentCommitted {
		d.clearChiefOfStaffIfSession(sessionID)
	}
	if !plan.instructionsCommitted {
		plan.instructionsRollback()
	}
}

func (plan *spawnPlan) commit() {
	plan.chiefAssignmentCommitted = true
	plan.instructionsCommitted = true
}

func (d *Daemon) validateSpawnPrelock(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (*spawnRequest, *spawnRejection) {
	requestedAgent := strings.TrimSpace(strings.ToLower(msg.Agent))
	pluginDriver, hasPluginDriver := d.ensurePluginRegistry().driver(requestedAgent)
	agent := normalizeSpawnAgent(msg.Agent)
	if hasPluginDriver {
		agent = pluginDriver.Agent
	} else if requestedAgent != "" && requestedAgent != protocol.AgentShellValue && agentdriver.Get(requestedAgent) == nil {
		return nil, &spawnRejection{err: fmt.Errorf("agent %q is not available", requestedAgent)}
	}
	isShell := agent == protocol.AgentShellValue
	initialPrompt := protocol.Deref(msg.InitialPrompt)
	if isShell && strings.TrimSpace(initialPrompt) != "" {
		return nil, &spawnRejection{err: errors.New("shell sessions do not accept an initial prompt")}
	}
	if strings.TrimSpace(initialPrompt) != "" {
		if hasPluginDriver && !pluginDriver.Capabilities["initial_prompt"] {
			return nil, &spawnRejection{err: fmt.Errorf("agent %q does not support initial prompts", requestedAgent)}
		}
		if !hasPluginDriver {
			driver := agentdriver.Get(agent)
			if driver == nil || !agentdriver.EffectiveCapabilities(driver).HasInitialPrompt {
				return nil, &spawnRejection{err: fmt.Errorf("agent %q does not support initial prompts", agent)}
			}
		}
	}
	workspaceID := strings.TrimSpace(msg.WorkspaceID)
	if workspaceID == "" {
		return nil, &spawnRejection{commandError: "missing workspace_id"}
	}
	if d.store.GetWorkspace(workspaceID) == nil {
		d.setWorkspacePaneStatusForSession(msg.ID, workspacelayout.PaneStatusFailed, "unknown workspace")
		return nil, &spawnRejection{commandError: "unknown workspace"}
	}
	return &spawnRequest{msg: msg, policy: policy, agent: agent, pluginDriver: pluginDriver, hasPluginDriver: hasPluginDriver, isShell: isShell, initialPrompt: initialPrompt, workspaceID: workspaceID}, nil
}

func (d *Daemon) normalizeSpawnRequest(req *spawnRequest) *spawnRejection {
	req.spawnStartedAt = time.Now()
	req.existingSession = d.store.Get(req.msg.ID)
	req.cwd = resolveSpawnCWD(req.msg.Cwd)
	req.label = protocol.Deref(req.msg.Label)
	if req.label == "" {
		req.label = filepath.Base(req.cwd)
	}
	// A non-empty stored label is the durable authority — a respawn or reload
	// must not revert a user rename, even if the client sends a stale label.
	if req.existingSession != nil && strings.TrimSpace(req.existingSession.Label) != "" {
		req.label = req.existingSession.Label
	}
	if req.msg.Cols <= 0 || req.msg.Rows <= 0 || req.msg.Cols > maxPTYDimValue || req.msg.Rows > maxPTYDimValue {
		return &spawnRejection{err: fmt.Errorf("invalid terminal size cols=%d rows=%d (expected 1..%d)", req.msg.Cols, req.msg.Rows, maxPTYDimValue)}
	}
	req.resumeSessionID = protocol.Deref(req.msg.ResumeSessionID)
	req.driver = agentdriver.Get(req.agent)
	return nil
}

func (d *Daemon) resolveSpawnIntent(req *spawnRequest) (*spawnPlan, *spawnRejection) {
	msg := req.msg
	if req.existingSession != nil && !req.hasPluginDriver {
		req.resumeSessionID = agentdriver.ResolveSpawnResumeSessionID(req.driver, req.existingSession.ID, req.resumeSessionID, d.store.GetResumeSessionID(msg.ID))
		// Downgrade to a fresh launch when the resume target is the session's own
		// id and no transcript exists for it yet. Claude launches with
		// --session-id <attn id> and writes its transcript lazily on the first
		// turn, so a session that booted but was never prompted resolves to its
		// own id as the resume target with nothing on disk — `claude --resume
		// <id>` then exits non-zero ("No conversation found"). A relaunch (the
		// sidebar Reload button, or the pane-mount auto-revive of a recoverable
		// session) must not spawn that dead agent: dropping the resume id
		// fresh-spawns while reusing --session-id, preserving identity. Scoped to
		// the self-id case so a distinct agent-native resume id (codex's, or a
		// cross-session resume) is still trusted and passed through unchanged.
		// Mirrors the fresh-spawn downgrade in buildReloadSpawnOptions (reload.go).
		if req.resumeSessionID == msg.ID && !agentdriver.ResumeAvailable(req.driver, req.resumeSessionID) {
			d.logf("spawn: self-resume target %s has no transcript yet; fresh-spawning instead", msg.ID)
			req.resumeSessionID = ""
		}
	} else if !req.hasPluginDriver && req.resumeSessionID == "" && protocol.Deref(msg.ResumePicker) {
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
			if agentdriver.ResumeAvailable(req.driver, ticketResumeID) {
				req.resumeSessionID = ticketResumeID
			} else {
				d.logf("spawn: ticket resume target %s for session %s is not resumable (no transcript yet); using resume picker", ticketResumeID, msg.ID)
			}
		}
	}
	configuredExecutable := strings.TrimSpace(protocol.Deref(msg.Executable))
	if configuredExecutable == "" {
		configuredExecutable = legacyExecutableFromSpawnMessage(msg, req.agent)
	}
	plan := &spawnPlan{cleanupInitialPrompt: func() {}, instructionsRollback: func() {}}
	if !req.hasPluginDriver {
		initialPromptFile, cleanup, err := d.writeInitialPromptFile(msg.ID, req.initialPrompt)
		if err != nil {
			return nil, &spawnRejection{err: err}
		}
		plan.cleanupInitialPrompt = cleanup
		plan.cleanupInitialPromptOnReturn = initialPromptFile != ""
		plan.spawnOpts.InitialPromptFile = initialPromptFile
	}
	plan.spawnOpts = ptybackend.SpawnOptions{ID: msg.ID, CWD: req.cwd, Agent: req.agent, Label: req.label, Cols: uint16(msg.Cols), Rows: uint16(msg.Rows), ResumeSessionID: req.resumeSessionID, ResumePicker: protocol.Deref(msg.ResumePicker), YoloMode: protocol.Deref(msg.YoloMode), InitialPromptFile: plan.spawnOpts.InitialPromptFile, Theme: d.currentTerminalTheme(), Executable: strings.TrimSpace(configuredExecutable), ClaudeExecutable: protocol.Deref(msg.ClaudeExecutable), CodexExecutable: protocol.Deref(msg.CodexExecutable), CopilotExecutable: protocol.Deref(msg.CopilotExecutable), LoginShellEnv: d.cachedLoginShellEnv(), WorkflowGuidanceEnabled: parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)), AutoApprove: parseBooleanSetting(d.store.GetSetting(SettingAutoApproveEnabled)), Model: strings.TrimSpace(protocol.Deref(msg.Model)), Effort: strings.TrimSpace(protocol.Deref(msg.Effort))}
	// The frontend sets chief_of_staff only on initial creation, not on
	// reconnect/resume spawns after a daemon restart. Fall back to the
	// persisted profile-roles table so chief settings survive respawns.
	requestedChief := protocol.Deref(msg.ChiefOfStaff)
	if req.hasPluginDriver && requestedChief && !req.pluginDriver.Capabilities["launch_instructions"] {
		plan.rollback(d, msg.ID)
		return nil, &spawnRejection{err: fmt.Errorf("agent %q cannot be chief of staff without launch_instructions capability", req.agent)}
	}
	if req.hasPluginDriver && requestedChief && !req.pluginDriver.Capabilities["resume"] {
		plan.rollback(d, msg.ID)
		return nil, &spawnRejection{err: fmt.Errorf("agent %q cannot be chief of staff without resume capability", req.agent)}
	}
	plan.chiefAssigned = d.maybeAssignChiefOnSpawn(msg.ID, req.agent, requestedChief, req.existingSession)
	plan.isChief = d.isChiefOfStaffSession(msg.ID)
	if plan.spawnOpts.Model == "" {
		// No per-spawn pin (delegation); a chief launch falls back to the
		// chief_model_<agent> setting.
		plan.spawnOpts.Model = d.chiefLaunchModel(req.agent, plan.isChief)
	}
	if plan.spawnOpts.Effort == "" {
		// No per-spawn pin (delegation); a chief launch falls back to the
		// chief_effort_<agent> setting.
		plan.spawnOpts.Effort = d.chiefLaunchEffort(req.agent, plan.isChief)
	}
	if launch := req.policy.unattendedLaunch; !launch.IsZero() {
		if err := launch.Validate(); err != nil {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: err}
		}
		if !strings.EqualFold(req.agent, launch.Agent) {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: fmt.Errorf("unattended launch agent %q does not match spawn agent %q", launch.Agent, req.agent)}
		}
		if strings.TrimSpace(protocol.Deref(msg.Model)) != strings.TrimSpace(launch.Model) || strings.TrimSpace(protocol.Deref(msg.Effort)) != strings.TrimSpace(launch.Effort) || strings.TrimSpace(configuredExecutable) != strings.TrimSpace(launch.Executable) {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: errors.New("spawn message disagrees with unattended launch contract")}
		}
		plan.spawnOpts.AutoApprove, plan.spawnOpts.TrustWorkingDirectory, plan.spawnOpts.Model, plan.spawnOpts.Effort, plan.spawnOpts.Executable, plan.spawnOpts.UnattendedLaunch = false, false, "", "", "", launch
	}
	// A chief launch caps its context window (chief_context_window_cap); non-chief
	// launches stay uncapped so delegated interactive agents are never affected.
	plan.spawnOpts.ChiefContextWindowCap = d.chiefContextWindowCap(plan.isChief)

	return plan, nil
}

func (d *Daemon) executeSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	msg := req.msg
	if req.existingSession != nil {
		for _, liveID := range d.ptyBackend.SessionIDs(context.Background()) {
			if liveID == msg.ID {
				plan.rollback(d, msg.ID)
				return &spawnOutcome{alreadyLive: true}
			}
		}
	}
	if req.hasPluginDriver {
		plan.pluginRunID = uuid.NewString()
		plan.spawnOpts.LifecycleID = plan.pluginRunID
		d.beginPluginSessionLaunch(msg.ID, req.pluginDriver.PluginName, plan.pluginRunID)
		params := pluginDriverSpawnParams{
			SessionID:     msg.ID,
			RunID:         plan.pluginRunID,
			CWD:           req.cwd,
			Label:         req.label,
			Yolo:          protocol.Deref(msg.YoloMode),
			Model:         plan.spawnOpts.Model,
			Effort:        plan.spawnOpts.Effort,
			InitialPrompt: req.initialPrompt,
		}
		if metadata := strings.TrimSpace(d.store.GetAgentMetadata(msg.ID)); metadata != "" && json.Valid([]byte(metadata)) {
			params.Metadata = json.RawMessage(metadata)
		}
		if req.pluginDriver.Capabilities["launch_instructions"] {
			instructions, rollback, err := d.preparePluginLaunchInstructions(msg.ID, req.workspaceID, plan.isChief)
			if err != nil {
				d.finishPluginSessionLaunch(msg.ID, false)
				plan.rollback(d, msg.ID)
				return &spawnOutcome{err: err}
			}
			params.Instructions, plan.instructionsRollback = instructions, rollback
		}
		result, err := d.resolvePluginDriverLaunch(req.pluginDriver, params, req.existingSession != nil && req.pluginDriver.Capabilities["resume"])
		if err != nil {
			d.finishPluginSessionLaunch(msg.ID, false)
			plan.rollback(d, msg.ID)
			return &spawnOutcome{err: err}
		}
		commandEnv, err := pluginCommandEnv(result.Env)
		if err != nil {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
			plan.rollback(d, msg.ID)
			return &spawnOutcome{err: err}
		}
		plan.spawnOpts.ExternalCommand = append([]string(nil), result.Argv...)
		plan.spawnOpts.ExternalEnv = commandEnv
		plan.spawnOpts.ExternalCWD = strings.TrimSpace(result.CWD)
	}

	// Persist the complete launch intent before creating the worker. If the daemon
	// dies after Spawn, startup recovery can now associate that worker with its
	// workspace, pane, ticket, and automation run instead of seeing an anonymous
	// process with no durable session row.
	plan.launchSession = buildSpawnSessionRecord(msg, req.agent, req.cwd, req.label, req.existingSession, req.isShell, req.hasPluginDriver && !req.pluginDriver.Capabilities["state_reporting"])
	session := plan.launchSession
	if err := d.store.AddChecked(session); err != nil {
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: fmt.Errorf("persist session launch intent: %w", err)}
	}
	// The recovery contract must be durable before the worker exists: a daemon
	// death after Spawn but before commit would otherwise leave a recoverable
	// session with no stored launch intent to revive from.
	plan.priorIntent, plan.hadPriorIntent = d.store.LaunchIntent(session.ID)
	d.store.SetLaunchIntent(session.ID, store.LaunchIntent{
		YoloMode:         plan.spawnOpts.YoloMode,
		Executable:       plan.spawnOpts.Executable,
		Model:            plan.spawnOpts.Model,
		Effort:           plan.spawnOpts.Effort,
		ChiefOfStaff:     plan.isChief,
		UnattendedLaunch: plan.spawnOpts.UnattendedLaunch,
	})
	if err := d.ptyBackend.Spawn(context.Background(), plan.spawnOpts); err != nil {
		if req.existingSession == nil {
			d.store.Remove(msg.ID)
		} else if restoreErr := d.store.AddChecked(req.existingSession); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore prior session after spawn failure: %w", restoreErr))
		}
		if req.existingSession != nil && plan.hadPriorIntent {
			d.store.SetLaunchIntent(msg.ID, plan.priorIntent)
		}
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: err}
	}
	if plan.spawnOpts.InitialPromptFile != "" {
		// The spawned wrapper removes the file after reading it. Keep a fallback
		// for failures between PTY spawn and wrapper startup.
		plan.cleanupInitialPromptOnReturn = false
		time.AfterFunc(5*time.Minute, plan.cleanupInitialPrompt)
	}
	return &spawnOutcome{}
}

func (d *Daemon) commitSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	msg, session := req.msg, plan.launchSession
	// A state transition can land between executeSpawn's persist and this commit
	// (the wrapper reports working as soon as the PTY boots). The commit upsert
	// must not rewind it to the pre-spawn snapshot.
	if current := d.store.Get(session.ID); current != nil {
		session.State = current.State
		session.StateSince = current.StateSince
		session.StateUpdatedAt = current.StateUpdatedAt
	}
	d.clearLongRunTracking(msg.ID)
	if err := d.store.AddChecked(session); err != nil {
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
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
		if req.existingSession != nil && plan.hadPriorIntent {
			d.store.SetLaunchIntent(msg.ID, plan.priorIntent)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: persistErr}
	}
	if req.hasPluginDriver && !d.store.BeginAgentDriverRun(session.ID, req.pluginDriver.PluginName, plan.pluginRunID) {
		d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		killErr := d.ptyBackend.Kill(context.Background(), msg.ID, syscall.SIGTERM)
		removeErr := d.ptyBackend.Remove(context.Background(), msg.ID)
		if req.existingSession == nil {
			d.store.Remove(session.ID)
		} else if plan.hadPriorIntent {
			d.store.SetLaunchIntent(session.ID, plan.priorIntent)
		}
		cursorErr := fmt.Errorf("initialize plugin driver run cursor")
		if killErr != nil {
			cursorErr = fmt.Errorf("%w; kill spawned runtime: %v", cursorErr, killErr)
		}
		if removeErr != nil {
			cursorErr = fmt.Errorf("%w; remove spawned runtime: %v", cursorErr, removeErr)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: cursorErr}
	}
	if persistResumeID := agentdriver.SpawnResumeSessionID(req.driver, session.ID, req.resumeSessionID, protocol.Deref(msg.ResumePicker)); persistResumeID != "" {
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
	if !req.isShell {
		d.startTranscriptWatcher(session.ID, session.Agent, session.Directory, req.spawnStartedAt)
	}
	d.store.UpsertRecentLocation(req.cwd)
	d.associateSessionWithWorkspace(session.ID, req.workspaceID)
	// Single mechanism: spawn_session guarantees the session has a layout
	// pane. Clients that pre-created one (app, delegate, ticket resume) hit
	// the adopt path; bare spawns (wsctl, scripts) get default placement.
	// A pane failure must not fail the spawn — the session is already live.
	if req.workspaceID != "" {
		if _, err := d.ensureWorkspaceSessionPane(req.workspaceID, session.ID, session.Label); err != nil {
			d.logf("ensure workspace pane for session %s: %v", session.ID, err)
		}
	}
	d.setWorkspacePaneStatusForSession(session.ID, workspacelayout.PaneStatusReady, "")
	eventType := protocol.EventSessionRegistered
	if req.existingSession != nil {
		eventType = protocol.EventSessionStateChanged
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{Event: eventType, Session: d.sessionForBroadcast(session)})
	d.recomputeAndBroadcastWorkspaceForSession(session.ID)
	if req.hasPluginDriver {
		if exit := d.finishPluginSessionLaunch(msg.ID, true); exit != nil {
			d.handlePTYExit(*exit)
		}
	}
	plan.commit()
	return &spawnOutcome{}
}

// runSpawnPipeline executes the full spawn pipeline without any client
// communication. nil means the session is live (spawned or already running).
func (d *Daemon) runSpawnPipeline(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) *spawnRejection {
	req, rejection := d.validateSpawnPrelock(msg, policy)
	if rejection != nil {
		return rejection
	}
	releaseSpawnLock := d.acquireSpawnLock(msg.ID)
	defer releaseSpawnLock()

	if rejection := d.normalizeSpawnRequest(req); rejection != nil {
		return rejection
	}
	plan, rejection := d.resolveSpawnIntent(req)
	if rejection != nil {
		return rejection
	}
	if outcome := d.executeSpawn(req, plan); outcome.err != nil {
		return &spawnRejection{err: outcome.err}
	} else if outcome.alreadyLive {
		return nil
	}
	if outcome := d.commitSpawn(req, plan); outcome.err != nil {
		return &spawnRejection{err: outcome.err}
	}
	return nil
}
