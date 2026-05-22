package daemon

import (
	"encoding/json"
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
