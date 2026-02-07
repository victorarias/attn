package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"
)

const (
	DefaultScrollbackSize = 1024 * 1024
	defaultKillTimeout    = 10 * time.Second
)

var ErrSessionNotFound = errors.New("session not found")

type LogFunc func(format string, args ...interface{})

type SpawnOptions struct {
	ID    string
	CWD   string
	Agent string
	Label string

	Cols uint16
	Rows uint16

	ResumeSessionID string
	ResumePicker    bool
	ForkSession     bool

	ClaudeExecutable string
	CodexExecutable  string
}

type AttachInfo struct {
	Scrollback          []byte
	ScrollbackTruncated bool
	LastSeq             uint32
	Cols                uint16
	Rows                uint16
	PID                 int
	Running             bool
	ExitCode            *int
	ExitSignal          *string
}

type ExitInfo struct {
	ID       string
	ExitCode int
	Signal   string
}

type Manager struct {
	mu             sync.RWMutex
	sessions       map[string]*Session
	scrollbackSize int
	logf           LogFunc
	onExit         func(ExitInfo)
	onState        func(sessionID, state string)
}

func NewManager(scrollbackSize int, logf LogFunc) *Manager {
	if scrollbackSize <= 0 {
		scrollbackSize = DefaultScrollbackSize
	}
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &Manager{
		sessions:       make(map[string]*Session),
		scrollbackSize: scrollbackSize,
		logf:           logf,
	}
}

func (m *Manager) SetExitHandler(handler func(ExitInfo)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onExit = handler
}

func (m *Manager) SetStateHandler(handler func(sessionID, state string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onState = handler
}

func (m *Manager) Spawn(opts SpawnOptions) error {
	if opts.ID == "" {
		return errors.New("missing session id")
	}
	if opts.CWD == "" {
		return errors.New("missing cwd")
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if opts.Rows == 0 {
		opts.Rows = 24
	}

	agent := normalizeAgent(opts.Agent)

	m.mu.Lock()
	if _, exists := m.sessions[opts.ID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("session %s already exists", opts.ID)
	}
	m.mu.Unlock()

	cmd, err := buildSpawnCommand(opts, agent)
	if err != nil {
		return err
	}

	cmd.Dir = opts.CWD
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if agent != "shell" {
		cmd.Env = append(cmd.Env,
			"ATTN_INSIDE_APP=1",
			"ATTN_DAEMON_MANAGED=1",
			"ATTN_SESSION_ID="+opts.ID,
			"ATTN_AGENT="+agent,
		)
		if opts.ClaudeExecutable != "" {
			cmd.Env = append(cmd.Env, "ATTN_CLAUDE_EXECUTABLE="+opts.ClaudeExecutable)
		}
		if opts.CodexExecutable != "" {
			cmd.Env = append(cmd.Env, "ATTN_CODEX_EXECUTABLE="+opts.CodexExecutable)
		}
	}

	ptmx, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{
		Cols: opts.Cols,
		Rows: opts.Rows,
	})
	if err != nil {
		return fmt.Errorf("spawn session %s: %w", opts.ID, err)
	}

	session := &Session{
		id:          opts.ID,
		cwd:         opts.CWD,
		agent:       agent,
		cols:        opts.Cols,
		rows:        opts.Rows,
		ptmx:        ptmx,
		cmd:         cmd,
		scrollback:  NewRingBuffer(m.scrollbackSize),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
	}

	m.mu.Lock()
	m.sessions[opts.ID] = session
	onExit := m.onExit
	onState := m.onState
	m.mu.Unlock()

	if agent == "codex" {
		session.detector = newCodexStateDetector()
		if onState != nil {
			session.onState = func(state string) {
				onState(opts.ID, state)
			}
		}
	}

	m.logf("pty spawn: id=%s agent=%s cwd=%s pid=%d", opts.ID, agent, opts.CWD, cmd.Process.Pid)
	go session.readLoop(func(exitCode int, signal string) {
		m.logf("pty exited: id=%s code=%d signal=%s", session.id, exitCode, signal)
		if onExit != nil {
			onExit(ExitInfo{ID: session.id, ExitCode: exitCode, Signal: signal})
		}
	}, m.logf)

	return nil
}

func (m *Manager) Attach(sessionID, subscriberID string, send func([]byte, uint32) bool, onDrop func(reason string)) (AttachInfo, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return AttachInfo{}, err
	}
	if send == nil {
		return AttachInfo{}, errors.New("subscriber send callback is required")
	}
	session.addSubscriber(subscriberID, send, onDrop)
	return session.info(), nil
}

func (m *Manager) Detach(sessionID, subscriberID string) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return
	}
	session.removeSubscriber(subscriberID)
}

func (m *Manager) Input(sessionID string, data []byte) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	return session.input(data)
}

func (m *Manager) Resize(sessionID string, cols, rows uint16) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	return session.resize(cols, rows)
}

func (m *Manager) Kill(sessionID string, sig syscall.Signal) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	return session.kill(sig, defaultKillTimeout)
}

func (m *Manager) Remove(sessionID string) {
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if ok {
		session.closePTY()
	}
}

func (m *Manager) Shutdown() {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.RUnlock()

	for _, session := range sessions {
		_ = session.kill(syscall.SIGTERM, defaultKillTimeout)
	}
}

func (m *Manager) SessionIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

func (m *Manager) getSession(id string) (*Session, error) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	return session, nil
}

func normalizeAgent(agent string) string {
	a := strings.TrimSpace(strings.ToLower(agent))
	switch a {
	case "", "codex":
		return "codex"
	case "claude":
		return "claude"
	case "shell":
		return "shell"
	default:
		return "codex"
	}
}

func buildSpawnCommand(opts SpawnOptions, agent string) (*exec.Cmd, error) {
	shellPath := getUserLoginShell()
	if agent == "shell" {
		return exec.Command(shellPath, "-l"), nil
	}

	attnPath := resolveAttnPath()
	args := []string{attnPath}
	if opts.Label != "" {
		args = append(args, "-s", opts.Label)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "--resume")
	}
	if opts.ForkSession {
		args = append(args, "--fork-session")
	}

	cmdline := "exec " + shellJoin(args)
	return exec.Command(shellPath, "-l", "-c", cmdline), nil
}

func resolveAttnPath() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	if home, err := os.UserHomeDir(); err == nil {
		local := filepath.Join(home, ".local", "bin", "attn")
		if _, statErr := os.Stat(local); statErr == nil {
			return local
		}
	}
	return "attn"
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func getUserLoginShell() string {
	if runtime.GOOS == "darwin" {
		if usr, err := user.Current(); err == nil {
			out, dsclErr := exec.Command("dscl", ".", "-read", "/Users/"+usr.Username, "UserShell").Output()
			if dsclErr == nil {
				for _, line := range strings.Split(string(out), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "UserShell:") {
						shell := strings.TrimSpace(strings.TrimPrefix(line, "UserShell:"))
						if shell != "" {
							return shell
						}
					}
				}
			}
		}
	}

	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}

	if usr, err := user.Current(); err == nil {
		if passwd, readErr := os.ReadFile("/etc/passwd"); readErr == nil {
			prefix := usr.Username + ":"
			for _, line := range strings.Split(string(passwd), "\n") {
				if !strings.HasPrefix(line, prefix) {
					continue
				}
				parts := strings.Split(line, ":")
				if len(parts) >= 7 && parts[6] != "" {
					return strings.TrimSpace(parts[6])
				}
			}
		}
	}

	return "/bin/bash"
}
