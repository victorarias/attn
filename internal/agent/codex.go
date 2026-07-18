package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// Codex implements Driver and optional capabilities for OpenAI Codex CLI.
type Codex struct{}

var _ Driver = (*Codex)(nil)
var _ TranscriptFinder = (*Codex)(nil)
var _ ClassifierProvider = (*Codex)(nil)
var _ PTYStatePolicyProvider = (*Codex)(nil)
var _ ExecutableClassifierProvider = (*Codex)(nil)
var _ ConfigOverrideProvider = (*Codex)(nil)
var _ ResumePolicyProvider = (*Codex)(nil)
var _ LaunchPreparer = (*Codex)(nil)
var _ HeadlessTaskProvider = (*Codex)(nil)

func init() {
	Register(&Codex{})
}

func (c *Codex) Name() string              { return "codex" }
func (c *Codex) DisplayName() string       { return "Codex" }
func (c *Codex) DefaultExecutable() string { return "codex" }
func (c *Codex) ExecutableEnvVar() string  { return "ATTN_CODEX_EXECUTABLE" }

func (c *Codex) ResolveExecutable(configured string) string {
	return resolveExec(c.ExecutableEnvVar(), configured, c.DefaultExecutable())
}

func (c *Codex) Capabilities() Capabilities {
	// Transcripts remain needed for Stop-hook classification; live state is hook-owned.
	return Capabilities{
		HasHooks:            true,
		HasTranscript:       true,
		HasClassifier:       true,
		HasStateDetector:    false,
		HasApprovalResolver: true,
		HasResume:           true,
		HasYolo:             true,
		HasInitialPrompt:    true,
		HasWorkspaceContext: true,
		// HasSelfMonitor: false — Codex has no live ticket Monitor. It still uses
		// the shared daemon nudge for unread ticket activity.
		HasModelPin:  true,
		HasEffortPin: true,
	}
}

