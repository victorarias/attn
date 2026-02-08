package pty

import "testing"

func TestClassifyState_PromptAtEndIsWaiting(t *testing.T) {
	text := "All done.\n› "
	got := classifyState(text, defaultStateHeuristics)
	if got != stateWaitingInput {
		t.Fatalf("classifyState() = %q, want %q", got, stateWaitingInput)
	}
}

func TestClassifyState_PromptNotLastStaysWorking(t *testing.T) {
	text := "› \nThinking through your request..."
	got := classifyState(text, defaultStateHeuristics)
	if got != stateWorking {
		t.Fatalf("classifyState() = %q, want %q", got, stateWorking)
	}
}
