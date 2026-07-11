package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/git"
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
	scrollback          []byte
	replaySegments      []ptybackend.ReplaySegment
	scrollbackTruncated bool
	screenSnapshot      []byte
	screenCols          uint16
	screenRows          uint16
	screenCursorX       uint16
	screenCursorY       uint16
	screenCursorVisible bool
	screenSnapshotFresh bool
	derivedSnapshot     bool
	rawReplayDecision   string
	rawReplayReason     string
}

// maxAgentRawReplayBytes bounds how much raw terminal history one attach can
// synchronously feed into the frontend terminal model. Workers retain a deeper
// replay log so the daemon can select the newest self-sufficient tail, but the
// WebKit main thread must not parse the full retained history in one message.
const maxAgentRawReplayBytes = 64 * 1024

func shouldPreferAgentRawReplay(session *protocol.Session) bool {
	if session == nil {
		return false
	}
	agent := strings.TrimSpace(strings.ToLower(string(session.Agent)))
	return agent == string(protocol.SessionAgentCodex)
}

func shouldIncludeAttachReplay(policy protocol.AttachPolicy, session *protocol.Session) bool {
	switch policy {
	case protocol.AttachPolicySameAppRemount:
		// Ghostty terminal models are local to a mounted renderer. Remounting a
		// pane creates an empty model that must be rehydrated from the daemon.
		return true
	case protocol.AttachPolicyFreshSpawn:
		// Codex's TUI emits terminal capability queries (CPR, DA, kitty
		// keyboard, OSC 10) on startup and waits for the responses before it
		// will draw anything. Bytes emitted between PTY spawn and terminal
		// attach are lost when replay is omitted, so Codex hangs forever
		// waiting for query responses that the frontend never had a chance to
		// produce. For Codex sessions we replay scrollback so the terminal processes
		// the queries and emits the responses Codex is waiting for.
		return shouldPreferAgentRawReplay(session)
	default:
		return true
	}
}

func shouldRestoreTerminalModelHistory(policy protocol.AttachPolicy) bool {
	return policy == protocol.AttachPolicyRelaunchRestore || policy == protocol.AttachPolicySameAppRemount
}

func limitReplayTail(data []byte, limit int) ([]byte, bool) {
	if len(data) == 0 || limit <= 0 || len(data) <= limit {
		return data, false
	}
	return data[len(data)-limit:], true
}

func replaySegmentsToPTY(segments []ptybackend.ReplaySegment) []pty.ReplaySegment {
	if len(segments) == 0 {
		return nil
	}
	out := make([]pty.ReplaySegment, 0, len(segments))
	for _, segment := range segments {
		out = append(out, pty.ReplaySegment{
			Cols: segment.Cols,
			Rows: segment.Rows,
			Data: append([]byte(nil), segment.Data...),
		})
	}
	return out
}

func rawReplaySegmentsMatchFreshSnapshot(segments []ptybackend.ReplaySegment, info ptybackend.AttachInfo) (bool, string) {
	if len(segments) == 0 {
		return false, "empty_raw_segments"
	}
	if !info.ScreenSnapshotFresh || len(info.ScreenSnapshot) == 0 {
		return false, "fresh_snapshot_unavailable"
	}
	derived, ok := pty.ScreenSnapshotFromReplaySegments(replaySegmentsToPTY(segments))
	if !ok {
		return false, "derived_snapshot_unavailable"
	}
	if derived.Cols != info.ScreenCols || derived.Rows != info.ScreenRows {
		return false, "snapshot_geometry_mismatch"
	}
	if !bytes.Equal(derived.Payload, info.ScreenSnapshot) {
		return false, "snapshot_payload_mismatch"
	}
	return true, ""
}

func rawReplayMatchesFreshSnapshot(rawTail []byte, info ptybackend.AttachInfo) (bool, string) {
	if len(rawTail) == 0 {
		return false, "empty_raw_tail"
	}
	if !info.ScreenSnapshotFresh || len(info.ScreenSnapshot) == 0 {
		return false, "fresh_snapshot_unavailable"
	}
	if info.Cols == 0 || info.Rows == 0 {
		return false, "pty_geometry_unavailable"
	}
	derived, ok := pty.ScreenSnapshotFromReplay(rawTail, info.Cols, info.Rows)
	if !ok {
		return false, "derived_snapshot_unavailable"
	}
	if derived.Cols != info.ScreenCols || derived.Rows != info.ScreenRows {
		return false, "snapshot_geometry_mismatch"
	}
	if !bytes.Equal(derived.Payload, info.ScreenSnapshot) {
		return false, "snapshot_payload_mismatch"
	}
	return true, ""
}

