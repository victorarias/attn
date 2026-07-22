package probetui

import (
	"bytes"
	"fmt"
	"regexp"
	"testing"
)

var testStyles = []Style{StyleCodex, StyleClaude}
var testGeometries = [][2]int{{80, 24}, {30, 10}}

var geometryRowPattern = regexp.MustCompile(`ATTN-PROBE \d+x\d+`)

func TestFrameDeterminism(t *testing.T) {
	for _, style := range testStyles {
		for _, g := range testGeometries {
			cols, rows := g[0], g[1]
			t.Run(fmt.Sprintf("%s/%dx%d", style, cols, rows), func(t *testing.T) {
				a := Frame(style, cols, rows, 1)
				b := Frame(style, cols, rows, 1)
				if !bytes.Equal(a, b) {
					t.Fatalf("Frame(%s,%d,%d,1) is not deterministic across calls", style, cols, rows)
				}

				c := Frame(style, cols, rows, 2)
				if bytes.Equal(a, c) {
					t.Fatalf("Frame(%s,%d,%d,1) == Frame(...,2); seq must change output", style, cols, rows)
				}

				matches := geometryRowPattern.FindAll(a, -1)
				if len(matches) != 1 {
					t.Fatalf("geometry banner %q appears %d times in frame, want exactly 1 (frame: %q)", geometryRowPattern.String(), len(matches), a)
				}

				styleRow := truncateToWidth(bannerStyleRow(style, 1), cols)
				if n := bytes.Count(a, []byte(styleRow)); n != 1 {
					t.Fatalf("style banner row %q appears %d times in frame, want exactly 1", styleRow, n)
				}
			})
		}
	}
}

func TestFrameGeometryDiscipline(t *testing.T) {
	const cols, rows = 30, 10
	for _, style := range testStyles {
		t.Run(string(style), func(t *testing.T) {
			geoRaw := bannerGeometryRow(cols, rows)
			styleRaw := bannerStyleRow(style, 1)

			geoTrunc := TruncateToWidth(geoRaw, cols)
			styleTrunc := TruncateToWidth(styleRaw, cols)
			if len(geoTrunc) > cols {
				t.Fatalf("geometry row %q (%d bytes) exceeds %d cols after truncation", geoTrunc, len(geoTrunc), cols)
			}
			if len(styleTrunc) > cols {
				t.Fatalf("style row %q (%d bytes) exceeds %d cols after truncation", styleTrunc, len(styleTrunc), cols)
			}

			frame := Frame(style, cols, rows, 1)
			if !bytes.Contains(frame, []byte(geoTrunc)) {
				t.Fatalf("frame does not contain the expected geometry row %q", geoTrunc)
			}
			if !bytes.Contains(frame, []byte(styleTrunc)) {
				t.Fatalf("frame does not contain the expected style row %q", styleTrunc)
			}
		})
	}
}

// TestFrameGeometryDisciplineNarrow exercises an actual truncation, at a
// width narrow enough (~10 cols) that both banner rows must be clipped —
// the case a single-row combined banner could never satisfy in a
// realistic ~20-col pane.
func TestFrameGeometryDisciplineNarrow(t *testing.T) {
	const cols, rows = 10, 24
	for _, style := range testStyles {
		t.Run(string(style), func(t *testing.T) {
			geoRaw := bannerGeometryRow(cols, rows)
			if len(geoRaw) <= cols {
				t.Fatalf("test geometry too wide to exercise truncation: row %q (%d bytes) already fits %d cols", geoRaw, len(geoRaw), cols)
			}

			geoTrunc := TruncateToWidth(geoRaw, cols)
			if len(geoTrunc) != cols {
				t.Fatalf("TruncateToWidth(%q, %d) = %q (%d bytes), want exactly %d bytes", geoRaw, cols, geoTrunc, len(geoTrunc), cols)
			}

			frame := Frame(style, cols, rows, 1)
			if bytes.Contains(frame, []byte(geoRaw)) {
				t.Fatalf("frame contains the untruncated geometry row %q; must be clipped to %d cols", geoRaw, cols)
			}
			if !bytes.Contains(frame, []byte(geoTrunc)) {
				t.Fatalf("frame does not contain the expected truncated geometry row %q", geoTrunc)
			}
		})
	}
}
