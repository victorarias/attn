package agent

import (
	"context"
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
	if strings.TrimSpace(opts.NotebookRoot) != "" {
		// A chief launch injected Notebook guidance at launch; mark it so the
		// SessionStart hook does not also emit workspace-context guidance.
		env = append(env, "ATTN_NOTEBOOK_GUIDANCE=developer_instructions")
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
	return runHeadlessCommand(ctx, request.Executable, codexHeadlessArgs(request), request.WorkDir, "codex")
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

// codexHeadlessArgs builds the native-tools arg set: a workspace-write sandbox
// makes cwd (the scratch WorkDir via cmd.Dir) writable, and Codex's default
// file/exec tooling is active. approval_policy="never" keeps writes autonomous
// and the run non-interactive.
func codexHeadlessArgs(request HeadlessTaskRequest) []string {
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
