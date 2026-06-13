package agent

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildCommand_YoloMapping(t *testing.T) {
	tests := []struct {
		name     string
		driver   Driver
		wantFlag string
	}{
		{name: "claude", driver: &Claude{}, wantFlag: "--dangerously-skip-permissions"},
		{name: "codex", driver: &Codex{}, wantFlag: "--dangerously-bypass-approvals-and-sandbox"},
		{name: "copilot", driver: &Copilot{}, wantFlag: "--yolo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.driver.BuildCommand(SpawnOpts{
				SessionID:  "sess-1",
				CWD:        "/tmp/project",
				Executable: tt.driver.DefaultExecutable(),
				YoloMode:   true,
			})
			found := false
			for _, arg := range cmd.Args {
				if arg == tt.wantFlag {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s args = %#v, want flag %q", tt.name, cmd.Args, tt.wantFlag)
			}
		})
	}
}

func TestCodexBuildCommand_IncludesConfigOverridesBeforeResume(t *testing.T) {
	cmd := (&Codex{}).BuildCommand(SpawnOpts{
		CWD:             "/tmp/project",
		Executable:      "codex",
		ResumeSessionID: "codex-session",
		ConfigOverrides: []string{"features.hooks=true"},
	})
	args := strings.Join(cmd.Args, "\x00")
	wantArgs := []string{"codex", "-c", "features.hooks=true", "resume", "codex-session", "-C", "/tmp/project"}
	want := strings.Join(wantArgs, "\x00")
	if args != want {
		t.Fatalf("args = %#v, want %#v", cmd.Args, wantArgs)
	}
}

func TestCodexBuildEnvMarksDeveloperInstructionGuidance(t *testing.T) {
	env := (&Codex{}).BuildEnv(SpawnOpts{
		SessionID:            "sess-1",
		WorkspaceContextPath: "/tmp/context.md",
	})
	if !slices.Contains(env, "ATTN_WORKSPACE_CONTEXT_GUIDANCE=developer_instructions") {
		t.Fatalf("env = %#v, want developer instruction guidance marker", env)
	}
}

func TestClaudeBuildCommand_AppendsWorkspaceContextSystemPrompt(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:            "sess-1",
		Executable:           "claude",
		WorkspaceContextPath: "/tmp/context.md",
	})
	flagIndex := slices.Index(cmd.Args, "--append-system-prompt")
	if flagIndex == -1 || flagIndex+1 >= len(cmd.Args) {
		t.Fatalf("args = %#v, want --append-system-prompt with guidance", cmd.Args)
	}
	if !strings.Contains(cmd.Args[flagIndex+1], "/tmp/context.md") {
		t.Fatalf("system prompt = %q, want workspace context path", cmd.Args[flagIndex+1])
	}
}

func TestClaudeBuildEnvMarksAppendSystemPromptGuidance(t *testing.T) {
	env := (&Claude{}).BuildEnv(SpawnOpts{
		WorkspaceContextPath: "/tmp/context.md",
	})
	if !slices.Contains(env, "ATTN_WORKSPACE_CONTEXT_GUIDANCE=append_system_prompt") {
		t.Fatalf("env = %#v, want append system prompt guidance marker", env)
	}
}

// A chief launch (NotebookRoot set) injects Notebook guidance and suppresses the
// workspace-context guidance, even if a checkout path is also present.
func TestClaudeBuildCommand_NotebookGuidanceTakesPrecedence(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:            "sess-1",
		Executable:           "claude",
		WorkspaceContextPath: "/tmp/context.md",
		NotebookRoot:         "/home/u/attn-notebook",
	})
	flagIndex := slices.Index(cmd.Args, "--append-system-prompt")
	if flagIndex == -1 || flagIndex+1 >= len(cmd.Args) {
		t.Fatalf("args = %#v, want --append-system-prompt with notebook guidance", cmd.Args)
	}
	prompt := cmd.Args[flagIndex+1]
	if !strings.Contains(prompt, "/home/u/attn-notebook") || !strings.Contains(prompt, "chief-of-staff role") {
		t.Fatalf("system prompt = %q, want notebook guidance", prompt)
	}
	if strings.Contains(prompt, "/tmp/context.md") {
		t.Fatalf("chief launch must not inject workspace-context guidance: %q", prompt)
	}
}

func TestClaudeBuildEnvMarksNotebookGuidance(t *testing.T) {
	env := (&Claude{}).BuildEnv(SpawnOpts{
		WorkspaceContextPath: "/tmp/context.md",
		NotebookRoot:         "/home/u/attn-notebook",
	})
	if !slices.Contains(env, "ATTN_NOTEBOOK_GUIDANCE=append_system_prompt") {
		t.Fatalf("env = %#v, want notebook guidance marker", env)
	}
	if slices.Contains(env, "ATTN_WORKSPACE_CONTEXT_GUIDANCE=append_system_prompt") {
		t.Fatalf("env = %#v, chief launch should not also mark workspace context guidance", env)
	}
}

func TestCodexConfigOverrides_NotebookGuidanceTakesPrecedence(t *testing.T) {
	overrides := (&Codex{}).GenerateConfigOverrides(SpawnOpts{
		SessionID:            "sess-1",
		WorkspaceContextPath: "/tmp/context.md",
		NotebookRoot:         "/home/u/attn-notebook",
	})
	joined := strings.Join(overrides, "\n")
	if !strings.Contains(joined, "developer_instructions=") {
		t.Fatal("codex overrides should set developer_instructions for a chief launch")
	}
	if !strings.Contains(joined, "attn-notebook") || !strings.Contains(joined, "chief-of-staff role") {
		t.Fatalf("developer_instructions should carry notebook guidance: %q", joined)
	}
	if strings.Contains(joined, "/tmp/context.md") {
		t.Fatalf("chief launch must not inject workspace-context guidance: %q", joined)
	}
}

func TestCodexBuildEnvMarksNotebookGuidance(t *testing.T) {
	env := (&Codex{}).BuildEnv(SpawnOpts{
		SessionID:    "sess-1",
		NotebookRoot: "/home/u/attn-notebook",
	})
	if !slices.Contains(env, "ATTN_NOTEBOOK_GUIDANCE=developer_instructions") {
		t.Fatalf("env = %#v, want notebook guidance marker", env)
	}
}

func TestBuildCommand_AppendsInitialPromptAfterOptionTerminator(t *testing.T) {
	for _, driver := range []Driver{&Claude{}, &Codex{}} {
		t.Run(driver.Name(), func(t *testing.T) {
			cmd := driver.BuildCommand(SpawnOpts{
				SessionID:     "sess-1",
				CWD:           "/tmp/project",
				Executable:    driver.DefaultExecutable(),
				InitialPrompt: "--help is text, not a flag",
			})
			got := cmd.Args[len(cmd.Args)-2:]
			if got[0] != "--" || got[1] != "--help is text, not a flag" {
				t.Fatalf("trailing args = %#v, want option terminator and initial prompt; args=%#v", got, cmd.Args)
			}
		})
	}
}

func TestCopilotDoesNotSupportInitialPrompt(t *testing.T) {
	if (&Copilot{}).Capabilities().HasInitialPrompt {
		t.Fatal("expected Copilot delegation support to remain disabled")
	}
}
