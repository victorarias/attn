package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// reloadStuckFlagGrace bounds how long a reloading flag lives after a successful
// respawn. The killed worker's exit normally consumes it within milliseconds; this
// is only a backstop so a never-arriving exit can't wedge the flag and suppress a
// future, unrelated exit of the re-spawned session.
const reloadStuckFlagGrace = 5 * time.Second

func (d *Daemon) markReloading(sessionID string) {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	if d.reloadingSessions == nil {
		d.reloadingSessions = make(map[string]bool)
	}
	d.reloadingSessions[sessionID] = true
}

// consumeReloading atomically reports whether sessionID is mid-reload and clears
// the flag. It is one-shot on purpose: exactly the killed worker's exit should be
// suppressed; the re-spawned session's later exits must be handled normally.
func (d *Daemon) consumeReloading(sessionID string) bool {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	if d.reloadingSessions[sessionID] {
		delete(d.reloadingSessions, sessionID)
		return true
	}
	return false
}

func (d *Daemon) clearReloading(sessionID string) {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	delete(d.reloadingSessions, sessionID)
}

// reloadKillMarkTTL bounds how long a reload-kill mark stays consumable. The
// backend's Kill returns only after the child exits, so the exit event lands
// within moments of the mark; the TTL only exists so a mark whose exit never
// arrived cannot suppress the crash seam for an unrelated death much later.
const reloadKillMarkTTL = 15 * time.Second

// markReloadKill records that sessionID's next exit is the kill half of a
// client-initiated reload (kill_session with reload:true). Must be called
// before the backend Kill so the mark beats the async exit event.
func (d *Daemon) markReloadKill(sessionID string) {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	if d.reloadKills == nil {
		d.reloadKills = make(map[string]time.Time)
	}
	d.reloadKills[sessionID] = time.Now()
}

// consumeReloadKill atomically reports whether sessionID's exit was caused by a
// client reload and clears the mark. One-shot: exactly the reload-killed
// worker's exit skips the ticket seam; any later exit of the respawned session
// is judged normally.
func (d *Daemon) consumeReloadKill(sessionID string) bool {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	markedAt, ok := d.reloadKills[sessionID]
	if !ok {
		return false
	}
	delete(d.reloadKills, sessionID)
	return time.Since(markedAt) <= reloadKillMarkTTL
}

// reloadLockFor returns the per-session mutex that serializes reloadSessionAgent's
// kill→remove→spawn composite. Without it, two concurrent reloads of the same
// session (a double-toggle, or a role transfer reloading both chiefs) interleave
// and the Spawn loser hits "already exists", whose failure path tears down the
// freshly-respawned agent. Holding it across the whole composite makes the toggles
// run latest-wins: the later reload kills the in-flight respawn and re-spawns with
// the session's current chief status.
func (d *Daemon) reloadLockFor(sessionID string) *sync.Mutex {
	d.reloadLocksMu.Lock()
	defer d.reloadLocksMu.Unlock()
	if d.reloadLocks == nil {
		d.reloadLocks = make(map[string]*sync.Mutex)
	}
	lock := d.reloadLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		d.reloadLocks[sessionID] = lock
	}
	return lock
}

// sessionHasLiveWorker reports whether the backend currently holds a runtime for
// sessionID. Mirrors the live-session probe handleSpawnSession uses.
func (d *Daemon) sessionHasLiveWorker(sessionID string) bool {
	if d.ptyBackend == nil {
		return false
	}
	for _, liveID := range d.ptyBackend.SessionIDs(context.Background()) {
		if liveID == sessionID {
			return true
		}
	}
	return false
}

// agentSupportsChiefReload reports whether an agent's launch path injects chief
// guidance (claude via --append-system-prompt, codex via developer_instructions,
// or a plugin driver that advertises launch_instructions).
func (d *Daemon) agentSupportsChiefGuidance(agent string) bool {
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case string(protocol.SessionAgentClaude), string(protocol.SessionAgentCodex):
		return true
	}
	driver, ok := d.ensurePluginRegistry().driver(agent)
	return ok && driver.Capabilities["launch_instructions"]
}

