package agent

import (
	"os/exec"
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
