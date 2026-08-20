package pty

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/ghosttyvt"
)

// TerminalTheme carries the frontend's resolved terminal colors as "#rrggbb"
// hex strings; zero-value fields fall back to built-in dark defaults.
type TerminalTheme struct {
	Foreground  string
	Background  string
	Cursor      string
	ANSIPalette [16]string
}

// Default OSC 10/11/12 colors, matching the frontend's built-in dark theme.
const (
	defaultThemeForeground = "#d4d4d4"
	defaultThemeBackground = "#1e1e1e"
	defaultThemeCursor     = "#d4d4d4"
)

// infoSnapshotHook is a test-only seam fired inside info() between the ghostty
// serialize and the LastSeq read. nil in production.
var infoSnapshotHook func()

// readLoopSeqGapHook is a test-only seam fired after a chunk's seq is
// allocated but before the chunk is applied under replayMu. nil in production.
var readLoopSeqGapHook func()

// colorSchemeReplyHook is a test-only seam fired after a DSR 996 reply is
// written. nil in production.
var colorSchemeReplyHook func()

type sessionSubscriber struct {
	id     string
	send   func(data []byte, seq uint32) bool
	onDrop func(reason string)
	// onPlacements receives the kitty placement set whenever a chunk moved it;
	// nil for subscribers that do not draw. See OnPlacements.
	onPlacements func(update PlacementUpdate)
}

type terminalQueries struct {
	da1 bool
	cpr bool
	// osc10/osc11/osc12 count OCCURRENCES, not presence: each query needs its
	// own reply or the program hangs. Derived from oscQueryOrder.
	osc10 int
	osc11 int
	osc12 int
	// oscQueryOrder lists the OSC color codes (10/11/12) queried, in ask order
	// — clients that pair replies positionally depend on it.
	oscQueryOrder []int
	// colorScheme counts DSR `CSI ? 996 n` color-scheme queries, the same way
	// the OSC counts do: one reply per ask.
	colorScheme int
	// da1BeforeCPR records that the chunk asked DA1 before CPR.
	da1BeforeCPR bool
}

type Session struct {
	id    string
	cwd   string
	agent string

	metaMu sync.RWMutex
	cols   uint16
	rows   uint16
	// Cell size in device pixels; zero until the first fit. The cell is
	// remembered rather than the total so a pixel-less resize can re-derive
	// its total.
	cellW uint16
	cellH uint16

	ptmx *os.File
	cmd  *exec.Cmd
	// cleanup removes spawn-time resources that must outlive shell startup.
	cleanup func()

	// ghostty is the server-authoritative parsed terminal (libghostty-vt):
	// approval detection, query replies, screen snapshots, attach restore.
	ghostty *ghosttyvt.Terminal
	// wireFeed owns writes into ghostty and returns the bytes the wire carries
	// instead. nil exactly when ghostty is nil; every use is nil-guarded.
	wireFeed *wireFeeder
	// kittyEpoch is the offset folded into every kitty generation this session
	// reports; wireFeed holds the same value for the placement half, this
	// field serves kittyImage. Set at construction. See mintKittyEpoch.
	kittyEpoch uint64
	seqCounter atomic.Uint32

	// replayMu makes Ghostty feeds and lastReplaySeq atomic for snapshots, so
	// a re-attaching frontend never drops a chunk that landed between the
	// payload snapshot and the watermark read. fanOut stays outside it.
	replayMu      sync.Mutex
	lastReplaySeq uint32

	subMu       sync.RWMutex
	subscribers map[string]*sessionSubscriber

	// writeMu guards every ptmx access that is not a Read: writes, the resize
	// ioctl, and the close — Fd() must not race Close. ptmxClosed makes a late
	// caller a no-op instead of a use of a dead fd.
	writeMu    sync.Mutex
	ptmxClosed bool

	// themeMu guards theme, which seeds OSC 10/11/12 and DSR 996 replies, and
	// reportedScheme, the light/dark answer the child was last told — the
	// gate that keeps SetTheme from re-announcing a scheme that did not move.
	themeMu        sync.RWMutex
	theme          TerminalTheme
	reportedScheme colorScheme
	// colorSchemeReports is set by the child's DECSET 2031 and cleared by its
	// DECRST: unsolicited scheme reports are what that mode subscribes to, and
	// a child that never asked for them must not receive any.
	colorSchemeReports atomic.Bool

	// harnessSignals and shellSignals read state signals off the RAW stream;
	// neither alters the bytes. shellSignals is nil for non-shell agents.
	harnessSignals *harnessSignalObserver
	shellSignals   *shellSignalArbiter
	onState        func(obs Observation)

	// lastSignal is the most recent observation either observer emitted, kept
	// so a restarted daemon can READ the level: an agent parked at its prompt
	// writes nothing, so there would otherwise be no evidence until the user
	// typed. Written by both emitters, read from the info RPC.
	lastSignalMu sync.RWMutex
	lastSignal   *Observation

	exitMu     sync.RWMutex
	running    bool
	exitCode   *int
	exitSignal *string
	exited     chan struct{}
	exitOnce   sync.Once
	startedAt  time.Time
}

