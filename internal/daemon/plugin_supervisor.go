package daemon

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/supervise"
)

// The plugin runtime is one consumer of internal/supervise: every installed
// plugin is a supervised child named by its manifest, launched with the plugin
// environment the daemon computes for that generation. Restart backoff,
// generation fencing, the stability window, the disconnect grace and the
// give-up tripwire all live in the shared package; what stays here is how a
// plugin manifest becomes a process.

const (
	pluginDesiredRunning = supervise.DesiredRunning
	pluginDesiredStopped = supervise.DesiredStopped
)

const (
	pluginPhaseStarting  = supervise.PhaseStarting
	pluginPhaseConnected = supervise.PhaseConnected
	pluginPhaseBackoff   = supervise.PhaseBackoff
	pluginPhaseStopped   = supervise.PhaseStopped
	pluginPhaseParked    = supervise.PhaseParked
)

var pluginRestartBackoff = supervise.RestartBackoff

const pluginDisconnectGrace = supervise.DisconnectGrace

type pluginExit = supervise.Exit
type pluginRuntimeSnapshot = supervise.Snapshot
type pluginProcessHandle = supervise.Process
type pluginSupervisorTimer = supervise.Timer
type pluginSupervisorClock = supervise.Clock

// pluginProcessLauncher turns one manifest into a running process. Log is the
// supervisor's per-plugin append-only log file, or nil when capture is off.
type pluginProcessLauncher interface {
	Start(manifest pluginManifest, env []string, log io.Writer) (pluginProcessHandle, error)
}

type execPluginProcessLauncher struct{}

func (execPluginProcessLauncher) Start(manifest pluginManifest, env []string, log io.Writer) (pluginProcessHandle, error) {
	var cmd *exec.Cmd
	switch manifest.Plugin.Kind {
	case plugins.EntrypointExecutable:
		cmd = exec.Command(filepath.Join(manifest.Dir, manifest.Plugin.Path))
	case plugins.EntrypointBun:
		cmd = exec.Command("/usr/bin/env", "bun", "run", manifest.Plugin.Path)
	default:
		return nil, fmt.Errorf("start plugin %q: unsupported entrypoint kind %q", manifest.Name, manifest.Plugin.Kind)
	}
	cmd.Dir = manifest.Dir
	cmd.Env = env
	process, err := supervise.StartCommand(cmd, log)
	if err != nil {
		return nil, fmt.Errorf("start %s plugin process: %w", manifest.Plugin.Kind, err)
	}
	return process, nil
}

// pluginSupervisor adapts the shared supervisor to plugin manifests.
type pluginSupervisor struct {
	*supervise.Supervisor
	launcher pluginProcessLauncher
	env      func(pluginManifest, uint64) []string
}

func newPluginSupervisor(
	launcher pluginProcessLauncher,
	clock pluginSupervisorClock,
	env func(pluginManifest, uint64) []string,
	options supervise.Options,
) *pluginSupervisor {
	if launcher == nil {
		launcher = execPluginProcessLauncher{}
	}
	if env == nil {
		env = func(pluginManifest, uint64) []string { return nil }
	}
	options.Clock = clock
	return &pluginSupervisor{
		Supervisor: supervise.New(options),
		launcher:   launcher,
		env:        env,
	}
}

// Ensure starts the plugin, or adopts the manifest for its next start.
func (s *pluginSupervisor) Ensure(manifest pluginManifest) error {
	return s.Supervisor.Ensure(manifest.Name, func(req supervise.StartRequest) (supervise.Process, error) {
		return s.launcher.Start(manifest, s.env(manifest, req.Generation), req.Log)
	})
}
