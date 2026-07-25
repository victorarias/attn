package pty

import (
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClassifyState_PromptAtEndIsWaiting(t *testing.T) {
	text := "All done.\n› "
	got := classifyState(text, defaultStateHeuristics)
	if got != protocol.StateWaitingInput {
		t.Fatalf("classifyState() = %q, want %q", got, protocol.StateWaitingInput)
	}
}

func TestClassifyState_PromptNotLastStaysWorking(t *testing.T) {
	text := "› \nThinking through your request..."
	got := classifyState(text, defaultStateHeuristics)
	if got != protocol.StateWorking {
		t.Fatalf("classifyState() = %q, want %q", got, protocol.StateWorking)
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
	if got != protocol.StatePendingApproval {
		t.Fatalf("classifyState() = %q, want %q", got, protocol.StatePendingApproval)
	}
}

func TestClassifyState_NumberedListQuestionStaysWaitingInput(t *testing.T) {
	text := `I can do this a few ways. Choose one:
1. Quick patch
2. Full refactor
3. Explain tradeoffs
› `

	got := classifyState(text, defaultStateHeuristics)
	if got != protocol.StateWaitingInput {
		t.Fatalf("classifyState() = %q, want %q", got, protocol.StateWaitingInput)
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
	if got != protocol.StatePendingApproval {
		t.Fatalf("classifyState() = %q, want %q", got, protocol.StatePendingApproval)
	}
}

func TestCopilotStateDetector_EmitsWorkingPulseForAnimationFrames(t *testing.T) {
	d := newCopilotStateDetector()
	frame := []byte("\x1b[2m• working\x1b[0m\r")

	state, changed := d.Observe(frame)
	if !changed {
		t.Fatal("first animation frame should produce a state update")
	}
	if state != protocol.StateWorking {
		t.Fatalf("state=%q want=%q", state, protocol.StateWorking)
	}

	d.lastWorkingPulse = time.Now().Add(-workingPulseInterval - 50*time.Millisecond)
	state, changed = d.Observe(frame)
	if !changed {
		t.Fatal("animation heartbeat should emit a working pulse")
	}
	if state != protocol.StateWorking {
		t.Fatalf("state=%q want=%q", state, protocol.StateWorking)
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

// TestNewScreenDetector_NoneIsNilInterface guards the typed-nil trap: the session
// wires its state callback on `detector != nil`, so returning a nil *screenDetector
// as a non-nil stateDetector would silently give a detector-less agent a detector
// that claims nothing.
func TestNewScreenDetector_NoneIsNilInterface(t *testing.T) {
	if d := newScreenDetector(agentdriver.ScreenDetectorNone); d != nil {
		t.Fatalf("newScreenDetector(none) = %#v, want a nil interface", d)
	}
	if d := newScreenDetector(agentdriver.ScreenDetectorKind("nonexistent")); d != nil {
		t.Fatalf("newScreenDetector(unknown) = %#v, want a nil interface", d)
	}
	if newScreenDetector(agentdriver.ScreenDetectorCopilot) == nil {
		t.Fatal("newScreenDetector(copilot) = nil, want a detector")
	}
}

// TestScreenDetector_RepeatedSettledClaimIsDroppedOnce covers the claim
// suppression both agents now share: a settled state is reported once, and a
// repeat of it says nothing new, so it must not re-emit.
func TestScreenDetector_RepeatedSettledClaimIsDroppedOnce(t *testing.T) {
	d := newScreenDetector(agentdriver.ScreenDetectorCopilot)
	frame := []byte("\r\n> Try \"fix the bug\"\r\n")

	state, changed := d.Observe(frame)
	if !changed || state != protocol.StateWaitingInput {
		t.Fatalf("first observe = (%q, %v), want (%q, true)", state, changed, protocol.StateWaitingInput)
	}
	if state, changed := d.Observe(frame); changed {
		t.Fatalf("repeat observe = (%q, true), want no claim", state)
	}
}
