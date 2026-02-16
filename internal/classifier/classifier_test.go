package classifier

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/claude-agent-sdk-go/types"
)

func TestParseResponse_Waiting(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"WAITING", "waiting_input"},
		{"waiting", "waiting_input"},
		{"WAITING\n", "waiting_input"},
		{"  WAITING  ", "waiting_input"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseResponse_Done(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"DONE", "idle"},
		{"done", "idle"},
		{"DONE\n", "idle"},
		{"anything else", "idle"},
		{"", "idle"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

// Additional edge case tests for comprehensive coverage

func TestParseResponse_MixedCase(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"Waiting", "waiting_input"},
		{"WaItInG", "waiting_input"},
		{"Done", "idle"},
		{"DoNe", "idle"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseResponse_SurroundingText(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{"WAITING line prefix", "WAITING - asks a follow-up", "waiting_input"},
		{"multi-line waiting with rationale", "WAITING\nThe text ends with a direct question.", "waiting_input"},
		{"verdict label with done", "Verdict: DONE", "idle"},
		{"verdict label with waiting", "verdict = waiting", "waiting_input"},
		{"DONE line prefix", "DONE (completed)", "idle"},
		{"multi-line with final verdict", "analysis...\nDONE", "idle"},
		{"sentence without explicit verdict prefix", "The assistant is waiting for user input", "idle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResponse(tt.response)
			if got != tt.want {
				t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
			}
		})
	}
}

func TestParseResponse_NoStandaloneToken(t *testing.T) {
	got := ParseResponse("This appears complete without further input.")
	if got != "idle" {
		t.Errorf("expected idle when no WAITING/DONE prefix, got %q", got)
	}
}

func TestParseResponse_JSONStructured(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{"json verdict waiting", `{"verdict":"WAITING"}`, "waiting_input"},
		{"json state done", `{"state":"DONE"}`, "idle"},
		{"json status idle", `{"status":"IDLE"}`, "idle"},
		{"json needs_input true", `{"needs_input":true}`, "waiting_input"},
		{"fenced json verdict waiting", "```json\n{\"verdict\":\"WAITING\"}\n```", "waiting_input"},
		{"fenced json verdict done", "```json\n{\"verdict\":\"DONE\"}\n```", "idle"},
		{"bulleted rubric line then verdict", "- WAITING means asks a question\nVerdict: DONE", "idle"},
		{"invalid json no verdict", `{"foo":"bar"}`, "idle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResponse(tt.response)
			if got != tt.want {
				t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
			}
		})
	}
}

func TestParseVerdictFromResponse_ExplicitVerdictRequired(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
		ok       bool
	}{
		{"plain sentence no explicit verdict", "The assistant is waiting for user input", "", false},
		{"explicit waiting verdict", "WAITING\nbecause it asks a question", "waiting_input", true},
		{"explicit done verdict", "Verdict: DONE", "idle", true},
		{"json verdict", `{"verdict":"DONE"}`, "idle", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseVerdictFromResponse(tt.response)
			if ok != tt.ok {
				t.Fatalf("parseVerdictFromResponse(%q) ok=%v, want %v", tt.response, ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("parseVerdictFromResponse(%q) = %q, want %q", tt.response, got, tt.want)
			}
		})
	}
}

