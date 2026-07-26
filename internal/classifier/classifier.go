package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const promptTemplate = `Classify whether this assistant message is waiting for user input.

Return STRICT JSON only, matching exactly one of:
{"verdict":"WAITING"}
{"verdict":"DONE"}

Decision rules (in order):
1) WAITING if the assistant asks the user any direct question.
2) WAITING if the assistant asks for confirmation, permission, choice, clarification, or next direction.
3) DONE only if the assistant message is complete and does not ask the user for anything.

Examples:
- "Hello! What can I help you with today?" -> WAITING
- "Would you like me to continue?" -> WAITING
- "I finished the task and saved the file." -> DONE
- "I'm here whenever you need me." -> DONE

Text to analyze:
"""
%s
"""
`

// ClaudeVerdictSchema is the JSON Schema the Claude backend's final answer must
// validate against (passed to the CLI as --json-schema). The validated object
// comes back as the run's structured output; ParseVerdict reads it.
const ClaudeVerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["WAITING","DONE"]}},"required":["verdict"],"additionalProperties":false}`

// ClaudeMaxTurns caps the Claude backend's agentic turns. The run is a tool-less
// single-shot judgment; the cap is a runaway backstop, and 2 leaves room for the
// structured-output turn after the answer.
const ClaudeMaxTurns = 2

// DefaultClaudeClassifierModel is the model the Claude backend classifies with
// when ATTN_CLAUDE_CLASSIFIER_MODEL is unset.
const DefaultClaudeClassifierModel = "haiku"

var verdictLineRegex = regexp.MustCompile(`(?i)^\s*(?:VERDICT\s*[:=]\s*)?(WAITING_INPUT|WAITING|DONE|IDLE)(?:\s*(?:[-:]\s+.*|\([^)]*\)|[.!?]))?\s*$`)

const classifierLogSnippetMaxChars = 600

const (
	defaultCodexClassifierModel   = "gpt-5.4-mini"
	defaultCodexReasoningEffort   = "low"
	defaultCodexClassifierTimeout = 30 * time.Second
	defaultCodexExecutable        = "codex"
	codexConfigReasoningEffortKey = "model_reasoning_effort"
	// codexConfigDisableShellToolKV removes the built-in shell tool: the
	// classifier only needs the model to emit a verdict, never to run commands.
	codexConfigDisableShellToolKV = "features.shell_tool=false"
)

type codexEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Item    struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// BuildPrompt creates the classification prompt
func BuildPrompt(text string) string {
	return fmt.Sprintf(promptTemplate, text)
}

func parseVerdictToken(value string) (string, bool) {
	match := verdictLineRegex.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) < 2 {
		return "", false
	}

	switch strings.ToUpper(match[1]) {
	case "WAITING", "WAITING_INPUT":
		return "waiting_input", true
	case "DONE", "IDLE":
		return "idle", true
	default:
		return "", false
	}
}

func parseVerdictFromValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return parseVerdictToken(typed)
	case map[string]any:
		for _, key := range []string{"verdict", "state", "status"} {
			if raw, ok := typed[key]; ok {
				if result, ok := parseVerdictFromValue(raw); ok {
					return result, true
				}
			}
		}
		if raw, ok := typed["classification"]; ok {
			if result, ok := parseVerdictFromValue(raw); ok {
				return result, true
			}
		}
		if raw, ok := typed["needs_input"]; ok {
			if needsInput, ok := raw.(bool); ok {
				if needsInput {
					return "waiting_input", true
				}
				return "idle", true
			}
		}
	}
	return "", false
}

func parseVerdictFromJSONResponse(response string) (string, bool) {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return "", false
	}

	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parseVerdictFromValue(parsed)
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(trimmed[start:end+1]), &parsed); err == nil {
			return parseVerdictFromValue(parsed)
		}
	}

	return "", false
}

func parseVerdictFromResponse(response string) (string, bool) {
	if result, ok := parseVerdictFromJSONResponse(response); ok {
		return result, true
	}

	for _, line := range strings.Split(strings.TrimSpace(response), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if result, ok := parseVerdictToken(trimmed); ok {
			return result, true
		}
	}

	return "", false
}

func truncateForLog(value string, maxChars int) string {
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[:maxChars] + "...(truncated)"
}

// ClaudeClassifierModel resolves the model the Claude backend classifies with.
func ClaudeClassifierModel() string {
	if model := strings.TrimSpace(os.Getenv("ATTN_CLAUDE_CLASSIFIER_MODEL")); model != "" {
		return model
	}
	return DefaultClaudeClassifierModel
}

// ParseVerdict maps one headless Claude run's output to a state. The
// schema-validated structured output wins when it carries a verdict; the final
// assistant text is the fallback for a run that answered in prose (or ended
// before the structured-output turn). Returns ok=false when neither carries an
// explicit verdict — callers report "unknown" rather than guessing.
func ParseVerdict(structuredOutput json.RawMessage, finalText string) (string, bool) {
	if len(structuredOutput) > 0 {
		if result, ok := parseVerdictFromJSONResponse(string(structuredOutput)); ok {
			DefaultLogger("classifier: parsed result from structured output: %s", result)
			return result, true
		}
	}
	if result, ok := parseVerdictFromResponse(finalText); ok {
		DefaultLogger("classifier: parsed result from final text: %s", result)
		return result, true
	}
	return "", false
}

// TruncateForLog bounds a value echoed into the daemon log.
func TruncateForLog(value string) string {
	return truncateForLog(value, classifierLogSnippetMaxChars)
}

func resolveCodexExecutable(configuredExecutable string) string {
	if envExecutable := strings.TrimSpace(os.Getenv("ATTN_CODEX_EXECUTABLE")); envExecutable != "" {
		return envExecutable
	}
	if configuredExecutable := strings.TrimSpace(configuredExecutable); configuredExecutable != "" {
		return configuredExecutable
	}
	return defaultCodexExecutable
}

func parseCodexErrorFromJSONL(output []byte) string {
	for _, rawLine := range bytes.Split(output, []byte{'\n'}) {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}
		var evt codexEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "error":
			return strings.TrimSpace(evt.Message)
		case "turn.failed":
			return strings.TrimSpace(evt.Error.Message)
		}
	}
	return ""
}

func parseVerdictFromCodexJSONL(output []byte) (string, bool) {
	for _, rawLine := range bytes.Split(output, []byte{'\n'}) {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}

		var evt codexEvent
		if err := json.Unmarshal([]byte(line), &evt); err == nil {
			if evt.Item.Text != "" {
				if result, ok := parseVerdictFromResponse(evt.Item.Text); ok {
					return result, true
				}
			}
			if evt.Message != "" {
				if result, ok := parseVerdictFromResponse(evt.Message); ok {
					return result, true
				}
			}
		}

		if result, ok := parseVerdictFromResponse(line); ok {
			return result, true
		}
	}
	return "", false
}

func runCodexClassifierAttempt(ctx context.Context, executable, model, reasoningEffort, prompt, workDir string) (string, string, error) {
	tempDir, err := os.MkdirTemp("", "attn-codex-classifier-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tempDir)

	lastMessagePath := filepath.Join(tempDir, "last-message.txt")
	args := []string{
		"exec",
		"--json",
		"--output-last-message", lastMessagePath,
		// Classify regardless of the session's cwd. Codex refuses `exec` outside a
		// trusted git repo otherwise, which is how a codex turn that ends in an
		// untrusted dir (e.g. /tmp) gets misclassified as unknown.
		"--skip-git-repo-check",
		// Ignore the user's config.toml entirely: the classifier must not inherit
		// their MCP servers (which add startup latency, auth noise, and tool-schema
		// tokens) or other agent settings. Auth is read separately and still works.
		"--ignore-user-config",
		"-m", model,
		"-c", fmt.Sprintf("%s=%q", codexConfigReasoningEffortKey, reasoningEffort),
		"-c", codexConfigDisableShellToolKV,
		prompt,
	}

	cmd := exec.CommandContext(ctx, executable, args...)
	if wd := strings.TrimSpace(workDir); wd != "" {
		cmd.Dir = wd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	lastMessageBytes, readErr := os.ReadFile(lastMessagePath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", stdout.String(), readErr
	}
	lastMessage := strings.TrimSpace(string(lastMessageBytes))
	if err == nil {
		return lastMessage, stdout.String(), nil
	}

	if parsedErr := parseCodexErrorFromJSONL(stdout.Bytes()); parsedErr != "" {
		return lastMessage, stdout.String(), fmt.Errorf("%w: %s", err, parsedErr)
	}
	stderrText := strings.TrimSpace(stderr.String())
	if stderrText != "" {
		return lastMessage, stdout.String(), fmt.Errorf("%w: %s", err, stderrText)
	}
	return lastMessage, stdout.String(), err
}

// ParseResponse parses the LLM response into a state
func ParseResponse(response string) string {
	if result, ok := parseVerdictFromResponse(response); ok {
		return result
	}

	return "idle"
}

// LogFunc is a function type for logging
type LogFunc func(format string, args ...interface{})

// DefaultLogger is a no-op logger
var DefaultLogger LogFunc = func(format string, args ...interface{}) {}

// SetLogger sets the logger function
func SetLogger(fn LogFunc) {
	DefaultLogger = fn
}

// ClassifyWithCopilot uses Copilot CLI (Haiku model) to classify text.
// Returns "waiting_input", "idle", or "unknown".
func ClassifyWithCopilot(text string, timeout time.Duration) (string, error) {
	if text == "" {
		DefaultLogger("classifier: empty text, returning idle")
		return "idle", nil
	}
	DefaultLogger("classifier: input text (%d chars): %q", len(text), text)

	prompt := BuildPrompt(text)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	executable := strings.TrimSpace(os.Getenv("ATTN_COPILOT_EXECUTABLE"))
	if executable == "" {
		executable = "copilot"
	}
	model := strings.TrimSpace(os.Getenv("ATTN_COPILOT_CLASSIFIER_MODEL"))
	if model == "" {
		model = "claude-haiku-4.5"
	}

	DefaultLogger(
		"classifier: calling copilot CLI executable=%s model=%s timeout=%d seconds",
		executable,
		model,
		int(timeout.Seconds()),
	)

	args := []string{
		"-p", prompt,
		"-s",
		"--model", model,
		"--no-color",
		"--no-custom-instructions",
	}
	// Use an isolated working directory so classifier runs do not overlap
	// with interactive Copilot session cwd-based transcript discovery.
	workDir, err := os.MkdirTemp("", "attn-copilot-classifier-*")
	if err == nil {
		defer os.RemoveAll(workDir)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	output, err := cmd.CombinedOutput()
	outputText := strings.TrimSpace(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		DefaultLogger("classifier: timeout reached while calling copilot")
		return "unknown", fmt.Errorf("copilot timeout: %w", ctx.Err())
	}
	if err != nil {
		if outputText != "" {
			DefaultLogger("classifier: copilot CLI error: %v output=%s", err, outputText)
		} else {
			DefaultLogger("classifier: copilot CLI error: %v", err)
		}
		return "unknown", fmt.Errorf("copilot cli: %w", err)
	}
	if outputText == "" {
		DefaultLogger("classifier: copilot CLI returned empty response")
		return "unknown", nil
	}

	DefaultLogger("classifier: copilot CLI response (%d chars): %q", len(outputText), outputText)

	result, ok := parseVerdictFromResponse(outputText)
	if !ok {
		DefaultLogger("classifier: copilot response missing explicit WAITING/DONE verdict, returning unknown")
		return "unknown", nil
	}
	DefaultLogger("classifier: parsed result: %s", result)
	return result, nil
}

// ClassifyWithCodex uses Codex CLI with a single model (default gpt-5.4-mini,
// low effort; override via ATTN_CODEX_CLASSIFIER_MODEL).
// Returns "waiting_input", "idle", or "unknown".
func ClassifyWithCodex(text string, timeout time.Duration) (string, error) {
	return ClassifyWithCodexExecutable(text, "", timeout)
}

// ClassifyWithCodexExecutable uses Codex CLI with a single model (default
// gpt-5.4-mini, low effort; override via ATTN_CODEX_CLASSIFIER_MODEL).
// Executable resolution order:
// 1) ATTN_CODEX_EXECUTABLE env var
// 2) configuredExecutable argument
// 3) "codex"
// Returns "waiting_input", "idle", or "unknown".
func ClassifyWithCodexExecutable(text, configuredExecutable string, timeout time.Duration) (string, error) {
	return ClassifyWithCodexExecutableInDir(text, configuredExecutable, "", timeout)
}

// ClassifyWithCodexExecutableInDir is like ClassifyWithCodexExecutable but runs
// the Codex subprocess from the provided working directory when one is set.
func ClassifyWithCodexExecutableInDir(text, configuredExecutable, workDir string, timeout time.Duration) (string, error) {
	if text == "" {
		DefaultLogger("classifier: empty text, returning idle")
		return "idle", nil
	}
	DefaultLogger("classifier: input text (%d chars): %q", len(text), text)

	if timeout <= 0 {
		timeout = defaultCodexClassifierTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	executable := resolveCodexExecutable(configuredExecutable)
	reasoningEffort := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_CODEX_CLASSIFIER_REASONING_EFFORT")))
	if reasoningEffort == "" {
		reasoningEffort = defaultCodexReasoningEffort
	}
	model := strings.TrimSpace(os.Getenv("ATTN_CODEX_CLASSIFIER_MODEL"))
	if model == "" {
		model = defaultCodexClassifierModel
	}

	prompt := BuildPrompt(text)
	DefaultLogger(
		"classifier: calling codex CLI executable=%s model=%s reasoning_effort=%s timeout=%d seconds work_dir=%q",
		executable,
		model,
		reasoningEffort,
		int(timeout.Seconds()),
		strings.TrimSpace(workDir),
	)

	lastMessage, rawJSONL, err := runCodexClassifierAttempt(ctx, executable, model, reasoningEffort, prompt, workDir)
	if err != nil {
		DefaultLogger("classifier: codex CLI failed model=%s err=%v", model, err)
		return "unknown", fmt.Errorf("codex cli: %w", err)
	}

	if lastMessage != "" {
		DefaultLogger("classifier: codex CLI last message (%d chars): %q", len(lastMessage), lastMessage)
		if result, ok := parseVerdictFromResponse(lastMessage); ok {
			DefaultLogger("classifier: parsed result: %s", result)
			return result, nil
		}
	}

	if result, ok := parseVerdictFromCodexJSONL([]byte(rawJSONL)); ok {
		DefaultLogger("classifier: parsed result from codex json stream: %s", result)
		return result, nil
	}
	DefaultLogger("classifier: codex response missing explicit WAITING/DONE verdict, returning unknown")
	return "unknown", nil
}
