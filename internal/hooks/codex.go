package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// GenerateCodexConfigOverrides returns Codex CLI -c overrides required for
// attn-managed terminals without mutating user or project Codex config.
// attn has always owned the resize-reflow value for its embedded renderer:
// xterm needed it disabled, while Ghostty correctly renders the enabled redraw.
func GenerateCodexConfigOverrides(sessionID, socketPath, wrapperPath, workspaceContextPath, notebookRoot string, injectWorkflow bool) []string {
	wrapper := strings.TrimSpace(wrapperPath)
	if wrapper == "" {
		wrapper = "attn"
	}

	command := func(args ...string) string {
		parts := []string{shellQuote(wrapper)}
		for _, arg := range args {
			parts = append(parts, shellQuote(arg))
		}
		return strings.Join(parts, " ")
	}

	hook := func(command string) string {
		return fmt.Sprintf(`{ type = "command", command = %s, timeout = 5 }`, strconv.Quote(command))
	}
	group := func(matcher string, command string) string {
		if strings.TrimSpace(matcher) == "" {
			return fmt.Sprintf(`[{ hooks = [%s] }]`, hook(command))
		}
		return fmt.Sprintf(`[{ matcher = %s, hooks = [%s] }]`, strconv.Quote(matcher), hook(command))
	}

	sessionStart := command("_hook-session-start")
	userPromptSubmit := command("_hook-state", "working")
	permissionRequest := command("_hook-state", "pending_approval")
	preToolUse := command("_hook-state", "working")
	postToolUse := command("_hook-state", "working")
	stop := command("_hook-stop")

	overrides := []string{
		// Codex's shell environment policy is applied independently for each tool
		// working directory. Values inherited by the top-level Codex process can
		// therefore disappear when an agent runs a tool from a child worktree.
		// Pin attn's routing identity in the per-launch policy so hooks and agent
		// commands keep targeting this session regardless of tool cwd.
		"shell_environment_policy.set.ATTN_SESSION_ID=" + strconv.Quote(strings.TrimSpace(sessionID)),
		"shell_environment_policy.set.ATTN_WRAPPER_PATH=" + strconv.Quote(wrapper),
		"features.hooks=true",
		// Ghostty renders Codex's SIGWINCH redraw correctly, and enabling
		// reflow prevents resized inline UIs from leaving stale headers.
		"features.terminal_resize_reflow=true",
		trustedHashOverrides([]codexHookTrustEntry{
			{eventKey: "session_start", matcher: "startup|resume|clear|compact", command: sessionStart},
			{eventKey: "user_prompt_submit", command: userPromptSubmit},
			{eventKey: "permission_request", matcher: "*", command: permissionRequest},
			{eventKey: "pre_tool_use", matcher: "*", command: preToolUse},
			{eventKey: "post_tool_use", matcher: "*", command: postToolUse},
			{eventKey: "stop", command: stop},
		}),
		"hooks.SessionStart=" + group("startup|resume|clear|compact", sessionStart),
		"hooks.UserPromptSubmit=" + group("", userPromptSubmit),
		"hooks.PermissionRequest=" + group("*", permissionRequest),
		"hooks.PreToolUse=" + group("*", preToolUse),
		"hooks.PostToolUse=" + group("*", postToolUse),
		"hooks.Stop=" + group("", stop),
	}
	if socket := strings.TrimSpace(socketPath); socket != "" {
		overrides = append(overrides,
			"shell_environment_policy.set.ATTN_SOCKET_PATH="+strconv.Quote(socket),
		)
	}
	// A chief-of-staff launch (notebookRoot set) gets chief guidance instead
	// of the workspace-context checkout guidance. Every other workspace agent gets
	// its workspace-context guidance (plus workflow-trigger guidance when enabled,
	// folded in by AgentInstructions). Non-chief agents are NOT nudged to journal:
	// the keeper narrates each workspace's own work into the journal, and the chief
	// journals the cross-workspace layer.
	if guidance := ChiefGuidance(notebookRoot); guidance != "" {
		overrides = append(overrides, "developer_instructions="+strconv.Quote(guidance))
	} else if instructions := AgentInstructions(workspaceContextPath, injectWorkflow); instructions != "" {
		overrides = append(overrides, "developer_instructions="+strconv.Quote(instructions))
	}
	return overrides
}

type codexHookTrustEntry struct {
	eventKey string
	matcher  string
	command  string
}

func trustedHashOverrides(entries []codexHookTrustEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		key := fmt.Sprintf("/<session-flags>/config.toml:%s:0:0", entry.eventKey)
		parts = append(parts, fmt.Sprintf(
			"%s = { trusted_hash = %s }",
			strconv.Quote(key),
			strconv.Quote(commandHookHash(entry.eventKey, entry.matcher, entry.command)),
		))
	}
	return fmt.Sprintf("hooks.state={ %s }", strings.Join(parts, ", "))
}

func commandHookHash(eventKey, matcher, command string) string {
	group := map[string]any{
		"event_name": eventKey,
		"hooks": []any{
			map[string]any{
				"async":   false,
				"command": command,
				"timeout": 5,
				"type":    "command",
			},
		},
	}
	if normalized := normalizedMatcher(eventKey, matcher); normalized != "" {
		group["matcher"] = normalized
	}

	serialized, err := json.Marshal(group)
	if err != nil {
		panic(fmt.Sprintf("failed to hash Codex hook identity: %v", err))
	}
	sum := sha256.Sum256(serialized)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedMatcher(eventKey, matcher string) string {
	switch eventKey {
	case "user_prompt_submit", "stop":
		return ""
	default:
		return strings.TrimSpace(matcher)
	}
}
