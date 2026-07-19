package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEventTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendEventTranscript(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadEventPageCodexNormalizesAndRedacts(t *testing.T) {
	path := writeEventTranscript(t,
		`{"timestamp":"2026-07-19T10:00:00Z","type":"session_meta","payload":{"id":"native-id"}}`,
		`{"timestamp":"2026-07-19T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"deploy with token=super-secret-value"}}`,
		`{"timestamp":"2026-07-19T10:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"checking now"}}`,
		`{"timestamp":"2026-07-19T10:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checking now"}]}}`,
		`{"timestamp":"2026-07-19T10:00:03Z","type":"response_item","payload":{"type":"custom_tool_call","name":"shell","call_id":"call-1","input":"{\"cmd\":\"curl\",\"api_key\":\"hidden-value\"}"}}`,
		`{"timestamp":"2026-07-19T10:00:04Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_text","text":"Authorization: Bearer abcdefghijklmnop"}]}}`,
		`{"timestamp":"2026-07-19T10:00:05Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-2","output":"Process exited with code 2"}}`,
	)

	page, err := ReadEventPage(path, "codex", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !page.AtEnd {
		t.Fatal("expected complete page to be at end")
	}
	if len(page.Events) != 5 {
		t.Fatalf("events = %#v, want 5 provider-neutral events", page.Events)
	}
	if page.Events[0].Kind != EventKindUser || page.Events[0].Timestamp != "2026-07-19T10:00:01Z" {
		t.Fatalf("user event = %#v", page.Events[0])
	}
	if strings.Contains(page.Events[0].Text, "super-secret-value") || !strings.Contains(page.Events[0].Text, "[REDACTED]") {
		t.Fatalf("user secret was not redacted: %q", page.Events[0].Text)
	}
	if page.Events[1].Kind != EventKindAssistant || page.Events[1].Text != "checking now" {
		t.Fatalf("assistant event = %#v", page.Events[1])
	}
	if page.Events[2].Kind != EventKindToolCall || page.Events[2].ToolName != "shell" || strings.Contains(page.Events[2].Text, "hidden-value") {
		t.Fatalf("tool call = %#v", page.Events[2])
	}
	if page.Events[3].Kind != EventKindToolResult || page.Events[3].ToolCallID != "call-1" || strings.Contains(page.Events[3].Text, "abcdefghijklmnop") {
		t.Fatalf("tool result = %#v", page.Events[3])
	}
	if page.Events[4].Kind != EventKindToolResult || !page.Events[4].IsError {
		t.Fatalf("failed tool result = %#v", page.Events[4])
	}
	for _, event := range page.Events {
		if event.Cursor == "" {
			t.Fatalf("event missing cursor: %#v", event)
		}
	}
}

func TestReadEventPageCodexResponseItemAssistantOnly(t *testing.T) {
	path := writeEventTranscript(t,
		`{"timestamp":"2026-07-19T10:00:00Z","type":"session_meta","payload":{"id":"native-id"}}`,
		`{"timestamp":"2026-07-19T10:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"First output."},{"type":"output_text","text":"Second output."}]}}`,
	)

	page, err := ReadEventPage(path, "codex", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events = %#v, want one assistant event", page.Events)
	}
	event := page.Events[0]
	if event.Kind != EventKindAssistant || event.Role != "assistant" || event.Text != "First output.\nSecond output." {
		t.Fatalf("assistant event = %#v", event)
	}
}

func TestReadEventPageCodexDeduplicatesPairedAssistantAcrossFollowPoll(t *testing.T) {
	path := writeEventTranscript(t,
		`{"timestamp":"2026-07-19T10:00:00Z","type":"session_meta","payload":{"id":"native-id"}}`,
		`{"timestamp":"2026-07-19T10:00:01Z","type":"event_msg","payload":{"type":"agent_message","message":"checking now"}}`,
	)

	first, err := ReadEventPage(path, "codex", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].Text != "checking now" || !first.AtEnd {
		t.Fatalf("first poll = %#v", first)
	}

	appendEventTranscript(t, path,
		`{"timestamp":"2026-07-19T10:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checking now"}]}}`,
	)

	second, err := ReadEventPage(path, "codex", first.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 0 || !second.AtEnd {
		t.Fatalf("paired response_item was not suppressed: %#v", second)
	}
}

func TestReadEventPageCursorResumesWithoutDuplicates(t *testing.T) {
	path := writeEventTranscript(t,
		`{"type":"session_meta","payload":{"id":"native-id"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"one"}}`,
		`{"type":"event_msg","payload":{"type":"token_count"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"two"}}`,
	)

	first, err := ReadEventPage(path, "codex", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].Text != "one" || first.AtEnd {
		t.Fatalf("first page = %#v", first)
	}

	second, err := ReadEventPage(path, "codex", first.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 1 || second.Events[0].Text != "two" || !second.AtEnd {
		t.Fatalf("second page = %#v", second)
	}
	if first.Events[0].Cursor == second.Events[0].Cursor {
		t.Fatal("resumed event reused the prior source cursor")
	}
}

func TestReadEventPageCursorResumesWithinMultiEventRecord(t *testing.T) {
	path := writeEventTranscript(t,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working"},{"type":"tool_use","id":"tool-1","name":"Read","input":{"file_path":"/tmp/x"}}]}}`,
	)
	first, err := ReadEventPage(path, "claude", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].Kind != EventKindAssistant {
		t.Fatalf("first page = %#v", first)
	}
	second, err := ReadEventPage(path, "claude", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 1 || second.Events[0].Kind != EventKindToolCall {
		t.Fatalf("second page = %#v", second)
	}
	third, err := ReadEventPage(path, "claude", second.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Events) != 0 || !third.AtEnd {
		t.Fatalf("third page = %#v", third)
	}
}

func TestReadEventPageWaitsForCompleteFinalRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	prefix := "{\"type\":\"session_meta\"}\n"
	partial := `{"type":"event_msg","payload":{"type":"agent_message","message":"later"}}`
	if err := os.WriteFile(path, []byte(prefix+partial), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := ReadEventPage(path, "codex", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 0 {
		t.Fatalf("partial record emitted events: %#v", first.Events)
	}
	if err := os.WriteFile(path, []byte(prefix+partial+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := ReadEventPage(path, "codex", first.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 1 || second.Events[0].Text != "later" {
		t.Fatalf("completed record = %#v", second.Events)
	}
}

func TestReadEventPageRejectsCursorFromAnotherTranscript(t *testing.T) {
	firstPath := writeEventTranscript(t,
		`{"type":"session_meta","payload":{"id":"one"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"one"}}`,
	)
	secondPath := writeEventTranscript(t,
		`{"type":"session_meta","payload":{"id":"two"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"two"}}`,
	)
	first, err := ReadEventPage(firstPath, "codex", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReadEventPage(secondPath, "codex", first.NextCursor, 1)
	if !errors.Is(err, ErrCursorMismatch) {
		t.Fatalf("error = %v, want ErrCursorMismatch", err)
	}
}

func TestReadEventPageClaudeAndCopilot(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		path := writeEventTranscript(t,
			`{"timestamp":"2026-07-19T11:00:00Z","type":"user","origin":{"kind":"human"},"message":{"content":[{"type":"text","text":"please inspect"}]}}`,
			`{"timestamp":"2026-07-19T11:00:01Z","type":"assistant","message":{"content":[{"type":"text","text":"on it"},{"type":"tool_use","id":"tool-1","name":"Read","input":{"file_path":"/tmp/x"}}]}}`,
			`{"timestamp":"2026-07-19T11:00:02Z","type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"done","is_error":false}]}}`,
			`{"timestamp":"2026-07-19T11:00:03Z","type":"result","is_error":true,"result":"runtime failed"}`,
		)
		page, err := ReadEventPage(path, "claude", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		wantKinds := []string{EventKindUser, EventKindAssistant, EventKindToolCall, EventKindToolResult, EventKindError}
		if len(page.Events) != len(wantKinds) {
			t.Fatalf("events = %#v", page.Events)
		}
		for i, kind := range wantKinds {
			if page.Events[i].Kind != kind {
				t.Fatalf("event %d kind = %q, want %q", i, page.Events[i].Kind, kind)
			}
		}
	})

	t.Run("copilot", func(t *testing.T) {
		path := writeEventTranscript(t,
			`{"timestamp":"2026-07-19T12:00:00Z","type":"user.message","data":{"content":"please inspect"}}`,
			`{"timestamp":"2026-07-19T12:00:01Z","type":"tool.execution_start","data":{"toolCallId":"tool-1","toolName":"bash","arguments":{"cmd":"pwd"}}}`,
			`{"timestamp":"2026-07-19T12:00:02Z","type":"tool.execution_complete","data":{"toolCallId":"tool-1","success":false,"error":"exit 1"}}`,
		)
		page, err := ReadEventPage(path, "copilot", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) != 3 || page.Events[2].Kind != EventKindToolResult || !page.Events[2].IsError {
			t.Fatalf("events = %#v", page.Events)
		}
	})
}

func TestRedactTextCoversCredentialForms(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer abc.def.ghi",
		"password=hunter2",
		"github_pat_abcdefghijklmnopqrstuvwxyz",
		"https://user:password@example.com/repo.git",
		"-----BEGIN PRIVATE KEY-----\nprivate material\n-----END PRIVATE KEY-----",
	}, "\n")
	got := RedactText(input)
	for _, secret := range []string{"abc.def.ghi", "hunter2", "github_pat_abcdefghijklmnopqrstuvwxyz", "password@example.com", "private material"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q remains in %q", secret, got)
		}
	}
}
