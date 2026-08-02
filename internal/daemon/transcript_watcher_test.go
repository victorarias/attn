package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

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
	// Codex live state is hook-owned, with one exception: its hooks never report
	// a turn the user halted, and `turn_aborted` in its transcript is the only
	// record of one. The watcher is on for that and its behavior does nothing
	// else — see codexTranscriptWatcherBehavior.
	if !isTranscriptWatchedAgent(protocol.SessionAgentCodex) {
		t.Fatal("codex should be transcript-watched so a halted turn is seen")
	}
	if !isTranscriptWatchedAgent(protocol.SessionAgentCopilot) {
		t.Fatal("copilot should be transcript-watched")
	}
}

// Discovery is a directory walk over the agent's whole session tree, and it
// repeats until it lands. A session that never gets a transcript would otherwise
// walk thousands of files twice a second for as long as it lives.
func TestTranscriptDiscoveryBacksOff(t *testing.T) {
	if got := transcriptDiscoveryDelay(1); got != 0 {
		t.Fatalf("first attempts should retry on the next poll, got %s", got)
	}
	if got := transcriptDiscoveryDelay(transcriptDiscoveryFastAttempts - 1); got != 0 {
		t.Fatalf("a transcript still plausibly being created should be looked for eagerly, got %s", got)
	}
	if got := transcriptDiscoveryDelay(transcriptDiscoveryFastAttempts); got != transcriptDiscoverySlowInterval {
		t.Fatalf("delay = %s, want %s once the eager window is spent", got, transcriptDiscoverySlowInterval)
	}
	if got := transcriptDiscoveryDelay(transcriptDiscoverySlowAttempts); got != transcriptDiscoveryIdleInterval {
		t.Fatalf("delay = %s, want %s for a session that is never getting one", got, transcriptDiscoveryIdleInterval)
	}
	// The eager window has to be short enough to matter and long enough to cover
	// an agent that takes a few seconds to write its first line.
	eager := time.Duration(transcriptDiscoveryFastAttempts) * transcriptPollInterval
	if eager < 5*time.Second || eager > 30*time.Second {
		t.Fatalf("eager discovery window is %s, which is outside the range any agent takes to start writing", eager)
	}
}

func TestIsTranscriptWatchedAgent_CapabilityOverride(t *testing.T) {
	t.Setenv("ATTN_AGENT_CLAUDE_TRANSCRIPT", "0")
	if isTranscriptWatchedAgent(protocol.SessionAgentClaude) {
		t.Fatal("claude transcript watching should be disabled by capability override")
	}
}
