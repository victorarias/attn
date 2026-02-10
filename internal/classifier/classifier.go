package classifier

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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

var classifierOutputFormat = map[string]any{
	"type": "json_schema",
	"schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdict": map[string]any{
				"type": "string",
				"enum": []string{"WAITING", "DONE"},
			},
		},
		"required":             []string{"verdict"},
		"additionalProperties": false,
	},
}

var verdictLineRegex = regexp.MustCompile(`(?i)^\s*(?:[-*>\d.)]+\s*)?(?:VERDICT\s*[:=]\s*)?(WAITING_INPUT|WAITING|DONE|IDLE)\b`)

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
	DefaultLogger("classifier: input text (%d chars): %q", len(text), text)

	prompt := BuildPrompt(text)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	model := strings.TrimSpace(os.Getenv("ATTN_CLAUDE_CLASSIFIER_MODEL"))
	if model == "" {
		model = "haiku"
	}

	DefaultLogger("classifier: calling claude SDK model=%s timeout=%d seconds", model, int(timeout.Seconds()))
	messages, err := sdk.RunQuery(
		ctx,
		prompt,
		types.WithModel(model),
		types.WithOutputFormat(classifierOutputFormat),
		types.WithMaxTurns(1),
	)
	if err != nil {
		DefaultLogger("classifier: claude SDK error: %v", err)
		return "waiting_input", fmt.Errorf("claude sdk: %w", err)
	}

	var lastAssistantResponse string
	for _, msg := range messages {
		switch typed := msg.(type) {
		case *types.AssistantMessage:
			lastAssistantResponse = typed.Text()
		case *types.ResultMessage:
			if result, ok := parseVerdictFromValue(typed.StructuredOutput); ok {
				DefaultLogger("classifier: parsed result from structured output: %s", result)
				return result, nil
			}
			if typed.Result != nil {
				if result, ok := parseVerdictFromResponse(*typed.Result); ok {
					DefaultLogger("classifier: parsed result from result payload: %s", result)
					return result, nil
				}
			}
		}
	}

	if lastAssistantResponse != "" {
		DefaultLogger("classifier: claude SDK response (%d chars): %q", len(lastAssistantResponse), lastAssistantResponse)
		result, ok := parseVerdictFromResponse(lastAssistantResponse)
		if !ok {
			DefaultLogger("classifier: response missing explicit WAITING/DONE verdict, defaulting to waiting_input")
			return "waiting_input", nil
		}
		DefaultLogger("classifier: parsed result: %s", result)
		return result, nil
	}

	// No usable response found
	DefaultLogger("classifier: no assistant or structured result in claude response")
	return "waiting_input", nil
}

// ClassifyWithCopilot uses Copilot CLI (Haiku model) to classify text.
// Returns "waiting_input" or "idle".
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

	DefaultLogger("classifier: copilot CLI response (%d chars): %q", len(outputText), outputText)

	result, ok := parseVerdictFromResponse(outputText)
	if !ok {
		DefaultLogger("classifier: copilot response missing explicit WAITING/DONE verdict, defaulting to waiting_input")
		return "waiting_input", nil
	}
	DefaultLogger("classifier: parsed result: %s", result)
	return result, nil
}
