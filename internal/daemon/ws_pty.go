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

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// Each attach gets its own subscriber id. The PTY session's subscriber map is
// keyed by subscriber id, and a workerStream close emits a Detach RPC for the
// id it registered. Reusing a subID on re-attach lets the dying stream's
// Detach remove the freshly installed subscriber, silently starving the new
// stream of output.
var wsSubscriberCounter atomic.Int64

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

const maxAgentRawReplayBytes = 256 * 1024

func shouldPreferAgentRawReplay(session *protocol.Session) bool {
	if session == nil {
		return false
	}
	agent := strings.TrimSpace(strings.ToLower(string(session.Agent)))
	return agent == string(protocol.SessionAgentCodex)
}

func shouldIncludeAttachReplay(policy protocol.AttachPolicy) bool {
	switch policy {
	case protocol.AttachPolicySameAppRemount, protocol.AttachPolicyFreshSpawn:
		return false
	default:
		return true
	}
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

	if !shouldIncludeAttachReplay(policy) {
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

	if shouldPreferAgentRawReplay(session) && (len(info.Scrollback) > 0 || len(info.ReplaySegments) > 0) {
		if len(info.ReplaySegments) > 0 {
			segments, clipped := pty.LimitReplaySegmentsTail(replaySegmentsToPTY(info.ReplaySegments), maxAgentRawReplayBytes)
			backendSegments := make([]ptybackend.ReplaySegment, 0, len(segments))
			for _, segment := range segments {
				backendSegments = append(backendSegments, ptybackend.ReplaySegment{
					Cols: segment.Cols,
					Rows: segment.Rows,
					Data: append([]byte(nil), segment.Data...),
				})
			}
			if !clipped {
				if matches, reason := rawReplaySegmentsMatchFreshSnapshot(backendSegments, info); matches {
					payload.replaySegments = backendSegments
					payload.scrollbackTruncated = info.ReplayTruncated
					payload.screenSnapshot = nil
					payload.screenCols = 0
					payload.screenRows = 0
					payload.screenCursorX = 0
					payload.screenCursorY = 0
					payload.screenCursorVisible = false
					payload.screenSnapshotFresh = false
					payload.rawReplayDecision = "use_segmented_raw_replay"
					payload.rawReplayReason = "full_segmented_raw_replay_matches_fresh_snapshot"
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

	// Fresh visible-frame snapshots are enough for current websocket clients to
	// restore the screen while live output catches up. Sending full scrollback in
	// addition to that snapshot turns split/remount attaches into multi-megabyte
	// JSON payloads with no UI benefit.
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
	case string(protocol.SessionAgentPi):
		return strings.TrimSpace(protocol.Deref(msg.PiExecutable))
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

func (d *Daemon) handleSpawnSession(client *wsClient, msg *protocol.SpawnSessionMessage) {
	agent := normalizeSpawnAgent(msg.Agent)
	isShell := agent == protocol.AgentShellValue
	spawnStartedAt := time.Now()
	existingSession := d.store.Get(msg.ID)
	cwd := resolveSpawnCWD(msg.Cwd)
	label := protocol.Deref(msg.Label)
	if label == "" {
		label = filepath.Base(cwd)
	}
	if msg.Cols <= 0 || msg.Rows <= 0 || msg.Cols > maxPTYDimValue || msg.Rows > maxPTYDimValue {
		d.sendToClient(client, protocol.SpawnResultMessage{
			Event:   protocol.EventSpawnResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(fmt.Sprintf("invalid terminal size cols=%d rows=%d (expected 1..%d)", msg.Cols, msg.Rows, maxPTYDimValue)),
		})
		return
	}
	resumeSessionID := protocol.Deref(msg.ResumeSessionID)
	driver := agentdriver.Get(agent)
	if existingSession != nil {
		resumeSessionID = agentdriver.ResolveSpawnResumeSessionID(
			driver,
			existingSession.ID,
			resumeSessionID,
			d.store.GetResumeSessionID(msg.ID),
		)
	}

	configuredExecutable := strings.TrimSpace(protocol.Deref(msg.Executable))
	if configuredExecutable == "" {
		configuredExecutable = legacyExecutableFromSpawnMessage(msg, agent)
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
		ForkSession:       protocol.Deref(msg.ForkSession),
		YoloMode:          protocol.Deref(msg.YoloMode),
		Executable:        strings.TrimSpace(configuredExecutable),
		ClaudeExecutable:  protocol.Deref(msg.ClaudeExecutable),
		CodexExecutable:   protocol.Deref(msg.CodexExecutable),
		CopilotExecutable: protocol.Deref(msg.CopilotExecutable),
		PiExecutable:      protocol.Deref(msg.PiExecutable),
		LoginShellEnv:     d.cachedLoginShellEnv(),
	}

	if err := d.ptyBackend.Spawn(context.Background(), spawnOpts); err != nil {
		d.sendToClient(client, protocol.SpawnResultMessage{
			Event:   protocol.EventSpawnResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(err.Error()),
		})
		return
	}

	// Shell PTYs are utility terminals by default (Cmd+T in the Tauri
	// app — fire-and-forget, not tracked as sessions). The native canvas
	// app needs them as first-class sessions so the workspace can render
	// each PTY as a panel; it opts in via the `shell_as_session`
	// capability declared in client_hello.
	registerAsSession := !isShell || client.HasCapability(protocol.CapabilityShellAsSession)
	if registerAsSession {
		d.clearLongRunTracking(msg.ID)
		branchInfo, _ := git.GetBranchInfo(cwd)
		nowStr := string(protocol.TimestampNow())
		session := &protocol.Session{
			ID:             msg.ID,
			Label:          label,
			Agent:          protocol.SessionAgent(agent),
			Directory:      cwd,
			State:          protocol.SessionStateLaunching,
			StateSince:     nowStr,
			StateUpdatedAt: nowStr,
			LastSeen:       nowStr,
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
		d.store.Add(session)
		if _, err := d.ensureSessionLayout(session.ID); err != nil {
			d.logf("workspace bootstrap failed for session %s: %v", session.ID, err)
		}
		if persistResumeID := agentdriver.SpawnResumeSessionID(
			driver,
			session.ID,
			resumeSessionID,
			protocol.Deref(msg.ResumePicker),
		); persistResumeID != "" {
			d.store.SetResumeSessionID(session.ID, persistResumeID)
		}
		d.startTranscriptWatcher(session.ID, session.Agent, session.Directory, spawnStartedAt)
		d.store.UpsertRecentLocation(cwd, label)
		if workspaceID := strings.TrimSpace(protocol.Deref(msg.WorkspaceID)); workspaceID != "" {
			d.associateSessionWithWorkspace(session.ID, workspaceID)
		}
		eventType := protocol.EventSessionRegistered
		if existingSession != nil {
			eventType = protocol.EventSessionStateChanged
		}
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   eventType,
			Session: d.sessionForBroadcast(session),
		})
		d.broadcastSessionLayout(session.ID)
		d.recomputeAndBroadcastWorkspaceForSession(session.ID)
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

func (d *Daemon) handleDetachSessionWS(client *wsClient, msg *protocol.DetachSessionMessage) {
	d.detachSession(client, msg.ID)
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
			d.logf(
				"pty_output forward: id=%s seq=%d bytes=%d preview=%q",
				sessionID,
				event.Seq,
				len(event.Data),
				previewBinaryForLog(event.Data),
			)
			encoded := base64.StdEncoding.EncodeToString(event.Data)
			wsEvent := &protocol.WebSocketEvent{
				Event: protocol.EventPtyOutput,
				ID:    protocol.Ptr(sessionID),
				Data:  protocol.Ptr(encoded),
				Seq:   protocol.Ptr(int(event.Seq)),
			}
			payload, err := json.Marshal(wsEvent)
			if err != nil {
				d.logf("pty_output marshal failed: id=%s seq=%d err=%v", sessionID, event.Seq, err)
				continue
			}
			if !d.sendOutboundBlocking(client, outboundMessage{kind: messageKindText, payload: payload}, ptyOutputSendWait) {
				d.logf("pty_output send failed, closing stream: id=%s seq=%d", sessionID, event.Seq)
				_ = stream.Close()
				return
			}
		case ptybackend.OutputEventKindDesync:
			d.logf("pty_desync forward: id=%s reason=%s", sessionID, event.Reason)
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
	if source := strings.TrimSpace(protocol.Deref(msg.Source)); source != "" {
		d.setPendingInputSource(msg.ID, source)
	}
	d.logf(
		"pty_input: id=%s bytes=%d preview=%q source=%s",
		msg.ID,
		len(msg.Data),
		previewBinaryForLog([]byte(msg.Data)),
		strings.TrimSpace(protocol.Deref(msg.Source)),
	)
	if err := d.ptyBackend.Input(context.Background(), msg.ID, []byte(msg.Data)); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("pty_input failed for %s: %v", msg.ID, err)
		}
	} else {
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
	if err := d.ptyBackend.Kill(context.Background(), msg.ID, sig); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("kill_session failed for %s: %v", msg.ID, err)
		}
	}
}

func shouldLogPtyCommandError(err error) bool {
	return !errors.Is(err, pty.ErrSessionNotFound)
}