func (c *Codex) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{}
	for _, override := range opts.ConfigOverrides {
		if strings.TrimSpace(override) == "" {
			continue
		}
		args = append(args, "-c", override)
	}

	if opts.ResumeSessionID != "" {
		args = append(args, "resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "resume")
	}

	args = append(args, "-C", opts.CWD)
	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	if strings.TrimSpace(opts.NotebookRoot) != "" {
		// Cap the chief's effective context window (model_auto_compact_token_limit
		// is codex's compaction-trigger knob, the analogue of Claude's
		// CLAUDE_CODE_AUTO_COMPACT_WINDOW). Gated on the chief branch so delegated
		// interactive agents are never capped.
		args = append(args, codexContextWindowCapArgs(opts.AutoCompactWindow)...)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		// Codex has no dedicated effort flag; model_reasoning_effort is its
		// native config knob (the -c value is parsed as TOML, hence the quotes).
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	if opts.YoloMode {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else if opts.AutoApprove {
		// Native auto-approve: route approval requests to codex's guardian LLM
		// reviewer (auto_review) instead of the user, so the agent runs unattended.
		// on-request is codex's recommended interactive policy — the model escalates
		// when it needs to, and auto_review approves/denies in the user's place
		// (the codex analog of Claude's --permission-mode auto). Yolo bypasses both.
		args = append(args, "-c", `approval_policy="on-request"`, "-c", `approvals_reviewer="auto_review"`)
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}

	return exec.Command(opts.Executable, args...)
}

func (c *Codex) BuildEnv(opts SpawnOpts) []string {
	env := []string{
		"ATTN_SESSION_ID=" + opts.SessionID,
		"ATTN_AGENT=codex",
	}
	if wrapper := strings.TrimSpace(opts.WrapperPath); wrapper != "" {
		env = append(env, "ATTN_WRAPPER_PATH="+wrapper)
	}
	if strings.TrimSpace(opts.NotebookRoot) != "" {
		// A chief launch injected chief guidance at launch; mark it so the
		// SessionStart hook does not also emit workspace-context guidance.
		env = append(env, "ATTN_CHIEF_GUIDANCE=developer_instructions")
	} else if strings.TrimSpace(opts.WorkspaceContextPath) != "" {
		env = append(env, "ATTN_WORKSPACE_CONTEXT_GUIDANCE=developer_instructions")
	}
	if opts.SocketPath != "" {
		env = append(env, "ATTN_SOCKET_PATH="+opts.SocketPath)
	}
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

func (c *Codex) PrepareLaunch(opts SpawnOpts) error {
	return ensureAttnCodexSkillInstalled()
}

func (c *Codex) RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error) {
	// Dispatch: the keeper/notebook tasks wire NO MCP server and run in
	// native-tools mode; the workflow engine sets a writable CWD+Sandbox (and an
	// MCP result sink when schema-validated) and runs the MCP-config path.
	// The process-global headless cap governs every headless run uniformly; read
	// it once here and pass it into the pure arg builders as an explicit input.
	window := HeadlessContextWindowCap()
	if request.usesNativeToolsPath() {
		args := codexHeadlessArgs(request, window)
		if request.DisableTools {
			args = codexToolFreeHeadlessArgs(request, window)
		}
		result, stdout, err := runHeadlessCommand(ctx, request.Executable, args, request.WorkDir, "codex")
		if err != nil {
			return result, err
		}
		result.Text = parseCodexFinalText(stdout)
		return result, nil
	}

	// Capture the child's final message to a file. This is the parser-free,
	// robust no-schema text path (codex -o, --output-last-message <FILE>). We
	// create the file ourselves so cleanup is deterministic; codex overwrites it.
	// It is rooted at WorkDir (NOT CWD) so the working tree stays clean even on
	// the writable path.
	lastMsgPath := ""
	if f, err := os.CreateTemp(headlessTempDir(request.WorkDir), "codex-last-msg-*.txt"); err == nil {
		lastMsgPath = f.Name()
		f.Close()
		defer os.Remove(lastMsgPath)
	}

	args := buildCodexHeadlessArgs(request, lastMsgPath, window)

	// The process working directory is CWD when set (the writable engine path
	// points it at the run's working tree), else WorkDir (back-compat: the
	// keeper's throwaway temp dir).
	runDir := strings.TrimSpace(request.CWD)
	if runDir == "" {
		runDir = request.WorkDir
	}

	result, stdout, err := runHeadlessCommand(ctx, request.Executable, args, runDir, "codex")
	if err != nil {
		return result, err
	}
	result.Text = codexFinalText(lastMsgPath, stdout)
	return result, nil
}

// buildCodexHeadlessArgs builds the `codex exec` argv for the MCP-config
// (workflow-engine) headless run. It is pure (no process spawn, no filesystem
// access beyond the caller-provided lastMsgPath string) so a table test can
// assert the sandbox/feature flags and MCP-server wiring without running codex.
//
// Sandbox posture:
//   - request.Sandbox == "workspace-write" => `--sandbox workspace-write` and
//     `features.shell_tool=true`. SECURITY BOUNDARY: on macOS this confines
//     writes to the process cwd + TMPDIR with network disabled by default via
//     the OS seatbelt. `approval_policy="never"` stays because the sandbox — NOT
//     an interactive approval prompt — is the enforcement boundary; with no human
//     in the loop a prompt would only deadlock. We NEVER emit
//     `--dangerously-bypass-approvals-and-sandbox` and NEVER use
//     `danger-full-access`; every non-essential feature stays disabled exactly as
//     on the read-only path. The ONLY things re-enabled are the OS sandbox mode
//     and the shell tool.
//   - any other value (including "") => read-only, byte-identical to the janitor:
//     `--sandbox read-only` + `features.shell_tool=false`.
func buildCodexHeadlessArgs(request HeadlessTaskRequest, lastMsgPath string, window int) []string {
	serverName := strings.TrimSpace(request.MCPServerName)
	if serverName == "" {
		serverName = "attn_context"
	}
	toolNames := headlessToolNames(request.ToolName)

	writable := request.Sandbox == "workspace-write"
	sandboxMode := "read-only"
	shellTool := "features.shell_tool=false"
	if writable {
		sandboxMode = "workspace-write"
		shellTool = "features.shell_tool=true"
	}

	args := []string{
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox", sandboxMode,
	}
	// Only pin the model when one is requested. An empty "-m" makes codex reject
	// the run as "model is invalid or unavailable"; omitting the flag lets codex
	// fall back to its own default model (the faithful "harness decides" default
	// for a workflow agent() with no per-call/run model override).
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "-m", model)
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	if lastMsgPath != "" {
		args = append(args, "--output-last-message", lastMsgPath)
	}
	args = append(args,
		"-c", `approval_policy="never"`,
		"-c", shellTool,
		// Every other feature stays OFF on BOTH paths. Writable re-enables only the
		// shell tool above; nothing else here changes between read-only and writable.
		"-c", "features.unified_exec=false",
	)
	// Shared non-file feature locks (identical to the native-tools path).
	args = append(args, codexFeatureLocks()...)
	args = append(args, codexMCPServerArgs(serverName, request.MCPServerCommand, request.MCPServerArgs, toolNames)...)
	// Attach any additional MCP servers IN ADDITION to the primary one, mirroring
	// its emission exactly (command/args/required/enabled_tools/approval-mode).
	for _, spec := range request.ExtraMCPServers {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		args = append(args, codexMCPServerArgs(name, spec.Command, spec.Args, spec.EnabledTools)...)
	}
	args = append(args, codexContextWindowCapArgs(window)...)
	args = append(args, request.Prompt)
	return args
}

// codexFeatureLocks are the non-file feature locks. They keep the agent surface
// minimal (no apps/hooks/plugins/browser/etc.) and are independent of the
// file/exec tooling enabled by the workspace-write sandbox.
func codexFeatureLocks() []string {
	return []string{
		"-c", "features.apps=false",
		"-c", "features.hooks=false",
		"-c", "features.plugins=false",
		"-c", "features.browser_use=false",
		"-c", "features.in_app_browser=false",
		"-c", "features.computer_use=false",
		"-c", "features.image_generation=false",
		"-c", "features.memories=false",
		"-c", "features.multi_agent=false",
		"-c", "features.goals=false",
		"-c", "features.shell_snapshot=false",
		"-c", "features.standalone_web_search=false",
		"-c", "features.tool_suggest=false",
		"-c", "features.workspace_dependencies=false",
	}
}

// codexContextWindowCapArgs returns the `-c model_auto_compact_token_limit=<n>`
// override that caps codex's effective context window (auto-compaction fires at
// this token threshold instead of near the model's full window), or nil when
// window <= 0. The value is a TOML integer, so it is unquoted. This is codex's
// analogue of Claude's CLAUDE_CODE_AUTO_COMPACT_WINDOW.
func codexContextWindowCapArgs(window int) []string {
	if window <= 0 {
		return nil
	}
	return []string{"-c", "model_auto_compact_token_limit=" + strconv.Itoa(window)}
}

// codexMCPServerArgs emits the `-c mcp_servers.<name>.*` argv pairs for one MCP
// server. Both the primary server and each ExtraMCPServers entry go through here
// so their wiring is identical by construction.
func codexMCPServerArgs(name, command string, cmdArgs, enabledTools []string) []string {
	return []string{
		"-c", fmt.Sprintf("mcp_servers.%s.command=%s", name, strconv.Quote(command)),
		"-c", fmt.Sprintf("mcp_servers.%s.args=%s", name, tomlStringArray(cmdArgs)),
		"-c", fmt.Sprintf("mcp_servers.%s.required=true", name),
		"-c", fmt.Sprintf("mcp_servers.%s.enabled_tools=%s", name, tomlStringArray(enabledTools)),
		"-c", fmt.Sprintf(`mcp_servers.%s.default_tools_approval_mode="approve"`, name),
	}
}

// tomlStringArray renders a Go string slice as a TOML inline array literal with
// each element double-quoted, for `codex -c key=[...]` overrides.
func tomlStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

// codexFinalText returns the child's final assistant message. It prefers the
// --output-last-message file (parser-free, robust) and falls back to scanning
// the live --json stdout for the last agent_message item. NOTE: the live
// `codex exec --json` stream is NOT the on-disk transcript envelope, so we do
// not route this through internal/transcript.
func codexFinalText(lastMsgPath string, stdout []byte) string {
	if lastMsgPath != "" {
		if b, err := os.ReadFile(lastMsgPath); err == nil {
			if text := strings.TrimSpace(string(b)); text != "" {
				return text
			}
		}
	}
	return parseCodexFinalText(stdout)
}

// parseCodexFinalText scans `codex exec --json` stdout (JSONL) for the LAST
// agent_message item and returns its text. The relevant line shape is:
//
//	{"type":"item.completed","item":{"type":"agent_message","text":"..."}}
func parseCodexFinalText(stdout []byte) string {
	last := ""
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Type == "item.completed" && event.Item.Type == "agent_message" {
			if text := strings.TrimSpace(event.Item.Text); text != "" {
				last = text
			}
		}
	}
	return last
}

