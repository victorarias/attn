// Package hostsession owns the headless agent hosts the daemon runs beside its
// PTY sessions.
//
// A host is one child process per attn session that speaks a conversation
// rather than a terminal: envelopes out on fd 3, verbs in on stdin, its own
// stdout and stderr to a log file. The daemon never parses a host's render
// bodies; it stamps them with the session and forwards them.
//
// The lifecycle rule this package exists to enforce: a host is spawned as a
// PROCESS-GROUP LEADER and the group is killed, not the process. Hard-killing
// the host alone orphans the tool subprocesses it started — reproduced three
// times against pi 0.83.0 on 2026-08-04 — so every teardown path here ends in a
// group sweep, including the paths where the host exits on its own.
package hostsession

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

// Event is one envelope a host emitted, already split into the parts the
// daemon acts on (session, seq, kind) and the part it only forwards (body).
type Event struct {
	SessionID string
	Seq       int
	Kind      string
	Body      map[string]interface{}
	// LifecycleID matches the run this host was spawned for, for the same
	// reason ExitInfo carries one: a superseded host that is still draining
	// must not be able to describe the session that replaced it.
	LifecycleID string
}

// ExitInfo reports a host that is gone, after its group has been swept.
type ExitInfo struct {
	SessionID string
	ExitCode  int
	Signal    string
	// LifecycleID matches the run this host was spawned for, so a late exit
	// from a superseded host cannot retire the session that replaced it.
	LifecycleID string
}

type SpawnOptions struct {
	SessionID   string
	LifecycleID string
	Command     []string
	Env         []string
	CWD         string
	// LogPath collects the host's own stdout and stderr. pi loads the user's
	// extensions, and any of them may print; keeping that away from the
	// envelope fd is why the envelopes have their own.
	LogPath string
	// RegistryPath is where the host's durable record lives (see registry.go).
	// Written right after spawn, removed once the host is fully gone, and read
	// by `attn profile clean` to reap hosts a dead daemon left behind. Empty
	// means no record is kept.
	RegistryPath string
}

// terminationGrace is how long a host gets to tear down cooperatively after
// SIGTERM before the group is killed outright.
//
// Receipt (2026-08-05, this machine, compiled host, pi 0.83.0): SIGTERM to
// process exit measured 3 ms across four idle hosts, and 3 ms for a host
// mid-run with a live `sleep 47` bash tool — whose subprocess was gone by the
// time the host had exited, because pi's own dispose tears its tools down.
// 3 s is a tripwire a thousand times past that: a host that reaches it is
// wedged, not busy, and the log says so.
const terminationGrace = 3 * time.Second

// envelopeDrainGrace bounds how long a dead host's exit waits for its envelope
// stream to finish.
//
// The exit must not be announced before the last envelope is delivered: the
// daemon turns it into `session_exited`, and a client that sees the session end
// before the run that was closing it would draw a run stuck open on a dead
// session. Draining costs only the time to consume what the pipe already holds
// — a pipe buffer is 64 KB — so this is microseconds of work.
//
// It is bounded because EOF is not guaranteed. pi spawns tool subprocesses that
// inherit the host's fds and lead their own process groups, so one that outlives
// the host and escapes the group sweep would hold the write end open forever and
// the exit would never be announced. 2 s is many orders of magnitude past the
// real drain; reaching it means something still holds the fd, which the log
// names before the read end is closed out from under it.
const envelopeDrainGrace = 2 * time.Second

type host struct {
	sessionID    string
	lifecycleID  string
	cmd          *exec.Cmd
	pgid         int
	registryPath string
	stdin        *os.File
	envelopes    *os.File
	logFile      *os.File
	// reaped closes as soon as the process is gone; exited closes once the
	// host is fully finished — drained, deregistered, and about to be
	// announced. Kill escalates on the first and returns on the second, so a
	// caller that gets a nil error can spawn the same session id again.
	reaped   chan struct{}
	exited   chan struct{}
	drained  chan struct{}
	killOnce sync.Once
}

type Manager struct {
	logf    func(format string, args ...interface{})
	onEvent func(Event)
	onExit  func(ExitInfo)

	mu    sync.Mutex
	hosts map[string]*host
}

