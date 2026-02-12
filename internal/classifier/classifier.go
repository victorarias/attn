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

var verdictLineRegex = regexp.MustCompile(`(?i)^\s*(?:VERDICT\s*[:=]\s*)?(WAITING_INPUT|WAITING|DONE|IDLE)(?:\s*(?:[-:]\s+.*|\([^)]*\)|[.!?]))?\s*$`)

const classifierLogSnippetMaxChars = 600

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

func logClaudeMessageDump(messages []types.Message) {
	if len(messages) == 0 {
		DefaultLogger("classifier: claude SDK message dump: empty messages slice")
		return
	}
	for i, msg := range messages {
		switch typed := msg.(type) {
		case *types.SystemMessage:
			dataJSON, _ := json.Marshal(typed.Data)
			DefaultLogger(
				"classifier: claude SDK message[%d/%d] system subtype=%q session_id=%q version=%q data=%s",
				i+1,
				len(messages),
				typed.Subtype,
				typed.SessionID,
				typed.Version,
				truncateForLog(string(dataJSON), classifierLogSnippetMaxChars),
			)
		case *types.AssistantMessage:
			toolNames := make([]string, 0, len(typed.ToolCalls()))
			for _, call := range typed.ToolCalls() {
				if call != nil {
					toolNames = append(toolNames, call.Name)
				}
			}
			DefaultLogger(
				"classifier: claude SDK message[%d/%d] assistant model=%q stop_reason=%q error=%v text=%q thinking_chars=%d tool_calls=%d tool_names=%q",
				i+1,
				len(messages),
				typed.Model,
				typed.StopReason,
				typed.Error,
				truncateForLog(typed.Text(), classifierLogSnippetMaxChars),
				len(typed.Thinking()),
				len(toolNames),
				toolNames,
			)
		case *types.ResultMessage:
			resultText := ""
			if typed.Result != nil {
				resultText = *typed.Result
			}
			structuredJSON, _ := json.Marshal(typed.StructuredOutput)
			DefaultLogger(
				"classifier: claude SDK message[%d/%d] result subtype=%q is_error=%v num_turns=%d duration_ms=%d duration_api_ms=%d result=%q structured_output=%s",
				i+1,
				len(messages),
				typed.Subtype,
				typed.IsError,
				typed.NumTurns,
				typed.DurationMS,
				typed.DurationAPI,
				truncateForLog(resultText, classifierLogSnippetMaxChars),
				truncateForLog(string(structuredJSON), classifierLogSnippetMaxChars),
			)
		default:
			payload, _ := json.Marshal(msg)
			DefaultLogger(
				"classifier: claude SDK message[%d/%d] type=%T payload=%s",
				i+1,
				len(messages),
				msg,
				truncateForLog(string(payload), classifierLogSnippetMaxChars),
			)
		}
	}
}

func classifyClaudeMessages(messages []types.Message) (result string, ok bool, lastAssistantResponse string) {
	var lastAssistantVerdict string

	for _, msg := range messages {
		switch typed := msg.(type) {
		case *types.AssistantMessage:
			assistantText := strings.TrimSpace(typed.Text())
			if assistantText == "" {
				continue
			}
			lastAssistantResponse = assistantText
			if parsed, parsedOK := parseVerdictFromResponse(assistantText); parsedOK {
				lastAssistantVerdict = parsed
			}
		case *types.ResultMessage:
			if parsed, parsedOK := parseVerdictFromValue(typed.StructuredOutput); parsedOK {
				DefaultLogger("classifier: parsed result from structured output: %s", parsed)
				return parsed, true, lastAssistantResponse
			}
			if typed.Result != nil {
				if parsed, parsedOK := parseVerdictFromResponse(*typed.Result); parsedOK {
					DefaultLogger("classifier: parsed result from result payload: %s", parsed)
					return parsed, true, lastAssistantResponse
				}
			}
		}
	}

	if lastAssistantVerdict != "" {
		DefaultLogger("classifier: parsed result from assistant message: %s", lastAssistantVerdict)
		return lastAssistantVerdict, true, lastAssistantResponse
	}

	return "", false, lastAssistantResponse
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
// Returns "waiting_input", "idle", or "unknown".
func Classify(text string, timeout time.Duration) (string, error) {
	return ClassifyWithClaude(text, timeout)
}

// ClassifyWithClaude uses Claude SDK (Haiku) to classify text.
// Returns "waiting_input", "idle", or "unknown".
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
		types.WithMaxTurns(2),
	)
	if err != nil {
		DefaultLogger("classifier: claude SDK error: %v", err)
		return "unknown", fmt.Errorf("claude sdk: %w", err)
	}

	result, ok, lastAssistantResponse := classifyClaudeMessages(messages)
	if ok {
		return result, nil
	}

	if lastAssistantResponse != "" {
		DefaultLogger("classifier: claude SDK response (%d chars): %q", len(lastAssistantResponse), lastAssistantResponse)
		DefaultLogger("classifier: response missing explicit WAITING/DONE verdict, returning unknown")
		logClaudeMessageDump(messages)
		return "unknown", nil
	}

	// No usable response found
	DefaultLogger("classifier: no assistant or structured result in claude response")
	logClaudeMessageDump(messages)
	return "unknown", nil
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
