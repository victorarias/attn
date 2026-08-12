// Package agent defines the Driver interface and optional capability
// interfaces for coding agents managed by attn. A new agent implements Driver
// plus whichever optional interfaces it supports; missing ones get defaults
// (no hook-driven state, classifier skipped on stop, idle after stop).
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

	"github.com/victorarias/attn/internal/hooks"
)

// Driver is the core interface every agent must implement.
type Driver interface {
	// Name returns the canonical agent id; must match the protocol's SessionAgent enum.
	Name() string

	// DisplayName returns a human-friendly name for UI display.
	DisplayName() string

	// DefaultExecutable returns the default binary name.
	DefaultExecutable() string

	// ExecutableEnvVar returns the executable-override env var, or "" when unsupported.
	ExecutableEnvVar() string

	// ResolveExecutable returns the path: env override, configured, then DefaultExecutable.
	ResolveExecutable(configured string) string

	// BuildCommand builds the exec.Cmd to launch the agent, not started.
	BuildCommand(opts SpawnOpts) *exec.Cmd

	// BuildEnv returns agent-specific env extras only; the caller handles ATTN_* basics.
	BuildEnv(opts SpawnOpts) []string

	// Capabilities returns which optional features this agent supports.
	Capabilities() Capabilities
}

// Capabilities declares which optional features an agent supports.
type Capabilities struct {
	// HasHooks: hook/settings system; implement HookProvider or ConfigOverrideProvider.
	HasHooks bool

	// HasTranscript: discoverable transcript files; implement TranscriptFinder.
	HasTranscript bool

	// HasTranscriptWatcher: real-time transcript watching. Requires HasTranscript.
	HasTranscriptWatcher bool

	// HasClassifier: the agent provides its own ClassifierProvider backend.
	HasClassifier bool

	// HarnessSignals names the harness-owned PTY signals this agent emits (OSC
	// 0 title heartbeat, OSC 777), or HarnessSignalsNone. They come from the
	// agent itself, not from scraping its rendered TUI.
	HarnessSignals HarnessSignalKind

	// HasResume: the agent supports resuming previous sessions.
	HasResume bool

	// HasYolo: the agent supports launching with approvals bypassed.
	HasYolo bool

	// HasInitialPrompt: can start interactive and immediately submit a prompt.
	HasInitialPrompt bool

	// HasWorkspaceContext: accepts hidden launch instructions for a workspace
	// context checkout.
	HasWorkspaceContext bool

	// HasSelfMonitor: the agent can watch its own ticket/event stream via a
	// live Monitor. Selects chief guidance only; nudge eligibility is shared.
	HasSelfMonitor bool

	// HasModelPin: launch accepts SpawnOpts.Model; delegation rejects --model without it.
	HasModelPin bool

	// HasEffortPin: launch accepts SpawnOpts.Effort; delegation rejects --effort without it.
	HasEffortPin bool
}

// HarnessSignalKind identifies a set of harness-owned PTY signals. The parsing
// lives in internal/pty; this names which agent's dialect to read.
type HarnessSignalKind string

const (
	// HarnessSignalsNone is an agent that emits no harness state signals.
	HarnessSignalsNone HarnessSignalKind = ""
	// HarnessSignalsClaude is Claude Code: a braille/U+2733 title heartbeat plus
	// OSC 777 notifications.
	HarnessSignalsClaude HarnessSignalKind = "claude"
	// HarnessSignalsCodex is Codex: a braille title heartbeat, no notification
	// OSC.
	HarnessSignalsCodex HarnessSignalKind = "codex"
)

var capabilityEnvNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9]+`)

// EffectiveCapabilities returns driver capabilities after applying optional
// env overrides ATTN_AGENT_<AGENT>_<CAP>=0|1 (suffixes below); <AGENT> is the
// uppercased name with non-alphanumerics as underscores ("gemini-cli" -> "GEMINI_CLI").
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
	// HARNESS_SIGNALS=0 disables; =1 keeps the driver's kind (there is no
	// dialect for =1 to invent).
	if v, ok := boolEnv(prefix + "HARNESS_SIGNALS"); ok && !v {
		caps.HarnessSignals = HarnessSignalsNone
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

	// Transcript watcher requires transcript support.
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

	// AutoApprove launches the agent in its native auto-approve mode (Claude
	// --permission-mode auto, Codex approvals_reviewer=auto_review). Yolo wins.
	AutoApprove bool

	// Model, when set, pins the agent's model via --model; empty means default.
	Model string

	// Effort, when set, pins reasoning effort via the agent's native mechanism;
	// only meaningful for drivers with HasEffortPin.
	Effort string

	// AutoCompactWindow, when > 0, triggers auto-compaction at that token
	// threshold (Claude: CLAUDE_CODE_AUTO_COMPACT_WINDOW; Codex:
	// model_auto_compact_token_limit); applied on every launch shape.
	AutoCompactWindow int

	// Executable is the resolved executable path (from ResolveExecutable).
	Executable string

	// SocketPath is the daemon's unix socket path (for hook commands).
	SocketPath string

	// WrapperPath is the resolved path to the attn binary.
	WrapperPath string

	// SettingsPath is a generated settings/hooks file path (Claude --settings).
	SettingsPath string

	// WorkspaceContextPath is this session's local checkout of the workspace's
	// shared context; may become stale after launch.
	WorkspaceContextPath string

	// InjectWorkflowGuidance appends the workflow-trigger guidance to the
	// launch instructions. Never set for workflow subagents.
	InjectWorkflowGuidance bool

	// NotebookRoot, when set, makes this a chief-of-staff launch (Notebook
	// guidance); at most one of NotebookRoot and WorkspaceContextPath is set.
	NotebookRoot string

	// ConfigOverrides are agent CLI config overrides generated for this launch.
	ConfigOverrides []string

	// TrustWorkingDirectory lets an unattended daemon-owned launch pass the
	// driver's repository trust gate; interactive launches leave it false.
	TrustWorkingDirectory bool

	// GardenReady is how many seeds this session's workspace had ready when the
	// launch resolved it, and nil when the daemon had no answer — no garden
	// reachable from here. It is what the garden primer is built from.
	GardenReady *int
}

// launchGuidance is the system-prompt block for this launch: chief guidance or
// the workspace agent's, plus the garden primer. hasSelfMonitor is the driver's
// own capability, which is why the driver composes rather than the caller.
func (o SpawnOpts) launchGuidance(hasSelfMonitor bool) string {
	return hooks.Launch{
		NotebookRoot:         o.NotebookRoot,
		HasSelfMonitor:       hasSelfMonitor,
		WorkspaceContextPath: o.WorkspaceContextPath,
		InjectWorkflow:       o.InjectWorkflowGuidance,
		GardenReady:          o.GardenReady,
	}.Instructions()
}

// --- Optional capability interfaces ---

// HookProvider generates hook/settings configurations for agents that support them.
type HookProvider interface {
	// GenerateHooksConfig returns settings/hooks config file content; the
	// caller writes it to a temp file and passes --settings to the agent. It
	// takes the whole launch because that file is also how a launch knob the
	// user's own settings could otherwise overwrite reaches the agent.
	GenerateHooksConfig(opts SpawnOpts) string
}

// ConfigOverrideProvider generates per-launch CLI config overrides.
type ConfigOverrideProvider interface {
	GenerateConfigOverrides(opts SpawnOpts) []string
}

// HeadlessTaskRequest describes a daemon-owned non-interactive task; it must
// not create an interactive attn session. Validation + commit stay daemon-owned.
type HeadlessTaskRequest struct {
	Executable string
	Model      string
	// ReasoningEffort selects the provider's reasoning setting; empty preserves
	// the provider default.
	ReasoningEffort  string
	Prompt           string
	WorkDir          string
	MCPServerName    string
	MCPServerCommand string
	MCPServerArgs    []string

	// ToolName overrides the single MCP tool name in the driver argv; empty =>
	// the janitor default set {read_context, replace_context}.
	ToolName string
	// Schema is the per-call JSON Schema the result sink advertises as the tool
	// inputSchema. NOT consumed by the driver; it travels via MCPServerArgs.
	Schema json.RawMessage
	// ResultPath is the per-call file the sink writes the validated payload to.
	// Like Schema, baked into MCPServerArgs by the caller; drivers ignore it.
	ResultPath string

	// Sandbox selects the OS sandbox posture: "" => read-only; "workspace-write"
	// => edits + shell, seatbelt-confined to cwd + TMPDIR, network off, no
	// approval bypass. Any unrecognized value is read-only (fail closed).
	Sandbox string
	// CWD is the process working directory; empty falls back to WorkDir.
	// Scratch files stay rooted at WorkDir to keep the tree clean.
	CWD string
	// ExtraMCPServers are attached IN ADDITION to the primary MCPServer*
	// triple, not instead of it.
	ExtraMCPServers []MCPServerSpec

	// AllowedTools overrides the default native tool set; empty => provider
	// default. Codex ignores it.
	AllowedTools []string

	// DisableTools runs with NO tools at all — the explicit tool-less switch,
	// since empty AllowedTools still falls back to the provider default set.
	// Uncallable but not free: the definitions still ship in the billed
	// prefix (see claudeHeadlessArgs).
	DisableTools bool

	// ExtraWritableRoots widens WRITE access beyond the scratch WorkDir.
	// Claude: IGNORED (dontAsk is not fs-sandboxed, writes anywhere already).
	// Codex: each root becomes `--add-dir <root>`.
	ExtraWritableRoots []string

	// MaxTurns/MaxBudgetUSD/OutputSchema are Claude-only runaway caps +
	// structured output; Codex ignores all three. See
	// docs/plans/2026-07-01-orphaned-ticket-reconciliation.md.

	// MaxTurns caps agentic turns (claude: --max-turns). 0 => uncapped.
	MaxTurns int
	// MaxBudgetUSD caps API spend, decimal string (claude: --max-budget-usd).
	MaxBudgetUSD string
	// OutputSchema is an inline JSON Schema the final answer must validate
	// against (claude: --json-schema); returns HeadlessTaskResult.StructuredOutput.
	OutputSchema json.RawMessage

	// SystemPrompt REPLACES the agent CLI's own system prompt (claude:
	// --system-prompt). Empty => the CLI's default, which is what every caller
	// wanted before this field existed.
	//
	// This is a cost lever, not a behavior lever. A coding CLI's default system
	// prompt is written for interactive coding and is thousands of tokens the
	// run pays for on every invocation; a single-shot judgment or one-line
	// completion needs none of it. Measured on claude-haiku-4-5, tool-less, with
	// a --json-schema answer: the billed prefix drops from ~49.8K tokens to
	// ~37.0K. Receipt: docs/plans/2026-08-07-session-activity.md.
	//
	// Both drivers honour it, by different means: Claude gets --system-prompt,
	// Codex has no system/developer-prompt flag at all, so the driver folds this
	// text in front of Prompt (see codexPrompt). A caller therefore writes one
	// split prompt and gets the same evidence boundary on either agent.
	SystemPrompt string
}

// usesNativeToolsPath reports whether this request runs the native-tools
// headless path (keeper/notebook tasks) rather than the MCP-config path; any
// MCP-server, CWD, or Sandbox marker selects the latter.
func (r HeadlessTaskRequest) usesNativeToolsPath() bool {
	return strings.TrimSpace(r.MCPServerName) == "" &&
		strings.TrimSpace(r.MCPServerCommand) == "" &&
		len(r.ExtraMCPServers) == 0 &&
		strings.TrimSpace(r.CWD) == "" &&
		strings.TrimSpace(r.Sandbox) == ""
}

// MCPServerSpec describes one MCP server to attach to a headless run
// (HeadlessTaskRequest.ExtraMCPServers).
type MCPServerSpec struct {
	// Name is the server identifier; tool names are prefixed with it for Claude
	// (mcp__<Name>__<tool>) and keyed under mcp_servers.<Name> for Codex.
	Name string
	// Command is the executable hosting the MCP server (stdio transport).
	Command string
	// Args are the command arguments.
	Args []string
	// EnabledTools is the explicit tool allowlist added to the driver's
	// enabled_tools / --allowedTools (prefixed for Claude).
	EnabledTools []string
}

type HeadlessTaskResult struct {
	Diagnostics string
	// FailureOutput is the bounded raw tail of a failed child's stderr/stdout.
	// It can echo prompt/workspace text, so callers choose whether their
	// surface may show it.
	FailureOutput string
	// Text is the child's final assistant text (no-schema path), set on success.
	Text string
	// StructuredOutput is the schema-validated result object when OutputSchema
	// was set; empty means "no verdict" (no schema, cap-hit, or error).
	StructuredOutput json.RawMessage
	// TotalCostUSD / NumTurns are spend telemetry; zero when unreported.
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
	// FindTranscript returns the session's transcript path, "" if not found;
	// cwd/startedAt narrow the search for agents without session-ID filenames.
	FindTranscript(sessionID, cwd string, startedAt time.Time) string

	// FindTranscriptForResume returns the transcript for a resumed session,
	// "" if not applicable or not found.
	FindTranscriptForResume(resumeID string) string

	// BootstrapBytes is how many bytes to read from a transcript's end when
	// starting to watch mid-session.
	BootstrapBytes() int64
}

// ClassifierProvider provides a custom classification backend.
type ClassifierProvider interface {
	// Classify returns "waiting_input", "idle", or "unknown".
	Classify(text string, timeout time.Duration) (string, error)
}

// LaunchPreparer performs best-effort agent-specific setup before launch
// (e.g. Claude resume transcript copy).
type LaunchPreparer interface {
	PrepareLaunch(opts SpawnOpts) error
}

// --- Registry ---

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Driver)
)

// Register adds a driver to the global registry; panics on a duplicate name.
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

// GetTranscriptWatcherBehavior returns a transcript watcher behavior — custom
// via TranscriptWatcherBehaviorProvider, else the default.
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
