package classifier

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	normalized := strings.TrimSpace(strings.ToUpper(response))
	if strings.Contains(normalized, "WAITING") {
		return "waiting_input"
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

// FindClaudePath searches for the claude binary in common locations.
// Returns the absolute path or an error if not found.
func FindClaudePath() (string, error) {
	// Try PATH first (works when running from shell)
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	// Common installation locations
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get home dir: %w", err)
	}

	candidates := []string{
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".claude", "local", "claude"),
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("claude binary not found in PATH or common locations")
}

// Classify calls Claude CLI to classify the text.
// Deprecated: Use ClassifyWithPath instead for daemon context.
// Returns "waiting_input" or "idle"
func Classify(text string, timeout time.Duration) (string, error) {
	claudePath, err := FindClaudePath()
	if err != nil {
		DefaultLogger("classifier: %v", err)
		return "waiting_input", err
	}
	return ClassifyWithPath(claudePath, text, timeout)
}

// ClassifyWithPath calls Claude CLI at the given path to classify the text.
// Returns "waiting_input" or "idle"
func ClassifyWithPath(claudePath, text string, timeout time.Duration) (string, error) {
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

	DefaultLogger("classifier: calling claude CLI with %d second timeout (using haiku)", int(timeout.Seconds()))
	cmd := exec.CommandContext(ctx, claudePath, "-p", prompt, "--print", "--model", "haiku", "--no-session-persistence")
	var stdout, stderr bytes.Buffer
	cmd.Stdin = nil // Explicitly close stdin to prevent hanging
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		DefaultLogger("classifier: claude CLI error: %v, stderr: %s, stdout: %s", err, stderr.String(), stdout.String())
		return "waiting_input", fmt.Errorf("claude cli: %w: %s", err, stderr.String())
	}

	response := stdout.String()
	DefaultLogger("classifier: claude CLI response: %s", strings.TrimSpace(response))

	result := ParseResponse(response)
	DefaultLogger("classifier: parsed result: %s", result)

	return result, nil
}
