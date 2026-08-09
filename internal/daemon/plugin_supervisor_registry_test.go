package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
)

// The registry is the crash-recovery net for plugin runtime processes: a record
// appears at spawn and disappears once the process is reaped. The reap
// semantics are procreap's to test; this covers the launcher's side.
func TestExecLauncherWritesAndRemovesTheRegistryEntry(t *testing.T) {
	pluginDir := t.TempDir()
	script := filepath.Join(pluginDir, "driver.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile true; do sleep 0.05; done\n"), 0o755); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	manifest := pluginManifest{Dir: pluginDir}
	manifest.Name = "fake-plugin"
	manifest.Plugin.Kind = plugins.EntrypointExecutable
	manifest.Plugin.Path = "driver.sh"

	registryDir := t.TempDir()
	launcher := execPluginProcessLauncher{registryDir: registryDir}
	process, err := launcher.Start(manifest, os.Environ(), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	registered := process.(*reapRegisteredProcess)
	pid := registered.pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	if registered.registryPath == "" {
		t.Fatal("launcher recorded no registry path")
	}
	entry, err := procreap.ReadEntry(registered.registryPath)
	if err != nil {
		t.Fatalf("registry entry not written at spawn: %v", err)
	}
	if entry.ID != "fake-plugin" || entry.PID != pid {
		t.Fatalf("registry entry is incomplete: %+v", entry)
	}
	if entry.PGID != entry.PID {
		t.Fatalf("driver should lead its own process group, entry says pgid %d for pid %d", entry.PGID, entry.PID)
	}
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("live process does not lead its own group (pgid %d, err %v)", pgid, err)
	}
	if entry.ProcessStartTime == "" {
		t.Fatalf("registry entry carries no process start time: %+v", entry)
	}

	done := make(chan pluginExit, 1)
	go func() { done <- process.Wait() }()
	if err := process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("driver never exited after Kill")
	}
	if _, err := os.Stat(registered.registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry entry survived the driver's exit: %v", err)
	}
}
