package pty

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

var (
	corpusSizeSuffix = regexp.MustCompile(`-(\d+)x(\d+)\.bytes$`)
	updateGoldens    = flag.Bool("update", false, "update Ghostty terminal corpus golden files")
)

type ghosttyCorpusFixture struct {
	name                         string
	goldenCursorX, goldenCursorY int
}

func TestGhosttyCorpusGoldens(t *testing.T) {
	probe, err := ghosttyvt.New(80, 24, ghosttyvt.Options{})
	if err != nil {
		t.Skipf("ghosttyvt unavailable; skipping corpus goldens: %v", err)
	}
	probeSnapshot := probe.Serialize()
	probe.Close()
	if probeSnapshot.VTDump == nil {
		t.Skip("ghosttyvt returned a nil VT dump; skipping corpus goldens on the non-native stub")
	}

	fixtures := []ghosttyCorpusFixture{
		{name: "claude-approval-80x24.bytes", goldenCursorX: 0, goldenCursorY: 6},
		{name: "codex-approval-80x24.bytes", goldenCursorX: 0, goldenCursorY: 17},
		{name: "fish-resize-80x24.bytes", goldenCursorX: 0, goldenCursorY: 21},
		{name: "sgr-heavy-80x24.bytes", goldenCursorX: 0, goldenCursorY: 23},
		{name: "sgr-heavy-120x40.bytes", goldenCursorX: 0, goldenCursorY: 39},
		{name: "vim-altscreen-80x24.bytes", goldenCursorX: 0, goldenCursorY: 0},
		{name: "vim-altscreen-120x40.bytes", goldenCursorX: 0, goldenCursorY: 0},
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
					term, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
					if err != nil {
						t.Fatalf("ghosttyvt.New(%d, %d): %v", cols, rows, err)
					}
					defer term.Close()

					for start := 0; start < len(data); start += chunkSize {
						end := min(start+chunkSize, len(data))
						term.Write(data[start:end])
					}

					got := term.ViewportText()
					if *updateGoldens {
						writeCorpusGolden(t, fixture.name, got)
					} else {
						golden := readCorpusGolden(t, fixture.name)
						assertCorpusViewportEqual(t, "ghostty", "golden", got, golden)
						gotX, gotY := term.CursorPos()
						if gotX != fixture.goldenCursorX || gotY != fixture.goldenCursorY {
							t.Errorf("golden cursor mismatch: ghostty=(%d,%d), golden=(%d,%d)",
								gotX, gotY, fixture.goldenCursorX, fixture.goldenCursorY)
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

func corpusGoldenPath(fixture string) string {
	return filepath.Join("testdata", "corpus", strings.TrimSuffix(fixture, ".bytes")+".golden")
}

func readCorpusGolden(t *testing.T, fixture string) string {
	t.Helper()
	goldenPath := corpusGoldenPath(fixture)
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read corpus golden %q: %v", goldenPath, err)
	}
	return string(golden)
}

func writeCorpusGolden(t *testing.T, fixture, content string) {
	t.Helper()
	goldenPath := corpusGoldenPath(fixture)
	if err := os.WriteFile(goldenPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write corpus golden %q: %v", goldenPath, err)
	}
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
