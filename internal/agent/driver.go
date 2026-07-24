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
//	HeadlessTaskProvider            — scoped non-interactive agent execution
//
// Agents that don't implement an optional interface get sensible defaults:
//   - No hooks: no hook-driven state updates
//   - No transcript finder: classifier skipped on stop
//   - No classifier: session falls back to idle after stop
package agent

import (
	"context"
	"encoding/json"
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
	// If true, the driver should implement HookProvider or ConfigOverrideProvider.
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

	// HasApprovalResolver indicates the daemon should clear pending_approval ->
	// working off the rendered PTY screen for this agent. Needed by hook-driven
	// agents that fire no hook when an approval is granted, so the only signal
	// the tool is now running is the approval prompt leaving the screen.
	HasApprovalResolver bool

	// HasResume indicates the agent supports resuming previous sessions.
	HasResume bool

	// HasYolo indicates the agent supports launching with approvals bypassed.
	HasYolo bool

	// HasInitialPrompt indicates the agent can start an interactive session and
	// immediately submit a prompt supplied by attn.
	HasInitialPrompt bool

	// HasWorkspaceContext indicates attn can give the agent hidden launch
	// instructions for using a workspace context checkout.
	HasWorkspaceContext bool

	// HasSelfMonitor indicates the agent can optionally watch its own ticket/event
	// stream via a live Monitor. It selects chief guidance only; daemon nudge
	// eligibility is shared across runtimes. Only Claude supports this today.
	HasSelfMonitor bool

	// HasModelPin indicates the agent's launch command accepts a per-session
	// model pin (SpawnOpts.Model). Delegation rejects --model for agents without it.
	HasModelPin bool

	// HasEffortPin indicates the agent's launch command accepts a per-session
	// reasoning-effort pin (SpawnOpts.Effort). Delegation rejects --effort for
	// agents without it.
	HasEffortPin bool
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
//   - ATTN_AGENT_<AGENT>_APPROVAL_RESOLVER=0|1
//   - ATTN_AGENT_<AGENT>_RESUME=0|1
//   - ATTN_AGENT_<AGENT>_YOLO=0|1
//   - ATTN_AGENT_<AGENT>_INITIAL_PROMPT=0|1
//   - ATTN_AGENT_<AGENT>_WORKSPACE_CONTEXT=0|1
//   - ATTN_AGENT_<AGENT>_SELF_MONITOR=0|1
//   - ATTN_AGENT_<AGENT>_MODEL_PIN=0|1
//   - ATTN_AGENT_<AGENT>_EFFORT_PIN=0|1
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
	if v, ok := boolEnv(prefix + "APPROVAL_RESOLVER"); ok {
		caps.HasApprovalResolver = v
	}
	if v, ok := boolEnv(prefix + "RESUME"); ok {
		caps.HasResume = v
	}
	if v, ok := boolEnv(prefix + "YOLO"); ok {
		caps.HasYolo = v
	}
	if v, ok := boolEnv(prefix + "INITIAL_PROMPT"); ok {
		caps.HasInitialPrompt = v
	}
	if v, ok := boolEnv(prefix + "WORKSPACE_CONTEXT"); ok {
		caps.HasWorkspaceContext = v
	}
	if v, ok := boolEnv(prefix + "SELF_MONITOR"); ok {
		caps.HasSelfMonitor = v
	}
	if v, ok := boolEnv(prefix + "MODEL_PIN"); ok {
		caps.HasModelPin = v
	}
	if v, ok := boolEnv(prefix + "EFFORT_PIN"); ok {
		caps.HasEffortPin = v
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
	InitialPrompt   string
	Cols            uint16
	Rows            uint16
	ResumeSessionID string
	ResumePicker    bool
	YoloMode        bool

	// AutoApprove, when true (and not YoloMode), launches the agent in its native
	// auto-approve mode (Claude --permission-mode auto, Codex
	// approvals_reviewer=auto_review) so it runs unattended without stalling on
	// permission gates. Gated by the daemon's auto_approve_enabled setting and
	// threaded into the worker via ATTN_AUTO_APPROVE. Yolo takes precedence.
	AutoApprove bool

	// Model, when set, pins the interactive agent's model via --model (an alias
	// like "opus"/"sonnet" or a full model id). Empty means the agent's own
	// default. Sourced from a delegation's --model flag, or else the daemon's
	// chief_model_<agent> setting (chief launches only) or default_model_<agent>
	// setting (every launch), and threaded into the worker via ATTN_MODEL.
	Model string

	// Effort, when set, pins the interactive agent's reasoning effort using the
	// agent's native mechanism (Claude --effort, Codex model_reasoning_effort).
	// Empty means the agent's own default. Sourced from a delegation's --effort
	// flag, or else the daemon's chief_effort_<agent> setting (chief launches
	// only) or default_effort_<agent> setting (every launch), and threaded into
	// the worker via ATTN_EFFORT. Only meaningful for drivers with HasEffortPin.
	Effort string

	// AutoCompactWindow, when > 0, caps this launch's effective context window so
	// auto-compaction triggers at that token threshold instead of the model's full
	// window (Claude: CLAUDE_CODE_AUTO_COMPACT_WINDOW env var; Codex:
	// model_auto_compact_token_limit config override). Sourced from the
	// chief_context_window_cap setting for chief launches and relayed into the
	// worker via ATTN_CHIEF_AUTO_COMPACT_WINDOW; 0 means no cap. Only applied on a
	// chief launch, so delegated interactive agents are never capped.
	AutoCompactWindow int

	// Executable is the resolved executable path (from ResolveExecutable).
	Executable string

	// SocketPath is the daemon's unix socket path (for hook commands).
	SocketPath string

	// WrapperPath is the resolved path to the attn binary.
	WrapperPath string

	// SettingsPath is a generated settings/hooks file path for agents that
	// support it (e.g. Claude's --settings <path>).
	SettingsPath string

	// WorkspaceContextPath is this session's local checkout of the workspace's
	// shared context. It may become stale after launch.
	WorkspaceContextPath string

	// InjectWorkflowGuidance, when true, appends the workflow-trigger guidance to
	// this session's launch instructions (system prompt / developer instructions).
	// It is gated by the daemon's workflows_enabled setting and is never set for
	// workflow subagents, which spawn through the headless path instead.
	InjectWorkflowGuidance bool

	// NotebookRoot, when set, makes this a chief-of-staff launch: the agent
	// receives Notebook guidance (its profile-wide durable home) instead of the
	// workspace-context checkout guidance. In practice the launch path sets at
	// most one of NotebookRoot and WorkspaceContextPath.
	NotebookRoot string

	// ConfigOverrides are agent CLI config overrides generated for this launch.
	ConfigOverrides []string

	// TrustWorkingDirectory allows an unattended, daemon-owned launch to pass
	// the driver's repository trust gate. Interactive launches leave this false.
	TrustWorkingDirectory bool
}

// --- Optional capability interfaces ---

// HookProvider generates hook/settings configurations for agents that support them.
// Some agents consume these through a generated settings file.
type HookProvider interface {
	// GenerateHooksConfig returns the content of a settings/hooks config file.
	// The caller writes it to a temp file and passes --settings to the agent.
	GenerateHooksConfig(sessionID, socketPath, wrapperPath string) string
}

// ConfigOverrideProvider generates per-launch CLI config overrides.
type ConfigOverrideProvider interface {
	GenerateConfigOverrides(opts SpawnOpts) []string
}

// HeadlessTaskRequest describes a daemon-owned non-interactive task. The task
// must not create an interactive attn session. The agent runs in native-tools
// mode: it gets its OWN file tools and a writable working dir (WorkDir). The
// daemon writes inputs into WorkDir and reads the agent's output file back;
// validation + commit stay daemon-owned.
type HeadlessTaskRequest struct {
	Executable string
	Model      string
	// ReasoningEffort selects the provider's reasoning setting for this one
	// bounded headless request. Empty preserves the provider default.
	ReasoningEffort  string
	Prompt           string
	WorkDir          string
	MCPServerName    string
	MCPServerCommand string
	MCPServerArgs    []string

	// --- E2 additive (all optional; the janitor sets none of these) ---

	// ToolName overrides the single MCP tool name threaded through the driver
	// argv. Empty => the janitor default tool set {read_context, replace_context}
	// (back-compat). Non-empty => exactly that one tool is enabled.
	ToolName string
	// Schema, when non-empty, is the per-call JSON Schema the result sink
	// advertises as the tool inputSchema. It is NOT consumed by the driver; it
	// travels to the sink via MCPServerArgs (the caller builds those argv).
	// Stored on the request for documentation/threading symmetry; drivers ignore it.
	Schema json.RawMessage
	// ResultPath is the per-call file the sink writes the validated payload to.
	// Like Schema, the caller bakes it into MCPServerArgs; drivers ignore it.
	ResultPath string

	// --- E3 additive (all optional; the janitor sets none of these) ---

	// Sandbox selects the OS sandbox posture of the headless run. Accepted values:
	//   - ""               => read-only (DEFAULT, byte-identical to the janitor):
	//                         Codex `--sandbox read-only` + every feature stripped;
	//                         Claude locked to the MCP tool allowlist only.
	//   - "workspace-write" => writable: the agent may edit files and run shell,
	//                          confined by the macOS seatbelt to cwd + TMPDIR with
	//                          network disabled by default. NO other features are
	//                          re-enabled, and no approval bypass is used.
	// Any UNRECOGNIZED value is treated as read-only (fail closed).
	Sandbox string
	// CWD is the process working directory for the run. Empty => fall back to
	// WorkDir (back-compat). The writable engine path sets this to the run's
	// working tree so edits land where the workflow expects them; scratch files
	// (last-message, schema, result) stay rooted at WorkDir to keep the tree clean.
	CWD string
	// ExtraMCPServers are attached IN ADDITION to the primary MCPServer* triple
	// (not instead of it), so a workflow session's MCP tools reach the subagent
	// alongside return_result. The janitor sets none.
	ExtraMCPServers []MCPServerSpec

	// AllowedTools optionally overrides the default native tool set
	// (Claude: Read,Write,Edit,Grep,Glob). Empty => provider default. (Codex
	// ignores this field; its native tooling comes from the workspace-write
	// sandbox defaults, not a CLI list.) Used by the native-tools headless path
	// (the keeper/notebook tasks), which wires no MCP server.
	AllowedTools []string

	// DisableTools, when true, runs the native-tools headless path with NO
	// tools at all — a pure single-shot completion. This overrides
	// AllowedTools entirely: an empty AllowedTools alone still falls back to
	// the provider's native default tool set, so DisableTools is the explicit
	// way to get a truly tool-less run. Empty/false is byte-for-byte identical
	// to today's behavior. Used by the ticket reconciliation classifier, which
	// judges a pre-extracted transcript slice with no need to touch disk.
	DisableTools bool

	// ExtraWritableRoots optionally widens the set of directories the agent may
	// WRITE to, beyond the scratch WorkDir. The notebook narration tasks use this
	// so a headless agent can write the curated journal / raw tier under the
	// notebook root (which lives outside the scratch tempdir).
	//
	// Provider behavior:
	//   - Claude: IGNORED. Claude headless runs with --permission-mode dontAsk,
	//     which is NOT filesystem-sandboxed — it can already write anywhere the OS
	//     user can, given absolute paths. No widening is needed or applied.
	//   - Codex: each root is passed as `--add-dir <root>` so the
	//     workspace-write sandbox (which otherwise confines writes to the cwd
	//     WorkDir) also permits writes under these roots. Reads are unrestricted
	//     under workspace-write, so transcript dirs need no widening.
	//
	// Empty (the keeper's compaction case) leaves both providers' existing
	// scratch-only behavior unchanged.
	ExtraWritableRoots []string

	// --- reconciliation additive (all optional; the keeper/workflow set none) ---
	// Runaway caps + structured output for judgment-style runs (the ticket
	// reconciliation classifier). Claude-only: Claude Code is the one agent CLI
	// with enforceable turn/dollar caps and schema-validated output, which is why
	// reconciliation always runs `claude -p` regardless of the judged agent (see
	// docs/plans/2026-07-01-orphaned-ticket-reconciliation.md). Codex ignores all
	// three, like AllowedTools.

	// MaxTurns caps agentic turns (claude: --max-turns). 0 => uncapped.
	MaxTurns int
	// MaxBudgetUSD caps API spend, as a decimal string (claude: --max-budget-usd).
	// Empty => uncapped.
	MaxBudgetUSD string
	// OutputSchema, when non-empty, is passed inline as a JSON Schema the final
	// answer must validate against (claude: --json-schema). Unlike Schema (the
	// MCP result-sink path), this IS consumed by the driver argv; the validated
	// object comes back in HeadlessTaskResult.StructuredOutput.
	OutputSchema json.RawMessage
}

// usesNativeToolsPath reports whether this request runs through the native-tools
// headless path (the keeper / notebook narration tasks) rather than the
// MCP-config / writable-tree path (the workflow engine). The keeper sets none of
// the MCP-server fields and neither CWD nor Sandbox; the workflow engine always
// sets at least a writable CWD+Sandbox, and additionally an MCP result sink when
// a call needs a schema-validated return. Any one of those markers selects the
// MCP-config path.
func (r HeadlessTaskRequest) usesNativeToolsPath() bool {
	return strings.TrimSpace(r.MCPServerName) == "" &&
		strings.TrimSpace(r.MCPServerCommand) == "" &&
		len(r.ExtraMCPServers) == 0 &&
		strings.TrimSpace(r.CWD) == "" &&
		strings.TrimSpace(r.Sandbox) == ""
}

// MCPServerSpec describes one MCP server to attach to a headless run. It mirrors
// the primary MCPServer* triple as a value so a list of additional servers can
// be threaded through HeadlessTaskRequest.ExtraMCPServers.
type MCPServerSpec struct {
	// Name is the server identifier; tool names are prefixed with it for Claude
	// (mcp__<Name>__<tool>) and keyed under mcp_servers.<Name> for Codex.
	Name string
	// Command is the executable that hosts the MCP server (stdio transport).
	Command string
	// Args are the command arguments.
	Args []string
	// EnabledTools is the explicit tool allowlist exposed by this server. Each is
	// added to the driver's enabled_tools / --allowedTools (prefixed for Claude).
	EnabledTools []string
}

type HeadlessTaskResult struct {
	Diagnostics string
	// FailureOutput is the bounded raw tail of the failed child's stderr and
	// stdout — the ground truth behind the Diagnostics keyword bucket. Set only
	// on a failed run. It can echo prompt/workspace text, so callers choose
	// whether their surface may show it (the reconcile failure comment does;
	// keeper journal surfaces stick to Diagnostics).
	FailureOutput string
	// Text is the child's captured final assistant text (the no-schema path).
	// Drivers populate this on a successful run.
	Text string
	// StructuredOutput is the schema-validated result object when the request
	// set OutputSchema (claude: the result envelope's structured_output field).
	// Empty when no schema was requested or the run ended before producing one
	// (cap-hit, error) — callers must treat empty as "no verdict".
	StructuredOutput json.RawMessage
	// TotalCostUSD / NumTurns are spend telemetry from the result envelope
	// (claude --output-format json). Zero when the provider doesn't report them.
	TotalCostUSD float64
	NumTurns     int
}

// HeadlessTaskProvider runs a bounded non-interactive agent task.
type HeadlessTaskProvider interface {
	RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error)
}

// HeadlessTaskAvailabilityProvider reports whether the current process
// environment can run the driver's isolated headless mode.
type HeadlessTaskAvailabilityProvider interface {
	HeadlessTaskAvailability() (bool, string)
}

func HeadlessTaskAvailability(driver Driver) (bool, string) {
	if driver == nil {
		return false, "agent is not installed"
	}
	if _, ok := driver.(HeadlessTaskProvider); !ok {
		return false, "agent does not support headless tasks"
	}
	if provider, ok := driver.(HeadlessTaskAvailabilityProvider); ok {
		return provider.HeadlessTaskAvailability()
	}
	return true, ""
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
// (e.g. Claude resume transcript copy for session handoff).
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

// GetConfigOverrideProvider returns the ConfigOverrideProvider if supported.
func GetConfigOverrideProvider(d Driver) (ConfigOverrideProvider, bool) {
	if d == nil || !EffectiveCapabilities(d).HasHooks {
		return nil, false
	}
	cp, ok := d.(ConfigOverrideProvider)
	return cp, ok
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
