package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

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
// guidance (claude via --append-system-prompt, codex via developer_instructions).
// Only those agents gain anything from a reload; plugin-driver agents (e.g.
// copilot) and shells do not, so reloading them would be a pointless kill+resume.
func agentSupportsChiefReload(agent string) bool {
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case string(protocol.SessionAgentClaude), string(protocol.SessionAgentCodex):
		return true
	default:
		return false
	}
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
	if !agentSupportsChiefReload(agent) {
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
		return
	}

	// Success. Do NOT clear the flag here — the killed worker's exit consumes it.
	// AfterFunc is a backstop only (never-arriving exit), so the flag cannot wedge.
	time.AfterFunc(reloadStuckFlagGrace, func() { d.clearReloading(sessionID) })
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventRuntimeRespawned,
		ID:    protocol.Ptr(sessionID),
	})
	d.logf("reload: respawned %s (agent=%s resume=%t yolo=%t)", sessionID, opts.Agent, opts.ResumeSessionID != "", opts.YoloMode)
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
		YoloMode:                params.YoloMode,
		Executable:              params.Executable,
		ClaudeExecutable:        params.ClaudeExecutable,
		CodexExecutable:         params.CodexExecutable,
		CopilotExecutable:       params.CopilotExecutable,
		LoginShellEnv:           d.cachedLoginShellEnv(),
		WorkflowGuidanceEnabled: parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)),
	}, nil
}
