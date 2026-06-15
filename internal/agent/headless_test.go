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
		Executable: executable,
		Model:      "gpt-test",
		Prompt:     "compact",
		WorkDir:    t.TempDir(),
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
		"--skip-git-repo-check",
		"workspace-write",
		`approval_policy="never"`,
		"features.apps=false",
		"features.hooks=false",
		"features.plugins=false",
		"features.browser_use=false",
		"features.memories=false",
		"features.multi_agent=false",
		"features.standalone_web_search=false",
		"gpt-test",
		"compact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Codex args missing %q:\n%s", want, got)
		}
	}
	// The constrained-MCP read-only sandbox and read_context/replace_context pin
	// must be gone: native mode gives Codex its own file tools.
	for _, forbidden := range []string{
		"read-only",
		"features.shell_tool=false",
		"features.unified_exec=false",
		"mcp_servers.attn_context",
		"read_context",
		"replace_context",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("Codex args unexpectedly contained %q:\n%s", forbidden, got)
		}
	}
}

func TestClaudeRunHeadlessTaskExcludesNonManagedSettingsWithoutExplicitAuthentication(t *testing.T) {
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
		Executable: executable,
		Model:      "claude-test",
		Prompt:     "compact",
		WorkDir:    t.TempDir(),
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
		"--setting-sources",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--no-chrome",
		"--allowedTools",
		"Read,Write,Edit,Grep,Glob",
		"--permission-mode",
		"dontAsk",
		"claude-test",
		"compact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Claude args missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "--setting-sources\n\n--model") {
		t.Fatalf("Claude args did not pass an empty setting source list:\n%s", got)
	}
	// The constrained-MCP pin must be gone in native mode.
	for _, forbidden := range []string{
		"--strict-mcp-config",
		"--mcp-config",
		"--tools",
		"mcp__attn_context__read_context,mcp__attn_context__replace_context",
		"--bare",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("Claude managed-auth args unexpectedly contained %q:\n%s", forbidden, got)
		}
	}
}

func TestClaudeRunHeadlessTaskUsesBareModeWithExplicitAuthentication(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Claude{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable: executable,
		Model:      "claude-test",
		Prompt:     "compact",
		WorkDir:    t.TempDir(),
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

func TestClaudeHeadlessTaskAvailabilitySupportsManagedAuthentication(t *testing.T) {
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
	if got := claudeHeadlessIsolationArgs(); len(got) != 2 || got[0] != "--setting-sources" || got[1] != "" {
		t.Fatalf("isolation args = %#v, want empty --setting-sources", got)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	if got := claudeHeadlessIsolationArgs(); len(got) != 1 || got[0] != "--bare" {
		t.Fatalf("isolation args = %#v, want --bare", got)
	}
}

// TestCodexHeadlessArgsWidensWritableRootsAdditively proves the notebook narrator's
// ExtraWritableRoots map to `--add-dir <root>` entries (so the workspace-write
// sandbox also permits writes under the notebook root), placed AFTER the base
// sandbox args and BEFORE the prompt, without disturbing the feature locks. The
// janitor's empty ExtraWritableRoots must add no --add-dir (regression guard).
func TestCodexHeadlessArgsWidensWritableRootsAdditively(t *testing.T) {
	t.Run("narration widens", func(t *testing.T) {
		args := codexHeadlessArgs(HeadlessTaskRequest{
			Model:              "gpt-test",
			Prompt:             "narrate",
			ExtraWritableRoots: []string{"/notebook/root", "  ", "/notebook/raw"},
		})
		joined := strings.Join(args, "\x00")
		if !strings.Contains(joined, "--add-dir\x00/notebook/root") {
			t.Fatalf("missing --add-dir for notebook root:\n%v", args)
		}
		if !strings.Contains(joined, "--add-dir\x00/notebook/raw") {
			t.Fatalf("missing --add-dir for raw root:\n%v", args)
		}
		// The blank entry is skipped.
		if strings.Count(joined, "--add-dir") != 2 {
			t.Fatalf("expected exactly 2 --add-dir entries, got:\n%v", args)
		}
		// Still the base sandbox + feature locks + prompt.
		for _, want := range []string{"workspace-write", "features.apps=false", "narrate"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing base arg %q:\n%v", want, args)
			}
		}
		// --add-dir must precede the feature locks and the prompt.
		addDirIdx := strings.Index(joined, "--add-dir")
		lockIdx := strings.Index(joined, "features.apps=false")
		promptIdx := strings.LastIndex(joined, "narrate")
		if !(addDirIdx < lockIdx && lockIdx < promptIdx) {
			t.Fatalf("arg ordering wrong (add-dir=%d lock=%d prompt=%d):\n%v", addDirIdx, lockIdx, promptIdx, args)
		}
	})

	t.Run("janitor adds nothing", func(t *testing.T) {
		args := codexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "compact"})
		if strings.Contains(strings.Join(args, "\x00"), "--add-dir") {
			t.Fatalf("janitor (no ExtraWritableRoots) unexpectedly added --add-dir:\n%v", args)
		}
	})
}

// TestClaudeHeadlessArgsIgnoreWritableRoots proves Claude never gains an --add-dir
// (or any sandbox-widening flag) from ExtraWritableRoots: dontAsk is not
// filesystem-sandboxed, so the field is a no-op for Claude. The allow-list and the
// model/prompt are unchanged whether or not the roots are present.
func TestClaudeHeadlessArgsIgnoreWritableRoots(t *testing.T) {
	withRoots := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:              "claude-test",
		Prompt:             "narrate",
		AllowedTools:       []string{"Read", "Write", "Edit", "Grep", "Glob", "Bash"},
		ExtraWritableRoots: []string{"/notebook/root"},
	})
	joined := strings.Join(withRoots, "\x00")
	if strings.Contains(joined, "--add-dir") || strings.Contains(joined, "/notebook/root") {
		t.Fatalf("Claude args leaked ExtraWritableRoots:\n%v", withRoots)
	}
	if !strings.Contains(joined, "Read,Write,Edit,Grep,Glob,Bash") {
		t.Fatalf("Claude args dropped the explicit Bash-inclusive allow-list:\n%v", withRoots)
	}
	for _, want := range []string{"--permission-mode", "dontAsk", "claude-test", "narrate"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Claude args missing %q:\n%v", want, withRoots)
		}
	}
}
