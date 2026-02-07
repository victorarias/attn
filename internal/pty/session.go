package pty

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"
)

type sessionSubscriber struct {
	id     string
	send   func(data []byte, seq uint32) bool
	onDrop func(reason string)
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
	screen     *virtualScreen
	seqCounter atomic.Uint32

	subMu       sync.RWMutex
	subscribers map[string]*sessionSubscriber

	writeMu sync.Mutex

	// Codex-only state detection based on PTY output.
	detector *codexStateDetector
	onState  func(state string)

	exitMu     sync.RWMutex
	running    bool
	exitCode   *int
	exitSignal *string
	exited     chan struct{}
	exitOnce   sync.Once
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
				seq := s.seqCounter.Add(1)
				s.scrollback.Write(data)
				if s.screen != nil {
					s.screen.Observe(data)
				}
				s.fanOut(data, seq)
				if s.detector != nil && s.onState != nil {
					if state, changed := s.detector.Observe(data); changed {
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
		s.scrollback.Write(carryover)
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
