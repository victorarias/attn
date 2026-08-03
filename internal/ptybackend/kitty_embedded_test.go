//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package ptybackend

// The embedded backend hosts the same pty.Session the worker does, so it owes
// its clients the same description. A placement pipeline that only exists on
// the worker path is the classic "works where I tested it" defect: the daemon
// picks its backend at runtime.

import (
	"context"
	"testing"

	"github.com/victorarias/attn/internal/pty"
)

func TestEmbeddedBackendDescribesPlacementsAndServesTheImage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv("ATTN_KITTY_STORAGE_LIMIT", "16777216")

	backend := NewEmbedded(pty.NewManager(nil))
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })

	sessionID := "embedded-kitty"
	payload := kittyPayloadFile(t, "\x1b[3;1H"+kittyPlaceRGB(91, 16, 32))
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:              sessionID,
		CWD:             t.TempDir(),
		Agent:           "probe-kitty",
		ExternalCommand: []string{"/bin/sh", "-c", "read release; cat " + payload},
		Cols:            40,
		Rows:            12,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	_, stream, err := backend.Attach(context.Background(), sessionID, "kitty-sub")
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	defer stream.Close()

	evt := releaseAndReadPlacements(t, stream, func() error {
		return backend.Input(context.Background(), sessionID, []byte("\n"))
	})
	if evt.Seq == 0 {
		t.Error("placement event seq = 0: the set is unusable without the chunk it describes")
	}
	if len(evt.Placements) != 1 || evt.Placements[0].ImageID != 91 {
		t.Fatalf("placements = %+v, want the image the program emitted", evt.Placements)
	}

	img, err := backend.KittyImage(context.Background(), sessionID, evt.Placements[0].ImageID)
	if err != nil {
		t.Fatalf("KittyImage() error: %v", err)
	}
	if img.Width != 16 || img.Height != 32 || len(img.Data) != 16*32*3 {
		t.Errorf("fetched image = %dx%d with %d bytes, want 16x32 with %d",
			img.Width, img.Height, len(img.Data), 16*32*3)
	}
}
