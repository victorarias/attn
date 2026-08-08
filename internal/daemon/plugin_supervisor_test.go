package daemon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/supervise"
)

// The supervision behaviors themselves (backoff, generation fencing, stability
// window, disconnect grace, parking) are covered in internal/supervise. What is
// tested here is the plugin runtime's own wiring onto it: manifests becoming
// processes, output landing in the plugin's log file, and a parked plugin
// reaching the user.

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
	handle, err := (execPluginProcessLauncher{}).Start(manifest, []string{"PLUGIN_MARKER_PATH=" + marker, "PLUGIN_MARKER_VALUE=direct"}, nil)
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

// A plugin's stdout and stderr used to go to /dev/null. They now land in the
// supervisor's per-plugin log file, which is what `attn` has to show when a
// plugin misbehaves.
func TestSupervisedPluginWritesStdoutAndStderrToItsLogFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := "#!/bin/sh\necho plugin-said-hello\necho plugin-complained >&2\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "provider"), []byte(script), 0o755); err != nil {
		t.Fatalf("write provider: %v", err)
	}
	manifestData := []byte("name = \"noisy\"\nversion = \"0.1.0\"\nattn_api_version = 5\n\n[plugin]\nkind = \"executable\"\npath = \"bin/provider\"\n")
	if err := os.WriteFile(filepath.Join(root, pluginManifestName), manifestData, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifest, err := loadPluginManifest(filepath.Join(root, pluginManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	logDir := filepath.Join(t.TempDir(), "plugin-log")
	supervisor := newPluginSupervisor(execPluginProcessLauncher{}, nil, nil, supervise.Options{LogDir: logDir})
	t.Cleanup(supervisor.Shutdown)
	if err := supervisor.Ensure(manifest); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	logPath := filepath.Join(logDir, "noisy.log")
	var captured string
	waitForSupervisor(t, func() bool {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return false
		}
		captured = string(data)
		return strings.Contains(captured, "plugin-said-hello") && strings.Contains(captured, "plugin-complained")
	})
	if !strings.Contains(captured, "starting noisy generation 1") {
		t.Fatalf("log missing the start marker:\n%s", captured)
	}
}

func TestPluginLogDirSitsOutsideThePluginDiscoveryDirectory(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "attn.sock")
	logDir := pluginLogDirForSocket(socket)
	if within, err := filepath.Rel(pluginDirForSocket(socket), logDir); err == nil && !strings.HasPrefix(within, "..") {
		t.Fatalf("log dir %s is inside the plugin discovery dir; manifests are scanned there", logDir)
	}
}

// Parking is the end of the retry line, so it has to be visible: the daemon
// turns it into a durable notification pointing back at the plugin.
func TestParkedPluginRaisesADurableNotification(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	exitCode := 9
	d.notifyPluginParked("looper", pluginRuntimeSnapshot{
		Phase:          pluginPhaseParked,
		RestartAttempt: 10,
		LastExit:       &pluginExit{At: time.Now(), ExitCode: &exitCode},
	})

	list, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("notifications=%d, want 1", len(list))
	}
	record := list[0]
	if record.Kind != notificationKindPluginParked || record.SourceKind != "plugin" || record.SourceID != "looper" {
		t.Fatalf("notification=%+v, want a plugin-parked record for looper", record)
	}
	if !strings.Contains(record.Title, "looper") || !strings.Contains(record.Body, "10") {
		t.Fatalf("notification title=%q body=%q, want the plugin name and the restart count", record.Title, record.Body)
	}
	if !strings.Contains(record.Detail, "exit code 9") {
		t.Fatalf("notification detail=%q, want the last exit", record.Detail)
	}
}

func newTestPluginSupervisor(clock *fakePluginClock, launcher *fakePluginLauncher) *pluginSupervisor {
	return newPluginSupervisor(launcher, clock, func(manifest pluginManifest, generation uint64) []string {
		return []string{fmt.Sprintf("ATTN_PLUGIN_NAME=%s", manifest.Name), fmt.Sprintf("ATTN_PLUGIN_GENERATION=%d", generation)}
	}, supervise.Options{})
}

type fakePluginLauncher struct {
	mu          sync.Mutex
	handles     []*fakePluginProcess
	startErrors []error
	envs        [][]string
}

func (l *fakePluginLauncher) Start(_ pluginManifest, env []string, _ io.Writer) (pluginProcessHandle, error) {
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
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("supervisor condition did not become true")
}

func intPtr(value int) *int { return &value }