func (s *Session) addSubscriber(subID string, send func([]byte, uint32) bool, onDrop func(reason string), opts ...SubscriberOption) {
	sub := &sessionSubscriber{
		id:     subID,
		send:   send,
		onDrop: onDrop,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(sub)
		}
	}
	s.subMu.Lock()
	defer s.subMu.Unlock()
	s.subscribers[subID] = sub
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

// fanOutPlacements hands one placement update to every subscriber that asked
// for placements. Called AFTER the chunk's bytes are fanned out and with
// replayMu released: an update states where images sit on the grid THOSE bytes
// produce, so it must not arrive first.
func (s *Session) fanOutPlacements(update PlacementUpdate) {
	s.subMu.RLock()
	var subs []*sessionSubscriber
	for _, sub := range s.subscribers {
		if sub.onPlacements != nil {
			subs = append(subs, sub)
		}
	}
	s.subMu.RUnlock()

	for _, sub := range subs {
		sub.onPlacements(update)
	}
}

// forceResync drops every subscriber with reason, reaching each client as a
// pty_desync: the frontend resets and re-attaches for a fresh snapshot — the
// escape hatch for a chunk whose grid effect the wire could not express
// (wireFeeder). Call with replayMu released; the callbacks take their own
// locks.
func (s *Session) forceResync(reason string) {
	s.subMu.Lock()
	subs := make([]*sessionSubscriber, 0, len(s.subscribers))
	for id, sub := range s.subscribers {
		subs = append(subs, sub)
		delete(s.subscribers, id)
	}
	s.subMu.Unlock()

	for _, sub := range subs {
		if sub.onDrop != nil {
			sub.onDrop(reason)
		}
	}
}

