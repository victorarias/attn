package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPluginSupervisorRestartsExitsWithCappedBackoff(t *testing.T) {
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	supervisor := newTestPluginSupervisor(clock, launcher)
	manifest := pluginManifest{Name: "fixture"}

	if err := supervisor.Ensure(manifest); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for attempt := 1; attempt <= len(pluginRestartBackoff)+2; attempt++ {
		handle := launcher.handle(attempt - 1)
		handle.exit(pluginExit{ExitCode: intPtr(0)})
		waitForSupervisor(t, func() bool {
			snapshot, _ := supervisor.Snapshot("fixture")
			return snapshot.Phase == pluginPhaseBackoff && snapshot.RestartAttempt == attempt
		})
		snapshot, _ := supervisor.Snapshot("fixture")
		index := attempt - 1
		if index >= len(pluginRestartBackoff) {
			index = len(pluginRestartBackoff) - 1
		}
		wantDelay := pluginRestartBackoff[index]
		if got := snapshot.NextRestartAt.Sub(clock.Now()); got != wantDelay {
			t.Fatalf("attempt %d delay=%s, want %s", attempt, got, wantDelay)
		}
		clock.Advance(wantDelay)
		waitForSupervisor(t, func() bool { return launcher.count() == attempt+1 })
	}
}

func TestPluginSupervisorRetriesStartFailure(t *testing.T) {
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{startErrors: []error{errors.New("bun missing")}}
	supervisor := newTestPluginSupervisor(clock, launcher)

	if err := supervisor.Ensure(pluginManifest{Name: "fixture"}); err == nil {
		t.Fatal("Ensure error=nil, want start failure")
	}
	snapshot, _ := supervisor.Snapshot("fixture")
	if snapshot.Phase != pluginPhaseBackoff || snapshot.RestartAttempt != 1 || snapshot.LastExit == nil {
		t.Fatalf("snapshot=%+v, want first backoff with exit", snapshot)
	}
	clock.Advance(250 * time.Millisecond)
	waitForSupervisor(t, func() bool { return launcher.count() == 2 })
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.Phase != pluginPhaseStarting || !snapshot.Running {
		t.Fatalf("snapshot=%+v, want restarted process", snapshot)
	}
}

func TestPluginSupervisorResetsAttemptsOnlyAfterStableConnection(t *testing.T) {
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	supervisor := newTestPluginSupervisor(clock, launcher)
	_ = supervisor.Ensure(pluginManifest{Name: "fixture"})
	launcher.handle(0).exit(pluginExit{Error: "crash"})
	waitForSupervisor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.RestartAttempt == 1
	})
	clock.Advance(250 * time.Millisecond)
	waitForSupervisor(t, func() bool { return launcher.count() == 2 })
	snapshot, _ := supervisor.Snapshot("fixture")
	if !supervisor.NoteConnected("fixture", snapshot.Generation) {
		t.Fatal("NoteConnected rejected current generation")
	}
	clock.Advance(pluginStableConnection - time.Millisecond)
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.RestartAttempt != 1 {
		t.Fatalf("attempt=%d before stability window, want 1", snapshot.RestartAttempt)
	}
	clock.Advance(time.Millisecond)
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.RestartAttempt != 0 || snapshot.Phase != pluginPhaseConnected {
		t.Fatalf("snapshot=%+v after stability window, want connected attempt 0", snapshot)
	}
}

func TestPluginSupervisorDisconnectGraceReconnectAndKill(t *testing.T) {
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	supervisor := newTestPluginSupervisor(clock, launcher)
	_ = supervisor.Ensure(pluginManifest{Name: "fixture"})
	snapshot, _ := supervisor.Snapshot("fixture")
	generation := snapshot.Generation
	if !supervisor.NoteConnected("fixture", generation) {
		t.Fatal("NoteConnected rejected current generation")
	}

	supervisor.NoteDisconnected("fixture", generation)
	clock.Advance(pluginDisconnectGrace - time.Millisecond)
	if got := launcher.handle(0).killCount(); got != 0 {
		t.Fatalf("kills before grace=%d, want 0", got)
	}
	if !supervisor.NoteConnected("fixture", generation) {
		t.Fatal("same-generation reconnect was rejected")
	}
	clock.Advance(time.Millisecond)
	if got := launcher.handle(0).killCount(); got != 0 {
		t.Fatalf("kills after canceled grace=%d, want 0", got)
	}

	supervisor.NoteDisconnected("fixture", generation)
	clock.Advance(pluginDisconnectGrace)
	if got := launcher.handle(0).killCount(); got != 1 {
		t.Fatalf("kills after expired grace=%d, want 1", got)
	}
	waitForSupervisor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == pluginPhaseBackoff
	})
}