func (d *Daemon) agentSupportsChiefReload(agent string) bool {
	if !d.agentSupportsChiefGuidance(agent) {
		return false
	}
	if driver, ok := d.ensurePluginRegistry().driver(agent); ok {
		return driver.Capabilities["resume"]
	}
	return true
}

// reloadSessionAgent kills a live session's agent worker and re-spawns it in place
// (resume-preserving) so the launch path re-runs with the session's current
// chief-of-staff status — the only way a post-launch promotion/demotion reaches the
// system prompt. The transcript is preserved via resume; the in-flight turn, if
// any, is lost (accepted). On any inability to reconstruct the original launch
// flags it ABORTS without touching the live worker, so a yolo chief never silently
// comes back without --dangerously-skip-permissions.
func (d *Daemon) reloadSessionAgent(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.ptyBackend == nil || d.store == nil {
		return
	}
	// Serialize reloads of this session: the kill→remove→spawn composite below must
	// not interleave with a concurrent reload of the same id, or the Spawn loser tears
	// down the other's respawn. Held across the whole composite (latest-wins).
	lock := d.reloadLockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()

	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("reload: session %s not found (closed or remote); skipping", sessionID)
		return
	}
	agent := string(session.Agent)
	if !d.agentSupportsChiefReload(agent) {
		d.logf("reload: agent %q for session %s has no chief-guidance launch path; skipping", agent, sessionID)
		return
	}
	if !d.sessionHasLiveWorker(sessionID) {
		// Nothing to reload: a dead pane's next (frontend-driven) spawn re-runs the
		// launch path and picks up the current chief status on its own.
		d.logf("reload: session %s has no live worker; skipping", sessionID)
		return
	}

	opts, err := d.buildReloadSpawnOptions(session)
	if err != nil {
		// Abort, never respawn with defaulted launch flags — leave the live worker
		// running. A chief that kept its (stale) guidance beats one that silently
		// loses yolo/executable.
		d.logf("reload: cannot reconstruct launch params for %s: %v; aborting (live worker preserved)", sessionID, err)
		return
	}
	pluginReload, err := d.preparePluginReload(session, &opts, d.isChiefOfStaffSession(sessionID))
	if err != nil {
		d.logf("reload: cannot reconstruct plugin launch for %s: %v; aborting (live worker preserved)", sessionID, err)
		return
	}
	if err := d.executePreparedSessionReload(sessionID, opts, pluginReload); err != nil {
		d.logf("reload: %v", err)
	}
}

