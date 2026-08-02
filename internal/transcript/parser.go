package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// readJSONLLines iterates JSONL lines without bufio.Scanner token limits.
// It returns an error only on underlying I/O errors.
func readJSONLLines(r io.Reader, fn func(line []byte)) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				fn(line)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// contentBlock represents a single content block in the message
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"` // For "text" type blocks
}

// transcriptEntry represents a single entry in the JSONL transcript
// Claude Code uses content as an array of content blocks, not a string
type transcriptEntry struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"` // Can be string or array
	} `json:"message"`
}

func isUserEntry(line []byte) bool {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err == nil {
		if entry.Type == "user" || entry.Message.Role == "user" {
			return true
		}
	}

	var codex codexEnvelope
	if err := json.Unmarshal(line, &codex); err == nil {
		switch codex.Type {
		case "event_msg":
			var payload codexEventMessage
			if err := json.Unmarshal(codex.Payload, &payload); err == nil {
				if payload.Type == "user_message" && payload.Message != "" {
					return true
				}
			}
		case "response_item":
			var payload codexResponseMessage
			if err := json.Unmarshal(codex.Payload, &payload); err == nil {
				if payload.Type == "message" && payload.Role == "user" {
					return true
				}
			}
		}
	}

	var copilot struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &copilot); err == nil {
		if copilot.Type == "user.message" {
			return true
		}
	}
	return false
}

func extractLineTimestamp(line []byte) time.Time {
	var entry struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return time.Time{}
	}
	return parseTranscriptTime(entry.Timestamp)
}

func extractLineUUID(line []byte) string {
	var entry struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return ""
	}
	return strings.TrimSpace(entry.UUID)
}

type AssistantTurn struct {
	Content   string
	Timestamp time.Time
	UUID      string
}

// ExtractLastAssistantMessage reads a JSONL transcript and returns
// the last N characters of the last assistant message.
func ExtractLastAssistantMessage(path string, maxChars int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lastAssistantContent string
	if err := readJSONLLines(file, func(line []byte) {
		if content := ExtractAssistantContent(line); content != "" {
			lastAssistantContent = content
		}
	}); err != nil {
		return "", err
	}

	// Truncate to last maxChars
	if len(lastAssistantContent) > maxChars {
		lastAssistantContent = lastAssistantContent[len(lastAssistantContent)-maxChars:]
	}

	return lastAssistantContent, nil
}

// ExtractLastAssistantMessageAfterLastUser reads a JSONL transcript and returns
// the last assistant message only if it appears after the latest user message.
// This prevents returning a stale prior-turn assistant message when a new turn
// has started but the assistant response has not been flushed yet.
func ExtractLastAssistantMessageAfterLastUser(path string, maxChars int) (string, error) {
	return ExtractLastAssistantMessageAfterLastUserSince(path, maxChars, time.Time{})
}

// ExtractLastAssistantMessageAfterLastUserSince reads a JSONL transcript and returns
// the last assistant message only if it appears after the latest user message.
// If minAssistantTimestamp is non-zero, assistant messages older than that are
// ignored (treated as stale).
func ExtractLastAssistantMessageAfterLastUserSince(path string, maxChars int, minAssistantTimestamp time.Time) (string, error) {
	turn, err := ExtractLastAssistantTurnAfterLastUserSince(path, maxChars, minAssistantTimestamp)
	if err != nil {
		return "", err
	}
	return turn.Content, nil
}

// ExtractLastAssistantTurnAfterLastUserSince reads a JSONL transcript and returns
// metadata for the last assistant message after the latest user message.
func ExtractLastAssistantTurnAfterLastUserSince(path string, maxChars int, minAssistantTimestamp time.Time) (AssistantTurn, error) {
	file, err := os.Open(path)
	if err != nil {
		return AssistantTurn{}, err
	}
	defer file.Close()

	var (
		lastAssistantContent string
		lastAssistantSeq     int
		lastUserSeq          int
		lastAssistantTS      time.Time
		lastAssistantUUID    string
		seq                  int
	)

	if err := readJSONLLines(file, func(line []byte) {
		seq++
		if isUserEntry(line) {
			lastUserSeq = seq
			return
		}
		if content := ExtractAssistantContent(line); content != "" {
			lastAssistantContent = content
			lastAssistantSeq = seq
			lastAssistantTS = extractLineTimestamp(line)
			lastAssistantUUID = extractLineUUID(line)
		}
	}); err != nil {
		return AssistantTurn{}, err
	}

	// Latest user has no subsequent assistant yet.
	if lastUserSeq > 0 && lastAssistantSeq <= lastUserSeq {
		return AssistantTurn{}, nil
	}
	if !minAssistantTimestamp.IsZero() && !lastAssistantTS.IsZero() && lastAssistantTS.Before(minAssistantTimestamp) {
		return AssistantTurn{}, nil
	}

	if len(lastAssistantContent) > maxChars {
		lastAssistantContent = lastAssistantContent[len(lastAssistantContent)-maxChars:]
	}
	return AssistantTurn{
		Content:   lastAssistantContent,
		Timestamp: lastAssistantTS,
		UUID:      lastAssistantUUID,
	}, nil
}

type codexEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexEventMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type codexResponseMessage struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type copilotEventEntry struct {
	Type string `json:"type"`
	Data struct {
		Content string `json:"content"`
	} `json:"data"`
}

type CopilotToolLifecycle struct {
	Kind       string
	ToolCallID string
	ToolName   string
}

// ExtractAssistantContent extracts assistant content from Claude Code, Codex, or Copilot JSONL lines.
func ExtractAssistantContent(line []byte) string {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err == nil {
		// Check if this is an assistant message (either by type or message.role)
		isAssistant := entry.Type == "assistant" || entry.Message.Role == "assistant"
		if isAssistant {
			content := extractTextContent(entry.Message.Content)
			if content != "" {
				return content
			}
		}
	}

	var codex codexEnvelope
	if err := json.Unmarshal(line, &codex); err != nil {
		return ""
	}

	switch codex.Type {
	case "event_msg":
		var payload codexEventMessage
		if err := json.Unmarshal(codex.Payload, &payload); err != nil {
			return ""
		}
		if payload.Type == "agent_message" && payload.Message != "" {
			return payload.Message
		}
	case "response_item":
		var payload codexResponseMessage
		if err := json.Unmarshal(codex.Payload, &payload); err != nil {
			return ""
		}
		if payload.Type == "message" && payload.Role == "assistant" {
			content := extractTextContent(payload.Content)
			if content != "" {
				return content
			}
		}
	}

	var copilot copilotEventEntry
	if err := json.Unmarshal(line, &copilot); err == nil {
		if copilot.Type == "assistant.message" && copilot.Data.Content != "" {
			return copilot.Data.Content
		}
	}

	return ""
}

