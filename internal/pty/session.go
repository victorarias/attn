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
// subscribers before the real interactive terminal is ready. They should not suppress
// terminal-query fallbacks such as DA1.
const debugCaptureSubscriberID = "__attn_debug_capture__"

const (
	fallbackOSC10Response      = "\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\"
	fallbackOSC11Response      = "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	startupQueryFallbackWindow = 5 * time.Second
)

// infoSnapshotHook is a test-only seam invoked inside info() after the replay
// payload (scrollback/replay segments) and screen snapshot are captured but
// before the attach sequence watermark (LastSeq) is read. It is nil in
// production. Tests set it to inject PTY writes into that window to
// deterministically reproduce the snapshot/watermark consistency race.
var infoSnapshotHook func()

// readLoopSeqGapHook is a test-only seam invoked in the read loop after a
// chunk's sequence number is allocated but before the chunk is applied to
// replay/screen state under replayMu. It is nil in production. Tests set it
// to take snapshots inside that gap and deterministically verify the
// snapshot's watermark never claims chunks its screen does not contain.
var readLoopSeqGapHook func()

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
	// da1BeforeCPR records that the chunk asked DA1 before CPR. Query-driven
	// programs read replies sequentially, so the daemon answers in ask order.
	da1BeforeCPR bool
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

	// replayMu makes the attach replay payload (scrollback / replay segments /
	// screen) and its sequence watermark (lastReplaySeq) a consistent pair, so
	// a re-attaching frontend never drops a chunk that landed between the
	// payload snapshot and the watermark read. Held briefly around each chunk's
	// buffer writes and around info()'s snapshot; fanOut stays outside it.
	replayMu      sync.Mutex
	lastReplaySeq uint32

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

	// approvalResolver clears pending_approval->working off the rendered screen
	// when the user resolves an approval prompt (no hook fires at that moment).
	// Sampled from readLoop (throttled by lastApprovalEval) and from an
	// independent approvalTimer so the clear completes even when the approved
	// command produces no further output. approvalMu serializes the two paths.
	approvalResolver *approvalResolver
	approvalMu       sync.Mutex
	lastApprovalEval time.Time
	approvalTimer    *time.Timer

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

// PTY reads are coalesced before fan-out so sustained output (builds, logs,
// `seq`-style floods) produces few large downstream messages instead of one
// per read. macOS pty reads return tiny chunks under load (~100 bytes, the
// tty queue's pacing), and every message costs real memory in the WebKit
// frontend regardless of size — message count, not byte volume, is what
// balloons the app during heavy output. Interactive traffic must not pay for
// this: a read with nothing queued behind it is emitted immediately, so echo
// latency is unchanged, and a flood batch is bounded by ptyCoalesceWindow.
const (
	ptyReadBufBytes     = 16 * 1024
	ptyCoalesceMaxBytes = 256 * 1024
	ptyCoalesceWindow   = 5 * time.Millisecond
)

type ptyRead struct {
	data []byte
	err  error
}

// nextCoalescedRead returns the next batch of PTY output, blocking for the
// first read. If no further read is already queued the first one is returned
// as-is — the interactive path adds zero latency. A queued read means the
// producer is outpacing the pipeline, so reads are folded in until the batch
// reaches maxBytes or the window elapses. The returned error belongs to the
// last read folded into the batch; callers must not receive again after it.
func nextCoalescedRead(reads <-chan ptyRead, maxBytes int, window time.Duration) ([]byte, error) {
	first := <-reads
	if first.err != nil {
		return first.data, first.err
	}

	var batch []byte
	select {
	case r := <-reads:
		batch = append(make([]byte, 0, maxBytes+ptyReadBufBytes), first.data...)
		batch = append(batch, r.data...)
		if r.err != nil {
			return batch, r.err
		}
	default:
		return first.data, nil
	}

	timer := time.NewTimer(window)
	defer timer.Stop()
	for len(batch) < maxBytes {
		select {
		case r := <-reads:
			batch = append(batch, r.data...)
			if r.err != nil {
				return batch, r.err
			}
		case <-timer.C:
			return batch, nil
		}
	}
	return batch, nil
}

