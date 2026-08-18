package supervise

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRestartsExitsWithCappedBackoff(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock})

	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for attempt := 1; attempt <= len(RestartBackoff)+1; attempt++ {
		handle := launcher.handle(attempt - 1)
		handle.exit(Exit{ExitCode: intPtr(0)})
		waitFor(t, func() bool {
			snapshot, _ := supervisor.Snapshot("fixture")
			return snapshot.Phase == PhaseBackoff && snapshot.RestartAttempt == attempt
		})
		snapshot, _ := supervisor.Snapshot("fixture")
		index := attempt - 1
		if index >= len(RestartBackoff) {
			index = len(RestartBackoff) - 1
		}
		wantDelay := RestartBackoff[index]
		if got := snapshot.NextRestartAt.Sub(clock.Now()); got != wantDelay {
			t.Fatalf("attempt %d delay=%s, want %s", attempt, got, wantDelay)
		}
		clock.Advance(wantDelay)
		waitFor(t, func() bool { return launcher.count() == attempt+1 })
	}
}

func TestRetriesStartFailure(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{startErrors: []error{errors.New("bun missing")}}
	supervisor := New(Options{Clock: clock})

	if err := supervisor.Ensure("fixture", launcher.start); err == nil {
		t.Fatal("Ensure error=nil, want start failure")
	}
	snapshot, _ := supervisor.Snapshot("fixture")
	if snapshot.Phase != PhaseBackoff || snapshot.RestartAttempt != 1 || snapshot.LastExit == nil {
		t.Fatalf("snapshot=%+v, want first backoff with exit", snapshot)
	}
	clock.Advance(250 * time.Millisecond)
	waitFor(t, func() bool { return launcher.count() == 2 })
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.Phase != PhaseStarting || !snapshot.Running {
		t.Fatalf("snapshot=%+v, want restarted process", snapshot)
	}
}

func TestResetsAttemptsOnlyAfterStableConnection(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock})
	_ = supervisor.Ensure("fixture", launcher.start)
	launcher.handle(0).exit(Exit{Error: "crash"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.RestartAttempt == 1
	})
	clock.Advance(250 * time.Millisecond)
	waitFor(t, func() bool { return launcher.count() == 2 })
	snapshot, _ := supervisor.Snapshot("fixture")
	if !supervisor.NoteConnected("fixture", snapshot.Generation) {
		t.Fatal("NoteConnected rejected current generation")
	}
	clock.Advance(StableConnection - time.Millisecond)
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.RestartAttempt != 1 {
		t.Fatalf("attempt=%d before stability window, want 1", snapshot.RestartAttempt)
	}
	clock.Advance(time.Millisecond)
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.RestartAttempt != 0 || snapshot.Phase != PhaseConnected {
		t.Fatalf("snapshot=%+v after stability window, want connected attempt 0", snapshot)
	}
}

func TestDisconnectGraceReconnectAndKill(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock})
	_ = supervisor.Ensure("fixture", launcher.start)
	snapshot, _ := supervisor.Snapshot("fixture")
	generation := snapshot.Generation
	if !supervisor.NoteConnected("fixture", generation) {
		t.Fatal("NoteConnected rejected current generation")
	}

	supervisor.NoteDisconnected("fixture", generation)
	clock.Advance(DisconnectGrace - time.Millisecond)
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
	clock.Advance(DisconnectGrace)
	if got := launcher.handle(0).killCount(); got != 1 {
		t.Fatalf("kills after expired grace=%d, want 1", got)
	}
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
}

func TestRestartsProcessThatNeverConnects(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock})
	_ = supervisor.Ensure("fixture", launcher.start)

	clock.Advance(DisconnectGrace)
	if got := launcher.handle(0).killCount(); got != 1 {
		t.Fatalf("kills after startup grace=%d, want 1", got)
	}
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff && snapshot.RestartAttempt == 1
	})
	clock.Advance(RestartBackoff[0])
	waitFor(t, func() bool { return launcher.count() == 2 })
}

func TestIntentionalStopAndShutdownNeverRestart(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock})
	_ = supervisor.Ensure("one", launcher.start)
	_ = supervisor.Ensure("two", launcher.start)
	one, _ := supervisor.Snapshot("one")
	if !supervisor.NoteConnected("one", one.Generation) {
		t.Fatal("connect one")
	}
	supervisor.NoteDisconnected("one", one.Generation)
	supervisor.Stop("one")

	stopped, _ := supervisor.Snapshot("one")
	if stopped.Phase != PhaseStopped || stopped.Running || stopped.Desired != DesiredStopped {
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
		if snapshot.Phase != PhaseStopped || snapshot.Running {
			t.Fatalf("%s snapshot=%+v after shutdown", name, snapshot)
		}
	}
	if err := supervisor.Ensure("three", launcher.start); err == nil {
		t.Fatal("Ensure after shutdown error=nil, want refusal")
	}
}

