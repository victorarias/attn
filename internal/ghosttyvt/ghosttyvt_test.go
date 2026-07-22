//go:build darwin && arm64

package ghosttyvt

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// newT is a test helper that creates a Terminal and registers cleanup.
func newT(t *testing.T, cols, rows int) *Terminal {
	t.Helper()
	term, err := New(cols, rows, Options{})
	if err != nil {
		t.Fatalf("New(%d,%d): %v", cols, rows, err)
	}
	t.Cleanup(term.Close)
	return term
}

// styledCorpus produces a byte stream with scrollback, SGR styling, a soft-wrap,
// a hyperlink, and a trailing prompt — a representative session slice.
func styledCorpus() []byte {
	var b bytes.Buffer
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "\x1b[3%dmline-%03d\x1b[0m plain tail\r\n", i%7+1, i)
	}
	b.WriteString("\x1b[1;4;35mSTYLED-BOLD-UNDER-MAGENTA\x1b[0m\r\n")
	b.WriteString(strings.Repeat("wrapme-", 30)) // 210 chars > 80 cols → soft wraps
	b.WriteString("\r\n")
	b.WriteString("\x1b]8;;https://example.com\x1b\\hyperlinked\x1b]8;;\x1b\\\r\n")
	b.WriteString("final-prompt$ ")
	return b.Bytes()
}

// TestRoundTripPlainText is the core correctness invariant: feed terminal A,
// serialize it, replay the dump into a fresh terminal B, and assert A and B
// render identical plain text (viewport + scrollback).
func TestRoundTripPlainText(t *testing.T) {
	a := newT(t, 80, 10)
	a.Write(styledCorpus())

	snap := a.Serialize()
	if len(snap.VTDump) == 0 {
		t.Fatal("empty VT dump")
	}

	b := newT(t, snap.Cols, snap.Rows)
	b.Write(snap.VTDump)

	plainA, plainB := a.PlainText(), b.PlainText()
	if plainA != plainB {
		t.Errorf("round-trip plain text mismatch\n A=%q\n B=%q", plainA, plainB)
	}
	if !strings.Contains(plainA, "line-001") {
		t.Errorf("scrollback lost: line-001 not in plain text %q", firstN(plainA, 120))
	}
	if !strings.Contains(plainA, "final-prompt$") {
		t.Errorf("viewport lost: prompt not present")
	}
}

// TestRoundTripCursor asserts the restored cursor lands at the same position —
// this is what the trailing-CUP fix in serializeLocked guarantees.
func TestRoundTripCursor(t *testing.T) {
	a := newT(t, 80, 10)
	// Land the cursor at a known spot that is NOT a tabstop column, so the
	// upstream cursor/tabstop ordering bug would move it if uncorrected.
	a.Write([]byte("hello\r\nworld\x1b[3;7H"))
	ax, ay := a.cursorXY()

	b := newT(t, 80, 10)
	b.Write(a.Serialize().VTDump)
	bx, by := b.cursorXY()

	if ax != bx || ay != by {
		t.Errorf("cursor position mismatch after restore: A=(%d,%d) B=(%d,%d)", ax, ay, bx, by)
	}
}

// TestReflowAfterRestore feeds a soft-wrapped long line, restores into B, then
// resizes B narrower and asserts the content reflows (soft-wrap survived the
// dump). It compares against terminal C which consumed the raw bytes then
// resized — B and C must render identically.
func TestReflowAfterRestore(t *testing.T) {
	raw := []byte(strings.Repeat("wrapme-", 30) + "\r\n") // 210 chars
	a := newT(t, 80, 10)
	a.Write(raw)

	b := newT(t, 80, 10)
	b.Write(a.Serialize().VTDump)
	b.Resize(40, 10)

	c := newT(t, 80, 10)
	c.Write(raw)
	c.Resize(40, 10)

	if got, want := b.PlainText(), c.PlainText(); got != want {
		t.Errorf("reflow mismatch after resize\n restored=%q\n direct=%q", got, want)
	}
	// The wrapped payload must remain contiguous when newlines are stripped.
	flat := strings.ReplaceAll(b.PlainText(), "\n", "")
	if !strings.Contains(flat, "wrapme-wrapme-wrapme") {
		t.Errorf("reflow corrupted long line: %q", firstN(flat, 120))
	}
}

