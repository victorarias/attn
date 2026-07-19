package transcript

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	EventKindUser       = "user"
	EventKindAssistant  = "assistant"
	EventKindToolCall   = "tool_call"
	EventKindToolResult = "tool_result"
	EventKindError      = "error"

	defaultEventPageSize = 200
	cursorVersion        = "v1"
)

var (
	ErrInvalidCursor  = errors.New("invalid transcript cursor")
	ErrCursorMismatch = errors.New("transcript cursor does not match this transcript")
	ErrCursorPastEnd  = errors.New("transcript cursor is past the end of the transcript")

	bearerSecretPattern     = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	assignmentSecretPattern = regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|password|passwd|secret|authorization|cookie|private[_-]?key)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;}]+)`)
	knownTokenPattern       = regexp.MustCompile(`\b(?:github_pat_[A-Za-z0-9_]{12,}|gh[opusr]_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16})\b`)
	urlCredentialPattern    = regexp.MustCompile(`(https?://[^\s:/@]+:)[^\s/@]+(@)`)
	privateKeyPattern       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	toolFailurePattern      = regexp.MustCompile(`(?i)(?:process|script|command) exited with (?:code )?[1-9][0-9]*|["']?exit_code["']?\s*[:=]\s*[1-9][0-9]*`)
)

