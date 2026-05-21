package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHandlePTYState_CodexIgnoresPTYState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "sock"))

	for _, state := range []string{
		protocol.StateWaitingInput,
		protocol.StateIdle,
		protocol.StatePendingApproval,
		protocol.StateWorking,
	} {
		id := "codex-sess-" + state
		nowStr := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID:             id,
			Label:          "codex",
			Agent:          protocol.SessionAgentCodex,
			Directory:      "/tmp",
			State:          protocol.SessionStateIdle,
			StateSince:     nowStr,
			StateUpdatedAt: nowStr,
			LastSeen:       nowStr,
		})

		d.handlePTYState(id, state)
		if got := d.store.Get(id); got.State != protocol.SessionStateIdle {
			t.Fatalf("codex %s PTY state should be ignored, got=%s", state, got.State)
		}
	}
}

func TestHandlePTYState_CodexWorkingDoesNotOverrideStoppedStates(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "sock"))

	addCodexSession := func(id string, state protocol.SessionState) {
		nowStr := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID:             id,
			Label:          id,
			Agent:          protocol.SessionAgentCodex,
			Directory:      "/tmp",
			State:          state,
			StateSince:     nowStr,
			StateUpdatedAt: nowStr,
			LastSeen:       nowStr,
		})
	}

	addCodexSession("codex-idle", protocol.SessionStateIdle)
	d.handlePTYState("codex-idle", protocol.StateWorking)
	if got := d.store.Get("codex-idle"); got.State != protocol.SessionStateIdle {
		t.Fatalf("codex working PTY should not override idle, got=%s", got.State)
	}

	addCodexSession("codex-waiting", protocol.SessionStateWaitingInput)
	d.handlePTYState("codex-waiting", protocol.StateWorking)
	if got := d.store.Get("codex-waiting"); got.State != protocol.SessionStateWaitingInput {
		t.Fatalf("codex working PTY should not override waiting_input, got=%s", got.State)
	}

	addCodexSession("codex-pending", protocol.SessionStatePendingApproval)
	d.handlePTYState("codex-pending", protocol.StateWorking)
	if got := d.store.Get("codex-pending"); got.State != protocol.SessionStatePendingApproval {
		t.Fatalf("codex working PTY should not override pending_approval, got=%s", got.State)
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
