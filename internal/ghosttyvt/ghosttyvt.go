//go:build darwin && arm64

// Package ghosttyvt is the worker-side owner of an authoritative parsed
// terminal, backed by libghostty-vt (Ghostty's VT core) via cgo.
//
// It is the ONLY package that may include vt.h or touch native handles. It
// exists so a client attach can be served by serializing the parsed grid +
// scrollback (the tmux/zellij model) instead of replaying the app's raw byte
// stream. Live output keeps streaming as raw bytes; only the attach/restore
// path uses this package.
//
// The static library and headers under third_party/ghostty-vt/ are produced by
// scripts/build-libghostty-vt.sh (source of truth; gitignored output). See
// docs/plans/2026-07-22-server-authoritative-terminal.md.
package ghosttyvt

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/ghostty-vt/include
#cgo LDFLAGS: ${SRCDIR}/../../third_party/ghostty-vt/lib/libghostty-vt.a
#cgo LDFLAGS: -framework CoreFoundation -framework CoreText -framework CoreGraphics -framework Foundation
#include <stdlib.h>
#include <string.h>
#include <ghostty/vt.h>

// Implemented in callback.go; the terminal invokes it synchronously during
// vt_write with query-response bytes (CPR, DA1, kitty CSI ? u, DECRQM…).
extern void goWritePty(GhosttyTerminal term, void* userdata, const uint8_t* data, size_t len);

// Install the userdata pointer + write_pty callback in one shot. userdata is the
// address of the sink's cgo.Handle field. ghostty_terminal_set retains it past
// this call, so the caller pins that address with a runtime.Pinner (held until
// after ghostty_terminal_free) — the supported way for C to legally retain a Go
// pointer. The callback dereferences it back to a cgo.Handle.
static GhosttyResult ghosttyvt_install(GhosttyTerminal t, void* userdata) {
	GhosttyResult rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_USERDATA, userdata);
	if (rc != GHOSTTY_SUCCESS) return rc;
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_WRITE_PTY, (const void*)goWritePty);
}

// Build formatter options: one self-contained VT (or plain) stream with all
// "extra" state on and unwrap=false so soft-wrap survives the dump. NULL
// selection = the entire screen including scrollback history.
static GhosttyFormatterTerminalOptions ghosttyvt_make_opts(GhosttyFormatterFormat emit) {
	GhosttyFormatterTerminalOptions o;
	memset(&o, 0, sizeof(o));
	o.size = sizeof(GhosttyFormatterTerminalOptions);
	o.emit = emit;
	o.unwrap = false;
	o.trim = false;
	o.extra.size = sizeof(GhosttyFormatterTerminalExtra);
	o.extra.palette = true;
	o.extra.modes = true;
	o.extra.scrolling_region = true;
	o.extra.tabstops = true;
	o.extra.pwd = true;
	o.extra.keyboard = true;
	o.extra.screen.size = sizeof(GhosttyFormatterScreenExtra);
	o.extra.screen.cursor = true;
	o.extra.screen.style = true;
	o.extra.screen.hyperlink = true;
	o.extra.screen.protection = true;
	o.extra.screen.kitty_keyboard = true;
	o.extra.screen.charsets = true;
	o.selection = NULL;
	return o;
}

static uint16_t ghosttyvt_get_u16(GhosttyTerminal t, GhosttyTerminalData data) {
	uint16_t v = 0;
	ghostty_terminal_get(t, data, &v);
	return v;
}

static int ghosttyvt_active_screen(GhosttyTerminal t) {
	GhosttyTerminalScreen s = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &s);
	return (int)s;
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"unsafe"
)

// Nominal cell pixel size. We never render, but resize + XTWINOPS size reports
// need non-zero pixel dimensions. The exact values are immaterial to grid
// reflow; they only scale pixel-unit size reports.
const (
	cellWidthPx  = 8
	cellHeightPx = 16
)

// DefaultMaxScrollback is the scrollback cap (lines). ~0.8MB RSS measured for a
// 10k-line 200x50 scrollback (see plan Phase 0 notes).
const DefaultMaxScrollback = 10000

// Options configures a new Terminal.
type Options struct {
	// MaxScrollback caps retained scrollback lines. Zero uses DefaultMaxScrollback.
	MaxScrollback int
}

