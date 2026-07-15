package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInstallPathDiscoverAndRemove(t *testing.T) {
	installMarker := installFakeBun(t)
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
	if _, err := os.Stat(filepath.Join(manifest.Dir, installMarker)); err != nil {
		t.Fatalf("bun install marker stat failed: %v", err)
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

func TestInstallPathWithOptionsUsesProvidedEnvironment(t *testing.T) {
	installMarker, env := fakeBunEnvironment(t)
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "worktree-provider")

	pluginDir := filepath.Join(t.TempDir(), "plugins")
	manifest, err := InstallPathWithOptions(sourceDir, pluginDir, InstallOptions{Env: env})
	if err != nil {
		t.Fatalf("InstallPathWithOptions failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manifest.Dir, installMarker)); err != nil {
		t.Fatalf("provided-env bun install marker stat failed: %v", err)
	}
}

func TestInstallSourceWithOptionsUsesLocalDirectory(t *testing.T) {
	installMarker, env := fakeBunEnvironment(t)
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "worktree-provider")

	manifest, err := InstallSourceWithOptions(sourceDir, filepath.Join(t.TempDir(), "plugins"), InstallOptions{Env: env})
	if err != nil {
		t.Fatalf("InstallSourceWithOptions failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manifest.Dir, installMarker)); err != nil {
		t.Fatalf("local source dependency marker stat failed: %v", err)
	}
}

func TestInstallSourceWithOptionsClonesGitRepositoryUsingProvidedEnvironment(t *testing.T) {
	installMarker, env := fakeBunEnvironment(t)
	binDir := strings.SplitN(strings.TrimPrefix(env[0], "PATH="), string(os.PathListSeparator), 2)[0]
	gitScript := `#!/bin/sh
set -eu
test "$PLUGIN_CLONE_TEST" = "expected"
test "$1" = "clone"
test "$2" = "--depth"
test "$3" = "1"
test "$4" = "git@ghe.spotify.net:victora/attn-snipe.git"
mkdir -p "$5/src"
cat > "$5/attn-plugin.toml" <<'EOF'
name = "attn-snipe"
version = "0.1.0"
attn_api_version = 3

[plugin]
entrypoint = "src/index.ts"
EOF
: > "$5/src/index.ts"
`
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	env = append(env, "PLUGIN_CLONE_TEST=expected")

	manifest, err := InstallSourceWithOptions(
		"git@ghe.spotify.net:victora/attn-snipe.git",
		filepath.Join(t.TempDir(), "plugins"),
		InstallOptions{Env: env},
	)
	if err != nil {
		t.Fatalf("InstallSourceWithOptions failed: %v", err)
	}
	if manifest.Name != "attn-snipe" {
		t.Fatalf("installed name=%q, want attn-snipe", manifest.Name)
	}
	if _, err := os.Stat(filepath.Join(manifest.Dir, installMarker)); err != nil {
		t.Fatalf("git source dependency marker stat failed: %v", err)
	}
}

func TestInstallSourceWithOptionsRedactsCredentialsFromCloneFailure(t *testing.T) {
	_, env := fakeBunEnvironment(t)
	binDir := strings.SplitN(strings.TrimPrefix(env[0], "PATH="), string(os.PathListSeparator), 2)[0]
	script := "#!/bin/sh\necho \"$4\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write failing git: %v", err)
	}
	source := "https://user:token@example.com/team/plugin.git?secret=value"

	_, err := InstallSourceWithOptions(source, filepath.Join(t.TempDir(), "plugins"), InstallOptions{Env: env})
	if err == nil {
		t.Fatal("InstallSourceWithOptions error=nil, want clone failure")
	}
	for _, secret := range []string{"user:token", "secret=value"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("clone error leaked %q: %v", secret, err)
		}
	}
	if !strings.Contains(err.Error(), "REDACTED") {
		t.Fatalf("clone error=%v, want redacted source detail", err)
	}
}

func TestInstallPathRejectsDuplicatePlugin(t *testing.T) {
	installFakeBun(t)
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

func TestInstallPathConcurrentDuplicateInstallsPublishExactlyOnePlugin(t *testing.T) {
	installFakeBun(t)
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "worktree-provider")
	pluginDir := filepath.Join(t.TempDir(), "plugins")

	const installs = 8
	results := make([]error, installs)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range installs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, results[index] = InstallPath(sourceDir, pluginDir)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful installs=%d, want exactly 1; errors=%v", successes, results)
	}

	installedManifest := filepath.Join(pluginDir, "worktree-provider", ManifestName)
	if _, err := LoadManifest(installedManifest); err != nil {
		t.Fatalf("installed manifest missing or invalid after concurrent install: %v", err)
	}
}

func TestInstallPathRemovesCopiedPluginWhenDependencyInstallFails(t *testing.T) {
	installFailingBun(t)
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "worktree-provider")
	pluginDir := filepath.Join(t.TempDir(), "plugins")

	if _, err := InstallPath(sourceDir, pluginDir); err == nil {
		t.Fatal("InstallPath error=nil, want bun install failure")
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "worktree-provider")); !os.IsNotExist(err) {
		t.Fatalf("failed install directory still exists, stat err=%v", err)
	}
}

func TestInstallPathRejectsTraversalName(t *testing.T) {
	installFakeBun(t)
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "../bad")
	if _, err := InstallPath(sourceDir, filepath.Join(t.TempDir(), "plugins")); err == nil {
		t.Fatal("InstallPath error=nil, want invalid install name")
	}
}

func TestLoadManifestAllowsRuntimeOnlyNames(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "source")
	writeTestPlugin(t, sourceDir, "team/worktree-provider")
	manifest, err := LoadManifest(filepath.Join(sourceDir, ManifestName))
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}
	if manifest.Name != "team/worktree-provider" {
		t.Fatalf("manifest name=%q, want team/worktree-provider", manifest.Name)
	}
}

func TestLoadManifestRejectsEntrypointTraversal(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.ts"), []byte("// outside\n"), 0o644); err != nil {
		t.Fatalf("write outside entrypoint: %v", err)
	}
	manifest := []byte(`
name = "worktree-provider"
version = "0.1.0"
attn_api_version = 3

[plugin]
entrypoint = "../outside.ts"
`)
	if err := os.WriteFile(filepath.Join(sourceDir, ManifestName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := LoadManifest(filepath.Join(sourceDir, ManifestName)); err == nil {
		t.Fatal("LoadManifest error=nil, want traversal entrypoint rejection")
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
attn_api_version = 3

[plugin]
entrypoint = "src/index.ts"
`)
	if err := os.WriteFile(filepath.Join(root, ManifestName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func installFakeBun(t *testing.T) string {
	t.Helper()
	installMarker, env := fakeBunEnvironment(t)
	t.Setenv("PATH", envPath(env))
	return installMarker
}

func fakeBunEnvironment(t *testing.T) (string, []string) {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bun dir: %v", err)
	}
	const installMarker = "node_modules/.bun-install-ran"
	script := "#!/bin/sh\nmkdir -p node_modules\n: > " + installMarker + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "bun"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bun: %v", err)
	}
	path := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	return installMarker, []string{"PATH=" + path}
}

func envPath(env []string) string {
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			return strings.TrimPrefix(entry, "PATH=")
		}
	}
	return ""
}

func installFailingBun(t *testing.T) {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir failing bun dir: %v", err)
	}
	script := "#!/bin/sh\necho dependency install failed >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "bun"), []byte(script), 0o755); err != nil {
		t.Fatalf("write failing bun: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
