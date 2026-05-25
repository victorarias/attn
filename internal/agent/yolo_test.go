package agent

import (
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
