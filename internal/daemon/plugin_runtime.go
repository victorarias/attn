package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/plugins"
)

const pluginManifestName = plugins.ManifestName

type pluginManifest = plugins.Manifest
type pluginManifestIssue = plugins.ManifestIssue

func discoverPluginManifests(pluginDir string) ([]pluginManifest, []pluginManifestIssue) {
	return plugins.Discover(pluginDir)
}

// pluginDirForSocket keeps plugin discovery in the same runtime root as the
// daemon socket. App-managed daemon restarts route by socket path, so relying
// only on an inherited ATTN_PROFILE can otherwise make a profile daemon start
// against the default profile's plugins after a restart.
func pluginDirForSocket(socketPath string) string {
	if override := strings.TrimSpace(os.Getenv("ATTN_PLUGIN_DIR")); override != "" {
		return override
	}
	return filepath.Join(filepath.Dir(socketPath), "plugins")
}

func loadPluginManifest(path string) (pluginManifest, error) {
	return plugins.LoadManifest(path)
}

func (d *Daemon) ensurePluginSupervisor() *pluginSupervisor {
	d.pluginSupervisorMu.Lock()
	defer d.pluginSupervisorMu.Unlock()
	if d.pluginSupervisor == nil {
		d.pluginSupervisor = newPluginSupervisor(
			execPluginProcessLauncher{},
			realPluginSupervisorClock{},
			func(manifest pluginManifest, generation uint64) []string {
				return d.pluginCommandEnv(
					"ATTN_SOCKET_PATH="+d.socketPath,
					"ATTN_PLUGIN_NAME="+manifest.Name,
					"ATTN_PLUGIN_GENERATION="+strconv.FormatUint(generation, 10),
				)
			},
			d.broadcastPluginsUpdated,
		)
	}
	return d.pluginSupervisor
}

func (d *Daemon) startInstalledPlugins() {
	manifests, issues := discoverPluginManifests(d.pluginDir)
	d.logf("plugin discovery dir=%s manifests=%d issues=%d", d.pluginDir, len(manifests), len(issues))
	for _, issue := range issues {
		d.logf("plugin manifest skipped: %v", issue)
	}
	for _, manifest := range manifests {
		if err := d.startInstalledPlugin(manifest); err != nil {
			d.logf("plugin %s failed to start: %v", manifest.Name, err)
		}
	}
}

func (d *Daemon) startInstalledPlugin(manifest pluginManifest) error {
	return d.ensurePluginSupervisor().Ensure(manifest)
}

func (d *Daemon) pluginCommandEnv(extra ...string) []string {
	env := append([]string(nil), os.Environ()...)
	env = mergePluginEnvironment(env, d.cachedLoginShellEnv())
	env = mergePluginEnvironment(env, extra)
	return env
}

func mergePluginEnvironment(base, overlay []string) []string {
	if len(overlay) == 0 {
		return append([]string(nil), base...)
	}

	merged := make([]string, 0, len(base)+len(overlay))
	index := make(map[string]int, len(base)+len(overlay))
	add := func(entry string) {
		key := entry
		if split := strings.Index(entry, "="); split >= 0 {
			key = entry[:split]
		}
		if pos, ok := index[key]; ok {
			merged[pos] = entry
			return
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range overlay {
		add(entry)
	}
	return merged
}

func (d *Daemon) stopInstalledPlugins() {
	d.ensurePluginSupervisor().Shutdown()
}

func (d *Daemon) stopInstalledPlugin(name string) {
	d.ensurePluginSupervisor().Stop(name, pluginStopRemove)
}