// Event is attn's stable, provider-neutral transcript record. Cursor identifies
// the event within its source record so a consumer can checkpoint after every
// emitted event without skipping siblings decoded from the same JSONL line.
type Event struct {
	Cursor     string `json:"cursor"`
	Timestamp  string `json:"timestamp,omitempty"`
	Kind       string `json:"kind"`
	Role       string `json:"role,omitempty"`
	Text       string `json:"text,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
}

// EventPage is one bounded read of a transcript. NextCursor advances across
// both emitted events and ignored provider noise so follow-mode polling never
// repeatedly parses the same source records.
type EventPage struct {
	Events     []Event
	NextCursor string
	AtEnd      bool
}

// ReadEventPage reads complete JSONL records strictly after cursor. It never
// consumes a partially written final record, so a later call can decode it
// exactly once after the writer appends its newline.
func ReadEventPage(path, agent, cursor string, maxEvents int) (EventPage, error) {
	if maxEvents <= 0 {
		maxEvents = defaultEventPageSize
	}

	f, err := os.Open(path)
	if err != nil {
		return EventPage{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return EventPage{}, err
	}
	fingerprint, hasCompleteRecord, err := transcriptFingerprint(f)
	if err != nil {
		return EventPage{}, err
	}

	offset := int64(0)
	skipEvents := 0
	if strings.TrimSpace(cursor) != "" {
		cursorFingerprint, cursorOffset, cursorEventIndex, decodeErr := decodeEventCursor(cursor)
		if decodeErr != nil {
			return EventPage{}, decodeErr
		}
		if !hasCompleteRecord || cursorFingerprint != fingerprint {
			return EventPage{}, ErrCursorMismatch
		}
		if cursorOffset > info.Size() {
			return EventPage{}, ErrCursorPastEnd
		}
		if cursorOffset > 0 {
			var previous [1]byte
			if _, err := f.ReadAt(previous[:], cursorOffset-1); err != nil || previous[0] != '\n' {
				return EventPage{}, ErrInvalidCursor
			}
		}
		offset = cursorOffset
		skipEvents = cursorEventIndex
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return EventPage{}, err
	}
	previousEvent, hasPreviousEvent, err := previousNormalizedEvent(f, agent, offset)
	if err != nil {
		return EventPage{}, err
	}

	page := EventPage{Events: []Event{}, NextCursor: strings.TrimSpace(cursor)}
	reader := bufio.NewReader(f)
	for {
		record, readErr := reader.ReadBytes('\n')
		complete := len(record) > 0 && record[len(record)-1] == '\n'
		if !complete {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return EventPage{}, readErr
			}
			page.AtEnd = true
			return page, nil
		}

		lineOffset := offset
		nextOffset := offset + int64(len(record))
		line := bytes.TrimSpace(record)
		parsed := parseEventLine(agent, line)
		allEvents := parsed.events
		if parsed.dedupeAssistantEcho && hasPreviousEvent &&
			previousEvent.Kind == EventKindAssistant && previousEvent.Text == allEvents[0].Text {
			allEvents = nil
		}
		if skipEvents > len(allEvents) {
			return EventPage{}, ErrInvalidCursor
		}
		if skipEvents > 0 {
			previousEvent = allEvents[skipEvents-1]
			hasPreviousEvent = true
		}
		events := allEvents[skipEvents:]

		remaining := maxEvents - len(page.Events)
		if len(events) > remaining {
			events = events[:remaining]
		}
		for i := range events {
			events[i].Cursor = encodeEventCursor(fingerprint, lineOffset, skipEvents+i+1)
			redactEvent(&events[i])
		}
		page.Events = append(page.Events, events...)
		if len(events) > 0 {
			page.NextCursor = events[len(events)-1].Cursor
		}
		if len(allEvents) > 0 {
			previousEvent = allEvents[len(allEvents)-1]
			hasPreviousEvent = true
		}

		if len(page.Events) >= maxEvents {
			return page, nil
		}

		offset = nextOffset
		skipEvents = 0
		if hasCompleteRecord {
			page.NextCursor = encodeEventCursor(fingerprint, offset, 0)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				page.AtEnd = true
				return page, nil
			}
			return EventPage{}, readErr
		}
	}
}

func previousNormalizedEvent(f *os.File, agent string, before int64) (Event, bool, error) {
	for before > 0 {
		line, start, ok, err := previousCompleteLine(f, before)
		if err != nil {
			return Event{}, false, err
		}
		if !ok {
			return Event{}, false, nil
		}
		parsed := parseEventLine(agent, bytes.TrimSpace(line))
		if len(parsed.events) > 0 {
			return parsed.events[len(parsed.events)-1], true, nil
		}
		before = start
	}
	return Event{}, false, nil
}

func previousCompleteLine(f *os.File, before int64) ([]byte, int64, bool, error) {
	end := before
	var last [1]byte
	if _, err := f.ReadAt(last[:], end-1); err != nil {
		return nil, 0, false, err
	}
	if last[0] == '\n' {
		end--
	}
	if end <= 0 {
		return nil, 0, false, nil
	}

	const chunkSize int64 = 4096
	for searchEnd := end; searchEnd > 0; {
		searchStart := max(int64(0), searchEnd-chunkSize)
		chunk := make([]byte, searchEnd-searchStart)
		if _, err := f.ReadAt(chunk, searchStart); err != nil {
			return nil, 0, false, err
		}
		if newline := bytes.LastIndexByte(chunk, '\n'); newline >= 0 {
			start := searchStart + int64(newline) + 1
			line := make([]byte, end-start)
			if _, err := f.ReadAt(line, start); err != nil {
				return nil, 0, false, err
			}
			return line, start, true, nil
		}
		searchEnd = searchStart
	}

	line := make([]byte, end)
	if _, err := f.ReadAt(line, 0); err != nil {
		return nil, 0, false, err
	}
	return line, 0, true, nil
}

func transcriptFingerprint(f *os.File) (string, bool, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", false, err
	}
	record, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	if len(record) == 0 || record[len(record)-1] != '\n' {
		return "", false, nil
	}
	sum := sha256.Sum256(bytes.TrimSpace(record))
	return hex.EncodeToString(sum[:16]), true, nil
}

func encodeEventCursor(fingerprint string, offset int64, eventIndex int) string {
	return fmt.Sprintf("%s:%s:%d:%d", cursorVersion, fingerprint, offset, eventIndex)
}

func decodeEventCursor(cursor string) (string, int64, int, error) {
	parts := strings.Split(strings.TrimSpace(cursor), ":")
	if len(parts) != 4 || parts[0] != cursorVersion || len(parts[1]) != 32 {
		return "", 0, 0, ErrInvalidCursor
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", 0, 0, ErrInvalidCursor
	}
	offset, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || offset < 0 {
		return "", 0, 0, ErrInvalidCursor
	}
	eventIndex, err := strconv.Atoi(parts[3])
	if err != nil || eventIndex < 0 {
		return "", 0, 0, ErrInvalidCursor
	}
	return parts[1], offset, eventIndex, nil
}

type eventEnvelope struct {
	Type      string           `json:"type"`
	Timestamp string           `json:"timestamp"`
	Subtype   string           `json:"subtype"`
	Error     string           `json:"error"`
	Result    string           `json:"result"`
	IsError   bool             `json:"is_error"`
	Origin    *sliceLineOrigin `json:"origin"`
	Message   json.RawMessage  `json:"message"`
	Payload   json.RawMessage  `json:"payload"`
	Data      json.RawMessage  `json:"data"`
}

type eventMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type eventContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type parsedEventLine struct {
	events              []Event
	dedupeAssistantEcho bool
}

func parseEventLine(agent string, line []byte) parsedEventLine {
	if len(line) == 0 {
		return parsedEventLine{}
	}
	var envelope eventEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return parsedEventLine{}
	}

	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude":
		return parsedEventLine{events: parseClaudeEvent(envelope)}
	case "copilot":
		return parsedEventLine{events: parseCopilotEvent(envelope)}
	case "codex":
		events := parseCodexEvent(envelope)
		return parsedEventLine{
			events:              events,
			dedupeAssistantEcho: envelope.Type == "response_item" && len(events) == 1 && events[0].Kind == EventKindAssistant,
		}
	default:
		return parsedEventLine{}
	}
}

func parseCodexEvent(envelope eventEnvelope) []Event {
	switch envelope.Type {
	case "event_msg":
		var payload struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			return nil
		}
		switch payload.Type {
		case "user_message":
			return textEvent(envelope.Timestamp, EventKindUser, "user", payload.Message)
		case "agent_message":
			return textEvent(envelope.Timestamp, EventKindAssistant, "assistant", payload.Message)
		}
		if strings.Contains(payload.Type, "error") || payload.Error != "" {
			return textEvent(envelope.Timestamp, EventKindError, "", firstNonEmpty(payload.Error, payload.Message))
		}
	case "response_item":
		var payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Name    string          `json:"name"`
			CallID  string          `json:"call_id"`
			Input   json.RawMessage `json:"input"`
			Output  json.RawMessage `json:"output"`
			Status  string          `json:"status"`
			Error   string          `json:"error"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			return nil
		}
		switch payload.Type {
		case "message":
			if payload.Role == "assistant" {
				return textEvent(envelope.Timestamp, EventKindAssistant, "assistant", extractEventContentText(payload.Content))
			}
		case "custom_tool_call", "function_call":
			return []Event{{
				Timestamp:  envelope.Timestamp,
				Kind:       EventKindToolCall,
				ToolName:   payload.Name,
				ToolCallID: payload.CallID,
				Text:       renderJSONValue(payload.Input),
				IsError:    strings.EqualFold(payload.Status, "failed"),
			}}
		case "custom_tool_call_output", "function_call_output":
			text := renderJSONValue(payload.Output)
			return []Event{{
				Timestamp:  envelope.Timestamp,
				Kind:       EventKindToolResult,
				ToolCallID: payload.CallID,
				Text:       text,
				IsError:    strings.EqualFold(payload.Status, "failed") || payload.Error != "" || toolFailurePattern.MatchString(text),
			}}
		}
		if strings.Contains(payload.Type, "error") || payload.Error != "" {
			return textEvent(envelope.Timestamp, EventKindError, "", payload.Error)
		}
	case "error":
		var payload struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(envelope.Payload, &payload)
		return textEvent(envelope.Timestamp, EventKindError, "", firstNonEmpty(payload.Error, payload.Message))
	}
	return nil
}

