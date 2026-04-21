package daemon

import "testing"

// Re-attach on the same websocket client must produce a fresh subscriber id.
// When the id is reused, the PTY session subscriber map overwrites the
// previous callback; the old stream's eventual detach then removes the new
// subscriber, silently starving the live stream of output (regression fix
// for tr205-claude close-pane redraw).
func TestWSSubscriberIDIsUniquePerAttach(t *testing.T) {
	client := &wsClient{}
	seen := map[string]int{}
	const iterations = 100
	for range iterations {
		id := wsSubscriberID(client, "session-42")
		seen[id]++
	}
	if len(seen) != iterations {
		t.Fatalf("expected %d unique subscriber ids for %d attaches, got %d", iterations, iterations, len(seen))
	}
}
