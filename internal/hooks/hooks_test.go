package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateHooks(t *testing.T) {
	sessionID := "abc123"
	socketPath := "/home/user/.claude-manager.sock"

	settings := Generate(sessionID, socketPath)

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Check hooks exist as a map
	hooksMap, ok := parsed["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks field not found or not a map")
	}

	// Should have 3 event types
	if len(hooksMap) < 3 {
		t.Errorf("expected at least 3 event types, got %d", len(hooksMap))
	}

	// Verify each event type has hook entries
	for eventType, entries := range hooksMap {
		entriesArray, ok := entries.([]interface{})
		if !ok {
			t.Errorf("event %s: expected array of entries", eventType)
			continue
		}
		for _, entry := range entriesArray {
			hook := entry.(map[string]interface{})
			if _, ok := hook["matcher"]; !ok {
				t.Errorf("event %s: hook missing matcher", eventType)
			}
			if _, ok := hook["hooks"]; !ok {
				t.Errorf("event %s: hook missing hooks array", eventType)
			}
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
