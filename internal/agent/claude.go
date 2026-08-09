package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/transcript"
)

// Claude implements Driver and optional capabilities for Claude Code.
type Claude struct{}

var _ Driver = (*Claude)(nil)
var _ HookProvider = (*Claude)(nil)
var _ TranscriptFinder = (*Claude)(nil)
var _ TranscriptWatcherBehaviorProvider = (*Claude)(nil)
var _ ClassifierProvider = (*Claude)(nil)
var _ ExecutableClassifierProvider = (*Claude)(nil)
var _ LaunchPreparer = (*Claude)(nil)
var _ SessionRecoveryPolicyProvider = (*Claude)(nil)
var _ RecoveredStatePolicyProvider = (*Claude)(nil)
var _ ResumePolicyProvider = (*Claude)(nil)
var _ ResumeAvailabilityProvider = (*Claude)(nil)
var _ TranscriptClassificationExtractor = (*Claude)(nil)
var _ HeadlessTaskProvider = (*Claude)(nil)
var _ HeadlessTaskAvailabilityProvider = (*Claude)(nil)

const (
	claudeTranscriptRetryWindow   = 2 * time.Second
	claudeTranscriptRetryInterval = 100 * time.Millisecond
	claudeTranscriptFreshnessSkew = 5 * time.Second
)

func init() {
	Register(&Claude{})
}

func (c *Claude) Name() string              { return "claude" }
func (c *Claude) DisplayName() string       { return "Claude Code" }
func (c *Claude) DefaultExecutable() string { return "claude" }
func (c *Claude) ExecutableEnvVar() string  { return "ATTN_CLAUDE_EXECUTABLE" }

func (c *Claude) ResolveExecutable(configured string) string {
	return resolveExec(c.ExecutableEnvVar(), configured, c.DefaultExecutable())
}

func (c *Claude) Capabilities() Capabilities {
	return Capabilities{
		HasHooks:             true,
		HasTranscript:        true,
		HasTranscriptWatcher: true,
		HasClassifier:        true,
		HarnessSignals:       HarnessSignalsClaude,
		HasResume:            true,
		HasYolo:              true,
		HasInitialPrompt:     true,
		HasWorkspaceContext:  true,
		HasSelfMonitor:       true,
		HasModelPin:          true,
		HasEffortPin:         true,
	}
}

func (c *Claude) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{}

	useSessionID := true
	if opts.ResumeSessionID != "" || opts.ResumePicker {
		useSessionID = false
	}
	if useSessionID {
		args = append(args, "--session-id", opts.SessionID)
	}

	if strings.TrimSpace(opts.SettingsPath) != "" {
		args = append(args, "--settings", opts.SettingsPath)
	}
	// A chief-of-staff launch (NotebookRoot set) gets chief guidance instead
	// of the workspace-context checkout guidance. Every other workspace agent gets
	// its workspace-context guidance (plus workflow-trigger guidance when enabled,
	// folded in by hooks.AgentInstructions). Non-chief agents are NOT nudged to
	// journal: the keeper narrates each workspace's own work into the journal, and
	// the chief journals the cross-workspace layer.
	if guidance := hooks.ChiefGuidance(opts.NotebookRoot, c.Capabilities().HasSelfMonitor); guidance != "" {
		args = append(args, "--append-system-prompt", guidance)
	} else if instructions := hooks.AgentInstructions(opts.WorkspaceContextPath, opts.InjectWorkflowGuidance); instructions != "" {
		args = append(args, "--append-system-prompt", instructions)
	}

	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}

	if opts.ResumeSessionID != "" {
		args = append(args, "-r", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "-r")
	}
	if opts.YoloMode {
		args = append(args, "--dangerously-skip-permissions")
	} else if opts.AutoApprove {
		// Native auto-approve mode: an LLM permission classifier silently allows
		// safe/in-scope actions and denies risky ones, so the agent runs unattended
		// without stalling on approval prompts. Mutually exclusive with yolo, which
		// bypasses permissions entirely.
		args = append(args, "--permission-mode", "auto")
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}

	return exec.Command(opts.Executable, args...)
}

