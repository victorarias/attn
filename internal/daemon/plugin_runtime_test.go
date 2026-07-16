package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPluginDirForSocketUsesSocketRuntimeRoot(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "profile", "attn.sock")
	if got, want := pluginDirForSocket(socketPath), filepath.Join(filepath.Dir(socketPath), "plugins"); got != want {
		t.Fatalf("pluginDirForSocket() = %q, want %q", got, want)
	}

	override := filepath.Join(t.TempDir(), "custom-plugins")
	t.Setenv("ATTN_PLUGIN_DIR", override)
	if got := pluginDirForSocket(socketPath); got != override {
		t.Fatalf("pluginDirForSocket() with override = %q, want %q", got, override)
	}
}

func TestBundledPluginDirForExecutableUsesExplicitOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "bundled")
	t.Setenv("ATTN_BUNDLED_PLUGIN_DIR", override)
	if got := bundledPluginDirForExecutable(); got != override {
		t.Fatalf("bundledPluginDirForExecutable()=%q, want %q", got, override)
	}
}

func TestDiscoverPluginManifests_LoadsValidInstalledPlugins(t *testing.T) {
	pluginDir := filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, pluginDir, "worktree-provider")

	manifests, issues := discoverPluginManifests(pluginDir)
	if len(issues) != 0 {
		t.Fatalf("manifest issues=%v, want none", issues)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifest count=%d, want 1", len(manifests))
	}
	if manifests[0].Name != "worktree-provider" {
		t.Fatalf("manifest name=%q, want worktree-provider", manifests[0].Name)
	}
	if manifests[0].Plugin.Entrypoint != "src/index.ts" {
		t.Fatalf("manifest entrypoint=%q, want src/index.ts", manifests[0].Plugin.Entrypoint)
	}
}

func TestDiscoverPluginManifests_ReportsInvalidManifest(t *testing.T) {
	pluginDir := filepath.Join(t.TempDir(), "plugins")
	badDir := filepath.Join(pluginDir, "bad-plugin")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir bad plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, pluginManifestName), []byte(`
name = "bad-plugin"
version = "0.1.0"
attn_api_version = 4
`), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}

	manifests, issues := discoverPluginManifests(pluginDir)
	if len(manifests) != 0 {
		t.Fatalf("manifests=%v, want none", manifests)
	}
	if len(issues) != 1 {
		t.Fatalf("issues=%v, want one issue", issues)
	}
}

func TestDiscoverPluginManifests_AllowsRuntimeOnlyManifestNames(t *testing.T) {
	pluginDir := filepath.Join(t.TempDir(), "plugins")
	root := filepath.Join(pluginDir, "manual-provider")
	entrypointDir := filepath.Join(root, "src")
	if err := os.MkdirAll(entrypointDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entrypointDir, "index.ts"), []byte("// fake entrypoint\n"), 0o644); err != nil {
		t.Fatalf("write fake entrypoint: %v", err)
	}
	manifest := []byte(`
name = "manual/provider"
version = "0.1.0"
attn_api_version = 4

[plugin]
entrypoint = "src/index.ts"
`)
	if err := os.WriteFile(filepath.Join(root, pluginManifestName), manifest, 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}

	manifests, issues := discoverPluginManifests(pluginDir)
	if len(issues) != 0 {
		t.Fatalf("manifest issues=%v, want none", issues)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifest count=%d, want 1", len(manifests))
	}
	if manifests[0].Name != "manual/provider" {
		t.Fatalf("manifest name=%q, want manual/provider", manifests[0].Name)
	}
}

func TestDaemon_StartInstalledPlugins_SpawnsProviderPlugin(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19971")
	t.Setenv("ATTN_PLUGIN_HELPER", "1")
	t.Setenv("ATTN_TEST_HELPER_BINARY", os.Args[0])

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "plugin-runtime.sock")
	pluginDir := filepath.Join(tmpDir, "plugins")
	writeTestPluginManifest(t, pluginDir, "spawned-provider")

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bun dir: %v", err)
	}
	bunPath := filepath.Join(binDir, "bun")
	if err := os.WriteFile(bunPath, []byte("#!/bin/sh\nexec \"$ATTN_TEST_HELPER_BINARY\" -test.run '^TestDaemonPluginProcessHelper$'\n"), 0o755); err != nil {
		t.Fatalf("write fake bun: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := NewForTesting(sockPath)
	d.pluginDir = pluginDir
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)
	waitForSurfaceHandlers(t, d, "worktree.create", 1, 5*time.Second)

	handlers := d.plugins.handlersForSurface("worktree.create")
	if handlers[0].PluginName != "spawned-provider" {
		t.Fatalf("spawned provider=%q, want spawned-provider", handlers[0].PluginName)
	}
}

