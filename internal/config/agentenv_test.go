package config

import (
	"os"
	"slices"
	"testing"
)

func TestScrubInheritedAgentSessionEnv(t *testing.T) {
	// Leaked per-session vars that must be removed.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "cbcaa879-725b-44b2-8d91-212f6b00a516")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDE_EFFORT", "xhigh")

	// User configuration and unrelated vars that must survive — they are not in
	// the scrub list, mirroring how a real login shell re-exports profile config.
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("PATH", os.Getenv("PATH"))

	scrubbed := ScrubInheritedAgentSessionEnv()

	for _, key := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_EFFORT"} {
		if _, ok := os.LookupEnv(key); ok {
			t.Errorf("%s should have been scrubbed but is still set", key)
		}
	}
	if !slices.Contains(scrubbed, "CLAUDE_CODE_SESSION_ID") {
		t.Errorf("returned scrubbed keys %v missing CLAUDE_CODE_SESSION_ID", scrubbed)
	}

	if got, ok := os.LookupEnv("CLAUDE_CODE_USE_BEDROCK"); !ok || got != "1" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK should survive (user config), got %q ok=%v", got, ok)
	}
	if got, ok := os.LookupEnv("ANTHROPIC_API_KEY"); !ok || got != "sk-test" {
		t.Errorf("ANTHROPIC_API_KEY should survive, got %q ok=%v", got, ok)
	}
}

func TestScrubInheritedAgentSessionEnv_OnlyReportsSetKeys(t *testing.T) {
	// Ensure none of the scrub keys are set in this process.
	for _, key := range inheritedAgentSessionEnvKeys {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

	scrubbed := ScrubInheritedAgentSessionEnv()

	if len(scrubbed) != 1 || scrubbed[0] != "CLAUDE_CODE_ENTRYPOINT" {
		t.Errorf("expected only CLAUDE_CODE_ENTRYPOINT reported, got %v", scrubbed)
	}
}
