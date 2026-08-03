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

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
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
	ghosttyBlocks []pty.AttachBlockData
	// ghosttyPlacements are the kitty placements on that same screen, captured
	// in the same hold. The dump has no images in it — the APC bytes were
	// stripped before any of this — so without these a restore silently loses
	// every image the session was showing.
	ghosttyPlacements   []pty.KittyPlacement
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
		ghosttyPlacements:   info.GhosttyPlacements,
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
	// A revive must re-enter the launch lifecycle; preserving recoverable would let
	// commitSpawn's record overwrite the live state applied during spawn.
	if existing != nil && existing.State != protocol.SessionStateRecoverable {
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
	// Endpoint binding: prefer the spawn message's explicit endpoint; a respawn
	// with no endpoint in the message keeps the stored binding.
	if id := strings.TrimSpace(protocol.Deref(msg.EndpointID)); id != "" {
		session.EndpointID = protocol.Ptr(id)
	} else if existing != nil {
		session.EndpointID = existing.EndpointID
	}
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

func (d *Daemon) handleSpawnSession(client *wsClient, msg *protocol.SpawnSessionMessage) {
	d.handleSpawnSessionWithPolicy(client, msg, internalSpawnPolicy{})
}

// handleSpawnSessionWithPolicy is reserved for daemon-owned launch paths. The
// public workspace protocol must not be able to grant automatic approval or
// working-directory trust independently of the user's daemon settings.
func (d *Daemon) handleSpawnSessionWithPolicy(client *wsClient, msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) {
	if rejection := d.runSpawnPipeline(msg, policy); rejection != nil {
		d.sendSpawnRejection(client, msg.ID, rejection)
		return
	}
	d.sendToClient(client, protocol.SpawnResultMessage{Event: protocol.EventSpawnResult, ID: msg.ID, Success: true})
}

func (d *Daemon) sendSpawnRejection(client *wsClient, sessionID string, rejection *spawnRejection) {
	if rejection.commandError != "" {
		d.sendCommandError(client, protocol.CmdSpawnSession, rejection.commandError)
		return
	}
	d.sendSpawnFailure(client, sessionID, rejection.err)
}

// buildStoredIntentSpawn reconstructs the spawn message and internal policy for
// relaunching a session from its durable record and launch intent, with
// client-supplied geometry. Shared by revive-on-attach and daemon-owned reload.
func buildStoredIntentSpawn(session *protocol.Session, intent store.LaunchIntent, cols, rows int) (*protocol.SpawnSessionMessage, internalSpawnPolicy) {
	spawnMsg := &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          session.ID,
		Cwd:         session.Directory,
		Agent:       string(session.Agent),
		WorkspaceID: session.WorkspaceID,
		Label:       protocol.Ptr(session.Label),
		Cols:        cols,
		Rows:        rows,
		YoloMode:    protocol.Ptr(intent.YoloMode),
	}
	if intent.Executable != "" {
		spawnMsg.Executable = protocol.Ptr(intent.Executable)
	}
	if intent.Model != "" {
		spawnMsg.Model = protocol.Ptr(intent.Model)
	}
	if intent.Effort != "" {
		spawnMsg.Effort = protocol.Ptr(intent.Effort)
	}
	launch := intent.UnattendedLaunch
	if !launch.IsZero() {
		launch = launch.WithLegacyDefaults()
		spawnMsg.Model = protocol.Ptr(launch.Model)
		spawnMsg.Effort = protocol.Ptr(launch.Effort)
		spawnMsg.Executable = protocol.Ptr(launch.Executable)
	}
	return spawnMsg, internalSpawnPolicy{unattendedLaunch: launch}
}

// reviveSessionForAttach respawns a recoverable session from its durable record
// so a revive-policy attach can proceed. It refuses (error, no spawn) unless the
// stored session is recoverable, geometry is positive, and a LaunchIntent exists.
func (d *Daemon) reviveSessionForAttach(msg *protocol.AttachSessionMessage) error {
	session := d.store.Get(msg.ID)
	if session == nil || session.State != protocol.SessionStateRecoverable {
		return errors.New("session not recoverable")
	}
	if msg.Cols == nil || msg.Rows == nil || *msg.Cols <= 0 || *msg.Rows <= 0 {
		return errors.New("revive requires pty geometry")
	}
	intent, ok := d.store.LaunchIntent(msg.ID)
	if !ok {
		return errors.New("no stored launch intent")
	}
	spawnMsg, policy := buildStoredIntentSpawn(session, intent, int(*msg.Cols), int(*msg.Rows))
	if rejection := d.runSpawnPipeline(spawnMsg, policy); rejection != nil {
		return rejection.err
	}
	return nil
}

