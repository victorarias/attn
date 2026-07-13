package pty

import (
	"context"
	"encoding/json"
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
	agentdriver "github.com/victorarias/attn/internal/agent"
)

const (
	// DefaultScrollbackSize bounds the eager per-session ring buffer (flat
	// scrollback, used for snapshot derivation and legacy flat replay). It is
	// allocated up front in every PTY worker subprocess, so it stays small.
	DefaultScrollbackSize = 1 * 1024 * 1024
	// DefaultReplayLogSize bounds the lazily-grown segmented replay log — the
	// source of terminal history restored on remount/relaunch (see
	// daemon.buildAttachReplayPayload). Unlike the ring it only costs memory
	// proportional to what a session actually emitted, so it retains enough
	// history for the daemon to select a recent self-sufficient replay tail or
	// derive a current screen snapshot. Attach transport is capped separately
	// because parsing this whole log synchronously would stall the frontend.
	// Must stay >= daemon.maxAgentRawReplayBytes or attach replay is starved.
	DefaultReplayLogSize = 8 * 1024 * 1024
	defaultKillTimeout   = 10 * time.Second
	shellEnvTimeout      = 2 * time.Second
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

	ResumeSessionID   string
	ResumePicker      bool
	YoloMode          bool
	InitialPromptFile string

	// Executable is the selected CLI path for the current agent.
	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	ExternalCommand   []string
	ExternalEnv       []string
	ExternalCWD       string
	LifecycleID       string

	// LoginShellEnv, when non-nil, is a pre-computed login shell environment
	// that replaces the ReadLoginShellEnv call.
	LoginShellEnv []string

	// Theme seeds the colors the session answers OSC 10/11/12 queries with,
	// before the child's first query — set explicitly so a spawn under a
	// non-default theme never briefly answers with built-in defaults.
	Theme TerminalTheme
}

type AttachInfo struct {
	Scrollback          []byte
	ScrollbackTruncated bool
	ReplaySegments      []ReplaySegment
	ReplayTruncated     bool
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
	ID          string
	ExitCode    int
	Signal      string
	LifecycleID string
}