func parseClaudeEvent(envelope eventEnvelope) []Event {
	if envelope.Type != "user" && envelope.Type != "assistant" && envelope.Type != "system" && envelope.Type != "result" {
		return nil
	}

	if envelope.Type == "system" || envelope.Type == "result" {
		if envelope.IsError || strings.Contains(envelope.Subtype, "error") || envelope.Error != "" {
			return textEvent(envelope.Timestamp, EventKindError, "", firstNonEmpty(envelope.Error, envelope.Result, extractEventMessageText(envelope.Message)))
		}
		return nil
	}

	var message eventMessage
	if json.Unmarshal(envelope.Message, &message) != nil {
		return nil
	}
	blocks := decodeEventContent(message.Content)
	var events []Event
	var textParts []string
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text":
			if strings.TrimSpace(block.Text) != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			events = append(events, Event{
				Timestamp:  envelope.Timestamp,
				Kind:       EventKindToolCall,
				ToolName:   block.Name,
				ToolCallID: block.ID,
				Text:       renderJSONValue(block.Input),
			})
		case "tool_result":
			events = append(events, Event{
				Timestamp:  envelope.Timestamp,
				Kind:       EventKindToolResult,
				ToolCallID: block.ToolUseID,
				Text:       renderJSONValue(block.Content),
				IsError:    block.IsError,
			})
		}
	}

	text := strings.Join(textParts, "\n")
	if strings.TrimSpace(text) != "" {
		if envelope.Type == "assistant" {
			events = append([]Event{{Timestamp: envelope.Timestamp, Kind: EventKindAssistant, Role: "assistant", Text: text}}, events...)
		} else if envelope.Origin == nil || envelope.Origin.Kind == "human" {
			events = append([]Event{{Timestamp: envelope.Timestamp, Kind: EventKindUser, Role: "user", Text: text}}, events...)
		}
	}
	return events
}

