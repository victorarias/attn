//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// The whole worker half, on a real spawned process: the override reaches the
// terminal ghostty is built with, a program's image is observed and described
// to the attached client, and the pixels behind it can be fetched back.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// kittySpawn starts a session whose child emits payload once the test releases
// it. The handshake matters: a child that writes at spawn time can be finished
// before the test attaches, and a test that closed that gap with a sleep would
// pass or fail on machine speed.
type kittySpawn struct {
	manager *Manager
	id      string
	updates chan PlacementUpdate
	exited  chan struct{}
}

func newKittySpawn(t *testing.T, id, payload string) *kittySpawn {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	m := NewManager(nil)
	t.Cleanup(m.Shutdown)
	spawn := &kittySpawn{
		manager: m,
		id:      id,
		updates: make(chan PlacementUpdate, 16),
		exited:  make(chan struct{}),
	}
	m.SetExitHandler(func(ExitInfo) { close(spawn.exited) })

	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-kitty",
		ExternalCommand: []string{"/bin/sh", "-c", "read release; cat " + path},
		Cols:            40,
		Rows:            12,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	if _, err := m.Attach(id, "test-client",
		func([]byte, uint32) bool { return true },
		nil,
		OnPlacements(func(update PlacementUpdate) { spawn.updates <- update }),
	); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	return spawn
}

// release lets the child emit, and returns once it has exited — which is the
// read loop's own statement that every byte it produced has been fed and fanned.
func (k *kittySpawn) release(t *testing.T) {
	t.Helper()
	if err := k.manager.Input(k.id, []byte("\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	select {
	case <-k.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the child never exited after being released")
	}
}

// The default is dark, and it is dark on the real spawn path rather than only
// in a hand-built terminal: no override, no storage, no placement, whatever the
// program emits.
func TestSpawnedSessionDescribesNoImagesByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "")

	spawn := newKittySpawn(t, "kitty-default", kittyPlaceRGB(80, 16, 32, ""))
	spawn.release(t)

	select {
	case update := <-spawn.updates:
		t.Fatalf("a placement was described with images disabled: %+v", update)
	default:
	}
	if _, err := spawn.manager.KittyImage(spawn.id, 80); !errors.Is(err, ErrKittyImageNotFound) {
		t.Errorf("KittyImage() error = %v, want ErrKittyImageNotFound: nothing was stored", err)
	}
}

// With the override, every hop works on the spawn path: the limit reaches
// ghostty's options, the transmission is accepted, the placement is observed,
// the attached client is told, and the pixels are fetchable by the id the
// placement carries.
func TestSpawnedSessionDescribesImagesUnderTheStorageOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	spawn := newKittySpawn(t, "kitty-enabled", kittyPlaceRGB(81, 16, 32, ""))
	spawn.release(t)

	var update PlacementUpdate
	select {
	case update = <-spawn.updates:
	default:
		t.Fatal("no placement was described for an image the program emitted")
	}
	if len(update.Placements) != 1 {
		t.Fatalf("placements = %+v, want the one image", update.Placements)
	}
	placement := update.Placements[0]
	if placement.ImageID != 81 {
		t.Fatalf("described image id = %d, want 81", placement.ImageID)
	}

	img, err := spawn.manager.KittyImage(spawn.id, placement.ImageID)
	if err != nil {
		t.Fatalf("KittyImage(%d) error: %v", placement.ImageID, err)
	}
	if img.Width != 16 || img.Height != 32 {
		t.Errorf("fetched image = %dx%d, want 16x32", img.Width, img.Height)
	}
	// Stored images are always raw pixels, so the payload is the full
	// width*height*bpp regardless of how the program encoded it.
	if got, want := len(img.Data), 16*32*3; got != want {
		t.Errorf("fetched pixels = %d bytes, want %d (16x32 RGB, decoded)", got, want)
	}
	if img.Generation != placement.ImageGeneration {
		t.Errorf("fetched generation = %d, want the %d the placement referenced", img.Generation, placement.ImageGeneration)
	}

	if _, err := spawn.manager.KittyImage(spawn.id, 999); !errors.Is(err, ErrKittyImageNotFound) {
		t.Errorf("KittyImage(999) error = %v, want ErrKittyImageNotFound", err)
	}
	if _, err := spawn.manager.KittyImage("no-such-session", 81); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("KittyImage() on an unknown session = %v, want ErrSessionNotFound", err)
	}
}
