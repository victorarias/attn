package daemon

import (
	"os"
	"path/filepath"
	"testing"

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

func TestIsTranscriptWatchedAgent_CapabilityOverride(t *testing.T) {
	t.Setenv("ATTN_AGENT_CLAUDE_TRANSCRIPT", "0")
	if isTranscriptWatchedAgent(protocol.SessionAgentClaude) {
		t.Fatal("claude transcript watching should be disabled by capability override")
	}
}