func TestPluginSupervisorRestartsProcessThatNeverConnects(t *testing.T) {
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	supervisor := newTestPluginSupervisor(clock, launcher)
	_ = supervisor.Ensure(pluginManifest{Name: "fixture"})

	clock.Advance(pluginDisconnectGrace)
	if got := launcher.handle(0).killCount(); got != 1 {
		t.Fatalf("kills after startup grace=%d, want 1", got)
	}
	waitForSupervisor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == pluginPhaseBackoff && snapshot.RestartAttempt == 1
	})
	clock.Advance(pluginRestartBackoff[0])
	waitForSupervisor(t, func() bool { return launcher.count() == 2 })
}

func TestPluginSupervisorIntentionalStopAndShutdownNeverRestart(t *testing.T) {
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	supervisor := newTestPluginSupervisor(clock, launcher)
	_ = supervisor.Ensure(pluginManifest{Name: "one"})
	_ = supervisor.Ensure(pluginManifest{Name: "two"})
	one, _ := supervisor.Snapshot("one")
	if !supervisor.NoteConnected("one", one.Generation) {
		t.Fatal("connect one")
	}
	supervisor.NoteDisconnected("one", one.Generation)
	supervisor.Stop("one", pluginStopRemove)

	stopped, _ := supervisor.Snapshot("one")
	if stopped.Phase != pluginPhaseStopped || stopped.Running || stopped.Desired != pluginDesiredStopped {
		t.Fatalf("stopped snapshot=%+v", stopped)
	}
	if supervisor.NoteConnected("one", one.Generation) {
		t.Fatal("stale generation reconnect accepted")
	}
	clock.Advance(time.Hour)
	if got := launcher.count(); got != 2 {
		t.Fatalf("starts after intentional stop=%d, want 2", got)
	}

	supervisor.Shutdown()
	clock.Advance(time.Hour)
	if got := launcher.count(); got != 2 {
		t.Fatalf("starts after shutdown=%d, want 2", got)
	}
	for _, name := range []string{"one", "two"} {
		snapshot, _ := supervisor.Snapshot(name)
		if snapshot.Phase != pluginPhaseStopped || snapshot.Running {
			t.Fatalf("%s snapshot=%+v after shutdown", name, snapshot)
		}
	}
}

func TestPluginSupervisorSnapshotsStartingConnectedBackoffAndStopped(t *testing.T) {
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	supervisor := newTestPluginSupervisor(clock, launcher)
	_ = supervisor.Ensure(pluginManifest{Name: "fixture"})

	snapshot, _ := supervisor.Snapshot("fixture")
	if snapshot.Phase != pluginPhaseStarting || !snapshot.Running || snapshot.Connected {
		t.Fatalf("starting snapshot=%+v", snapshot)
	}
	if !supervisor.NoteConnected("fixture", snapshot.Generation) {
		t.Fatal("connect current generation")
	}
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.Phase != pluginPhaseConnected || !snapshot.Connected {
		t.Fatalf("connected snapshot=%+v", snapshot)
	}
	launcher.handle(0).exit(pluginExit{Error: "boom"})
	waitForSupervisor(t, func() bool {
		snapshot, _ = supervisor.Snapshot("fixture")
		return snapshot.Phase == pluginPhaseBackoff
	})
	if snapshot.LastExit == nil || snapshot.NextRestartAt.IsZero() {
		t.Fatalf("backoff snapshot=%+v", snapshot)
	}
	supervisor.Stop("fixture", pluginStopRemove)
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.Phase != pluginPhaseStopped {
		t.Fatalf("stopped snapshot=%+v", snapshot)
	}
}

