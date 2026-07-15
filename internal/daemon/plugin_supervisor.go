package daemon

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type pluginDesiredState string

const (
	pluginDesiredRunning pluginDesiredState = "running"
	pluginDesiredStopped pluginDesiredState = "stopped"
)

type pluginRuntimePhase string

const (
	pluginPhaseStarting  pluginRuntimePhase = "starting"
	pluginPhaseConnected pluginRuntimePhase = "connected"
	pluginPhaseBackoff   pluginRuntimePhase = "backoff"
	pluginPhaseStopped   pluginRuntimePhase = "stopped"
)

type pluginStopReason string

const (
	pluginStopRemove   pluginStopReason = "remove"
	pluginStopShutdown pluginStopReason = "shutdown"
)

var pluginRestartBackoff = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

const pluginDisconnectGrace = 5 * time.Second
const pluginStableConnection = 60 * time.Second

type pluginExit struct {
	At       time.Time
	ExitCode *int
	Signal   string
	Error    string
}

func (e pluginExit) String() string {
	detail := strings.TrimSpace(e.Error)
	if detail == "" && e.Signal != "" {
		detail = "signal " + e.Signal
	}
	if detail == "" && e.ExitCode != nil {
		detail = fmt.Sprintf("exit code %d", *e.ExitCode)
	}
	if detail == "" {
		detail = "process exited"
	}
	if e.At.IsZero() {
		return detail
	}
	return fmt.Sprintf("%s: %s", e.At.Format(time.RFC3339), detail)
}

type pluginRuntimeSnapshot struct {
	Desired        pluginDesiredState
	Phase          pluginRuntimePhase
	Generation     uint64
	Running        bool
	Connected      bool
	RestartAttempt int
	StartedAt      time.Time
	ConnectedAt    time.Time
	NextRestartAt  time.Time
	LastExit       *pluginExit
}

type pluginProcessHandle interface {
	Wait() pluginExit
	Kill() error
}

type pluginProcessLauncher interface {
	Start(manifest pluginManifest, env []string) (pluginProcessHandle, error)
}

type pluginSupervisorTimer interface {
	Stop() bool
}

type pluginSupervisorClock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) pluginSupervisorTimer
}

type realPluginSupervisorClock struct{}

func (realPluginSupervisorClock) Now() time.Time { return time.Now() }
func (realPluginSupervisorClock) AfterFunc(delay time.Duration, fn func()) pluginSupervisorTimer {
	return time.AfterFunc(delay, fn)
}

type execPluginProcessLauncher struct{}

func (execPluginProcessLauncher) Start(manifest pluginManifest, env []string) (pluginProcessHandle, error) {
	cmd := exec.Command("/usr/bin/env", "bun", "run", manifest.Plugin.Entrypoint)
	cmd.Dir = manifest.Dir
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start bun process: %w", err)
	}
	return &execPluginProcess{cmd: cmd}, nil
}

type execPluginProcess struct {
	cmd *exec.Cmd
}

func (p *execPluginProcess) Wait() pluginExit {
	err := p.cmd.Wait()
	exit := pluginExit{}
	if err != nil {
		exit.Error = err.Error()
	}
	if state := p.cmd.ProcessState; state != nil {
		if code := state.ExitCode(); code >= 0 {
			exit.ExitCode = intPtr(code)
		}
		if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			exit.Signal = status.Signal().String()
		}
	}
	return exit
}

func (p *execPluginProcess) Kill() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

type managedPlugin struct {
	manifest pluginManifest
	desired  pluginDesiredState
	phase    pluginRuntimePhase

	generation     uint64
	process        pluginProcessHandle
	restartAttempt int
	startedAt      time.Time
	connectedAt    time.Time
	nextRestartAt  time.Time
	lastExit       *pluginExit

	restartTimer    pluginSupervisorTimer
	disconnectTimer pluginSupervisorTimer
	stabilityTimer  pluginSupervisorTimer
}

type pluginSupervisor struct {
	mu       sync.Mutex
	plugins  map[string]*managedPlugin
	launcher pluginProcessLauncher
	clock    pluginSupervisorClock
	env      func(pluginManifest, uint64) []string
	onChange func()
	shutdown bool
}

func newPluginSupervisor(
	launcher pluginProcessLauncher,
	clock pluginSupervisorClock,
	env func(pluginManifest, uint64) []string,
	onChange func(),
) *pluginSupervisor {
	if launcher == nil {
		launcher = execPluginProcessLauncher{}
	}
	if clock == nil {
		clock = realPluginSupervisorClock{}
	}
	if env == nil {
		env = func(pluginManifest, uint64) []string { return nil }
	}
	return &pluginSupervisor{
		plugins:  make(map[string]*managedPlugin),
		launcher: launcher,
		clock:    clock,
		env:      env,
		onChange: onChange,
	}
}

