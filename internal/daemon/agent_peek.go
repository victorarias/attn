package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/transcript"
)

// agent_peek is the passive half of agents conversing and observing: one agent
// reads another session's state, todos, last assistant message, and rendered
// screen, all from what the daemon already holds. Nothing here writes to the
// target's PTY or wakes its agent — watching must cost the observed session
// nothing.

// agentPeekMessageMaxChars bounds the last assistant message. Same receipt as
// the annotatable window (ws_session_message.go): the largest prose block seen
// across 120 transcripts was 18,713 chars, so 64KiB is a tripwire only a
// runaway transcript touches.
const agentPeekMessageMaxChars = annotatableMessageMaxChars

// agentShortIDLength matches what `attn agent list` prints, so an id the daemon
// puts in front of an agent is one that agent can paste back.
const agentShortIDLength = 8

// agentPeekSnapshotTimeout matches the model-capture snapshot budget: the
// worker answers from its parsed terminal without touching the agent process.
const agentPeekSnapshotTimeout = modelCaptureSnapshotTimeout

func (d *Daemon) handleAgentPeek(conn net.Conn, msg *protocol.AgentPeekMessage) {
	session, errCode := d.resolveSessionByIDOrPrefix(msg.TargetSessionID)
	if session == nil {
		d.sendError(conn, errCode)
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:              true,
		AgentPeekResult: d.agentPeekResult(session),
	})
}

// resolveSessionByIDOrPrefix accepts a full session id or a unique prefix —
// `attn agent list` prints 8-char short ids, and every other `attn agent`
// command takes them back. The error code names what went wrong: an ambiguous
// prefix is the caller's to lengthen, not ours to guess.
func (d *Daemon) resolveSessionByIDOrPrefix(target string) (*protocol.Session, string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, "session_not_found"
	}
	if session := d.store.Get(target); session != nil {
		return session, ""
	}
	var match *protocol.Session
	for _, session := range d.store.List("") {
		if !strings.HasPrefix(session.ID, target) {
			continue
		}
		if match != nil {
			return nil, "ambiguous_session"
		}
		match = session
	}
	if match == nil {
		return nil, "session_not_found"
	}
	return match, ""
}

func (d *Daemon) agentPeekResult(session *protocol.Session) *protocol.AgentPeekResult {
	decorated := d.sessionForBroadcast(session)
	result := &protocol.AgentPeekResult{
		SessionID:   decorated.ID,
		Label:       decorated.Label,
		Agent:       string(decorated.Agent),
		WorkspaceID: decorated.WorkspaceID,
		State:       string(decorated.State),
		StateSince:  decorated.StateSince,
		LastSeen:    decorated.LastSeen,
		StateReason: decorated.StateReason,
		TurnOwed:    decorated.TurnOwed,
		Todos:       decorated.Todos,
		CrewMember:  decorated.CrewMember,
	}
	if result.Todos == nil {
		result.Todos = []string{}
	}
	if workspace := d.store.GetWorkspace(decorated.WorkspaceID); workspace != nil {
		result.WorkspaceTitle = protocol.Ptr(workspace.Title)
	}
	if path := d.inspectableTranscriptPath(session); path != "" {
		if message, err := transcript.ExtractLastAssistantMessage(path, agentPeekMessageMaxChars); err == nil && strings.TrimSpace(message) != "" {
			result.LastAssistantMessage = protocol.Ptr(message)
		}
	}
	result.Screen = d.agentPeekScreen(session.ID)
	return result
}

// agentPeekScreen degrades to nil — never an error — when the backend cannot
// serve a snapshot (no provider, old worker, no rendered frame yet).
func (d *Daemon) agentPeekScreen(sessionID string) *protocol.AgentPeekScreen {
	provider, ok := d.ptyBackend.(ptybackend.SnapshotProvider)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentPeekSnapshotTimeout)
	defer cancel()
	snapshot, err := provider.Snapshot(ctx, sessionID)
	if err != nil {
		d.logf("agent peek snapshot unavailable: session=%s err=%v", sessionID, err)
		return nil
	}
	if snapshot.Screen == nil || !snapshot.Screen.HasText {
		return nil
	}
	return &protocol.AgentPeekScreen{
		Text: snapshot.Screen.Text,
		Cols: int(snapshot.Screen.Cols),
		Rows: int(snapshot.Screen.Rows),
	}
}