func TestTerminateGenerationRestartsOnlyTheExactProcess(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock})
	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	first, _ := supervisor.Snapshot("fixture")
	if !supervisor.NoteConnected("fixture", first.Generation) {
		t.Fatal("connect first generation")
	}
	terminated, err := supervisor.TerminateGeneration("fixture", first.Generation)
	if err != nil || !terminated {
		t.Fatalf("terminate generation %d = %t, %v; want true, nil", first.Generation, terminated, err)
	}
	if got := launcher.handle(0).killCount(); got != 1 {
		t.Fatalf("first-generation kills = %d, want 1", got)
	}
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff && snapshot.RestartAttempt == 1
	})
	backoff, _ := supervisor.Snapshot("fixture")
	if backoff.Desired != DesiredRunning {
		t.Fatalf("desired after termination = %q, want %q", backoff.Desired, DesiredRunning)
	}

	clock.Advance(RestartBackoff[0])
	waitFor(t, func() bool { return launcher.count() == 2 })
	second, _ := supervisor.Snapshot("fixture")
	if second.Generation <= first.Generation {
		t.Fatalf("replacement generation = %d, want greater than %d", second.Generation, first.Generation)
	}
	if !supervisor.NoteConnected("fixture", second.Generation) {
		t.Fatal("connect replacement generation")
	}

	terminated, err = supervisor.TerminateGeneration("fixture", first.Generation)
	if err != nil || terminated {
		t.Fatalf("terminate stale generation %d = %t, %v; want false, nil", first.Generation, terminated, err)
	}
	if got := launcher.handle(1).killCount(); got != 0 {
		t.Fatalf("replacement kills after stale termination = %d, want 0", got)
	}

	terminated, err = supervisor.TerminateGeneration("fixture", second.Generation)
	if err != nil || !terminated {
		t.Fatalf("terminate generation %d = %t, %v; want true, nil", second.Generation, terminated, err)
	}
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff && snapshot.RestartAttempt == 2
	})
	if got := launcher.handle(1).killCount(); got != 1 {
		t.Fatalf("replacement kills = %d, want 1", got)
	}
}

func TestSnapshotsStartingConnectedBackoffAndStopped(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock})
	_ = supervisor.Ensure("fixture", launcher.start)

	snapshot, _ := supervisor.Snapshot("fixture")
	if snapshot.Phase != PhaseStarting || !snapshot.Running || snapshot.Connected {
		t.Fatalf("starting snapshot=%+v", snapshot)
	}
	if !supervisor.NoteConnected("fixture", snapshot.Generation) {
		t.Fatal("connect current generation")
	}
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.Phase != PhaseConnected || !snapshot.Connected {
		t.Fatalf("connected snapshot=%+v", snapshot)
	}
	launcher.handle(0).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ = supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
	if snapshot.LastExit == nil || snapshot.NextRestartAt.IsZero() {
		t.Fatalf("backoff snapshot=%+v", snapshot)
	}
	supervisor.Stop("fixture")
	snapshot, _ = supervisor.Snapshot("fixture")
	if snapshot.Phase != PhaseStopped {
		t.Fatalf("stopped snapshot=%+v", snapshot)
	}
}

// A child that keeps dying is parked after the configured number of restarts,
// and parking is announced once — the daemon turns that into a notification.
func TestParksAfterGiveUpTripwireAndAnnouncesOnce(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	var mu sync.Mutex
	var gaveUp []Snapshot
	supervisor := New(Options{
		Clock:       clock,
		GiveUpAfter: 3,
		OnGiveUp: func(_ string, snapshot Snapshot) {
			mu.Lock()
			gaveUp = append(gaveUp, snapshot)
			mu.Unlock()
		},
	})
	_ = supervisor.Ensure("fixture", launcher.start)

	for attempt := 1; attempt <= 3; attempt++ {
		launcher.handle(attempt - 1).exit(Exit{Error: "boom"})
		waitFor(t, func() bool {
			snapshot, _ := supervisor.Snapshot("fixture")
			return snapshot.Phase == PhaseBackoff && snapshot.RestartAttempt == attempt
		})
		clock.Advance(RestartBackoff[attempt-1])
		waitFor(t, func() bool { return launcher.count() == attempt+1 })
	}
	launcher.handle(3).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseParked
	})

	snapshot, _ := supervisor.Snapshot("fixture")
	if snapshot.Running || !snapshot.NextRestartAt.IsZero() || snapshot.RestartAttempt != 3 {
		t.Fatalf("parked snapshot=%+v, want stopped-with-nothing-scheduled at attempt 3", snapshot)
	}
	clock.Advance(time.Hour)
	if got := launcher.count(); got != 4 {
		t.Fatalf("starts after parking=%d, want 4", got)
	}
	mu.Lock()
	announced := len(gaveUp)
	mu.Unlock()
	if announced != 1 {
		t.Fatalf("OnGiveUp calls=%d, want 1", announced)
	}
}

