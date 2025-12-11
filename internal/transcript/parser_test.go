package transcript

import (
	"os"
	"path/filepath"
	"testing"
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
