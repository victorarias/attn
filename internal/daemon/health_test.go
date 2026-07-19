package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHealthRoutingPathsCanonicalizeRelativeOverridesAndSymlinkedAncestors(t *testing.T) {
	realRoot := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(realRoot, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	workingDir := t.TempDir()
	if err := os.Symlink(realRoot, filepath.Join(workingDir, "linked")); err != nil {
		t.Fatal(err)
	}

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("ATTN_DATA_DIR", filepath.Join("linked", "data"))
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join("linked", "data", "attn.sock"))

	dataDir, socketPath, routingErr := healthRoutingPaths()
	if routingErr != "" {
		t.Fatalf("health routing error = %q", routingErr)
	}
	if want := filepath.Join(canonicalRoot, "data"); dataDir != want {
		t.Fatalf("data_dir = %q, want %q", dataDir, want)
	}
	if want := filepath.Join(canonicalRoot, "data", "attn.sock"); socketPath != want {
		t.Fatalf("socket_path = %q, want %q", socketPath, want)
	}
}
