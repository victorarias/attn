//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

// Package ghosttyvt is the worker-side authoritative parsed terminal, backed
// by libghostty-vt via cgo; attach/restore is served by serializing it. It is
// the ONLY package that may include vt.h or touch native handles. Links on
// darwin/arm64 and linux/amd64+arm64; every other target compiles the pure-Go
// stub (stub.go). Archives: third_party/ghostty-vt/<goos>_<goarch>/ from
// scripts/build-libghostty-vt.sh. See docs/plans/2026-07-22-server-authoritative-terminal.md.
package ghosttyvt

/*
// Per-platform archive + headers (download-first; see the build script). The
// four macOS frameworks are darwin-only over-linking Ghostty's build pulls in;
// the Linux targets link only the self-contained .a (its C/C++ runtime deps
// from simdutf/highway are statically baked in) plus libc/libm/libpthread.
#cgo darwin,arm64 CFLAGS: -I${SRCDIR}/../../third_party/ghostty-vt/darwin_arm64/include
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../third_party/ghostty-vt/darwin_arm64/lib/libghostty-vt.a
#cgo darwin,arm64 LDFLAGS: -framework CoreFoundation -framework CoreText -framework CoreGraphics -framework Foundation
#cgo linux,amd64 CFLAGS: -I${SRCDIR}/../../third_party/ghostty-vt/linux_amd64/include
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/../../third_party/ghostty-vt/linux_amd64/lib/libghostty-vt.a
#cgo linux,arm64 CFLAGS: -I${SRCDIR}/../../third_party/ghostty-vt/linux_arm64/include
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/../../third_party/ghostty-vt/linux_arm64/lib/libghostty-vt.a
#cgo linux LDFLAGS: -lm -lpthread
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

// Set the kitty image storage limit. ghostty_terminal_set reads the value
// synchronously, so the caller's stack local need not outlive the call.
static GhosttyResult ghosttyvt_set_kitty_limit(GhosttyTerminal t, uint64_t v) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_STORAGE_LIMIT, &v);
}

// Scrollback is a construction-time concern for us but a post-construction
// option upstream, so it is set immediately after ghostty_terminal_new.
static GhosttyResult ghosttyvt_set_max_scrollback(GhosttyTerminal t, uint64_t v) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &v);
}

// Retain the bytes of an unfinished sequence so a snapshot taken mid-sequence
// can restore the parser. Tracking is off by default, and encoding a terminal
// whose parser is not at ground fails without it.
static GhosttyResult ghosttyvt_set_continuation(GhosttyTerminal t, size_t v) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES, &v);
}

static GhosttyResult ghosttyvt_snapshot_encode(GhosttyTerminal t, uint8_t **ptr, size_t *n) {
	return ghostty_snapshot_encode_alloc(t, NULL, ptr, n);
}

// Decode a whole snapshot — READY prefix and every history page — into a fresh
// terminal. The decoder borrows the source bytes, so it is freed before this
// returns and the caller's buffer need not outlive the call.
static GhosttyResult ghosttyvt_snapshot_decode(const uint8_t *ptr, size_t n, GhosttyTerminal *out) {
	GhosttySnapshotDecoder d;
	GhosttyResult rc = ghostty_snapshot_decoder_new_buf(NULL, &d, ptr, n);
	if (rc != GHOSTTY_SUCCESS) return rc;
	rc = ghostty_snapshot_decoder_decode(d, out);
	ghostty_snapshot_decoder_free(d);
	return rc;
}

static GhosttyColorRgb ghosttyvt_rgb(uint32_t value) {
	GhosttyColorRgb color = {
		.r = (uint8_t)(value >> 16),
		.g = (uint8_t)(value >> 8),
		.b = (uint8_t)value,
	};
	return color;
}

// Set embedder defaults while preserving any program-owned OSC overrides.
// Ghostty's palette setter takes all 256 entries, so begin with its current
// default palette and replace only the 16 ANSI colors attn configures.
static GhosttyResult ghosttyvt_set_color_theme(
	GhosttyTerminal t,
	bool has_foreground, uint32_t foreground,
	bool has_background, uint32_t background,
	bool has_cursor, uint32_t cursor,
	bool has_ansi_palette, const uint32_t* ansi_palette
) {
	GhosttyResult rc;
	if (has_foreground) {
		GhosttyColorRgb color = ghosttyvt_rgb(foreground);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &color);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	if (has_background) {
		GhosttyColorRgb color = ghosttyvt_rgb(background);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &color);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	if (has_cursor) {
		GhosttyColorRgb color = ghosttyvt_rgb(cursor);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, &color);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	if (has_ansi_palette) {
		GhosttyColorRgb palette[256];
		rc = ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_COLOR_PALETTE_DEFAULT, palette);
		if (rc != GHOSTTY_SUCCESS) return rc;
		for (size_t i = 0; i < 16; i++) palette[i] = ghosttyvt_rgb(ansi_palette[i]);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, palette);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	return GHOSTTY_SUCCESS;
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

// Modes are read through the terminal's data query: `mode` goes in, `value`
// comes back. False on a failed read is the safe answer everywhere it is used
// below, so the failure is folded into the helper.
static bool ghosttyvt_mode(GhosttyTerminal t, GhosttyMode mode) {
	GhosttyTerminalModeConfig cfg;
	memset(&cfg, 0, sizeof(cfg));
	cfg.mode = mode;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_MODE, &cfg) != GHOSTTY_SUCCESS) return false;
	return cfg.value;
}

static bool ghosttyvt_cursor_visible(GhosttyTerminal t) {
	return ghosttyvt_mode(t, GHOSTTY_MODE_CURSOR_VISIBLE);
}

// DEC wraparound (mode 7, DECAWM). False on a failed read is the safe answer:
// the caller then resizes plainly instead of toggling a mode it could not read,
// so a failure can never leave wraparound enabled behind the program's back.
static bool ghosttyvt_wraparound(GhosttyTerminal t) {
	return ghosttyvt_mode(t, GHOSTTY_MODE_WRAPAROUND);
}

// Left/right margin mode (DECLRMM, DEC private mode 69). False on a failed read
// leaves the caller trusting its own scroll measurement, which is what a
// terminal without margins earns anyway.
static bool ghosttyvt_left_right_margin_mode(GhosttyTerminal t) {
	return ghosttyvt_mode(t, GHOSTTY_MODE_LEFT_RIGHT_MARGIN);
}

static GhosttyPoint ghosttyvt_viewport_point(uint16_t x, uint32_t y) {
	GhosttyPoint p;
	memset(&p, 0, sizeof(p));
	p.tag = GHOSTTY_POINT_TAG_VIEWPORT;
	p.value.coordinate.x = x;
	p.value.coordinate.y = y;
	return p;
}

static GhosttyResult ghosttyvt_format_viewport(
	GhosttyTerminal t,
	uint16_t cols,
	uint16_t rows,
	GhosttyFormatterFormat emit,
	uint8_t** out_ptr,
	size_t* out_len
) {
	GhosttyGridRef start;
	GhosttyGridRef end;
	GhosttySelection selection;
	GhosttyFormatter formatter;
	GhosttyFormatterTerminalOptions opts;
	GhosttyResult rc;

	if (cols == 0 || rows == 0) return GHOSTTY_INVALID_VALUE;
	if ((rc = ghostty_terminal_grid_ref(t, ghosttyvt_viewport_point(0, 0), &start)) != GHOSTTY_SUCCESS) return rc;
	if ((rc = ghostty_terminal_grid_ref(t, ghosttyvt_viewport_point(cols - 1, rows - 1), &end)) != GHOSTTY_SUCCESS) return rc;

	memset(&selection, 0, sizeof(selection));
	selection.size = sizeof(selection);
	selection.start = start;
	selection.end = end;
	selection.rectangle = false;

	opts = ghosttyvt_make_opts(emit);
	opts.selection = &selection;
	if ((rc = ghostty_formatter_terminal_new(NULL, &formatter, t, opts)) != GHOSTTY_SUCCESS) return rc;
	rc = ghostty_formatter_format_alloc(formatter, NULL, out_ptr, out_len);
	ghostty_formatter_free(formatter);
	return rc;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/cgo"
	"strings"
	"sync"
	"unsafe"
)

// Placeholder cell pixel size until a client reports the real one; image
// emitters size output from pixel reports, so it must be replaced.
const (
	defaultCellWidthPx  = 8
	defaultCellHeightPx = 16
)

// DefaultScrollbackBytes is the scrollback cap. Ghostty budgets scrollback in
// bytes, not rows, and stores it in pages: measured at 200 columns, 4MB holds
// 1,955 rows and 16MB holds 9,095, so a row costs roughly 1.8KB and this
// budget is about 4,400 rows of agent output. Resident only once a session has
// actually produced that much; a quiet session holds one page.
//
// It replaced a 10,000 that was written for a rows option and passed to a
// bytes one — ten kilobytes, measured at 289 rows.
const DefaultScrollbackBytes = 8 << 20

// ContinuationMaxBytes bounds the unfinished sequence a snapshot may carry.
// It is ghostty's own kitty APC buffer limit: a longer sequence never reaches
// the parser intact in the first place, so nothing legitimate can sit above
// it, and the snapshot decoder defaults to the same number — a snapshot we
// encode is one an unconfigured decoder accepts. It costs no steady-state
// memory: only the bytes of a sequence currently in flight are retained, and
// the APC handler is holding those same bytes already.
const ContinuationMaxBytes = 65 << 20

// Options configures a new Terminal.
type Options struct {
	// ScrollbackBytes caps retained scrollback in bytes. Zero uses
	// DefaultScrollbackBytes.
	ScrollbackBytes int

	// KittyImageStorageLimit caps kitty image storage in bytes. Zero disables
	// the protocol entirely — deliberate: the library default is 10MB, and a
	// silently-live parser desyncs the grid from the client model.
	KittyImageStorageLimit uint64
}

// Snapshot is a self-contained serialization for reconstructing a terminal on
// a client. Whole-terminal serialization fills Payload; the viewport-only form
// fills VTDump.
type Snapshot struct {
	Cols, Rows int
	// Payload is ghostty's binary snapshot format, decoded into a terminal.
	Payload []byte
	// VTDump replays into a fresh same-size terminal; no interrogative sequences.
	VTDump []byte
}

// respSink accumulates query-response bytes (CPR, DA1, …) headed back to the
// pty. The cgo.Handle references the sink, NOT the Terminal, so the Terminal's
// finalizer still runs; its own mutex guards buf, independent of the Terminal's.
type respSink struct {
	mu     sync.Mutex
	buf    []byte
	handle cgo.Handle
}

// Terminal wraps a native libghostty-vt terminal; all methods serialize on its
// mutex. Caller MUST Close exactly once — the finalizer is a backstop, not a
// substitute (native memory + a cgo.Handle leak otherwise).
type Terminal struct {
	mu     sync.Mutex
	term   C.GhosttyTerminal
	sink   *respSink      // referenced by sink.handle; holds query-response bytes
	pinner runtime.Pinner // pins &sink.handle for C to retain as userdata
	cols   int
	rows   int
	// Cell size in device pixels; every resize re-derives the total pane size.
	cellW int
	cellH int

	closed bool
}

// New creates a Terminal of the given size. cols and rows must be > 0.
func New(cols, rows int, opts Options) (*Terminal, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("ghosttyvt: invalid size %dx%d", cols, rows)
	}
	maxSB := opts.ScrollbackBytes
	if maxSB <= 0 {
		maxSB = DefaultScrollbackBytes
	}
	// Process-global, idempotent; without it ghostty rejects every PNG (f=100).
	installPNGDecoder()
	t := &Terminal{
		cols:  cols,
		rows:  rows,
		cellW: defaultCellWidthPx,
		cellH: defaultCellHeightPx,
		sink:  &respSink{},
	}
	if rc := C.ghostty_terminal_new(nil, &t.term, C.uint16_t(cols), C.uint16_t(rows)); rc != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("ghosttyvt: terminal_new failed: rc=%d", int(rc))
	}
	return t.configure(maxSB, opts)
}

// Restore decodes a snapshot produced by Serialize into a live Terminal.
//
// The decoder owns the whole record stream in one call: history is small
// enough on this side that staging it costs more than it saves — the browser,
// where the first frame is on the line, decodes READY and prepends history
// after painting.
func Restore(payload []byte, opts Options) (*Terminal, error) {
	if len(payload) == 0 {
		return nil, errors.New("ghosttyvt: empty snapshot")
	}
	maxSB := opts.ScrollbackBytes
	if maxSB <= 0 {
		maxSB = DefaultScrollbackBytes
	}
	installPNGDecoder()
	t := &Terminal{
		cellW: defaultCellWidthPx,
		cellH: defaultCellHeightPx,
		sink:  &respSink{},
	}
	if rc := C.ghosttyvt_snapshot_decode(
		(*C.uint8_t)(unsafe.Pointer(&payload[0])), C.size_t(len(payload)), &t.term,
	); rc != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("ghosttyvt: snapshot decode failed: rc=%d", int(rc))
	}
	t.cols = int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_COLS))
	t.rows = int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_ROWS))
	return t.configure(maxSB, opts)
}

// configure applies our options to a freshly created or decoded terminal and
// installs the response callback. It owns t.term on failure.
func (t *Terminal) configure(maxSB int, opts Options) (*Terminal, error) {
	if rc := C.ghosttyvt_set_max_scrollback(t.term, C.uint64_t(maxSB)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		return nil, fmt.Errorf("ghosttyvt: set max scrollback failed: rc=%d", int(rc))
	}
	// Written even when zero: zero overrides the library's 10MB default.
	if rc := C.ghosttyvt_set_kitty_limit(t.term, C.uint64_t(opts.KittyImageStorageLimit)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		return nil, fmt.Errorf("ghosttyvt: set kitty image storage limit failed: rc=%d", int(rc))
	}
	// Enabled from the first byte: tracking cannot reconstruct a sequence that
	// was already in flight when it was switched on, and a pty chunk boundary
	// lands mid-sequence often enough that an attach would otherwise fail to
	// serialize whenever it did.
	if rc := C.ghosttyvt_set_continuation(t.term, C.size_t(ContinuationMaxBytes)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		return nil, fmt.Errorf("ghosttyvt: set continuation tracking failed: rc=%d", int(rc))
	}
	// C retains the userdata: pin &sink.handle (the supported way for C to hold
	// a Go pointer); unpinned in Close, after ghostty_terminal_free.
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

// Write feeds raw PTY bytes through the parser (malformed input is safe);
// query responses accumulate for DrainResponses.
func (t *Terminal) Write(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeLocked(p)
}

// SetColorTheme replaces the embedder-owned color defaults; ghostty preserves
// program-issued OSC overrides.
func (t *Terminal) SetColorTheme(theme ColorTheme) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	palette := [16]C.uint32_t{}
	for i, color := range theme.ANSIPalette {
		palette[i] = C.uint32_t(color)
	}
	rc := C.ghosttyvt_set_color_theme(
		t.term,
		C.bool(theme.HasForeground), C.uint32_t(theme.Foreground),
		C.bool(theme.HasBackground), C.uint32_t(theme.Background),
		C.bool(theme.HasCursor), C.uint32_t(theme.Cursor),
		C.bool(theme.HasANSIPalette), &palette[0],
	)
	if rc != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("ghosttyvt: set color theme failed: rc=%d", int(rc))
	}
	return nil
}

// Resize changes dimensions; the primary screen reflows when wraparound is on.
func (t *Terminal) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resizeLocked(cols, rows)
}

// DECAWM toggle selecting ghostty's no-reflow resize; same bytes as the client.
var (
	disableWraparound = []byte("\x1b[?7l")
	enableWraparound  = []byte("\x1b[?7h")
)

// ResizeNoReflow resizes on ghostty's no-reflow path by temporarily disabling
// DECAWM, mirroring the client's resizeGhosttyWithoutReflow
// (app/src/utils/ghosttyResize.ts) so worker and client grids stay frame-equal
// — every row-indexed mapping rides on that. Toggle bytes never reach a wire.
func (t *Terminal) ResizeNoReflow(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if !bool(C.ghosttyvt_wraparound(t.term)) {
		t.resizeLocked(cols, rows)
		return
	}
	// Held across all three steps: an interleaved write would parse wraparound-off.
	t.writeLocked(disableWraparound)
	defer t.writeLocked(enableWraparound)
	t.resizeLocked(cols, rows)
}

// writeLocked feeds bytes through the parser. Caller holds t.mu.
func (t *Terminal) writeLocked(p []byte) {
	if len(p) == 0 || t.closed {
		return
	}
	C.ghostty_terminal_vt_write(t.term, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))
}

// SetCellPixelSize sets the cell size in device pixels and pushes it to the
// native terminal immediately — waiting for the next grid resize would leave a
// still window sized from the placeholder. Non-positive dimensions are ignored.
func (t *Terminal) SetCellPixelSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || (t.cellW == w && t.cellH == h) {
		return
	}
	t.cellW, t.cellH = w, h
	t.resizeLocked(t.cols, t.rows)
}

// resizeLocked resizes the native terminal; caller holds t.mu, cols/rows valid.
func (t *Terminal) resizeLocked(cols, rows int) {
	if t.closed {
		return
	}
	C.ghostty_terminal_resize(t.term, C.uint16_t(cols), C.uint16_t(rows), C.uint32_t(t.cellW), C.uint32_t(t.cellH))
	t.cols, t.rows = cols, rows
}

// DrainResponses returns and clears accumulated query-response bytes (sink
// lock only, independent of t.mu).
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

// PlainText renders the terminal (viewport + scrollback) as plain UTF-8 text.
func (t *Terminal) PlainText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	return string(t.format(C.GHOSTTY_FORMATTER_FORMAT_PLAIN))
}

// CursorPos returns the active-screen cursor position (0-indexed viewport).
func (t *Terminal) CursorPos() (x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, 0
	}
	return t.cursorXYLocked()
}

// CursorVisible reports whether the cursor is visible (DECTCEM, mode 25).
func (t *Terminal) CursorVisible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed && bool(C.ghosttyvt_cursor_visible(t.term))
}

// LeftRightMarginMode reports DECLRMM (mode 69); false on a failed read.
func (t *Terminal) LeftRightMarginMode() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed && bool(C.ghosttyvt_left_right_margin_mode(t.term))
}

// ViewportText returns the visible screen as plain text, one \n-terminated
// trimmed line per row; "" only if formatting fails.
func (t *Terminal) ViewportText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	return t.viewportTextLocked()
}

// SerializeViewport returns a self-contained styled VT stream of the visible
// screen only (no scrollback), including cursor position and visibility.
func (t *Terminal) SerializeViewport() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}

	dump, ok := t.formatViewport(C.GHOSTTY_FORMATTER_FORMAT_VT)
	if !ok {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}

	// The formatter emits its cursor CUP before tabstop resets, so append the
	// true position last (0-indexed native coords → 1-based CUP).
	cx, cy := t.cursorXYLocked()
	dump = fmt.Appendf(dump, "\x1b[%d;%dH", cy+1, cx+1)
	if C.ghosttyvt_cursor_visible(t.term) {
		dump = append(dump, "\x1b[?25h"...)
	} else {
		dump = append(dump, "\x1b[?25l"...)
	}
	return Snapshot{Cols: t.cols, Rows: t.rows, VTDump: dump}
}

// Serialize produces a Snapshot of the whole terminal.
func (t *Terminal) Serialize() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.serializeLocked()
}

// serializeLocked assumes t.mu is held; for callers capturing a snapshot
// atomically with an external watermark (e.g. the read-loop seq).
func (t *Terminal) serializeLocked() Snapshot {
	if t.closed {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}
	return Snapshot{
		Cols:    t.cols,
		Rows:    t.rows,
		Payload: t.encodeSnapshotLocked(),
	}
}

// encodeSnapshotLocked serializes the whole terminal — both screens, their
// scrollback, and any unfinished parser input — into ghostty's binary snapshot
// format. Caller holds t.mu, not after Close.
func (t *Terminal) encodeSnapshotLocked() []byte {
	var ptr *C.uint8_t
	var n C.size_t
	if rc := C.ghosttyvt_snapshot_encode(t.term, &ptr, &n); rc != C.GHOSTTY_SUCCESS {
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
// active. Blocks are a primary-screen concept: alt-pinned ones are excluded.
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

// formatViewport formats the visible viewport only; ok is false only on
// failure (a blank viewport is a non-nil empty slice). Caller holds t.mu.
func (t *Terminal) formatViewport(emit C.GhosttyFormatterFormat) ([]byte, bool) {
	var ptr *C.uint8_t
	var n C.size_t
	if rc := C.ghosttyvt_format_viewport(t.term, C.uint16_t(t.cols), C.uint16_t(t.rows), emit, &ptr, &n); rc != C.GHOSTTY_SUCCESS {
		return nil, false
	}
	defer C.ghostty_free(nil, ptr, n)
	if n == 0 {
		return []byte{}, true
	}
	return C.GoBytes(unsafe.Pointer(ptr), C.int(n)), true
}

// viewportTextLocked normalizes formatter text into a stable row shape. Caller holds t.mu.
func (t *Terminal) viewportTextLocked() string {
	raw, ok := t.formatViewport(C.GHOSTTY_FORMATTER_FORMAT_PLAIN)
	if !ok {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out strings.Builder
	for row := 0; row < t.rows; row++ {
		line := ""
		if row < len(lines) {
			line = strings.TrimRight(lines[row], " ")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
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
	// Unpin only after the native terminal can no longer read the userdata.
	t.pinner.Unpin()
	t.sink.handle.Delete()
	runtime.SetFinalizer(t, nil)
}

// finalize is the SetFinalizer backstop; Close is idempotent.
func (t *Terminal) finalize() {
	t.Close()
}
