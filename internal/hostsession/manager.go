// Package hostsession owns the daemon's headless agent hosts: one child
// process per attn session, envelopes out on fd 3, verbs in on stdin, its own
// stdout/stderr to a log file.
//
// Invariant: a host is spawned as a process-group leader and every teardown
// path ends in a group sweep — hard-killing the host alone orphans its tool
// subprocesses (reproduced 3x against pi 0.83.0, 2026-08-04).
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

// Event is one host envelope: the daemon acts on session/seq/kind, forwards Body.
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
	// LifecycleID matches the spawning run, so a late exit from a superseded
	// host cannot retire the session that replaced it.
	LifecycleID string
}

// SpawnOptions configures Spawn.
type SpawnOptions struct {
	SessionID   string
	LifecycleID string
	Command     []string
	Env         []string
	CWD         string
	// LogPath collects the host's own stdout/stderr, kept off the envelope fd.
	LogPath string
	// RegistryPath is where the host's durable record lives (see registry.go).
	// Written right after spawn, removed once the host is fully gone, and read
	// by `attn profile clean` to reap hosts a dead daemon left behind. Empty
	// means no record is kept.
	RegistryPath string
}

// terminationGrace bounds cooperative teardown after SIGTERM before the group
// is killed outright. Measured: 3 ms SIGTERM-to-exit (pi 0.83.0, idle and
// mid-run, 2026-08-05); 3 s is a tripwire — reaching it means wedged, not busy.
const terminationGrace = 3 * time.Second

// envelopeDrainGrace bounds waiting out a dead host's envelope stream: the exit
// must not be announced before the last envelope, but a tool child that
// inherited fd 3 can hold the pipe open forever; 2 s means something holds it.
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
	// reaped closes when the process is gone; exited once teardown is complete —
	// drained and deregistered, so a caller that gets a nil error from Kill can
	// spawn the same session id again. Kill escalates on the first, returns on
	// the second.
	reaped   chan struct{}
	exited   chan struct{}
	drained  chan struct{}
	killOnce sync.Once
}

// Manager spawns and tears down the daemon's host processes.
type Manager struct {
	logf    func(format string, args ...interface{})
	onEvent func(Event)
	onExit  func(ExitInfo)

	mu    sync.Mutex
	hosts map[string]*host
}

// New builds a Manager; nil callbacks are replaced with no-ops.
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

// ErrNotFound reports a session id with no live host.
var ErrNotFound = errors.New("host session not found")

// Spawn starts a host for the session as a process-group leader.
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
	// The child leads its own process group so teardown can sweep it as a unit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		envelopeR.Close()
		envelopeW.Close()
		stdinR.Close()
		stdinW.Close()
		return fmt.Errorf("start host %v: %w", opts.Command, err)
	}
	// Close our copies or the envelope reader never sees EOF.
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

// maxEnvelopeBytes bounds one line off the envelope fd. Receipt: the largest
// body is one assistant message; pi 0.83.0's largest catalog maxTokens is
// 2,000,000 ≈ 8 MB at 4 bytes/token, so 64 MB is 8x past it. Exceeding it is a
// protocol violation — the host is torn down naming the limit, never truncated.
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
		// The daemon trusts the process it spawned, not the stamped session id.
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

// monitor reaps the host and sweeps its process group on EVERY exit path — the
// sweep that catches pi orphaning its tool subprocesses. Post-reap is safe: a
// pgid is held until its last member leaves; an empty group is a harmless ESRCH.
func (m *Manager) monitor(h *host) {
	waitErr := h.cmd.Wait()
	if err := syscall.Kill(-h.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		m.logf("host session %s: sweeping process group %d failed: %v", h.sessionID, h.pgid, err)
	}
	close(h.reaped)
	h.stdin.Close()
	h.logFile.Close()

	// Drain before announcing the exit; see envelopeDrainGrace for the bound.
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

// Snapshot asks a host for the whole conversation as it stands.
//
// The answer comes back the same way a tool detail's does: as an envelope on the
// host's own stream, reaching every client. That is deliberate — a snapshot is
// the conversation's version of the terminal's restore dump, and the point of
// broadcasting it is that two clients attaching to one session are provably
// looking at the same transcript rather than two independently assembled ones.
func (m *Manager) Snapshot(sessionID string) error {
	return m.send(sessionID, map[string]interface{}{"verb": "snapshot"})
}

// History asks a host for the page of transcript items older than `before`.
//
// The answer travels the same broadcast path a snapshot does, addressed by the
// anchor rather than by a request — so a second window sitting at the same place
// in the conversation is served by one read, and a client holding a different
// anchor drops the page. A host that holds nothing before the anchor answers an
// empty page rather than nothing at all, which is how a client learns it has
// reached the start of what this host can serve.
func (m *Manager) History(sessionID, before string) error {
	if before == "" {
		return errors.New("history needs a before cursor")
	}
	return m.send(sessionID, map[string]interface{}{"verb": "history", "before": before})
}

// SetModel switches the model a host's agent runs on, from its next run.
//
// The host answers with a `model_changed` envelope carrying the model actually
// in force — including when the switch was refused — so nothing here has to
// guess whether it landed, and every client sees the same answer.
func (m *Manager) SetModel(sessionID, model string) error {
	if model == "" {
		return errors.New("set model needs a model")
	}
	return m.send(sessionID, map[string]interface{}{"verb": "set_model", "model": model})
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

// Has reports whether a live host exists for the session.
func (m *Manager) Has(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.hosts[sessionID]
	return ok
}

// SessionIDs lists the sessions with live hosts.
func (m *Manager) SessionIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}
	return ids
}

// Kill tears a host down; nil error means the group is gone and the session id
// can be respawned. The SIGTERM is load-bearing: pi's tool subprocesses lead
// their OWN process groups (measured 2026-08-05), so only pi's dispose on
// SIGTERM reaches them — the group kill is the backstop, and a host wedged past
// the grace can still strand a detached tool child (accepted residual).
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

	// Escalate on reaped, return on exited — escalating on exited would count
	// the envelope drain against the host's grace.
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

// Shutdown tears down every live host so none outlives the daemon.
func (m *Manager) Shutdown() {
	for _, id := range m.SessionIDs() {
		if err := m.Kill(id); err != nil && !errors.Is(err, ErrNotFound) {
			m.logf("host session %s: shutdown kill failed: %v", id, err)
		}
	}
}