func (d *Daemon) handleAttachSession(client *wsClient, msg *protocol.AttachSessionMessage) {
	subID := wsSubscriberID(client, msg.ID)

	info, stream, err := d.ptyBackend.Attach(context.Background(), msg.ID, subID)
	revived := false
	if err != nil && errors.Is(err, pty.ErrSessionNotFound) && protocol.Deref(msg.AttachPolicy) == protocol.AttachPolicyRevive {
		attachErr := err
		if reviveErr := d.reviveSessionForAttach(msg); reviveErr != nil {
			err = errors.Join(attachErr, reviveErr)
		} else {
			info, stream, err = d.ptyBackend.Attach(context.Background(), msg.ID, subID)
			if err == nil {
				revived = true
			}
		}
	}
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
	if revived {
		result.Revived = protocol.Ptr(true)
	}
	if len(replay.ghosttySnapshot) > 0 {
		result.Snapshot = &protocol.AttachSnapshot{
			Cols:                int(replay.ghosttyCols),
			Rows:                int(replay.ghosttyRows),
			VtDumpB64:           base64.StdEncoding.EncodeToString(replay.ghosttySnapshot),
			Blocks:              attachBlocksToProtocol(replay.ghosttyBlocks),
			Placements:          placementsToProtocol(replay.ghosttyPlacements),
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
	snapshotBytes := 0
	screenCols := 0
	screenRows := 0
	if info.Screen != nil {
		snapshotBytes = len(info.Screen.Payload)
		screenCols = int(info.Screen.Cols)
		screenRows = int(info.Screen.Rows)
		result.ScreenSnapshot = protocol.Ptr(base64.StdEncoding.EncodeToString(info.Screen.Payload))
		result.ScreenRows = protocol.Ptr(screenRows)
		result.ScreenCols = protocol.Ptr(screenCols)
	}
	d.logf(
		"PTY screen snapshot: id=%s running=%v last_seq=%d snapshot_bytes=%d screen=%dx%d have_screen=%v",
		msg.ID, info.Running, info.LastSeq, snapshotBytes,
		screenCols, screenRows, info.Screen != nil,
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
		case ptybackend.OutputEventKindPlacements:
			// Byte-stream companion, not a state change: the set describes the
			// grid the same-seq output produces, so it rides this per-session
			// path in order behind those bytes rather than the bus, exactly as
			// pty_output does. Clients that cannot draw images never asked to
			// hear about them.
			if !client.HasCapability(protocol.CapabilityKittyImages) {
				continue
			}
			outbound, err := encodeKittyPlacementsMessage(sessionID, event)
			if err != nil {
				d.logf("kitty_placements marshal failed: id=%s seq=%d err=%v", sessionID, event.Seq, err)
				continue
			}
			// Blocking, like the bytes: a dropped set leaves the client drawing
			// an image that has moved or gone, and only the next change would
			// heal it — which on an idle session never comes.
			if !d.sendOutboundBlocking(client, outbound, ptyOutputSendWait) {
				d.logf("kitty_placements send failed, closing stream: id=%s seq=%d", sessionID, event.Seq)
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
	// Under the session's write fence: a keystroke that races a doorbell's
	// paste-then-Enter pair lands after it, never inside it.
	if err := d.writePTY(msg.ID, []byte(msg.Data)); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("pty_input failed for %s: %v", msg.ID, err)
		}
	} else if d.debugLogging {
		d.logf("pty_input ok: id=%s bytes=%d", msg.ID, len(msg.Data))
	}
}

// ptyGeometry is the payload of FactSessionPTYResized. The new size is not
// derivable from the store, so the fact carries it.
type ptyGeometry struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (d *Daemon) projectSessionPTYResized(ev bus.Event) {
	geometry, ok := decodeFact[ptyGeometry](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPtyResized,
		ID:    protocol.Ptr(ev.Subject),
		Cols:  protocol.Ptr(geometry.Cols),
		Rows:  protocol.Ptr(geometry.Rows),
	})
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
	d.publishFact(FactSessionPTYResized, msg.ID, ptyGeometry{Cols: msg.Cols, Rows: msg.Rows})
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
