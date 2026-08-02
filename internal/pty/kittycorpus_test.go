//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// The cross-runtime parity corpus for the kitty wire rewrite.
//
// testdata/kitty_rewrite_corpus.json is consumed twice. Here, against two real
// native ghostty terminals; and in app/src/utils/kittyWireRewrite.parity.test.ts,
// against the shipped wasm model — the runtime that actually renders in the app
// and the only one whose agreement proves the synthesized bytes MEAN the same
// thing to the client. Neither side can move without the other noticing.
//
// The JSON is generated, not authored: kittyCorpusInputs below is the source of
// truth for the inputs, `go test ./internal/pty -run TestKittyWireRewriteCorpus
// -update` regenerates the file, and a normal run fails when the two disagree.
// That is what lets a fuzz-minimized failure enter the corpus as one new input
// entry plus one regeneration.
//
// The wire bytes ARE pinned byte-for-byte here, unlike the mirror gate next
// door, which judges only the resulting grids. They have to be: they are the
// artifact the wasm side replays, so a corpus that recorded only outcomes would
// hand the other runtime nothing to replay. Reshaping synthesis is expected to
// move them — regenerate, and let both layers re-prove the meaning.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

const kittyCorpusFileName = "kitty_rewrite_corpus.json"

const kittyCorpusDescription = "Cross-runtime parity corpus for the kitty wire rewrite (docs/plans/2026-08-02-terminal-kitty-images.md). " +
	"GENERATED — do not hand-edit. Inputs come from kittyCorpusInputs in internal/pty/kittycorpus_test.go; " +
	"regenerate with `go test ./internal/pty -run TestKittyWireRewriteCorpus -update`. " +
	"Each entry feeds `chunks` (base64, in order) through one real wireFeeder into a kitty-LIVE ghostty terminal. " +
	"`wire[i]` is what the fan-out carried for chunk i (base64; \"\" when the feeder held the chunk and the fan-out was skipped) " +
	"and `resync[i]` is the reason chunk i could not be expressed on the wire (\"\" when none). " +
	"`workerPlainText`, `workerViewportText`, `cursorCol` and `cursorRow` record the kitty-live terminal after the last chunk. " +
	"Replaying `wire` into a terminal that cannot parse kitty must reproduce exactly those three — that agreement is the no-desync property. " +
	"An entry with any nonempty `resync` is exempt: the wire deliberately carries nothing for that chunk and a snapshot re-push makes the client whole."

type kittyCorpusEntry struct {
	Name   string   `json:"name"`
	Cols   int      `json:"cols"`
	Rows   int      `json:"rows"`
	Chunks []string `json:"chunks"`

	Wire   []string `json:"wire"`
	Resync []string `json:"resync"`

	WorkerPlainText    string `json:"workerPlainText"`
	WorkerViewportText string `json:"workerViewportText"`
	CursorCol          int    `json:"cursorCol"`
	CursorRow          int    `json:"cursorRow"`
}

func (e kittyCorpusEntry) resynced() bool {
	for _, reason := range e.Resync {
		if reason != "" {
			return true
		}
	}
	return false
}

type kittyCorpusFile struct {
	Description string             `json:"description"`
	Entries     []kittyCorpusEntry `json:"entries"`
}

type kittyCorpusInput struct {
	name       string
	cols, rows int
	chunks     []string
}

// kittyCorpusPixels is the mirror gate's pixel formula (kittyPlaceRGB), lifted
// so the multi-escape builders below fill their images identically. Fixed and
// content-free on purpose: a corpus that carried real image bytes would carry a
// timestamp or a compressor version with them.
func kittyCorpusPixels(w, h int) []byte {
	pix := make([]byte, w*h*3)
	for i := range pix {
		pix[i] = byte((i*7 + 13) % 251)
	}
	return pix
}

