package hooks

// SPIKE — throwaway. Emits the codex `-c` overrides needed to wire an arbitrary
// logger script as codex's hook set, reusing the production trusted-hash
// computation so codex actually accepts them. Used to give the state-detection
// spike hook-derived ground truth for codex, the same way --settings does for
// claude.
//
//   go test ./internal/hooks -run TestSpikePrintCodexOverrides -v \
//     -args-unused   ATTN_SPIKE_HOOKLOG=/path/hooklog.sh

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestSpikePrintCodexOverrides(t *testing.T) {
	logger := strings.TrimSpace(os.Getenv("ATTN_SPIKE_HOOKLOG"))
	if logger == "" {
		t.Skip("set ATTN_SPIKE_HOOKLOG to the hook logger script")
	}

	// Same shape as GenerateCodexConfigOverrides, but each event invokes the
	// logger with its own name so the capture can tell them apart.
	events := []struct {
		key     string
		matcher string
		name    string
	}{
		{"session_start", "startup|resume|clear|compact", "SessionStart"},
		{"user_prompt_submit", "", "UserPromptSubmit"},
		{"permission_request", "*", "PermissionRequest"},
		{"pre_tool_use", "*", "PreToolUse"},
		{"post_tool_use", "*", "PostToolUse"},
		{"stop", "", "Stop"},
	}

	hook := func(command string) string {
		return fmt.Sprintf(`{ type = "command", command = %s, timeout = 5 }`, strconv.Quote(command))
	}
	group := func(matcher, command string) string {
		if strings.TrimSpace(matcher) == "" {
			return fmt.Sprintf(`[{ hooks = [%s] }]`, hook(command))
		}
		return fmt.Sprintf(`[{ matcher = %s, hooks = [%s] }]`, strconv.Quote(matcher), hook(command))
	}

	var trust []string
	var out []string
	out = append(out, "features.hooks=true")
	for _, e := range events {
		command := shellQuote(logger) + " " + shellQuote(e.name)
		key := fmt.Sprintf("/<session-flags>/config.toml:%s:0:0", e.key)
		trust = append(trust, fmt.Sprintf("%s = { trusted_hash = %s }",
			strconv.Quote(key),
			strconv.Quote(commandHookHash(e.key, e.matcher, command))))

		tomlKey := map[string]string{
			"session_start":      "SessionStart",
			"user_prompt_submit": "UserPromptSubmit",
			"permission_request": "PermissionRequest",
			"pre_tool_use":       "PreToolUse",
			"post_tool_use":      "PostToolUse",
			"stop":               "Stop",
		}[e.key]
		out = append(out, "hooks."+tomlKey+"="+group(e.matcher, command))
	}
	out = append(out, fmt.Sprintf("hooks.state={ %s }", strings.Join(trust, ", ")))

	// One override per line, for the capture script to split on.
	for _, o := range out {
		fmt.Println("OVERRIDE\t" + o)
	}
}
