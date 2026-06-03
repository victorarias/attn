package pty

import (
	"testing"
	"time"
)

// Rendered-screen text as it appears (vt10x-resolved) while each agent shows an
// approval prompt. Captured from real Claude/Codex sessions.
const (
	claudeApprovalScreen = `Bash command
  rm /tmp/scratch_demo.txt
  Remove the scratch file

Permission rule Bash(rm:*) requires confirmation for this command.
/permissions to update rules

Do you want to proceed?
❯ 1. Yes
  2. No

Esc to cancel · Tab to amend · ctrl+e to explain`

	claudeWorkingScreen = `⏺ I'll remove the file.

⏺ Bash(rm /tmp/scratch_demo.txt)
  ⎿  Done
  ⎿  Allowed by PermissionRequest hook

✻ Fiddle-faddling… (7s · ↓ 269 tokens)`

	codexApprovalScreen = `• Running rm approval-prompt-test-file.txt
  Would you like to run the following command?
  Reason: requested by user

  1. Yes
  2. Yes, and don't ask again for commands that start with ` + "`rm`" + `
  3. No, and tell Codex what to do differently`

	codexWorkingScreen = `✔ You approved codex to run rm approval-prompt-test-file.txt this time
• Running rm approval-prompt-test-file.txt
• Working (10s • esc to interrupt)
› Write tests for @filename`
)

func TestIsPendingApproval_RealRenderedScreens(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		want   bool
	}{
		{"claude prompt", claudeApprovalScreen, true},
		{"claude working", claudeWorkingScreen, false},
		{"codex prompt", codexApprovalScreen, true},
		{"codex working", codexWorkingScreen, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPendingApproval(tt.screen); got != tt.want {
				t.Fatalf("isPendingApproval(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestApprovalResolver_ClearsAfterPromptGoneAndDebounce(t *testing.T) {
	r := &approvalResolver{}
	base := time.Unix(0, 0)

	// Prompt first visible: arm and re-emit pending_approval to sync the PTY
	// layer's state view (so the later working clear is seen as a change).
	if state, changed := r.observe(claudeApprovalScreen, base); !changed || state != statePendingApproval {
		t.Fatalf("expected (%q,true) on first prompt sight, got (%q,%v)", statePendingApproval, state, changed)
	}
	if !r.armed {
		t.Fatal("resolver should arm while the prompt is visible")
	}
	// Prompt still visible on the next sample: armed, no repeat emit.
	if state, changed := r.observe(claudeApprovalScreen, base.Add(time.Millisecond)); changed {
		t.Fatalf("should not re-emit pending while prompt stays visible: %q", state)
	}

	// Prompt gone: this sample starts the clear debounce window.
	clearStart := base.Add(100 * time.Millisecond)
	if _, changed := r.observe(claudeWorkingScreen, clearStart); changed {
		t.Fatal("should not clear on the first prompt-gone sample")
	}

	// Still within the debounce window: no transition yet.
	if _, changed := r.observe(claudeWorkingScreen, clearStart.Add(approvalClearDebounce-time.Millisecond)); changed {
		t.Fatal("should not clear before debounce elapses")
	}

	// Debounce elapsed: emit working exactly once.
	state, changed := r.observe(claudeWorkingScreen, clearStart.Add(approvalClearDebounce+time.Millisecond))
	if !changed || state != stateWorking {
		t.Fatalf("expected (%q,true) after debounce, got (%q,%v)", stateWorking, state, changed)
	}
	if state, changed := r.observe(claudeWorkingScreen, base.Add(2*time.Second)); changed {
		t.Fatalf("should not re-emit after clearing: %q", state)
	}
}

// renderedText must resolve absolute-cursor-addressed output (how TUIs actually
// paint the prompt) into linear text, where the raw byte tail would not.
func TestRenderedText_ResolvesCursorAddressedPrompt(t *testing.T) {
	screen := newVirtualScreen(80, 24)
	// Clear + home, then paint the prompt out of order via absolute moves.
	screen.Observe([]byte("\x1b[2J\x1b[H"))
	screen.Observe([]byte("\x1b[7;3H2. No"))
	screen.Observe([]byte("\x1b[5;1HDo you want to proceed?"))
	screen.Observe([]byte("\x1b[6;1H❯ 1. Yes"))

	text := screen.renderedText()
	if !isPendingApproval(text) {
		t.Fatalf("rendered prompt should be detected as pending approval; got:\n%s", text)
	}

	// After approval the area is repainted with the result; prompt is gone.
	screen.Observe([]byte("\x1b[2J\x1b[H\x1b[5;1H⏺ Done"))
	if isPendingApproval(screen.renderedText()) {
		t.Fatalf("repainted screen should no longer look pending; got:\n%s", screen.renderedText())
	}
}

func TestApprovalResolver_NoTransitionWithoutPrompt(t *testing.T) {
	r := &approvalResolver{}
	now := time.Unix(0, 0)
	for i := range 10 {
		if state, changed := r.observe(claudeWorkingScreen, now.Add(time.Duration(i)*time.Second)); changed {
			t.Fatalf("never-armed resolver must not emit: %q", state)
		}
	}
}

func TestApprovalResolver_PromptReappearsResetsDebounce(t *testing.T) {
	r := &approvalResolver{}
	base := time.Unix(0, 0)

	r.observe(codexApprovalScreen, base)                          // arm
	r.observe(codexWorkingScreen, base.Add(200*time.Millisecond)) // start debounce

	// A second prompt appears (chained multi-step approval) before debounce ends.
	if _, changed := r.observe(codexApprovalScreen, base.Add(400*time.Millisecond)); changed {
		t.Fatal("a reappearing prompt must not produce a working transition")
	}
	if r.clearedSince != (time.Time{}) {
		t.Fatal("reappearing prompt should reset the clear debounce")
	}

	// Now resolved for real.
	r.observe(codexWorkingScreen, base.Add(500*time.Millisecond)) // restart debounce
	state, changed := r.observe(codexWorkingScreen, base.Add(500*time.Millisecond+approvalClearDebounce))
	if !changed || state != stateWorking {
		t.Fatalf("expected working after second prompt resolved, got (%q,%v)", state, changed)
	}
}
