package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
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

func TestClaudeBuildCommand_AppendsWorkspaceContextSystemPrompt(t *testing.T) {
	opts := SpawnOpts{
		SessionID:            "sess-1",
		Executable:           "claude",
		WorkspaceContextPath: "/tmp/context.md",
	}
	cmd := (&Claude{}).BuildCommand(opts)
	flagIndex := slices.Index(cmd.Args, "--append-system-prompt")
	if flagIndex == -1 || flagIndex+1 >= len(cmd.Args) {
		t.Fatalf("args = %#v, want --append-system-prompt with guidance", cmd.Args)
	}
	// A non-chief workspace agent gets ONLY its workspace-context guidance as the
	// system-prompt value — no journaling directive is appended. Pin the exact value
	// so a re-introduced directive (composed or as a separate arg) is caught.
	want := hooks.WorkspaceContextGuidance("/tmp/context.md")
	if cmd.Args[flagIndex+1] != want {
		t.Fatalf("system prompt = %q, want exactly the workspace-context guidance", cmd.Args[flagIndex+1])
	}
	// The same launch sets the suppression marker, so the SessionStart fallback does
	// not re-emit the guidance on resume/compact.
	if !slices.Contains((&Claude{}).BuildEnv(opts), "ATTN_WORKSPACE_CONTEXT_GUIDANCE=append_system_prompt") {
		t.Fatal("non-chief launch must set ATTN_WORKSPACE_CONTEXT_GUIDANCE so the SessionStart fallback is suppressed")
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
	if !strings.Contains(prompt, "/home/u/attn-notebook") || !strings.Contains(prompt, "chief of staff") {
		t.Fatalf("system prompt = %q, want notebook guidance", prompt)
	}
	if strings.Contains(prompt, "/tmp/context.md") {
		t.Fatalf("chief launch must not inject workspace-context guidance: %q", prompt)
	}
	// The chief's fuller guidance already covers journaling; it must not also carry
	// the lite directive (which would duplicate/contradict the curator guidance).
	if strings.Contains(prompt, "notable moments, not routine steps") {
		t.Fatalf("chief launch must not append the lite journaling directive: %q", prompt)
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
	if !strings.Contains(joined, "attn-notebook") || !strings.Contains(joined, "chief of staff") {
		t.Fatalf("developer_instructions should carry notebook guidance: %q", joined)
	}
	if strings.Contains(joined, "/tmp/context.md") {
		t.Fatalf("chief launch must not inject workspace-context guidance: %q", joined)
	}
	if strings.Contains(joined, "notable moments, not routine steps") {
		t.Fatalf("chief launch must not append the lite journaling directive: %q", joined)
	}
}

// A non-chief Codex launch carries ONLY its workspace-context guidance in a
// single developer_instructions override — no journaling directive is appended.
func TestCodexConfigOverrides_NonChiefOmitsJournalingDirective(t *testing.T) {
	overrides := (&Codex{}).GenerateConfigOverrides(SpawnOpts{
		SessionID:            "sess-1",
		WorkspaceContextPath: "/tmp/context.md",
	})
	var devInstr []string
	for _, o := range overrides {
		if strings.HasPrefix(o, "developer_instructions=") {
			devInstr = append(devInstr, o)
		}
	}
	// Exactly one developer_instructions entry, carrying the workspace-context guidance.
	if len(devInstr) != 1 {
		t.Fatalf("want exactly one developer_instructions override, got %d: %q", len(devInstr), overrides)
	}
	if !strings.Contains(devInstr[0], "/tmp/context.md") {
		t.Fatalf("developer_instructions should carry workspace-context guidance: %q", devInstr[0])
	}
	// The journaling directive must NOT be appended for non-chief agents.
	if strings.Contains(devInstr[0], "notable moments, not routine steps") {
		t.Fatalf("non-chief developer_instructions must not append the journaling directive: %q", devInstr[0])
	}
}

func TestCodexBuildEnvMarksNotebookGuidance(t *testing.T) {
	env := (&Codex{}).BuildEnv(SpawnOpts{
		SessionID:    "sess-1",
		WrapperPath:  "/Applications/attn-dev.app/Contents/MacOS/attn",
		NotebookRoot: "/home/u/attn-notebook",
	})
	if !slices.Contains(env, "ATTN_NOTEBOOK_GUIDANCE=developer_instructions") {
		t.Fatalf("env = %#v, want notebook guidance marker", env)
	}
	if !slices.Contains(env, "ATTN_WRAPPER_PATH=/Applications/attn-dev.app/Contents/MacOS/attn") {
		t.Fatalf("env = %#v, want explicit wrapper path", env)
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

func TestCopilotSupportsInitialPrompt(t *testing.T) {
	if !(&Copilot{}).Capabilities().HasInitialPrompt {
		t.Fatal("expected Copilot to support initial prompts via -i flag")
	}
}

func TestCopilotBuildCommandInitialPrompt(t *testing.T) {
	c := &Copilot{}
	cmd := c.BuildCommand(SpawnOpts{
		Executable:    "copilot",
		InitialPrompt: "fix the bug",
	})
	args := cmd.Args[1:] // skip executable
	found := false
	for i, a := range args {
		if a == "-i" && i+1 < len(args) && args[i+1] == "fix the bug" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected -i flag with prompt in args, got: %v", args)
	}
}
