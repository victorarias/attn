package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func wsSubscriberID(client *wsClient, sessionID string) string {
	return fmt.Sprintf("%p:%s", client, sessionID)
}

func (d *Daemon) detachSession(client *wsClient, sessionID string) {
	client.attachMu.Lock()
	stream, hasStream := client.attachedStreams[sessionID]
	if hasStream {
		delete(client.attachedStreams, sessionID)
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
		Executable:        strings.TrimSpace(configuredExecutable),
		ClaudeExecutable:  protocol.Deref(msg.ClaudeExecutable),
		CodexExecutable:   protocol.Deref(msg.CodexExecutable),
		CopilotExecutable: protocol.Deref(msg.CopilotExecutable),
		PiExecutable:      protocol.Deref(msg.PiExecutable),
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

	if !isShell {
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
		if _, err := d.ensureWorkspaceSnapshot(session.ID); err != nil {
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
		eventType := protocol.EventSessionRegistered
		if existingSession != nil {
			eventType = protocol.EventSessionStateChanged
		}
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   eventType,
			Session: d.sessionForBroadcast(session),
		})
		d.broadcastWorkspaceSnapshot(session.ID)
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
	d.logf(
		"PTY attach result: id=%s running=%v last_seq=%d scrollback_bytes=%d snapshot_bytes=%d snapshot_fresh=%v size=%dx%d screen=%dx%d",
		msg.ID,
		info.Running,
		info.LastSeq,
		len(info.Scrollback),
		len(info.ScreenSnapshot),
		info.ScreenSnapshotFresh,
		info.Cols,
		info.Rows,
		info.ScreenCols,
		info.ScreenRows,
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
		ScrollbackTruncated: protocol.Ptr(info.ScrollbackTruncated),
		LastSeq:             protocol.Ptr(int(info.LastSeq)),
		Cols:                protocol.Ptr(int(info.Cols)),
		Rows:                protocol.Ptr(int(info.Rows)),
		Pid:                 protocol.Ptr(info.PID),
		Running:             protocol.Ptr(info.Running),
	}
	if len(info.Scrollback) > 0 {
		encoded := base64.StdEncoding.EncodeToString(info.Scrollback)
		result.Scrollback = protocol.Ptr(encoded)
	}
	if len(info.ScreenSnapshot) > 0 {
		encoded := base64.StdEncoding.EncodeToString(info.ScreenSnapshot)
		result.ScreenSnapshot = protocol.Ptr(encoded)
		result.ScreenRows = protocol.Ptr(int(info.ScreenRows))
		result.ScreenCols = protocol.Ptr(int(info.ScreenCols))
		result.ScreenCursorX = protocol.Ptr(int(info.ScreenCursorX))
		result.ScreenCursorY = protocol.Ptr(int(info.ScreenCursorY))
		result.ScreenCursorVisible = protocol.Ptr(info.ScreenCursorVisible)
		result.ScreenSnapshotFresh = protocol.Ptr(info.ScreenSnapshotFresh)
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
	if err := d.ptyBackend.Kill(context.Background(), msg.ID, sig); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("kill_session failed for %s: %v", msg.ID, err)
		}
	}
}

func shouldLogPtyCommandError(err error) bool {
	return !errors.Is(err, pty.ErrSessionNotFound)
}
