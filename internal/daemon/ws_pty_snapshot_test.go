package daemon

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

type snapshotBackend struct {
	*fakeSpawnBackend
	snapshot pty.SnapshotInfo
}

func (b *snapshotBackend) Snapshot(context.Context, string) (pty.SnapshotInfo, error) {
	return b.snapshot, nil
}

func TestHandleGetScreenSnapshotSeedsFromViewportPayload(t *testing.T) {
	d := NewForTesting(t.TempDir())
	d.ptyBackend = &snapshotBackend{
		fakeSpawnBackend: &fakeSpawnBackend{},
		snapshot: pty.SnapshotInfo{
			LastSeq: 42,
			Cols:    100,
			Rows:    30,
			Running: true,
			Screen: &pty.ViewportSnapshot{
				Payload: []byte("rendered-by-worker"),
				Cols:    80,
				Rows:    24,
			},
		},
	}
	client := &wsClient{send: make(chan outboundMessage, 1)}

	d.handleGetScreenSnapshot(client, &protocol.GetScreenSnapshotMessage{ID: "session-1"})

	var result protocol.GetScreenSnapshotResultMessage
	readNotebookWSEvent(t, client.send, &result)
	if !result.Success {
		t.Fatalf("snapshot result failed: %v", result.Error)
	}
	if result.ScreenSnapshot == nil {
		t.Fatal("expected viewport payload in snapshot result")
	}
	payload, err := base64.StdEncoding.DecodeString(*result.ScreenSnapshot)
	if err != nil {
		t.Fatalf("decode snapshot payload: %v", err)
	}
	if string(payload) != "rendered-by-worker" {
		t.Fatalf("snapshot payload = %q, want rendered-by-worker", payload)
	}
	if got, want := protocol.Deref(result.ScreenCols), 80; got != want {
		t.Fatalf("screen cols = %d, want %d", got, want)
	}
	if got, want := protocol.Deref(result.ScreenRows), 24; got != want {
		t.Fatalf("screen rows = %d, want %d", got, want)
	}
}

func TestHandleGetScreenSnapshotLeavesObserverUnseededWithoutViewport(t *testing.T) {
	d := NewForTesting(t.TempDir())
	d.ptyBackend = &snapshotBackend{
		fakeSpawnBackend: &fakeSpawnBackend{},
		snapshot:         pty.SnapshotInfo{LastSeq: 42, Cols: 100, Rows: 30, Running: true},
	}
	client := &wsClient{send: make(chan outboundMessage, 1)}

	d.handleGetScreenSnapshot(client, &protocol.GetScreenSnapshotMessage{ID: "session-1"})

	var result protocol.GetScreenSnapshotResultMessage
	readNotebookWSEvent(t, client.send, &result)
	if !result.Success {
		t.Fatalf("snapshot result failed: %v", result.Error)
	}
	if result.ScreenSnapshot != nil || result.ScreenCols != nil || result.ScreenRows != nil {
		t.Fatalf("expected unseeded observer result, got %+v", result)
	}
}
