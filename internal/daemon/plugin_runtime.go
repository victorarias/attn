package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/victorarias/attn/internal/plugins"
)

const pluginManifestName = plugins.ManifestName

type pluginManifest = plugins.Manifest
type pluginManifestIssue = plugins.ManifestIssue

func discoverPluginManifests(pluginDir string) ([]pluginManifest, []pluginManifestIssue) {
	return plugins.Discover(pluginDir)
}

func loadPluginManifest(path string) (pluginManifest, error) {
	return plugins.LoadManifest(path)
}

type pluginProcessRegistry struct {
	mu        sync.Mutex
	processes map[string]*pluginProcess
}

type pluginProcess struct {
	manifest pluginManifest
	cmd      *exec.Cmd
}

func newPluginProcessRegistry() *pluginProcessRegistry {
	return &pluginProcessRegistry{processes: make(map[string]*pluginProcess)}
}

func (r *pluginProcessRegistry) add(process *pluginProcess) error {
	if process == nil || process.manifest.Name == "" {
		return errors.New("plugin process name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.processes[process.manifest.Name]; exists {
		return fmt.Errorf("plugin process %q is already running", process.manifest.Name)
	}
	r.processes[process.manifest.Name] = process
	return nil
}

func (r *pluginProcessRegistry) remove(name string, process *pluginProcess) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processes[name] == process {
		delete(r.processes, name)
	}
}

func (r *pluginProcessRegistry) get(name string) *pluginProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processes[name]
}

func (r *pluginProcessRegistry) isRunning(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processes[name] != nil
}

func (r *pluginProcessRegistry) list() []*pluginProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	processes := make([]*pluginProcess, 0, len(r.processes))
	for _, process := range r.processes {
		processes = append(processes, process)
	}
	return processes
}

func (d *Daemon) ensurePluginProcessRegistry() *pluginProcessRegistry {
	if d.pluginProcesses == nil {
		d.pluginProcesses = newPluginProcessRegistry()
	}
	return d.pluginProcesses
}

func (d *Daemon) startInstalledPlugins() {
	manifests, issues := discoverPluginManifests(d.pluginDir)
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
	cmd := exec.Command("bun", "run", manifest.Plugin.Entrypoint)
	cmd.Dir = manifest.Dir
	cmd.Env = append(
		os.Environ(),
		"ATTN_SOCKET_PATH="+d.socketPath,
		"ATTN_PLUGIN_NAME="+manifest.Name,
	)

	process := &pluginProcess{manifest: manifest, cmd: cmd}
	registry := d.ensurePluginProcessRegistry()
	if err := registry.add(process); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		registry.remove(manifest.Name, process)
		return fmt.Errorf("start bun process: %w", err)
	}

	go func() {
		err := cmd.Wait()
		registry.remove(manifest.Name, process)
		select {
		case <-d.done:
			return
		default:
		}
		if err != nil {
			d.logf("plugin %s exited: %v", manifest.Name, err)
			return
		}
		d.logf("plugin %s exited", manifest.Name)
	}()
	return nil
}

func (d *Daemon) stopInstalledPlugins() {
	for _, process := range d.ensurePluginProcessRegistry().list() {
		if process.cmd == nil || process.cmd.Process == nil {
			continue
		}
		_ = process.cmd.Process.Kill()
	}
}

func (d *Daemon) stopInstalledPlugin(name string) {
	process := d.ensurePluginProcessRegistry().get(name)
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return
	}
	_ = process.cmd.Process.Kill()
}
