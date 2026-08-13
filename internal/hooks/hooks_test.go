package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateHooks(t *testing.T) {
	sessionID := "abc123"
	socketPath := "/home/user/.claude-manager.sock"

	settings := Generate(sessionID, socketPath, "/tmp/attn", nil)

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

// The catch-all PostToolUse hook is the one that fires on every tool call, so
// it stays a single spawn: _hook-tool-use resets the working state AND records
// any markdown the call wrote, instead of a second hook entry doing the latter.
func TestGenerateHooks_CatchAllPostToolUseIsOneSpawn(t *testing.T) {
	settings := Generate("abc123", "/tmp/test.sock", "/tmp/attn", nil)

	var parsed SettingsConfig
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var catchAll []Hook
	for _, entry := range parsed.Hooks["PostToolUse"] {
		if entry.Matcher == "*" {
			catchAll = entry.Hooks
		}
	}
	if len(catchAll) != 1 {
		t.Fatalf("catch-all PostToolUse has %d commands, want exactly 1", len(catchAll))
	}
	if !strings.Contains(catchAll[0].Command, `_hook-tool-use "abc123"`) {
		t.Errorf("catch-all PostToolUse command = %q, want _hook-tool-use", catchAll[0].Command)
	}
}

func TestGenerateCodexConfigOverrides_PostToolUseRecordsEdits(t *testing.T) {
	overrides := strings.Join(GenerateCodexConfigOverrides("abc123", "/tmp/test.sock", "/tmp/attn", Launch{}), "\n")

	if !strings.Contains(overrides, "hooks.PostToolUse=[{ matcher = \"*\", hooks = [{ type = \"command\", command = \"'/tmp/attn' '_hook-tool-use'\"") {
		t.Fatalf("codex PostToolUse should run _hook-tool-use: %q", overrides)
	}
	// Codex only runs a hook whose recorded hash matches, so the trust entry
	// has to move with the command.
	if !strings.Contains(overrides, "post_tool_use") {
		t.Fatalf("codex overrides should trust the post_tool_use hook: %q", overrides)
	}
}

func TestGenerateHooks_ContainsSessionID(t *testing.T) {
	sessionID := "unique-session-id-12345"
	socketPath := "/tmp/test.sock"

	hooks := Generate(sessionID, socketPath, "/tmp/attn", nil)

	if !strings.Contains(hooks, sessionID) {
		t.Error("generated hooks should contain session ID")
	}
}

func TestGenerateHooks_ContainsSocketPath(t *testing.T) {
	sessionID := "test"
	socketPath := "/custom/path/to/socket.sock"

	hooks := Generate(sessionID, socketPath, "/tmp/attn", nil)

	if !strings.Contains(hooks, socketPath) {
		t.Error("generated hooks should contain socket path")
	}
}

func TestGenerateHooks_HasStopHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/tmp/attn", nil)

	if !strings.Contains(hooks, "Stop") {
		t.Error("hooks should include Stop event for waiting state")
	}
}

func TestGenerateHooks_HasSessionStartHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/tmp/attn", nil)

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

// The Notification hook is the only harness-owned signal that says the agent is
// blocked on the user *and* says why. Losing the registration would leave that
// evidence silently absent rather than failing anything.
func TestGenerateHooks_HasNotificationHook(t *testing.T) {
	var parsed SettingsConfig
	if err := json.Unmarshal([]byte(Generate("abc123", "/tmp/test.sock", "/tmp/attn", nil)), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entries := parsed.Hooks["Notification"]
	if len(entries) != 1 || len(entries[0].Hooks) != 1 {
		t.Fatalf("want one Notification hook, got %+v", entries)
	}
	if cmd := entries[0].Hooks[0].Command; !strings.Contains(cmd, `_hook-notification "abc123"`) {
		t.Fatalf("Notification command = %q, want _hook-notification for the session", cmd)
	}
}

func TestGenerateHooks_HasUserPromptSubmitHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/tmp/attn", nil)

	if !strings.Contains(hooks, "UserPromptSubmit") {
		t.Error("hooks should include UserPromptSubmit event for working state")
	}
}

func TestGenerateHooks_UsesWrapperPath(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/Users/testuser/Applications/attn.app/Contents/MacOS/attn", nil)

	if !strings.Contains(hooks, "'/Users/testuser/Applications/attn.app/Contents/MacOS/attn' _hook-stop") {
		t.Error("hooks should include wrapper path in stop hook command")
	}
}

func TestGenerateHooks_DefaultsWrapperToAttn(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "", nil)

	if !strings.Contains(hooks, "'attn' _hook-stop") {
		t.Error("hooks should default wrapper path to 'attn'")
	}
}

