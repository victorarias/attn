package config

import (
	"os"
	"slices"
	"testing"
)

func TestScrubInheritedAgentSessionEnv(t *testing.T) {
	// Leaked per-session vars (identity + tuning) that must be removed.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "cbcaa879-725b-44b2-8d91-212f6b00a516")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDE_EFFORT", "xhigh")
	t.Setenv("CLAUDE_CODE_NO_FLICKER", "1")

	// User configuration and unrelated vars that must survive — they are not in
	// the scrub list, mirroring how a real login shell re-exports profile config.
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("PATH", os.Getenv("PATH"))

	scrubbed := ScrubInheritedAgentSessionEnv()

	for _, key := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_EFFORT", "CLAUDE_CODE_NO_FLICKER"} {
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

// ScrubAgentSessionIdentityEnv runs on launch paths that inherit the live shell
// env directly, so it must drop the parent-session identity but keep tuning vars
// the user may have deliberately exported (nothing re-exports them there).
func TestScrubAgentSessionIdentityEnv_KeepsTuningVars(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "cbcaa879-leaked")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_CODE_NO_FLICKER", "1")
	t.Setenv("CLAUDE_EFFORT", "xhigh")

	scrubbed := ScrubAgentSessionIdentityEnv()

	for _, key := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT"} {
		if _, ok := os.LookupEnv(key); ok {
			t.Errorf("identity var %s should have been scrubbed but is still set", key)
		}
	}
	for _, key := range []string{"CLAUDE_CODE_NO_FLICKER", "CLAUDE_EFFORT"} {
		if _, ok := os.LookupEnv(key); !ok {
			t.Errorf("tuning var %s must survive identity-only scrub (no login shell re-captures it)", key)
		}
		if slices.Contains(scrubbed, key) {
			t.Errorf("identity scrub should not report tuning var %s", key)
		}
	}
}

func TestScrubInheritedAgentSessionEnv_OnlyReportsSetKeys(t *testing.T) {
	// Ensure none of the scrub keys are set in this process.
	for _, key := range append(slices.Clone(agentSessionIdentityEnvKeys), agentSessionTuningEnvKeys...) {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

	scrubbed := ScrubInheritedAgentSessionEnv()

	if len(scrubbed) != 1 || scrubbed[0] != "CLAUDE_CODE_ENTRYPOINT" {
		t.Errorf("expected only CLAUDE_CODE_ENTRYPOINT reported, got %v", scrubbed)
	}
}