// kittyPlaceRGBChunked builds a transmission the way kitty's own convention
// does — several escapes carrying m=1 until the last one — rather than the
// single escape kittyPlaceRGB emits. The segmenter sees N complete APCs and the
// terminal only places the image on the last, so a corpus entry that splits
// these across feed chunks exercises both the multi-escape and the
// mid-payload-split paths at once.
func kittyPlaceRGBChunked(id uint32, w, h, payloadChunk int) string {
	encoded := base64.StdEncoding.EncodeToString(kittyCorpusPixels(w, h))
	var out strings.Builder
	first := true
	for len(encoded) > 0 {
		take := min(payloadChunk, len(encoded))
		part := encoded[:take]
		encoded = encoded[take:]
		more := 0
		if len(encoded) > 0 {
			more = 1
		}
		if first {
			fmt.Fprintf(&out, "\x1b_Ga=T,i=%d,f=24,t=d,s=%d,v=%d,m=%d;%s\x1b\\", id, w, h, more, part)
			first = false
			continue
		}
		fmt.Fprintf(&out, "\x1b_Gm=%d;%s\x1b\\", more, part)
	}
	return out.String()
}

// kittyCorpusInputs is the corpus itself. Cell metrics are 8x16 px, so an image
// of v=16*n pixels is exactly n rows tall.
func kittyCorpusInputs() []kittyCorpusInput {
	return []kittyCorpusInput{
		{
			name: "image placed at a cursor in the middle of the screen",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[4;6Hxy", kittyPlaceRGB(1, 16, 32, "")},
		},
		{
			name: "image on the bottom row scrolls the screen",
			cols: 20, rows: 8,
			chunks: []string{"top\r\nsecond\r\n\x1b[8;1Hbottom", kittyPlaceRGB(2, 16, 32, "")},
		},
		{
			name: "image taller than the rows left below the cursor",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[6;3Hhere", kittyPlaceRGB(3, 16, 96, "")},
		},
		{
			name: "image placed inside a scroll region",
			cols: 20, rows: 8,
			chunks: []string{
				"one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix",
				"\x1b[3;6r\x1b[6;1Hin-region",
				kittyPlaceRGB(4, 16, 32, ""),
			},
		},
		{
			// Probed, not assumed: ghostty scrolls the REGION rather than letting
			// the post-placement cursor cross the bottom margin, so the cursor
			// ends inside the region and the synthesized CUU/CUD is a move the
			// client's own margins cannot clamp. The grids agree; there is no
			// resync here and none is wanted.
			name: "cursor on the bottom margin, image taller than the rows below it",
			cols: 20, rows: 8,
			chunks: []string{
				"one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\nseven",
				"\x1b[3;6r\x1b[6;1Hedge",
				kittyPlaceRGB(13, 16, 48, ""),
			},
		},
		{
			// The cursor parked BELOW the bottom margin, where ghostty clamps it
			// at the last row instead of scrolling anything.
			name: "cursor below the bottom margin of a scroll region",
			cols: 20, rows: 8,
			chunks: []string{
				"one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\nseven\r\neight",
				"\x1b[2;5r\x1b[8;1Hlast",
				kittyPlaceRGB(14, 16, 64, ""),
			},
		},
		{
			name: "image placed on the alternate screen, then back",
			cols: 20, rows: 8,
			chunks: []string{
				"primary line\r\n",
				"\x1b[?1049h\x1b[3;3Halt",
				kittyPlaceRGB(5, 16, 32, ""),
				"\x1b[?1049l",
			},
		},
		{
			name: "chunked transmission split across feed calls",
			cols: 20, rows: 8,
			chunks: splitEvery(kittyPlaceRGB(6, 16, 32, ""), 97),
		},
		{
			// The introducer itself cut in two places, which is the only way to
			// exercise the suffix the segmenter holds back on the chance the next
			// chunk completes ESC _ G.
			name: "the introducer split across three feed chunks",
			cols: 20, rows: 8,
			chunks: []string{"hi\x1b", "_", "G" + strings.TrimPrefix(kittyPlaceRGB(23, 8, 16, ""), "\x1b_G")},
		},
		{
			// Several m=1 escapes, cut so the splits land inside payloads AND
			// between escapes: the segmenter has to carry a partial APC across
			// feed calls while the terminal accumulates a transmission across
			// complete ones.
			name: "multi-escape m=1 transmission split mid-payload across feed chunks",
			cols: 20, rows: 8,
			chunks: splitEvery("\x1b[2;2Hbefore"+kittyPlaceRGBChunked(15, 16, 48, 512)+"after", 137),
		},
		{
			name: "a held escape, then text and a half image in one chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"abc\x1b",
				"[1mmore" + halfOf(kittyPlaceRGB(9, 16, 32, "")),
				restOf(kittyPlaceRGB(9, 16, 32, "")) + "end",
			},
		},
		{
			name: "delete removes the placement without touching the grid",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[2;2Hkeep", kittyPlaceRGB(7, 16, 32, ""), "\x1b_Ga=d\x1b\\"},
		},
		{
			name: "delete and re-place of the same image id in one chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(16, 16, 32, ""),
				"\x1b_Ga=d,d=i,i=16\x1b\\" + kittyPlaceRGB(16, 16, 48, ""),
			},
		},
		{
			name: "two images with text between them in one chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;1Hstart",
				kittyPlaceRGB(17, 16, 32, "") + " gap " + kittyPlaceRGB(18, 24, 32, "") + " tail",
			},
		},
		{
			// C=1 tells kitty not to move the cursor. Nothing moves, nothing
			// scrolls, and the wire carries nothing for the chunk — the case
			// that would break a synthesizer predicting from the image geometry
			// rather than observing the terminal.
			name: "placement with the no-cursor-move flag",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[3;4Hxy", kittyPlaceRGB(19, 16, 32, ",C=1")},
		},
		{
			name: "no-cursor-move placement on the bottom row",
			cols: 20, rows: 8,
			chunks: []string{"a\r\nb\r\nc\r\n\x1b[8;1Hbottom", kittyPlaceRGB(20, 16, 96, ",C=1")},
		},
		{
			// The support query ghostty answers from its own response channel.
			// It is stripped from the wire like any other APC, and the reply
			// reaches the program regardless (asserted in the mirror gate).
			name: "support query between text in one chunk",
			cols: 20, rows: 8,
			chunks: []string{"before \x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\ after"},
		},
		{
			// A stray ESC inside the payload abandons the sequence: the whole
			// prefix is ordinary bytes on BOTH sides, so the wire keeps it and
			// the grids still agree.
			name: "an APC abandoned by a stray escape mid-payload",
			cols: 20, rows: 8,
			chunks: []string{"A\x1b_Ga=T,i=21,f=24,t=d,s=16,v=32;AAAA\x1b[32mZ\x1b[0m done"},
		},
		{
			name: "osc 133 markers interleaved with images",
			cols: 40, rows: 10,
			chunks: []string{
				"\x1b]133;A\x1b\\$ ",
				"\x1b]133;B\x1b\\icat pic.png",
				"\r\n\x1b]133;C;cmdline_url=icat%20pic.png\x1b\\",
				kittyPlaceRGB(8, 16, 32, ""),
				"\r\n\x1b]133;D;0\x1b\\\x1b]133;A\x1b\\$ ",
			},
		},
		{
			// PROBED, and pinned as measured: on a brand-new PRIMARY screen the
			// cursor sits on absolute row 0, so a placement that scrolls pushes
			// the anchor cell to the top of retained history — where the clamp
			// guard cannot tell "retained at row 0" from "discarded and clamped
			// there". It resyncs. The anchor is in fact intact (the primary
			// screen keeps history), so this re-push is not needed; it is the
			// accepted cost of a guard that must never guess, and it costs one
			// snapshot on the first image of a session that has printed nothing.
			// Pinned here so a future synthesis change has to look at it.
			name: "first line of a fresh primary screen, image tall enough to scroll",
			cols: 20, rows: 8,
			chunks: []string{kittyPlaceRGB(22, 16, 160, "")},
		},
		{
			// The alternate screen keeps no history at all, so the same clamp
			// fires on a genuinely destroyed anchor. This one the re-push exists
			// for.
			name: "image taller than an alternate screen that keeps no history",
			cols: 20, rows: 6,
			chunks: []string{
				"\x1b[?1049h",
				"alt0\r\nalt1\r\nalt2\r\nalt3\r\nalt4\r\n\x1b[6;1Halt5",
				kittyPlaceRGB(12, 16, 128, ""),
			},
		},
	}
}

// runKittyCorpusEntry feeds one input through a real wireFeeder into a
// kitty-live terminal and records everything the corpus pins. The feeder is
// closed and the native ref count checked here, so every entry stands alone.
func runKittyCorpusEntry(t *testing.T, in kittyCorpusInput) kittyCorpusEntry {
	t.Helper()
	baseline := ghosttyvt.LiveTrackedRefs()

	worker := newKittyTerminal(t, in.cols, in.rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	feeder := newWireFeeder(worker)
	if feeder == nil {
		t.Fatalf("newWireFeeder returned nil for a live terminal")
	}

	entry := kittyCorpusEntry{Name: in.name, Cols: in.cols, Rows: in.rows}
	for _, chunk := range in.chunks {
		wire, resync := feeder.feed([]byte(chunk))
		entry.Chunks = append(entry.Chunks, base64.StdEncoding.EncodeToString([]byte(chunk)))
		// An empty wire is the read loop's skip condition, recorded as "" so the
		// replay side skips the same chunk rather than writing zero bytes.
		encoded := ""
		if len(wire) > 0 {
			encoded = base64.StdEncoding.EncodeToString(wire)
		}
		entry.Wire = append(entry.Wire, encoded)
		entry.Resync = append(entry.Resync, resync)
	}

	entry.WorkerPlainText = worker.PlainText()
	entry.WorkerViewportText = worker.ViewportText()
	entry.CursorCol, entry.CursorRow = worker.CursorPos()

	feeder.close()
	if got := ghosttyvt.LiveTrackedRefs(); got != baseline {
		t.Errorf("LiveTrackedRefs() = %d after %q, want the %d it started at", got, in.name, baseline)
	}
	return entry
}

// replayKittyWire writes the recorded wire into a terminal that cannot parse
// kitty — the closest native stand-in for the frontend's wasm model.
func replayKittyWire(t *testing.T, entry kittyCorpusEntry) *ghosttyvt.Terminal {
	t.Helper()
	client := newKittyTerminal(t, entry.Cols, entry.Rows, ghosttyvt.Options{})
	for i, encoded := range entry.Wire {
		if encoded == "" {
			continue
		}
		wire, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode wire chunk %d of %q: %v", i, entry.Name, err)
		}
		client.Write(wire)
	}
	return client
}

func TestKittyWireRewriteCorpus(t *testing.T) {
	inputs := kittyCorpusInputs()
	recorded := make([]kittyCorpusEntry, 0, len(inputs))
	for _, in := range inputs {
		recorded = append(recorded, runKittyCorpusEntry(t, in))
	}

	if *updateGoldens {
		writeKittyCorpus(t, recorded)
		t.Logf("regenerated %s with %d entries", kittyCorpusFileName, len(recorded))
		return
	}

	stored := readKittyCorpus(t)
	if len(stored) != len(recorded) {
		t.Fatalf("%s holds %d entries, the inputs produce %d: re-run with -update",
			kittyCorpusFileName, len(stored), len(recorded))
	}

	for i, want := range stored {
		t.Run(want.Name, func(t *testing.T) {
			assertKittyCorpusEntryEqual(t, want, recorded[i])

			if want.resynced() {
				// The wire deliberately carries nothing for the chunk that
				// failed observation; the snapshot re-push is what makes the
				// client whole, and it is the restore path's test, not this one.
				return
			}

			client := replayKittyWire(t, want)
			if got := client.PlainText(); got != want.WorkerPlainText {
				t.Errorf("replayed history diverged from the worker\nworker:\n%s\nclient:\n%s", want.WorkerPlainText, got)
			}
			if got := client.ViewportText(); got != want.WorkerViewportText {
				t.Errorf("replayed viewport diverged from the worker\nworker:\n%s\nclient:\n%s", want.WorkerViewportText, got)
			}
			if col, row := client.CursorPos(); col != want.CursorCol || row != want.CursorRow {
				t.Errorf("replayed cursor at (%d,%d), the worker's is at (%d,%d)", col, row, want.CursorCol, want.CursorRow)
			}
		})
	}
}

// assertKittyCorpusEntryEqual compares a stored entry against a fresh recording
// field by field, so a stale corpus names what moved instead of dumping two
// JSON blobs.
func assertKittyCorpusEntryEqual(t *testing.T, want, got kittyCorpusEntry) {
	t.Helper()
	const rerun = "re-run with -update once the change is intended"
	if want.Name != got.Name || want.Cols != got.Cols || want.Rows != got.Rows {
		t.Fatalf("entry identity moved: stored %q %dx%d, recorded %q %dx%d: %s",
			want.Name, want.Cols, want.Rows, got.Name, got.Cols, got.Rows, rerun)
	}
	assertBase64SliceEqual(t, "chunks", want.Chunks, got.Chunks, rerun)
	assertBase64SliceEqual(t, "wire", want.Wire, got.Wire, rerun)
	if strings.Join(want.Resync, "|") != strings.Join(got.Resync, "|") {
		t.Errorf("resync reasons = %q, stored %q: %s", got.Resync, want.Resync, rerun)
	}
	if want.WorkerPlainText != got.WorkerPlainText {
		t.Errorf("worker history moved\nstored:\n%s\nrecorded:\n%s\n%s", want.WorkerPlainText, got.WorkerPlainText, rerun)
	}
	if want.WorkerViewportText != got.WorkerViewportText {
		t.Errorf("worker viewport moved\nstored:\n%s\nrecorded:\n%s\n%s", want.WorkerViewportText, got.WorkerViewportText, rerun)
	}
	if want.CursorCol != got.CursorCol || want.CursorRow != got.CursorRow {
		t.Errorf("worker cursor = (%d,%d), stored (%d,%d): %s", got.CursorCol, got.CursorRow, want.CursorCol, want.CursorRow, rerun)
	}
}

// assertBase64SliceEqual reports a per-chunk mismatch with the bytes decoded,
// because a base64 diff is unreadable and the escapes are the whole point.
func assertBase64SliceEqual(t *testing.T, field string, want, got []string, rerun string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s has %d chunks, stored %d: %s", field, len(got), len(want), rerun)
	}
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		t.Errorf("%s[%d] = %q, stored %q: %s", field, i, decodeForMessage(got[i]), decodeForMessage(want[i]), rerun)
	}
}

func decodeForMessage(encoded string) string {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return encoded
	}
	return string(raw)
}

func kittyCorpusPath() string {
	return filepath.Join("testdata", kittyCorpusFileName)
}

func readKittyCorpus(t *testing.T) []kittyCorpusEntry {
	t.Helper()
	raw, err := os.ReadFile(kittyCorpusPath())
	if err != nil {
		t.Fatalf("read %s: %v", kittyCorpusFileName, err)
	}
	var file kittyCorpusFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse %s: %v", kittyCorpusFileName, err)
	}
	if len(file.Entries) == 0 {
		t.Fatalf("%s holds no entries", kittyCorpusFileName)
	}
	return file.Entries
}

func writeKittyCorpus(t *testing.T, entries []kittyCorpusEntry) {
	t.Helper()
	raw, err := json.MarshalIndent(kittyCorpusFile{Description: kittyCorpusDescription, Entries: entries}, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", kittyCorpusFileName, err)
	}
	if err := os.WriteFile(kittyCorpusPath(), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", kittyCorpusFileName, err)
	}
}
