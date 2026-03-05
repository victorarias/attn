// Package agent defines the Driver interface and optional capability interfaces
// for coding agents managed by attn. Adding a new agent (e.g. pi, gemini-cli)
// requires implementing Driver and whichever optional interfaces the agent supports.
//
// Core interface (required):
//
//	Driver — name, executable resolution, command building, env vars
//
// Optional capability interfaces (implement only what the agent supports):
//
//	HookProvider                    — generates hook/settings configs (e.g. Claude Code hooks)
//	TranscriptFinder                — locates transcript files on disk
//	TranscriptWatcherBehaviorProvider — custom real-time transcript state policy
//	ClassifierProvider              — custom classification backend
//	LaunchPreparer                  — best-effort setup before launch (e.g. resume copy)
//	SessionRecoveryPolicyProvider   — startup missing-PTY recovery policy
//	PTYStatePolicyProvider          — PTY state filtering/recovered-state policy
//	ResumePolicyProvider            — resume ID lifecycle policy
//	TranscriptClassificationExtractor — stop-time transcript extraction policy
//	ExecutableClassifierProvider    — classifier hook with explicit executable path
//
// Agents that don't implement an optional interface get sensible defaults:
//   - No hooks: no hook-driven state updates
//   - No transcript finder: classifier skipped on stop
//   - No classifier: session falls back to idle after stop
package agent

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Driver is the core interface every agent must implement.
// It provides the minimum needed to spawn and manage an agent process.
type Driver interface {
	// Name returns the canonical agent identifier (e.g. "claude", "pi", "gemini-cli").
	// Must match the SessionAgent enum value in the protocol.
	Name() string

	// DisplayName returns a human-friendly name for UI display (e.g. "Claude Code", "Pi").
	DisplayName() string

	// DefaultExecutable returns the default binary name (e.g. "claude", "pi").
	DefaultExecutable() string

	// ExecutableEnvVar returns the env var name that overrides the executable
	// (e.g. "ATTN_CLAUDE_EXECUTABLE"). Return "" if no override is supported.
	ExecutableEnvVar() string

	// ResolveExecutable returns the executable path, checking env var override,
	// configured value, and falling back to DefaultExecutable.
	ResolveExecutable(configured string) string

	// BuildCommand builds the exec.Cmd to launch the agent.
	// The command should NOT be started — the caller handles that.
	BuildCommand(opts SpawnOpts) *exec.Cmd

	// BuildEnv returns agent-specific environment variables to merge into
	// the spawned process environment. The caller handles ATTN_INSIDE_APP,
	// ATTN_SESSION_ID, etc. — this method only returns agent-specific extras
	// (e.g. executable overrides).
	BuildEnv(opts SpawnOpts) []string

	// Capabilities returns which optional features this agent supports.
	// Used by the daemon to decide which code paths to activate.
	Capabilities() Capabilities
}

// Capabilities declares which optional features an agent supports.
// This allows the daemon to skip code paths that don't apply to an agent
// without requiring stub implementations.
type Capabilities struct {
	// HasHooks indicates the agent supports a hook/settings system
	// (e.g. Claude Code hooks that report state changes via IPC).
	// If true, the driver should implement HookProvider.
	HasHooks bool

	// HasTranscript indicates the agent writes transcript files that attn
	// can discover and parse. If true, implement TranscriptFinder.
	HasTranscript bool

	// HasTranscriptWatcher indicates the daemon should run real-time transcript
	// watching for state updates. Requires HasTranscript.
	HasTranscriptWatcher bool

	// HasClassifier indicates the agent provides its own classification
	// backend via ClassifierProvider.
	HasClassifier bool

	// HasStateDetector indicates PTY state detection is enabled for this agent.
	HasStateDetector bool

	// HasResume indicates the agent supports resuming previous sessions.
	HasResume bool

	// HasFork indicates the agent supports forking a resumed session.
	HasFork bool
}

var capabilityEnvNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9]+`)

// EffectiveCapabilities returns driver capabilities after applying env overrides.
//
// Env format (per agent, optional):
//   - ATTN_AGENT_<AGENT>_HOOKS=0|1
//   - ATTN_AGENT_<AGENT>_TRANSCRIPT=0|1
//   - ATTN_AGENT_<AGENT>_TRANSCRIPT_WATCHER=0|1
//   - ATTN_AGENT_<AGENT>_CLASSIFIER=0|1
//   - ATTN_AGENT_<AGENT>_STATE_DETECTOR=0|1
//   - ATTN_AGENT_<AGENT>_RESUME=0|1
//   - ATTN_AGENT_<AGENT>_FORK=0|1
//
// <AGENT> is uppercased with non-alphanumeric chars replaced by underscores
// (e.g. "gemini-cli" -> "GEMINI_CLI").
func EffectiveCapabilities(d Driver) Capabilities {
	if d == nil {
		return Capabilities{}
	}
	caps := d.Capabilities()
	prefix := "ATTN_AGENT_" + envAgentKey(d.Name()) + "_"

	if v, ok := boolEnv(prefix + "HOOKS"); ok {
		caps.HasHooks = v
	}
	if v, ok := boolEnv(prefix + "TRANSCRIPT"); ok {
		caps.HasTranscript = v
	}
	if v, ok := boolEnv(prefix + "TRANSCRIPT_WATCHER"); ok {
		caps.HasTranscriptWatcher = v
	}
	if v, ok := boolEnv(prefix + "CLASSIFIER"); ok {
		caps.HasClassifier = v
	}
	if v, ok := boolEnv(prefix + "STATE_DETECTOR"); ok {
		caps.HasStateDetector = v
	}
	if v, ok := boolEnv(prefix + "RESUME"); ok {
		caps.HasResume = v
	}
	if v, ok := boolEnv(prefix + "FORK"); ok {
		caps.HasFork = v
	}

	// Consistency: transcript watcher requires transcript support.
	if !caps.HasTranscript {
		caps.HasTranscriptWatcher = false
	}
	return caps
}

func envAgentKey(name string) string {
	up := strings.ToUpper(strings.TrimSpace(name))
	up = capabilityEnvNameSanitizer.ReplaceAllString(up, "_")
	up = strings.Trim(up, "_")
	if up == "" {
		return "UNKNOWN"
	}
	return up
}

func boolEnv(key string) (bool, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return false, false
	}
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	if parsed, err := strconv.ParseBool(value); err == nil {
		return parsed, true
	}
	return false, false
}

// SpawnOpts contains the information needed to build an agent launch command.
type SpawnOpts struct {
	SessionID       string
	CWD             string
	Label           string
	Cols            uint16
	Rows            uint16
	ResumeSessionID string
	ResumePicker    bool
	ForkSession     bool

	// Executable is the resolved executable path (from ResolveExecutable).
	Executable string

	// SocketPath is the daemon's unix socket path (for hook commands).
	SocketPath string

	// WrapperPath is the resolved path to the attn binary.
	WrapperPath string

	// SettingsPath is a generated settings/hooks file path for agents that
	// support it (e.g. Claude's --settings <path>).
	SettingsPath string

	// AgentArgs are passthrough CLI args provided after attn wrapper flags.
	AgentArgs []string
}

// --- Optional capability interfaces ---

// HookProvider generates hook/settings configurations for agents that support them.
// Currently only Claude Code uses this (its hooks report state changes via IPC).
type HookProvider interface {
	// GenerateHooksConfig returns the content of a settings/hooks config file.
	// The caller writes it to a temp file and passes --settings to the agent.
	GenerateHooksConfig(sessionID, socketPath, wrapperPath string) string
}

// TranscriptFinder locates transcript files written by the agent.
type TranscriptFinder interface {
	// FindTranscript returns the path to the transcript file for a session.
	// Returns "" if not found. For agents without a session ID in the filename
	// (e.g. Codex, Copilot), cwd and startedAt help narrow the search.
	FindTranscript(sessionID, cwd string, startedAt time.Time) string

	// FindTranscriptForResume returns the transcript for a resumed session.
	// Returns "" if not applicable or not found.
	FindTranscriptForResume(resumeID string) string

	// BootstrapBytes returns how many bytes to read from the end of a transcript
	// when starting to watch mid-session (to catch recent context).
	BootstrapBytes() int64
}

// ClassifierProvider provides a custom classification backend.
type ClassifierProvider interface {
	// Classify determines whether the agent is waiting for input or done.
	// Returns "waiting_input", "idle", or "unknown".
	Classify(text string, timeout time.Duration) (string, error)
}

// LaunchPreparer performs best-effort agent-specific setup before launch
// (e.g. Claude resume transcript copy for fork/session handoff).
type LaunchPreparer interface {
	PrepareLaunch(opts SpawnOpts) error
}

// --- Registry ---

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Driver)
)

// Register adds a driver to the global registry.
// Panics if a driver with the same name is already registered.
func Register(d Driver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	name := d.Name()
	if _, exists := registry[name]; exists {
		panic("agent: driver already registered: " + name)
	}
	registry[name] = d
}

// Get returns the driver for the given agent name, or nil if not found.
func Get(name string) Driver {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[name]
}

// List returns the names of all registered drivers.
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// MustGet returns the driver for the given agent name, panicking if not found.
func MustGet(name string) Driver {
	d := Get(name)
	if d == nil {
		panic("agent: unknown driver: " + name)
	}
	return d
}

// --- Capability helpers ---

// GetHookProvider returns the HookProvider if the driver supports hooks.
func GetHookProvider(d Driver) (HookProvider, bool) {
	if d == nil || !EffectiveCapabilities(d).HasHooks {
		return nil, false
	}
	hp, ok := d.(HookProvider)
	return hp, ok
}

// GetTranscriptFinder returns the TranscriptFinder if the driver supports transcripts.
func GetTranscriptFinder(d Driver) (TranscriptFinder, bool) {
	if d == nil || !EffectiveCapabilities(d).HasTranscript {
		return nil, false
	}
	tf, ok := d.(TranscriptFinder)
	return tf, ok
}

// GetClassifier returns the ClassifierProvider if the driver provides one.
func GetClassifier(d Driver) (ClassifierProvider, bool) {
	if d == nil || !EffectiveCapabilities(d).HasClassifier {
		return nil, false
	}
	cp, ok := d.(ClassifierProvider)
	return cp, ok
}

// GetTranscriptWatcherBehavior returns a transcript watcher behavior for drivers
// that support transcript watching. Drivers may provide a custom behavior via
// TranscriptWatcherBehaviorProvider; otherwise a default behavior is used.
func GetTranscriptWatcherBehavior(d Driver) (TranscriptWatcherBehavior, bool) {
	if d == nil {
		return nil, false
	}
	caps := EffectiveCapabilities(d)
	if !caps.HasTranscript || !caps.HasTranscriptWatcher {
		return nil, false
	}
	if p, ok := d.(TranscriptWatcherBehaviorProvider); ok {
		behavior := p.NewTranscriptWatcherBehavior()
		if behavior != nil {
			behavior.Reset()
			return behavior, true
		}
	}
	behavior := newDefaultTranscriptWatcherBehavior()
	behavior.Reset()
	return behavior, true
}