func TestParseResponse_EmptyAndWhitespace(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"", "idle"},
		{"   ", "idle"},
		{"\n\n", "idle"},
		{"\t\t", "idle"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestBuildPrompt(t *testing.T) {
	text := "Would you like me to continue?"
	prompt := BuildPrompt(text)

	if prompt == "" {
		t.Error("BuildPrompt returned empty string")
	}
	if !strings.Contains(prompt, text) {
		t.Error("BuildPrompt should include the input text")
	}
	if !strings.Contains(prompt, "WAITING") {
		t.Error("BuildPrompt should mention WAITING")
	}
	if !strings.Contains(prompt, "DONE") {
		t.Error("BuildPrompt should mention DONE")
	}
}

func TestBuildPrompt_ContainsRequiredElements(t *testing.T) {
	prompt := BuildPrompt("Test input text")

	// Verify prompt requests strict JSON response
	if !strings.Contains(prompt, "STRICT JSON") {
		t.Error("BuildPrompt should request strict JSON response")
	}
	if !strings.Contains(prompt, `{"verdict":"WAITING"}`) || !strings.Contains(prompt, `{"verdict":"DONE"}`) {
		t.Error("BuildPrompt should include exact JSON verdict formats")
	}

	// Verify prompt explains WAITING criteria
	if !strings.Contains(prompt, "question") {
		t.Error("BuildPrompt should explain WAITING criteria (asks a question)")
	}

	// Verify prompt explains DONE criteria in contrast with asking for user input
	if !strings.Contains(prompt, "DONE only") || !strings.Contains(prompt, "does not ask the user") {
		t.Error("BuildPrompt should explain DONE criteria")
	}

	// Verify prompt includes concrete examples for greeting questions
	if !strings.Contains(prompt, "What can I help you with today?") {
		t.Error("BuildPrompt should include greeting question example")
	}
}

func TestBuildPrompt_EmptyInput(t *testing.T) {
	prompt := BuildPrompt("")

	// Even with empty input, prompt should have structure
	if !strings.Contains(prompt, "WAITING") {
		t.Error("BuildPrompt should still contain WAITING instruction for empty input")
	}
	if !strings.Contains(prompt, "DONE") {
		t.Error("BuildPrompt should still contain DONE instruction for empty input")
	}
}

func TestClassify_EmptyText_ReturnsIdleImmediately(t *testing.T) {
	// This test verifies the early return path for empty text
	// The Classify function should return "idle" immediately without calling CLI
	result, err := Classify("", 0) // 0 timeout is fine because it should return immediately
	if err != nil {
		t.Errorf("Classify empty text unexpected error: %v", err)
	}
	if result != "idle" {
		t.Errorf("Classify empty text = %q, want 'idle'", result)
	}
}

func TestClassifyClaudeMessages_PreservesAssistantVerdictWhenLaterToolCallIsEmpty(t *testing.T) {
	messages := []types.Message{
		&types.AssistantMessage{
			Content: []types.ContentBlock{
				&types.TextBlock{TextContent: "WAITING"},
			},
		},
		&types.AssistantMessage{
			Content: []types.ContentBlock{
				&types.ToolUseBlock{Name: "StructuredOutput"},
			},
		},
		&types.ResultMessage{
			Subtype: "error_max_turns",
		},
	}

	result, ok, lastAssistant := classifyClaudeMessages(messages)
	if !ok {
		t.Fatal("expected classifyClaudeMessages to return a verdict")
	}
	if result != "waiting_input" {
		t.Fatalf("result = %q, want waiting_input", result)
	}
	if lastAssistant != "WAITING" {
		t.Fatalf("lastAssistant = %q, want WAITING", lastAssistant)
	}
}

func TestClassifyClaudeMessages_PrefersStructuredOutputOverAssistantText(t *testing.T) {
	messages := []types.Message{
		&types.AssistantMessage{
			Content: []types.ContentBlock{
				&types.TextBlock{TextContent: "WAITING"},
			},
		},
		&types.ResultMessage{
			StructuredOutput: map[string]any{"verdict": "DONE"},
		},
	}

	result, ok, _ := classifyClaudeMessages(messages)
	if !ok {
		t.Fatal("expected classifyClaudeMessages to return a verdict")
	}
	if result != "idle" {
		t.Fatalf("result = %q, want idle", result)
	}
}

func TestParseVerdictFromCodexJSONL(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"type":"thread.started","thread_id":"abc"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"verdict\":\"DONE\"}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1}}`,
	}, "\n")

	got, ok := parseVerdictFromCodexJSONL([]byte(jsonl))
	if !ok {
		t.Fatal("expected parseVerdictFromCodexJSONL to parse verdict")
	}
	if got != "idle" {
		t.Fatalf("parseVerdictFromCodexJSONL() = %q, want idle", got)
	}
}

