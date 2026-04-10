package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"
)

// Worker-side debug capture and info probes attach transient/non-interactive
// subscribers before the real frontend xterm is ready. They should not suppress
// terminal-query fallbacks such as DA1.
const debugCaptureSubscriberID = "__attn_debug_capture__"

const (
	fallbackOSC10Response      = "\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\"
	fallbackOSC11Response      = "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	startupQueryFallbackWindow = 5 * time.Second
)

type sessionSubscriber struct {
	id     string
	send   func(data []byte, seq uint32) bool
	onDrop func(reason string)
}

type terminalQueries struct {
	da1   bool
	cpr   bool
	osc10 bool
	osc11 bool
}

func (q terminalQueries) any() bool {
	return q.da1 || q.cpr || q.osc10 || q.osc11
}

type stateDetector interface {
	Observe(chunk []byte) (string, bool)
}

type Session struct {
	id    string
	cwd   string
	agent string

	metaMu sync.RWMutex
	cols   uint16
	rows   uint16

	ptmx *os.File
	cmd  *exec.Cmd

	scrollback *RingBuffer
	replayLog  *ReplayLog
	screen     *virtualScreen
	seqCounter atomic.Uint32

	subMu       sync.RWMutex
	subscribers map[string]*sessionSubscriber

	writeMu sync.Mutex

	queryMu          sync.Mutex
	queryResponses   terminalQueries
	firstAttachMu    sync.Mutex
	firstAttachClaim bool

	// CLI state detection based on PTY output.
	detector      stateDetector
	onState       func(state string)
	stateMu       sync.RWMutex
	detectorState string

	exitMu     sync.RWMutex
	running    bool
	exitCode   *int
	exitSignal *string
	exited     chan struct{}
	exitOnce   sync.Once
	startedAt  time.Time
}

func (s *Session) addSubscriber(subID string, send func([]byte, uint32) bool, onDrop func(reason string)) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	s.subscribers[subID] = &sessionSubscriber{
		id:     subID,
		send:   send,
		onDrop: onDrop,
	}
}

func (s *Session) removeSubscriber(subID string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	delete(s.subscribers, subID)
}

func hasInteractiveSubscribers(subscribers map[string]*sessionSubscriber) bool {
	for id := range subscribers {
		if id == debugCaptureSubscriberID || strings.HasPrefix(id, "info-") {
			continue
		}
		return true
	}
	return false
}

func (s *Session) fanOut(data []byte, seq uint32) {
	s.subMu.RLock()
	if len(s.subscribers) == 0 {
		s.subMu.RUnlock()
		return
	}
	subs := make([]*sessionSubscriber, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		subs = append(subs, sub)
	}
	s.subMu.RUnlock()

	payload := append([]byte(nil), data...)
	var dropIDs []string
	for _, sub := range subs {
		if sub.send == nil {
			continue
		}
		if !sub.send(payload, seq) {
			dropIDs = append(dropIDs, sub.id)
			if sub.onDrop != nil {
				sub.onDrop("buffer_overflow")
			}
		}
	}

	if len(dropIDs) > 0 {
		s.subMu.Lock()
		for _, id := range dropIDs {
			delete(s.subscribers, id)
		}
		s.subMu.Unlock()
	}
}

