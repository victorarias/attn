package probetui

import "bytes"

// codex-cli 0.145.0 never uses the alternate screen, addresses the
// existing screen with CUP/EL, and queries DA1/CPR/OSC 10+11 colors on
// startup. See internal/probetui/testdata/agent-vocab-codex.json.

func startupCodex(cols, rows int) []byte {
	var b bytes.Buffer
	b.Write(privateMode(true, "1004", "2004"))
	b.Write(da1Query())
	b.Write(cprQuery())
	b.Write(oscColorQuery("10"))
	b.Write(oscColorQuery("11"))
	b.Write(decstbm(1, rows))
	return b.Bytes()
}

func frameCodex(cols, rows, seq int) []byte {
	var b bytes.Buffer
	b.Write(privateMode(true, "2026"))
	b.Write(privateMode(false, "25")) // hide cursor at frame start

	for row := 1; row <= rows; row++ {
		b.Write(cup(row, 1))
		b.Write(el())
		switch row {
		case 1:
			b.WriteString(truncateToWidth(bannerGeometryRow(cols, rows), cols))
		case 2:
			b.WriteString(truncateToWidth(bannerStyleRow(StyleCodex, seq), cols))
		case 3:
			// One SGR color and one OSC 8 hyperlink per frame.
			b.Write(sgr("38;5;2"))
			b.Write(hyperlink("file:///tmp/attn-probe/session.log", truncateToWidth("session.log", cols)))
			b.Write(sgr("0"))
		default:
			b.WriteString(fillRow(cols, rows, seq, row))
		}
	}

	b.Write(privateMode(true, "25")) // show cursor at frame end
	b.Write(privateMode(false, "2026"))
	return b.Bytes()
}

func onResizeCodex(cols, rows int) []byte {
	var b bytes.Buffer
	b.Write(cprQuery())
	b.Write(ed(2))
	return b.Bytes()
}

func teardownCodex() []byte {
	var b bytes.Buffer
	b.Write(privateMode(false, "2026"))
	b.Write(privateMode(true, "25"))
	b.Write(privateMode(false, "2004", "1004"))
	b.Write(decstbm(0, 0))
	return b.Bytes()
}
