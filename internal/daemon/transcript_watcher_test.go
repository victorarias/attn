package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHandlePTYState_CodexIgnoresWaitingAndIdle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "sock"))

	nowStr := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "codex-sess",
		Label:          "codex",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp",
		State:          protocol.SessionStateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	d.handlePTYState("codex-sess", protocol.StateWaitingInput)
	if got := d.store.Get("codex-sess"); got.State != protocol.SessionStateWorking {
		t.Fatalf("codex waiting_input should be ignored, got=%s", got.State)
	}

	d.handlePTYState("codex-sess", protocol.StateIdle)
	if got := d.store.Get("codex-sess"); got.State != protocol.SessionStateWorking {
		t.Fatalf("codex idle should be ignored, got=%s", got.State)
	}

	d.handlePTYState("codex-sess", protocol.StatePendingApproval)
	if got := d.store.Get("codex-sess"); got.State != protocol.SessionStatePendingApproval {
		t.Fatalf("codex pending_approval should be applied, got=%s", got.State)
	}
}

func TestHandlePTYState_ClaudeAcceptsWaiting(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "sock"))

	nowStr := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "claude-sess",
		Label:          "claude",
		Agent:          protocol.SessionAgentClaude,
		Directory:      "/tmp",
		State:          protocol.SessionStateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	d.handlePTYState("claude-sess", protocol.StateWaitingInput)
	if got := d.store.Get("claude-sess"); got.State != protocol.SessionStateWaitingInput {
		t.Fatalf("claude waiting_input should be applied, got=%s", got.State)
	}
}

func TestHandlePTYState_CopilotKeepsPendingAgainstWorkingNoise(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "sock"))

	nowStr := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "copilot-sess",
		Label:          "copilot",
		Agent:          protocol.SessionAgentCopilot,
		Directory:      "/tmp",
		State:          protocol.SessionStatePendingApproval,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	d.handlePTYState("copilot-sess", protocol.StateWorking)
	if got := d.store.Get("copilot-sess"); got.State != protocol.SessionStatePendingApproval {
		t.Fatalf("copilot working should not override pending_approval, got=%s", got.State)
	}
}

func TestReadTranscriptDelta(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "events.jsonl")
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	data, err := readTranscriptDelta(path, int64(len("line1\n")))
	if err != nil {
		t.Fatalf("readTranscriptDelta error: %v", err)
	}
	if string(data) != "line2\n" {
		t.Fatalf("unexpected delta: %q", string(data))
	}
}

func TestHasCopilotTranscriptPendingApproval(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"view-fast": {
			name:      "view",
			startedAt: now.Add(-10 * time.Second),
		},
		"bash-stalled": {
			name:      "bash",
			startedAt: now.Add(-(toolStartGraceWindow + 10*time.Millisecond)),
		},
	}

	if !hasCopilotTranscriptPendingApproval(pending, now, true) {
		t.Fatal("expected stalled bash tool to trigger pending approval")
	}
}

func TestHasCopilotTranscriptPendingApproval_CreateTool(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"create-stalled": {
			name:      "create",
			startedAt: now.Add(-(toolStartGraceWindow + 10*time.Millisecond)),
		},
	}

	if !hasCopilotTranscriptPendingApproval(pending, now, true) {
		t.Fatal("expected stalled create tool to trigger pending approval")
	}
}

func TestHasCopilotTranscriptPendingApproval_GraceWindow(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"bash-recent": {
			name:      "bash",
			startedAt: now.Add(-(toolStartGraceWindow - 50*time.Millisecond)),
		},
	}

	if hasCopilotTranscriptPendingApproval(pending, now, true) {
		t.Fatal("recent tool start should not trigger pending approval yet")
	}
}

func TestHasCopilotTranscriptPendingApproval_RequiresTurnOpen(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"bash-stalled": {
			name:      "bash",
			startedAt: now.Add(-(toolStartGraceWindow + 100*time.Millisecond)),
		},
	}

	if hasCopilotTranscriptPendingApproval(pending, now, false) {
		t.Fatal("closed turn should not trigger pending approval")
	}
}

func TestShouldPromoteTranscriptPending(t *testing.T) {
	if shouldPromoteTranscriptPending(protocol.SessionStateWorking) {
		t.Fatal("working state should not be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateIdle) {
		t.Fatal("idle state should be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateWaitingInput) {
		t.Fatal("waiting_input state should be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateUnknown) {
		t.Fatal("unknown state should be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateLaunching) {
		t.Fatal("launching state should be promoted to pending approval by transcript")
	}
	if shouldPromoteTranscriptPending(protocol.SessionStatePendingApproval) {
		t.Fatal("pending_approval state should not re-promote")
	}
}

func TestExtractEventType(t *testing.T) {
	if got := extractEventType([]byte(`{"type":"assistant.turn_start","data":{}}`)); got != "assistant.turn_start" {
		t.Fatalf("extractEventType() = %q, want assistant.turn_start", got)
	}
	if got := extractEventType([]byte(`not-json`)); got != "" {
		t.Fatalf("extractEventType(non-json) = %q, want empty", got)
	}
}

func TestIsTranscriptWatchedAgent(t *testing.T) {
	if !isTranscriptWatchedAgent(protocol.SessionAgentClaude) {
		t.Fatal("claude should be transcript-watched")
	}
	if !isTranscriptWatchedAgent(protocol.SessionAgentCodex) {
		t.Fatal("codex should be transcript-watched")
	}
	if !isTranscriptWatchedAgent(protocol.SessionAgentCopilot) {
		t.Fatal("copilot should be transcript-watched")
	}
}
