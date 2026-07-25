package pty

import (
	"strings"
	"testing"
	"time"
)

// Rendered-screen text as it appears while each agent shows an
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
	if sig := r.observe(claudeApprovalScreen, base); sig != approvalArmedPending {
		t.Fatalf("expected approvalArmedPending on first prompt sight, got %v", sig)
	}
	if !r.armed {
		t.Fatal("resolver should arm while the prompt is visible")
	}
	// Prompt still visible on the next sample: armed, no repeat signal.
	if sig := r.observe(claudeApprovalScreen, base.Add(time.Millisecond)); sig != approvalNone {
		t.Fatalf("should not re-signal while prompt stays visible, got %v", sig)
	}

	// Prompt gone: this sample starts the clear debounce window.
	clearStart := base.Add(100 * time.Millisecond)
	if sig := r.observe(claudeWorkingScreen, clearStart); sig != approvalClearStarted {
		t.Fatalf("expected approvalClearStarted on first prompt-gone sample, got %v", sig)
	}

	// Still within the debounce window: no transition yet.
	if sig := r.observe(claudeWorkingScreen, clearStart.Add(approvalClearDebounce-time.Millisecond)); sig != approvalNone {
		t.Fatalf("should not clear before debounce elapses, got %v", sig)
	}

	// Debounce elapsed: emit working exactly once.
	if sig := r.observe(claudeWorkingScreen, clearStart.Add(approvalClearDebounce+time.Millisecond)); sig != approvalCleared {
		t.Fatalf("expected approvalCleared after debounce, got %v", sig)
	}
	if sig := r.observe(claudeWorkingScreen, base.Add(2*time.Second)); sig != approvalNone {
		t.Fatalf("should not re-signal after clearing, got %v", sig)
	}
}

// ViewportText must resolve absolute-cursor-addressed output (how TUIs actually
// paint the prompt) into linear text, where the raw byte tail would not.
func TestViewportText_ResolvesCursorAddressedPrompt(t *testing.T) {
	term := newTestGhostty(t, 80, 24)
	write := func(data string) {
		t.Helper()
		term.Write([]byte(data))
	}

	// Clear + home, then paint the prompt out of order via absolute moves.
	write("\x1b[2J\x1b[H")
	write("\x1b[7;3H2. No")
	write("\x1b[5;1HDo you want to proceed?")
	write("\x1b[6;1H❯ 1. Yes")

	text := term.ViewportText()
	if !isPendingApproval(text) {
		t.Fatalf("rendered prompt should be detected as pending approval; got:\n%s", text)
	}

	// After approval the area is repainted with the result; prompt is gone.
	write("\x1b[2J\x1b[H\x1b[5;1H⏺ Done")
	if isPendingApproval(term.ViewportText()) {
		t.Fatalf("repainted screen should no longer look pending; got:\n%s", term.ViewportText())
	}
}

func TestApprovalResolver_NoTransitionWithoutPrompt(t *testing.T) {
	r := &approvalResolver{}
	now := time.Unix(0, 0)
	for i := range 10 {
		if sig := r.observe(claudeWorkingScreen, now.Add(time.Duration(i)*time.Second)); sig != approvalNone {
			t.Fatalf("never-armed resolver must not signal: %v", sig)
		}
	}
}

func TestApprovalResolver_PromptReappearsResetsDebounce(t *testing.T) {
	r := &approvalResolver{}
	base := time.Unix(0, 0)

	r.observe(codexApprovalScreen, base)                          // arm
	r.observe(codexWorkingScreen, base.Add(200*time.Millisecond)) // start debounce

	// A second prompt appears (chained multi-step approval) before debounce ends.
	if sig := r.observe(codexApprovalScreen, base.Add(400*time.Millisecond)); sig == approvalCleared {
		t.Fatal("a reappearing prompt must not produce a working transition")
	}
	if r.clearedSince != (time.Time{}) {
		t.Fatal("reappearing prompt should reset the clear debounce")
	}

	// Now resolved for real.
	r.observe(codexWorkingScreen, base.Add(500*time.Millisecond)) // restart debounce
	if sig := r.observe(codexWorkingScreen, base.Add(500*time.Millisecond+approvalClearDebounce)); sig != approvalCleared {
		t.Fatalf("expected approvalCleared after second prompt resolved, got %v", sig)
	}
}

// TestSession_ApprovalClearsWithoutFurtherOutput is the regression for the core
// contract: once the approval prompt disappears, the session returns to working
// even when the approved command produces no further PTY output. The clear must be
// driven by the scheduled recheck, not by the next output frame — so after the
// prompt-gone sample we make no further evaluateApproval calls and rely solely on
// the timer.
func TestSession_ApprovalClearsWithoutFurtherOutput(t *testing.T) {
	states := make(chan string, 8)
	gt := newTestGhostty(t, 80, 24)
	s := &Session{
		approvalResolver: &approvalResolver{},
		ghostty:          gt,
		onState:          func(state string) { states <- state },
	}
	s.running = true

	paint := func(screen string) {
		// Clear+home, then paint the screen with CRLF line breaks so each row
		// starts at column 0 (matching how a TUI repaints).
		s.ghostty.Write([]byte("\x1b[2J\x1b[H"))
		s.ghostty.Write([]byte(strings.ReplaceAll(screen, "\n", "\r\n")))
	}

	// Approval prompt appears -> pending emitted immediately (readLoop path).
	paint(codexApprovalScreen)
	s.evaluateApproval(time.Now(), false)
	select {
	case st := <-states:
		if st != statePendingApproval {
			t.Fatalf("expected %q when the prompt appears, got %q", statePendingApproval, st)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending_approval when the prompt appears")
	}

	// User approves: the screen repaints to the working view in a single burst,
	// then the command goes silent. Only the scheduled recheck can drive the clear.
	paint(codexWorkingScreen)
	s.evaluateApproval(time.Now(), false)

	select {
	case st := <-states:
		if st != stateWorking {
			t.Fatalf("expected %q from the scheduled recheck, got %q", stateWorking, st)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("working was not emitted without further PTY output (timer did not drive the clear)")
	}
}