func (s *Session) readLoop(onExit func(exitCode int, signal string), logf func(string, ...interface{})) {
	defer func() {
		_ = s.ptmx.Close()
	}()

	reads := make(chan ptyRead, 4)
	go func() {
		for {
			buf := make([]byte, ptyReadBufBytes)
			n, err := s.ptmx.Read(buf)
			reads <- ptyRead{data: buf[:n], err: err}
			if err != nil {
				return
			}
		}
	}()

	carryover := make([]byte, 0, 64)

	for {
		batch, err := nextCoalescedRead(reads, ptyCoalesceMaxBytes, ptyCoalesceWindow)
		if len(batch) > 0 {
			chunk := make([]byte, len(carryover)+len(batch))
			copy(chunk, carryover)
			copy(chunk[len(carryover):], batch)

			boundary := findSafeBoundary(chunk)
			if boundary < len(chunk) {
				carryover = append(carryover[:0], chunk[boundary:]...)
			} else {
				carryover = carryover[:0]
			}

			if boundary > 0 {
				data := chunk[:boundary]
				queries := detectTerminalQueries(data)

				// CPR and DA1 are answered directly by the daemon below — it owns
				// terminal geometry/capabilities (AGENTS.md pattern #7) and replies
				// always, race-free, regardless of frontend attach/replay timing.
				// fish blocks its prompt redraw on the resize-triggered CPR+DA1
				// until both are answered; after a reattach the frontend can be
				// mid-remount/replay and miss them, stalling the prompt for fish's
				// ~10 s query timeout. Only the theme-dependent OSC color queries
				// remain a startup/unattached fallback: the daemon can spawn shell
				// PTYs before the interactive terminal is ready (only non-interactive
				// worker subscribers like debug capture are attached), and fish waits
				// for these replies before its prompt becomes interactive; shell
				// prompts can re-issue them shortly after the first attach/resize, so
				// resends are allowed during the startup window — and a virtualized
				// running TUI can re-issue them repeatedly while no frontend parser
				// is attached, so unattached resends are always answered. Once a real
				// terminal is attached it owns the OSC color replies (they depend on
				// its theme).
				oscQueries := queries
				oscQueries.cpr = false
				oscQueries.da1 = false
				if oscQueries.any() {
					s.subMu.RLock()
					noInteractiveSubs := !hasInteractiveSubscribers(s.subscribers)
					s.subMu.RUnlock()
					if enabled, allowResend, source := s.terminalQueryFallbackMode(time.Now(), noInteractiveSubs); enabled {
						s.writeTerminalQueryResponses(oscQueries, source, allowResend, logf)
					}
				}

				seq := s.seqCounter.Add(1)
				if readLoopSeqGapHook != nil {
					readLoopSeqGapHook()
				}
				s.metaMu.RLock()
				cols := s.cols
				rows := s.rows
				s.metaMu.RUnlock()
				s.replayMu.Lock()
				s.scrollback.Write(data)
				if s.replayLog != nil {
					s.replayLog.Write(data, cols, rows)
				}
				if s.screen != nil {
					s.screen.Observe(data)
				}
				s.lastReplaySeq = seq
				s.replayMu.Unlock()
				// The daemon is the single authority for CPR (cursor position)
				// and DA1 (device attributes) replies. Answer after the chunk is
				// applied so the reported cursor is current, and reply in the
				// order the chunk asked (fish sends ESC[6n ESC[0c, but other
				// programs may ask DA1 first and read replies sequentially). fish
				// blocks its prompt redraw on the resize-triggered CPR+DA1 until it
				// gets both; routing them through the daemon makes the replies
				// race-free regardless of frontend attach/replay timing (the
				// frontend no longer answers either). See writeCursorPositionResponse
				// and writeDeviceAttributesResponse.
				if queries.da1BeforeCPR {
					s.writeDeviceAttributesResponse(logf)
					s.writeCursorPositionResponse(logf)
				} else {
					if queries.cpr {
						s.writeCursorPositionResponse(logf)
					}
					if queries.da1 {
						s.writeDeviceAttributesResponse(logf)
					}
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
				if len(data) > 0 {
					s.evaluateApproval(time.Now(), true)
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
		s.replayMu.Lock()
		s.scrollback.Write(carryover)
		if s.replayLog != nil {
			s.replayLog.Write(carryover, cols, rows)
		}
		if s.screen != nil {
			s.screen.Observe(carryover)
		}
		s.lastReplaySeq = seq
		s.replayMu.Unlock()
		s.fanOut(carryover, seq)
	}

	waitErr := s.cmd.Wait()
	exitCode, signal := parseExitStatus(waitErr)
	s.markExited(exitCode, signal)

	if onExit != nil {
		onExit(exitCode, signal)
	}
}

// approvalEvalInterval throttles how often the readLoop path inspects the
// rendered screen. Rendering is cheap but the output stream is dominated by many
// tiny cursor-addressed frames; sampling at this cadence keeps cost bounded while
// staying well below approvalClearDebounce.
const approvalEvalInterval = 100 * time.Millisecond

// evaluateApproval samples the rendered screen and applies any approval-state
// transition the resolver reports. It runs on two paths: the readLoop output path
// (throttle=true, so high-frequency frames don't re-render constantly) and a
// scheduled recheck (throttle=false). The scheduled recheck is what lets the
// pending_approval->working clear complete even when the approved command goes
// quiet and emits no further PTY output.
func (s *Session) evaluateApproval(now time.Time, throttle bool) {
	if s.approvalResolver == nil || s.onState == nil || s.screen == nil {
		return
	}
	// Never emit a transition for a session that has already exited; a late
	// timer firing after exit must not resurrect a "working" state.
	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()
	if !running {
		return
	}

	s.approvalMu.Lock()
	if throttle && !s.lastApprovalEval.IsZero() && now.Sub(s.lastApprovalEval) < approvalEvalInterval {
		s.approvalMu.Unlock()
		return
	}
	s.lastApprovalEval = now
	signal := s.approvalResolver.observe(s.screen.renderedText(), now)
	switch signal {
	case approvalClearStarted:
		s.scheduleApprovalRecheckLocked()
	case approvalCleared:
		s.stopApprovalTimerLocked()
	}
	s.approvalMu.Unlock()

	switch signal {
	case approvalArmedPending:
		s.applyApprovalState(statePendingApproval)
	case approvalCleared:
		s.applyApprovalState(stateWorking)
	}
}

func (s *Session) applyApprovalState(state string) {
	s.stateMu.Lock()
	s.detectorState = state
	s.stateMu.Unlock()
	s.onState(state)
}

// scheduleApprovalRecheckLocked arms a one-shot recheck a little past the
// debounce window so the prompt-gone -> working clear fires without depending on
// further PTY output. Caller holds approvalMu. The small extra margin guarantees
// the recheck's now.Sub(clearedSince) has crossed approvalClearDebounce.
func (s *Session) scheduleApprovalRecheckLocked() {
	s.stopApprovalTimerLocked()
	s.approvalTimer = time.AfterFunc(approvalClearDebounce+approvalEvalInterval, func() {
		s.evaluateApproval(time.Now(), false)
	})
}

func (s *Session) stopApprovalTimerLocked() {
	if s.approvalTimer != nil {
		s.approvalTimer.Stop()
		s.approvalTimer = nil
	}
}

func (s *Session) stopApprovalTimer() {
	s.approvalMu.Lock()
	s.stopApprovalTimerLocked()
	s.approvalMu.Unlock()
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
	// Cancel any pending approval recheck before flipping running=false so a
	// timer cannot fire a stale "working" against an exited session.
	s.stopApprovalTimer()

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

	// Capture the replay payload and its sequence watermark atomically so a
	// re-attaching frontend can dedup the live stream against LastSeq without a
	// hole: every byte is either in this payload (seq <= LastSeq) or a live
	// chunk it will apply (seq > LastSeq). Without this, a chunk written
	// between the payload snapshot and the watermark read is in neither — lost.
	s.replayMu.Lock()
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
	replayWatermark := s.lastReplaySeq
	s.replayMu.Unlock()

	// Test seam: drives a PTY write into the post-snapshot window to expose the
	// race on unfixed code. Fired after the unlock so it never deadlocks the
	// read loop. nil (zero overhead) in production.
	if infoSnapshotHook != nil {
		infoSnapshotHook()
	}

	// LastSeq is the dedup boundary: it names the last chunk covered by this
	// payload, so the frontend applies live chunks with seq > LastSeq and
	// drops the rest as already-replayed. screenSnapshot() reports the same
	// covered-chunk semantics; the two must not diverge or the first live
	// chunk after an attach is silently lost (or double-applied).
	return AttachInfo{
		Scrollback:          scrollback,
		ScrollbackTruncated: truncated,
		ReplaySegments:      replaySegments,
		ReplayTruncated:     replayTruncated,
		LastSeq:             replayWatermark,
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

// screenSnapshot is a lean, read-only view of the current rendered screen plus
// the sequence watermark. Unlike info() it omits scrollback and replay history,
// so it is cheap enough to call for many sessions at once (e.g. seeding every
// grid tile). It registers no subscriber and claims no geometry.
//
// The screen and its watermark are captured atomically under replayMu — the
// same critical section the read loop uses to apply a chunk and advance
// lastReplaySeq — so LastSeq names exactly the last chunk baked into this
// snapshot, matching info()/Attach semantics (the two must not diverge).
// seqCounter would be wrong here: the read loop increments it BEFORE applying
// the chunk, so a snapshot landing in that gap would claim to cover bytes the
// screen does not contain, and an observer deduping the live stream against
// LastSeq would silently drop the chunk carrying them.
func (s *Session) screenSnapshot() AttachInfo {
	s.metaMu.RLock()
	cols := s.cols
	rows := s.rows
	s.metaMu.RUnlock()

	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()

	pid := 0
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}

	info := AttachInfo{
		Cols:    cols,
		Rows:    rows,
		PID:     pid,
		Running: running,
	}
	s.replayMu.Lock()
	if s.screen != nil {
		if snapshot, ok := s.screen.Snapshot(); ok {
			info.ScreenSnapshot = snapshot.payload
			info.ScreenCols = snapshot.cols
			info.ScreenRows = snapshot.rows
			info.ScreenCursorX = snapshot.cursorX
			info.ScreenCursorY = snapshot.cursorY
			info.ScreenCursorVisible = snapshot.cursorVisible
			info.ScreenSnapshotFresh = true
		}
	}
	info.LastSeq = s.lastReplaySeq
	s.replayMu.Unlock()
	return info
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
	da1Idx := indexDA1Query(data)
	cprIdx := indexCPRQuery(data)
	return terminalQueries{
		da1:          da1Idx >= 0,
		cpr:          cprIdx >= 0,
		da1BeforeCPR: da1Idx >= 0 && cprIdx >= 0 && da1Idx < cprIdx,
		osc10:        containsOSCColorQuery(data, "10"),
		osc11:        containsOSCColorQuery(data, "11"),
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
		return true, true, "read_loop_unattached"
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

	if responses.osc10 {
		_, _ = s.ptmx.Write([]byte(fallbackOSC10Response))
	}
	if responses.osc11 {
		_, _ = s.ptmx.Write([]byte(fallbackOSC11Response))
	}

	if logf != nil {
		logf(
			"pty terminal-query fallback: session=%s source=%s osc10=%v osc11=%v",
			s.id,
			source,
			responses.osc10,
			responses.osc11,
		)
	}
}

// writeCursorPositionResponse answers a CPR (cursor position report) query from
// the authoritative screen model. The daemon is the single CPR responder for a
// session: fish blocks its prompt redraw on the resize-triggered CPR until it
// gets a reply, and routing every CPR through the daemon (which owns geometry,
// AGENTS.md pattern #7) makes the reply race-free regardless of frontend
// attach/replay timing. The frontend deliberately does not answer CPR, so there
// is no double-reply to confuse the shell.
func (s *Session) writeCursorPositionResponse(logf func(string, ...any)) {
	row, col := 1, 1
	if s.screen != nil {
		if snapshot, ok := s.screen.Snapshot(); ok {
			row = int(snapshot.cursorY) + 1
			col = int(snapshot.cursorX) + 1
		}
	}
	_, _ = fmt.Fprintf(s.ptmx, "\x1b[%d;%dR", row, col)
	if logf != nil {
		logf("pty cpr reply: session=%s row=%d col=%d", s.id, row, col)
	}
}

// writeDeviceAttributesResponse answers a DA1 (primary device attributes) query.
// Like CPR, the daemon is the single DA1 responder for a session: fish blocks its
// prompt redraw on the resize-triggered DA1 until it gets a reply, and after a
// reattach the frontend can be mid-remount/replay and miss it (fish then stalls
// for its ~10 s query timeout). The reply is a static capability string identical
// to the one the frontend would send, so routing every DA1 through the daemon
// (which owns geometry/capabilities, AGENTS.md pattern #7) is safe and race-free.
// The frontend deliberately does not answer DA1, so there is no double-reply.
func (s *Session) writeDeviceAttributesResponse(logf func(string, ...any)) {
	// DA1 response: VT100 with Advanced Video Option.
	_, _ = s.ptmx.Write([]byte("\x1b[?1;2c"))
	if logf != nil {
		logf("pty da1 reply: session=%s", s.id)
	}
}

// indexDA1Query returns the offset of the first CSI Primary Device Attributes
// query (ESC [ c  or  ESC [ 0 c) in data, or -1. It ignores DA2 (ESC [ > c)
// and other variants.
func indexDA1Query(data []byte) int {
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
			return i
		}
	}
	return -1
}

// indexCPRQuery returns the offset of the first DSR 6 / CPR query
// (ESC [ 6 n) in data, or -1.
func indexCPRQuery(data []byte) int {
	for i := 0; i < len(data)-3; i++ {
		if data[i] == 0x1b && data[i+1] == '[' && data[i+2] == '6' && data[i+3] == 'n' {
			return i
		}
	}
	return -1
}

func containsCPRQuery(data []byte) bool { return indexCPRQuery(data) >= 0 }

func containsOSCColorQuery(data []byte, code string) bool {
	prefix := []byte("\x1b]" + code + ";?")
	for i := 0; i+len(prefix) <= len(data); i++ {
		if string(data[i:i+len(prefix)]) == string(prefix) {
			return true
		}
	}
	return false
}