func New(logf func(format string, args ...interface{}), onEvent func(Event), onExit func(ExitInfo)) *Manager {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	if onEvent == nil {
		onEvent = func(Event) {}
	}
	if onExit == nil {
		onExit = func(ExitInfo) {}
	}
	return &Manager{logf: logf, onEvent: onEvent, onExit: onExit, hosts: make(map[string]*host)}
}

var ErrNotFound = errors.New("host session not found")

func (m *Manager) Spawn(opts SpawnOptions) error {
	if opts.SessionID == "" {
		return errors.New("host spawn needs a session id")
	}
	if len(opts.Command) == 0 {
		return errors.New("host spawn needs a command")
	}
	m.mu.Lock()
	if _, exists := m.hosts[opts.SessionID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("host for session %s is already running", opts.SessionID)
	}
	m.mu.Unlock()

	logFile, err := openLog(opts.LogPath)
	if err != nil {
		return err
	}

	envelopeR, envelopeW, err := os.Pipe()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("create envelope pipe: %w", err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		logFile.Close()
		envelopeR.Close()
		envelopeW.Close()
		return fmt.Errorf("create verb pipe: %w", err)
	}

	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	cmd.Dir = opts.CWD
	cmd.Env = opts.Env
	cmd.Stdin = stdinR
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// ExtraFiles[0] is the child's fd 3 — the envelope stream.
	cmd.ExtraFiles = []*os.File{envelopeW}
	// The whole point: the child leads its own process group, so its tool
	// subprocesses are reachable as one unit no matter how it dies.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		envelopeR.Close()
		envelopeW.Close()
		stdinR.Close()
		stdinW.Close()
		return fmt.Errorf("start host %v: %w", opts.Command, err)
	}
	// The child owns its copies now; holding ours open would keep the envelope
	// reader from ever seeing EOF.
	envelopeW.Close()
	stdinR.Close()

	h := &host{
		sessionID:    opts.SessionID,
		lifecycleID:  opts.LifecycleID,
		cmd:          cmd,
		pgid:         cmd.Process.Pid,
		registryPath: opts.RegistryPath,
		stdin:        stdinW,
		envelopes:    envelopeR,
		logFile:      logFile,
		reaped:       make(chan struct{}),
		exited:       make(chan struct{}),
		drained:      make(chan struct{}),
	}
	// The durable record must exist before anything can observe the host: a
	// daemon that dies right after this line has already left the trace `attn
	// profile clean` reaps by. A failed write is logged, not fatal — the host
	// is healthy, only the crash-recovery net has a hole the log names.
	if opts.RegistryPath != "" {
		entry := procreap.NewEntry(opts.SessionID, cmd.Process.Pid, cmd.Process.Pid, opts.Command)
		if err := procreap.WriteEntry(opts.RegistryPath, entry); err != nil {
			m.logf("host session %s: recording host registry entry failed: %v", opts.SessionID, err)
		}
	}
	m.mu.Lock()
	m.hosts[opts.SessionID] = h
	m.mu.Unlock()

	m.logf("host session %s started pid=%d pgid=%d cmd=%v", opts.SessionID, h.pgid, h.pgid, opts.Command)
	go m.readEnvelopes(h, envelopeR)
	go m.monitor(h)
	return nil
}

func openLog(path string) (*os.File, error) {
	if path == "" {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create host log dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open host log %s: %w", path, err)
	}
	return file, nil
}

// maxEnvelopeBytes bounds one line off the envelope fd.
//
// Receipt: the largest body this protocol version produces is a `message_end`
// carrying one whole assistant message, so the bound is the model's per-response
// output cap. The largest `maxTokens` in pi 0.83.0's model catalog is 2,000,000
// (vercel-ai-gateway, xai/grok-4.20-multi-agent); at 4 bytes per token — the
// worst case for UTF-8 — that is 8 MB of text. 64 MB is 8x past the largest
// message any model in the catalog can emit, and the scanner starts at 64 KB
// and grows only on demand, so the ceiling costs nothing until something
// abnormal reaches for it. A line that exceeds it is a protocol violation, and
// the host is torn down naming the limit rather than silently truncating the
// conversation.
const maxEnvelopeBytes = 64 << 20