func (s *pluginSupervisor) Ensure(manifest pluginManifest) error {
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("plugin process name is required")
	}
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return errors.New("plugin supervisor is shut down")
	}
	plugin := s.plugins[manifest.Name]
	if plugin == nil {
		plugin = &managedPlugin{manifest: manifest, desired: pluginDesiredRunning, phase: pluginPhaseStarting}
		s.plugins[manifest.Name] = plugin
	} else {
		plugin.manifest = manifest
		plugin.desired = pluginDesiredRunning
		if plugin.process != nil || plugin.restartTimer != nil {
			s.mu.Unlock()
			return nil
		}
	}
	err := s.spawnLocked(plugin)
	s.mu.Unlock()
	s.notify()
	return err
}

func (s *pluginSupervisor) Stop(name string, _ pluginStopReason) {
	s.mu.Lock()
	plugin := s.plugins[name]
	if plugin == nil {
		s.mu.Unlock()
		return
	}
	plugin.desired = pluginDesiredStopped
	plugin.phase = pluginPhaseStopped
	plugin.generation++
	plugin.connectedAt = time.Time{}
	plugin.nextRestartAt = time.Time{}
	stopPluginTimer(&plugin.restartTimer)
	stopPluginTimer(&plugin.disconnectTimer)
	stopPluginTimer(&plugin.stabilityTimer)
	process := plugin.process
	plugin.process = nil
	s.mu.Unlock()
	if process != nil {
		_ = process.Kill()
	}
	s.notify()
}

func (s *pluginSupervisor) Shutdown() {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return
	}
	s.shutdown = true
	names := make([]string, 0, len(s.plugins))
	for name := range s.plugins {
		names = append(names, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		s.Stop(name, pluginStopShutdown)
	}
}

// NoteConnected accepts untracked test/manual connections, but a supervised
// plugin must present the exact generation injected into its process.
func (s *pluginSupervisor) NoteConnected(name string, generation uint64) bool {
	s.mu.Lock()
	plugin := s.plugins[name]
	if plugin == nil {
		s.mu.Unlock()
		return true
	}
	if generation == 0 || generation != plugin.generation || plugin.desired != pluginDesiredRunning || plugin.process == nil {
		s.mu.Unlock()
		return false
	}
	plugin.phase = pluginPhaseConnected
	plugin.connectedAt = s.clock.Now()
	plugin.nextRestartAt = time.Time{}
	stopPluginTimer(&plugin.disconnectTimer)
	stopPluginTimer(&plugin.stabilityTimer)
	capturedGeneration := plugin.generation
	plugin.stabilityTimer = s.clock.AfterFunc(pluginStableConnection, func() {
		s.markStable(name, capturedGeneration)
	})
	s.mu.Unlock()
	s.notify()
	return true
}

func (s *pluginSupervisor) NoteDisconnected(name string, generation uint64) {
	s.mu.Lock()
	plugin := s.plugins[name]
	if plugin == nil || generation != plugin.generation || plugin.desired != pluginDesiredRunning || plugin.process == nil {
		s.mu.Unlock()
		return
	}
	plugin.phase = pluginPhaseStarting
	plugin.connectedAt = time.Time{}
	stopPluginTimer(&plugin.stabilityTimer)
	stopPluginTimer(&plugin.disconnectTimer)
	capturedProcess := plugin.process
	capturedGeneration := plugin.generation
	plugin.disconnectTimer = s.clock.AfterFunc(pluginDisconnectGrace, func() {
		s.disconnectExpired(name, capturedGeneration, capturedProcess)
	})
	s.mu.Unlock()
	s.notify()
}

func (s *pluginSupervisor) Snapshot(name string) (pluginRuntimeSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plugin := s.plugins[name]
	if plugin == nil {
		return pluginRuntimeSnapshot{}, false
	}
	snapshot := pluginRuntimeSnapshot{
		Desired:        plugin.desired,
		Phase:          plugin.phase,
		Generation:     plugin.generation,
		Running:        plugin.process != nil,
		Connected:      plugin.phase == pluginPhaseConnected,
		RestartAttempt: plugin.restartAttempt,
		StartedAt:      plugin.startedAt,
		ConnectedAt:    plugin.connectedAt,
		NextRestartAt:  plugin.nextRestartAt,
	}
	if plugin.lastExit != nil {
		exit := *plugin.lastExit
		if plugin.lastExit.ExitCode != nil {
			exit.ExitCode = intPtr(*plugin.lastExit.ExitCode)
		}
		snapshot.LastExit = &exit
	}
	return snapshot, true
}