// TestRoundTripAltScreen is the alt-screen fidelity invariant: when the
// alternate screen is active, the serialized dump must carry BOTH screens — the
// alt frame (visible now) and the primary screen (scrollback + prompt) hidden
// behind it — so leaving the alt screen after a restore reveals the original
// primary content. The plain terminal formatter serializes only the active
// screen, so without the alt-aware serializer a restored terminal shows a blank
// shell on 1049l. Phase 3 deletes the raw-replay fallback, so this path must be
// correct in snapshot form.
func TestRoundTripAltScreen(t *testing.T) {
	a := newT(t, 80, 10)
	a.Write(styledCorpus())        // primary: scrollback + "final-prompt$ "
	a.Write([]byte("\x1b[?1049h")) // enter alt screen (vim/less/TUI)
	a.Write([]byte("\x1b[2J\x1b[HVIM-EDITOR-SCREEN\r\n~\r\n~"))

	snap := a.Serialize()
	if len(snap.VTDump) == 0 {
		t.Fatal("empty VT dump")
	}

	b := newT(t, snap.Cols, snap.Rows)
	b.Write(snap.VTDump)

	// While alt is active, B must match A and show the alt frame — not the
	// primary content behind it.
	if got, want := b.PlainText(), a.PlainText(); got != want {
		t.Errorf("alt-active round-trip mismatch\n A=%q\n B=%q", firstN(want, 200), firstN(got, 200))
	}
	if !strings.Contains(b.PlainText(), "VIM-EDITOR-SCREEN") {
		t.Errorf("alt frame lost after restore: %q", firstN(b.PlainText(), 200))
	}
	if strings.Contains(b.PlainText(), "final-prompt$") {
		t.Errorf("primary content leaked into alt viewport: %q", firstN(b.PlainText(), 200))
	}

	// Leaving the alt screen must reveal the restored primary screen: scrollback
	// and prompt, NOT a blank shell.
	a.Write([]byte("\x1b[?1049l"))
	b.Write([]byte("\x1b[?1049l"))
	if got, want := b.PlainText(), a.PlainText(); got != want {
		t.Errorf("primary round-trip mismatch after leaving alt\n A=%q\n B=%q", firstN(want, 240), firstN(got, 240))
	}
	if !strings.Contains(b.PlainText(), "final-prompt$") {
		t.Errorf("primary prompt lost after leaving alt: %q", firstN(b.PlainText(), 240))
	}
	if !strings.Contains(b.PlainText(), "line-001") {
		t.Errorf("primary scrollback lost after leaving alt: %q", firstN(b.PlainText(), 240))
	}
	if strings.Contains(b.PlainText(), "VIM-EDITOR-SCREEN") {
		t.Errorf("alt content leaked into primary after leaving alt: %q", firstN(b.PlainText(), 240))
	}
}

// TestQueryResponses asserts the terminal answers the codex bootstrap query
// trio (CPR, DA1, kitty CSI ? u) via the write_pty callback, drained through
// DrainResponses.
func TestQueryResponses(t *testing.T) {
	term := newT(t, 80, 10)
	term.Write([]byte("\x1b[6n\x1b[c\x1b[?u"))
	resp := string(term.DrainResponses())
	if resp == "" {
		t.Fatal("no query responses drained")
	}
	// CPR → CSI row;col R; DA1 → CSI ? … c; kitty → CSI ? <flags> u.
	if !strings.Contains(resp, "R") {
		t.Errorf("CPR not answered: %q", resp)
	}
	if !strings.Contains(resp, "c") {
		t.Errorf("DA1 not answered: %q", resp)
	}
	if !strings.Contains(resp, "u") {
		t.Errorf("kitty CSI ? u not answered: %q", resp)
	}
	// Draining again yields nothing.
	if extra := term.DrainResponses(); extra != nil {
		t.Errorf("responses not cleared on drain: %q", string(extra))
	}
}

// TestMalformedInputSafe feeds hostile byte soup and asserts no crash and a
// still-usable terminal.
func TestMalformedInputSafe(t *testing.T) {
	term := newT(t, 80, 10)
	garbage := []byte("\x1b[999;999H\x1b[?xyz\x1b]999;bad\x07\xff\xfe\x1b[38;5;m\x1bP+q\x1b\\partial\x1b[")
	term.Write(garbage)
	// The garbage ends mid-CSI on purpose; a leading ESC in the next write
	// aborts the dangling sequence (ESC always restarts an escape). This
	// asserts the parser recovers and the terminal stays usable.
	term.Write([]byte("\x1b[0mrecovered\r\n"))
	if !strings.Contains(term.PlainText(), "recovered") {
		t.Errorf("terminal unusable after garbage input")
	}
}

// TestDumpHasNoInterrogativeSequences asserts a serialized dump never contains
// queries or OSC 52 clipboard writes — bytes that would affect the host if the
// client model tried to answer them. (Phase 2 hardens this further.)
func TestDumpHasNoInterrogativeSequences(t *testing.T) {
	term := newT(t, 80, 10)
	term.Write(styledCorpus())
	// Provoke query state without leaving queries in the grid.
	term.Write([]byte("\x1b[6n\x1b[c"))
	dump := term.Serialize().VTDump

	banned := map[string][]byte{
		"CPR request (ESC[6n)":   []byte("\x1b[6n"),
		"DA1 request (ESC[c)":    []byte("\x1b[c"),
		"OSC 52 clipboard":       []byte("\x1b]52;"),
		"DA1 secondary (ESC[>c)": []byte("\x1b[>c"),
		"XTVERSION (ESC[>q)":     []byte("\x1b[>q"),
		"kitty query (ESC[?u)":   []byte("\x1b[?u"),
	}
	for name, seq := range banned {
		if bytes.Contains(dump, seq) {
			t.Errorf("serialized dump contains %s", name)
		}
	}
}

// TestCloseIdempotent asserts Close can be called multiple times safely.
func TestCloseIdempotent(t *testing.T) {
	term, err := New(20, 5, Options{})
	if err != nil {
		t.Fatal(err)
	}
	term.Write([]byte("hi"))
	term.Close()
	term.Close() // must not panic or double-free
	// Post-close methods must be safe no-ops.
	term.Write([]byte("ignored"))
	if term.PlainText() != "" {
		t.Errorf("PlainText after close should be empty")
	}
}

// cursorXY reads the native cursor position (0-indexed) for tests.
func (t *Terminal) cursorXY() (x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cursorXYLocked()
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