func (c *Claude) BuildEnv(opts SpawnOpts) []string {
	var env []string
	if strings.TrimSpace(opts.NotebookRoot) != "" {
		// A chief launch injected chief guidance at launch; mark it so the
		// SessionStart hook does not also emit workspace-context guidance.
		env = append(env, "ATTN_CHIEF_GUIDANCE=append_system_prompt")
	} else if strings.TrimSpace(opts.WorkspaceContextPath) != "" {
		env = append(env, "ATTN_WORKSPACE_CONTEXT_GUIDANCE=append_system_prompt")
	}
	// Cap the effective context window so auto-compaction fires at the configured
	// threshold. The daemon owns the policy of who gets a cap (chief setting vs
	// default_context_window_cap_<agent>); this only applies what it resolved.
	if opts.AutoCompactWindow > 0 {
		env = append(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+strconv.Itoa(opts.AutoCompactWindow))
	}
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

// claudeNativeDefaultTools is the file-tool allow-list used when a native-tools
// headless task does not specify AllowedTools. Bash is intentionally omitted:
// the keeper's compaction duty only needs to read/write/edit files, and Grep/Glob cover
// navigation, so the surface stays minimal.
var claudeNativeDefaultTools = []string{"Read", "Write", "Edit", "Grep", "Glob"}

func (c *Claude) RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error) {
	// Dispatch: the keeper/notebook tasks wire NO MCP server and run in
	// native-tools mode; the workflow engine sets a writable CWD+Sandbox (and an
	// MCP result sink when schema-validated) and runs the MCP-config path. Any
	// MCP-server, CWD, or Sandbox marker selects the MCP-config path.
	var args []string
	if request.usesNativeToolsPath() {
		args = claudeHeadlessArgs(request)
	} else {
		built, err := buildClaudeHeadlessArgs(request)
		if err != nil {
			return HeadlessTaskResult{}, err
		}
		args = built
	}

	// The process working directory is CWD when set (the writable engine path
	// points it at the run's working tree), else WorkDir (back-compat: the
	// keeper's throwaway temp dir).
	runDir := strings.TrimSpace(request.CWD)
	if runDir == "" {
		runDir = request.WorkDir
	}

	result, stdout, err := runHeadlessCommand(ctx, request.Executable, args, runDir, "claude")
	if err != nil {
		// A failed `--output-format json` run usually still ends with a result
		// event whose text is the human-readable error ("This model may not
		// exist...", "Not logged in..."); lead FailureOutput with it so callers
		// that surface the raw cause show the message before the JSON tail.
		if text := parseClaudeFinalText(stdout); text != "" {
			result.FailureOutput = strings.TrimSpace("result: " + text + "\n" + result.FailureOutput)
		}
		return result, err
	}
	result.Text = parseClaudeFinalText(stdout)
	meta := parseClaudeResultMeta(stdout)
	result.StructuredOutput = meta.StructuredOutput
	result.TotalCostUSD = meta.TotalCostUSD
	result.NumTurns = meta.NumTurns
	return result, nil
}