func (s *pluginSupervisor) spawnLocked(plugin *managedPlugin) error {
	plugin.generation++
	plugin.phase = pluginPhaseStarting
	plugin.nextRestartAt = time.Time{}
	stopPluginTimer(&plugin.restartTimer)
	generation := plugin.generation
	process, err := s.launcher.Start(plugin.manifest, s.env(plugin.manifest, generation))
	if err != nil {
		exit := pluginExit{At: s.clock.Now(), Error: err.Error()}
		plugin.lastExit = &exit
		s.scheduleRestartLocked(plugin)
		return err
	}
	plugin.process = process
	plugin.startedAt = s.clock.Now()
	stopPluginTimer(&plugin.disconnectTimer)
	name := plugin.manifest.Name
	capturedProcess := process
	plugin.disconnectTimer = s.clock.AfterFunc(pluginDisconnectGrace, func() {
		s.disconnectExpired(name, generation, capturedProcess)
	})
	go func(name string, generation uint64, process pluginProcessHandle) {
		exit := process.Wait()
		s.processExited(name, generation, process, exit)
	}(name, generation, process)
	return nil
}

func (s *pluginSupervisor) processExited(name string, generation uint64, process pluginProcessHandle, exit pluginExit) {
	s.mu.Lock()
	plugin := s.plugins[name]
	if plugin == nil || generation != plugin.generation || plugin.process != process {
		s.mu.Unlock()
		return
	}
	plugin.process = nil
	plugin.connectedAt = time.Time{}
	stopPluginTimer(&plugin.disconnectTimer)
	stopPluginTimer(&plugin.stabilityTimer)
	exit.At = s.clock.Now()
	plugin.lastExit = &exit
	if plugin.desired == pluginDesiredRunning && !s.shutdown {
		s.scheduleRestartLocked(plugin)
	} else {
		plugin.phase = pluginPhaseStopped
		plugin.nextRestartAt = time.Time{}
	}
	s.mu.Unlock()
	s.notify()
}

func (s *pluginSupervisor) scheduleRestartLocked(plugin *managedPlugin) {
	stopPluginTimer(&plugin.restartTimer)
	plugin.restartAttempt++
	index := plugin.restartAttempt - 1
	if index >= len(pluginRestartBackoff) {
		index = len(pluginRestartBackoff) - 1
	}
	delay := pluginRestartBackoff[index]
	plugin.phase = pluginPhaseBackoff
	plugin.nextRestartAt = s.clock.Now().Add(delay)
	capturedGeneration := plugin.generation
	plugin.restartTimer = s.clock.AfterFunc(delay, func() {
		s.restart(nameOf(plugin), capturedGeneration)
	})
}

func (s *pluginSupervisor) restart(name string, generation uint64) {
	s.mu.Lock()
	plugin := s.plugins[name]
	if plugin == nil || generation != plugin.generation || plugin.desired != pluginDesiredRunning || s.shutdown {
		s.mu.Unlock()
		return
	}
	plugin.restartTimer = nil
	_ = s.spawnLocked(plugin)
	s.mu.Unlock()
	s.notify()
}

func (s *pluginSupervisor) markStable(name string, generation uint64) {
	s.mu.Lock()
	plugin := s.plugins[name]
	if plugin == nil || generation != plugin.generation || plugin.phase != pluginPhaseConnected {
		s.mu.Unlock()
		return
	}
	plugin.restartAttempt = 0
	plugin.stabilityTimer = nil
	s.mu.Unlock()
	s.notify()
}

func (s *pluginSupervisor) disconnectExpired(name string, generation uint64, process pluginProcessHandle) {
	s.mu.Lock()
	plugin := s.plugins[name]
	if plugin == nil || generation != plugin.generation || plugin.process != process || plugin.phase == pluginPhaseConnected || plugin.desired != pluginDesiredRunning {
		s.mu.Unlock()
		return
	}
	plugin.disconnectTimer = nil
	s.mu.Unlock()
	_ = process.Kill()
}

func (s *pluginSupervisor) notify() {
	if s.onChange != nil {
		s.onChange()
	}
}

func stopPluginTimer(timer *pluginSupervisorTimer) {
	if *timer != nil {
		(*timer).Stop()
		*timer = nil
	}
}

func nameOf(plugin *managedPlugin) string { return plugin.manifest.Name }

func intPtr(value int) *int { return &value }
