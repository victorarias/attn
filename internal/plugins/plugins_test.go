package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPathDiscoverAndRemove(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "worktree-provider")

	pluginDir := filepath.Join(t.TempDir(), "plugins")
	manifest, err := InstallPath(sourceDir, pluginDir)
	if err != nil {
		t.Fatalf("InstallPath failed: %v", err)
	}
	if manifest.Name != "worktree-provider" {
		t.Fatalf("installed name=%q, want worktree-provider", manifest.Name)
	}
	if manifest.Dir != filepath.Join(pluginDir, "worktree-provider") {
		t.Fatalf("installed dir=%q", manifest.Dir)
	}

	manifests, issues := Discover(pluginDir)
	if len(issues) != 0 {
		t.Fatalf("discover issues=%v, want none", issues)
	}
	if len(manifests) != 1 || manifests[0].Name != "worktree-provider" {
		t.Fatalf("discover manifests=%v, want worktree-provider", manifests)
	}

	if err := Remove(pluginDir, "worktree-provider"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "worktree-provider")); !os.IsNotExist(err) {
		t.Fatalf("installed directory still exists, stat err=%v", err)
	}
}

func TestInstallPathRejectsDuplicatePlugin(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "worktree-provider")
	pluginDir := filepath.Join(t.TempDir(), "plugins")
	if _, err := InstallPath(sourceDir, pluginDir); err != nil {
		t.Fatalf("first InstallPath failed: %v", err)
	}
	if _, err := InstallPath(sourceDir, pluginDir); err == nil {
		t.Fatal("second InstallPath error=nil, want duplicate install error")
	}
}

func TestLoadManifestRejectsTraversalName(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "../bad")
	if _, err := LoadManifest(filepath.Join(sourceDir, ManifestName)); err == nil {
		t.Fatal("LoadManifest error=nil, want invalid name")
	}
}

func writeTestPlugin(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir plugin source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "index.ts"), []byte("// entrypoint\n"), 0o644); err != nil {
		t.Fatalf("write entrypoint: %v", err)
	}
	manifest := []byte(`
name = "` + name + `"
version = "0.1.0"
attn_api_version = 1

[plugin]
entrypoint = "src/index.ts"
`)
	if err := os.WriteFile(filepath.Join(root, ManifestName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
