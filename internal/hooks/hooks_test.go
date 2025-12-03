package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateHooks(t *testing.T) {
	sessionID := "abc123"
	socketPath := "/home/user/.claude-manager.sock"

	hooks := Generate(sessionID, socketPath)

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(hooks), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Check hooks exist
	hooksArray, ok := parsed["hooks"].([]interface{})
	if !ok {
		t.Fatal("hooks field not found or not array")
	}

	// Should have multiple hooks
	if len(hooksArray) < 3 {
		t.Errorf("expected at least 3 hooks, got %d", len(hooksArray))
	}

	// Verify hook structure
	for _, h := range hooksArray {
		hook := h.(map[string]interface{})
		if _, ok := hook["matcher"]; !ok {
			t.Error("hook missing matcher")
		}
		if _, ok := hook["hooks"]; !ok {
			t.Error("hook missing hooks array")
		}
	}
}

func TestGenerateHooks_ContainsSessionID(t *testing.T) {
	sessionID := "unique-session-id-12345"
	socketPath := "/tmp/test.sock"

	hooks := Generate(sessionID, socketPath)

	if !strings.Contains(hooks, sessionID) {
		t.Error("generated hooks should contain session ID")
	}
}

func TestGenerateHooks_ContainsSocketPath(t *testing.T) {
	sessionID := "test"
	socketPath := "/custom/path/to/socket.sock"

	hooks := Generate(sessionID, socketPath)

	if !strings.Contains(hooks, socketPath) {
		t.Error("generated hooks should contain socket path")
	}
}

func TestGenerateHooks_HasStopHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock")

	if !strings.Contains(hooks, "Stop") {
		t.Error("hooks should include Stop event for waiting state")
	}
}

func TestGenerateHooks_HasUserPromptSubmitHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock")

	if !strings.Contains(hooks, "UserPromptSubmit") {
		t.Error("hooks should include UserPromptSubmit event for working state")
	}
}
