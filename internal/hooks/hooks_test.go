package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateHooks(t *testing.T) {
	sessionID := "abc123"
	socketPath := "/home/user/.claude-manager.sock"

	settings := Generate(sessionID, socketPath, "/tmp/attn")

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

	hooks := Generate(sessionID, socketPath, "/tmp/attn")

	if !strings.Contains(hooks, sessionID) {
		t.Error("generated hooks should contain session ID")
	}
}

func TestGenerateHooks_ContainsSocketPath(t *testing.T) {
	sessionID := "test"
	socketPath := "/custom/path/to/socket.sock"

	hooks := Generate(sessionID, socketPath, "/tmp/attn")

	if !strings.Contains(hooks, socketPath) {
		t.Error("generated hooks should contain socket path")
	}
}

func TestGenerateHooks_HasStopHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/tmp/attn")

	if !strings.Contains(hooks, "Stop") {
		t.Error("hooks should include Stop event for waiting state")
	}
}

func TestGenerateHooks_HasSessionStartHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/tmp/attn")

	for _, expected := range []string{
		"SessionStart",
		"startup|resume|clear|compact",
		"_hook-session-start",
	} {
		if !strings.Contains(hooks, expected) {
			t.Fatalf("Claude hooks should include %q", expected)
		}
	}
}

func TestGenerateHooks_HasUserPromptSubmitHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/tmp/attn")

	if !strings.Contains(hooks, "UserPromptSubmit") {
		t.Error("hooks should include UserPromptSubmit event for working state")
	}
}

func TestGenerateHooks_UsesWrapperPath(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/Users/testuser/Applications/attn.app/Contents/MacOS/attn")

	if !strings.Contains(hooks, "'/Users/testuser/Applications/attn.app/Contents/MacOS/attn' _hook-stop") {
		t.Error("hooks should include wrapper path in stop hook command")
	}
}

func TestGenerateHooks_DefaultsWrapperToAttn(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "")

	if !strings.Contains(hooks, "'attn' _hook-stop") {
		t.Error("hooks should default wrapper path to 'attn'")
	}
}

func TestGenerateCodexConfigOverrides_UsesStableEnvBasedCommands(t *testing.T) {
	overrides := GenerateCodexConfigOverrides("session-1", "/tmp/attn.sock", "/tmp/attn", "/tmp/context.md", "")
	joined := strings.Join(overrides, "\n")

	if !strings.Contains(joined, "hooks.SessionStart=") {
		t.Fatal("codex overrides should include SessionStart hook")
	}
	if !strings.Contains(joined, "_hook-session-start") {
		t.Fatal("codex overrides should sync session id on start")
	}
	if !strings.Contains(joined, "startup|resume|clear|compact") {
		t.Fatal("codex SessionStart hook should run after context resets")
	}
	if strings.Contains(joined, "session-1") {
		t.Fatal("codex hook commands should not embed per-session attn ids")
	}
	if strings.Contains(joined, "/tmp/attn.sock") {
		t.Fatal("codex hook commands should not embed per-session socket paths")
	}
	if !strings.Contains(joined, "features.hooks=true") {
		t.Fatal("codex overrides should enable hooks for attn-managed sessions")
	}
	if !strings.Contains(joined, "features.terminal_resize_reflow=true") {
		t.Fatal("codex overrides should enable terminal resize reflow for embedded sessions")
	}
	if !strings.Contains(joined, "hooks.PreToolUse=") {
		t.Fatal("codex overrides should include PreToolUse hook")
	}
	if !strings.Contains(joined, `"/<session-flags>/config.toml:pre_tool_use:0:0" = { trusted_hash =`) {
		t.Fatal("codex overrides should trust attn-managed PreToolUse hook")
	}
	if !strings.Contains(joined, `hooks.state={`) ||
		!strings.Contains(joined, `"/<session-flags>/config.toml:session_start:0:0" = { trusted_hash =`) {
		t.Fatal("codex overrides should trust attn-managed session flag hooks")
	}
	if !strings.Contains(joined, "developer_instructions=") ||
		!strings.Contains(joined, "/tmp/context.md") {
		t.Fatal("codex overrides should inject workspace context as developer instructions")
	}
}

func TestWorkspaceContextGuidance(t *testing.T) {
	guidance := WorkspaceContextGuidance("/tmp/context.md")
	for _, expected := range []string{
		"/tmp/context.md",
		"potentially stale coordination context",
		"System, developer, user, and repository instructions take precedence",
		"area map, not a single-task brief",
		"Area and Current Picture authoritative",
		"optional semantic Threads",
		"sourced Timeline turning points",
		"Do not infer dates, chronology, causality, ownership, or thread structure",
		"attn handles occasional broad compaction",
		"Avoid duplication, transcripts, raw command output",
		"load the attn skill's workspace-context reference",
		"status, update, and conflict workflow",
		"Do not pass --session",
	} {
		if !strings.Contains(guidance, expected) {
			t.Fatalf("guidance missing %q: %q", expected, guidance)
		}
	}
	for _, unwanted := range []string{
		"live checkout",
		"show --force",
		"mktemp",
		"workspace context update",
	} {
		if strings.Contains(guidance, unwanted) {
			t.Fatalf("guidance should leave procedural detail to the skill reference: found %q in %q", unwanted, guidance)
		}
	}
	if strings.Contains(guidance, "# Shared goal") {
		t.Fatal("guidance should not embed workspace context content")
	}
}

func TestWorkspaceContextSessionStartOutputWrapsGuidance(t *testing.T) {
	raw := WorkspaceContextSessionStartOutput("/tmp/context.md")
	var output sessionStartHookOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		t.Fatalf("WorkspaceContextSessionStartOutput returned invalid JSON: %v", err)
	}
	if output.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hook event = %q", output.HookSpecificOutput.HookEventName)
	}
	// Non-chief agents are NOT nudged to journal: the SessionStart fallback carries
	// only the workspace-context guidance, with no journaling directive appended.
	want := WorkspaceContextGuidance("/tmp/context.md")
	if output.HookSpecificOutput.AdditionalContext != want {
		t.Fatal("hook output should carry only the workspace context guidance")
	}
}

func TestNotebookGuidance(t *testing.T) {
	guidance := NotebookGuidance("/home/u/attn-notebook")
	for _, expected := range []string{
		"/home/u/attn-notebook",
		"chief of staff",
		"attn notebook show /memory/index.md",
		"attn notebook journal append",
		"attn notebook memory write",
		"--base-hash",
		"sources:",                              // grounding rule
		"paraphrase",                            // grounding rule
		"root-absolute",                         // linking convention
		"attn workspace context show --session", // opt-in workspace read
		"load the attn skill's notebook reference",
	} {
		if !strings.Contains(guidance, expected) {
			t.Fatalf("notebook guidance missing %q: %q", expected, guidance)
		}
	}
	// Wikilinks are explicitly not the convention.
	if strings.Contains(guidance, "[[") {
		t.Fatalf("notebook guidance should not suggest wikilinks: %q", guidance)
	}
}

func TestNotebookGuidanceEmptyWithoutRoot(t *testing.T) {
	if got := NotebookGuidance(""); got != "" {
		t.Fatalf("NotebookGuidance(\"\") = %q, want empty", got)
	}
	if got := NotebookGuidance("   "); got != "" {
		t.Fatalf("NotebookGuidance(whitespace) = %q, want empty", got)
	}
}

// NOTE: AskUserQuestion PostToolUse hook was removed because it fires
// AFTER the user responds, not when the question is displayed.
// See: https://github.com/anthropics/claude-code/issues/10168
