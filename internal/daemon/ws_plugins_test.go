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
attn_api_version = 5

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

func TestDaemon_BundledPluginIsAvailableAndInertByDefault(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "user-plugins")
	d.bundledPluginDir = filepath.Join(t.TempDir(), "bundled-plugins")
	writeTestPluginManifest(t, d.bundledPluginDir, "attn-opencode")
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newTestPluginSupervisor(newFakePluginClock(), launcher)

	d.startInstalledPlugins()
	if got := launcher.count(); got != 0 {
		t.Fatalf("bundled plugin starts=%d, want inert by default", got)
	}
	plugins := d.pluginsUpdatedMessage().Plugins
	if len(plugins) != 1 {
		t.Fatalf("plugins=%+v, want bundled catalog entry", plugins)
	}
	plugin := plugins[0]
	if plugin.Availability != "bundled" || plugin.InstallationState != "available" || !plugin.CanInstall || plugin.CanUninstall {
		t.Fatalf("available bundled plugin=%+v", plugin)
	}
}

func TestDaemon_InstallAndUninstallBundledPluginUpdatesProfileState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "user-plugins")
	d.bundledPluginDir = filepath.Join(t.TempDir(), "bundled-plugins")
	writeTestPluginManifest(t, d.bundledPluginDir, "attn-opencode")
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newTestPluginSupervisor(newFakePluginClock(), launcher)
	client := &wsClient{send: make(chan outboundMessage, 2)}

	d.handleInstallBundledPluginWS(client, &protocol.InstallBundledPluginMessage{Name: "attn-opencode"})
	if event := readOutboundEvent(t, client); event["success"] != true {
		t.Fatalf("install event=%v", event)
	}
	if got := launcher.count(); got != 1 {
		t.Fatalf("starts after install=%d, want 1", got)
	}
	installed := d.pluginsUpdatedMessage().Plugins[0]
	if installed.InstallationState != "installed" || installed.RuntimeState != "starting" || installed.CanInstall || !installed.CanUninstall {
		t.Fatalf("installed plugin=%+v", installed)
	}

	d.handleUninstallPluginWS(client, &protocol.UninstallPluginMessage{Name: "attn-opencode"})
	if event := readOutboundEvent(t, client); event["success"] != true {
		t.Fatalf("uninstall event=%v", event)
	}
	available := d.pluginsUpdatedMessage().Plugins[0]
	if available.InstallationState != "available" || available.RuntimeState != "stopped" || !available.CanInstall || available.CanUninstall {
		t.Fatalf("available plugin after uninstall=%+v", available)
	}
	if _, err := os.Stat(filepath.Join(d.bundledPluginDir, "attn-opencode", pluginManifestName)); err != nil {
		t.Fatalf("bundled artifact changed by uninstall: %v", err)
	}
}

func TestDaemon_InstallBundledPluginRejectsUserNameCollision(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "user-plugins")
	d.bundledPluginDir = filepath.Join(t.TempDir(), "bundled-plugins")
	writeTestPluginManifest(t, d.pluginDir, "attn-opencode")
	writeTestPluginManifest(t, d.bundledPluginDir, "attn-opencode")
	client := &wsClient{send: make(chan outboundMessage, 1)}

	d.handleInstallBundledPluginWS(client, &protocol.InstallBundledPluginMessage{Name: "attn-opencode"})
	event := readOutboundEvent(t, client)
	if event["success"] != false || !strings.Contains(event["error"].(string), "user plugin") {
		t.Fatalf("install collision event=%v", event)
	}
	plugins := d.pluginsUpdatedMessage().Plugins
	if len(plugins) != 1 || plugins[0].Availability != "user" {
		t.Fatalf("collision catalog=%+v, want user entry only", plugins)
	}
}

func TestDaemon_UninstallBundledPluginRejectsActiveOwnedRun(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.bundledPluginDir = filepath.Join(t.TempDir(), "bundled-plugins")
	writeTestPluginManifest(t, d.bundledPluginDir, "attn-opencode")
	if err := d.setBundledPluginInstalled("attn-opencode", true); err != nil {
		t.Fatalf("mark installed: %v", err)
	}
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{ID: "active", Agent: "opencode", State: protocol.SessionStateWorking, StateSince: now, LastSeen: now})
	if !d.store.BeginAgentDriverRun("active", "attn-opencode", "run-active") {
		t.Fatal("BeginAgentDriverRun failed")
	}
	client := &wsClient{send: make(chan outboundMessage, 1)}

	d.handleUninstallPluginWS(client, &protocol.UninstallPluginMessage{Name: "attn-opencode"})
	event := readOutboundEvent(t, client)
	if event["success"] != false || !strings.Contains(event["error"].(string), "active delegated run") {
		t.Fatalf("uninstall active-run event=%v", event)
	}
	if d.pluginsUpdatedMessage().Plugins[0].CanUninstall {
		t.Fatal("active-run plugin should not be uninstallable")
	}
}

func TestDaemon_BundledInstallDoesNotChangeMemoryWhenPersistenceFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	if got := d.installedBundledPlugins(); len(got) != 0 {
		t.Fatalf("initial installed set=%v, want empty", got)
	}
	if err := d.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := d.setBundledPluginInstalled("attn-opencode", true); err == nil {
		t.Fatal("setBundledPluginInstalled error=nil, want persistence failure")
	}
	if got := d.installedBundledPlugins(); len(got) != 0 {
		t.Fatalf("installed set after persistence failure=%v, want unchanged", got)
	}
}

func TestDaemon_BundledAppUpdatePreservesProfileInstallation(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.bundledPluginDir = filepath.Join(t.TempDir(), "bundled-plugins")
	writeTestPluginManifest(t, d.bundledPluginDir, "attn-opencode")
	if err := d.setBundledPluginInstalled("attn-opencode", true); err != nil {
		t.Fatalf("mark installed: %v", err)
	}
	manifestPath := filepath.Join(d.bundledPluginDir, "attn-opencode", pluginManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	updated := strings.Replace(string(data), `version = "0.1.0"`, `version = "0.2.0"`, 1)
	if err := os.WriteFile(manifestPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("update manifest: %v", err)
	}

	plugins := d.pluginsUpdatedMessage().Plugins
	if len(plugins) != 1 || plugins[0].Version != "0.2.0" || plugins[0].InstallationState != "installed" {
		t.Fatalf("catalog after app update=%+v, want updated installed artifact", plugins)
	}
}
