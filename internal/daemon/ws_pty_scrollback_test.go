package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/pty"
)

// The per-session PTY scrollback buffers exist only to feed attach/re-attach
// replay, which is always clipped to maxAgentRawReplayBytes (see
// buildAttachReplayPayload). The buffers are intentionally sized well above that
// clip for memory reasons; if the default ever drops below the clip, replay and
// Codex startup-query rehydration would be silently starved. Guard the
// invariant so future shrinks stay safe.
func TestDefaultScrollbackCoversRawReplayClip(t *testing.T) {
	if pty.DefaultScrollbackSize < maxAgentRawReplayBytes {
		t.Fatalf("DefaultScrollbackSize (%d) must be >= maxAgentRawReplayBytes (%d) or attach replay is starved",
			pty.DefaultScrollbackSize, maxAgentRawReplayBytes)
	}
}