func (s *Session) readLoop(onExit func(exitCode int, signal string), logf func(string, ...interface{})) {
	defer func() {
		_ = s.ptmx.Close()
	}()

	buf := make([]byte, 16*1024)
	carryover := make([]byte, 0, 64)

	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, len(carryover)+n)
			copy(chunk, carryover)
			copy(chunk[len(carryover):], buf[:n])

			boundary := findSafeBoundary(chunk)
			if boundary < len(chunk) {
				carryover = append(carryover[:0], chunk[boundary:]...)
			} else {
				carryover = carryover[:0]
			}

			if boundary > 0 {
				data := chunk[:boundary]
				queries := detectTerminalQueries(data)

				// Respond to DA1 (Primary Device Attributes) queries when no
				// frontend subscriber is attached. The daemon can spawn shell
				// PTYs before the interactive xterm is ready, while non-interactive
				// worker subscribers like debug capture are already attached.
				// Fish waits about 2 s for these terminal queries before the prompt
				// becomes truly interactive. Shell prompts can also re-issue the
				// same queries shortly after the first attach/resize, so allow
				// resends during the startup window for shell sessions.
				noInteractiveSubs := false
				if queries.any() {
					s.subMu.RLock()
					noInteractiveSubs = !hasInteractiveSubscribers(s.subscribers)
					s.subMu.RUnlock()
				}
				if enabled, allowResend, source := s.terminalQueryFallbackMode(time.Now(), noInteractiveSubs); enabled {
					s.writeTerminalQueryResponses(queries, source, allowResend, logf)
				}

				seq := s.seqCounter.Add(1)
				s.metaMu.RLock()
				cols := s.cols
				rows := s.rows
				s.metaMu.RUnlock()
				s.scrollback.Write(data)
				if s.replayLog != nil {
					s.replayLog.Write(data, cols, rows)
				}
				if s.screen != nil {
					s.screen.Observe(data)
				}
				s.fanOut(data, seq)
				if s.detector != nil && s.onState != nil {
					if state, changed := s.detector.Observe(data); changed {
						s.stateMu.Lock()
						s.detectorState = state
						s.stateMu.Unlock()
						s.onState(state)
					}
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && logf != nil {
				logf("pty read error for session %s: %v", s.id, err)
			}
			break
		}
	}

	if len(carryover) > 0 {
		seq := s.seqCounter.Add(1)
		s.metaMu.RLock()
		cols := s.cols
		rows := s.rows
		s.metaMu.RUnlock()
		s.scrollback.Write(carryover)
		if s.replayLog != nil {
			s.replayLog.Write(carryover, cols, rows)
		}
		if s.screen != nil {
			s.screen.Observe(carryover)
		}
		s.fanOut(carryover, seq)
	}

	waitErr := s.cmd.Wait()
	exitCode, signal := parseExitStatus(waitErr)
	s.markExited(exitCode, signal)

	if onExit != nil {
		onExit(exitCode, signal)
	}
}

func parseExitStatus(waitErr error) (int, string) {
	if waitErr == nil {
		return 0, ""
	}

	exitErr, ok := waitErr.(*exec.ExitError)
	if !ok {
		return 1, ""
	}

	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return exitErr.ExitCode(), ""
	}

	if status.Signaled() {
		return -1, status.Signal().String()
	}
	return status.ExitStatus(), ""
}

func (s *Session) markExited(exitCode int, signal string) {
	s.exitMu.Lock()
	defer s.exitMu.Unlock()

	s.running = false
	s.exitCode = &exitCode
	if signal != "" {
		signalCopy := signal
		s.exitSignal = &signalCopy
	}
	s.exitOnce.Do(func() {
		close(s.exited)
	})
}

func (s *Session) info() AttachInfo {
	s.metaMu.RLock()
	cols := s.cols
	rows := s.rows
	s.metaMu.RUnlock()

	s.exitMu.RLock()
	running := s.running
	var exitCode *int
	if s.exitCode != nil {
		val := *s.exitCode
		exitCode = &val
	}
	var exitSignal *string
	if s.exitSignal != nil {
		val := *s.exitSignal
		exitSignal = &val
	}
	s.exitMu.RUnlock()

	pid := 0
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}

	scrollback, truncated := s.scrollback.Snapshot()
	var replaySegments []ReplaySegment
	replayTruncated := false
	if s.replayLog != nil {
		replaySegments, replayTruncated = s.replayLog.Snapshot()
	}
	var (
		screenSnapshot      []byte
		screenCols          uint16
		screenRows          uint16
		screenCursorX       uint16
		screenCursorY       uint16
		screenCursorVisible bool
		screenSnapshotFresh bool
	)
	if s.screen != nil {
		if snapshot, ok := s.screen.Snapshot(); ok {
			screenSnapshot = snapshot.payload
			screenCols = snapshot.cols
			screenRows = snapshot.rows
			screenCursorX = snapshot.cursorX
			screenCursorY = snapshot.cursorY
			screenCursorVisible = snapshot.cursorVisible
			screenSnapshotFresh = true
		}
	}

	return AttachInfo{
		Scrollback:          scrollback,
		ScrollbackTruncated: truncated,
		ReplaySegments:      replaySegments,
		ReplayTruncated:     replayTruncated,
		LastSeq:             s.seqCounter.Load(),
		Cols:                cols,
		Rows:                rows,
		PID:                 pid,
		Running:             running,
		ExitCode:            exitCode,
		ExitSignal:          exitSignal,
		ScreenSnapshot:      screenSnapshot,
		ScreenCols:          screenCols,
		ScreenRows:          screenRows,
		ScreenCursorX:       screenCursorX,
		ScreenCursorY:       screenCursorY,
		ScreenCursorVisible: screenCursorVisible,
		ScreenSnapshotFresh: screenSnapshotFresh,
	}
}

