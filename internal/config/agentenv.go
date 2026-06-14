package config

import "os"

// agentSessionIdentityEnvKeys uniquely identify the *parent* agent session that
// launched this attn process. They must never be inherited by a freshly
// launched agent: a nested agent that reuses CLAUDE_CODE_SESSION_ID writes to
// its parent's transcript, which breaks transcript generation.
//
// These are pure runtime identity — never user configuration — so they are
// always safe to scrub, including on launch paths that inherit the live shell
// environment directly (the foreground `attn` wrapper) without re-capturing a
// login shell.
var agentSessionIdentityEnvKeys = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SSE_PORT",
}

// agentSessionTuningEnvKeys are per-session runtime *tuning* values the agent
// injects (effort, compaction window, rendering flags). They leak the same way
// the identity vars do, but unlike identity vars a user might deliberately
// export some of them from their shell profile (e.g. CLAUDE_CODE_NO_FLICKER).
//
// They are therefore only scrubbed from long-lived attn processes (the daemon
// and PTY worker) that re-capture a clean login shell afterward: that capture
// re-exports whatever the profile genuinely sets, so legitimate configuration
// survives while the leaked per-session values are dropped. Launch paths that
// inherit the live shell env directly must NOT scrub these — there is no
// re-capture to restore the user's configuration.
var agentSessionTuningEnvKeys = []string{
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS",
	"CLAUDE_CODE_NO_FLICKER",
	"CLAUDE_EFFORT",
}

// ScrubAgentSessionIdentityEnv removes only the parent-session identity vars
// from the current process environment and returns the keys that were set.
// Use it on launch paths that inherit the live shell env directly (the
// foreground `attn` wrapper), where scrubbing tuning vars would drop user
// configuration that nothing re-exports.
func ScrubAgentSessionIdentityEnv() []string {
	return scrubEnvKeys(agentSessionIdentityEnvKeys)
}

// ScrubInheritedAgentSessionEnv removes the full set of inherited agent-session
// vars (identity + tuning) and returns the keys that were set (for logging).
// Call it once, early, in any long-lived attn process that spawns agent or
// shell sessions — the daemon and the PTY worker — before the login-shell env
// is captured, so the leaked values never reach spawned sessions.
func ScrubInheritedAgentSessionEnv() []string {
	scrubbed := scrubEnvKeys(agentSessionIdentityEnvKeys)
	return append(scrubbed, scrubEnvKeys(agentSessionTuningEnvKeys)...)
}

func scrubEnvKeys(keys []string) []string {
	scrubbed := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := os.LookupEnv(key); ok {
			_ = os.Unsetenv(key)
			scrubbed = append(scrubbed, key)
		}
	}
	return scrubbed
}
