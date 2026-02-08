package classifier

import (
	"strings"
	"testing"
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

	// Verify prompt asks for single word response
	if !strings.Contains(prompt, "one word") {
		t.Error("BuildPrompt should request single word response")
	}

	// Verify prompt explains WAITING criteria
	if !strings.Contains(prompt, "question") {
		t.Error("BuildPrompt should explain WAITING criteria (asks a question)")
	}

	// Verify prompt explains DONE criteria
	if !strings.Contains(prompt, "completion") || !strings.Contains(prompt, "results") {
		// Check for various ways of describing DONE criteria
		if !strings.Contains(prompt, "States completion") && !strings.Contains(prompt, "Reports results") {
			t.Error("BuildPrompt should explain DONE criteria")
		}
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

func TestLooksLikeWaitingInput(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"What should I do next?", true},
		{"Tell me what you want me to do next.", true},
		{"I'm here whenever you need anything.", false},
		{"Done. Created the file.", false},
	}

	for _, tt := range tests {
		if got := LooksLikeWaitingInput(tt.text); got != tt.want {
			t.Errorf("LooksLikeWaitingInput(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
