package agent

import (
	"strings"
	"testing"
)

// Both built-in agents are launched into the same garden, so both carry the
// primer — and neither invents one when the daemon had no answer.
func TestBuiltinAgentsCarryTheGardenPrimer(t *testing.T) {
	ready := 3

	claude := argvValueAfter(
		(&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude", GardenReady: &ready}).Args,
		"--append-system-prompt",
	)
	if !strings.Contains(claude, "attn seed ready") || !strings.Contains(claude, "3 seeds were ready") {
		t.Fatalf("claude launch dropped the garden primer: %q", claude)
	}

	codex := strings.Join((&Codex{}).GenerateConfigOverrides(SpawnOpts{SessionID: "s", GardenReady: &ready}), "\n")
	if !strings.Contains(codex, "attn seed ready") || !strings.Contains(codex, "3 seeds were ready") {
		t.Fatalf("codex launch dropped the garden primer: %q", codex)
	}

	bare := argvValueAfter(
		(&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude"}).Args,
		"--append-system-prompt",
	)
	if strings.Contains(bare, "attn seed ready") {
		t.Fatalf("a launch with no garden answer primed anyway: %q", bare)
	}
}