func parseCopilotEvent(envelope eventEnvelope) []Event {
	var data struct {
		Content    string          `json:"content"`
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Arguments  json.RawMessage `json:"arguments"`
		Output     json.RawMessage `json:"output"`
		Result     json.RawMessage `json:"result"`
		Error      string          `json:"error"`
		Message    string          `json:"message"`
		Success    *bool           `json:"success"`
		Timestamp  string          `json:"timestamp"`
	}
	if json.Unmarshal(envelope.Data, &data) != nil {
		return nil
	}
	timestamp := firstNonEmpty(envelope.Timestamp, data.Timestamp)
	switch envelope.Type {
	case "user.message":
		return textEvent(timestamp, EventKindUser, "user", data.Content)
	case "assistant.message":
		return textEvent(timestamp, EventKindAssistant, "assistant", data.Content)
	case "tool.execution_start":
		return []Event{{
			Timestamp:  timestamp,
			Kind:       EventKindToolCall,
			ToolName:   data.ToolName,
			ToolCallID: data.ToolCallID,
			Text:       renderJSONValue(data.Arguments),
		}}
	case "tool.execution_complete":
		isError := data.Error != "" || (data.Success != nil && !*data.Success)
		return []Event{{
			Timestamp:  timestamp,
			Kind:       EventKindToolResult,
			ToolCallID: data.ToolCallID,
			Text:       firstNonEmpty(renderJSONValue(data.Output), renderJSONValue(data.Result), data.Error),
			IsError:    isError,
		}}
	}
	if strings.Contains(envelope.Type, "error") || data.Error != "" {
		return textEvent(timestamp, EventKindError, "", firstNonEmpty(data.Error, data.Message, data.Content))
	}
	return nil
}

func decodeEventContent(raw json.RawMessage) []eventContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []eventContentBlock{{Type: "text", Text: text}}
	}
	var blocks []eventContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	return nil
}

func extractEventMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var message eventMessage
	if json.Unmarshal(raw, &message) != nil {
		return ""
	}
	return extractEventContentText(message.Content)
}

func extractEventContentText(raw json.RawMessage) string {
	var parts []string
	for _, block := range decodeEventContent(raw) {
		if strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func textEvent(timestamp, kind, role, text string) []Event {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []Event{{Timestamp: timestamp, Kind: kind, Role: role, Text: text}}
}

func renderJSONValue(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return strings.TrimSpace(string(raw))
	}
	value = redactJSONValue(value, "")
	if text, ok := value.(string); ok {
		var nested any
		if json.Unmarshal([]byte(text), &nested) == nil {
			nested = redactJSONValue(nested, "")
			if encoded, err := json.Marshal(nested); err == nil {
				return string(encoded)
			}
		}
		return RedactText(text)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func redactEvent(event *Event) {
	event.Timestamp = strings.TrimSpace(event.Timestamp)
	event.Role = strings.TrimSpace(event.Role)
	event.ToolName = RedactText(strings.TrimSpace(event.ToolName))
	event.ToolCallID = RedactText(strings.TrimSpace(event.ToolCallID))
	event.Text = RedactText(strings.TrimSpace(event.Text))
}

// RedactText removes common credential forms from all human-readable event
// fields. Structured tool payloads are additionally redacted by key before
// rendering, while these patterns cover secrets embedded in plain output.
func RedactText(text string) string {
	if text == "" {
		return ""
	}
	text = privateKeyPattern.ReplaceAllString(text, "[REDACTED PRIVATE KEY]")
	text = bearerSecretPattern.ReplaceAllString(text, `${1}[REDACTED]`)
	text = assignmentSecretPattern.ReplaceAllString(text, `${1}[REDACTED]`)
	text = knownTokenPattern.ReplaceAllString(text, "[REDACTED]")
	text = urlCredentialPattern.ReplaceAllString(text, `${1}[REDACTED]${2}`)
	return text
}

func redactJSONValue(value any, key string) any {
	if isSensitiveKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			typed[childKey] = redactJSONValue(child, childKey)
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = redactJSONValue(typed[i], "")
		}
		return typed
	case string:
		return RedactText(typed)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(key))
	switch normalized {
	case "apikey", "accesstoken", "authtoken", "token", "password", "passwd", "secret", "authorization", "cookie", "privatekey", "clientsecret":
		return true
	default:
		return strings.HasSuffix(normalized, "token") || strings.HasSuffix(normalized, "secret") || strings.HasSuffix(normalized, "password")
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