// ExtractCopilotToolLifecycle extracts Copilot tool lifecycle events from JSONL lines.
// It returns start/complete events with the associated toolCallId and toolName (for starts).
func ExtractCopilotToolLifecycle(line []byte) (CopilotToolLifecycle, bool) {
	var evt struct {
		Type string `json:"type"`
		Data struct {
			ToolCallID string `json:"toolCallId"`
			ToolName   string `json:"toolName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &evt); err != nil {
		return CopilotToolLifecycle{}, false
	}
	if evt.Data.ToolCallID == "" {
		return CopilotToolLifecycle{}, false
	}

	switch evt.Type {
	case "tool.execution_start":
		return CopilotToolLifecycle{
			Kind:       "start",
			ToolCallID: evt.Data.ToolCallID,
			ToolName:   evt.Data.ToolName,
		}, true
	case "tool.execution_complete":
		return CopilotToolLifecycle{
			Kind:       "complete",
			ToolCallID: evt.Data.ToolCallID,
		}, true
	default:
		return CopilotToolLifecycle{}, false
	}
}

// Turn aborts.
//
// No agent reports a turn the user halted. Measured on claude 2.1.220 with all
// 31 of its hook events wired to a logger, on codex 0.146.0 with attn's own
// trusted-hash hook overrides, and on copilot 1.0.77: ESC mid-turn produces no
// Stop, no StopFailure, no Notification — nothing, for as long as you care to
// wait. All three write the abort to their transcript in the same second, which
// is the only place it can be read from.
//
// Nor can the title heartbeat stand in for the transcript. Measured on claude
// 2.1.220: halting a turn paints `✳ <task description>` 60ms later and then
// nothing for as long as you watch; a 60-second foreground `sleep` paints
// `✳ <task description>` partway through and then nothing for the remaining 64
// seconds. A halted turn and a blocking tool call are the same frames in the same
// order, so no silence threshold separates them — which is why the stale window
// is sized to the longest tool call rather than to the halt it cannot see.
const (
	// The two markers claude writes. Matched exactly rather than by prefix: a
	// user is free to type the text.
	claudeInterruptMarker           = "[Request interrupted by user]"
	claudeInterruptMarkerForToolUse = "[Request interrupted by user for tool use]"
	// The reason copilot gives when the user halts, as against the other ways it
	// abandons a turn.
	copilotUserAbortReason = "user_initiated"
	// The one codex reason that is a user halt. The enum in the 0.146.0 binary
	// also carries `replaced`, `review_ended`, and `budget_limited`, none of which
	// are: `replaced` means another turn took over and the session is working
	// again a moment later.
	codexUserAbortReason = "interrupted"
)

// TurnAbort describes a transcript line that ended a turn before it finished.
type TurnAbort struct {
	// Reason is what the agent called it, for the diagnosis.
	Reason string

	// UserHalt: the user did this. Only a user halt settles a session. The other
	// ways a turn can be abandoned are the harness's own business, and some of
	// them are followed immediately by another turn.
	UserHalt bool

	// At is the line's own timestamp, zero when the line carries none. It is what
	// separates a halt that just happened from one being re-read out of history —
	// which the watcher does routinely, because it rewinds into the file at
	// discovery and starts over at zero when a transcript is rewritten.
	At time.Time
}

// ClaudeTurnAborted reports whether a Claude transcript line records the user
// halting the turn.
//
// Two shapes, because claude writes two. `interruptedMessageId` is a dedicated
// field naming the API message ESC cancelled, and nothing else produces it — so
// it is believed on its own, and a release that reworded the marker would still
// be caught. Halting during a tool-use prompt writes the marker with no such
// field, so the marker is honored too, but only in the exact shape claude emits
// it: a lone text block, in an entry carrying none of the fields that mark a
// prompt the user submitted. A user who types or pastes the marker gets string
// content and a promptSource, and must not settle their own session.
func ClaudeTurnAborted(line []byte) (TurnAbort, bool) {
	var entry struct {
		Type                 string          `json:"type"`
		InterruptedMessageID string          `json:"interruptedMessageId"`
		Timestamp            string          `json:"timestamp"`
		PromptSource         string          `json:"promptSource"`
		PermissionMode       string          `json:"permissionMode"`
		Origin               json.RawMessage `json:"origin"`
		Message              struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "user" {
		return TurnAbort{}, false
	}

	abort := TurnAbort{UserHalt: true, At: parseTranscriptTime(entry.Timestamp)}

	if strings.TrimSpace(entry.InterruptedMessageID) != "" {
		abort.Reason = claudeInterruptMarker
		if marker, ok := claudeInterruptMarkerBlock(entry.Message.Content); ok {
			abort.Reason = marker
		}
		return abort, true
	}

	submitted := strings.TrimSpace(entry.PromptSource) != "" ||
		strings.TrimSpace(entry.PermissionMode) != "" ||
		len(entry.Origin) > 0
	if submitted {
		return TurnAbort{}, false
	}
	marker, ok := claudeInterruptMarkerBlock(entry.Message.Content)
	if !ok {
		return TurnAbort{}, false
	}
	abort.Reason = marker
	return abort, true
}

// claudeInterruptMarkerBlock matches the content shape claude writes for an
// interrupt: an array holding exactly one text block that is exactly a marker.
// The array is required — a marker the user typed arrives as a plain string.
func claudeInterruptMarkerBlock(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return "", false
	}
	var blocks []contentBlock
	if err := json.Unmarshal(trimmed, &blocks); err != nil || len(blocks) != 1 {
		return "", false
	}
	if blocks[0].Type != "text" {
		return "", false
	}
	switch text := strings.TrimSpace(blocks[0].Text); text {
	case claudeInterruptMarker, claudeInterruptMarkerForToolUse:
		return text, true
	default:
		return "", false
	}
}

// CodexTurnAborted reports whether a Codex transcript line is a turn_aborted
// event.
//
// Only `interrupted` is reported as a halt. The event itself means the turn ended
// early, but codex also emits it when one turn replaces another — where the
// session is working again immediately, and settling it would be wrong.
func CodexTurnAborted(line []byte) (TurnAbort, bool) {
	var envelope struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Type != "event_msg" {
		return TurnAbort{}, false
	}
	var payload struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.Type != "turn_aborted" {
		return TurnAbort{}, false
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = "aborted"
	}
	return TurnAbort{
		Reason:   reason,
		UserHalt: reason == codexUserAbortReason,
		At:       parseTranscriptTime(envelope.Timestamp),
	}, true
}

// CopilotTurnAborted reports whether a Copilot transcript line is an abort.
//
// Copilot writes a bare top-level `abort` event and — unlike its normal path —
// no `assistant.turn_end`, so every abort has to be seen whether or not the user
// caused it: the watcher's own turn bracket would otherwise stay open for the
// rest of the session, pinning it working. Only `user_initiated` is a halt.
func CopilotTurnAborted(line []byte) (TurnAbort, bool) {
	var entry struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Data      struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "abort" {
		return TurnAbort{}, false
	}
	reason := strings.TrimSpace(entry.Data.Reason)
	if reason == "" {
		reason = "aborted"
	}
	return TurnAbort{
		Reason:   reason,
		UserHalt: reason == copilotUserAbortReason,
		At:       parseTranscriptTime(entry.Timestamp),
	}, true
}

func parseTranscriptTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// extractTextContent extracts text from the content field which can be:
// - A string (simple format)
// - An array of content blocks (Claude Code format)
func extractTextContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try as string first (simple format)
	var strContent string
	if err := json.Unmarshal(raw, &strContent); err == nil && strContent != "" {
		return strContent
	}

	// Try as array of content blocks (Claude Code format)
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var texts []string
		for _, block := range blocks {
			if (block.Type == "text" || block.Type == "output_text") && block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	return ""
}