// codexHeadlessArgs builds the native-tools arg set: a workspace-write sandbox
// makes cwd (the scratch WorkDir via cmd.Dir) writable, and Codex's default
// file/exec tooling is active. approval_policy="never" keeps writes autonomous
// and the run non-interactive.
func codexHeadlessArgs(request HeadlessTaskRequest, window int) []string {
	args := []string{
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox", "workspace-write",
		"-m", strings.TrimSpace(request.Model),
		"-c", `approval_policy="never"`,
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	// Widen the workspace-write sandbox's writable set beyond the scratch WorkDir
	// for tasks that must write outside cwd (e.g. the notebook narrate pass writing the
	// curated journal under the notebook root). `--add-dir` is the codex exec flag
	// for "additional directories that should be writable alongside the primary
	// workspace"; reads stay unrestricted under workspace-write, so this is
	// write-only widening. Empty (the keeper's compaction duty) appends nothing,
	// preserving the scratch-tempdir-only behavior.
	for _, root := range request.ExtraWritableRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		args = append(args, "--add-dir", root)
	}
	args = append(args, codexFeatureLocks()...)
	args = append(args, codexContextWindowCapArgs(window)...)
	args = append(args, request.Prompt)
	return args
}

// codexToolFreeHeadlessArgs builds a pure completion invocation. The prompt is
// the complete model-visible evidence boundary: Codex receives neither native
// shell/file tools nor user-configured MCP, plugins, apps, or web search.
func codexToolFreeHeadlessArgs(request HeadlessTaskRequest, window int) []string {
	args := []string{
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"-m", strings.TrimSpace(request.Model),
		"-c", `approval_policy="never"`,
		"-c", "features.shell_tool=false",
		"-c", "features.unified_exec=false",
		"-c", `web_search="disabled"`,
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	args = append(args, codexFeatureLocks()...)
	args = append(args, codexContextWindowCapArgs(window)...)
	args = append(args, request.Prompt)
	return args
}

// --- ConfigOverrideProvider ---

func (c *Codex) GenerateConfigOverrides(opts SpawnOpts) []string {
	return hooks.GenerateCodexConfigOverrides(
		opts.SessionID,
		opts.SocketPath,
		opts.WrapperPath,
		opts.WorkspaceContextPath,
		opts.NotebookRoot,
		opts.InjectWorkflowGuidance,
	)
}

// --- TranscriptFinder ---

func (c *Codex) FindTranscript(sessionID, cwd string, startedAt time.Time) string {
	return transcript.FindCodexTranscript(cwd, startedAt)
}

func (c *Codex) FindTranscriptForResume(resumeID string) string {
	return transcript.FindCodexTranscriptForResume(resumeID)
}

func (c *Codex) BootstrapBytes() int64 {
	return 256 * 1024
}

func (c *Codex) RecoveredRunningState(ptyState string) protocol.SessionState {
	return protocol.SessionStateLaunching
}

func (c *Codex) ShouldApplyPTYState(current protocol.SessionState, incoming string) bool {
	// Codex live state is hook-owned, with one exception: no hook fires when the
	// user approves a permission request, so the approval prompt leaving the
	// rendered screen is the only signal the tool is now running. Allow that
	// single pending_approval -> working transition; ignore all other PTY state.
	return current == protocol.SessionStatePendingApproval && incoming == protocol.StateWorking
}

func (c *Codex) ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	requested := strings.TrimSpace(requestedResumeID)
	stored := strings.TrimSpace(storedResumeID)
	if stored != "" && (requested == "" || requested == strings.TrimSpace(existingSessionID)) {
		return stored
	}
	return requested
}

func (c *Codex) SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string {
	return strings.TrimSpace(resolvedResumeID)
}

func (c *Codex) ResumeSessionIDFromStopTranscriptPath(transcriptPath string) string {
	return ""
}

// --- ClassifierProvider ---

func (c *Codex) Classify(text string, timeout time.Duration) (string, error) {
	return c.ClassifyWithExecutable(text, "", "", timeout)
}

func (c *Codex) ClassifyWithExecutable(text, executable, workDir string, timeout time.Duration) (string, error) {
	return classifier.ClassifyWithCodexExecutableInDir(text, executable, workDir, timeout)
}
