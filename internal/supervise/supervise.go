// Package supervise keeps long-lived daemon child processes alive: it starts
// them, restarts them with capped exponential backoff, fences stale processes
// and timers behind a generation counter, and parks a child that keeps dying
// without ever running stably.
//
// The supervisor knows nothing about what it supervises. A consumer names a
// child and hands over a StartFunc; everything the child needs to be launched —
// binary, arguments, environment — is closed over there. Two consumers exist:
// the daemon's plugin runtime (one child per installed plugin) and the app
// runtime's Bun sidecar.
//
// A supervised child is expected to dial the daemon back and announce itself,
// which the consumer reports through NoteConnected/NoteDisconnected. A child
// that starts but never calls back inside the disconnect grace is killed, which
// puts it back on the restart path.
package supervise

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DesiredState is what the consumer asked for, independent of what the child is
// currently doing.
type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
)

// Phase is where one child sits in the supervision cycle. It is reported
// verbatim on attn's wire, so the strings are part of the observable surface.
type Phase string

const (
	PhaseStarting  Phase = "starting"
	PhaseConnected Phase = "connected"
	PhaseBackoff   Phase = "backoff"
	PhaseStopped   Phase = "stopped"
	// PhaseParked is a child the supervisor has given up restarting. Nothing
	// is scheduled; only a fresh Ensure brings it back.
	PhaseParked Phase = "parked"
)

// RestartBackoff is the delay before restart attempt N (capped at the last
// entry). Eight steps from 250ms to 30s.
var RestartBackoff = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

// DisconnectGrace is how long a started child has to dial back before it is
// killed, and how long a connected child that drops its connection has to
// reconnect.
const DisconnectGrace = 5 * time.Second

// StableConnection is how long a connection must hold before the child counts
// as healthy and its restart attempts reset.
const StableConnection = 60 * time.Second

// DefaultGiveUpAfter is how many consecutive restarts a child gets without ever
// reaching a stability window before it is parked.
//
// Tripwire, not a receipt: no healthy child restarts twice, let alone ten
// times. At the pinned backoff, ten restarts cost 121.75s of waiting
// (0.25+0.5+1+2+4+8+16+30+30+30), plus up to DisconnectGrace per attempt for a
// child that starts and never calls back — so a crash-looping child is parked
// after roughly two to three minutes. Recalibrate against a measurement if a
// legitimate child ever reaches it.
const DefaultGiveUpAfter = 10

// Exit is how a child process ended. All fields are best-effort: a process
// killed by a signal has no exit code, and a launcher that never produced a
// process reports only Error.
type Exit struct {
	At       time.Time
	ExitCode *int
	Signal   string
	Error    string
}

