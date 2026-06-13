package agent

import (
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
	// A chief-of-staff launch (NotebookRoot set) gets Notebook guidance instead
	// of the workspace-context checkout guidance.
	if guidance := hooks.NotebookGuidance(opts.NotebookRoot); guidance != "" {
		args = append(args, "--append-system-prompt", guidance)
	} else if guidance := hooks.WorkspaceContextGuidance(opts.WorkspaceContextPath); guidance != "" {
		args = append(args, "--append-system-prompt", guidance)
	}

	if opts.ResumeSessionID != "" {
		args = append(args, "-r", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "-r")
	}
	if opts.YoloMode {
		args = append(args, "--dangerously-skip-permissions")
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}

	return exec.Command(opts.Executable, args...)
}

func (c *Claude) BuildEnv(opts SpawnOpts) []string {
	var env []string
	if strings.TrimSpace(opts.NotebookRoot) != "" {
		// A chief launch injected Notebook guidance at launch; mark it so the
		// SessionStart hook does not also emit workspace-context guidance.
		env = append(env, "ATTN_NOTEBOOK_GUIDANCE=append_system_prompt")
	} else if strings.TrimSpace(opts.WorkspaceContextPath) != "" {
		env = append(env, "ATTN_WORKSPACE_CONTEXT_GUIDANCE=append_system_prompt")
	}
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

func (c *Claude) RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error) {
	serverName := strings.TrimSpace(request.MCPServerName)
	if serverName == "" {
		serverName = "attn_context"
	}
	config, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			serverName: map[string]any{
				"type":    "stdio",
				"command": request.MCPServerCommand,
				"args":    request.MCPServerArgs,
			},
		},
	})
	if err != nil {
		return HeadlessTaskResult{}, fmt.Errorf("encode MCP config: %w", err)
	}
	toolPrefix := "mcp__" + serverName + "__"
	tools := toolPrefix + "read_context," + toolPrefix + "replace_context"
	args := []string{"--print"}
	args = append(args, claudeHeadlessIsolationArgs()...)
	args = append(args,
		"--model", strings.TrimSpace(request.Model),
		"--no-session-persistence",
		"--strict-mcp-config",
		"--mcp-config", string(config),
		"--disable-slash-commands",
		"--no-chrome",
		"--tools", tools,
		"--allowedTools", tools,
		"--permission-mode", "dontAsk",
		"--output-format", "json",
		request.Prompt,
	)
	return runHeadlessCommand(ctx, request.Executable, args, request.WorkDir, "claude")
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
