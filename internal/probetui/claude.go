package probetui

import (
	"bytes"
	"fmt"
)

// claude 2.1.217 runs full-screen: alt-screen, mouse tracking, bracketed
// paste, and positions text with \r plus relative CSI column/row moves
// rather than CUP+newlines. See
// internal/probetui/testdata/agent-vocab-claude.json.

var claudeMouseModes = []string{"1000", "1002", "1003", "1006"}

func decsc() []byte { return []byte{0x1b, '7'} }
func decrc() []byte { return []byte{0x1b, '8'} }
func decscusr(style int) []byte {
	return csi(fmt.Sprintf("%d ", style), 'q')
}

func startupClaude(cols, rows int) []byte {
	var b bytes.Buffer
	b.Write(privateMode(true, "1049"))
	b.Write(privateMode(true, claudeMouseModes...))
	b.Write(privateMode(true, "1004", "2004", "2031"))
	b.Write(osc("0", "attn-probe"))
	b.Write(decscusr(2))
	b.Write(da1Query())
	return b.Bytes()
}

func frameClaude(cols, rows, seq int) []byte {
	var b bytes.Buffer
	b.Write(privateMode(true, "2026"))
	b.Write(privateMode(false, "25")) // hide cursor at frame start
	b.Write(decsc())

	b.WriteString("\r")
	b.Write(csi("1", 'G'))
	b.Write(sgr("38;5;4"))
	b.WriteString(truncateToWidth(bannerGeometryRow(cols, rows), cols))
	b.Write(sgr("0"))

	for row := 2; row <= rows; row++ {
		b.Write(csi("1", 'B'))
		b.WriteString("\r")
		switch row {
		case 2:
			b.WriteString(truncateToWidth(bannerStyleRow(StyleClaude, seq), cols))
		case 3:
			b.Write(csi("2", 'C'))
			b.Write(hyperlink("file:///tmp/attn-probe/session.log", truncateToWidth("session.log", cols)))
		default:
			b.WriteString(fillRow(cols, rows, seq, row))
		}
	}

	b.Write(decrc())
	b.Write(privateMode(true, "25")) // show cursor at frame end
	b.Write(privateMode(false, "2026"))
	return b.Bytes()
}

func onResizeClaude(cols, rows int) []byte {
	var b bytes.Buffer
	b.Write(privateMode(true, claudeMouseModes...))
	b.Write(ed(2))
	return b.Bytes()
}

func teardownClaude() []byte {
	var b bytes.Buffer
	b.Write(privateMode(false, "2026"))
	b.Write(privateMode(false, claudeMouseModes...))
	b.Write(privateMode(false, "1004", "2004", "2031"))
	b.Write(privateMode(true, "25"))
	b.Write(privateMode(false, "1049"))
	return b.Bytes()
}
