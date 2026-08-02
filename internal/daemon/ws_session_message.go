package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// The annotatable window: how much of a session's transcript is handed back for
// annotation. Annotations address offsets into these strings, so the caps exist
// only to keep a runaway transcript from being read into memory whole — every
// one is set far past what real sessions produce.
//
// Receipts, measured over the 120 most recent Claude Code transcripts on this
// machine (2,327 assistant prose blocks): the largest single block was 18,713
// chars (p99 3,949), and the last 32 blocks of a session totalled at most
// 21,282 chars (p90 13,486). The per-message cap is 3.4x the largest block ever
// seen and the window budget is 12x the heaviest tail, so a legitimate session
// never feels these exist. If one does, the budget is wrong: remeasure and
// raise it rather than letting a real message go missing.
const (
	annotatableMessageMaxChars = 64 * 1024
	annotatableWindowMessages  = 32
	annotatableWindowMaxChars  = 256 * 1024
)

// handleSessionMessagesGet replies with the markdown of a session's recent
// assistant messages, oldest first. Read-only: it resolves the transcript by
// the same identity ladder classification uses (hook-reported path, then
// agent-native resume id, then the finder's cwd guess) and reads it. No PTY, no
// session state.
//
// Recent messages rather than only the newest, because an annotation is about
// what the agent said, and the agent saying something else afterwards is not a
// reason to lose it. A transcript with no annotatable prose — structured
// verdicts, or pure tool activity — comes back as an empty list with
// success=true. That is not an error, and the client is expected to say so
// rather than present an empty annotation surface.
func (d *Daemon) handleSessionMessagesGet(client *wsClient, msg *protocol.SessionMessagesGetMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	result := protocol.SessionMessagesGetResultMessage{
		Event:     protocol.EventSessionMessagesGetResult,
		RequestID: msg.RequestID,
		SessionID: sessionID,
		Messages:  []protocol.SessionMessage{},
	}
	if sessionID == "" {
		result.Error = protocol.Ptr("session_messages_get: session_id is required")
		d.sendToClient(client, result)
		return
	}

	session := d.store.Get(sessionID)
	if session == nil {
		result.Error = protocol.Ptr("session_messages_get: unknown session " + sessionID)
		d.sendToClient(client, result)
		return
	}

	path := strings.TrimSpace(d.resolveTranscriptPathForSession(session, ""))
	if path == "" {
		result.Error = protocol.Ptr("session_messages_get: no transcript for session " + sessionID)
		d.sendToClient(client, result)
		return
	}

	messages, report, err := transcript.ReadRecentAssistantMessages(path, transcript.RecentAssistantMessagesLimits{
		MaxMessages:     annotatableWindowMessages,
		MaxMessageChars: annotatableMessageMaxChars,
		MaxTotalChars:   annotatableWindowMaxChars,
	})
	if err != nil {
		d.logf("session_messages_get: %s: %v", sessionID, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	// A window that left something out says which limit it hit and by how much,
	// so the number can be argued with rather than guessed at.
	if report.DroppedOversize > 0 {
		d.logf("session_messages_get: %s: dropped %d message(s) over the %d-char per-message cap (largest %d chars); annotations cannot address a partial message",
			sessionID, report.DroppedOversize, annotatableMessageMaxChars, report.LargestDropped)
	}
	if report.DroppedOld > 0 {
		d.logf("session_messages_get: %s: window held %d of %d message(s); caps are %d messages / %d chars",
			sessionID, len(messages), len(messages)+report.DroppedOld, annotatableWindowMessages, annotatableWindowMaxChars)
	}

	for _, message := range messages {
		result.Messages = append(result.Messages, protocol.SessionMessage{
			Key:      message.Key,
			Markdown: message.Content,
		})
	}
	result.Success = true
	result.Truncated = report.Truncated()
	d.sendToClient(client, result)
}