// Stop-then-Ensure is what every "restart" verb is built from, and it has to
// hand back a full restart budget. A revived child that still carries the
// attempts that parked it would park again on its first exit, which makes the
// way back from parked a door that opens once.
func TestStopClearsTheRestartBudget(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock, GiveUpAfter: 1})
	_ = supervisor.Ensure("fixture", launcher.start)

	launcher.handle(0).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
	clock.Advance(RestartBackoff[0])
	waitFor(t, func() bool { return launcher.count() == 2 })
	launcher.handle(1).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseParked
	})

	supervisor.Stop("fixture")
	if snapshot, _ := supervisor.Snapshot("fixture"); snapshot.RestartAttempt != 0 {
		t.Fatalf("restart attempts = %d after Stop, want the episode ended", snapshot.RestartAttempt)
	}
	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure after Stop: %v", err)
	}
	waitFor(t, func() bool { return launcher.count() == 3 })

	// The proof that the budget really came back: one exit puts it in backoff
	// rather than straight back into the park.
	launcher.handle(2).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
}

// Parking is reversible: the consumer's ordinary "this should be running" call
// revives the child with a clean restart budget.
func TestEnsureRevivesParkedChild(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock, GiveUpAfter: 1})
	_ = supervisor.Ensure("fixture", launcher.start)

	launcher.handle(0).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
	clock.Advance(RestartBackoff[0])
	waitFor(t, func() bool { return launcher.count() == 2 })
	launcher.handle(1).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseParked
	})

	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure on parked child: %v", err)
	}
	waitFor(t, func() bool { return launcher.count() == 3 })
	snapshot, _ := supervisor.Snapshot("fixture")
	if snapshot.Phase != PhaseStarting || !snapshot.Running || snapshot.RestartAttempt != 0 {
		t.Fatalf("revived snapshot=%+v, want a running child with a clean restart budget", snapshot)
	}
}

// And parking is only reversible deliberately. A caller that runs per unit of
// traffic — the app runtime's dispatch path — must leave a parked child parked,
// or the tripwire it crossed cannot hold: traffic would hand it a fresh restart
// budget over and over, and every parking on the way would be announced again.
func TestEnsureUnlessParkedLeavesAParkedChildAlone(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	var mu sync.Mutex
	var gaveUp int
	supervisor := New(Options{
		Clock:       clock,
		GiveUpAfter: 1,
		OnGiveUp: func(string, Snapshot) {
			mu.Lock()
			gaveUp++
			mu.Unlock()
		},
	})
	_ = supervisor.EnsureUnlessParked("fixture", launcher.start)

	launcher.handle(0).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
	clock.Advance(RestartBackoff[0])
	waitFor(t, func() bool { return launcher.count() == 2 })
	launcher.handle(1).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseParked
	})

	// Traffic keeps arriving. None of it restarts anything, and none of it
	// announces a second parking.
	for range 5 {
		if err := supervisor.EnsureUnlessParked("fixture", launcher.start); !errors.Is(err, ErrParked) {
			t.Fatalf("EnsureUnlessParked on a parked child = %v, want ErrParked", err)
		}
		clock.Advance(time.Hour)
	}
	if got := launcher.count(); got != 2 {
		t.Fatalf("starts = %d, want the 2 from before parking", got)
	}
	mu.Lock()
	announced := gaveUp
	mu.Unlock()
	if announced != 1 {
		t.Fatalf("OnGiveUp calls = %d, want 1 — one parking is one announcement", announced)
	}

	// The deliberate way back still works.
	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure on parked child: %v", err)
	}
	waitFor(t, func() bool { return launcher.count() == 3 })
}