func applyScreenSnapshot(payload *attachReplayPayload, snapshot pty.ReplayScreenSnapshot, decision, reason string) {
	payload.screenSnapshot = snapshot.Payload
	payload.screenCols = snapshot.Cols
	payload.screenRows = snapshot.Rows
	payload.screenCursorX = snapshot.CursorX
	payload.screenCursorY = snapshot.CursorY
	payload.screenCursorVisible = snapshot.CursorVisible
	payload.screenSnapshotFresh = true
	payload.derivedSnapshot = true
	payload.rawReplayDecision = decision
	payload.rawReplayReason = reason
}

func buildAttachReplayPayload(info ptybackend.AttachInfo, session *protocol.Session, policy protocol.AttachPolicy) attachReplayPayload {
	payload := attachReplayPayload{
		scrollbackTruncated: info.ScrollbackTruncated,
		screenSnapshot:      info.ScreenSnapshot,
		screenCols:          info.ScreenCols,
		screenRows:          info.ScreenRows,
		screenCursorX:       info.ScreenCursorX,
		screenCursorY:       info.ScreenCursorY,
		screenCursorVisible: info.ScreenCursorVisible,
		screenSnapshotFresh: info.ScreenSnapshotFresh,
		rawReplayDecision:   "default",
	}

	if !shouldIncludeAttachReplay(policy, session) {
		payload.scrollbackTruncated = false
		payload.screenSnapshot = nil
		payload.screenCols = 0
		payload.screenRows = 0
		payload.screenCursorX = 0
		payload.screenCursorY = 0
		payload.screenCursorVisible = false
		payload.screenSnapshotFresh = false
		payload.rawReplayDecision = "omit_replay_for_policy"
		return payload
	}

	if (shouldPreferAgentRawReplay(session) || shouldRestoreTerminalModelHistory(policy)) &&
		(len(info.Scrollback) > 0 || len(info.ReplaySegments) > 0) {
		if len(info.ReplaySegments) > 0 {
			// A clipped tail is still served as long as it reproduces the live
			// screen: segments are whole boundary-safe writes, so the tail never
			// opens mid-escape-sequence, and the snapshot match below proves the
			// kept history is self-sufficient. Restored panes keep deep
			// scrollback instead of degrading to a bare screen snapshot.
			segments, clipped := pty.LimitReplaySegmentsTail(replaySegmentsToPTY(info.ReplaySegments), maxAgentRawReplayBytes)
			backendSegments := make([]ptybackend.ReplaySegment, 0, len(segments))
			for _, segment := range segments {
				backendSegments = append(backendSegments, ptybackend.ReplaySegment{
					Cols: segment.Cols,
					Rows: segment.Rows,
					Data: append([]byte(nil), segment.Data...),
				})
			}
			if len(backendSegments) > 0 {
				if matches, reason := rawReplaySegmentsMatchFreshSnapshot(backendSegments, info); matches {
					payload.replaySegments = backendSegments
					payload.scrollbackTruncated = info.ReplayTruncated || clipped
					payload.screenSnapshot = nil
					payload.screenCols = 0
					payload.screenRows = 0
					payload.screenCursorX = 0
					payload.screenCursorY = 0
					payload.screenCursorVisible = false
					payload.screenSnapshotFresh = false
					payload.rawReplayDecision = "use_segmented_raw_replay"
					if clipped {
						payload.rawReplayReason = "clipped_segmented_tail_matches_fresh_snapshot"
					} else {
						payload.rawReplayReason = "full_segmented_raw_replay_matches_fresh_snapshot"
					}
					return payload
				} else {
					payload.rawReplayDecision = "use_fresh_snapshot"
					payload.rawReplayReason = reason
				}
			} else {
				payload.rawReplayDecision = "use_fresh_snapshot"
				payload.rawReplayReason = "segmented_raw_replay_exceeds_budget"
			}
		} else {
			tail, clipped := limitReplayTail(info.Scrollback, maxAgentRawReplayBytes)
			if !clipped {
				if matches, reason := rawReplayMatchesFreshSnapshot(tail, info); matches {
					payload.scrollback = tail
					payload.scrollbackTruncated = info.ScrollbackTruncated
					payload.screenSnapshot = nil
					payload.screenCols = 0
					payload.screenRows = 0
					payload.screenCursorX = 0
					payload.screenCursorY = 0
					payload.screenCursorVisible = false
					payload.screenSnapshotFresh = false
					payload.rawReplayDecision = "use_raw_replay"
					payload.rawReplayReason = "full_raw_replay_matches_fresh_snapshot"
					return payload
				} else {
					payload.rawReplayDecision = "use_fresh_snapshot"
					payload.rawReplayReason = reason
				}
			} else {
				payload.rawReplayDecision = "use_fresh_snapshot"
				payload.rawReplayReason = "raw_replay_exceeds_budget"
			}
		}
	}

	// When no live screen model is available, derive the visible frame from the
	// buffered PTY output so attaches can restore the current screen without
	// trusting an unverified raw replay. Prefer geometry-aware replay segments
	// over flattened scrollback when both are available.
	if len(payload.screenSnapshot) == 0 && len(info.ReplaySegments) > 0 {
		if snap, ok := pty.ScreenSnapshotFromReplaySegments(replaySegmentsToPTY(info.ReplaySegments)); ok {
			applyScreenSnapshot(&payload, snap, "use_derived_segmented_snapshot", "fresh_snapshot_missing")
		}
	}

	// Fall back to flat replay derivation only when geometry-aware replay
	// segments are unavailable.
	if len(payload.screenSnapshot) == 0 && len(info.Scrollback) > 0 {
		if snap, ok := pty.ScreenSnapshotFromReplay(info.Scrollback, info.Cols, info.Rows); ok {
			applyScreenSnapshot(&payload, snap, "use_derived_snapshot", "fresh_snapshot_missing")
		}
	}

	// A fresh snapshot is sufficient when verified raw history was unavailable.
	// Rehydrating a fresh Ghostty model uses raw history above only when it is
	// complete, bounded, and reconstructs the daemon's current visible frame.
	if len(info.Scrollback) > 0 && !(len(payload.screenSnapshot) > 0 && payload.screenSnapshotFresh) {
		payload.scrollback = info.Scrollback
		if payload.rawReplayDecision == "default" {
			payload.rawReplayDecision = "use_scrollback"
		}
	}

	return payload
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

func (d *Daemon) handleSpawnSession(client *wsClient, msg *protocol.SpawnSessionMessage) {
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
	isChief := protocol.Deref(msg.ChiefOfStaff) || d.isChiefOfStaffSession(msg.ID)
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

	// Assign the chief role BEFORE Spawn so the agent's launch path (and its async
	// notebook-guide query) sees chief=true and injects the guidance on the first
	// boot. Rolled back below if the launch fails, so a never-launched session
	// never holds the role.
	chiefAssigned := d.maybeAssignChiefOnSpawn(msg.ID, agent, protocol.Deref(msg.ChiefOfStaff), existingSession)

	if err := d.ptyBackend.Spawn(context.Background(), spawnOpts); err != nil {
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
		branchInfo, _ := git.GetBranchInfo(cwd)
		nowStr := string(protocol.TimestampNow())
		initialState := protocol.SessionStateLaunching
		if isShell {
			// A shell is a plain user-driven PTY: it has no agent turn
			// lifecycle, no Stop hook, and (deliberately) no state detector, so
			// nothing ever transitions it once spawned. Seed it `idle` rather
			// than `working` — a shell sitting at a prompt is not "working", and
			// the old `working` seed made every shell (and its workspace) show a
			// permanent green dot it could never leave until the process exited.
			initialState = protocol.SessionStateIdle
		}
		initialStateSince := nowStr
		initialStateUpdatedAt := nowStr
		if existingSession != nil {
			initialState = existingSession.State
			initialStateSince = existingSession.StateSince
			initialStateUpdatedAt = existingSession.StateUpdatedAt
			if initialStateSince == "" {
				initialStateSince = nowStr
			}
			if initialStateUpdatedAt == "" {
				initialStateUpdatedAt = nowStr
			}
		}
		if hasPluginDriver && !pluginDriver.Capabilities["state_reporting"] {
			initialState = protocol.SessionStateWorking
			initialStateSince = nowStr
			initialStateUpdatedAt = nowStr
		}
		session := &protocol.Session{
			ID:             msg.ID,
			Label:          label,
			Agent:          protocol.SessionAgent(agent),
			Directory:      cwd,
			State:          initialState,
			StateSince:     initialStateSince,
			StateUpdatedAt: initialStateUpdatedAt,
			LastSeen:       nowStr,
			WorkspaceID:    workspaceID,
		}
		if branchInfo != nil {
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
	replay := buildAttachReplayPayload(info, d.store.Get(msg.ID), protocol.Deref(msg.AttachPolicy))
	replayBytes := len(replay.scrollback)
	for _, segment := range replay.replaySegments {
		replayBytes += len(segment.Data)
	}
	d.logf(
		"PTY attach result: id=%s policy=%s running=%v last_seq=%d scrollback_bytes=%d replay_bytes=%d snapshot_bytes=%d snapshot_fresh=%v derived_snapshot=%v replay_decision=%s replay_reason=%s size=%dx%d screen=%dx%d",
		msg.ID,
		protocol.Deref(msg.AttachPolicy),
		info.Running,
		info.LastSeq,
		len(info.Scrollback),
		replayBytes,
		len(replay.screenSnapshot),
		replay.screenSnapshotFresh,
		replay.derivedSnapshot,
		replay.rawReplayDecision,
		replay.rawReplayReason,
		info.Cols,
		info.Rows,
		replay.screenCols,
		replay.screenRows,
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
		Event:               protocol.EventAttachResult,
		ID:                  msg.ID,
		Success:             true,
		ScrollbackTruncated: protocol.Ptr(replay.scrollbackTruncated),
		LastSeq:             protocol.Ptr(int(info.LastSeq)),
		Cols:                protocol.Ptr(int(info.Cols)),
		Rows:                protocol.Ptr(int(info.Rows)),
		Pid:                 protocol.Ptr(info.PID),
		Running:             protocol.Ptr(info.Running),
	}
	if len(replay.scrollback) > 0 {
		encoded := base64.StdEncoding.EncodeToString(replay.scrollback)
		result.Scrollback = protocol.Ptr(encoded)
	}
	if len(replay.replaySegments) > 0 {
		result.ReplaySegments = make([]protocol.ReplaySegment, 0, len(replay.replaySegments))
		for _, segment := range replay.replaySegments {
			result.ReplaySegments = append(result.ReplaySegments, protocol.ReplaySegment{
				Cols: int(segment.Cols),
				Rows: int(segment.Rows),
				Data: base64.StdEncoding.EncodeToString(segment.Data),
			})
		}
	}
	if len(replay.screenSnapshot) > 0 {
		encoded := base64.StdEncoding.EncodeToString(replay.screenSnapshot)
		result.ScreenSnapshot = protocol.Ptr(encoded)
		result.ScreenRows = protocol.Ptr(int(replay.screenRows))
		result.ScreenCols = protocol.Ptr(int(replay.screenCols))
		result.ScreenCursorX = protocol.Ptr(int(replay.screenCursorX))
		result.ScreenCursorY = protocol.Ptr(int(replay.screenCursorY))
		result.ScreenCursorVisible = protocol.Ptr(replay.screenCursorVisible)
		result.ScreenSnapshotFresh = protocol.Ptr(replay.screenSnapshotFresh)
	}
	d.sendToClient(client, result)
}

// snapshotSeedScreen resolves the visible frame to seed an observer with,
// preferring a fresh worker-rendered screen and otherwise deriving one from the
// session's buffered replay output. The derived path is what lets observers
// seed sessions whose worker predates the snapshot RPC: those workers can't
// render a screen on demand but still hand back their scrollback on attach.
// The bool results are (derived, ok).
func snapshotSeedScreen(info ptybackend.AttachInfo) (pty.ReplayScreenSnapshot, bool, bool) {
	if info.ScreenSnapshotFresh && len(info.ScreenSnapshot) > 0 {
		return pty.ReplayScreenSnapshot{
			Payload:       info.ScreenSnapshot,
			Cols:          info.ScreenCols,
			Rows:          info.ScreenRows,
			CursorX:       info.ScreenCursorX,
			CursorY:       info.ScreenCursorY,
			CursorVisible: info.ScreenCursorVisible,
		}, false, true
	}
	// Prefer geometry-aware replay segments over flattened scrollback when both
	// are present, mirroring the attach replay derivation.
	if len(info.ReplaySegments) > 0 {
		if snap, ok := pty.ScreenSnapshotFromReplaySegments(replaySegmentsToPTY(info.ReplaySegments)); ok {
			return snap, true, true
		}
	}
	if len(info.Scrollback) > 0 {
		if snap, ok := pty.ScreenSnapshotFromReplay(info.Scrollback, info.Cols, info.Rows); ok {
			return snap, true, true
		}
	}
	return pty.ReplayScreenSnapshot{}, false, false
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
	screen, derived, haveScreen := snapshotSeedScreen(info)
	if haveScreen {
		result.ScreenSnapshot = protocol.Ptr(base64.StdEncoding.EncodeToString(screen.Payload))
		result.ScreenRows = protocol.Ptr(int(screen.Rows))
		result.ScreenCols = protocol.Ptr(int(screen.Cols))
		result.ScreenCursorX = protocol.Ptr(int(screen.CursorX))
		result.ScreenCursorY = protocol.Ptr(int(screen.CursorY))
		result.ScreenCursorVisible = protocol.Ptr(screen.CursorVisible)
		// The frontend seeds only when this is set. A screen derived from buffered
		// output is just as paintable as a worker-rendered one, so we present it as
		// fresh — that is what lets observers seed sessions whose worker can't
		// render a screen on demand (e.g. an old worker that survived an upgrade).
		result.ScreenSnapshotFresh = protocol.Ptr(true)
	}
	d.logf(
		"PTY screen snapshot: id=%s running=%v last_seq=%d snapshot_bytes=%d screen=%dx%d have_screen=%v derived=%v",
		msg.ID, info.Running, info.LastSeq, len(screen.Payload),
		screen.Cols, screen.Rows, haveScreen, derived,
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