func (s *Session) state() string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.detectorState
}

func (s *Session) input(data []byte) error {
	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()
	if !running {
		return errors.New("session not running")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	_, err := s.ptmx.Write(data)
	return err
}

func (s *Session) resize(cols, rows uint16) error {
	s.metaMu.Lock()
	s.cols = cols
	s.rows = rows
	s.metaMu.Unlock()
	if s.screen != nil {
		s.screen.Resize(cols, rows)
	}

	return creackpty.Setsize(s.ptmx, &creackpty.Winsize{Cols: cols, Rows: rows})
}

func (s *Session) kill(sig syscall.Signal, waitTimeout time.Duration) error {
	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()
	if !running {
		return nil
	}

	if s.cmd == nil || s.cmd.Process == nil {
		return errors.New("process unavailable")
	}

	pgid := s.cmd.Process.Pid
	if pgid <= 0 {
		return errors.New("invalid process id")
	}
	if actualPGID, err := syscall.Getpgid(s.cmd.Process.Pid); err == nil && actualPGID > 0 {
		pgid = actualPGID
	}

	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}

	select {
	case <-s.exited:
		return nil
	case <-time.After(waitTimeout):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-s.exited
		return nil
	}
}

func (s *Session) closePTY() {
	_ = s.ptmx.Close()
}

func detectTerminalQueries(data []byte) terminalQueries {
	return terminalQueries{
		da1:   containsDA1Query(data),
		cpr:   containsCPRQuery(data),
		osc10: containsOSCColorQuery(data, "10"),
		osc11: containsOSCColorQuery(data, "11"),
	}
}

func (s *Session) claimTerminalQueryResponses(queries terminalQueries) terminalQueries {
	s.queryMu.Lock()
	defer s.queryMu.Unlock()

	responses := terminalQueries{}
	if queries.da1 && !s.queryResponses.da1 {
		s.queryResponses.da1 = true
		responses.da1 = true
	}
	if queries.cpr && !s.queryResponses.cpr {
		s.queryResponses.cpr = true
		responses.cpr = true
	}
	if queries.osc10 && !s.queryResponses.osc10 {
		s.queryResponses.osc10 = true
		responses.osc10 = true
	}
	if queries.osc11 && !s.queryResponses.osc11 {
		s.queryResponses.osc11 = true
		responses.osc11 = true
	}
	return responses
}

func (s *Session) markTerminalQueryResponses(queries terminalQueries) {
	s.queryMu.Lock()
	defer s.queryMu.Unlock()

	if queries.da1 {
		s.queryResponses.da1 = true
	}
	if queries.cpr {
		s.queryResponses.cpr = true
	}
	if queries.osc10 {
		s.queryResponses.osc10 = true
	}
	if queries.osc11 {
		s.queryResponses.osc11 = true
	}
}

func (s *Session) withinStartupQueryWindow(now time.Time) bool {
	if s.startedAt.IsZero() {
		return false
	}
	return now.Sub(s.startedAt) <= startupQueryFallbackWindow
}

func (s *Session) startupQueryFallbackAllowed(now time.Time) bool {
	return s.agent == "shell" && s.withinStartupQueryWindow(now)
}