type SessionInfo struct {
	SessionID string
	Agent     string
	CWD       string

	Running bool
	State   string

	Cols    uint16
	Rows    uint16
	PID     int
	LastSeq uint32

	ExitCode   *int
	ExitSignal *string
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

	agent := normalizeAgent(opts.Agent, len(opts.ExternalCommand) > 0)
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

	loginShell := GetUserLoginShell()
	shellCandidates := preferredShellCandidates(loginShell)
	cmdEnv := buildSpawnEnv(loginShell, opts, agent, attnPath, m.logf)

	var (
		cmd       *exec.Cmd
		ptmx      *os.File
		lastErr   error
		usedShell string
	)
	for i, shellPath := range shellCandidates {
		cmd = buildSpawnCommand(opts, agent, shellPath, attnPath, cmdEnv)
		cmd.Dir = opts.CWD
		if strings.TrimSpace(opts.ExternalCWD) != "" {
			cmd.Dir = opts.ExternalCWD
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
		replayLog:   NewReplayLog(DefaultReplayLogSize),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
		theme:       opts.Theme,
	}
	session.screen = newVirtualScreen(opts.Cols, opts.Rows)

	m.mu.Lock()
	m.sessions[opts.ID] = session
	onExit := m.onExit
	onState := m.onState
	m.mu.Unlock()

	detectorEnabled := true
	approvalResolverEnabled := false
	if d := agentdriver.Get(agent); d != nil {
		caps := agentdriver.EffectiveCapabilities(d)
		detectorEnabled = caps.HasStateDetector
		approvalResolverEnabled = caps.HasApprovalResolver
	}
	if detectorEnabled {
		switch agent {
		case "copilot":
			session.detector = newCopilotStateDetector()
		case "claude":
			session.detector = newClaudeWorkingDetector()
		}
	}
	if approvalResolverEnabled {
		session.approvalResolver = &approvalResolver{}
	}
	if (session.detector != nil || session.approvalResolver != nil) && onState != nil {
		session.onState = func(state string) {
			onState(opts.ID, state)
		}
	}

	m.logf("pty spawn: id=%s agent=%s cwd=%s pid=%d", opts.ID, agent, opts.CWD, cmd.Process.Pid)
	go session.readLoop(func(exitCode int, signal string) {
		m.logf("pty exited: id=%s code=%d signal=%s", session.id, exitCode, signal)
		if onExit != nil {
			onExit(ExitInfo{ID: session.id, ExitCode: exitCode, Signal: signal, LifecycleID: opts.LifecycleID})
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

// SetTheme replaces the colors sessionID answers OSC 10/11/12 queries with.
func (m *Manager) SetTheme(sessionID string, theme TerminalTheme) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	session.SetTheme(theme)
	return nil
}

// Snapshot returns the current rendered screen and sequence watermark for a
// session WITHOUT registering a subscriber or claiming geometry. It is the
// read-only seed for observers (e.g. grid tiles) that then dedup the live
// firehose against LastSeq.
func (m *Manager) Snapshot(sessionID string) (AttachInfo, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return AttachInfo{}, err
	}
	return session.screenSnapshot(), nil
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
	session.metaMu.RLock()
	prevCols, prevRows := session.cols, session.rows
	session.metaMu.RUnlock()
	pid := 0
	if session.cmd != nil && session.cmd.Process != nil {
		pid = session.cmd.Process.Pid
	}
	resizeErr := session.resize(cols, rows)
	m.logf("pty resize: id=%s prev=%dx%d new=%dx%d pid=%d err=%v", sessionID, prevCols, prevRows, cols, rows, pid, resizeErr)
	return resizeErr
}

func (m *Manager) Kill(sessionID string, sig syscall.Signal) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	return session.kill(sig, defaultKillTimeout)
}

func (m *Manager) SessionInfo(sessionID string) (SessionInfo, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}

	info := session.info()
	return SessionInfo{
		SessionID:  session.id,
		Agent:      session.agent,
		CWD:        session.cwd,
		Running:    info.Running,
		State:      session.state(),
		Cols:       info.Cols,
		Rows:       info.Rows,
		PID:        info.PID,
		LastSeq:    info.LastSeq,
		ExitCode:   info.ExitCode,
		ExitSignal: info.ExitSignal,
	}, nil
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

func normalizeAgent(agent string, external bool) string {
	a := strings.TrimSpace(strings.ToLower(agent))
	if a == "" {
		return "codex"
	}
	if a == "shell" {
		return "shell"
	}
	if agentdriver.Get(a) != nil {
		return a
	}
	if external {
		return a
	}
	return "codex"
}

func buildSpawnCommand(opts SpawnOptions, agent, shellPath, attnPath string, env []string) *exec.Cmd {
	if agent == "shell" {
		return exec.Command(shellPath, "-l")
	}
	if len(opts.ExternalCommand) > 0 {
		command := opts.ExternalCommand[0]
		if resolved, ok := resolveExternalCommandPath(command, env); ok {
			command = resolved
		}
		return exec.Command(command, opts.ExternalCommand[1:]...)
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
	if opts.YoloMode {
		args = append(args, "--yolo")
	}
	if opts.InitialPromptFile != "" {
		args = append(args, "--initial-prompt-file", opts.InitialPromptFile)
	}

	cmdline := "exec " + shellJoin(args)
	return exec.Command(shellPath, "-l", "-c", cmdline)
}

func resolveExternalCommandPath(command string, env []string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsRune(command, filepath.Separator) {
		return "", false
	}
	for _, entry := range env {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		candidates := make([]string, 0)
		for _, dir := range filepath.SplitList(strings.TrimPrefix(entry, "PATH=")) {
			if dir == "" {
				candidates = append(candidates, "."+string(filepath.Separator)+command)
				continue
			}
			candidates = append(candidates, filepath.Join(dir, command))
		}
		return firstExecutablePath(candidates)
	}
	return "", false
}

// readCachedShellEnvFromProcess reads a JSON-encoded login shell env that the
// daemon injected into this worker process's environment.
func readCachedShellEnvFromProcess() []string {
	raw := os.Getenv("ATTN_CACHED_SHELL_ENV")
	if raw == "" {
		return nil
	}
	var env []string
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil
	}
	return env
}

func buildSpawnEnv(loginShell string, opts SpawnOptions, agent, wrapperPath string, logf LogFunc) []string {
	env := os.Environ()

	shellEnv := opts.LoginShellEnv
	if len(shellEnv) == 0 {
		shellEnv = readCachedShellEnvFromProcess()
	}
	if len(shellEnv) > 0 {
		env = mergeEnvironment(env, shellEnv)
	} else if loginShell != "" {
		if captured, err := ReadLoginShellEnv(loginShell); err == nil {
			env = mergeEnvironment(env, captured)
		} else if logf != nil {
			logf("pty spawn: failed to capture login shell env from %s: %v", loginShell, err)
		}
	}
	// Don't leak worker-only configuration transport vars into spawned shells.
	env = filterEnvKeys(env, "ATTN_CACHED_SHELL_ENV", "ATTN_PTY_EXTERNAL_ENV")

	// Strip CLAUDECODE after all merges so spawned sessions don't think
	// they're nested.  This var leaks into the daemon env when started
	// from a Claude Code session, and ReadLoginShellEnv re-captures it
	// because the login shell inherits the current process environment.
	env = filterEnvKeys(env, "CLAUDECODE")

	// Interactive terminals should not inherit NO_COLOR from whichever
	// process launched attn. Agent runners commonly set it for their own
	// output, which would otherwise disable colors inside every PTY.
	env = filterEnvKeys(env, "NO_COLOR")

	// Pin TERM_PROGRAM to ghostty and scrub its version string.
	// TUIs gate OSC 8 hyperlink emission on TERM_PROGRAM; attn's terminal
	// core is ghostty and now supports OSC 8, so advertise that deterministically.
	env = filterEnvKeys(env, "TERM_PROGRAM_VERSION")
	env = mergeEnvironment(env, []string{"TERM=xterm-256color", "TERM_PROGRAM=ghostty"})
	if agent != "shell" {
		env = mergeEnvironment(env, []string{
			"ATTN_INSIDE_APP=1",
			"ATTN_DAEMON_MANAGED=1",
			"ATTN_SESSION_ID=" + opts.ID,
			"ATTN_AGENT=" + agent,
		})
		if wrapperPath != "" {
			env = mergeEnvironment(env, []string{"ATTN_WRAPPER_PATH=" + wrapperPath})
			// Ensure the directory containing attn is in PATH so that
			// tools (e.g. Claude Code skills) can find it as a bare command
			// even when installed only inside the .app bundle.
			if dir := filepath.Dir(wrapperPath); dir != "" && dir != "." {
				env = prependPath(env, dir)
			}
		}

		executable := configuredExecutableForAgent(opts, agent)
		if d := agentdriver.Get(agent); d != nil {
			envKey := strings.TrimSpace(d.ExecutableEnvVar())
			if envKey != "" && executable != "" && executable != d.DefaultExecutable() {
				env = mergeEnvironment(env, []string{envKey + "=" + executable})
			}
		} else {
			if opts.ClaudeExecutable != "" && opts.ClaudeExecutable != "claude" {
				env = mergeEnvironment(env, []string{"ATTN_CLAUDE_EXECUTABLE=" + opts.ClaudeExecutable})
			}
			if opts.CodexExecutable != "" && opts.CodexExecutable != "codex" {
				env = mergeEnvironment(env, []string{"ATTN_CODEX_EXECUTABLE=" + opts.CodexExecutable})
			}
			if opts.CopilotExecutable != "" && opts.CopilotExecutable != "copilot" {
				env = mergeEnvironment(env, []string{"ATTN_COPILOT_EXECUTABLE=" + opts.CopilotExecutable})
			}
		}
	}
	if len(opts.ExternalEnv) > 0 {
		env = mergeEnvironment(env, opts.ExternalEnv)
	}
	return env
}

func configuredExecutableForAgent(opts SpawnOptions, agent string) string {
	if strings.TrimSpace(opts.Executable) != "" {
		return strings.TrimSpace(opts.Executable)
	}
	switch agent {
	case "claude":
		return strings.TrimSpace(opts.ClaudeExecutable)
	case "codex":
		return strings.TrimSpace(opts.CodexExecutable)
	case "copilot":
		return strings.TrimSpace(opts.CopilotExecutable)
	default:
		return ""
	}
}

// ReadLoginShellEnv spawns a login shell and captures its environment.
// Typically ~130ms; callers should cache the result.
func ReadLoginShellEnv(shellPath string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvTimeout)
	defer cancel()

	args := []string{"-l", "-c", "env -0"}
	if strings.HasSuffix(shellPath, "zsh") {
		args = []string{"-l", "-i", "-c", "env -0"}
	}
	cmd := exec.CommandContext(ctx, shellPath, args...)
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

// prependPath adds dir to the front of the PATH variable in env,
// avoiding duplicates.
func prependPath(env []string, dir string) []string {
	for i, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			existing := entry[5:]
			for _, p := range strings.Split(existing, string(os.PathListSeparator)) {
				if p == dir {
					return env // already present
				}
			}
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + existing
			return env
		}
	}
	return append(env, "PATH="+dir)
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

// GetUserLoginShell returns the current user's login shell path.
func GetUserLoginShell() string {
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