func (d *Daemon) executePreparedSessionReload(sessionID string, opts ptybackend.SpawnOptions, pluginReload *preparedPluginReload) error {
	if pluginReload != nil {
		defer pluginReload.abort()
	}
	ctx := context.Background()
	// Mark BEFORE kill so the killed worker's async exit is suppressed no matter how
	// quickly it fires relative to the kill returning.
	d.markReloading(sessionID)

	if killErr := d.ptyBackend.Kill(ctx, sessionID, syscall.SIGTERM); killErr != nil {
		// Kill's contract is to return only after the child exits; an error here is
		// almost always "already gone". Mirror terminateSession and proceed.
		d.logf("reload: kill returned error for %s (continuing): %v", sessionID, killErr)
	}
	// Remove synchronously before re-spawning: the suppressed exit deliberately does
	// NOT remove the backend entry, and Spawn rejects a still-present id. Idempotent.
	if removeErr := d.ptyBackend.Remove(ctx, sessionID); removeErr != nil {
		d.logf("reload: remove returned error for %s (continuing): %v", sessionID, removeErr)
	}

	if spawnErr := d.ptyBackend.Spawn(ctx, opts); spawnErr != nil {
		// Respawn failed and the old worker is already dead. Never leave a live-looking
		// pane over a dead session: clear the flag and run normal exit finalization so
		// the UI degrades to a dead pane (Blocker 2).
		d.logf("reload: respawn failed for %s: %v; finalizing as exited", sessionID, spawnErr)
		d.clearReloading(sessionID)
		d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
		return fmt.Errorf("respawn failed for %s: %w", sessionID, spawnErr)
	}
	if pluginReload != nil {
		if commitErr := pluginReload.commit(); commitErr != nil {
			d.logf("reload: activate plugin run failed for %s: %v; finalizing as exited", sessionID, commitErr)
			_ = d.ptyBackend.Kill(ctx, sessionID, syscall.SIGTERM)
			_ = d.ptyBackend.Remove(ctx, sessionID)
			d.clearReloading(sessionID)
			d.closePluginDriverSession(sessionID, "reload_failed", nil, "")
			d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
			return fmt.Errorf("activate plugin run failed for %s: %w", sessionID, commitErr)
		}
	}

	// Success. Do NOT clear the flag here — the killed worker's exit consumes it.
	// AfterFunc is a backstop only (never-arriving exit), so the flag cannot wedge.
	time.AfterFunc(reloadStuckFlagGrace, func() { d.clearReloading(sessionID) })
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventRuntimeRespawned,
		ID:    protocol.Ptr(sessionID),
	})
	d.logf("reload: respawned %s (agent=%s resume=%t yolo=%t)", sessionID, opts.Agent, opts.ResumeSessionID != "", opts.YoloMode)
	return nil
}

// buildReloadSpawnOptions reconstructs the SpawnOptions for an in-place re-spawn of
// an existing session. Geometry comes from the live worker (SessionInfoProvider);
// the launch flags the daemon does not persist (yolo, executable) come from the
// worker registry (SessionLaunchParamsProvider). Returns an error — and the caller
// aborts — when those params cannot be trusted.
func (d *Daemon) buildReloadSpawnOptions(session *protocol.Session) (ptybackend.SpawnOptions, error) {
	sessionID := session.ID
	paramsProvider, ok := d.ptyBackend.(ptybackend.SessionLaunchParamsProvider)
	if !ok {
		return ptybackend.SpawnOptions{}, fmt.Errorf("backend does not record launch params")
	}
	params, err := paramsProvider.SessionLaunchParams(context.Background(), sessionID)
	if err != nil {
		return ptybackend.SpawnOptions{}, fmt.Errorf("read launch params: %w", err)
	}
	if !params.Recorded {
		return ptybackend.SpawnOptions{}, fmt.Errorf("launch params not recorded (pre-reload worker)")
	}

	cols, rows := uint16(80), uint16(24)
	if infoProvider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
		if info, err := infoProvider.SessionInfo(context.Background(), sessionID); err == nil {
			if info.Cols > 0 {
				cols = info.Cols
			}
			if info.Rows > 0 {
				rows = info.Rows
			}
		}
	}

	agent := normalizeSpawnAgent(string(session.Agent))
	if pluginDriver, ok := d.ensurePluginRegistry().driver(string(session.Agent)); ok {
		agent = pluginDriver.Agent
	}
	driver := agentdriver.Get(agent)
	resumeSessionID := agentdriver.ResolveSpawnResumeSessionID(driver, sessionID, "", d.store.GetResumeSessionID(sessionID))
	// Fresh-spawn when there is nothing to resume. A session is assigned its own
	// id as the resume target at spawn time, but Claude writes its transcript
	// lazily on the first turn — so a chief promoted before it ever took a turn
	// has a resume id pointing at a transcript that does not exist, and a resume
	// would exit non-zero (a dead chief). Downgrading to a fresh launch (which
	// reuses --session-id) preserves the session identity without resuming.
	if resumeSessionID != "" && !agentdriver.ResumeAvailable(driver, resumeSessionID) {
		d.logf("reload: resume target %s for session %s is not resumable (no transcript yet); fresh-spawning instead", resumeSessionID, sessionID)
		resumeSessionID = ""
	}

	return ptybackend.SpawnOptions{
		ID:                      sessionID,
		CWD:                     session.Directory,
		Agent:                   agent,
		Label:                   session.Label,
		Cols:                    cols,
		Rows:                    rows,
		ResumeSessionID:         resumeSessionID,
		Theme:                   d.currentTerminalTheme(),
		YoloMode:                params.YoloMode,
		Executable:              params.Executable,
		ClaudeExecutable:        params.ClaudeExecutable,
		CodexExecutable:         params.CodexExecutable,
		CopilotExecutable:       params.CopilotExecutable,
		Model:                   params.Model,
		Effort:                  params.Effort,
		LoginShellEnv:           d.cachedLoginShellEnv(),
		WorkflowGuidanceEnabled: parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)),
		AutoApprove:             parseBooleanSetting(d.store.GetSetting(SettingAutoApproveEnabled)),
		// Carry the chief context-window cap across an in-place reload so a
		// reloaded chief comes back capped, not just a fresh launch. The wrapper
		// re-derives the chief's NotebookRoot (and thus emits the cap) via the
		// NotebookGuide RPC keyed on sessionID, so gate on the persisted chief
		// role here to stay consistent with that RPC; non-chief sessions resolve
		// to 0 (uncapped), matching delegated/ordinary reloads.
		ChiefContextWindowCap: d.chiefContextWindowCap(d.isChiefOfStaffSession(sessionID)),
	}, nil
}