// Snapshot is a self-contained serialization of a Terminal's full state,
// suitable for reconstructing an identical terminal on a client.
type Snapshot struct {
	Cols, Rows int
	// VTDump is one self-contained VT stream (FORMAT_VT, unwrap=false, all
	// extras, full scrollback) that reproduces the terminal when replayed into
	// a fresh same-size terminal. It carries no interrogative sequences.
	VTDump []byte
}

// respSink accumulates bytes the terminal wants written back to the pty (query
// responses: CPR, DA1, kitty CSI ? u, DECRQM…). It is what the cgo.Handle
// references — deliberately NOT the Terminal — so the handle does not keep the
// Terminal reachable, and the Terminal's finalizer can still reclaim a leaked
// one. Its own mutex guards buf; goWritePty appends (synchronously during Write)
// and DrainResponses reads+clears, independent of the Terminal's mutex.
//
// handle references this sink; C retains &handle as its userdata (see New).
type respSink struct {
	mu     sync.Mutex
	buf    []byte
	handle cgo.Handle
}

// Terminal wraps a native libghostty-vt terminal. All methods are safe for
// concurrent use; each serializes on the Terminal's mutex.
//
// The caller MUST call Close exactly once when done; a finalizer is a backstop,
// not a substitute (native memory + a cgo.Handle leak otherwise).
type Terminal struct {
	mu     sync.Mutex
	term   C.GhosttyTerminal
	sink   *respSink      // referenced by sink.handle; holds query-response bytes
	pinner runtime.Pinner // pins &sink.handle for C to retain as userdata
	cols   int
	rows   int

	closed bool
}

// New creates a Terminal of the given size. cols and rows must be > 0.
func New(cols, rows int, opts Options) (*Terminal, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("ghosttyvt: invalid size %dx%d", cols, rows)
	}
	maxSB := opts.MaxScrollback
	if maxSB <= 0 {
		maxSB = DefaultMaxScrollback
	}
	t := &Terminal{cols: cols, rows: rows, sink: &respSink{}}
	copts := C.GhosttyTerminalOptions{
		cols:           C.uint16_t(cols),
		rows:           C.uint16_t(rows),
		max_scrollback: C.size_t(maxSB),
	}
	if rc := C.ghostty_terminal_new(nil, &t.term, copts); rc != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("ghosttyvt: terminal_new failed: rc=%d", int(rc))
	}
	// The handle references the sink (not t) so it does not pin the Terminal.
	// C retains the userdata past this call, so give it the *address* of the
	// sink's handle field and pin that address: a runtime.Pinner is the
	// supported way for C to legally hold a Go pointer. Unpinned in Close, after
	// ghostty_terminal_free.
	t.sink.handle = cgo.NewHandle(t.sink)
	t.pinner.Pin(&t.sink.handle)
	if rc := C.ghosttyvt_install(t.term, unsafe.Pointer(&t.sink.handle)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		t.pinner.Unpin()
		t.sink.handle.Delete()
		return nil, fmt.Errorf("ghosttyvt: install callbacks failed: rc=%d", int(rc))
	}
	runtime.SetFinalizer(t, (*Terminal).finalize)
	return t, nil
}

// Write feeds raw PTY bytes through the terminal's parser. It never fails;
// malformed input is safe by design. Query responses produced during the write
// are appended to the response buffer (see DrainResponses).
func (t *Terminal) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	C.ghostty_terminal_vt_write(t.term, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))
}

// Resize changes the terminal dimensions; the primary screen reflows when
// wraparound is enabled.
func (t *Terminal) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	C.ghostty_terminal_resize(t.term, C.uint16_t(cols), C.uint16_t(rows), cellWidthPx, cellHeightPx)
	t.cols, t.rows = cols, rows
}

// DrainResponses returns and clears the bytes the terminal wants written back
// to the pty (query responses accumulated since the last drain). It reads the
// sink under the sink's own lock, independent of the Terminal mutex.
func (t *Terminal) DrainResponses() []byte {
	s := t.sink
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 {
		return nil
	}
	out := s.buf
	s.buf = nil
	return out
}

