package agent

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/hooks"
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
var _ ResumePolicyProvider = (*Claude)(nil)
var _ TranscriptClassificationExtractor = (*Claude)(nil)

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
		HasResume:            true,
		HasFork:              true,
	}
}

func (c *Claude) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{}

	// Claude forbids --session-id with --resume unless --fork-session is set.
	useSessionID := true
	if (opts.ResumeSessionID != "" || opts.ResumePicker) && !opts.ForkSession {
		useSessionID = false
	}
	if useSessionID {
		args = append(args, "--session-id", opts.SessionID)
	}

	if strings.TrimSpace(opts.SettingsPath) != "" {
		args = append(args, "--settings", opts.SettingsPath)
	}

	if opts.ResumeSessionID != "" {
		args = append(args, "-r", opts.ResumeSessionID)
		if opts.ForkSession {
			args = append(args, "--fork-session")
		}
	} else if opts.ResumePicker {
		args = append(args, "-r")
	}

	args = append(args, opts.AgentArgs...)
	return exec.Command(opts.Executable, args...)
}

func (c *Claude) BuildEnv(opts SpawnOpts) []string {
	var env []string
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

// PrepareLaunch copies resume transcripts into the target project folder so
// Claude can resolve --resume in fork/session-handoff scenarios.
func (c *Claude) PrepareLaunch(opts SpawnOpts) error {
	if strings.TrimSpace(opts.ResumeSessionID) == "" {
		return nil
	}
	return copyTranscriptForFork(opts.ResumeSessionID, opts.CWD)
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

func copyTranscriptForFork(parentSessionID, forkCwd string) error {
	srcPath := transcript.FindClaudeTranscript(parentSessionID)
	if srcPath == "" {
		return fmt.Errorf("parent transcript not found for session %s", parentSessionID)
	}

	destDir := claudeProjectDir(forkCwd)
	if destDir == "" {
		return fmt.Errorf("could not determine Claude project directory")
	}
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to create project directory: %w", err)
	}

	destPath := filepath.Join(destDir, parentSessionID+".jsonl")
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
