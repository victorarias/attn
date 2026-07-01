package agent

import (
	"strings"
	"testing"
)

func joinArgs(args []string) string {
	return strings.Join(args, "\x00")
}

func assertContainsAll(t *testing.T, label string, args []string, wants ...string) {
	t.Helper()
	joined := joinArgs(args)
	for _, want := range wants {
		if !strings.Contains(joined, want) {
			t.Fatalf("%s missing %q:\n%v", label, want, args)
		}
	}
}

func assertContainsNone(t *testing.T, label string, args []string, forbidden ...string) {
	t.Helper()
	joined := joinArgs(args)
	for _, bad := range forbidden {
		if strings.Contains(joined, bad) {
			t.Fatalf("%s unexpectedly contained %q:\n%v", label, bad, args)
		}
	}
}

func TestClaudeHeadlessArgsUsesFileToolsAndDropsMCPPin(t *testing.T) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		t.Setenv(name, "")
	}
	args := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:   "claude-test",
		Prompt:  "compact the context file",
		WorkDir: "/tmp/scratch",
	})

	// Native file tools + unprompted-write permission.
	assertContainsAll(t, "Claude native args", args,
		"--print",
		"--setting-sources",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--no-chrome",
		"--allowedTools",
		"Read,Write,Edit,Grep,Glob",
		"--permission-mode",
		"dontAsk",
		"--output-format",
		"json",
		"claude-test",
		"compact the context file",
	)
	// The MCP keeper compaction pin must be gone.
	assertContainsNone(t, "Claude native args", args,
		"--strict-mcp-config",
		"--mcp-config",
		"--tools",
		"mcp__attn_context__read_context,mcp__attn_context__replace_context",
	)
}

func TestClaudeHeadlessArgsAppendsJudgmentCapsAndSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"assessment":{"type":"string"}},"required":["assessment"]}`
	args := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:        "sonnet",
		Prompt:       "judge the transcript",
		WorkDir:      "/tmp/scratch",
		AllowedTools: []string{"Read", "Grep", "Glob"},
		MaxTurns:     15,
		MaxBudgetUSD: "0.50",
		OutputSchema: []byte(schema),
	})
	assertContainsAll(t, "Claude judgment args", args,
		"--max-turns", "15",
		"--max-budget-usd", "0.50",
		"--json-schema", schema,
		"--allowedTools", "Read,Grep,Glob",
	)

	// Unset caps must leave the argv untouched (the keeper path).
	plain := claudeHeadlessArgs(HeadlessTaskRequest{Model: "sonnet", Prompt: "compact"})
	assertContainsNone(t, "Claude uncapped args", plain,
		"--max-turns", "--max-budget-usd", "--json-schema",
	)
}

func TestClaudeHeadlessArgsHonorsAllowedToolsOverride(t *testing.T) {
	args := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:        "claude-test",
		Prompt:       "compact",
		AllowedTools: []string{"Read", "Write"},
	})
	assertContainsAll(t, "Claude native override args", args, "--allowedTools", "Read,Write")
	assertContainsNone(t, "Claude native override args", args, "Read,Write,Edit,Grep,Glob")
}

func TestCodexHeadlessArgsUsesWorkspaceWriteAndDropsMCPPin(t *testing.T) {
	args := codexHeadlessArgs(HeadlessTaskRequest{
		Model:   "gpt-test",
		Prompt:  "compact the context file",
		WorkDir: "/tmp/scratch",
	})

	assertContainsAll(t, "Codex native args", args,
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox",
		"workspace-write",
		`approval_policy="never"`,
		// non-file feature locks stay.
		"features.apps=false",
		"features.browser_use=false",
		"features.standalone_web_search=false",
		"gpt-test",
		"compact the context file",
	)
	// Read-only sandbox, file/exec disables, and the MCP pin must all be gone.
	assertContainsNone(t, "Codex native args", args,
		"read-only",
		"features.shell_tool=false",
		"features.unified_exec=false",
		"mcp_servers.attn_context.command",
		"mcp_servers.attn_context.required=true",
		`mcp_servers.attn_context.enabled_tools=["read_context","replace_context"]`,
		`mcp_servers.attn_context.default_tools_approval_mode="approve"`,
	)
}
