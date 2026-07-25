package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// Copilot is the only agent whose state still arrives from the rendered screen.
// Claude and codex build no screen observer at all, so handlePTYState has nothing
// to filter for them — their states come from the evidence resolver, which is
// covered in internal/sessionstate and session_evidence_test.go.
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

	d.handlePTYState("copilot-sess", screenObs(protocol.StateWorking))
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
	if isTranscriptWatchedAgent(protocol.SessionAgentCodex) {
		t.Fatal("codex live state is hook-owned and should not be transcript-watched")
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
