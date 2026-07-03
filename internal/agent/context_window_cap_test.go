package agent

import (
	"slices"
	"strings"
	"testing"
)

// envHasCap reports whether env carries any CLAUDE_CODE_AUTO_COMPACT_WINDOW entry.
func envHasCap(env []string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=") {
			return true
		}
	}
	return false
}

// --- Chief (interactive) cap: BuildEnv / BuildCommand emission ---

// The chief cap rides SpawnOpts.AutoCompactWindow into the launch. Claude emits
// the CLAUDE_CODE_AUTO_COMPACT_WINDOW env var; codex emits the
// model_auto_compact_token_limit config override. Both gate on the chief branch
// (NotebookRoot set) so delegated interactive agents are never capped.
func TestClaudeBuildEnv_ChiefContextWindowCap(t *testing.T) {
	t.Run("chief launch emits the cap", func(t *testing.T) {
		env := (&Claude{}).BuildEnv(SpawnOpts{NotebookRoot: "/nb", AutoCompactWindow: 200000})
		if !slices.Contains(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000") {
			t.Fatalf("chief env missing the cap: %#v", env)
		}
	})

	t.Run("delegated launch is never capped", func(t *testing.T) {
		// A delegated interactive agent carries WorkspaceContextPath, not
		// NotebookRoot. Even if AutoCompactWindow is somehow set, it must not leak.
		env := (&Claude{}).BuildEnv(SpawnOpts{WorkspaceContextPath: "/ws", AutoCompactWindow: 200000})
		if envHasCap(env) {
			t.Fatalf("delegated env unexpectedly carried the cap: %#v", env)
		}
	})

	t.Run("chief launch with no cap emits nothing", func(t *testing.T) {
		env := (&Claude{}).BuildEnv(SpawnOpts{NotebookRoot: "/nb", AutoCompactWindow: 0})
		if envHasCap(env) {
			t.Fatalf("uncapped chief env unexpectedly carried the cap: %#v", env)
		}
	})
}

func TestCodexBuildCommand_ChiefContextWindowCap(t *testing.T) {
	t.Run("chief launch emits the cap override", func(t *testing.T) {
		cmd := (&Codex{}).BuildCommand(SpawnOpts{
			SessionID:         "sess-1",
			CWD:               "/tmp/project",
			Executable:        "codex",
			NotebookRoot:      "/nb",
			AutoCompactWindow: 200000,
		})
		if !argvHasPair(cmd.Args, "-c", "model_auto_compact_token_limit=200000") {
			t.Fatalf("chief codex args missing the cap override: %#v", cmd.Args)
		}
	})

	t.Run("delegated launch is never capped", func(t *testing.T) {
		cmd := (&Codex{}).BuildCommand(SpawnOpts{
			SessionID:            "sess-1",
			CWD:                  "/tmp/project",
			Executable:           "codex",
			WorkspaceContextPath: "/ws",
			AutoCompactWindow:    200000,
		})
		for _, arg := range cmd.Args {
			if arg == "model_auto_compact_token_limit=200000" {
				t.Fatalf("delegated codex args unexpectedly carried the cap: %#v", cmd.Args)
			}
		}
	})
}

// --- Headless cap: process-global injected at the spawn seam ---

func TestSetHeadlessContextWindowCap_ClampsNegative(t *testing.T) {
	t.Cleanup(func() { SetHeadlessContextWindowCap(0) })
	SetHeadlessContextWindowCap(-5)
	if got := HeadlessContextWindowCap(); got != 0 {
		t.Fatalf("negative cap not clamped: got %d, want 0", got)
	}
	SetHeadlessContextWindowCap(150000)
	if got := HeadlessContextWindowCap(); got != 150000 {
		t.Fatalf("cap not stored: got %d, want 150000", got)
	}
}

func TestHeadlessEnvironment_ClaudeContextWindowCap(t *testing.T) {
	t.Cleanup(func() { SetHeadlessContextWindowCap(0) })

	SetHeadlessContextWindowCap(150000)
	claudeEnv := headlessEnvironment("claude")
	if !slices.Contains(claudeEnv, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=150000") {
		t.Fatalf("claude headless env missing the cap: %#v", claudeEnv)
	}
	// The cap is a Claude env var; codex must not receive it (it uses a config
	// override applied in the arg builders instead).
	if envHasCap(headlessEnvironment("codex")) {
		t.Fatalf("codex headless env unexpectedly carried the Claude cap")
	}

	SetHeadlessContextWindowCap(0)
	if envHasCap(headlessEnvironment("claude")) {
		t.Fatalf("uncapped headless env unexpectedly carried the cap")
	}
}

func TestCodexHeadlessArgs_ContextWindowCap(t *testing.T) {
	t.Run("native path emits the override when capped", func(t *testing.T) {
		args := codexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "narrate"}, 150000)
		if !argvHasPair(args, "-c", "model_auto_compact_token_limit=150000") {
			t.Fatalf("native codex args missing the cap override: %#v", args)
		}
	})

	t.Run("MCP path emits the override when capped", func(t *testing.T) {
		args := buildCodexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "judge"}, "", 150000)
		if !argvHasPair(args, "-c", "model_auto_compact_token_limit=150000") {
			t.Fatalf("MCP codex args missing the cap override: %#v", args)
		}
	})

	t.Run("uncapped emits nothing", func(t *testing.T) {
		for _, args := range [][]string{
			codexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "p"}, 0),
			buildCodexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "p"}, "", 0),
		} {
			for _, arg := range args {
				if arg == "model_auto_compact_token_limit=0" || arg == "model_auto_compact_token_limit=" {
					t.Fatalf("uncapped codex args unexpectedly carried a cap override: %#v", args)
				}
			}
		}
	})
}
