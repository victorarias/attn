// Package probetui renders deterministic byte-for-byte terminal output that
// mirrors the VT vocabulary of a real coding agent's TUI (codex-cli or
// claude), without running the real agent. It backs the `attn _probe-tui`
// harness fixture: a fake "agent" the packaged-app harness can launch that
// exercises the same private-mode sets, cursor addressing, and query
// sequences a real agent does, so terminal-handling regressions can be
// caught without a live model in the loop.
//
// Renderers are pure functions of (style, geometry, seq): no wall-clock
// time, randomness, or PID leaks into the output, so a captured transcript
// is reproducible.
package probetui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// Style selects which real agent's VT vocabulary to mirror.
type Style string

const (
	StyleCodex  Style = "codex"
	StyleClaude Style = "claude"
)

// ParseStyle parses a --style flag value.
func ParseStyle(s string) (Style, error) {
	switch Style(s) {
	case StyleCodex:
		return StyleCodex, nil
	case StyleClaude:
		return StyleClaude, nil
	default:
		return "", fmt.Errorf("probetui: unknown style %q (want %q or %q)", s, StyleCodex, StyleClaude)
	}
}

// Startup returns the byte sequence the probe emits once on start, before
// the first frame: mode sets and (per style) terminal queries.
func Startup(style Style, cols, rows int) []byte {
	switch style {
	case StyleCodex:
		return startupCodex(cols, rows)
	case StyleClaude:
		return startupClaude(cols, rows)
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

// Frame returns one full deterministic repaint for the given geometry and
// monotonically increasing seq. Content depends only on (style, cols, rows,
// seq).
func Frame(style Style, cols, rows int, seq int) []byte {
	switch style {
	case StyleCodex:
		return frameCodex(cols, rows, seq)
	case StyleClaude:
		return frameClaude(cols, rows, seq)
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

// OnResize returns the bytes emitted in response to a size change, BEFORE
// the next Frame at the new geometry.
func OnResize(style Style, cols, rows int) []byte {
	switch style {
	case StyleCodex:
		return onResizeCodex(cols, rows)
	case StyleClaude:
		return onResizeClaude(cols, rows)
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

// Teardown returns the graceful shutdown sequence (mode resets; claude
// style exits the alternate screen).
func Teardown(style Style) []byte {
	switch style {
	case StyleCodex:
		return teardownCodex()
	case StyleClaude:
		return teardownClaude()
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

// Run drives the probe against w until ctx is done or a resize arrives on
// winch: Startup, then a Frame every interval and on each resize (via
// OnResize followed by a Frame at the new geometry), then Teardown on
// ctx.Done. size returns the current terminal geometry. Run never reads
// input.
func Run(
	ctx context.Context,
	w io.Writer,
	style Style,
	size func() (cols int, rows int, err error),
	winch <-chan os.Signal,
	interval time.Duration,
) error {
	cols, rows, err := size()
	if err != nil {
		return fmt.Errorf("probetui: initial size: %w", err)
	}

	if _, err := w.Write(Startup(style, cols, rows)); err != nil {
		return err
	}

	seq := 0
	writeFrame := func() error {
		seq++
		_, err := w.Write(Frame(style, cols, rows, seq))
		return err
	}
	if err := writeFrame(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_, err := w.Write(Teardown(style))
			return err

		case <-winch:
			newCols, newRows, err := size()
			if err != nil {
				continue
			}
			if newCols == cols && newRows == rows {
				continue
			}
			cols, rows = newCols, newRows
			if _, err := w.Write(OnResize(style, cols, rows)); err != nil {
				return err
			}
			if err := writeFrame(); err != nil {
				return err
			}

		case <-ticker.C:
			if err := writeFrame(); err != nil {
				return err
			}
		}
	}
}

// --- shared VT construction helpers ----------------------------------------

func csi(params string, final byte) []byte {
	return []byte("\x1b[" + params + string(final))
}

func privateMode(set bool, modes ...string) []byte {
	final := byte('l')
	if set {
		final = 'h'
	}
	params := "?"
	for i, m := range modes {
		if i > 0 {
			params += ";"
		}
		params += m
	}
	return csi(params, final)
}

func cup(row, col int) []byte { return csi(fmt.Sprintf("%d;%d", row, col), 'H') }
func el() []byte              { return csi("", 'K') }
func ed(n int) []byte {
	if n == 0 {
		return csi("", 'J')
	}
	return csi(fmt.Sprintf("%d", n), 'J')
}
func sgr(params string) []byte { return csi(params, 'm') }
func decstbm(top, bottom int) []byte {
	if top == 0 && bottom == 0 {
		return csi("", 'r')
	}
	return csi(fmt.Sprintf("%d;%d", top, bottom), 'r')
}
func da1Query() []byte { return csi("", 'c') }
func cprQuery() []byte { return csi("6", 'n') }

func osc(code, data string) []byte {
	return []byte("\x1b]" + code + ";" + data + "\x1b\\")
}

func oscColorQuery(code string) []byte { return osc(code, "?") }

func hyperlink(url, text string) []byte {
	var b bytes.Buffer
	b.WriteString("\x1b]8;;" + url + "\x1b\\")
	b.WriteString(text)
	b.WriteString("\x1b]8;;\x1b\\")
	return b.Bytes()
}

// bannerGeometryRow and bannerStyleRow are the two fixed-shape rows both
// styles' Frame must paint exactly once, at rows 1 and 2 respectively.
// Callers truncate each to cols when painting. Split across two rows (was
// one) so the identifying content survives narrow (~20 col) panes where a
// single combined banner line clipped past recognition.
func bannerGeometryRow(cols, rows int) string {
	return fmt.Sprintf("ATTN-PROBE %dx%d", cols, rows)
}

func bannerStyleRow(style Style, seq int) string {
	return fmt.Sprintf("style=%s seq=%d READY", style, seq)
}

// fillRow is a deterministic fill pattern for a non-banner row, a function
// only of (cols, rows, seq, row).
func fillRow(cols, rows, seq, row int) string {
	ch := byte('#')
	if (row+seq+rows)%2 == 1 {
		ch = '.'
	}
	if cols <= 0 {
		return ""
	}
	b := make([]byte, cols)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

// truncateToWidth clamps s to at most cols visible columns, assuming
// single-width ASCII content (the only content probetui renders).
func truncateToWidth(s string, cols int) string {
	if cols <= 0 {
		return ""
	}
	if len(s) <= cols {
		return s
	}
	return s[:cols]
}