// PTY reads are coalesced before fan-out: macOS pty reads return tiny chunks
// under load (~100 bytes), and MESSAGE COUNT, not byte volume, balloons the
// WebKit frontend. A read with nothing queued behind it is emitted
// immediately — echo latency unchanged; a flood batch is bounded by
// ptyCoalesceWindow.
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
// first read; with no further read queued it is returned as-is. The returned
// error belongs to the last read folded in; callers must not receive after it.
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
		s.closePTMX()
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

				// A reply makes preceding input observable to the child, so apply
				// its mode changes before any concurrent theme update can race in.
				s.trackColorSchemeReports(data)
				// The worker is the single, always-on responder for CPR, DA1,
				// and OSC 10/11/12 — race-free regardless of frontend
				// attach/replay timing; the frontend answers none of these.
				if len(queries.oscQueryOrder) > 0 {
					s.writeOSCColorResponses(queries, logf)
				}
				if queries.colorScheme > 0 {
					s.writeColorSchemeResponses(queries.colorScheme, logf)
					if colorSchemeReplyHook != nil {
						colorSchemeReplyHook()
					}
				}

				seq := s.seqCounter.Add(1)
				if readLoopSeqGapHook != nil {
					readLoopSeqGapHook()
				}
				wire, resync := data, ""
				var placements []KittyPlacement
				placementsMoved := false
				s.replayMu.Lock()
				if s.wireFeed != nil {
					// Feed under the same lock as the seq watermark so a
					// snapshot stays atomic with it; the placement set read in
					// the same hold is tied to these bytes by the seq.
					wire, resync = s.wireFeed.feed(data)
					placements, placementsMoved = s.wireFeed.changedPlacements()
				}
				s.lastReplaySeq = seq
				s.replayMu.Unlock()
				// Drain ghostty's query responses AFTER the lock (the sink has
				// its own mutex).
				s.drainGhosttyResponses(logf)
				// Answer CPR/DA1 after the chunk is applied so the reported
				// cursor is current, in ask order — fish sends ESC[6n ESC[0c
				// and blocks its prompt redraw until it gets both.
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
				// An empty wire chunk means the feeder is holding an
				// unterminated escape; dedup (`seq > last_seq`) tolerates the
				// missing seq.
				if len(wire) > 0 {
					s.fanOut(wire, seq)
				}
				// After the bytes, never before: the set describes the grid
				// they produce.
				if placementsMoved {
					s.fanOutPlacements(PlacementUpdate{Seq: seq, Placements: placements})
				}
				if resync != "" {
					if logf != nil {
						logf("pty layout resync: session=%s reason=%s", s.id, resync)
					}
					s.forceResync(resync)
				}
				// The signal observers read the RAW chunk, not the wire.
				if s.harnessSignals != nil && s.onState != nil {
					for _, obs := range s.harnessSignals.Observe(data, time.Now()) {
						s.emitSignal(obs)
					}
				}
				if s.shellSignals != nil && s.onState != nil {
					for _, obs := range s.shellSignals.ObserveOutput(data, time.Now()) {
						s.emitSignal(obs)
					}
				}
				if len(data) > 0 {
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
		wire, resync := carryover, ""
		var placements []KittyPlacement
		placementsMoved := false
		s.replayMu.Lock()
		if s.wireFeed != nil {
			wire, resync = s.wireFeed.feed(carryover)
			placements, placementsMoved = s.wireFeed.changedPlacements()
		}
		s.lastReplaySeq = seq
		s.replayMu.Unlock()
		s.drainGhosttyResponses(logf)
		if len(wire) > 0 {
			s.fanOut(wire, seq)
		}
		if placementsMoved {
			s.fanOutPlacements(PlacementUpdate{Seq: seq, Placements: placements})
		}
		if resync != "" {
			if logf != nil {
				logf("pty layout resync: session=%s reason=%s", s.id, resync)
			}
			s.forceResync(resync)
		}
	}

	waitErr := s.cmd.Wait()
	exitCode, signal := parseExitStatus(waitErr)
	s.markExited(exitCode, signal)

	if onExit != nil {
		onExit(exitCode, signal)
	}
}