func (e Exit) String() string {
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

// Snapshot is one child's supervision state at a moment, copied out so the
// caller holds no supervisor state.
type Snapshot struct {
	Desired        DesiredState
	Phase          Phase
	Generation     uint64
	Running        bool
	Connected      bool
	RestartAttempt int
	StartedAt      time.Time
	ConnectedAt    time.Time
	NextRestartAt  time.Time
	ParkedAt       time.Time
	LastExit       *Exit
}

// Park is a parking as something outside the supervisor remembers it.
//
// The supervisor's own memory ends with its process, so a consumer that must
// keep a child parked across restarts persists this and hands it back with
// AdoptParked. ParkedAt is the moment the give-up happened, not the moment it
// was restored: a park has one timestamp for as long as it lasts.
type Park struct {
	ParkedAt       time.Time
	RestartAttempt int
	LastExit       *Exit
}

// Process is a started child. Wait blocks until it ends; Kill asks it to die and
// must be safe to call after it already has.
type Process interface {
	Wait() Exit
	Kill() error
}

// StartRequest carries what the supervisor knows at launch time. Log is an
// append-only writer for the child's stdout/stderr — nil when log capture is
// off or the log file could not be opened, in which case the launcher should
// discard the child's output as before. The writer is closed once StartFunc
// returns, so the launcher must hand it to the child rather than keep it.
type StartRequest struct {
	Name       string
	Generation uint64
	Log        io.Writer
}

// StartFunc launches one child. It is called on every start, including
// restarts, so it must be safe to call repeatedly.
type StartFunc func(StartRequest) (Process, error)

// Timer is the subset of time.Timer the supervisor uses, so tests can drive
// time.
type Timer interface {
	Stop() bool
}

// Clock is the supervisor's view of time.
type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(delay time.Duration, fn func()) Timer {
	return time.AfterFunc(delay, fn)
}

// Options configures one supervisor. The zero value is usable: a real clock, the
// default give-up tripwire, no log capture, and no callbacks.
type Options struct {
	Clock Clock
	// LogDir holds one append-only <name>.log per child, following the
	// pty-worker pattern. Empty disables log capture.
	LogDir string
	// GiveUpAfter overrides DefaultGiveUpAfter. A negative value never parks.
	GiveUpAfter int
	// OnChange reports that one child's supervision state moved. The name is
	// required: the daemon turns it into a fact, and a fact needs the entity
	// it is about. Called without the supervisor lock held.
	OnChange func(name string)
	// OnGiveUp reports a child crossing into PhaseParked, once per parking.
	// Called without the supervisor lock held.
	OnGiveUp func(name string, snapshot Snapshot)
	// Logf receives supervisor-level diagnostics (a log file that would not
	// open, a parked child). Optional.
	Logf func(format string, args ...any)
}

type child struct {
	name    string
	start   StartFunc
	desired DesiredState
	phase   Phase

	generation     uint64
	process        Process
	restartAttempt int
	startedAt      time.Time
	connectedAt    time.Time
	nextRestartAt  time.Time
	parkedAt       time.Time
	lastExit       *Exit

	restartTimer    Timer
	disconnectTimer Timer
	stabilityTimer  Timer
}

// Supervisor supervises a set of named children.
type Supervisor struct {
	mu          sync.Mutex
	children    map[string]*child
	clock       Clock
	logDir      string
	giveUpAfter int
	onChange    func(string)
	onGiveUp    func(string, Snapshot)
	logf        func(string, ...any)
	shutdown    bool
}

func New(opts Options) *Supervisor {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	giveUpAfter := opts.GiveUpAfter
	if giveUpAfter == 0 {
		giveUpAfter = DefaultGiveUpAfter
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Supervisor{
		children:    make(map[string]*child),
		clock:       clock,
		logDir:      opts.LogDir,
		giveUpAfter: giveUpAfter,
		onChange:    opts.OnChange,
		onGiveUp:    opts.OnGiveUp,
		logf:        logf,
	}
}

// ErrParked says the child is one the supervisor has given up restarting, and
// the caller asked not to revive it.
var ErrParked = errors.New("supervise: child is parked")

// Ensure declares that a named child should be running, launching it if nothing
// is running or scheduled for it. Calling it again replaces the StartFunc used
// by the next start and is otherwise a no-op for a live child — so it is also
// how a parked child is revived, which resets its restart attempts.
func (s *Supervisor) Ensure(name string, start StartFunc) error {
	return s.ensure(name, start, true)
}

// EnsureUnlessParked is Ensure for a caller that runs per unit of traffic rather
// than per deliberate act: it starts the child, but reports ErrParked instead of
// reviving one the supervisor has given up on.
//
// Reviving has to stay deliberate. Ensure resets the restart budget, so a caller
// on a hot path calling it makes the give-up tripwire unreachable — the child
// crash-loops for as long as traffic keeps arriving, and every parking on the
// way is announced again.
func (s *Supervisor) EnsureUnlessParked(name string, start StartFunc) error {
	return s.ensure(name, start, false)
}

// AdoptParked begins supervising a child that is already parked, without ever
// launching it.
//
// It is the restore half of Park: a consumer that persisted a give-up hands it
// back here, and the child lands exactly where the give-up left it — nothing
// running, nothing scheduled, EnsureUnlessParked refused, Ensure the only way
// out. Nothing is announced, because nothing moved: the child was parked before
// this supervisor existed and it is parked now.
//
// A name that is already supervised is refused. Adopting onto a live child would
// overwrite what this process knows with a record an earlier one wrote.
func (s *Supervisor) AdoptParked(name string, park Park) error {
	if err := validateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return fmt.Errorf("supervise: supervisor is shut down, cannot adopt %q", name)
	}
	if s.children[name] != nil {
		return fmt.Errorf("supervise: child %q is already supervised, so a persisted park cannot be adopted onto it", name)
	}
	s.children[name] = &child{
		name: name,
		// Still wanted, still not running: parking is the supervisor giving up,
		// not the consumer changing its mind.
		desired:        DesiredRunning,
		phase:          PhaseParked,
		parkedAt:       park.ParkedAt,
		restartAttempt: park.RestartAttempt,
		lastExit:       copyExit(park.LastExit),
	}
	return nil
}

func (s *Supervisor) ensure(name string, start StartFunc, revive bool) error {
	if err := validateName(name); err != nil {
		return err
	}
	if start == nil {
		return fmt.Errorf("supervise: child %q needs a start function", name)
	}
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return fmt.Errorf("supervise: supervisor is shut down, cannot start %q", name)
	}
	c := s.children[name]
	if c == nil {
		c = &child{name: name, start: start, desired: DesiredRunning, phase: PhaseStarting}
		s.children[name] = c
	} else {
		c.start = start
		c.desired = DesiredRunning
		if c.process != nil || c.restartTimer != nil {
			s.mu.Unlock()
			return nil
		}
		if c.phase == PhaseParked {
			if !revive {
				s.mu.Unlock()
				return ErrParked
			}
			c.restartAttempt = 0
		}
	}
	err := s.spawnLocked(c)
	parked, snapshot := c.phase == PhaseParked, snapshotOf(c)
	s.mu.Unlock()
	if parked {
		s.reportGiveUp(name, snapshot)
	}
	s.notify(name)
	return err
}