func TestNeverParksWithNegativeGiveUpAfter(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock, GiveUpAfter: -1})
	_ = supervisor.Ensure("fixture", launcher.start)

	for attempt := 1; attempt <= DefaultGiveUpAfter+2; attempt++ {
		launcher.handle(attempt - 1).exit(Exit{Error: "boom"})
		waitFor(t, func() bool {
			snapshot, _ := supervisor.Snapshot("fixture")
			return snapshot.Phase == PhaseBackoff && snapshot.RestartAttempt == attempt
		})
		index := attempt - 1
		if index >= len(RestartBackoff) {
			index = len(RestartBackoff) - 1
		}
		clock.Advance(RestartBackoff[index])
		waitFor(t, func() bool { return launcher.count() == attempt+1 })
	}
}

// The child's own output lands in an append-only per-child file that survives
// restarts, so a crash loop leaves every generation's output behind.
func TestCapturesChildOutputPerChildAcrossRestarts(t *testing.T) {
	clock := newFakeClock()
	logDir := filepath.Join(t.TempDir(), "logs")
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock, LogDir: logDir})

	if err := supervisor.Ensure("fixture", func(req StartRequest) (Process, error) {
		if req.Log == nil {
			return nil, errors.New("no log writer")
		}
		fmt.Fprintf(req.Log, "generation %d speaking\n", req.Generation)
		return launcher.start(req)
	}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	launcher.handle(0).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
	clock.Advance(RestartBackoff[0])
	waitFor(t, func() bool { return launcher.count() == 2 })

	data, err := os.ReadFile(filepath.Join(logDir, "fixture.log"))
	if err != nil {
		t.Fatalf("read child log: %v", err)
	}
	log := string(data)
	for _, want := range []string{"generation 1 speaking", "generation 2 speaking", "starting fixture generation 2"} {
		if !strings.Contains(log, want) {
			t.Fatalf("child log missing %q:\n%s", want, log)
		}
	}
}

func TestStartsWithoutLogWriterWhenCaptureIsOff(t *testing.T) {
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: newFakeClock()})
	var sawWriter bool
	if err := supervisor.Ensure("fixture", func(req StartRequest) (Process, error) {
		sawWriter = req.Log != nil
		return launcher.start(req)
	}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if sawWriter {
		t.Fatal("start received a log writer with capture off")
	}
}

// A child name doubles as a log file name, so a name that could write outside
// the log directory is refused by name rather than sanitized silently.
func TestEnsureRefusesUnusableNames(t *testing.T) {
	supervisor := New(Options{Clock: newFakeClock()})
	launcher := &fakeLauncher{}
	for _, name := range []string{"", "   ", "..", ".", "nested/child", "../escape"} {
		if err := supervisor.Ensure(name, launcher.start); err == nil {
			t.Fatalf("Ensure(%q) error=nil, want refusal", name)
		}
	}
	if got := launcher.count(); got != 0 {
		t.Fatalf("starts for refused names=%d, want 0", got)
	}
}

type fakeLauncher struct {
	mu          sync.Mutex
	handles     []*fakeProcess
	startErrors []error
	requests    []StartRequest
}

func (l *fakeLauncher) start(req StartRequest) (Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, req)
	index := len(l.requests) - 1
	if index < len(l.startErrors) && l.startErrors[index] != nil {
		return nil, l.startErrors[index]
	}
	handle := &fakeProcess{wait: make(chan Exit, 1)}
	l.handles = append(l.handles, handle)
	return handle, nil
}

func (l *fakeLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.requests)
}

func (l *fakeLauncher) handle(index int) *fakeProcess {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handles[index]
}

type fakeProcess struct {
	mu     sync.Mutex
	wait   chan Exit
	exited bool
	kills  int
}

func (p *fakeProcess) Wait() Exit { return <-p.wait }

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.kills++
	alreadyExited := p.exited
	if !alreadyExited {
		p.exited = true
	}
	p.mu.Unlock()
	if !alreadyExited {
		p.wait <- Exit{Signal: "killed"}
	}
	return nil
}

func (p *fakeProcess) exit(exit Exit) {
	p.mu.Lock()
	if p.exited {
		p.mu.Unlock()
		return
	}
	p.exited = true
	p.mu.Unlock()
	p.wait <- exit
}

func (p *fakeProcess) killCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kills
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(delay time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{clock: c, at: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeClock) Advance(delay time.Duration) {
	target := c.Now().Add(delay)
	for {
		c.mu.Lock()
		var next *fakeTimer
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

type fakeTimer struct {
	clock   *fakeClock
	at      time.Time
	fn      func()
	stopped bool
	fired   bool
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func waitFor(t *testing.T, condition func() bool) {
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
