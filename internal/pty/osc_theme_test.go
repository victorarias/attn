package pty

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// newOSCTestSession spins up a Session whose ptmx is one end of a socketpair
// and starts its read loop, so tests can write raw bytes as the "PTY output"
// side and read replies off the peer — the same harness cpr_response_test.go
// uses for CPR/DA1.
func newOSCTestSession(t *testing.T) (s *Session, peer *os.File) {
	t.Helper()
	const cols, rows = 80, 24

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	ptmx := os.NewFile(uintptr(fds[0]), "ptmx")
	peer = os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { _ = ptmx.Close(); _ = peer.Close() })

	s = &Session{
		id:          "osc-theme",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() returns an error, never panics
		scrollback:  NewRingBuffer(1 << 20),
		replayLog:   NewReplayLog(1 << 20),
		screen:      newVirtualScreen(cols, rows),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now().Add(-time.Hour),
	}
	go s.readLoop(nil, func(string, ...any) {})
	return s, peer
}

// TestOSCColorQuerySingleReply covers 7a: a single OSC11 query gets exactly
// one reply, using the built-in default background.
func TestOSCColorQuerySingleReply(t *testing.T) {
	_, peer := newOSCTestSession(t)

	if _, err := peer.Write([]byte("\x1b]11;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want exactly %q (one response)", reply, want)
	}
}

// TestOSCColorQuerySetThemeAffectsReply covers 7b: SetTheme changes the
// answered color, and a spawn-seeded theme (via SpawnOptions.Theme, mirrored
// here by constructing the Session with theme already set) answers with the
// seeded color without any SetTheme call.
func TestOSCColorQuerySetThemeAffectsReply(t *testing.T) {
	s, peer := newOSCTestSession(t)
	s.SetTheme(TerminalTheme{Background: "#ffffff"})

	if _, err := peer.Write([]byte("\x1b]11;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply after SetTheme = %q, want %q", reply, want)
	}
}

func TestOSCColorQuerySeededAtSpawnAnswersWithoutSetTheme(t *testing.T) {
	const cols, rows = 80, 24
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	ptmx := os.NewFile(uintptr(fds[0]), "ptmx")
	peer := os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { _ = ptmx.Close(); _ = peer.Close() })

	// Mirrors what Manager.Spawn does with SpawnOptions.Theme: seed
	// session.theme before the read loop ever starts, with no SetTheme call.
	s := &Session{
		id:          "osc-theme-seeded",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		cmd:         &exec.Cmd{},
		scrollback:  NewRingBuffer(1 << 20),
		replayLog:   NewReplayLog(1 << 20),
		screen:      newVirtualScreen(cols, rows),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now().Add(-time.Hour),
		theme:       TerminalTheme{Background: "#010203"},
	}
	go s.readLoop(nil, func(string, ...any) {})

	if _, err := peer.Write([]byte("\x1b]11;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]11;rgb:0101/0202/0303\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want %q (seeded theme, no SetTheme call)", reply, want)
	}
}

// TestOSCColorQueryCountsPerChunk covers 7c: a chunk containing 3x OSC11 +
// 1x OSC10 must produce exactly 3 OSC11 + 1 OSC10 replies — an under-count
// bug the previous boolean-gated fallback had — and the replies must come
// back in query order, not grouped by code: positional-pairing clients
// depend on replies arriving in the order they asked.
func TestOSCColorQueryCountsPerChunk(t *testing.T) {
	_, peer := newOSCTestSession(t)

	chunk := "\x1b]11;?\x07\x1b]11;?\x07\x1b]10;?\x07\x1b]11;?\x07"
	if _, err := peer.Write([]byte(chunk)); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	osc10 := "\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\"
	osc11 := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	want := osc11 + osc11 + osc10 + osc11 // query order: 11, 11, 10, 11
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want %q (replies in query order, not grouped by code)", reply, want)
	}
}

// TestOSCColorQuerySplitAcrossChunksAnswersOnce covers 7d: a query whose
// bytes are split across two reads (carried over via findSafeBoundary) must
// be answered exactly once, not zero or twice.
func TestOSCColorQuerySplitAcrossChunksAnswersOnce(t *testing.T) {
	_, peer := newOSCTestSession(t)

	query := "\x1b]11;?\x07"
	split := len(query) / 2
	if _, err := peer.Write([]byte(query[:split])); err != nil {
		t.Fatalf("peer write (first half): %v", err)
	}
	// Give the read loop a beat to read+carry over the partial escape before
	// the rest arrives, so this genuinely exercises the carryover path rather
	// than racing to land both halves in one coalesced read.
	time.Sleep(20 * time.Millisecond)
	if _, err := peer.Write([]byte(query[split:])); err != nil {
		t.Fatalf("peer write (second half): %v", err)
	}

	want := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want exactly one response %q", reply, want)
	}

	// Confirm there is nothing further queued (a double-answer bug would
	// leave a second reply sitting on the pipe).
	if got, ok := readAvailable(peer, 150*time.Millisecond); ok {
		t.Fatalf("unexpected extra bytes after the single reply: %q", got)
	}
}

// TestOSCColorQuery12AnswersWithCursorColor covers 7e.
func TestOSCColorQuery12AnswersWithCursorColor(t *testing.T) {
	s, peer := newOSCTestSession(t)
	s.SetTheme(TerminalTheme{Cursor: "#abcdef"})

	if _, err := peer.Write([]byte("\x1b]12;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]12;rgb:abab/cdcd/efef\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

// TestOSCColorSetIsNeverAnswered covers 7f: an OSC color SET (no "?") must
// produce zero responses.
func TestOSCColorSetIsNeverAnswered(t *testing.T) {
	_, peer := newOSCTestSession(t)

	if _, err := peer.Write([]byte("\x1b]11;#000000\x1b\\")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	if got, ok := readAvailable(peer, 200*time.Millisecond); ok {
		t.Fatalf("OSC color SET must not be answered; got %q", got)
	}
}

// readAvailable waits up to timeout for at least one byte to arrive on f. It
// exists because SetReadDeadline does not reliably fire on the socketpair
// fds these tests use (a blocking Read on a raw syscall.Socketpair-derived
// os.File can ignore the deadline in this environment), so a bounded "assert
// nothing arrives" check cannot use it. The read goroutine may outlive the
// timeout case; it is unblocked when the test's t.Cleanup closes f.
func readAvailable(f *os.File, timeout time.Duration) (data []byte, ok bool) {
	ch := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := f.Read(buf)
		if n > 0 {
			ch <- buf[:n]
			return
		}
		_ = err
	}()
	select {
	case got := <-ch:
		return got, true
	case <-time.After(timeout):
		return nil, false
	}
}
