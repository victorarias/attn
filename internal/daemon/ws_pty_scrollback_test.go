package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/pty"
)

// The segmented replay log exists to feed attach/re-attach replay, which is
// clipped to maxAgentRawReplayBytes (see buildAttachReplayPayload). If the
// log's default capacity ever drops below the clip, restored scrollback depth
// would be silently starved. Guard the invariant so future shrinks stay safe.
func TestDefaultReplayLogCoversRawReplayClip(t *testing.T) {
	if pty.DefaultReplayLogSize < maxAgentRawReplayBytes {
		t.Fatalf("DefaultReplayLogSize (%d) must be >= maxAgentRawReplayBytes (%d) or attach replay is starved",
			pty.DefaultReplayLogSize, maxAgentRawReplayBytes)
	}
}