type preparedPluginReload struct {
	d          *Daemon
	sessionID  string
	pluginName string
	runID      string
	rollback   func()
	completed  bool
}

func (p *preparedPluginReload) abort() {
	if p == nil || p.completed {
		return
	}
	p.completed = true
	p.rollback()
	p.d.abortPluginSessionLaunch(p.sessionID, "launch_failed")
}

func (p *preparedPluginReload) commit() error {
	if p == nil || p.completed {
		return nil
	}
	oldRun := p.d.store.GetAgentDriverRun(p.sessionID)
	if !p.d.store.BeginAgentDriverRun(p.sessionID, p.pluginName, p.runID) {
		return fmt.Errorf("initialize plugin driver run cursor")
	}
	p.completed = true
	if exit := p.d.finishPluginSessionLaunch(p.sessionID, true); exit != nil {
		go p.d.handlePTYExit(*exit)
	}
	if oldRun.RunID != "" && oldRun.RunID != p.runID {
		p.d.notifyPluginDriverSessionClosed(oldRun.PluginName, p.sessionID, oldRun.RunID, "reloaded", nil, "")
	}
	return nil
}

// preparePluginReload resolves the replacement plugin command before the live
// worker is killed. That preserves the reload path's fail-safe: an unavailable
// plugin, invalid metadata, or instruction preparation error leaves the current
// runtime untouched.
func (d *Daemon) preparePluginReload(session *protocol.Session, opts *ptybackend.SpawnOptions, isChief bool) (*preparedPluginReload, error) {
	reg, ok := d.ensurePluginRegistry().driver(string(session.Agent))
	if !ok {
		return nil, nil
	}
	if !reg.Capabilities["launch_instructions"] || !reg.Capabilities["resume"] {
		return nil, fmt.Errorf("agent %q requires launch_instructions and resume capabilities", reg.Agent)
	}
	runID := uuid.NewString()
	d.beginPluginSessionLaunch(session.ID, reg.PluginName, runID)
	instructions, rollback, err := d.preparePluginLaunchInstructions(session.ID, session.WorkspaceID, isChief)
	if err != nil {
		d.finishPluginSessionLaunch(session.ID, false)
		return nil, err
	}
	prepared := &preparedPluginReload{
		d: d, sessionID: session.ID, pluginName: reg.PluginName, runID: runID, rollback: rollback,
	}
	params := pluginDriverSpawnParams{
		SessionID:    session.ID,
		RunID:        runID,
		CWD:          session.Directory,
		Label:        session.Label,
		Yolo:         opts.YoloMode,
		Model:        opts.Model,
		Effort:       opts.Effort,
		Instructions: instructions,
	}
	if metadata := strings.TrimSpace(d.store.GetAgentMetadata(session.ID)); metadata != "" && json.Valid([]byte(metadata)) {
		params.Metadata = json.RawMessage(metadata)
	}
	result, err := d.resolvePluginDriverLaunch(reg, params, true)
	if err != nil {
		prepared.abort()
		return nil, err
	}
	commandEnv, err := pluginCommandEnv(result.Env)
	if err != nil {
		prepared.abort()
		return nil, err
	}
	opts.Agent = reg.Agent
	opts.ResumeSessionID = ""
	opts.LifecycleID = runID
	opts.ExternalCommand = append([]string(nil), result.Argv...)
	opts.ExternalEnv = commandEnv
	opts.ExternalCWD = strings.TrimSpace(result.CWD)
	return prepared, nil
}

