package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHeadlessArgsRecorder(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args.log")
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellSingleQuote(logPath) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	return scriptPath, logPath
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestCodexRunHeadlessTaskScopesToolsAndConfiguration(t *testing.T) {
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Codex{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable:       executable,
		Model:            "gpt-test",
		Prompt:           "compact",
		WorkDir:          t.TempDir(),
		MCPServerName:    "attn_context",
		MCPServerCommand: "/tmp/attn",
		MCPServerArgs:    []string{"_workspace-context-janitor-mcp", "--source-file", "/tmp/source"},
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	for _, want := range []string{
		"exec",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"read-only",
		`approval_policy="never"`,
		"features.shell_tool=false",
		"features.unified_exec=false",
		"features.apps=false",
		"features.hooks=false",
		"features.plugins=false",
		"features.browser_use=false",
		"features.memories=false",
		"features.multi_agent=false",
		"features.standalone_web_search=false",
		"mcp_servers.attn_context.command=\"/tmp/attn\"",
		"mcp_servers.attn_context.required=true",
		`mcp_servers.attn_context.enabled_tools=["read_context","replace_context"]`,
		`mcp_servers.attn_context.default_tools_approval_mode="approve"`,
		"gpt-test",
		"compact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Codex args missing %q:\n%s", want, got)
		}
	}
}

func TestClaudeRunHeadlessTaskUsesSafeModeWithoutExplicitAuthentication(t *testing.T) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		t.Setenv(name, "")
	}
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Claude{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable:       executable,
		Model:            "claude-test",
		Prompt:           "compact",
		WorkDir:          t.TempDir(),
		MCPServerName:    "attn_context",
		MCPServerCommand: "/tmp/attn",
		MCPServerArgs:    []string{"_workspace-context-janitor-mcp", "--candidate-file", "/tmp/candidate"},
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	for _, want := range []string{
		"--print",
		"--safe-mode",
		"--no-session-persistence",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-chrome",
		"--tools",
		"mcp__attn_context__read_context,mcp__attn_context__replace_context",
		"--allowedTools",
		"mcp__attn_context__read_context,mcp__attn_context__replace_context",
		"claude-test",
		"compact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Claude args missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--bare") {
		t.Fatalf("Claude safe-mode args unexpectedly contained --bare:\n%s", got)
	}
}

func TestClaudeRunHeadlessTaskUsesBareModeWithExplicitAuthentication(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Claude{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable:       executable,
		Model:            "claude-test",
		Prompt:           "compact",
		WorkDir:          t.TempDir(),
		MCPServerName:    "attn_context",
		MCPServerCommand: "/tmp/attn",
		MCPServerArgs:    []string{"_workspace-context-janitor-mcp"},
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	if !strings.Contains(got, "--bare") {
		t.Fatalf("Claude explicit-auth args missing --bare:\n%s", got)
	}
	if strings.Contains(got, "--safe-mode") {
		t.Fatalf("Claude explicit-auth args unexpectedly contained --safe-mode:\n%s", got)
	}
}

func TestRunHeadlessCommandUsesMinimalEnvironmentAndDiscardsOutput(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "env.log")
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\nenv > " + shellSingleQuote(logPath) + "\nprintf 'workspace context secret\\n'\nprintf 'stderr secret\\n' >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	t.Setenv("ATTN_SESSION_ID", "session-secret")
	t.Setenv("CODEX_THREAD_ID", "thread-secret")
	t.Setenv("UNRELATED_SECRET", "secret")
	t.Setenv("ANTHROPIC_API_KEY", "auth-kept")
	t.Setenv("OPENAI_API_KEY", "other-provider-secret")

	result, err := runHeadlessCommand(context.Background(), scriptPath, nil, dir, "claude")
	if err != nil {
		t.Fatalf("runHeadlessCommand error: %v", err)
	}
	if result.Diagnostics != "" {
		t.Fatalf("diagnostics = %q, want empty", result.Diagnostics)
	}
	envBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read environment: %v", err)
	}
	env := string(envBytes)
	for _, forbidden := range []string{"ATTN_SESSION_ID=", "CODEX_THREAD_ID=", "UNRELATED_SECRET="} {
		if strings.Contains(env, forbidden) {
			t.Fatalf("headless environment retained %s:\n%s", forbidden, env)
		}
	}
	if !strings.Contains(env, "ANTHROPIC_API_KEY=auth-kept") {
		t.Fatalf("headless environment dropped provider authentication:\n%s", env)
	}
	if !strings.Contains(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
		t.Fatalf("Claude environment did not disable auto-memory:\n%s", env)
	}
	if strings.Contains(env, "OPENAI_API_KEY=") {
		t.Fatalf("Claude environment retained Codex provider authentication:\n%s", env)
	}
}

func TestRunHeadlessCommandClassifiesFailureWithoutLeakingOutput(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\nprintf 'authentication_failed workspace context secret\\n'\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	result, err := runHeadlessCommand(context.Background(), scriptPath, nil, dir, "claude")
	if err == nil {
		t.Fatal("runHeadlessCommand unexpectedly succeeded")
	}
	if result.Diagnostics != "headless agent authentication failed" {
		t.Fatalf("diagnostics = %q", result.Diagnostics)
	}
	if strings.Contains(err.Error(), "workspace context secret") {
		t.Fatalf("error leaked child output: %v", err)
	}
}

func TestClaudeHeadlessTaskAvailabilitySupportsSafeModeAuthentication(t *testing.T) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		t.Setenv(name, "")
	}
	if available, reason := (&Claude{}).HeadlessTaskAvailability(); !available || reason != "" {
		t.Fatalf("availability = %t, reason = %q", available, reason)
	}
	if got := claudeHeadlessIsolationFlag(); got != "--safe-mode" {
		t.Fatalf("isolation flag = %q, want --safe-mode", got)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	if got := claudeHeadlessIsolationFlag(); got != "--bare" {
		t.Fatalf("isolation flag = %q, want --bare", got)
	}
}
