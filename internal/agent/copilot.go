package agent

import (
	"os/exec"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/transcript"
)

// Copilot implements Driver and optional capabilities for GitHub Copilot CLI.
type Copilot struct{}

var _ Driver = (*Copilot)(nil)
var _ TranscriptFinder = (*Copilot)(nil)
var _ ClassifierProvider = (*Copilot)(nil)

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
		HasFork:              false,
	}
}

func (c *Copilot) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "--resume")
	}
	args = append(args, opts.AgentArgs...)
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

// --- ClassifierProvider ---

func (c *Copilot) Classify(text string, timeout time.Duration) (string, error) {
	return classifier.ClassifyWithCopilot(text, timeout)
}