func TestParseCodexErrorFromJSONL(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"type":"thread.started","thread_id":"abc"}`,
		`{"type":"error","message":"model_not_found"}`,
	}, "\n")
	got := parseCodexErrorFromJSONL([]byte(jsonl))
	if got != "model_not_found" {
		t.Fatalf("parseCodexErrorFromJSONL() = %q, want model_not_found", got)
	}
}

func TestParseCodexErrorFromJSONL_LargeLine(t *testing.T) {
	line, err := json.Marshal(map[string]any{
		"type":    "error",
		"message": strings.Repeat("x", 70*1024) + "model_not_found",
	})
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}

	got := parseCodexErrorFromJSONL(append(line, '\n'))
	if !strings.HasSuffix(got, "model_not_found") {
		t.Fatalf("parseCodexErrorFromJSONL() suffix mismatch: %q", got)
	}
}

func TestParseVerdictFromCodexJSONL_LargeLine(t *testing.T) {
	line, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_0",
			"type": "agent_message",
			"text": strings.Repeat("x", 70*1024) + `{"verdict":"DONE"}`,
		},
	})
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}

	got, ok := parseVerdictFromCodexJSONL(append(line, '\n'))
	if !ok {
		t.Fatal("expected parseVerdictFromCodexJSONL to parse verdict from large line")
	}
	if got != "idle" {
		t.Fatalf("parseVerdictFromCodexJSONL() = %q, want idle", got)
	}
}

func TestClassifyWithCodex_ModelFallbackAndLastMessageParse(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "invocations.log")
	scriptPath := filepath.Join(tmp, "codex")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
model=""
last=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -m)
      model="$2"
      shift 2
      ;;
    --output-last-message)
      last="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
echo "$model" >> %s
echo '{"type":"thread.started","thread_id":"abc"}'
echo '{"type":"turn.started"}'
if [ "$model" = "spark" ]; then
  echo '{"type":"error","message":"model_not_found"}'
  echo '{"type":"turn.failed","error":{"message":"model_not_found"}}'
  echo 'rollout noise' >&2
  exit 1
fi
printf '{"verdict":"DONE"}\n' > "$last"
echo '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"verdict\":\"DONE\"}"}}'
echo '{"type":"turn.completed"}'
`, shellEscapeSingleQuotes(logPath))

	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("ATTN_CODEX_EXECUTABLE", scriptPath)
	t.Setenv("ATTN_CODEX_CLASSIFIER_MODELS", "spark,base")
	t.Setenv("ATTN_CODEX_CLASSIFIER_REASONING_EFFORT", "low")

	got, err := ClassifyWithCodex("done text", 3*time.Second)
	if err != nil {
		t.Fatalf("ClassifyWithCodex unexpected err: %v", err)
	}
	if got != "idle" {
		t.Fatalf("ClassifyWithCodex() = %q, want idle", got)
	}

	invocationsRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	invocations := strings.Split(strings.TrimSpace(string(invocationsRaw)), "\n")
	if len(invocations) != 2 || invocations[0] != "spark" || invocations[1] != "base" {
		t.Fatalf("model invocations = %q, want [spark base]", invocations)
	}
}

func TestResolveCodexExecutable_Precedence(t *testing.T) {
	t.Setenv("ATTN_CODEX_EXECUTABLE", "/env/codex")
	if got := resolveCodexExecutable("/config/codex"); got != "/env/codex" {
		t.Fatalf("resolveCodexExecutable(env, config) = %q, want /env/codex", got)
	}

	t.Setenv("ATTN_CODEX_EXECUTABLE", "")
	if got := resolveCodexExecutable("/config/codex"); got != "/config/codex" {
		t.Fatalf("resolveCodexExecutable(config) = %q, want /config/codex", got)
	}

	if got := resolveCodexExecutable(""); got != "codex" {
		t.Fatalf("resolveCodexExecutable(default) = %q, want codex", got)
	}
}

func TestClassifyWithCodexExecutable_UsesConfiguredPathWhenEnvUnset(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "invocations.log")
	scriptPath := filepath.Join(tmp, "custom-codex")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
last=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message)
      last="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
echo called >> %s
printf '{"verdict":"DONE"}\n' > "$last"
echo '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"verdict\":\"DONE\"}"}}'
`, shellEscapeSingleQuotes(logPath))

	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("ATTN_CODEX_EXECUTABLE", "")
	t.Setenv("ATTN_CODEX_CLASSIFIER_MODELS", "base")
	t.Setenv("ATTN_CODEX_CLASSIFIER_REASONING_EFFORT", "low")

	got, err := ClassifyWithCodexExecutable("done text", scriptPath, 3*time.Second)
	if err != nil {
		t.Fatalf("ClassifyWithCodexExecutable unexpected err: %v", err)
	}
	if got != "idle" {
		t.Fatalf("ClassifyWithCodexExecutable() = %q, want idle", got)
	}

	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected configured codex executable to be invoked, stat err: %v", err)
	}
}

func shellEscapeSingleQuotes(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
