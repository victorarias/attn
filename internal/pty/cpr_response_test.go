package pty

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"testing"
	"time"
)

// TestDaemonAnswersCPRAndDA1FromReadLoop locks in the reattach-hang fix: the
// daemon is the single authority for both CPR (cursor position) and DA1 (device
// attributes) replies and answers them directly from its read loop — even while
// an interactive client is attached.
//
// fish reacts to a resize/SIGWINCH (which the app sends on every reattach) by
// emitting CPR+DA1 (ESC[6n ESC[0c) and BLOCKING its prompt redraw until BOTH are
// answered. Before this fix the frontend answered them, and after a reattach it
// is mid-remount/replay and misses them, so fish hung for its ~10 s query timeout
// ("make output shows, then a long pause, then the prompt"). Routing CPR and DA1
// through the daemon makes the replies race-free; the CPR reflects the screen
// model and the DA1 is a static capability string identical to the frontend's.
func TestDaemonAnswersCPRAndDA1FromReadLoop(t *testing.T) {
	const cols, rows = 80, 24

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	ptmx := os.NewFile(uintptr(fds[0]), "ptmx")
	peer := os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { _ = ptmx.Close(); _ = peer.Close() })

	s := &Session{
		id:          "cpr",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() returns an error, never panics
		screen:      newVirtualScreen(cols, rows),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		// Long past the startup-query fallback window: the daemon must answer
		// CPR+DA1 regardless, unlike the gated OSC color fallbacks.
		startedAt: time.Now().Add(-time.Hour),
	}
	go s.readLoop(nil, func(string, ...any) {})

	// An interactive client is attached — the daemon must STILL answer. The hang
	// reproduced precisely in this state (app reattached, then resized).
	s.addSubscriber("frontend", func([]byte, uint32) bool { return true }, nil)

	// Mirror fish-on-resize: position the cursor, then query CPR + DA1 together.
	if _, err := peer.Write([]byte("\x1b[5;7H\x1b[6n\x1b[0c")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	// Read until both replies have arrived (DA1 ends in 'c', written after CPR).
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return bytes.IndexByte(buf, 'R') >= 0 && bytes.IndexByte(buf, 'c') >= 0
	})

	cpr := regexp.MustCompile(`\x1b\[\d+;\d+R`)
	if !cpr.MatchString(reply) {
		t.Fatalf("daemon did not answer CPR on the PTY; got %q", reply)
	}
	// CPR reflects the screen-model cursor (CUP to row 5, col 7).
	if !bytes.Contains([]byte(reply), []byte("\x1b[5;7R")) {
		t.Fatalf("CPR reply should report the screen cursor \x1b[5;7R; got %q", reply)
	}
	// DA1 is the static capability string.
	if !bytes.Contains([]byte(reply), []byte("\x1b[?1;2c")) {
		t.Fatalf("daemon should answer DA1 with \x1b[?1;2c; got %q", reply)
	}
}

// TestTerminalQueryRepliesPreserveChunkOrder: a real terminal answers queries
// in the order they were asked, and query-driven programs read replies
// sequentially. A chunk asking DA1 before CPR must get the DA1 reply first —
// not a fixed CPR-then-DA1 order tuned to fish.
func TestTerminalQueryRepliesPreserveChunkOrder(t *testing.T) {
	const cols, rows = 80, 24

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	ptmx := os.NewFile(uintptr(fds[0]), "ptmx")
	peer := os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { _ = ptmx.Close(); _ = peer.Close() })

	s := &Session{
		id:          "query-order",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() returns an error, never panics
		screen:      newVirtualScreen(cols, rows),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now().Add(-time.Hour),
	}
	go s.readLoop(nil, func(string, ...any) {})

	// DA1 first, then CPR — the reverse of fish's resize probe.
	if _, err := peer.Write([]byte("\x1b[3;4H\x1b[0c\x1b[6n")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return bytes.IndexByte(buf, 'R') >= 0 && bytes.IndexByte(buf, 'c') >= 0
	})

	da1Idx := bytes.Index([]byte(reply), []byte("\x1b[?1;2c"))
	cprIdx := bytes.Index([]byte(reply), []byte("\x1b[3;4R"))
	if da1Idx < 0 || cprIdx < 0 {
		t.Fatalf("expected both DA1 and CPR replies; got %q", reply)
	}
	if da1Idx > cprIdx {
		t.Fatalf("DA1 was asked first but answered second; got %q", reply)
	}
}

func readReplyUntil(t *testing.T, f *os.File, timeout time.Duration, done func([]byte) bool) string {
	t.Helper()
	_ = f.SetReadDeadline(time.Now().Add(timeout))
	var out []byte
	buf := make([]byte, 256)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if done(out) {
				return string(out)
			}
		}
		if err != nil {
			return string(out)
		}
	}
}