// Size returns the current terminal dimensions.
func (t *Terminal) Size() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

// PlainText renders the terminal (viewport + scrollback) as plain UTF-8 text,
// no escape sequences. Primarily for tests and shadow-mode divergence checks.
func (t *Terminal) PlainText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	return string(t.format(C.GHOSTTY_FORMATTER_FORMAT_PLAIN))
}

// Serialize produces a Snapshot: one self-contained VT stream that reproduces
// the terminal state (grid + scrollback + modes + cursor) when replayed into a
// fresh same-size terminal.
func (t *Terminal) Serialize() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.serializeLocked()
}

// serializeLocked assumes t.mu is held. Exposed for callers that must capture a
// snapshot atomically with an external watermark (e.g. the read-loop seq).
func (t *Terminal) serializeLocked() Snapshot {
	if t.closed {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}
	dump := t.serializeVTLocked()

	// Upstream ordering bug: the VT dump emits the cursor CUP before tabstop
	// resets, and setting tabstops moves the cursor — so without a corrective
	// CUP the restored cursor lands on the last tabstop column. Append the true
	// cursor position last. (0-indexed native coords → 1-based CUP.)
	cx, cy := t.cursorXYLocked()
	dump = fmt.Appendf(dump, "\x1b[%d;%dH", cy+1, cx+1)

	return Snapshot{
		Cols:   t.cols,
		Rows:   t.rows,
		VTDump: dump,
	}
}

// serializeVTLocked returns the alt-screen-aware VT serialization of the whole
// terminal (primary + alternate screens, scrollback, modes, cursor) via the
// carried ghostty_terminal_serialize_vt patch. When the alternate screen is
// active it emits the primary screen (scrollback + frame), then ?1049h, then
// the alt frame — so a restored terminal keeps its primary content behind an
// alt-screen app. Caller holds t.mu and must not call after Close.
func (t *Terminal) serializeVTLocked() []byte {
	var ptr *C.uint8_t
	var n C.size_t
	if rc := C.ghostty_terminal_serialize_vt(nil, t.term, &ptr, &n); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer C.ghostty_free(nil, ptr, n)
	if n == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(ptr), C.int(n))
}

// cursorXYLocked returns the native cursor position (0-indexed). Caller holds t.mu.
func (t *Terminal) cursorXYLocked() (x, y int) {
	return int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_CURSOR_X)),
		int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_CURSOR_Y))
}

// AltScreenActive reports whether the alternate screen (DEC 1049/1047/47) is
// currently active. Blocks are a primary-screen concept: the block table
// records this at pin time and excludes alt-pinned blocks at serialize.
func (t *Terminal) AltScreenActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	return C.ghosttyvt_active_screen(t.term) == C.int(C.GHOSTTY_TERMINAL_SCREEN_ALTERNATE)
}

// format runs the upstream formatter and returns freshly-allocated Go bytes.
// Caller must hold t.mu and must not call it after Close.
func (t *Terminal) format(emit C.GhosttyFormatterFormat) []byte {
	var f C.GhosttyFormatter
	opts := C.ghosttyvt_make_opts(emit)
	if rc := C.ghostty_formatter_terminal_new(nil, &f, t.term, opts); rc != C.GHOSTTY_SUCCESS {
		// Formatter construction should not fail for a live terminal; return
		// empty rather than panicking a worker over a snapshot.
		return nil
	}
	defer C.ghostty_formatter_free(f)
	var ptr *C.uint8_t
	var n C.size_t
	if rc := C.ghostty_formatter_format_alloc(f, nil, &ptr, &n); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer C.ghostty_free(nil, ptr, n)
	if n == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(ptr), C.int(n))
}

// Close frees the native terminal and releases the cgo.Handle. Idempotent.
func (t *Terminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	C.ghostty_terminal_free(t.term)
	t.term = nil
	// Unpin only after the native terminal is gone (it can no longer read the
	// userdata), then release the handle.
	t.pinner.Unpin()
	t.sink.handle.Delete()
	runtime.SetFinalizer(t, nil)
}

// finalize is the SetFinalizer backstop. It must not depend on t.mu being
// uncontended; Close handles the real work and is idempotent.
func (t *Terminal) finalize() {
	t.Close()
}
