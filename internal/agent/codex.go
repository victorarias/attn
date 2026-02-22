package agent

import (
	"os/exec"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/transcript"
)

// Codex implements Driver and optional capabilities for OpenAI Codex CLI.
type Codex struct{}

var _ Driver = (*Codex)(nil)
var _ TranscriptFinder = (*Codex)(nil)
var _ ClassifierProvider = (*Codex)(nil)

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

func (c *Codex) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{}

	if opts.ResumeSessionID != "" {
		args = append(args, "resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "resume")
	}

	hasCwdFlag := false
	for i := 0; i < len(opts.AgentArgs); i++ {
		if opts.AgentArgs[i] == "-C" || opts.AgentArgs[i] == "--cd" {
			hasCwdFlag = true
			break
		}
	}
	if !hasCwdFlag {
		args = append(args, "-C", opts.CWD)
	}

	args = append(args, opts.AgentArgs...)
	return exec.Command(opts.Executable, args...)
}

func (c *Codex) BuildEnv(opts SpawnOpts) []string {
	var env []string
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
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

// --- ClassifierProvider ---

func (c *Codex) Classify(text string, timeout time.Duration) (string, error) {
	return classifier.ClassifyWithCodexExecutable(text, "", timeout)
}
