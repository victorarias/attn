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
var _ RecoveredStatePolicyProvider = (*Codex)(nil)
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
	// Transcripts back Stop-hook classification; live state is hook-owned. The
	// watcher exists only for a turn the user halted (codex's `turn_aborted`),
	// which the hooks never report.
	return Capabilities{
		HasHooks:             true,
		HasTranscript:        true,
		HasTranscriptWatcher: true,
		HasClassifier:        true,
		HarnessSignals:       HarnessSignalsCodex,
		HasResume:            true,
		HasYolo:              true,
		HasInitialPrompt:     true,
		HasWorkspaceContext:  true,
		HasModelPin:          true,
		HasEffortPin:         true,
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
	// A woken crew member's awareness dirs, which codex spells the same way
	// claude does.
	args = append(args, opts.addDirArgs()...)
	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	// The daemon owns who gets a cap; this only applies what it resolved.
	args = append(args, codexContextWindowCapArgs(opts.AutoCompactWindow)...)
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		// No dedicated effort flag; -c values are parsed as TOML, hence the quotes.
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	if opts.YoloMode {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else if opts.AutoApprove {
		// Approvals route to codex's guardian LLM reviewer, not the user; yolo
		// bypasses both.
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
		// Keeps the SessionStart hook from also emitting workspace-context guidance.
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
	_, err := EnsureAgentsSkillInstalled()
	return err
}

func (c *Codex) RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error) {
	// Keeper/notebook tasks run native-tools; the workflow engine takes the
	// MCP-config path with a writable CWD+Sandbox.
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

	// The parser-free no-schema text path, rooted at WorkDir (NOT CWD) so the
	// working tree stays clean and cleanup is deterministic.
	lastMsgPath := ""
	if f, err := os.CreateTemp(headlessTempDir(request.WorkDir), "codex-last-msg-*.txt"); err == nil {
		lastMsgPath = f.Name()
		f.Close()
		defer os.Remove(lastMsgPath)
	}

	args := buildCodexHeadlessArgs(request, lastMsgPath, window)

	// CWD when set (the writable engine path), else the keeper's throwaway WorkDir.
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
// headless run. SECURITY BOUNDARY: the OS sandbox, not an approval prompt,
// confines a headless run — `approval_policy="never"` stays because no human is
// in the loop. "workspace-write" re-enables ONLY the sandbox mode and the shell
// tool; nothing here ever emits bypass-approvals or danger-full-access.
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
	// An empty "-m" makes codex reject the run; omitting it uses codex's default.
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
		// Every other feature stays OFF on BOTH paths.
		"-c", "features.unified_exec=false",
	)
	args = append(args, codexFeatureLocks()...)
	args = append(args, codexMCPServerArgs(serverName, request.MCPServerCommand, request.MCPServerArgs, toolNames)...)
	for _, spec := range request.ExtraMCPServers {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		args = append(args, codexMCPServerArgs(name, spec.Command, spec.Args, spec.EnabledTools)...)
	}
	args = append(args, codexContextWindowCapArgs(window)...)
	args = append(args, codexPrompt(request))
	return args
}

// codexFeatureLocks are the non-file feature locks, independent of the file/exec
// tooling the workspace-write sandbox enables.
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

// codexContextWindowCapArgs caps the effective context window by moving codex's
// auto-compaction threshold; the value is a TOML integer, so it is unquoted.
func codexContextWindowCapArgs(window int) []string {
	if window <= 0 {
		return nil
	}
	return []string{"-c", "model_auto_compact_token_limit=" + strconv.Itoa(window)}
}

// codexMCPServerArgs emits the `-c mcp_servers.<name>.*` argv pairs for one MCP
// server; primary and extra servers share it so their wiring cannot drift.
func codexMCPServerArgs(name, command string, cmdArgs, enabledTools []string) []string {
	return []string{
		"-c", fmt.Sprintf("mcp_servers.%s.command=%s", name, strconv.Quote(command)),
		"-c", fmt.Sprintf("mcp_servers.%s.args=%s", name, tomlStringArray(cmdArgs)),
		"-c", fmt.Sprintf("mcp_servers.%s.required=true", name),
		"-c", fmt.Sprintf("mcp_servers.%s.enabled_tools=%s", name, tomlStringArray(enabledTools)),
		"-c", fmt.Sprintf(`mcp_servers.%s.default_tools_approval_mode="approve"`, name),
	}
}

// tomlStringArray renders a string slice as a TOML inline array, each element
// double-quoted, for `codex -c key=[...]` overrides.
func tomlStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

// codexFinalText returns the child's final assistant message, preferring the
// --output-last-message file over stdout. The live `codex exec --json` stream is
// NOT the on-disk transcript envelope, so internal/transcript does not apply.
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

// codexHeadlessArgs builds the native-tools arg set: workspace-write makes the
// scratch WorkDir writable, and approval_policy="never" keeps it non-interactive.
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
	// Write-only widening for tasks that write outside cwd; reads are unrestricted.
	for _, root := range request.ExtraWritableRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		args = append(args, "--add-dir", root)
	}
	args = append(args, codexFeatureLocks()...)
	args = append(args, codexContextWindowCapArgs(window)...)
	args = append(args, codexPrompt(request))
	return args
}

// codexPrompt folds SystemPrompt into the prompt: `codex exec` has no
// system-prompt flag, so a split prompt would otherwise lose its invariant half.
func codexPrompt(request HeadlessTaskRequest) string {
	system := strings.TrimSpace(request.SystemPrompt)
	if system == "" {
		return request.Prompt
	}
	return system + "\n\n" + request.Prompt
}

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
	args = append(args, codexPrompt(request))
	return args
}

// --- ConfigOverrideProvider ---

func (c *Codex) GenerateConfigOverrides(opts SpawnOpts) []string {
	overrides := hooks.GenerateCodexConfigOverrides(
		opts.SessionID,
		opts.SocketPath,
		opts.WrapperPath,
		hooks.Launch{
			NotebookRoot:         opts.NotebookRoot,
			WorkspaceContextPath: opts.WorkspaceContextPath,
			InjectWorkflow:       opts.InjectWorkflowGuidance,
			Garden:               opts.Garden,
			Crew:                 opts.CrewPriming,
		},
	)
	if opts.TrustWorkingDirectory {
		overrides = append(overrides, fmt.Sprintf(`projects.%s.trust_level="trusted"`, strconv.Quote(opts.CWD)))
	}
	return overrides
}

// --- TranscriptFinder ---

func (c *Codex) FindTranscript(sessionID, cwd string, startedAt time.Time) string {
	return transcript.FindCodexTranscript(cwd, startedAt)
}

func (c *Codex) FindTranscriptForResume(resumeID string) string {
	return transcript.FindCodexTranscriptForResume(resumeID)
}

// ResumeAvailable reports whether Codex still has the recorded rollout;
// unattended continuation must fail closed when it was pruned or moved.
func (c *Codex) ResumeAvailable(resumeID string) bool {
	return transcript.FindCodexTranscriptForResume(resumeID) != ""
}

func (c *Codex) BootstrapBytes() int64 {
	return 256 * 1024
}

func (c *Codex) NewTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &codexTranscriptWatcherBehavior{}
}

// RecoveredRunningState is always no-opinion: codex announces approvals in its
// title, so no codex worker ever caches a protocol state to recover.
func (c *Codex) RecoveredRunningState(ptyState string) (protocol.SessionState, bool) {
	return "", false
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