func (s *Session) terminalQueryFallbackMode(now time.Time, noInteractiveSubs bool) (enabled bool, allowResend bool, source string) {
	startupFallback := s.startupQueryFallbackAllowed(now)
	switch {
	case noInteractiveSubs && startupFallback:
		return true, true, "read_loop_startup_unattached"
	case noInteractiveSubs:
		return true, false, "read_loop_unattached"
	case startupFallback:
		return true, true, "read_loop_startup_interactive"
	default:
		return false, false, ""
	}
}

func (s *Session) claimFirstAttach() bool {
	s.firstAttachMu.Lock()
	defer s.firstAttachMu.Unlock()
	if s.firstAttachClaim {
		return false
	}
	s.firstAttachClaim = true
	return true
}

func (s *Session) flushStartupQueryResponses(logf func(string, ...interface{})) {
	if !s.withinStartupQueryWindow(time.Now()) {
		return
	}
	scrollback, _ := s.scrollback.Snapshot()
	s.writeTerminalQueryResponses(detectTerminalQueries(scrollback), "first_attach_scrollback", true, logf)
}

func (s *Session) writeTerminalQueryResponses(queries terminalQueries, source string, allowResend bool, logf func(string, ...interface{})) {
	var responses terminalQueries
	if allowResend {
		responses = queries
		s.markTerminalQueryResponses(queries)
	} else {
		responses = s.claimTerminalQueryResponses(queries)
	}
	if !responses.any() {
		return
	}

	cprRow := 0
	cprCol := 0
	if responses.osc10 {
		_, _ = s.ptmx.Write([]byte(fallbackOSC10Response))
	}
	if responses.osc11 {
		_, _ = s.ptmx.Write([]byte(fallbackOSC11Response))
	}
	if responses.da1 {
		// xterm DA1 response: VT100 with Advanced Video Option
		_, _ = s.ptmx.Write([]byte("\x1b[?1;2c"))
	}
	if responses.cpr {
		row, col := 1, 1
		if s.screen != nil {
			if snapshot, ok := s.screen.Snapshot(); ok {
				row = int(snapshot.cursorY) + 1
				col = int(snapshot.cursorX) + 1
			}
		}
		cprRow = row
		cprCol = col
		_, _ = s.ptmx.Write([]byte(fmt.Sprintf("\x1b[%d;%dR", row, col)))
	}

	if logf != nil {
		logf(
			"pty terminal-query fallback: session=%s source=%s da1=%v cpr=%v osc10=%v osc11=%v cpr_row=%d cpr_col=%d",
			s.id,
			source,
			responses.da1,
			responses.cpr,
			responses.osc10,
			responses.osc11,
			cprRow,
			cprCol,
		)
	}
}

// containsDA1Query scans data for a CSI Primary Device Attributes query
// (ESC [ c  or  ESC [ 0 c).  It ignores DA2 (ESC [ > c) and other variants.
func containsDA1Query(data []byte) bool {
	for i := 0; i < len(data)-2; i++ {
		if data[i] != 0x1b || data[i+1] != '[' {
			continue
		}
		j := i + 2
		// Skip digit parameters (0x30-0x39) and semicolons (0x3b)
		for j < len(data) && ((data[j] >= '0' && data[j] <= '9') || data[j] == ';') {
			j++
		}
		if j < len(data) && data[j] == 'c' {
			return true
		}
	}
	return false
}

// containsCPRQuery scans data for a DSR 6 / CPR query (ESC [ 6 n).
func containsCPRQuery(data []byte) bool {
	for i := 0; i < len(data)-3; i++ {
		if data[i] == 0x1b && data[i+1] == '[' && data[i+2] == '6' && data[i+3] == 'n' {
			return true
		}
	}
	return false
}

func containsOSCColorQuery(data []byte, code string) bool {
	prefix := []byte("\x1b]" + code + ";?")
	for i := 0; i+len(prefix) <= len(data); i++ {
		if string(data[i:i+len(prefix)]) == string(prefix) {
			return true
		}
	}
	return false
}
