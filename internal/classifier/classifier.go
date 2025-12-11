package classifier

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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

// Classify calls Claude CLI to classify the text
// Returns "waiting_input" or "idle"
func Classify(text string, timeout time.Duration) (string, error) {
	if text == "" {
		return "idle", nil
	}

	prompt := BuildPrompt(text)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--print")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "waiting_input", fmt.Errorf("claude cli: %w: %s", err, stderr.String())
	}

	return ParseResponse(stdout.String()), nil
}
