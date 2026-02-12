package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// contentBlock represents a single content block in the message
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"` // For "text" type blocks
}

// transcriptEntry represents a single entry in the JSONL transcript
// Claude Code uses content as an array of content blocks, not a string
type transcriptEntry struct {
	Type    string `json:"type"`
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
	if strings.TrimSpace(entry.Timestamp) == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return ts
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
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if content := ExtractAssistantContent(line); content != "" {
			lastAssistantContent = content
		}
	}

	if err := scanner.Err(); err != nil {
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
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var (
		lastAssistantContent string
		lastAssistantSeq     int
		lastUserSeq          int
		lastAssistantTS      time.Time
		seq                  int
	)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		seq++

		if isUserEntry(line) {
			lastUserSeq = seq
		}
		if content := ExtractAssistantContent(line); content != "" {
			lastAssistantContent = content
			lastAssistantSeq = seq
			lastAssistantTS = extractLineTimestamp(line)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	// Latest user has no subsequent assistant yet.
	if lastUserSeq > 0 && lastAssistantSeq <= lastUserSeq {
		return "", nil
	}
	if !minAssistantTimestamp.IsZero() && !lastAssistantTS.IsZero() && lastAssistantTS.Before(minAssistantTimestamp) {
		return "", nil
	}

	if len(lastAssistantContent) > maxChars {
		lastAssistantContent = lastAssistantContent[len(lastAssistantContent)-maxChars:]
	}
	return lastAssistantContent, nil
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