// Stop kills a child and stops supervising it until the next Ensure.
//
// It also ends the crash-loop episode: the restart budget counts restarts the
// supervisor chose, and a deliberate stop is not one of them. Without the reset,
// stop-then-start — which is what a "restart" verb is — would revive a parked
// child with no budget left, and the next single exit would park it again. That
// makes the way back from parked a door that opens once.
func (s *Supervisor) Stop(name string) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil {
		s.mu.Unlock()
		return
	}
	c.desired = DesiredStopped
	c.phase = PhaseStopped
	c.restartAttempt = 0
	c.generation++
	c.connectedAt = time.Time{}
	c.nextRestartAt = time.Time{}
	c.parkedAt = time.Time{}
	stopTimer(&c.restartTimer)
	stopTimer(&c.disconnectTimer)
	stopTimer(&c.stabilityTimer)
	process := c.process
	c.process = nil
	s.mu.Unlock()
	if process != nil {
		_ = process.Kill()
	}
	s.notify(name)
}

// Shutdown stops every child and refuses further Ensure calls.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return
	}
	s.shutdown = true
	names := make([]string, 0, len(s.children))
	for name := range s.children {
		names = append(names, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		s.Stop(name)
	}
}

// NoteConnected reports that a child dialed back. It accepts untracked
// test/manual connections, but a supervised child must present the exact
// generation injected into its process.
func (s *Supervisor) NoteConnected(name string, generation uint64) bool {
	s.mu.Lock()
	c := s.children[name]
	if c == nil {
		s.mu.Unlock()
		return true
	}
	if generation == 0 || generation != c.generation || c.desired != DesiredRunning || c.process == nil {
		s.mu.Unlock()
		return false
	}
	c.phase = PhaseConnected
	c.connectedAt = s.clock.Now()
	c.nextRestartAt = time.Time{}
	stopTimer(&c.disconnectTimer)
	stopTimer(&c.stabilityTimer)
	capturedGeneration := c.generation
	c.stabilityTimer = s.clock.AfterFunc(StableConnection, func() {
		s.markStable(name, capturedGeneration)
	})
	s.mu.Unlock()
	s.notify(name)
	return true
}