// drainGhosttyResponses clears the ghostty terminal's accumulated query
// responses and forwards the ones the scan-based responder does not cover
// (kitty CSI ? u, etc.) to the PTY, so the worker answers every query and a
// snapshot-restored client can suppress all responses. Call after replayMu is
// released; the sink has its own lock.
func (s *Session) drainGhosttyResponses(logf func(string, ...interface{})) {
	// The nil check and the drain are one critical section: teardown nils the
	// field under replayMu, so checking outside would drain a freed terminal.
	s.replayMu.Lock()
	var drained []byte
	if s.ghostty != nil {
		drained = s.ghostty.DrainResponses()
	}
	s.replayMu.Unlock()
	if len(drained) == 0 {
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

// stripScannerOwnedResponses removes the response classes the scan-based
// responder already emits — CPR (CSI … R), DA (CSI … c), OSC 10/11/12 color
// reports — so forwarding the remainder never double-answers. Unrecognized
// bytes are preserved so a partial stream is never silently dropped.
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
			// CPR (R) and DA (c) are the scanner's; keep the rest.
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
// (ESC ] 1{0,1,2} ;) — the codes the scan-based responder answers.
func isOSCColorReport(seq []byte) bool {
	const prefixLen = 5 // ESC ] 1 X ;
	if len(seq) < prefixLen || seq[0] != 0x1b || seq[1] != ']' || seq[2] != '1' {
		return false
	}
	return (seq[3] == '0' || seq[3] == '1' || seq[3] == '2') && seq[4] == ';'
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

// emitSignal is the single exit for both signal observers: it remembers the
// observation before handing it on. Both callers run on their own goroutine,
// hence the guard.
func (s *Session) emitSignal(obs Observation) {
	s.lastSignalMu.Lock()
	stored := obs
	s.lastSignal = &stored
	s.lastSignalMu.Unlock()
	s.onState(obs)
}

// LastSignal is the most recent level either observer emitted, false when none
// — what a reconnecting daemon reads to recover evidence it missed.
func (s *Session) LastSignal() (Observation, bool) {
	s.lastSignalMu.RLock()
	defer s.lastSignalMu.RUnlock()
	if s.lastSignal == nil {
		return Observation{}, false
	}
	return *s.lastSignal, true
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

	// Serialize the ghostty terminal and read the watermark atomically: every
	// byte in the dump has seq <= LastSeq, every live chunk to apply has
	// seq > LastSeq. Without this a chunk written between the two is lost.
	s.replayMu.Lock()
	var ghosttySnapshot []byte
	// libghostty-vt surfaces no scrollback-truncation flag yet; reported false
	// until the native serializer exposes one.
	var ghosttyTruncated bool
	if s.ghostty != nil {
		snapshot := s.ghostty.Serialize()
		ghosttySnapshot = snapshot.Payload
	}
	// Blocks and placements resolve inside the SAME hold: the attach snapshot
	// is an atomic {dump, blocks, placements, watermark} quadruple.
	var ghosttyBlocks []AttachBlockData
	var ghosttyPlacements []KittyPlacement
	if s.wireFeed != nil {
		ghosttyBlocks = s.wireFeed.snapshotBlocks()
		ghosttyPlacements, _ = s.wireFeed.snapshotPlacements()
	}
	replayWatermark := s.lastReplaySeq
	s.replayMu.Unlock()

	// Test seam; fired after the unlock so it never deadlocks the read loop.
	if infoSnapshotHook != nil {
		infoSnapshotHook()
	}

	// LastSeq is the dedup boundary. screenSnapshot() reports the same
	// covered-chunk semantics; the two must not diverge or the first live
	// chunk after an attach is silently lost (or double-applied).
	return AttachInfo{
		LastSeq:                    replayWatermark,
		Cols:                       cols,
		Rows:                       rows,
		PID:                        pid,
		Running:                    running,
		ExitCode:                   exitCode,
		ExitSignal:                 exitSignal,
		GhosttySnapshot:            ghosttySnapshot,
		GhosttySnapshotFormat:      snapshotFormat(ghosttySnapshot),
		GhosttyBlocks:              ghosttyBlocks,
		GhosttyPlacements:          ghosttyPlacements,
		GhosttyScrollbackTruncated: ghosttyTruncated,
	}
}

// snapshotFormat names the format of bytes this build just encoded. Nothing to
// name when there are no bytes.
func snapshotFormat(snapshot []byte) string {
	if len(snapshot) == 0 {
		return ""
	}
	return buildinfo.SnapshotFormat
}

// kittyImage copies one stored image out of the session's terminal. Under
// replayMu like every terminal read: teardown nils the terminal under that
// lock. No terminal and an unknown id give the same ordinary answer.
func (s *Session) kittyImage(imageID uint32) (KittyImage, error) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	if s.ghostty == nil {
		return KittyImage{}, fmt.Errorf("%w: image %d (session has no terminal)", ErrKittyImageNotFound, imageID)
	}
	img, ok := s.ghostty.KittyImage(imageID)
	if !ok {
		return KittyImage{}, fmt.Errorf("%w: image %d", ErrKittyImageNotFound, imageID)
	}
	// The second and last fold of the epoch (readPlacements is the other): the
	// two halves must speak the same numbering or the pull repeats forever.
	img.Generation += s.kittyEpoch
	return img, nil
}

// screenSnapshot is a lean, read-only ghostty viewport serialization plus the
// sequence watermark — no scrollback or replay history, cheap enough for many
// sessions at once; no subscriber, no geometry claim.
//
// Captured under replayMu so LastSeq names exactly the last chunk baked in,
// matching info()/Attach semantics. seqCounter would be wrong here: the read
// loop increments it BEFORE applying the chunk, so a snapshot in that gap
// would claim bytes the screen does not contain.
func (s *Session) screenSnapshot() SnapshotInfo {
	s.metaMu.RLock()
	cols := s.cols
	rows := s.rows
	s.metaMu.RUnlock()

	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()

	info := SnapshotInfo{
		Cols:    cols,
		Rows:    rows,
		Running: running,
	}
	s.replayMu.Lock()
	if s.ghostty != nil {
		viewportText := s.ghostty.ViewportText()
		snapshot := s.ghostty.SerializeViewport()
		if snapshot.VTDump != nil {
			info.Screen = &ViewportSnapshot{
				Payload: snapshot.VTDump,
				Text:    viewportText,
				HasText: true,
				Cols:    uint16(snapshot.Cols),
				Rows:    uint16(snapshot.Rows),
			}
		}
	}
	info.LastSeq = s.lastReplaySeq
	s.replayMu.Unlock()
	return info
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

// resize applies a client's geometry to the grid, the worker terminal and the
// kernel's winsize. xpixel/ypixel are the pane's TOTAL device pixels; zero
// means no pixel geometry, and the session then reports the totals its
// remembered cell size implies — an attach-time reconcile must not blank out
// what a fit already measured.
func (s *Session) resize(cols, rows, xpixel, ypixel uint16) error {
	// The cell is derived once, here; everything downstream speaks cells.
	cellW, cellH := uint16(0), uint16(0)
	if xpixel > 0 && ypixel > 0 && cols > 0 && rows > 0 {
		cellW, cellH = xpixel/cols, ypixel/rows
	}
	s.metaMu.Lock()
	s.cols = cols
	s.rows = rows
	if cellW > 0 && cellH > 0 {
		s.cellW, s.cellH = cellW, cellH
	} else {
		cellW, cellH = s.cellW, s.cellH
		if cellW > 0 && cellH > 0 {
			xpixel, ypixel = cols*cellW, rows*cellH
		}
	}
	s.metaMu.Unlock()
	// The resize mutates the same terminal info() serializes, so it belongs in
	// that critical section. No-reflow because every client frame is: the
	// app's fit and replay resize with DEC wraparound off
	// (app/src/utils/ghosttyResize.ts), and every row-indexed mapping across
	// the wire — placements above all — rides on the grids being equal.
	s.replayMu.Lock()
	if s.ghostty != nil {
		// Before the grid resize so the terminal never answers a size report
		// from the old cell against the new grid.
		if cellW > 0 && cellH > 0 {
			s.ghostty.SetCellPixelSize(int(cellW), int(cellH))
		}
		s.ghostty.ResizeNoReflow(int(cols), int(rows))
	}
	// A resize moves images without producing a byte of output, so no chunk
	// carries the correction; deferring to "the next chunk" fails on an idle
	// session, the common case.
	var placements []KittyPlacement
	placementsHeld := false
	if s.wireFeed != nil {
		placements, placementsHeld = s.wireFeed.snapshotPlacements()
	}
	seq := s.lastReplaySeq
	s.replayMu.Unlock()

	// Stamped with the replay watermark, not a fresh seq: no bytes were
	// produced. Clients take a set whose seq is >= the last applied, and every
	// emission carries the WHOLE set, so any dropped one is healed by the next.
	if placementsHeld {
		s.fanOutPlacements(PlacementUpdate{Seq: seq, Placements: placements})
	}

	// The ioctl resolves ptmx.Fd(), so it must not overlap the close.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ptmxClosed {
		return nil
	}
	// X/Y are ws_xpixel/ws_ypixel: the pane's total pixel size, which an image
	// emitter reads through TIOCGWINSZ to decide how large to draw.
	return creackpty.Setsize(s.ptmx, &creackpty.Winsize{Cols: cols, Rows: rows, X: xpixel, Y: ypixel})
}

// closePTMX closes the pty exactly once, shutting out the writers and the
// resize ioctl that share writeMu.
func (s *Session) closePTMX() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ptmxClosed {
		return
	}
	s.ptmxClosed = true
	_ = s.ptmx.Close()
}

// sigtermToHUPGrace is how long kill waits for a SIGTERM'd child before
// escalating to SIGHUP: interactive shells ignore SIGTERM by design but every
// shell honors terminal hangup.
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

// closePTY releases the pty and the native terminal state behind it. Teardown
// takes replayMu — the same lock info() and resize() hold — because
// Manager.Remove can hand an already-looked-up session to an in-flight attach;
// both fields are nil'd under the lock so a late reader sees absence, not a
// freed handle.
func (s *Session) closePTY() {
	s.closePTMX()

	s.replayMu.Lock()
	wireFeed, ghostty := s.wireFeed, s.ghostty
	s.wireFeed, s.ghostty = nil, nil
	if wireFeed != nil {
		wireFeed.close()
	}
	if ghostty != nil {
		ghostty.Close()
	}
	s.replayMu.Unlock()
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
		colorScheme:   countColorSchemeQueries(data),
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
func (s *Session) SetTheme(theme TerminalTheme) error {
	if s.ghostty != nil {
		if err := s.ghostty.SetColorTheme(ghosttyColorTheme(theme)); err != nil {
			return err
		}
	}
	s.themeMu.Lock()
	s.theme = theme
	scheme := themeColorScheme(theme)
	changed := scheme != s.reportedScheme
	s.reportedScheme = scheme
	s.themeMu.Unlock()
	// A child subscribed to DECSET 2031 keeps its own theme in step from these
	// reports; one that never subscribed is not written to, and neither is one
	// whose scheme did not move — a repaint nobody asked for is what the mode
	// exists to avoid.
	if changed && s.colorSchemeReports.Load() {
		s.writeColorSchemeReport(scheme)
	}
	return nil
}

func parseThemeColor(value, fallback string) uint32 {
	if len(value) != 7 || value[0] != '#' {
		value = fallback
	}
	parsed, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		parsed, _ = strconv.ParseUint(fallback[1:], 16, 32)
	}
	return uint32(parsed)
}

func ghosttyColorTheme(theme TerminalTheme) ghosttyvt.ColorTheme {
	result := ghosttyvt.ColorTheme{
		Foreground:    parseThemeColor(theme.Foreground, defaultThemeForeground),
		Background:    parseThemeColor(theme.Background, defaultThemeBackground),
		Cursor:        parseThemeColor(theme.Cursor, defaultThemeCursor),
		HasForeground: true,
		HasBackground: true,
		HasCursor:     true,
	}
	for i, value := range theme.ANSIPalette {
		if len(value) != 7 || value[0] != '#' {
			return result
		}
		parsed, err := strconv.ParseUint(value[1:], 16, 32)
		if err != nil {
			return result
		}
		result.ANSIPalette[i] = uint32(parsed)
	}
	result.HasANSIPalette = true
	return result
}

func (s *Session) currentTheme() TerminalTheme {
	s.themeMu.RLock()
	defer s.themeMu.RUnlock()
	return s.theme
}

// writeOSCColorResponses answers every OSC 10/11/12 query in
// queries.oscQueryOrder, one reply per query in ask order — the order a
// positional-pairing client depends on.
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

// colorScheme is the light/dark preference a `CSI ? 996 n` query asks for.
// The zero value means the child has not been told anything yet.
type colorScheme int

const (
	colorSchemeUnknown colorScheme = iota
	colorSchemeDark
	colorSchemeLight
)

// themeColorScheme derives the light/dark answer from the theme's background,
// with the WCAG relative luminance and the >= 0.5 cut pi itself applies to the
// OSC 11 color it falls back to (pi 0.83.0, theme.ts getThemeForRgbColor). The
// two answers come from one background and one rule, so they cannot disagree.
func themeColorScheme(theme TerminalTheme) colorScheme {
	background := theme.Background
	if !isValidHexColor(background) {
		background = defaultThemeBackground
	}
	channel := func(hex string) float64 {
		value, _ := strconv.ParseUint(hex, 16, 32)
		linear := float64(value) / 255
		if linear <= 0.03928 {
			return linear / 12.92
		}
		return math.Pow((linear+0.055)/1.055, 2.4)
	}
	luminance := 0.2126*channel(background[1:3]) + 0.7152*channel(background[3:5]) + 0.0722*channel(background[5:7])
	if luminance >= 0.5 {
		return colorSchemeLight
	}
	return colorSchemeDark
}

// writeColorSchemeResponses answers every `CSI ? 996 n` query in the chunk.
// pi asks this before it falls back to OSC 11, so an unanswered query leaves
// it running on an environment guess for a terminal that knows the answer.
func (s *Session) writeColorSchemeResponses(count int, logf func(string, ...interface{})) {
	s.themeMu.Lock()
	scheme := themeColorScheme(s.theme)
	s.reportedScheme = scheme
	s.themeMu.Unlock()

	s.writeMu.Lock()
	for i := 0; i < count; i++ {
		_, _ = s.ptmx.Write(colorSchemeReport(scheme))
	}
	s.writeMu.Unlock()

	if logf != nil {
		logf("pty color-scheme reply: session=%s scheme=%d count=%d", s.id, scheme, count)
	}
}

// writeColorSchemeReport sends an unsolicited scheme report, which is what a
// child that enabled DECSET 2031 is listening for.
func (s *Session) writeColorSchemeReport(scheme colorScheme) {
	s.writeMu.Lock()
	_, _ = s.ptmx.Write(colorSchemeReport(scheme))
	s.writeMu.Unlock()
}

// colorSchemeReport is the DSR reply the color-palette-notification protocol
// defines: `CSI ? 997 ; 1 n` for dark, `; 2 n` for light.
func colorSchemeReport(scheme colorScheme) []byte {
	if scheme == colorSchemeLight {
		return []byte("\x1b[?997;2n")
	}
	return []byte("\x1b[?997;1n")
}

// trackColorSchemeReports follows the child's DECSET/DECRST 2031, the mode
// that subscribes to unsolicited scheme reports. Last one in the chunk wins.
func (s *Session) trackColorSchemeReports(data []byte) {
	set := bytes.LastIndex(data, []byte("\x1b[?2031h"))
	reset := bytes.LastIndex(data, []byte("\x1b[?2031l"))
	if set < 0 && reset < 0 {
		return
	}
	s.colorSchemeReports.Store(set > reset)
}

// countColorSchemeQueries counts DSR color-scheme queries (ESC [ ? 9 9 6 n).
func countColorSchemeQueries(data []byte) int {
	return bytes.Count(data, []byte("\x1b[?996n"))
}

// hexColorToOSCValue converts "#rrggbb" into the "rgb:RRRR/GGGG/BBBB" value
// XTerm-style OSC color replies use, doubling each 8-bit channel by repeating
// its hex pair. Falls back to fallbackHex (assumed valid) when malformed.
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

// writeCursorPositionResponse answers a CPR query from the authoritative
// screen model. The daemon is the single CPR responder — fish blocks its
// prompt redraw on the resize-triggered CPR — and the frontend deliberately
// does not answer, so there is no double-reply.
func (s *Session) writeCursorPositionResponse(logf func(string, ...any)) {
	row, col := 1, 1
	// Under replayMu: teardown nils the terminal under that lock.
	s.replayMu.Lock()
	if s.ghostty != nil {
		x, y := s.ghostty.CursorPos()
		row, col = y+1, x+1
	}
	s.replayMu.Unlock()
	s.writeMu.Lock()
	_, _ = fmt.Fprintf(s.ptmx, "\x1b[%d;%dR", row, col)
	s.writeMu.Unlock()
	if logf != nil {
		logf("pty cpr reply: session=%s row=%d col=%d", s.id, row, col)
	}
}

// writeDeviceAttributesResponse answers a DA1 query. Like CPR, the daemon is
// the single responder: after a reattach the frontend can be mid-remount and
// miss it, and fish then stalls for its ~10 s query timeout.
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
// query (ESC [ c or ESC [ 0 c), or -1. It ignores DA2 (ESC [ > c).
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
// (ESC [ 6 n), or -1.
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
// <code> ; ?). An OSC color SET (no "?") never matches.
var oscColorQueryPrefixes = [...]struct {
	code   int
	prefix []byte
}{
	{10, []byte("\x1b]10;?")},
	{11, []byte("\x1b]11;?")},
	{12, []byte("\x1b]12;?")},
}

// scanOSCColorQueries scans data for non-overlapping OSC 10/11/12 color
// queries and returns their codes in encounter order.
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