type preparedPluginRoleReload struct {
	d         *Daemon
	sessionID string
	opts      ptybackend.SpawnOptions
	plugin    *preparedPluginReload
	lock      *sync.Mutex
	completed bool
}

func (p *preparedPluginRoleReload) abort() {
	if p == nil || p.completed {
		return
	}
	p.completed = true
	p.plugin.abort()
	p.lock.Unlock()
}

func (p *preparedPluginRoleReload) execute() error {
	if p == nil || p.completed {
		return nil
	}
	p.completed = true
	defer p.lock.Unlock()
	return p.d.executePreparedSessionReload(p.sessionID, p.opts, p.plugin)
}

// preparePluginRoleReload runs driver.resume before a public chief-role change
// is persisted. A failure leaves both the role and the live worker untouched.
func (d *Daemon) preparePluginRoleReload(sessionID string, desiredChief bool) (*preparedPluginRoleReload, bool, error) {
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, false, nil
	}
	reg, registered := d.ensurePluginRegistry().driver(string(session.Agent))
	activeRun := d.store.GetAgentDriverRun(sessionID)
	pluginSession := registered || activeRun.RunID != ""
	if !pluginSession {
		return nil, false, nil
	}
	if !d.sessionHasLiveWorker(sessionID) {
		return nil, true, nil
	}
	if !registered {
		return nil, true, fmt.Errorf("agent %q plugin driver is unavailable", session.Agent)
	}
	if !reg.Capabilities["launch_instructions"] || !reg.Capabilities["resume"] {
		return nil, true, fmt.Errorf("agent %q requires launch_instructions and resume capabilities", reg.Agent)
	}

	lock := d.reloadLockFor(sessionID)
	lock.Lock()
	if !d.sessionHasLiveWorker(sessionID) {
		lock.Unlock()
		return nil, true, nil
	}
	session = d.store.Get(sessionID)
	if session == nil {
		lock.Unlock()
		return nil, true, nil
	}
	opts, err := d.buildReloadSpawnOptions(session)
	if err != nil {
		lock.Unlock()
		return nil, true, err
	}
	opts.ChiefContextWindowCap = d.chiefContextWindowCap(desiredChief)
	pluginReload, err := d.preparePluginReload(session, &opts, desiredChief)
	if err != nil {
		lock.Unlock()
		return nil, true, err
	}
	if pluginReload == nil {
		lock.Unlock()
		return nil, true, fmt.Errorf("agent %q plugin driver became unavailable", session.Agent)
	}
	return &preparedPluginRoleReload{
		d: d, sessionID: sessionID, opts: opts, plugin: pluginReload, lock: lock,
	}, true, nil
}