func (m *Manager) readEnvelopes(h *host, r *os.File) {
	defer close(h.drained)
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEnvelopeBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var envelope struct {
			SessionID string                 `json:"session_id"`
			Seq       int                    `json:"seq"`
			Kind      string                 `json:"kind"`
			Body      map[string]interface{} `json:"body"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			m.logf("host session %s: undecodable envelope (%d bytes): %v", h.sessionID, len(line), err)
			continue
		}
		if envelope.Kind == "" {
			m.logf("host session %s: envelope seq=%d has no kind; dropped", h.sessionID, envelope.Seq)
			continue
		}
		if envelope.Body == nil {
			envelope.Body = map[string]interface{}{}
		}
		// The host stamps its own session id; the daemon trusts the process it
		// spawned, not the field, so a mismatch is a bug worth naming.
		if envelope.SessionID != "" && envelope.SessionID != h.sessionID {
			m.logf("host session %s: envelope claims session %s; using the spawned one", h.sessionID, envelope.SessionID)
		}
		m.onEvent(Event{
			SessionID:   h.sessionID,
			Seq:         envelope.Seq,
			Kind:        envelope.Kind,
			Body:        envelope.Body,
			LifecycleID: h.lifecycleID,
		})
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			m.logf("host session %s: envelope exceeded maxEnvelopeBytes=%d; tearing the host down", h.sessionID, maxEnvelopeBytes)
			go m.Kill(h.sessionID)
			return
		}
		m.logf("host session %s: envelope stream failed: %v", h.sessionID, err)
	}
}

// monitor waits for the host to die and then sweeps its process group, on
// EVERY exit path — cooperative shutdown, crash, kill. This is the sweep that
// catches the receipted bug: pi orphans its running tool subprocesses when the
// host goes away without cleaning up.
//
// Sweeping after the reap is safe where it matters. A process group's id is
// held until its LAST member leaves, and a pid cannot be reallocated while a
// group carries it — so whenever there is actually an orphan to kill, this
// pgid is still unambiguously ours. When the group is already empty the signal
// is a harmless ESRCH.
func (m *Manager) monitor(h *host) {
	waitErr := h.cmd.Wait()
	if err := syscall.Kill(-h.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		m.logf("host session %s: sweeping process group %d failed: %v", h.sessionID, h.pgid, err)
	}
	close(h.reaped)
	h.stdin.Close()
	h.logFile.Close()

	// Everything the host said before it died reaches the daemon before the
	// death does. See envelopeDrainGrace for why this is bounded.
	select {
	case <-h.drained:
	case <-time.After(envelopeDrainGrace):
		m.logf("host session %s: envelope stream still open %s after exit; something inherited fd 3", h.sessionID, envelopeDrainGrace)
		h.envelopes.Close()
		<-h.drained
	}

	exitCode, signal := exitStatus(h.cmd, waitErr)
	m.mu.Lock()
	if current, ok := m.hosts[h.sessionID]; ok && current == h {
		delete(m.hosts, h.sessionID)
	}
	m.mu.Unlock()

	// The process and its group are gone; retire the durable record so the
	// registry only ever names hosts that may still be running.
	if h.registryPath != "" {
		if err := procreap.RemoveEntry(h.registryPath); err != nil {
			m.logf("host session %s: removing host registry entry failed: %v", h.sessionID, err)
		}
	}

	close(h.exited)

	m.logf("host session %s exited code=%d signal=%q pgid=%d", h.sessionID, exitCode, signal, h.pgid)
	m.onExit(ExitInfo{SessionID: h.sessionID, ExitCode: exitCode, Signal: signal, LifecycleID: h.lifecycleID})
}

func exitStatus(cmd *exec.Cmd, waitErr error) (int, string) {
	state := cmd.ProcessState
	if state == nil {
		if waitErr != nil {
			return -1, ""
		}
		return 0, ""
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), status.Signal().String()
	}
	return state.ExitCode(), ""
}

// Delivery is when a host should let its agent read a message.
type Delivery string

const (
	// DeliveryPrompt is the first word of a run. A host refuses it mid-run.
	DeliveryPrompt Delivery = "prompt"
	// DeliverySteer lands at the agent's next turn boundary, interrupting the
	// run in progress. This is what a doorbell uses.
	DeliverySteer Delivery = "steer"
	// DeliveryFollowUp lands only when the run would otherwise settle, so it
	// queues behind the work instead of cutting into it.
	DeliveryFollowUp Delivery = "follow_up"
)

// Deliver sends one message to a live host, to be read at `how`.
//
// The host, not this manager, decides what a steer means on a session with no
// run open: it starts one. So a caller never has to know what the agent is
// doing to reach it.
func (m *Manager) Deliver(sessionID string, how Delivery, text string) error {
	switch how {
	case DeliveryPrompt, DeliverySteer, DeliveryFollowUp:
	default:
		return fmt.Errorf("unsupported host delivery %q", how)
	}
	return m.send(sessionID, map[string]interface{}{"verb": string(how), "text": text})
}

// ToolDetail asks a host for what an expanded tool card shows.
//
// The answer does not come back here: it arrives as another envelope on the
// host's own stream, addressed by the same call id, and reaches every client
// through the ordinary forwarding path. Two clients with the same card open
// therefore cost one fetch, and neither has a request to time out.
//
// `full` asks for pi's untruncated output file rather than the clipped result
// it handed the model; it means nothing for a call that produced no such file,
// and the host answers with what it has.
func (m *Manager) ToolDetail(sessionID, callID string, full bool) error {
	if callID == "" {
		return errors.New("tool detail needs a call id")
	}
	return m.send(sessionID, map[string]interface{}{"verb": "tool_detail", "call_id": callID, "full": full})
}

// ClearQueue drops everything the agent has been sent and not yet read.
//
// The host answers with the agent's own queue state, so the strip a client is
// drawing empties on the agent's word rather than on this call returning.
func (m *Manager) ClearQueue(sessionID string) error {
	return m.send(sessionID, map[string]interface{}{"verb": "clear_queue"})
}

func (m *Manager) send(sessionID string, verb map[string]interface{}) error {
	m.mu.Lock()
	h, ok := m.hosts[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	encoded, err := json.Marshal(verb)
	if err != nil {
		return fmt.Errorf("encode host verb: %w", err)
	}
	if _, err := h.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write host verb for session %s: %w", sessionID, err)
	}
	return nil
}

func (m *Manager) Has(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.hosts[sessionID]
	return ok
}

func (m *Manager) SessionIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}
	return ids
}

// Kill tears a host down and returns once its process group is gone and the
// host is deregistered — a caller that gets a nil error can spawn the same
// session id again.
//
// The cooperative SIGTERM is the load-bearing half, not a courtesy: pi spawns
// each tool subprocess into its OWN process group (measured 2026-08-05: a bash
// `sleep 60` runs as the host's child but leads its own group), so the group
// kill below cannot reach them. What reaches them is pi's dispose, which the
// host runs on SIGTERM. The group kill is the backstop for the host itself and
// whatever stayed in its group; a host wedged past the grace window can still
// strand a detached tool child, and that is the residual this design accepts.
func (m *Manager) Kill(sessionID string) error {
	m.mu.Lock()
	h, ok := m.hosts[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}

	h.killOnce.Do(func() {
		if err := syscall.Kill(-h.pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			m.logf("host session %s: SIGTERM to group %d failed: %v", sessionID, h.pgid, err)
		}
	})

	// The grace window is about the process, so it watches reaped; the return is
	// about the teardown, so it waits out exited. Escalating on exited instead
	// would count the envelope drain against the host's time to shut down.
	select {
	case <-h.reaped:
		<-h.exited
		return nil
	case <-time.After(terminationGrace):
	}

	m.logf("host session %s did not exit within %s of SIGTERM; killing group %d", sessionID, terminationGrace, h.pgid)
	if err := syscall.Kill(-h.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill host group %d for session %s: %w", h.pgid, sessionID, err)
	}
	<-h.exited
	return nil
}

// Shutdown tears down every live host. Used when the daemon itself is going
// away, so no host outlives the daemon that owns it.
func (m *Manager) Shutdown() {
	for _, id := range m.SessionIDs() {
		if err := m.Kill(id); err != nil && !errors.Is(err, ErrNotFound) {
			m.logf("host session %s: shutdown kill failed: %v", id, err)
		}
	}
}