func TestGenerateHooks_EnvBlock(t *testing.T) {
	decode := func(t *testing.T, content string) map[string]any {
		t.Helper()
		var parsed map[string]any
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		return parsed
	}

	t.Run("carries the supplied env", func(t *testing.T) {
		parsed := decode(t, Generate("test", "/tmp/test.sock", "/tmp/attn", map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "128000"}))
		env, ok := parsed["env"].(map[string]any)
		if !ok {
			t.Fatalf("env block missing: %#v", parsed)
		}
		if got := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "128000" {
			t.Fatalf("env cap = %v, want \"128000\"", got)
		}
	})

	t.Run("omits an empty env", func(t *testing.T) {
		if _, present := decode(t, Generate("test", "/tmp/test.sock", "/tmp/attn", nil))["env"]; present {
			t.Error("nil env should not emit an env block")
		}
		if _, present := decode(t, Generate("test", "/tmp/test.sock", "/tmp/attn", map[string]string{}))["env"]; present {
			t.Error("empty env should not emit an env block")
		}
	})
}

func TestGenerateCodexConfigOverrides_UsesStableEnvBasedCommands(t *testing.T) {
	overrides := GenerateCodexConfigOverrides("session-1", "/tmp/attn.sock", "/tmp/attn", Launch{WorkspaceContextPath: "/tmp/context.md"})
	joined := strings.Join(overrides, "\n")
	for _, expected := range []string{
		`shell_environment_policy.set.ATTN_SESSION_ID="session-1"`,
		`shell_environment_policy.set.ATTN_WRAPPER_PATH="/tmp/attn"`,
		`shell_environment_policy.set.ATTN_SOCKET_PATH="/tmp/attn.sock"`,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("codex overrides missing stable tool environment %q: %q", expected, joined)
		}
	}

	if !strings.Contains(joined, "hooks.SessionStart=") {
		t.Fatal("codex overrides should include SessionStart hook")
	}
	if !strings.Contains(joined, "_hook-session-start") {
		t.Fatal("codex overrides should sync session id on start")
	}
	if !strings.Contains(joined, "startup|resume|clear|compact") {
		t.Fatal("codex SessionStart hook should run after context resets")
	}
	if strings.Contains(joined, "_hook-session-start session-1") ||
		strings.Contains(joined, "_hook-state working session-1") {
		t.Fatal("codex hook commands should read identity from their stable environment")
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

func TestGenerateCodexConfigOverrides_OmitsEmptySocketButKeepsSessionIdentity(t *testing.T) {
	overrides := strings.Join(GenerateCodexConfigOverrides("session-2", "", "", Launch{}), "\n")
	if !strings.Contains(overrides, `shell_environment_policy.set.ATTN_SESSION_ID="session-2"`) ||
		!strings.Contains(overrides, `shell_environment_policy.set.ATTN_WRAPPER_PATH="attn"`) {
		t.Fatalf("codex overrides dropped required attn identity: %q", overrides)
	}
	if strings.Contains(overrides, "ATTN_SOCKET_PATH") {
		t.Fatalf("codex overrides should omit an empty socket path: %q", overrides)
	}
}

func TestAgentInstructionsComposition(t *testing.T) {
	workflow := WorkflowTriggerGuidance()
	context := WorkspaceContextGuidance("/tmp/context.md")
	ticket := TicketAwarenessGuidance()

	// The ticket-awareness pointer is always-on: even with no checkout and no
	// workflow guidance, AgentInstructions returns exactly the ticket block.
	if got := AgentInstructions("", false); got != ticket {
		t.Fatalf("AgentInstructions(empty, false) = %q, want the ticket block %q", got, ticket)
	}

	// Workspace context, then the always-on ticket block.
	contextOnly := AgentInstructions("/tmp/context.md", false)
	if want := strings.Join([]string{context, ticket}, "\n\n"); contextOnly != want {
		t.Fatalf("context-only instructions = %q, want %q", contextOnly, want)
	}
	if strings.Contains(contextOnly, "hypercode") {
		t.Fatalf("context-only instructions leaked workflow guidance: %q", contextOnly)
	}

	// Workflow guidance (no checkout), then the always-on ticket block.
	workflowOnly := AgentInstructions("", true)
	if want := strings.Join([]string{workflow, ticket}, "\n\n"); workflowOnly != want {
		t.Fatalf("workflow-only instructions = %q, want %q", workflowOnly, want)
	}

	// Both, joined with a blank line, context first, ticket block last.
	both := AgentInstructions("/tmp/context.md", true)
	if want := strings.Join([]string{context, workflow, ticket}, "\n\n"); both != want {
		t.Fatalf("combined instructions = %q, want %q", both, want)
	}
}

func TestGenerateCodexConfigOverrides_InjectsWorkflowGuidanceWhenEnabled(t *testing.T) {
	off := strings.Join(GenerateCodexConfigOverrides("s", "/sock", "/attn", Launch{WorkspaceContextPath: "/tmp/context.md"}), "\n")
	if strings.Contains(off, "hypercode") {
		t.Fatalf("workflow guidance injected while disabled: %q", off)
	}

	on := strings.Join(GenerateCodexConfigOverrides("s", "/sock", "/attn", Launch{WorkspaceContextPath: "/tmp/context.md", InjectWorkflow: true}), "\n")
	if !strings.Contains(on, "developer_instructions=") {
		t.Fatal("enabled overrides dropped developer_instructions")
	}
	if !strings.Contains(on, "hypercode") {
		t.Fatalf("enabled overrides missing workflow guidance: %q", on)
	}
	// The workspace context still rides alongside the workflow guidance.
	if !strings.Contains(on, "/tmp/context.md") {
		t.Fatalf("enabled overrides dropped the workspace context: %q", on)
	}

	// Workflow guidance is injected even without a workspace checkout.
	noCtx := strings.Join(GenerateCodexConfigOverrides("s", "/sock", "/attn", Launch{InjectWorkflow: true}), "\n")
	if !strings.Contains(noCtx, "developer_instructions=") || !strings.Contains(noCtx, "hypercode") {
		t.Fatalf("workflow guidance not injected without a checkout: %q", noCtx)
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

func TestChiefGuidanceEmptyWithoutRoot(t *testing.T) {
	if got := ChiefGuidance("", true); got != "" {
		t.Fatalf("ChiefGuidance(\"\") = %q, want empty", got)
	}
	if got := ChiefGuidance("   ", false); got != "" {
		t.Fatalf("ChiefGuidance(whitespace) = %q, want empty", got)
	}
}

// NOTE: AskUserQuestion PostToolUse hook was removed because it fires
// AFTER the user responds, not when the question is displayed.
// See: https://github.com/anthropics/claude-code/issues/10168

// The primer is what makes an attn-launched agent a gardener: it arrives
// knowing the vocabulary, the loop, and whether there is anything to pick up.
func TestGardenPrimer(t *testing.T) {
	// No garden reachable — an outpost, or a daemon that could not answer.
	// Telling an agent to run a command that refuses is worse than silence.
	if got := GardenPrimer(nil); got != "" {
		t.Fatalf("GardenPrimer(nil) = %q, want nothing", got)
	}

	counts := map[int]string{
		0: "Nothing was ready",
		1: "One seed was ready",
		4: "4 seeds were ready",
	}
	for count, want := range counts {
		primer := GardenPrimer(&count)
		if !strings.Contains(primer, want) {
			t.Fatalf("primer for %d does not say %q:\n%s", count, want, primer)
		}
		// The loop, and where the live answer is: the count is a starting
		// position, composed once at launch.
		for _, phrase := range []string{"attn seed ready", "attn seed tend", "attn seed harvest", "live answer"} {
			if !strings.Contains(primer, phrase) {
				t.Fatalf("primer does not name %q:\n%s", phrase, primer)
			}
		}
	}
}

// Every attn-launched agent lives in the same garden, so the primer rides with
// chief guidance and workspace guidance alike — and never replaces either.
func TestLaunchInstructionsCarryTheGardenPrimer(t *testing.T) {
	count := 2

	workspace := Launch{WorkspaceContextPath: "/tmp/context.md", GardenReady: &count}.Instructions()
	if want := AgentInstructions("/tmp/context.md", false); !strings.HasPrefix(workspace, want) {
		t.Fatalf("workspace launch dropped the agent instructions:\n%s", workspace)
	}
	if !strings.Contains(workspace, "2 seeds were ready") {
		t.Fatalf("workspace launch dropped the primer:\n%s", workspace)
	}

	chief := Launch{NotebookRoot: "/tmp/notebook", GardenReady: &count}.Instructions()
	if want := ChiefGuidance("/tmp/notebook", false); !strings.HasPrefix(chief, want) {
		t.Fatalf("chief launch dropped the chief guidance:\n%s", chief)
	}
	if !strings.Contains(chief, "2 seeds were ready") {
		t.Fatalf("chief launch dropped the primer:\n%s", chief)
	}

	// Nothing to prime with leaves the rest exactly as it was.
	bare := Launch{WorkspaceContextPath: "/tmp/context.md"}.Instructions()
	if bare != AgentInstructions("/tmp/context.md", false) {
		t.Fatalf("a garden-less launch changed the instructions:\n%s", bare)
	}
}
