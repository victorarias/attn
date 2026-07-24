package pty

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

var corpusSizeSuffix = regexp.MustCompile(`-(\d+)x(\d+)\.bytes$`)

type parityCorpusFixture struct {
	name                         string
	knownVT10xDivergence         string
	goldenCursorX, goldenCursorY int
}

func TestGhosttyVT10xParityCorpus(t *testing.T) {
	probe, err := ghosttyvt.New(80, 24, ghosttyvt.Options{})
	if err != nil {
		t.Skipf("ghosttyvt unavailable; skipping parity corpus: %v", err)
	}
	probeSnapshot := probe.Serialize()
	probe.Close()
	if probeSnapshot.VTDump == nil {
		t.Skip("ghosttyvt returned a nil VT dump; skipping parity corpus on the non-native stub")
	}

	fixtures := []parityCorpusFixture{
		{name: "claude-approval-80x24.bytes"},
		{
			name: "codex-approval-80x24.bytes",
			// A real tmux replay at 80x24 matches Ghostty, including cursor (0,17).
			// vt10x mishandles this Codex TUI's CSI r scroll-region and CSI S scroll-up traffic.
			knownVT10xDivergence: "vt10x mishandles Codex scroll-region and scroll-up traffic; golden was verified against a real 80x24 tmux replay",
			goldenCursorX:        0,
			goldenCursorY:        17,
		},
		{name: "fish-resize-80x24.bytes"},
		{name: "sgr-heavy-80x24.bytes"},
		{name: "sgr-heavy-120x40.bytes"},
		{name: "vim-altscreen-80x24.bytes"},
		{name: "vim-altscreen-120x40.bytes"},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			cols, rows := corpusFixtureSize(t, fixture.name)
			data, err := os.ReadFile(filepath.Join("testdata", "corpus", fixture.name))
			if err != nil {
				t.Fatalf("read corpus fixture: %v", err)
			}

			for _, chunkSize := range []int{4096, 1024} {
				t.Run(fmt.Sprintf("chunk-%d", chunkSize), func(t *testing.T) {
					screen := newVirtualScreen(uint16(cols), uint16(rows))
					term, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
					if err != nil {
						t.Fatalf("ghosttyvt.New(%d, %d): %v", cols, rows, err)
					}
					defer term.Close()

					// script(1) records no tmux resize event. In particular, the fish
					// fixture is replayed at its filename's fixed 80x24 grid by both
					// emulators, so this asserts parity over the same byte stream.
					for start := 0; start < len(data); start += chunkSize {
						end := min(start+chunkSize, len(data))
						chunk := data[start:end]
						screen.Observe(chunk)
						term.Write(chunk)
					}

					ghosttyText := term.ViewportText()
					if fixture.knownVT10xDivergence != "" {
						golden := readCorpusGolden(t, fixture.name)
						assertCorpusViewportEqual(t, "ghostty", "golden", ghosttyText, golden)
						if screen.renderedText() == ghosttyText {
							t.Fatalf("vt10x unexpectedly matches Ghostty for known divergence (%s); promote this fixture back to direct parity", fixture.knownVT10xDivergence)
						}
						gotX, gotY := term.CursorPos()
						if gotX != fixture.goldenCursorX || gotY != fixture.goldenCursorY {
							t.Errorf("golden cursor mismatch: ghostty=(%d,%d), golden=(%d,%d)",
								gotX, gotY, fixture.goldenCursorX, fixture.goldenCursorY)
						}
						return
					}

					assertCorpusViewportEqual(t, "ghostty", "vt10x", ghosttyText, screen.renderedText())
					if snapshot, ok := screen.Snapshot(); ok {
						gotX, gotY := term.CursorPos()
						if gotX != int(snapshot.cursorX) || gotY != int(snapshot.cursorY) {
							t.Errorf("cursor mismatch: ghostty=(%d,%d), vt10x=(%d,%d)",
								gotX, gotY, snapshot.cursorX, snapshot.cursorY)
						}
					}
				})
			}
		})
	}
}

func corpusFixtureSize(t *testing.T, fixture string) (int, int) {
	t.Helper()
	matches := corpusSizeSuffix.FindStringSubmatch(fixture)
	if matches == nil {
		t.Fatalf("fixture name has no terminal size suffix: %q", fixture)
	}
	cols, err := strconv.Atoi(matches[1])
	if err != nil {
		t.Fatalf("parse fixture columns %q: %v", matches[1], err)
	}
	rows, err := strconv.Atoi(matches[2])
	if err != nil {
		t.Fatalf("parse fixture rows %q: %v", matches[2], err)
	}
	return cols, rows
}

func readCorpusGolden(t *testing.T, fixture string) string {
	t.Helper()
	goldenPath := filepath.Join("testdata", "corpus", strings.TrimSuffix(fixture, ".bytes")+".golden")
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read corpus golden %q: %v", goldenPath, err)
	}
	return string(golden)
}

func assertCorpusViewportEqual(t *testing.T, gotName, wantName, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	row, gotRow, wantRow := firstDifferingCorpusRow(got, want)
	t.Errorf("viewport text mismatch: first differing row %d\n"+
		"%s row: %q\n"+
		"%s row: %q\n"+
		"--- %s full render ---\n%s"+
		"--- %s full render ---\n%s",
		row, gotName, gotRow, wantName, wantRow, gotName, got, wantName, want)
}

func firstDifferingCorpusRow(got, want string) (int, string, string) {
	gotRows := corpusRenderedRows(got)
	wantRows := corpusRenderedRows(want)
	rows := max(len(gotRows), len(wantRows))
	for row := range rows {
		gotRow := "<missing row>"
		if row < len(gotRows) {
			gotRow = gotRows[row]
		}
		wantRow := "<missing row>"
		if row < len(wantRows) {
			wantRow = wantRows[row]
		}
		if gotRow != wantRow {
			return row, gotRow, wantRow
		}
	}
	return -1, "", ""
}

func corpusRenderedRows(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