func TestDaemon_StartInstalledPlugins_RestartsCleanExitWithNewGeneration(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19972")
	t.Setenv("ATTN_PLUGIN_HELPER", "1")
	t.Setenv("ATTN_TEST_HELPER_BINARY", os.Args[0])

	tmpDir := shortTempDir(t)
	t.Setenv("ATTN_PLUGIN_EXIT_ONCE_MARKER", filepath.Join(tmpDir, "first-generation-exited"))
	sockPath := filepath.Join(tmpDir, "plugin-runtime.sock")
	pluginDir := filepath.Join(tmpDir, "plugins")
	writeTestPluginManifest(t, pluginDir, "restarted-provider")
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bun dir: %v", err)
	}
	bunPath := filepath.Join(binDir, "bun")
	if err := os.WriteFile(bunPath, []byte("#!/bin/sh\nexec \"$ATTN_TEST_HELPER_BINARY\" -test.run '^TestDaemonPluginProcessHelper$'\n"), 0o755); err != nil {
		t.Fatalf("write fake bun: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := NewForTesting(sockPath)
	d.pluginDir = pluginDir
	// Isolate process launching from the daemon's asynchronous login-shell env
	// prewarm so every generation resolves the same fake bun fixture.
	d.pluginSupervisor = newPluginSupervisor(
		execPluginProcessLauncher{},
		realPluginSupervisorClock{},
		func(manifest pluginManifest, generation uint64) []string {
			return mergePluginEnvironment(os.Environ(), []string{
				"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"ATTN_SOCKET_PATH=" + sockPath,
				"ATTN_PLUGIN_NAME=" + manifest.Name,
				"ATTN_PLUGIN_GENERATION=" + strconv.FormatUint(generation, 10),
			})
		},
		d.broadcastPluginsUpdated,
	)
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := d.ensurePluginSupervisor().Snapshot("restarted-provider")
		if ok && snapshot.Generation >= 2 && snapshot.Phase == pluginPhaseConnected {
			if len(d.plugins.handlersForSurface("worktree.create")) != 1 {
				t.Fatal("restarted plugin did not restore its surface")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot, _ := d.ensurePluginSupervisor().Snapshot("restarted-provider")
	t.Fatalf("plugin did not reconnect after clean exit: %+v", snapshot)
}

func TestPluginCommandEnv_UsesLoginShellEnvironment(t *testing.T) {
	t.Setenv("PATH", "/daemon/bin")

	d := NewForTesting(filepath.Join(t.TempDir(), "plugin-runtime.sock"))
	d.loginShellEnv = []string{
		"PATH=/user/bin:/usr/bin:/bin",
		"USER_ONLY=present",
	}

	env := d.pluginCommandEnv("ATTN_PLUGIN_NAME=test-plugin")
	if got := envValue(env, "PATH"); got != "/user/bin:/usr/bin:/bin" {
		t.Fatalf("PATH=%q, want login-shell PATH", got)
	}
	if got := envValue(env, "USER_ONLY"); got != "present" {
		t.Fatalf("USER_ONLY=%q, want present", got)
	}
	if got := envValue(env, "ATTN_PLUGIN_NAME"); got != "test-plugin" {
		t.Fatalf("ATTN_PLUGIN_NAME=%q, want test-plugin", got)
	}
}

func TestDaemonPluginProcessHelper(t *testing.T) {
	if os.Getenv("ATTN_PLUGIN_HELPER") != "1" {
		return
	}

	socketPath := os.Getenv("ATTN_SOCKET_PATH")
	conn, err := dialPluginHelper(socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial daemon socket: %v", err)
	}
	defer conn.Close()

	name := os.Getenv("ATTN_PLUGIN_NAME")
	generation, err := strconv.ParseUint(os.Getenv("ATTN_PLUGIN_GENERATION"), 10, 64)
	if err != nil || generation == 0 {
		t.Fatalf("invalid plugin generation %q", os.Getenv("ATTN_PLUGIN_GENERATION"))
	}
	sendPluginHelloWithGeneration(t, conn, name, []string{"worktree.create", "worktree.delete"}, generation)
	if resp := decodeJSONRPCMessage(t, conn); resp.Error != nil {
		t.Fatalf("helper hello error=%#v", resp.Error)
	}
	if marker := os.Getenv("ATTN_PLUGIN_EXIT_ONCE_MARKER"); marker != "" {
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			return
		}
		if !os.IsExist(err) {
			t.Fatalf("create one-shot exit marker: %v", err)
		}
	}

	time.Sleep(30 * time.Second)
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func writeTestPluginManifest(t *testing.T, pluginDir, name string) {
	t.Helper()
	root := filepath.Join(pluginDir, name)
	entrypointDir := filepath.Join(root, "src")
	if err := os.MkdirAll(entrypointDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entrypointDir, "index.ts"), []byte("// fake entrypoint\n"), 0o644); err != nil {
		t.Fatalf("write fake entrypoint: %v", err)
	}
	manifest := []byte(`
name = "` + name + `"
version = "0.1.0"
attn_api_version = 4

[plugin]
entrypoint = "src/index.ts"
`)
	if err := os.WriteFile(filepath.Join(root, pluginManifestName), manifest, 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
}

func waitForSurfaceHandlers(t *testing.T, d *Daemon, surface string, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(d.plugins.handlersForSurface(surface)) == count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("surface %s did not reach handler count %d before timeout", surface, count)
}

func dialPluginHelper(socketPath string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, os.ErrDeadlineExceeded
}
