package pty

import (
	"testing"
	"time"
)

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

func TestCodexStateDetector_EmitsWorkingPulseForAnimationFrames(t *testing.T) {
	d := newCodexStateDetector()
	frame := []byte("\x1b[2m• working\x1b[0m\r")

	state, changed := d.Observe(frame)
	if !changed {
		t.Fatal("first animation frame should produce a state update")
	}
	if state != stateWorking {
		t.Fatalf("state=%q want=%q", state, stateWorking)
	}

	d.lastWorkingPulse = time.Now().Add(-workingPulseInterval - 50*time.Millisecond)
	state, changed = d.Observe(frame)
	if !changed {
		t.Fatal("animation heartbeat should emit a working pulse")
	}
	if state != stateWorking {
		t.Fatalf("state=%q want=%q", state, stateWorking)
	}
}

func TestLooksLikeWorkingAnimation(t *testing.T) {
	if !looksLikeWorkingAnimation("\x1b[2mworking on it\x1b[0m\r") {
		t.Fatal("ansi carriage-return frame should be treated as animation")
	}
	if looksLikeWorkingAnimation("\x1b[2mstatus line\x1b[0m\r") {
		t.Fatal("frames without working keywords should not be treated as animation")
	}
	if looksLikeWorkingAnimation("plain output\n") {
		t.Fatal("plain output should not be treated as animation")
	}
}

func TestClaudeWorkingDetector_EmitsWorkingPulseForStatusFrames(t *testing.T) {
	d := newClaudeWorkingDetector()
	frame := []byte("\x1b[35m✻\x1b[0m \x1b[36mMetamorphosing…\x1b[0m (3m 53s · ↓ 2.9k tokens)\r")

	state, changed := d.Observe(frame)
	if !changed {
		t.Fatal("first claude status frame should produce a state update")
	}
	if state != stateWorking {
		t.Fatalf("state=%q want=%q", state, stateWorking)
	}

	d.lastWorkingPulse = time.Now().Add(-workingPulseInterval - 50*time.Millisecond)
	state, changed = d.Observe(frame)
	if !changed {
		t.Fatal("claude status heartbeat should emit a working pulse")
	}
	if state != stateWorking {
		t.Fatalf("state=%q want=%q", state, stateWorking)
	}
}

func TestLooksLikeClaudeWorkingStatusFrame(t *testing.T) {
	if !looksLikeClaudeWorkingStatusFrame("\x1b[35m✻\x1b[0m \x1b[36mMetamorphosing…\x1b[0m (3m 53s · ↓ 2.9k tokens)\r") {
		t.Fatal("animated claude status line should be treated as working")
	}
	if looksLikeClaudeWorkingStatusFrame("\x1b[35m✻\x1b[0m Brewed for 3m 27s\n") {
		t.Fatal("final brewed summary must not be treated as working animation")
	}
	if looksLikeClaudeWorkingStatusFrame("\x1b[35m✻\x1b[0m Simmered for 3m 27s\r") {
		t.Fatal("final summary wording variants must not be treated as working animation")
	}
	if looksLikeClaudeWorkingStatusFrame("\x1b[35m✻\x1b[0m Metamorphosing…\r") {
		t.Fatal("status lines without timer should not be treated as working animation")
	}
}
