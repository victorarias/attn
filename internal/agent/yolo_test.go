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
