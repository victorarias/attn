package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDaemon_SetPluginPriority_PersistsAndReordersProviders(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "alpha-provider")
	writeTestPluginManifest(t, d.pluginDir, "beta-provider")

	registry := d.ensurePluginRegistry()
	alpha := &pluginConnection{name: "alpha-provider"}
	beta := &pluginConnection{name: "beta-provider"}
	for _, plugin := range []*pluginConnection{alpha, beta} {
		if err := registry.register(plugin); err != nil {
			t.Fatalf("register %s: %v", plugin.name, err)
		}
		if err := registry.registerSurfaces(plugin, []string{"worktree.create"}); err != nil {
			t.Fatalf("register %s surfaces: %v", plugin.name, err)
		}
	}

	if err := d.setPluginPriority("beta-provider", 50); err != nil {
		t.Fatalf("set plugin priority: %v", err)
	}

	var stored map[string]int
	if err := json.Unmarshal([]byte(d.store.GetSetting(SettingPluginPriorities)), &stored); err != nil {
		t.Fatalf("decode stored priorities: %v", err)
	}
	if got := stored["beta-provider"]; got != 50 {
		t.Fatalf("stored beta priority=%d, want 50", got)
	}

	handlers := registry.handlersForSurface("worktree.create")
	if len(handlers) != 2 {
		t.Fatalf("handler count=%d, want 2", len(handlers))
	}
	if handlers[0].PluginName != "beta-provider" {
		t.Fatalf("first provider=%q, want beta-provider", handlers[0].PluginName)
	}

	plugins := d.pluginsUpdatedMessage().Plugins
	if len(plugins) != 2 {
		t.Fatalf("plugin count=%d, want 2", len(plugins))
	}
	if plugins[0].Name != "beta-provider" || plugins[0].Priority != 50 {
		t.Fatalf("first plugin=%+v, want beta-provider priority 50", plugins[0])
	}
}

func TestDaemon_HandleListPluginsWS_ReturnsInstalledPlugins(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "worktree-provider")

	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleListPluginsWS(client)

	event := readOutboundEvent(t, client)
	if event["event"] != protocol.EventPluginsUpdated {
		t.Fatalf("event=%v, want %q", event["event"], protocol.EventPluginsUpdated)
	}
	plugins, ok := event["plugins"].([]interface{})
	if !ok || len(plugins) != 1 {
		t.Fatalf("plugins=%v, want one plugin", event["plugins"])
	}
}

func TestDaemon_PluginsUpdatedMessageIncludesSupervisorBackoff(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "recovering-provider")
	manifest, err := loadPluginManifest(filepath.Join(d.pluginDir, "recovering-provider", pluginManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newTestPluginSupervisor(clock, launcher)
	if err := d.pluginSupervisor.Ensure(manifest); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	launcher.handle(0).exit(pluginExit{ExitCode: intPtr(17)})
	waitForSupervisor(t, func() bool {
		snapshot, _ := d.pluginSupervisor.Snapshot(manifest.Name)
		return snapshot.Phase == pluginPhaseBackoff
	})

	plugins := d.pluginsUpdatedMessage().Plugins
	if len(plugins) != 1 {
		t.Fatalf("plugin count=%d, want 1", len(plugins))
	}
	plugin := plugins[0]
	if got := protocol.Deref(plugin.RuntimePhase); got != string(pluginPhaseBackoff) {
		t.Fatalf("runtime phase=%q, want backoff", got)
	}
	if got := protocol.Deref(plugin.RestartAttempt); got != 1 {
		t.Fatalf("restart attempt=%d, want 1", got)
	}
	if plugin.NextRestartAt == nil || *plugin.NextRestartAt != clock.Now().Add(pluginRestartBackoff[0]).Format(time.RFC3339Nano) {
		t.Fatalf("next restart=%v, want first backoff deadline", plugin.NextRestartAt)
	}
	if plugin.LastExit == nil || !strings.Contains(*plugin.LastExit, "exit code 17") {
		t.Fatalf("last exit=%v, want exit code 17", plugin.LastExit)
	}
}

func TestDaemon_HandleInstallPluginWS_InstallsGitSource(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")

	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake tool directory: %v", err)
	}
	gitScript := `#!/bin/sh
set -eu
test "$1" = "clone"
test "$4" = "git@ghe.spotify.net:victora/attn-snipe.git"
mkdir -p "$5/src"
cat > "$5/attn-plugin.toml" <<'EOF'
name = "attn-snipe"
version = "0.1.0"
attn_api_version = 4

[plugin]
entrypoint = "src/index.ts"
EOF
: > "$5/src/index.ts"
`
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "bun"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake bun: %v", err)
	}
	d.loginShellEnv = []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleInstallPluginWS(client, &protocol.InstallPluginMessage{
		Source: "git@ghe.spotify.net:victora/attn-snipe.git",
	})

	event := readOutboundEvent(t, client)
	if event["event"] != protocol.EventPluginActionResult || event["action"] != "install" || event["success"] != true {
		t.Fatalf("install result=%v, want successful install action", event)
	}
	if event["name"] != "attn-snipe" {
		t.Fatalf("installed name=%v, want attn-snipe", event["name"])
	}
	if _, err := os.Stat(filepath.Join(d.pluginDir, "attn-snipe", "attn-plugin.toml")); err != nil {
		t.Fatalf("installed plugin manifest missing: %v", err)
	}
}

func TestDaemon_HandleRemovePluginWSStopsSupervisorAfterDeletingFiles(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "removable")
	manifest, err := loadPluginManifest(filepath.Join(d.pluginDir, "removable", pluginManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newTestPluginSupervisor(clock, launcher)
	if err := d.pluginSupervisor.Ensure(manifest); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleRemovePluginWS(client, &protocol.RemovePluginMessage{Name: "removable"})
	event := readOutboundEvent(t, client)
	if event["success"] != true {
		t.Fatalf("remove event=%v", event)
	}
	if _, err := os.Stat(manifest.Dir); !os.IsNotExist(err) {
		t.Fatalf("plugin dir still exists: %v", err)
	}
	clock.Advance(time.Hour)
	if got := launcher.count(); got != 1 {
		t.Fatalf("start count after remove=%d, want 1", got)
	}
	snapshot, _ := d.pluginSupervisor.Snapshot("removable")
	if snapshot.Desired != pluginDesiredStopped || snapshot.Running {
		t.Fatalf("snapshot after remove=%+v", snapshot)
	}
}

func TestDaemon_HandleRemovePluginWSKeepsSupervisorRunningWhenDeletionFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "removable")
	manifest, err := loadPluginManifest(filepath.Join(d.pluginDir, "removable", pluginManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newTestPluginSupervisor(clock, launcher)
	if err := d.pluginSupervisor.Ensure(manifest); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	d.removePlugin = func(pluginDir, name string) error {
		return os.ErrPermission
	}

	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleRemovePluginWS(client, &protocol.RemovePluginMessage{Name: "removable"})
	event := readOutboundEvent(t, client)
	if event["success"] != false {
		t.Fatalf("remove event=%v, want failure", event)
	}
	if _, err := os.Stat(manifest.Dir); err != nil {
		t.Fatalf("installed plugin missing after failed remove: %v", err)
	}
	snapshot, _ := d.pluginSupervisor.Snapshot("removable")
	if snapshot.Desired != pluginDesiredRunning || !snapshot.Running {
		t.Fatalf("snapshot after failed remove=%+v", snapshot)
	}
	if got := launcher.count(); got != 1 {
		t.Fatalf("start count after failed remove=%d, want 1", got)
	}
}
