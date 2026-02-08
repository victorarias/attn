package classifier

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/victorarias/claude-agent-sdk-go/sdk"
	"github.com/victorarias/claude-agent-sdk-go/types"
)

const promptTemplate = `Analyze this text from an AI assistant and determine if it's waiting for user input.

Reply with exactly one word: WAITING or DONE

WAITING means:
- Asks a question
- Requests clarification
- Offers choices requiring selection
- Asks for confirmation to proceed

DONE means:
- States completion
- Provides information without asking
- Reports results
- No question or request for input

Text to analyze:
"""
%s
"""
`

// BuildPrompt creates the classification prompt
func BuildPrompt(text string) string {
	return fmt.Sprintf(promptTemplate, text)
}

// ParseResponse parses the LLM response into a state
func ParseResponse(response string) string {
	lastLine := ""
	for _, line := range strings.Split(strings.TrimSpace(response), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lastLine = trimmed
		}
	}
	if lastLine == "" {
		return "idle"
	}

	normalized := strings.ToUpper(lastLine)
	if strings.HasPrefix(normalized, "WAITING") {
		return "waiting_input"
	}
	if strings.HasPrefix(normalized, "DONE") {
		return "idle"
	}

	return "idle"
}

// LooksLikeWaitingInput returns true if plain-text heuristics strongly indicate
// the assistant is asking for user input.
func LooksLikeWaitingInput(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "?") {
		return true
	}

	phrases := []string{
		"let me know",
		"tell me",
		"what would you like",
		"what do you want",
		"what should",
		"how can i help",
		"can you",
		"could you",
		"do you want",
		"want me to",
		"which one",
		"choose",
		"select",
		"confirm",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// LogFunc is a function type for logging
type LogFunc func(format string, args ...interface{})

// DefaultLogger is a no-op logger
var DefaultLogger LogFunc = func(format string, args ...interface{}) {}

// SetLogger sets the logger function
func SetLogger(fn LogFunc) {
	DefaultLogger = fn
}

// Classify uses the default classifier backend (Claude SDK).
// Returns "waiting_input" or "idle"
func Classify(text string, timeout time.Duration) (string, error) {
	return ClassifyWithClaude(text, timeout)
}

// ClassifyWithClaude uses Claude SDK (Haiku) to classify text.
// Returns "waiting_input" or "idle".
func ClassifyWithClaude(text string, timeout time.Duration) (string, error) {
	if text == "" {
		DefaultLogger("classifier: empty text, returning idle")
		return "idle", nil
	}

	// Truncate text for logging (first 200 chars)
	logText := text
	if len(logText) > 200 {
		logText = logText[:200] + "..."
	}
	DefaultLogger("classifier: input text (truncated): %s", logText)

	prompt := BuildPrompt(text)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	model := strings.TrimSpace(os.Getenv("ATTN_CLAUDE_CLASSIFIER_MODEL"))
	if model == "" {
		model = "haiku"
	}

	DefaultLogger("classifier: calling claude SDK model=%s timeout=%d seconds", model, int(timeout.Seconds()))
	messages, err := sdk.RunQuery(ctx, prompt, types.WithModel(model))
	if err != nil {
		DefaultLogger("classifier: claude SDK error: %v", err)
		return "waiting_input", fmt.Errorf("claude sdk: %w", err)
	}

	// Extract text from AssistantMessage
	for _, msg := range messages {
		if m, ok := msg.(*types.AssistantMessage); ok {
			response := m.Text()
			DefaultLogger("classifier: claude SDK response: %s", strings.TrimSpace(response))

			result := ParseResponse(response)
			DefaultLogger("classifier: parsed result: %s", result)

			return result, nil
		}
	}

	// No assistant message found
	DefaultLogger("classifier: no assistant message in claude response")
	return "waiting_input", nil
}

// ClassifyWithCopilot uses Copilot CLI (Haiku model) to classify text.
// Returns "waiting_input" or "idle".
func ClassifyWithCopilot(text string, timeout time.Duration) (string, error) {
	if text == "" {
		DefaultLogger("classifier: empty text, returning idle")
		return "idle", nil
	}

	// Truncate text for logging (first 200 chars)
	logText := text
	if len(logText) > 200 {
		logText = logText[:200] + "..."
	}
	DefaultLogger("classifier: input text (truncated): %s", logText)

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
		return "waiting_input", fmt.Errorf("copilot timeout: %w", ctx.Err())
	}
	if err != nil {
		if outputText != "" {
			DefaultLogger("classifier: copilot CLI error: %v output=%s", err, outputText)
		} else {
			DefaultLogger("classifier: copilot CLI error: %v", err)
		}
		return "waiting_input", fmt.Errorf("copilot cli: %w", err)
	}
	if outputText == "" {
		DefaultLogger("classifier: copilot CLI returned empty response")
		return "waiting_input", nil
	}

	DefaultLogger("classifier: copilot CLI response: %s", outputText)

	result := ParseResponse(outputText)
	DefaultLogger("classifier: parsed result: %s", result)
	return result, nil
}