func TestExecPluginProcessLauncherRunsExecutableWithoutBun(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "started")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := "#!/bin/sh\nprintf '%s' \"$PLUGIN_MARKER_VALUE\" > \"$PLUGIN_MARKER_PATH\"\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "provider"), []byte(script), 0o755); err != nil {
		t.Fatalf("write provider: %v", err)
	}
	manifestData := []byte("name = \"provider\"\nversion = \"0.1.0\"\nattn_api_version = 5\n\n[plugin]\nkind = \"executable\"\npath = \"bin/provider\"\n")
	if err := os.WriteFile(filepath.Join(root, pluginManifestName), manifestData, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifest, err := loadPluginManifest(filepath.Join(root, pluginManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	handle, err := (execPluginProcessLauncher{}).Start(manifest, []string{"PLUGIN_MARKER_PATH=" + marker, "PLUGIN_MARKER_VALUE=direct"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if exit := handle.Wait(); exit.Error != "" || exit.Signal != "" || exit.ExitCode == nil || *exit.ExitCode != 0 {
		t.Fatalf("exit=%+v", exit)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(data) != "direct" {
		t.Fatalf("marker=%q, want direct", data)
	}
}

func newTestPluginSupervisor(clock *fakePluginClock, launcher *fakePluginLauncher) *pluginSupervisor {
	return newPluginSupervisor(launcher, clock, func(manifest pluginManifest, generation uint64) []string {
		return []string{fmt.Sprintf("ATTN_PLUGIN_NAME=%s", manifest.Name), fmt.Sprintf("ATTN_PLUGIN_GENERATION=%d", generation)}
	}, nil)
}

type fakePluginLauncher struct {
	mu          sync.Mutex
	handles     []*fakePluginProcess
	startErrors []error
	envs        [][]string
}

func (l *fakePluginLauncher) Start(_ pluginManifest, env []string) (pluginProcessHandle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.envs = append(l.envs, append([]string(nil), env...))
	index := len(l.envs) - 1
	if index < len(l.startErrors) && l.startErrors[index] != nil {
		return nil, l.startErrors[index]
	}
	handle := &fakePluginProcess{wait: make(chan pluginExit, 1)}
	l.handles = append(l.handles, handle)
	return handle, nil
}

func (l *fakePluginLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.envs)
}

func (l *fakePluginLauncher) handle(index int) *fakePluginProcess {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handles[index]
}

type fakePluginProcess struct {
	mu     sync.Mutex
	wait   chan pluginExit
	exited bool
	kills  int
}

func (p *fakePluginProcess) Wait() pluginExit { return <-p.wait }

func (p *fakePluginProcess) Kill() error {
	p.mu.Lock()
	p.kills++
	alreadyExited := p.exited
	if !alreadyExited {
		p.exited = true
	}
	p.mu.Unlock()
	if !alreadyExited {
		p.wait <- pluginExit{Signal: "killed"}
	}
	return nil
}

func (p *fakePluginProcess) exit(exit pluginExit) {
	p.mu.Lock()
	if p.exited {
		p.mu.Unlock()
		return
	}
	p.exited = true
	p.mu.Unlock()
	p.wait <- exit
}

func (p *fakePluginProcess) killCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kills
}

type fakePluginClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakePluginTimer
}

func newFakePluginClock() *fakePluginClock {
	return &fakePluginClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *fakePluginClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakePluginClock) AfterFunc(delay time.Duration, fn func()) pluginSupervisorTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakePluginTimer{clock: c, at: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakePluginClock) Advance(delay time.Duration) {
	target := c.Now().Add(delay)
	for {
		c.mu.Lock()
		var next *fakePluginTimer
		for _, timer := range c.timers {
			if timer.stopped || timer.fired || timer.at.After(target) {
				continue
			}
			if next == nil || timer.at.Before(next.at) {
				next = timer
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		c.now = next.at
		next.fired = true
		fn := next.fn
		c.mu.Unlock()
		fn()
	}
}

type fakePluginTimer struct {
	clock   *fakePluginClock
	at      time.Time
	fn      func()
	stopped bool
	fired   bool
}

func (t *fakePluginTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func waitForSupervisor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("supervisor condition did not become true")
}
