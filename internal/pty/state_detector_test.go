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

func TestClassifyState_CopilotPermissionPromptIsPendingApproval(t *testing.T) {
	text := `Count lines of Go code
find /Users/victor.arias/projects/conductor-bot -name "*.go" | head -20 | xargs wc -l | tail -1

Do you want to run this command?

1. Yes
2. Yes, and approve 'xargs' for the rest of the running session
3. No, and tell Copilot what to do differently (Esc to stop)

Confirm with number keys or up/down keys and Enter, Cancel with Esc`

	got := classifyState(text, defaultStateHeuristics)
	if got != statePendingApproval {
		t.Fatalf("classifyState() = %q, want %q", got, statePendingApproval)
	}
}

func TestClassifyState_NumberedListQuestionStaysWaitingInput(t *testing.T) {
	text := `I can do this a few ways. Choose one:
1. Quick patch
2. Full refactor
3. Explain tradeoffs
› `

	got := classifyState(text, defaultStateHeuristics)
	if got != stateWaitingInput {
		t.Fatalf("classifyState() = %q, want %q", got, stateWaitingInput)
	}
}

func TestClassifyState_CopilotAllowDirectoryAccessIsPendingApproval(t *testing.T) {
	text := `Allow directory access
Copilot is attempting to read the following path outside your allowed directory list.

/tmp/hello

Do you want to add these directories to the allowed list?

1. Yes
2. No (Esc)

up/down to navigate - Enter to select - Esc to cancel`

	got := classifyState(text, defaultStateHeuristics)
	if got != statePendingApproval {
		t.Fatalf("classifyState() = %q, want %q", got, statePendingApproval)
	}
}
