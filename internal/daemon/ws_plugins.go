package daemon

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/protocol"
)

const SettingPluginPriorities = "plugin_provider_priorities"

func (d *Daemon) handleListPluginsWS(client *wsClient) {
	d.sendToClient(client, d.pluginsUpdatedMessage())
}

func (d *Daemon) handleInstallPluginWS(client *wsClient, msg *protocol.InstallPluginMessage) {
	source := strings.TrimSpace(msg.Source)
	if source == "" {
		d.sendPluginActionResult(client, "install", "", false, "plugin source is required")
		return
	}

	manifest, err := plugins.InstallSourceWithOptions(source, d.pluginDir, plugins.InstallOptions{
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
	d.stopInstalledPlugin(name)
	if err := plugins.Remove(d.pluginDir, name); err != nil {
		d.sendPluginActionResult(client, "remove", name, false, err.Error())
		return
	}

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
		connection := d.ensurePluginRegistry().get(manifest.Name)
		runtime, supervised := d.ensurePluginSupervisor().Snapshot(manifest.Name)
		healthStatus := "unknown"
		var healthMessage string
		var lastHealthAt string
		if connection != nil {
			status, message, checkedAt := connection.healthSnapshot()
			healthStatus = status
			healthMessage = message
			if !checkedAt.IsZero() {
				lastHealthAt = checkedAt.Format(time.RFC3339Nano)
			}
		}
		info := protocol.PluginInfo{
			Name:         manifest.Name,
			Version:      manifest.Version,
			Dir:          manifest.Dir,
			Priority:     priorities[manifest.Name],
			Connected:    connection != nil,
			Running:      runtime.Running,
			HealthStatus: protocol.Ptr(healthStatus),
		}
		runtimePhase := pluginPhaseStopped
		if supervised {
			runtimePhase = runtime.Phase
		} else if connection != nil {
			runtimePhase = pluginPhaseConnected
		}
		info.RuntimePhase = protocol.Ptr(string(runtimePhase))
		if runtime.RestartAttempt > 0 {
			info.RestartAttempt = protocol.Ptr(runtime.RestartAttempt)
		}
		if !runtime.NextRestartAt.IsZero() {
			info.NextRestartAt = protocol.Ptr(runtime.NextRestartAt.Format(time.RFC3339Nano))
		}
		if runtime.LastExit != nil {
			info.LastExit = protocol.Ptr(runtime.LastExit.String())
		}
		if manifest.Description != "" {
			info.Description = protocol.Ptr(manifest.Description)
		}
		if healthMessage != "" {
			info.HealthMessage = protocol.Ptr(healthMessage)
		}
		if lastHealthAt != "" {
			info.LastHealthAt = protocol.Ptr(lastHealthAt)
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
