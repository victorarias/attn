package agent

import (
	"os/exec"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// Copilot implements Driver and optional capabilities for GitHub Copilot CLI.
type Copilot struct{}

var _ Driver = (*Copilot)(nil)
var _ TranscriptFinder = (*Copilot)(nil)
var _ TranscriptWatcherBehaviorProvider = (*Copilot)(nil)
var _ ClassifierProvider = (*Copilot)(nil)
var _ PTYStatePolicyProvider = (*Copilot)(nil)

func init() {
	Register(&Copilot{})
}

func (c *Copilot) Name() string              { return "copilot" }
func (c *Copilot) DisplayName() string       { return "Copilot" }
func (c *Copilot) DefaultExecutable() string { return "copilot" }
func (c *Copilot) ExecutableEnvVar() string  { return "ATTN_COPILOT_EXECUTABLE" }

func (c *Copilot) ResolveExecutable(configured string) string {
	return resolveExec(c.ExecutableEnvVar(), configured, c.DefaultExecutable())
}

func (c *Copilot) Capabilities() Capabilities {
	return Capabilities{
		HasHooks:             false,
		HasTranscript:        true,
		HasTranscriptWatcher: true,
		HasClassifier:        true,
		HasStateDetector:     true,
		HasResume:            true,
		HasYolo:              true,
		HasInitialPrompt:     true,
	}
}

func (c *Copilot) BuildCommand(opts SpawnOpts) *exec.Cmd {
	// attn owns text selection and scroll handling itself (see GhosttyTerminal's
	// selection-drag and wheel logic). Copilot's TUI independently enables SGR
	// mouse tracking (DECSET 1000/1002/1003) in alt-screen mode, and attn's
	// "forward mouse to app" path has no fail-safe for a release event that
	// lands outside the terminal's own DOM node (unlike the hardened
	// text-selection path). When that happens, the dropped release leaves
	// Copilot's TUI believing the button is still held, producing a stuck
	// selection/drag, plus post-refresh scroll desync (see attn issue: stuck
	// selection / scroll-in-textarea after refresh, Copilot-only). Disabling
	// mouse support avoids the whole class of bug; attn's own mouse handling
	// covers selection and scrolling regardless.
	args := []string{"--no-mouse"}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "--resume")
	}
	if opts.YoloMode {
		args = append(args, "--yolo")
	}
	// Copilot's -i/--interactive starts an interactive session that auto-executes
	// the prompt and stays alive for steering — the model attn delegation needs,
	// matching how claude/codex keep an interactive session after their initial
	// prompt. Do NOT use -p/--prompt: that runs non-interactively and exits after
	// completion, which would tear the delegated session down immediately.
	// Verified against copilot CLI v1.0.63 (`copilot --help`; example
	// `copilot -i "Fix the bug in main.js"`).
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--interactive", opts.InitialPrompt)
	}
	return exec.Command(opts.Executable, args...)
}

func (c *Copilot) BuildEnv(opts SpawnOpts) []string {
	var env []string
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

// --- TranscriptFinder ---

func (c *Copilot) FindTranscript(sessionID, cwd string, startedAt time.Time) string {
	return transcript.FindCopilotTranscript(cwd, startedAt)
}

func (c *Copilot) FindTranscriptForResume(resumeID string) string {
	return transcript.FindCopilotTranscriptForResume(resumeID)
}

func (c *Copilot) BootstrapBytes() int64 { return 512 * 1024 }

func (c *Copilot) NewTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &copilotTranscriptWatcherBehavior{}
}

func (c *Copilot) RecoveredRunningState(ptyState string) protocol.SessionState {
	switch ptyState {
	case protocol.StatePendingApproval:
		return protocol.SessionStatePendingApproval
	default:
		return protocol.SessionStateLaunching
	}
}

func (c *Copilot) ShouldApplyPTYState(current protocol.SessionState, incoming string) bool {
	if incoming != protocol.StateWorking && incoming != protocol.StatePendingApproval {
		return false
	}
	if current == protocol.SessionStatePendingApproval && incoming == protocol.StateWorking {
		return false
	}
	return true
}

// --- ClassifierProvider ---

func (c *Copilot) Classify(text string, timeout time.Duration) (string, error) {
	return classifier.ClassifyWithCopilot(text, timeout)
}
