package pty

import (
	"context"
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
	// Keep a deeper PTY history so session restore/re-attach can replay
	// substantially more terminal output.
	DefaultScrollbackSize = 8 * 1024 * 1024
	defaultKillTimeout    = 10 * time.Second
	shellEnvTimeout       = 2 * time.Second
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

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
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
	ScreenSnapshot      []byte
	ScreenCols          uint16
	ScreenRows          uint16
	ScreenCursorX       uint16
	ScreenCursorY       uint16
	ScreenCursorVisible bool
	ScreenSnapshotFresh bool
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
	attnPath := ""
	if agent != "shell" {
		attnPath = resolveAttnPath()
	}

	m.mu.Lock()
	if _, exists := m.sessions[opts.ID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("session %s already exists", opts.ID)
	}
	m.mu.Unlock()

	loginShell := getUserLoginShell()
	shellCandidates := preferredShellCandidates(loginShell)
	cmdEnv := buildSpawnEnv(loginShell, opts, agent, attnPath, m.logf)

	var (
		cmd       *exec.Cmd
		ptmx      *os.File
		lastErr   error
		usedShell string
	)
	for i, shellPath := range shellCandidates {
		cmd = buildSpawnCommand(opts, agent, shellPath, attnPath)
		cmd.Dir = opts.CWD
		if shouldSetpgidForPTY() {
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
		cmd.Env = cmdEnv

		ptmx, lastErr = creackpty.StartWithSize(cmd, &creackpty.Winsize{
			Cols: opts.Cols,
			Rows: opts.Rows,
		})
		if lastErr == nil {
			usedShell = shellPath
			break
		}

		if i < len(shellCandidates)-1 && shouldFallbackShell(lastErr) {
			m.logf("pty spawn: failed with shell=%s id=%s err=%v; trying fallback shell", shellPath, opts.ID, lastErr)
			continue
		}
		return fmt.Errorf("spawn session %s: %w", opts.ID, lastErr)
	}
	if usedShell != "" && usedShell != loginShell {
		m.logf("pty spawn: using fallback shell=%s (preferred=%s) id=%s", usedShell, loginShell, opts.ID)
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
	if agent == "codex" {
		session.screen = newVirtualScreen(opts.Cols, opts.Rows)
	}

	m.mu.Lock()
	m.sessions[opts.ID] = session
	onExit := m.onExit
	onState := m.onState
	m.mu.Unlock()

	if agent == "codex" || agent == "copilot" {
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
	case "copilot":
		return "copilot"
	case "shell":
		return "shell"
	default:
		return "codex"
	}
}

func buildSpawnCommand(opts SpawnOptions, agent, shellPath, attnPath string) *exec.Cmd {
	if agent == "shell" {
		return exec.Command(shellPath, "-l")
	}

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
	return exec.Command(shellPath, "-l", "-c", cmdline)
}

func buildSpawnEnv(loginShell string, opts SpawnOptions, agent, wrapperPath string, logf LogFunc) []string {
	env := os.Environ()

	if loginShell != "" {
		if shellEnv, err := readLoginShellEnv(loginShell); err == nil {
			env = mergeEnvironment(env, shellEnv)
		} else if logf != nil {
			logf("pty spawn: failed to capture login shell env from %s: %v", loginShell, err)
		}
	}

	// Strip CLAUDECODE after all merges so spawned sessions don't think
	// they're nested.  This var leaks into the daemon env when started
	// from a Claude Code session, and readLoginShellEnv re-captures it
	// because the login shell inherits the current process environment.
	env = filterEnvKeys(env, "CLAUDECODE")

	env = mergeEnvironment(env, []string{"TERM=xterm-256color"})
	if agent != "shell" {
		env = mergeEnvironment(env, []string{
			"ATTN_INSIDE_APP=1",
			"ATTN_DAEMON_MANAGED=1",
			"ATTN_SESSION_ID=" + opts.ID,
			"ATTN_AGENT=" + agent,
		})
		if wrapperPath != "" {
			env = mergeEnvironment(env, []string{"ATTN_WRAPPER_PATH=" + wrapperPath})
		}
		if opts.ClaudeExecutable != "" {
			env = mergeEnvironment(env, []string{"ATTN_CLAUDE_EXECUTABLE=" + opts.ClaudeExecutable})
		}
		if opts.CodexExecutable != "" {
			env = mergeEnvironment(env, []string{"ATTN_CODEX_EXECUTABLE=" + opts.CodexExecutable})
		}
		if opts.CopilotExecutable != "" {
			env = mergeEnvironment(env, []string{"ATTN_COPILOT_EXECUTABLE=" + opts.CopilotExecutable})
		}
	}
	return env
}

func readLoginShellEnv(shellPath string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shellPath, "-l", "-i", "-c", "env -0")
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout after %s", shellEnvTimeout)
		}
		return nil, err
	}
	return parseNullSeparatedEnv(output), nil
}

func parseNullSeparatedEnv(output []byte) []string {
	if len(output) == 0 {
		return nil
	}
	parts := strings.Split(string(output), "\x00")
	env := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		env = append(env, part)
	}
	return env
}

func mergeEnvironment(base, overlay []string) []string {
	if len(overlay) == 0 {
		return append([]string(nil), base...)
	}
	merged := make([]string, 0, len(base)+len(overlay))
	index := make(map[string]int, len(base)+len(overlay))
	add := func(entry string) {
		key := entry
		if idx := strings.Index(entry, "="); idx >= 0 {
			key = entry[:idx]
		}
		if pos, ok := index[key]; ok {
			merged[pos] = entry
			return
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range overlay {
		add(entry)
	}
	return merged
}

func filterEnvKeys(env []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key := entry
		if idx := strings.Index(entry, "="); idx >= 0 {
			key = entry[:idx]
		}
		if _, ok := drop[key]; ok {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func preferredShellCandidates(primary string) []string {
	candidates := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(shell string) {
		shell = strings.TrimSpace(shell)
		if shell == "" {
			return
		}
		if _, ok := seen[shell]; ok {
			return
		}
		seen[shell] = struct{}{}
		candidates = append(candidates, shell)
	}

	add(primary)
	if runtime.GOOS == "darwin" {
		add("/bin/zsh")
		add("/bin/bash")
	} else {
		add("/bin/bash")
	}
	add("/bin/sh")
	return candidates
}

func shouldFallbackShell(err error) bool {
	return errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, exec.ErrNotFound)
}

func shouldSetpgidForPTY() bool {
	// On macOS, creack/pty (forkpty) already creates a new session/process group.
	// Requesting Setpgid via os/exec conflicts and fails with EPERM.
	return runtime.GOOS != "darwin"
}

func resolveAttnPath() string {
	candidates := make([]string, 0, 4)
	if wrapperPath := strings.TrimSpace(os.Getenv("ATTN_WRAPPER_PATH")); wrapperPath != "" {
		candidates = append(candidates, wrapperPath)
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		candidates = append(candidates, exe)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "attn"))
	}
	if path, err := exec.LookPath("attn"); err == nil && path != "" {
		candidates = append(candidates, path)
	}
	if resolved, ok := firstExecutablePath(candidates); ok {
		return resolved
	}
	return "attn"
}

func firstExecutablePath(candidates []string) (string, bool) {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		// On unix, require execute bits. Windows doesn't expose unix mode bits.
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, true
	}
	return "", false
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
