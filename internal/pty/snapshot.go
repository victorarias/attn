package pty

import (
	"bytes"
	"strconv"
	"sync"

	"github.com/hinshun/vt10x"
)

const (
	// Mirrors vt10x internal glyph mode bits.
	snapshotAttrReverse   int16 = 1 << 0
	snapshotAttrUnderline int16 = 1 << 1
	snapshotAttrBold      int16 = 1 << 2
	snapshotAttrItalic    int16 = 1 << 4
	snapshotAttrBlink     int16 = 1 << 5
)

type screenSnapshot struct {
	payload       []byte
	cols          uint16
	rows          uint16
	cursorX       uint16
	cursorY       uint16
	cursorVisible bool
}

type virtualScreen struct {
	mu    sync.Mutex
	term  vt10x.Terminal
	wrote bool
}

type glyphStyle struct {
	bold      bool
	underline bool
	italic    bool
	blink     bool
	reverse   bool
	fg        vt10x.Color
	bg        vt10x.Color
}

func newVirtualScreen(cols, rows uint16) *virtualScreen {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return &virtualScreen{
		term: vt10x.New(vt10x.WithSize(int(cols), int(rows))),
	}
}

func (v *virtualScreen) Observe(data []byte) {
	if v == nil || len(data) == 0 {
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if _, err := v.term.Write(data); err == nil {
		v.wrote = true
	}
}

func (v *virtualScreen) Resize(cols, rows uint16) {
	if v == nil || cols == 0 || rows == 0 {
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.term.Resize(int(cols), int(rows))
}

func (v *virtualScreen) Snapshot() (screenSnapshot, bool) {
	if v == nil {
		return screenSnapshot{}, false
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.wrote {
		return screenSnapshot{}, false
	}

	view := v.term
	view.Lock()
	defer view.Unlock()

	cols, rows := view.Size()
	if cols <= 0 || rows <= 0 {
		return screenSnapshot{}, false
	}

	cursor := view.Cursor()
	cursorX := clampToRange(cursor.X, cols-1)
	cursorY := clampToRange(cursor.Y, rows-1)
	payload := renderVisibleFrame(view, cols, rows, cursorX, cursorY, view.CursorVisible())
	if len(payload) == 0 {
		return screenSnapshot{}, false
	}

	return screenSnapshot{
		payload:       payload,
		cols:          uint16(cols),
		rows:          uint16(rows),
		cursorX:       uint16(cursorX),
		cursorY:       uint16(cursorY),
		cursorVisible: view.CursorVisible(),
	}, true
}

func renderVisibleFrame(view vt10x.View, cols, rows, cursorX, cursorY int, cursorVisible bool) []byte {
	var out bytes.Buffer
	mode := view.Mode()

	// Paint into a blank screen and restore cursor position/visibility at the end.
	if mode&vt10x.ModeAltScreen != 0 {
		out.WriteString("\x1b[?1049h")
	}
	out.WriteString("\x1b[?25l\x1b[?7l\x1b[0m\x1b[H\x1b[2J")

	for y := 0; y < rows; y++ {
		writeCursorMove(&out, y+1, 1)
		current := glyphStyle{
			fg: vt10x.DefaultFG,
			bg: vt10x.DefaultBG,
		}
		styleSet := false
		lastPaintCol := lastNonDefaultCol(view, y, cols)

		for x := 0; x <= lastPaintCol; x++ {
			cell := view.Cell(x, y)
			next := glyphStyleFromCell(cell)
			if !styleSet || next != current {
				writeStyle(&out, next)
				current = next
				styleSet = true
			}

			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}
			out.WriteRune(ch)
		}
		if lastPaintCol < cols-1 {
			out.WriteString("\x1b[0m\x1b[K")
		} else {
			out.WriteString("\x1b[0m")
		}
	}

	out.WriteString("\x1b[0m")
	if mode&vt10x.ModeWrap != 0 {
		out.WriteString("\x1b[?7h")
	}
	writeCursorMove(&out, cursorY+1, cursorX+1)
	if cursorVisible {
		out.WriteString("\x1b[?25h")
	} else {
		out.WriteString("\x1b[?25l")
	}

	return out.Bytes()
}

func lastNonDefaultCol(view vt10x.View, row, cols int) int {
	last := -1
	for x := 0; x < cols; x++ {
		cell := view.Cell(x, row)
		style := glyphStyleFromCell(cell)
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		if ch != ' ' || !style.isDefault() {
			last = x
		}
	}
	if last < 0 {
		return -1
	}
	return last
}

func writeCursorMove(out *bytes.Buffer, row, col int) {
	out.WriteString("\x1b[")
	out.WriteString(strconv.Itoa(row))
	out.WriteByte(';')
	out.WriteString(strconv.Itoa(col))
	out.WriteByte('H')
}

func writeStyle(out *bytes.Buffer, style glyphStyle) {
	params := make([]int, 0, 8)
	params = append(params, 0)
	if style.bold {
		params = append(params, 1)
	}
	if style.underline {
		params = append(params, 4)
	}
	if style.blink {
		params = append(params, 5)
	}
	if style.reverse {
		params = append(params, 7)
	}
	if style.italic {
		params = append(params, 3)
	}
	params = appendColorParams(params, style.fg, true)
	params = appendColorParams(params, style.bg, false)

	out.WriteString("\x1b[")
	for i, p := range params {
		if i > 0 {
			out.WriteByte(';')
		}
		out.WriteString(strconv.Itoa(p))
	}
	out.WriteByte('m')
}

func appendColorParams(params []int, color vt10x.Color, foreground bool) []int {
	defaultCode := 49
	if foreground {
		defaultCode = 39
	}
	if (foreground && color == vt10x.DefaultFG) || (!foreground && color == vt10x.DefaultBG) {
		return params
	}
	if color.ANSI() {
		idx := int(color)
		switch {
		case idx < 8:
			if foreground {
				return append(params, 30+idx)
			}
			return append(params, 40+idx)
		case idx < 16:
			if foreground {
				return append(params, 90+(idx-8))
			}
			return append(params, 100+(idx-8))
		default:
			// Fall through to 256-color mode for safety.
		}
	}
	n := int(color)
	if n < 0 {
		return append(params, defaultCode)
	}
	if foreground {
		return append(params, 38, 5, n)
	}
	return append(params, 48, 5, n)
}

func glyphStyleFromCell(cell vt10x.Glyph) glyphStyle {
	return glyphStyle{
		bold:      cell.Mode&snapshotAttrBold != 0,
		underline: cell.Mode&snapshotAttrUnderline != 0,
		italic:    cell.Mode&snapshotAttrItalic != 0,
		blink:     cell.Mode&snapshotAttrBlink != 0,
		reverse:   cell.Mode&snapshotAttrReverse != 0,
		fg:        cell.FG,
		bg:        cell.BG,
	}
}

func (g glyphStyle) isDefault() bool {
	return !g.bold &&
		!g.underline &&
		!g.italic &&
		!g.blink &&
		!g.reverse &&
		g.fg == vt10x.DefaultFG &&
		g.bg == vt10x.DefaultBG
}

func clampToRange(value, max int) int {
	if value < 0 {
		return 0
	}
	if value > max {
		return max
	}
	return value
}