// NoteDisconnected reports that a child's connection dropped, starting the
// grace period in which it may reconnect before being killed.
func (s *Supervisor) NoteDisconnected(name string, generation uint64) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.desired != DesiredRunning || c.process == nil {
		s.mu.Unlock()
		return
	}
	c.phase = PhaseStarting
	c.connectedAt = time.Time{}
	stopTimer(&c.stabilityTimer)
	stopTimer(&c.disconnectTimer)
	capturedProcess := c.process
	capturedGeneration := c.generation
	c.disconnectTimer = s.clock.AfterFunc(DisconnectGrace, func() {
		s.disconnectExpired(name, capturedGeneration, capturedProcess)
	})
	s.mu.Unlock()
	s.notify(name)
}

// Snapshot copies out one child's state.
func (s *Supervisor) Snapshot(name string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.children[name]
	if c == nil {
		return Snapshot{}, false
	}
	return snapshotOf(c), true
}

func snapshotOf(c *child) Snapshot {
	snapshot := Snapshot{
		Desired:        c.desired,
		Phase:          c.phase,
		Generation:     c.generation,
		Running:        c.process != nil,
		Connected:      c.phase == PhaseConnected,
		RestartAttempt: c.restartAttempt,
		StartedAt:      c.startedAt,
		ConnectedAt:    c.connectedAt,
		NextRestartAt:  c.nextRestartAt,
		ParkedAt:       c.parkedAt,
	}
	snapshot.LastExit = copyExit(c.lastExit)
	return snapshot
}

// copyExit deep-copies an exit so neither side of a hand-off shares the other's
// memory — the supervisor keeps mutating its copy, and ExitCode is a pointer.
func copyExit(from *Exit) *Exit {
	if from == nil {
		return nil
	}
	exit := *from
	if from.ExitCode != nil {
		code := *from.ExitCode
		exit.ExitCode = &code
	}
	return &exit
}

func (s *Supervisor) spawnLocked(c *child) error {
	c.generation++
	c.phase = PhaseStarting
	c.nextRestartAt = time.Time{}
	c.parkedAt = time.Time{}
	stopTimer(&c.restartTimer)
	generation := c.generation
	process, err := s.startChild(c, generation)
	if err != nil {
		exit := Exit{At: s.clock.Now(), Error: err.Error()}
		c.lastExit = &exit
		s.scheduleRestartLocked(c)
		return err
	}
	c.process = process
	c.startedAt = s.clock.Now()
	stopTimer(&c.disconnectTimer)
	name := c.name
	capturedProcess := process
	c.disconnectTimer = s.clock.AfterFunc(DisconnectGrace, func() {
		s.disconnectExpired(name, generation, capturedProcess)
	})
	go func(name string, generation uint64, process Process) {
		exit := process.Wait()
		s.processExited(name, generation, process, exit)
	}(name, generation, process)
	return nil
}

// startChild opens this start's log file, launches the child, and closes the
// supervisor's copy of the file descriptor: the child keeps its own, so nothing
// here has to outlive the launch.
func (s *Supervisor) startChild(c *child, generation uint64) (Process, error) {
	req := StartRequest{Name: c.name, Generation: generation}
	if file := s.openLog(c.name, generation); file != nil {
		defer func() { _ = file.Close() }()
		req.Log = file
	}
	return c.start(req)
}

