package config

import "os"

// inheritedAgentSessionEnvKeys are the per-session environment variables a
// running coding agent (Claude Code today) injects into its child processes.
//
// attn is frequently (re)launched from inside such a session — most commonly an
// agent running `make install` — so these leak into the long-lived daemon and,
// from there, into every agent or shell the daemon spawns. A nested agent that
// inherits CLAUDE_CODE_SESSION_ID reuses its parent's session identity, which
// breaks transcript generation and other per-session behavior.
//
// These are runtime/session values, never user configuration. We scrub them
// from the daemon/worker process at startup (see ScrubInheritedAgentSessionEnv)
// rather than at each spawn site on purpose: with the process env clean, the
// login-shell env capture is spawned from a clean process and re-exports only
// what the user's shell profile genuinely sets (e.g. CLAUDE_CODE_USE_BEDROCK),
// so legitimate configuration survives while the leaked per-session values are
// dropped. Stripping at the spawn site cannot tell the two apart.
//
// When the agent adds a new per-session variable, add it here.
var inheritedAgentSessionEnvKeys = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS",
	"CLAUDE_CODE_NO_FLICKER",
	"CLAUDE_CODE_SSE_PORT",
	"CLAUDE_EFFORT",
}

// ScrubInheritedAgentSessionEnv removes the inherited agent-session variables
// from the current process environment and returns the keys that were actually
// set (for logging). Call it once, early, in any long-lived attn process that
// spawns agent or shell sessions — the daemon and the PTY worker — before the
// login-shell env is captured so the leaked values never reach spawned sessions.
func ScrubInheritedAgentSessionEnv() []string {
	scrubbed := make([]string, 0, len(inheritedAgentSessionEnvKeys))
	for _, key := range inheritedAgentSessionEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			_ = os.Unsetenv(key)
			scrubbed = append(scrubbed, key)
		}
	}
	return scrubbed
}
