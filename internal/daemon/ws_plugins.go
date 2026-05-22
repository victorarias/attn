package daemon

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/protocol"
)

const SettingPluginPriorities = "plugin_provider_priorities"

func (d *Daemon) handleListPluginsWS(client *wsClient) {
	d.sendToClient(client, d.pluginsUpdatedMessage())
}

func (d *Daemon) handleInstallPluginWS(client *wsClient, msg *protocol.InstallPluginMessage) {
	path := strings.TrimSpace(msg.Path)
	if path == "" {
		d.sendPluginActionResult(client, "install", "", false, "plugin path is required")
		return
	}

	manifest, err := plugins.InstallPathWithOptions(path, d.pluginDir, plugins.InstallOptions{
		Env: d.pluginCommandEnv(),
	})
	if err != nil {
		d.sendPluginActionResult(client, "install", "", false, err.Error())
		return
	}
	if err := d.startInstalledPlugin(manifest); err != nil {
		d.logf("plugin %s installed but failed to start: %v", manifest.Name, err)
	}

	d.broadcastPluginsUpdated()
	d.sendPluginActionResult(client, "install", manifest.Name, true, "")
}

func (d *Daemon) handleRemovePluginWS(client *wsClient, msg *protocol.RemovePluginMessage) {
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		d.sendPluginActionResult(client, "remove", "", false, "plugin name is required")
		return
	}
	if err := plugins.Remove(d.pluginDir, name); err != nil {
		d.sendPluginActionResult(client, "remove", name, false, err.Error())
		return
	}

	d.stopInstalledPlugin(name)
	d.broadcastPluginsUpdated()
	d.sendPluginActionResult(client, "remove", name, true, "")
}

func (d *Daemon) handleSetPluginPriorityWS(client *wsClient, msg *protocol.SetPluginPriorityMessage) {
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		d.sendPluginActionResult(client, "set_priority", "", false, "plugin name is required")
		return
	}
	if err := d.setPluginPriority(name, int(msg.Priority)); err != nil {
		d.sendPluginActionResult(client, "set_priority", name, false, err.Error())
		return
	}

	d.broadcastPluginsUpdated()
	d.sendPluginActionResult(client, "set_priority", name, true, "")
}

func (d *Daemon) broadcastPluginsUpdated() {
	d.wsHub.BroadcastValue(d.pluginsUpdatedMessage())
}

func (d *Daemon) pluginsUpdatedMessage() *protocol.PluginsUpdatedMessage {
	manifests, manifestIssues := discoverPluginManifests(d.pluginDir)
	priorities := d.pluginPriorities()
	pluginInfos := make([]protocol.PluginInfo, 0, len(manifests))
	for _, manifest := range manifests {
		info := protocol.PluginInfo{
			Name:      manifest.Name,
			Version:   manifest.Version,
			Dir:       manifest.Dir,
			Priority:  priorities[manifest.Name],
			Connected: d.ensurePluginRegistry().get(manifest.Name) != nil,
			Running:   d.ensurePluginProcessRegistry().isRunning(manifest.Name),
		}
		if manifest.Description != "" {
			info.Description = protocol.Ptr(manifest.Description)
		}
		pluginInfos = append(pluginInfos, info)
	}
	sort.Slice(pluginInfos, func(i, j int) bool {
		if pluginInfos[i].Priority != pluginInfos[j].Priority {
			return pluginInfos[i].Priority > pluginInfos[j].Priority
		}
		return pluginInfos[i].Name < pluginInfos[j].Name
	})

	var issues []protocol.PluginIssue
	for _, issue := range manifestIssues {
		issues = append(issues, protocol.PluginIssue{
			Path:  issue.Path,
			Error: issue.Err.Error(),
		})
	}

	message := &protocol.PluginsUpdatedMessage{
		Event:   protocol.EventPluginsUpdated,
		Plugins: pluginInfos,
	}
	if len(issues) > 0 {
		message.Issues = issues
	}
	return message
}

func (d *Daemon) sendPluginActionResult(client *wsClient, action, name string, success bool, errMsg string) {
	result := &protocol.PluginActionResultMessage{
		Event:   protocol.EventPluginActionResult,
		Action:  action,
		Success: success,
	}
	if name != "" {
		result.Name = protocol.Ptr(name)
	}
	if errMsg != "" {
		result.Error = protocol.Ptr(errMsg)
	}
	d.sendToClient(client, result)
}

func (d *Daemon) pluginPriorities() map[string]int {
	raw := strings.TrimSpace(d.store.GetSetting(SettingPluginPriorities))
	if raw == "" {
		return map[string]int{}
	}
	priorities := map[string]int{}
	if err := json.Unmarshal([]byte(raw), &priorities); err != nil {
		d.logf("failed to decode plugin priorities: %v", err)
		return map[string]int{}
	}
	return priorities
}

func (d *Daemon) setPluginPriority(name string, priority int) error {
	if priority < -1_000_000 || priority > 1_000_000 {
		return fmt.Errorf("plugin priority must be between -1000000 and 1000000")
	}
	priorities := d.pluginPriorities()
	priorities[name] = priority
	encoded, err := json.Marshal(priorities)
	if err != nil {
		return fmt.Errorf("encode plugin priorities: %w", err)
	}
	d.store.SetSetting(SettingPluginPriorities, string(encoded))
	d.ensurePluginRegistry().setPriorities(priorities)
	return nil
}