func (s *Supervisor) openLog(name string, generation uint64) *os.File {
	if s.logDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.logDir, 0o700); err != nil {
		s.logf("supervise: log dir %s: %v", s.logDir, err)
		return nil
	}
	path := filepath.Join(s.logDir, name+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.logf("supervise: log file %s: %v", path, err)
		return nil
	}
	fmt.Fprintf(file, "\n=== %s starting %s generation %d ===\n", s.clock.Now().Format(time.RFC3339), name, generation)
	return file
}

func (s *Supervisor) processExited(name string, generation uint64, process Process, exit Exit) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.process != process {
		s.mu.Unlock()
		return
	}
	c.process = nil
	c.connectedAt = time.Time{}
	stopTimer(&c.disconnectTimer)
	stopTimer(&c.stabilityTimer)
	exit.At = s.clock.Now()
	c.lastExit = &exit
	if c.desired == DesiredRunning && !s.shutdown {
		s.scheduleRestartLocked(c)
	} else {
		c.phase = PhaseStopped
		c.nextRestartAt = time.Time{}
	}
	parked, snapshot := c.phase == PhaseParked, snapshotOf(c)
	s.mu.Unlock()
	if parked {
		s.reportGiveUp(name, snapshot)
	}
	s.notify(name)
}

// scheduleRestartLocked queues the next restart, or parks the child when it has
// burned through its restarts without ever reaching a stability window.
func (s *Supervisor) scheduleRestartLocked(c *child) {
	stopTimer(&c.restartTimer)
	if s.giveUpAfter > 0 && c.restartAttempt >= s.giveUpAfter {
		c.phase = PhaseParked
		c.nextRestartAt = time.Time{}
		c.parkedAt = s.clock.Now()
		detail := "no exit recorded"
		if c.lastExit != nil {
			detail = c.lastExit.String()
		}
		s.logf("supervise: giving up on %q after %d restarts with no stable connection; last exit: %s", c.name, c.restartAttempt, detail)
		return
	}
	c.restartAttempt++
	index := c.restartAttempt - 1
	if index >= len(RestartBackoff) {
		index = len(RestartBackoff) - 1
	}
	delay := RestartBackoff[index]
	c.phase = PhaseBackoff
	c.nextRestartAt = s.clock.Now().Add(delay)
	name := c.name
	capturedGeneration := c.generation
	c.restartTimer = s.clock.AfterFunc(delay, func() {
		s.restart(name, capturedGeneration)
	})
}

func (s *Supervisor) restart(name string, generation uint64) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.desired != DesiredRunning || s.shutdown {
		s.mu.Unlock()
		return
	}
	c.restartTimer = nil
	_ = s.spawnLocked(c)
	parked, snapshot := c.phase == PhaseParked, snapshotOf(c)
	s.mu.Unlock()
	if parked {
		s.reportGiveUp(name, snapshot)
	}
	s.notify(name)
}

func (s *Supervisor) markStable(name string, generation uint64) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.phase != PhaseConnected {
		s.mu.Unlock()
		return
	}
	c.restartAttempt = 0
	c.stabilityTimer = nil
	s.mu.Unlock()
	s.notify(name)
}

func (s *Supervisor) disconnectExpired(name string, generation uint64, process Process) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.process != process || c.phase == PhaseConnected || c.desired != DesiredRunning {
		s.mu.Unlock()
		return
	}
	c.disconnectTimer = nil
	s.mu.Unlock()
	_ = process.Kill()
}

func (s *Supervisor) notify(name string) {
	if s.onChange != nil {
		s.onChange(name)
	}
}

func (s *Supervisor) reportGiveUp(name string, snapshot Snapshot) {
	if s.onGiveUp != nil {
		s.onGiveUp(name, snapshot)
	}
}

// validateName keeps a child name usable both as a map key and as a log file
// name, so a name can never write outside LogDir.
func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("supervise: child name is required")
	}
	if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsRune(name, os.PathSeparator) {
		return fmt.Errorf("supervise: child name %q must be a plain file name", name)
	}
	return nil
}

func stopTimer(timer *Timer) {
	if *timer != nil {
		(*timer).Stop()
		*timer = nil
	}
}
