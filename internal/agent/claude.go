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
	"strings"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// Claude implements Driver and optional capabilities for Claude Code.
type Claude struct{}

var _ Driver = (*Claude)(nil)
var _ HookProvider = (*Claude)(nil)
var _ TranscriptFinder = (*Claude)(nil)
var _ TranscriptWatcherBehaviorProvider = (*Claude)(nil)
var _ ClassifierProvider = (*Claude)(nil)
var _ LaunchPreparer = (*Claude)(nil)
var _ SessionRecoveryPolicyProvider = (*Claude)(nil)
var _ PTYStatePolicyProvider = (*Claude)(nil)
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
		HasStateDetector:     true,
		HasApprovalResolver:  true,
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
	if guidance := hooks.ChiefGuidance(opts.NotebookRoot); guidance != "" {
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
		return result, err
	}
	result.Text = parseClaudeFinalText(stdout)
	return result, nil
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
func claudeHeadlessArgs(request HeadlessTaskRequest) []string {
	tools := request.AllowedTools
	if len(tools) == 0 {
		tools = claudeNativeDefaultTools
	}
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
// no special recovery needs; this method exists only so Claude satisfies
// PTYStatePolicyProvider (which requires both methods) and ShouldApplyPTYState
// below is actually consulted.
func (c *Claude) RecoveredRunningState(ptyState string) protocol.SessionState {
	switch ptyState {
	case protocol.StateWaitingInput:
		return protocol.SessionStateWaitingInput
	case protocol.StatePendingApproval:
		return protocol.SessionStatePendingApproval
	default:
		return protocol.SessionStateLaunching
	}
}

// ShouldApplyPTYState keeps `scheduled` hook-authoritative. A session enters
// `scheduled` only via the Stop hook when Claude parks on a /loop or cron
// (session_crons present). The live PTY working-detector still observes the
// settled idle prompt and would otherwise emit idle/waiting_input/pending_approval
// and silently knock the session out of `scheduled`. The only legitimate live
// exit from a park is the loop/cron actually firing, which resumes a turn and
// the detector reports as `working` — so allow that single transition and
// reject every other incoming PTY state while parked. All non-scheduled
// transitions keep the default behavior (Claude otherwise trusts its detector).
func (c *Claude) ShouldApplyPTYState(current protocol.SessionState, incoming string) bool {
	if current == protocol.SessionStateScheduled {
		return incoming == protocol.StateWorking
	}
	return true
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
	deadline := time.Now().Add(claudeTranscriptRetryWindow)
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
		time.Sleep(claudeTranscriptRetryInterval)
	}
}

// --- ClassifierProvider ---

func (c *Claude) Classify(text string, timeout time.Duration) (string, error) {
	return classifier.ClassifyWithClaude(text, timeout)
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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	escapedPath := strings.ReplaceAll(cwd, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	return filepath.Join(homeDir, ".claude", "projects", escapedPath)
}
