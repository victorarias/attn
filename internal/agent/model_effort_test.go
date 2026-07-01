package agent

import (
	"slices"
	"testing"
)

// The model/effort pins ride SpawnOpts into each driver's launch argv. Claude
// has native --model/--effort flags; codex takes -m/--model and the
// model_reasoning_effort config override (quoted so the -c value parses as TOML).
func TestClaudeBuildCommand_ModelAndEffortPins(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:  "sess-1",
		CWD:        "/tmp/project",
		Executable: "claude",
		Model:      "claude-fable-5",
		Effort:     "low",
	})
	if i := slices.Index(cmd.Args, "--model"); i < 0 || cmd.Args[i+1] != "claude-fable-5" {
		t.Fatalf("args = %#v, want --model claude-fable-5", cmd.Args)
	}
	if i := slices.Index(cmd.Args, "--effort"); i < 0 || cmd.Args[i+1] != "low" {
		t.Fatalf("args = %#v, want --effort low", cmd.Args)
	}
}

func TestCodexBuildCommand_ModelAndEffortPins(t *testing.T) {
	cmd := (&Codex{}).BuildCommand(SpawnOpts{
		SessionID:  "sess-1",
		CWD:        "/tmp/project",
		Executable: "codex",
		Model:      "gpt-5.2-codex",
		Effort:     "high",
	})
	if i := slices.Index(cmd.Args, "--model"); i < 0 || cmd.Args[i+1] != "gpt-5.2-codex" {
		t.Fatalf("args = %#v, want --model gpt-5.2-codex", cmd.Args)
	}
	found := false
	for i, arg := range cmd.Args {
		if arg == "-c" && i+1 < len(cmd.Args) && cmd.Args[i+1] == `model_reasoning_effort="high"` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("args = %#v, want -c model_reasoning_effort=\"high\"", cmd.Args)
	}
}

func TestBuildCommand_NoModelEffortPinsByDefault(t *testing.T) {
	for _, driver := range []Driver{&Claude{}, &Codex{}} {
		cmd := driver.BuildCommand(SpawnOpts{
			SessionID:  "sess-1",
			CWD:        "/tmp/project",
			Executable: driver.DefaultExecutable(),
		})
		for _, arg := range cmd.Args {
			if arg == "--model" || arg == "--effort" || arg == `model_reasoning_effort=""` {
				t.Fatalf("%s args = %#v, want no model/effort flags", driver.Name(), cmd.Args)
			}
		}
	}
}
