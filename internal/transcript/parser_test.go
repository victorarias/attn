package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractLastAssistantMessage(t *testing.T) {
	content := `{"type":"user","message":{"content":"Hello"}}
{"type":"assistant","message":{"content":"Hi there! How can I help you today?"}}
{"type":"user","message":{"content":"Fix the bug"}}
{"type":"assistant","message":{"content":"I've fixed the bug. The issue was in the validation logic. All tests are now passing!"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "I've fixed the bug. The issue was in the validation logic. All tests are now passing!"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_Truncates(t *testing.T) {
	longMsg := ""
	for i := 0; i < 100; i++ {
		longMsg += "Hello world! "
	}

	content := `{"type":"assistant","message":{"content":"` + longMsg + `"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) > 50 {
		t.Errorf("result length %d exceeds limit 50", len(result))
	}
}

func TestExtractLastAssistantMessage_NoAssistant(t *testing.T) {
	content := `{"type":"user","message":{"content":"Hello"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

// Tests for Claude Code's actual transcript format (content is an array)
func TestExtractLastAssistantMessage_ClaudeCodeFormat(t *testing.T) {
	// Claude Code uses content as an array of blocks with type and text fields
	content := `{"type":"user","message":{"role":"user","content":"test"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me think..."},{"type":"text","text":"How would you like to proceed with the video description feature? I need your input on next steps."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "How would you like to proceed with the video description feature? I need your input on next steps."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_ClaudeCodeFormat_MultipleTextBlocks(t *testing.T) {
	// Claude might have multiple text blocks in a single message
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"First paragraph."},{"type":"text","text":"Second paragraph."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "First paragraph.\nSecond paragraph."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_ClaudeCodeFormat_OnlyThinking(t *testing.T) {
	// If a message only has thinking blocks, we should get empty string
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Just thinking here..."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only thinking block, no text, should return empty
	if result != "" {
		t.Errorf("expected empty string for thinking-only message, got %q", result)
	}
}

func TestExtractLastAssistantMessage_MessageRoleAsAssistant(t *testing.T) {
	// Test detection by message.role instead of type
	content := `{"message":{"role":"assistant","content":[{"type":"text","text":"Hello from assistant"}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Hello from assistant"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_CodexEventMessage(t *testing.T) {
	content := `{"type":"event_msg","payload":{"type":"user_message","message":"Hi"}}
{"type":"event_msg","payload":{"type":"agent_message","message":"Codex reply one."}}
{"type":"event_msg","payload":{"type":"agent_message","message":"Codex reply two."}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Codex reply two."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_CodexResponseItemOutputText(t *testing.T) {
	content := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"First output."}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Second output."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Second output."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_CopilotEvents(t *testing.T) {
	content := `{"type":"session.start","data":{"sessionId":"abc"}}
{"type":"assistant.message","data":{"content":"Copilot reply one."}}
{"type":"assistant.message","data":{"content":"Copilot final reply with question?"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "events.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Copilot final reply with question?"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractCopilotToolLifecycle_Start(t *testing.T) {
	line := []byte(`{"type":"tool.execution_start","data":{"toolCallId":"tool-123","toolName":"bash"}}`)
	got, ok := ExtractCopilotToolLifecycle(line)
	if !ok {
		t.Fatal("expected lifecycle event")
	}
	if got.Kind != "start" {
		t.Fatalf("kind = %q, want %q", got.Kind, "start")
	}
	if got.ToolCallID != "tool-123" {
		t.Fatalf("toolCallId = %q, want %q", got.ToolCallID, "tool-123")
	}
	if got.ToolName != "bash" {
		t.Fatalf("toolName = %q, want %q", got.ToolName, "bash")
	}
}

func TestExtractCopilotToolLifecycle_Complete(t *testing.T) {
	line := []byte(`{"type":"tool.execution_complete","data":{"toolCallId":"tool-123","success":true}}`)
	got, ok := ExtractCopilotToolLifecycle(line)
	if !ok {
		t.Fatal("expected lifecycle event")
	}
	if got.Kind != "complete" {
		t.Fatalf("kind = %q, want %q", got.Kind, "complete")
	}
	if got.ToolCallID != "tool-123" {
		t.Fatalf("toolCallId = %q, want %q", got.ToolCallID, "tool-123")
	}
	if got.ToolName != "" {
		t.Fatalf("toolName = %q, want empty", got.ToolName)
	}
}

func TestExtractCopilotToolLifecycle_IgnoresNonLifecycle(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"type":"assistant.message","data":{"content":"hello"}}`),
		[]byte(`{"type":"tool.execution_start","data":{"toolName":"bash"}}`),
		[]byte(`not-json`),
	}
	for _, line := range cases {
		if _, ok := ExtractCopilotToolLifecycle(line); ok {
			t.Fatalf("expected no lifecycle for line: %s", string(line))
		}
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Claude_StaleAssistantIgnored(t *testing.T) {
	content := `{"type":"user","message":{"content":"hello"}}
{"type":"assistant","message":{"content":"Hi! How can I help?"}}
{"type":"user","message":{"content":"show me what this bot does"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Claude_AssistantAfterLatestUser(t *testing.T) {
	content := `{"type":"user","message":{"content":"hello"}}
{"type":"assistant","message":{"content":"Hi! How can I help?"}}
{"type":"user","message":{"content":"show me what this bot does"}}
{"type":"assistant","message":{"content":"This bot tracks sessions and attention."}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "This bot tracks sessions and attention."
	if result != expected {
		t.Fatalf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Claude_NoUserStillReturnsLastAssistant(t *testing.T) {
	content := `{"type":"assistant","message":{"content":"Hello! What can I help you with today?"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "Hello! What can I help you with today?"
	if result != expected {
		t.Fatalf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Copilot_StaleAssistantIgnored(t *testing.T) {
	content := `{"type":"user.message","data":{"content":"hello"}}
{"type":"assistant.message","data":{"content":"Hi there!"}}
{"type":"user.message","data":{"content":"run this command"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "events.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
}

func TestExtractLastAssistantMessageAfterLastUserSince_Claude_StaleByTimestampIgnored(t *testing.T) {
	content := `{"type":"user","timestamp":"2026-02-11T23:57:15.222Z","message":{"role":"user","content":"hhello!"}}
{"type":"assistant","timestamp":"2026-02-11T23:57:18.631Z","message":{"role":"assistant","content":[{"type":"text","text":"Hey! How can I help you today?"}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	minTS := time.Date(2026, 2, 11, 23, 57, 30, 0, time.UTC)
	result, err := ExtractLastAssistantMessageAfterLastUserSince(path, 500, minTS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result for stale assistant, got %q", result)
	}
}

func TestExtractLastAssistantMessageAfterLastUserSince_Claude_FreshAssistantReturned(t *testing.T) {
	content := `{"type":"user","timestamp":"2026-02-11T23:57:35.472Z","message":{"role":"user","content":"i don tknow"}}
{"type":"assistant","timestamp":"2026-02-11T23:57:38.427Z","message":{"role":"assistant","content":[{"type":"text","text":"No worries! Whenever something comes up, I'm here."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	minTS := time.Date(2026, 2, 11, 23, 57, 30, 0, time.UTC)
	result, err := ExtractLastAssistantMessageAfterLastUserSince(path, 500, minTS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "No worries! Whenever something comes up, I'm here."
	if result != expected {
		t.Fatalf("got %q, want %q", result, expected)
	}
}
