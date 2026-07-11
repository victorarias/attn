package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
attn_api_version = 2

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
