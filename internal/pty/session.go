package pty

import (
	"bytes"
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

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// TerminalTheme carries the frontend's resolved terminal colors as "#rrggbb"
// hex strings. Zero-value fields fall back to built-in dark defaults.
type TerminalTheme struct {
	Foreground string
	Background string
	Cursor     string
}

// Default OSC 10/11/12 colors, used for any TerminalTheme field that is empty
// or fails hex validation. These match the frontend's built-in dark theme.
const (
	defaultThemeForeground = "#d4d4d4"
	defaultThemeBackground = "#1e1e1e"
	defaultThemeCursor     = "#d4d4d4"
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
	da1 bool
	cpr bool
	// osc10/osc11/osc12 count OCCURRENCES in the chunk, not presence — a chunk
	// containing three OSC 11 queries (e.g. a TUI probing color support) must
	// get three replies, or the caller under-answers and the program hangs
	// waiting for a reply that already went out for an earlier query. Derived
	// from oscQueryOrder below.
	osc10 int
	osc11 int
	osc12 int
	// oscQueryOrder lists the OSC color codes (10/11/12) queried in this
	// chunk in the order they appeared. Real terminals answer OSC queries in
	// ask order, and a client that writes a burst of mixed OSC10/11/12
	// queries and pairs replies positionally depends on that order — a
	// fixed-order reply (e.g. all OSC10 first) would mispair against it.
	oscQueryOrder []int
	// da1BeforeCPR records that the chunk asked DA1 before CPR. Query-driven
	// programs read replies sequentially, so the daemon answers in ask order.
	da1BeforeCPR bool
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
	// cleanup removes spawn-time resources that must outlive shell startup,
	// such as an isolated startup-file overlay for an interactive shell pane.
	cleanup func()

	scrollback *RingBuffer
	replayLog  *ReplayLog
	screen     *virtualScreen
	// ghostty is the server-authoritative parsed terminal (libghostty-vt). In
	// shadow mode it is fed the same bytes as the three stores above but nothing
	// reads it in production yet (Phase 1). It answers query responses during
	// Write; those are drained-and-discarded here so the scan-based responder
	// stays the sole answerer. May be nil if construction failed — shadow mode
	// must never break a session, so every use is nil-guarded.
	ghostty *ghosttyvt.Terminal
	// blockFeed owns writes into ghostty, splitting at OSC 133 markers to
	// maintain the worker-side command-block table (Phase 3a). nil exactly
	// when ghostty is nil; every use is nil-guarded like ghostty's.
	blockFeed *blockFeeder
	// attachSnapshotMode mirrors the daemon's ATTN_ATTACH_SNAPSHOT flag (read
	// from the worker's env at spawn). When true, the read loop forwards the
	// ghostty terminal's query responses the scan-based responder does not cover
	// (e.g. kitty CSI ? u) to the PTY, making the worker the complete query
	// answerer so a snapshot-restored client can suppress every response. When
	// false, behavior is exactly today's: responses are drained and discarded.
	attachSnapshotMode bool
	seqCounter         atomic.Uint32

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

	// themeMu guards theme, which seeds OSC 10/11/12 (fg/bg/cursor color)
	// replies. Set at spawn (SpawnOptions.Theme) and updated live via SetTheme;
	// read from the read loop on every OSC color query.
	themeMu sync.RWMutex
	theme   TerminalTheme

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
		if s.cleanup != nil {
			s.cleanup()
		}
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

				// The worker is the single, always-on responder for CPR, DA1, and
				// OSC 10/11/12 — race-free regardless of frontend attach/replay
				// timing, and unaffected by whether an interactive subscriber is
				// attached. CPR and DA1 are answered below from the screen model
				// / a static capability string (AGENTS.md pattern #7). OSC 10/11/12
				// (fg/bg/cursor color) are answered here from the daemon-pushed
				// theme (see SetTheme); the frontend does not answer any of these.
				if len(queries.oscQueryOrder) > 0 {
					s.writeOSCColorResponses(queries, logf)
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
				if s.blockFeed != nil {
					// Feed the server-authoritative terminal under the same lock
					// as the seq watermark so a snapshot stays atomic with it;
					// the feeder splits at OSC 133 markers to pin block positions.
					s.blockFeed.feed(data)
				}
				s.lastReplaySeq = seq
				s.replayMu.Unlock()
				// Drain ghostty's query responses AFTER the lock (the sink has
				// its own mutex). In snapshot mode, forward the responses the
				// scanner does not cover (kitty CSI ? u, etc.) so the worker
				// answers every query and a restored client can suppress all of
				// them; otherwise drain-and-discard (scanner stays sole answerer).
				s.drainGhosttyResponses(logf)
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
		if s.blockFeed != nil {
			s.blockFeed.feed(carryover)
		}
		s.lastReplaySeq = seq
		s.replayMu.Unlock()
		s.drainGhosttyResponses(logf)
		s.fanOut(carryover, seq)
	}

	waitErr := s.cmd.Wait()
	exitCode, signal := parseExitStatus(waitErr)
	s.markExited(exitCode, signal)

	s.logShadowDivergence(logf)

	if onExit != nil {
		onExit(exitCode, signal)
	}
}

// attachSnapshotEnv gates server-authoritative snapshot attach. When set to "1"
// the daemon serves a ghostty-serialized snapshot on attach and the worker
// forwards the query responses the scanner does not cover. Read from the worker
// process env at spawn, mirroring the daemon-side flag (both processes see it).
const attachSnapshotEnv = "ATTN_ATTACH_SNAPSHOT"

// attachSnapshotEnabled reports whether server-authoritative snapshot mode is on.
func attachSnapshotEnabled() bool {
	return os.Getenv(attachSnapshotEnv) == "1"
}

// drainGhosttyResponses clears the ghostty terminal's accumulated query
// responses. In snapshot mode it forwards the responses the scan-based responder
// does not cover (kitty CSI ? u and any other non-CPR/DA1/OSC-color reports) to
// the PTY, so the worker answers every query and a snapshot-restored client can
// suppress all responses. Otherwise the responses are discarded (the scanner
// stays the sole answerer, byte-identical to pre-snapshot behavior). Must be
// called after replayMu is released; the sink has its own lock.
func (s *Session) drainGhosttyResponses(logf func(string, ...interface{})) {
	if s.ghostty == nil {
		return
	}
	drained := s.ghostty.DrainResponses()
	if !s.attachSnapshotMode || len(drained) == 0 {
		return
	}
	gap := stripScannerOwnedResponses(drained)
	if len(gap) == 0 {
		return
	}
	s.writeMu.Lock()
	_, _ = s.ptmx.Write(gap)
	s.writeMu.Unlock()
	if logf != nil {
		logf("pty ghostty gap reply: session=%s bytes=%d", s.id, len(gap))
	}
}

// stripScannerOwnedResponses removes, from a ghostty query-response stream, the
// response classes the scan-based responder already emits — CPR (CSI … R), DA
// (CSI … c), and OSC 10/11/12 color reports — so forwarding the remainder never
// double-answers a query the scanner handles. Kitty keyboard reports (CSI ? … u),
// DECRQM reports (CSI ? … $ y), and anything else are kept. Unrecognized bytes
// are preserved so a partial/interleaved stream is never silently dropped.
func stripScannerOwnedResponses(resp []byte) []byte {
	out := make([]byte, 0, len(resp))
	for i := 0; i < len(resp); {
		if resp[i] != 0x1b || i+1 >= len(resp) {
			out = append(out, resp[i])
			i++
			continue
		}
		switch resp[i+1] {
		case '[': // CSI … final byte in 0x40–0x7e
			j := i + 2
			for j < len(resp) && !(resp[j] >= 0x40 && resp[j] <= 0x7e) {
				j++
			}
			if j >= len(resp) {
				out = append(out, resp[i:]...)
				i = len(resp)
				continue
			}
			final := resp[j]
			seq := resp[i : j+1]
			// CPR (R) and DA (c) are the scanner's; drop them. Everything else
			// (kitty u, DECRQM $y, …) is a gap the scanner misses — keep it.
			if final != 'R' && final != 'c' {
				out = append(out, seq...)
			}
			i = j + 1
		case ']': // OSC … terminated by BEL or ST (ESC \)
			j := i + 2
			for j < len(resp) {
				if resp[j] == 0x07 {
					j++
					break
				}
				if resp[j] == 0x1b && j+1 < len(resp) && resp[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			seq := resp[i:j]
			// OSC 10/11/12 color reports are the scanner's; drop them.
			if !isOSCColorReport(seq) {
				out = append(out, seq...)
			}
			i = j
		default:
			out = append(out, resp[i], resp[i+1])
			i += 2
		}
	}
	return out
}

// isOSCColorReport reports whether an OSC sequence is a 10/11/12 color report
// (ESC ] 1{0,1,2} ;). It matches the codes the scan-based responder answers.
func isOSCColorReport(seq []byte) bool {
	const prefixLen = 5 // ESC ] 1 X ;
	if len(seq) < prefixLen || seq[0] != 0x1b || seq[1] != ']' || seq[2] != '1' {
		return false
	}
	return (seq[3] == '0' || seq[3] == '1' || seq[3] == '2') && seq[4] == ';'
}

// shadowDivergenceEnv gates the Phase 1 shadow divergence metric. When set to
// "1", session close compares the ghostty viewport against the vt10x oracle and
// logs the first differing row. Off by default (the comparison renders both
// screens, which is cheap but not free).
const shadowDivergenceEnv = "ATTN_GHOSTTY_SHADOW_DIVERGENCE"

// logShadowDivergence compares the ghostty-parsed viewport against the vt10x
// oracle's visible screen and logs the first divergence, if any. This is the
// Phase 1 risk-burn signal: it tells us where the two emulators disagree on
// real sessions before anything reads ghostty in production. Divergences where
// vt10x is known-weak (truecolor, wide chars, reflow) are expected; triage
// ghostty-side surprises. Best-effort and behind a debug flag.
func (s *Session) logShadowDivergence(logf func(string, ...any)) {
	if logf == nil || s.ghostty == nil || s.screen == nil {
		return
	}
	if os.Getenv(shadowDivergenceEnv) != "1" {
		return
	}
	oracle := s.screen.renderedText()
	if oracle == "" {
		return
	}
	s.metaMu.RLock()
	rows := int(s.rows)
	s.metaMu.RUnlock()
	shadow := ghosttyViewportTail(s.ghostty.PlainText(), rows)

	oracleLines := strings.Split(oracle, "\n")
	shadowLines := strings.Split(shadow, "\n")
	row, ok := firstDivergentRow(oracleLines, shadowLines)
	if !ok {
		logf("ghostty shadow: session %s viewport matches vt10x (%d rows)", s.id, len(oracleLines))
		return
	}
	logf("ghostty shadow DIVERGENCE session %s row %d\n  vt10x:   %q\n  ghostty: %q",
		s.id, row, rowAt(oracleLines, row), rowAt(shadowLines, row))
}

// ghosttyViewportTail extracts the visible viewport from ghostty's full-screen
// plain text (which includes scrollback) by taking the last `rows` lines and
// trimming trailing blank lines, to match vt10x renderedText's shape. Assumes
// the terminal is scrolled to the bottom, which holds for a live session.
func ghosttyViewportTail(plain string, rows int) string {
	lines := strings.Split(plain, "\n")
	// Trim trailing blank rows first (viewport bottom padding + the formatter's
	// trailing newline) so the last line is real content, then window to the
	// bottom `rows` lines — mirroring renderedText's shape.
	for len(lines) > 0 && strings.TrimRight(lines[len(lines)-1], " ") == "" {
		lines = lines[:len(lines)-1]
	}
	if rows > 0 && len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	// Trailing whitespace per row also differs cosmetically; normalize.
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func firstDivergentRow(a, b []string) (int, bool) {
	n := max(len(a), len(b))
	for i := range n {
		if rowAt(a, i) != rowAt(b, i) {
			return i, true
		}
	}
	return 0, false
}

func rowAt(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
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
	// Serialize the server-authoritative ghostty terminal under the same lock,
	// so its VT dump and the watermark below form the same atomic pair as the
	// raw-replay payload: every byte in the dump has seq <= replayWatermark.
	// Always captured when ghostty exists (cheap, ~180µs); the daemon decides
	// whether to serve it (ATTN_ATTACH_SNAPSHOT). nil when construction failed.
	var ghosttySnapshot []byte
	if s.ghostty != nil {
		ghosttySnapshot = s.ghostty.Serialize().VTDump
	}
	// Resolve command blocks inside the SAME hold as the dump and watermark:
	// the attach snapshot is an atomic {dump, blocks, watermark} triple, so a
	// block row always indexes the dump it shipped with (Phase 3a contract).
	var ghosttyBlocks []AttachBlockData
	if s.blockFeed != nil {
		ghosttyBlocks = s.blockFeed.snapshotBlocks()
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
		GhosttySnapshot:     ghosttySnapshot,
		GhosttyBlocks:       ghosttyBlocks,
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
	if s.ghostty != nil {
		s.ghostty.Resize(int(cols), int(rows))
	}

	return creackpty.Setsize(s.ptmx, &creackpty.Winsize{Cols: cols, Rows: rows})
}

// sigtermToHUPGrace is how long kill waits for a SIGTERM'd child before
// escalating to SIGHUP. Interactive shells ignore SIGTERM by design but
// every shell honors terminal hangup; without this escalation a shell
// pane close stalls the full kill timeout and ends in SIGKILL.
const sigtermToHUPGrace = 2 * time.Second

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

	deadline := time.Now().Add(waitTimeout)

	if sig == syscall.SIGTERM {
		grace := sigtermToHUPGrace
		if half := waitTimeout / 2; grace > half {
			grace = half
		}
		select {
		case <-s.exited:
			return nil
		case <-time.After(grace):
			_ = syscall.Kill(-pgid, syscall.SIGHUP)
		}
	}

	select {
	case <-s.exited:
		return nil
	case <-time.After(time.Until(deadline)):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-s.exited
		return nil
	}
}

func (s *Session) closePTY() {
	_ = s.ptmx.Close()
	if s.blockFeed != nil {
		s.blockFeed.close()
	}
	if s.ghostty != nil {
		s.ghostty.Close()
	}
}

func detectTerminalQueries(data []byte) terminalQueries {
	da1Idx := indexDA1Query(data)
	cprIdx := indexCPRQuery(data)
	oscOrder := scanOSCColorQueries(data)
	var osc10, osc11, osc12 int
	for _, code := range oscOrder {
		switch code {
		case 10:
			osc10++
		case 11:
			osc11++
		case 12:
			osc12++
		}
	}
	return terminalQueries{
		da1:           da1Idx >= 0,
		cpr:           cprIdx >= 0,
		da1BeforeCPR:  da1Idx >= 0 && cprIdx >= 0 && da1Idx < cprIdx,
		osc10:         osc10,
		osc11:         osc11,
		osc12:         osc12,
		oscQueryOrder: oscOrder,
	}
}

// SetTheme replaces the colors used to answer OSC 10/11/12 queries. Safe to
// call concurrently with the read loop.
func (s *Session) SetTheme(theme TerminalTheme) {
	s.themeMu.Lock()
	s.theme = theme
	s.themeMu.Unlock()
}

func (s *Session) currentTheme() TerminalTheme {
	s.themeMu.RLock()
	defer s.themeMu.RUnlock()
	return s.theme
}

// writeOSCColorResponses answers every OSC 10/11/12 query in queries.oscQueryOrder,
// one reply per query in the order the chunk asked — real terminals answer OSC
// queries in ask order, and a client that writes a burst of mixed OSC10/11/12
// queries and pairs replies positionally depends on that order.
func (s *Session) writeOSCColorResponses(queries terminalQueries, logf func(string, ...interface{})) {
	theme := s.currentTheme()
	fg := hexColorToOSCValue(theme.Foreground, defaultThemeForeground)
	bg := hexColorToOSCValue(theme.Background, defaultThemeBackground)
	cursor := hexColorToOSCValue(theme.Cursor, defaultThemeCursor)

	s.writeMu.Lock()
	for _, code := range queries.oscQueryOrder {
		switch code {
		case 10:
			_, _ = fmt.Fprintf(s.ptmx, "\x1b]10;%s\x1b\\", fg)
		case 11:
			_, _ = fmt.Fprintf(s.ptmx, "\x1b]11;%s\x1b\\", bg)
		case 12:
			_, _ = fmt.Fprintf(s.ptmx, "\x1b]12;%s\x1b\\", cursor)
		}
	}
	s.writeMu.Unlock()

	if logf != nil {
		logf(
			"pty terminal-query reply: session=%s osc10=%d osc11=%d osc12=%d",
			s.id,
			queries.osc10,
			queries.osc11,
			queries.osc12,
		)
	}
}

// hexColorToOSCValue converts a "#rrggbb" hex color into the "rgb:RRRR/GGGG/BBBB"
// value XTerm-style OSC color replies use, doubling each 8-bit channel to
// 16-bit by repeating its hex pair. Falls back to fallbackHex (assumed valid)
// when value is malformed or empty.
func hexColorToOSCValue(value, fallbackHex string) string {
	if !isValidHexColor(value) {
		value = fallbackHex
	}
	r, g, b := value[1:3], value[3:5], value[5:7]
	return fmt.Sprintf("rgb:%s%s/%s%s/%s%s", r, r, g, g, b, b)
}

func isValidHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := value[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
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
	s.writeMu.Lock()
	_, _ = fmt.Fprintf(s.ptmx, "\x1b[%d;%dR", row, col)
	s.writeMu.Unlock()
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
	s.writeMu.Lock()
	_, _ = s.ptmx.Write([]byte("\x1b[?1;2c"))
	s.writeMu.Unlock()
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

// oscColorQueryPrefixes are the recognized OSC color query prefixes (ESC ]
// <code> ; ?, terminated by BEL or ST — the prefix match is sufficient). An
// OSC color SET (e.g. "\x1b]11;#000000\x1b\\", no "?") never matches: the
// prefix requires "?" immediately after ";".
var oscColorQueryPrefixes = [...]struct {
	code   int
	prefix []byte
}{
	{10, []byte("\x1b]10;?")},
	{11, []byte("\x1b]11;?")},
	{12, []byte("\x1b]12;?")},
}

// scanOSCColorQueries scans data for non-overlapping OSC 10/11/12 color
// queries and returns their codes in encounter order — the order real
// terminals answer in, and the order a positional-pairing client depends on.
func scanOSCColorQueries(data []byte) []int {
	var codes []int
	for i := 0; i < len(data); {
		matched := false
		for _, p := range oscColorQueryPrefixes {
			if i+len(p.prefix) <= len(data) && bytes.Equal(data[i:i+len(p.prefix)], p.prefix) {
				codes = append(codes, p.code)
				i += len(p.prefix)
				matched = true
				break
			}
		}
		if !matched {
			i++
		}
	}
	return codes
}