// claudeResultMeta is the telemetry slice of Claude's `--output-format json`
// result envelope: the schema-validated structured output (when --json-schema
// was passed) plus spend/turn accounting.
type claudeResultMeta struct {
	StructuredOutput json.RawMessage `json:"structured_output"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	NumTurns         int             `json:"num_turns"`
}

// parseClaudeResultMeta extracts the result envelope's meta fields from either
// stdout shape parseClaudeFinalText handles: a single result object, or a
// stream array whose last `type==result` event is the envelope. Absent fields
// stay zero — callers treat an empty StructuredOutput as "no verdict".
func parseClaudeResultMeta(stdout []byte) claudeResultMeta {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return claudeResultMeta{}
	}

	type resultEvent struct {
		Type string `json:"type"`
		claudeResultMeta
	}

	// (a) single result object.
	var single resultEvent
	if err := json.Unmarshal(trimmed, &single); err == nil && single.Type == "result" {
		return single.claudeResultMeta
	}

	// (b) stream array: last type==result wins.
	var events []json.RawMessage
	if err := json.Unmarshal(trimmed, &events); err == nil {
		for i := len(events) - 1; i >= 0; i-- {
			var ev resultEvent
			if json.Unmarshal(events[i], &ev) != nil {
				continue
			}
			if ev.Type == "result" {
				return ev.claudeResultMeta
			}
		}
	}
	return claudeResultMeta{}
}

// buildClaudeHeadlessArgs builds the `claude --print` argv for the MCP-config
// (workflow-engine) headless path. It is pure except for the env-dependent
// isolation arg (--bare vs --setting-sources), so a table test can assert the
// tool allowlist and --mcp-config wiring without spawning claude.
//
// Sandbox posture:
//   - request.Sandbox == "workspace-write" => the writable tool set adds Edit,
//     Write, MultiEdit, and Bash alongside the prefixed MCP tools. We keep
//     --permission-mode dontAsk: in Claude headless (`--print`) it auto-approves
//     edits and bash without any interactive prompt, which is exactly the
//     no-human-in-the-loop posture the engine needs (acceptEdits would NOT
//     auto-approve Bash). SECURITY BOUNDARY: unlike Codex, Claude has no OS
//     seatbelt here, so the allowlist itself is the boundary — only edit/write
//     and bash are added, nothing else; no MCP/network features beyond the
//     attached servers, and no --dangerously-skip-permissions.
//   - any other value (including "") => the locked MCP-tool-only allowlist.
func buildClaudeHeadlessArgs(request HeadlessTaskRequest) ([]string, error) {
	serverName := strings.TrimSpace(request.MCPServerName)
	if serverName == "" {
		serverName = "attn_context"
	}

	mcpServers := map[string]any{
		serverName: map[string]any{
			"type":    "stdio",
			"command": request.MCPServerCommand,
			"args":    request.MCPServerArgs,
		},
	}
	// Merge any additional MCP servers IN ADDITION to the primary one.
	for _, spec := range request.ExtraMCPServers {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		mcpServers[name] = map[string]any{
			"type":    "stdio",
			"command": spec.Command,
			"args":    spec.Args,
		}
	}
	config, err := json.Marshal(map[string]any{"mcpServers": mcpServers})
	if err != nil {
		return nil, fmt.Errorf("encode MCP config: %w", err)
	}

	// Primary server's prefixed tool names.
	prefixed := claudePrefixedTools(serverName, headlessToolNames(request.ToolName))
	// Each additional server's prefixed tool names.
	for _, spec := range request.ExtraMCPServers {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		prefixed = append(prefixed, claudePrefixedTools(name, spec.EnabledTools)...)
	}

	if request.Sandbox == "workspace-write" {
		// Built-in edit + shell tools. These are NOT mcp__-prefixed; they are
		// Claude's native tool names.
		prefixed = append(prefixed, "Edit", "Write", "MultiEdit", "Bash")
	}

	tools := strings.Join(prefixed, ",")
	args := []string{"--print"}
	args = append(args, claudeHeadlessIsolationArgs()...)
	// Only pin the model when one is requested; an empty "--model" is rejected as
	// an invalid model. Omitting it lets Claude use its own default (the faithful
	// "harness decides" default when agent() has no model override).
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args,
		"--no-session-persistence",
		"--strict-mcp-config",
		"--mcp-config", string(config),
		"--disable-slash-commands",
		"--no-chrome",
		"--tools", tools,
		"--allowedTools", tools,
		// dontAsk auto-approves edits AND bash in --print mode (acceptEdits would
		// not cover Bash); it is the headless no-prompt posture for both paths.
		"--permission-mode", "dontAsk",
		"--output-format", "json",
		request.Prompt,
	)
	return args, nil
}

// claudeHeadlessArgs builds the native-tools arg set (the keeper/notebook path):
// the agent gets its own file tools and writes into cmd.Dir (the scratch
// WorkDir). Only the allow-list and permission mode let it read/write its cwd
// unprompted.
//
// DisableTools is the one exception: it skips the claudeNativeDefaultTools
// fallback entirely and emits an empty --allowedTools, so the run gets no
// tools at all (a pure single-shot completion). Without this special case, an
// empty AllowedTools alone would silently re-enable the native defaults —
// exactly the trap DisableTools exists to avoid.
func claudeHeadlessArgs(request HeadlessTaskRequest) []string {
	tools := request.AllowedTools
	if len(tools) == 0 && !request.DisableTools {
		tools = claudeNativeDefaultTools
	}
	// Not trimmed here: --disallowedTools. An empty --allowedTools makes the
	// native tools uncallable but still ships their definitions in the billed
	// prefix, and --disallowedTools "*" does drop them (~24.8K prefix tokens to
	// ~2.3K, measured). It also disables StructuredOutput, the tool the CLI uses
	// to deliver a --json-schema answer, so a schema-validated run produces
	// nothing and exits non-zero. Cost saving that can silently cost the answer
	// is not worth having; the enumerated-list alternative works but has to track
	// the CLI's tool set forever with the same failure mode when it drifts.
	args := []string{"--print"}
	args = append(args, claudeHeadlessIsolationArgs()...)
	// --strict-mcp-config with no --mcp-config loads ZERO MCP servers. Without
	// it the user's claude.ai account connectors (Slack/Gmail/Drive/Calendar)
	// still attach — --setting-sources "" does not cover them (verified
	// empirically, 2.1.198) — and a failing or needs-auth connector can sink an
	// otherwise-healthy run. No native-tools task needs MCP.
	args = append(args, "--strict-mcp-config")
	// Only pin the model when one is requested; an empty "--model" is rejected as
	// an invalid model. Omitting it lets Claude use its own default (the faithful
	// "harness decides" default when agent() has no model override).
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	// Reasoning effort, when the caller pinned one. Measured as inert on
	// claude-haiku-4-5 (none/low/medium/high all produce ~900-1,050 output tokens
	// on the same input), but a configurable-effort setting has to actually reach
	// the CLI to be honest about what it does.
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "--effort", effort)
	}
	// Judgment-run caps + structured output (the reconciliation classifier).
	// --max-turns is accepted by the CLI though absent from --help (verified
	// empirically, 2.1.198); --max-budget-usd and --json-schema are documented.
	if request.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(request.MaxTurns))
	}
	if budget := strings.TrimSpace(request.MaxBudgetUSD); budget != "" {
		args = append(args, "--max-budget-usd", budget)
	}
	if len(request.OutputSchema) > 0 {
		args = append(args, "--json-schema", string(request.OutputSchema))
	}
	// Replacing the CLI's own system prompt is the largest single cost lever on a
	// single-shot run; see HeadlessTaskRequest.SystemPrompt for the measurement.
	if prompt := strings.TrimSpace(request.SystemPrompt); prompt != "" {
		args = append(args, "--system-prompt", prompt)
	}
	args = append(args,
		"--no-session-persistence",
		"--disable-slash-commands",
		"--no-chrome",
		"--allowedTools", strings.Join(tools, ","),
		"--permission-mode", "dontAsk",
		"--output-format", "json",
		request.Prompt,
	)
	return args
}

// claudePrefixedTools maps an MCP server's tool names to their mcp__<server>__
// prefixed form for --tools/--allowedTools.
func claudePrefixedTools(serverName string, names []string) []string {
	prefix := "mcp__" + serverName + "__"
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

// parseClaudeFinalText extracts the final assistant text from Claude headless
// `--output-format json` stdout. Claude canonically emits a single result
// object {"type":"result","result":"<final text>"}, but some configs emit a
// stream array of events instead. Both shapes are handled. We do not route
// through internal/transcript (a different on-disk shape).
func parseClaudeFinalText(stdout []byte) string {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return ""
	}

	// (a) single object with a string `result`.
	var single struct {
		Type   string          `json:"type"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(trimmed, &single); err == nil {
		if text := claudeResultString(single.Result); text != "" {
			return text
		}
	}

	// (b) stream array of events: take the last `type==result` with a string
	// `result`, else the last assistant message's joined text blocks.
	var events []json.RawMessage
	if err := json.Unmarshal(trimmed, &events); err == nil {
		for i := len(events) - 1; i >= 0; i-- {
			var ev struct {
				Type   string          `json:"type"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(events[i], &ev) != nil {
				continue
			}
			if ev.Type == "result" {
				if text := claudeResultString(ev.Result); text != "" {
					return text
				}
			}
		}
		for i := len(events) - 1; i >= 0; i-- {
			if text := claudeAssistantText(events[i]); text != "" {
				return text
			}
		}
	}
	return ""
}

// claudeResultString returns the trimmed string value of a `result` field, or
// "" when it is absent / not a string.
func claudeResultString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// claudeAssistantText joins the text blocks of an `assistant` stream event.
func claudeAssistantText(raw json.RawMessage) string {
	var ev struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Type != "assistant" {
		return ""
	}
	var parts []string
	for _, block := range ev.Message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func (c *Claude) HeadlessTaskAvailability() (bool, string) {
	return true, ""
}

func claudeHeadlessIsolationArgs() []string {
	if claudeHasBareModeAuthentication() {
		return []string{"--bare"}
	}
	return []string{"--setting-sources", ""}
}

func claudeHasBareModeAuthentication() bool {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

// PrepareLaunch copies resume transcripts into the target project folder so
// Claude can resolve --resume when the resumed transcript belongs to another project folder.
func (c *Claude) PrepareLaunch(opts SpawnOpts) error {
	if err := ensureAttnClaudeSkillInstalled(); err != nil {
		return err
	}
	if strings.TrimSpace(opts.ResumeSessionID) == "" {
		return nil
	}
	return copyTranscriptForResume(opts.ResumeSessionID, opts.CWD)
}

// --- HookProvider ---

func (c *Claude) GenerateHooksConfig(sessionID, socketPath, wrapperPath string) string {
	return hooks.Generate(sessionID, socketPath, wrapperPath)
}

// --- TranscriptFinder ---

func (c *Claude) FindTranscript(sessionID, cwd string, startedAt time.Time) string {
	return transcript.FindClaudeTranscript(sessionID)
}

func (c *Claude) FindTranscriptForResume(resumeID string) string {
	// Claude transcripts are found by session ID, resume uses the same mechanism.
	return transcript.FindClaudeTranscript(resumeID)
}

// ResumeAvailable reports whether resumeID can be resumed. Claude resumes via
// `claude -r <id>`, which needs a transcript on disk; that transcript is written
// lazily on the first turn, so a zero-turn session has none and a resume would
// exit non-zero. The transcript's existence is therefore the exact resumability
// signal.
func (c *Claude) ResumeAvailable(resumeID string) bool {
	return transcript.FindClaudeTranscript(resumeID) != ""
}

func (c *Claude) BootstrapBytes() int64 {
	return 256 * 1024
}

func (c *Claude) NewTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &claudeTranscriptWatcherBehavior{}
}

func (c *Claude) RecoverOnMissingPTY() bool {
	return true
}

// RecoveredRunningState mirrors the default recovered-state mapping. Claude has
// no special recovery needs; the method exists to satisfy
// RecoveredStatePolicyProvider.
func (c *Claude) RecoveredRunningState(ptyState string) (protocol.SessionState, bool) {
	return recoveredStateFromPTYClaim(ptyState)
}

func (c *Claude) ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	requested := strings.TrimSpace(requestedResumeID)
	stored := strings.TrimSpace(storedResumeID)
	if stored != "" && (requested == "" || requested == strings.TrimSpace(existingSessionID)) {
		return stored
	}
	return requested
}

func (c *Claude) SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string {
	resolved := strings.TrimSpace(resolvedResumeID)
	if resolved != "" {
		return resolved
	}
	if !resumePicker {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func (c *Claude) ResumeSessionIDFromStopTranscriptPath(transcriptPath string) string {
	clean := strings.TrimSpace(transcriptPath)
	if clean == "" {
		return ""
	}
	base := filepath.Base(clean)
	if !strings.HasSuffix(base, ".jsonl") {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(base, ".jsonl"))
}

func (c *Claude) ExtractLastAssistantForClassification(
	transcriptPath string,
	maxChars int,
	classificationStart time.Time,
	lastClassifiedTurnID string,
) (content string, turnID string, err error) {
	return c.extractLastAssistantForClassification(
		transcriptPath,
		maxChars,
		classificationStart,
		lastClassifiedTurnID,
		claudeTranscriptRetryWindow,
		claudeTranscriptRetryInterval,
	)
}

func (c *Claude) extractLastAssistantForClassification(
	transcriptPath string,
	maxChars int,
	classificationStart time.Time,
	lastClassifiedTurnID string,
	retryWindow time.Duration,
	retryInterval time.Duration,
) (content string, turnID string, err error) {
	deadline := time.Now().Add(retryWindow)
	minAssistantTimestamp := classificationStart.Add(-claudeTranscriptFreshnessSkew)
	lastClassified := strings.TrimSpace(lastClassifiedTurnID)
	for {
		turn, turnErr := transcript.ExtractLastAssistantTurnAfterLastUserSince(
			transcriptPath,
			maxChars,
			minAssistantTimestamp,
		)
		if turnErr == nil && strings.TrimSpace(turn.Content) != "" {
			turnUUID := strings.TrimSpace(turn.UUID)
			if turnUUID != "" && turnUUID == lastClassified {
				turnErr = ErrNoNewAssistantTurn
			} else {
				return turn.Content, turnUUID, nil
			}
		}
		if !time.Now().Before(deadline) {
			if turnErr == nil {
				turnErr = ErrNoNewAssistantTurn
			}
			return "", "", turnErr
		}
		time.Sleep(retryInterval)
	}
}

// --- ClassifierProvider ---

func (c *Claude) Classify(text string, timeout time.Duration) (string, error) {
	return c.ClassifyWithExecutable(text, "", "", timeout)
}

// ClassifyWithExecutable classifies a stop-time assistant message through a
// bounded headless `claude -p` run: no tools, capped turns, and a JSON Schema the
// verdict must validate against. It shares the headless seam with every other
// non-interactive Claude run in the daemon, so it inherits the same isolation
// posture (no MCP servers, no user settings, no session transcript, scrubbed
// environment) instead of the interactive CLI's ambient configuration.
//
// workDir is ignored: the run reads nothing from disk, so it executes in a
// throwaway scratch dir rather than the session's directory. It stays in the
// signature to satisfy ExecutableClassifierProvider, which Codex needs (its CLI
// refuses to run outside a trusted repo).
func (c *Claude) ClassifyWithExecutable(text, executable, workDir string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(text) == "" {
		classifier.DefaultLogger("classifier: empty text, returning idle")
		return "idle", nil
	}
	classifier.DefaultLogger("classifier: input text (%d chars): %q", len(text), text)

	resolved, err := exec.LookPath(c.ResolveExecutable(executable))
	if err != nil {
		classifier.DefaultLogger("classifier: claude executable unresolved: %v", err)
		return "unknown", fmt.Errorf("resolve claude executable: %w", err)
	}

	scratchDir, err := os.MkdirTemp("", "attn-claude-classifier-*")
	if err != nil {
		return "unknown", fmt.Errorf("create classifier scratch dir: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	model := classifier.ClaudeClassifierModel()
	classifier.DefaultLogger(
		"classifier: calling claude CLI executable=%s model=%s timeout=%d seconds",
		resolved,
		model,
		int(timeout.Seconds()),
	)

	result, err := c.RunHeadlessTask(ctx, HeadlessTaskRequest{
		Executable:   resolved,
		Model:        model,
		Prompt:       classifier.BuildPrompt(text),
		WorkDir:      scratchDir,
		DisableTools: true,
		MaxTurns:     classifier.ClaudeMaxTurns,
		OutputSchema: json.RawMessage(classifier.ClaudeVerdictSchema),
	})
	if err != nil {
		classifier.DefaultLogger("classifier: claude CLI failed model=%s err=%v output=%s",
			model, err, classifier.TruncateForLog(result.FailureOutput))
		return "unknown", fmt.Errorf("claude cli: %w", err)
	}

	classifier.DefaultLogger(
		"classifier: claude CLI run num_turns=%d cost_usd=%.4f structured_output=%s text=%s",
		result.NumTurns,
		result.TotalCostUSD,
		classifier.TruncateForLog(string(result.StructuredOutput)),
		classifier.TruncateForLog(result.Text),
	)

	if state, ok := classifier.ParseVerdict(result.StructuredOutput, result.Text); ok {
		return state, nil
	}
	classifier.DefaultLogger("classifier: claude response missing explicit WAITING/DONE verdict, returning unknown")
	return "unknown", nil
}

func copyTranscriptForResume(resumeSessionID, cwd string) error {
	srcPath := transcript.FindClaudeTranscript(resumeSessionID)
	if srcPath == "" {
		return fmt.Errorf("resume transcript not found for session %s", resumeSessionID)
	}

	destDir := claudeProjectDir(cwd)
	if destDir == "" {
		return fmt.Errorf("could not determine Claude project directory")
	}
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to create project directory: %w", err)
	}

	destPath := filepath.Join(destDir, resumeSessionID+".jsonl")
	if srcPath == destPath {
		return nil
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source transcript: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create destination transcript: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy transcript: %w", err)
	}
	return nil
}

func claudeProjectDir(cwd string) string {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return ""
	}
	escapedPath := strings.ReplaceAll(cwd, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	return filepath.Join(homeDir, ".claude", "projects", escapedPath)
}
