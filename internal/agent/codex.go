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
	if opts.YoloMode {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
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
	if strings.TrimSpace(opts.WorkspaceContextPath) != "" {
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
	serverName := strings.TrimSpace(request.MCPServerName)
	if serverName == "" {
		serverName = "attn_context"
	}
	toolNames := headlessToolNames(request.ToolName)

	// Capture the child's final message to a file. This is the parser-free,
	// robust no-schema text path (codex -o, --output-last-message <FILE>). We
	// create the file ourselves so cleanup is deterministic; codex overwrites it.
	lastMsgPath := ""
	if f, err := os.CreateTemp(headlessTempDir(request.WorkDir), "codex-last-msg-*.txt"); err == nil {
		lastMsgPath = f.Name()
		f.Close()
		defer os.Remove(lastMsgPath)
	}

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
	}
	if lastMsgPath != "" {
		args = append(args, "--output-last-message", lastMsgPath)
	}
	args = append(args,
		"-c", `approval_policy="never"`,
		"-c", "features.shell_tool=false",
		"-c", "features.unified_exec=false",
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
		"-c", fmt.Sprintf("mcp_servers.%s.command=%s", serverName, strconv.Quote(request.MCPServerCommand)),
		"-c", fmt.Sprintf("mcp_servers.%s.args=%s", serverName, tomlStringArray(request.MCPServerArgs)),
		"-c", fmt.Sprintf("mcp_servers.%s.required=true", serverName),
		"-c", fmt.Sprintf("mcp_servers.%s.enabled_tools=%s", serverName, tomlStringArray(toolNames)),
		"-c", fmt.Sprintf(`mcp_servers.%s.default_tools_approval_mode="approve"`, serverName),
		request.Prompt,
	)
	result, stdout, err := runHeadlessCommand(ctx, request.Executable, args, request.WorkDir, "codex")
	if err != nil {
		return result, err
	}
	result.Text = codexFinalText(lastMsgPath, stdout)
	return result, nil
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

func tomlStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

// --- ConfigOverrideProvider ---

func (c *Codex) GenerateConfigOverrides(opts SpawnOpts) []string {
	return hooks.GenerateCodexConfigOverrides(
		opts.SessionID,
		opts.SocketPath,
		opts.WrapperPath,
		opts.WorkspaceContextPath,
	)
}

// --- TranscriptFinder ---

func (c *Codex) FindTranscript(sessionID, cwd string, startedAt time.Time) string {
	return transcript.FindCodexTranscript(cwd, startedAt)
}

func (c *Codex) FindTranscriptForResume(resumeID string) string {
	return "" // Codex doesn't support resume-id transcript lookup.
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
