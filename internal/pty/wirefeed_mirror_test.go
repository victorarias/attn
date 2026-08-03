//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// The no-desync gate for the kitty feed path, at unit level.
//
// Two REAL ghostty terminals. The worker has kitty live and is fed raw input
// through the real composed feeder; the client has kitty disabled — the closest
// stand-in for the frontend's wasm model, which cannot parse the protocol at
// all — and is fed only what the feeder puts on the wire. After every case the
// two must agree on their whole text and on the cursor.
//
// That agreement IS the property this phase exists to protect: the worker grid
// backs approval classification and the restore dump, so a grid the client
// cannot reproduce shows up as the screen changing under the user on reattach.
// The tests below judge that outcome and never the byte recipe that produces
// it — synthesis is free to change shape as long as the two grids still match.

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// mirrorStorageLimit is a positive limit so the protocol is live; the value
// only has to exceed the test images.
const mirrorStorageLimit = 10 << 20

// Cells are 8x16 px in ghosttyvt, so every image size below is an exact cell
// count: 16x32 is 2x2 cells, 16x96 is 2x6.
func kittyPlaceRGB(id uint32, w, h int, extra string) string {
	pix := make([]byte, w*h*3)
	for i := range pix {
		pix[i] = byte((i*7 + 13) % 251)
	}
	return fmt.Sprintf("\x1b_Ga=T,i=%d,f=24,t=d,s=%d,v=%d%s;%s\x1b\\",
		id, w, h, extra, base64.StdEncoding.EncodeToString(pix))
}

// kittyTransmitRGB carries pixels and places nothing: a=t replaces the image
// stored under an id, so a placement already showing that id keeps its key and
// its position while its content — and its cell footprint — moves under it.
func kittyTransmitRGB(id uint32, w, h int) string {
	return fmt.Sprintf("\x1b_Ga=t,i=%d,f=24,t=d,s=%d,v=%d;%s\x1b\\",
		id, w, h, base64.StdEncoding.EncodeToString(kittyCorpusPixels(w, h)))
}

// undescribed puts an APC where the segmenter must not cut it out: an
// unfinished CSI in front of it makes the APC's leading ESC that CSI's exit
// too, so removing the APC would remove the exit with it. The bytes therefore
// go to the wire verbatim while ghostty still dispatches the command — an image
// on the worker's grid that the client, which cannot parse kitty, never sees.
// See kittyseg.go, and the corpus entry "an apc that cancels an unfinished csi".
func undescribed(apc string) string { return "\x1b[1" + apc }

// mirror holds the two terminals and the feeder under test.
type mirror struct {
	worker *ghosttyvt.Terminal
	client *ghosttyvt.Terminal
	feed   *wireFeeder
	// clientSeg is the stand-in's own segmenter, used only to keep OSC 133 out
	// of the client terminal — see writeAsClient.
	clientSeg feedSegmenter

	lastWire   []byte
	lastResync string
}

// newKittyTerminal builds a terminal and gives it cell metrics: placement
// geometry is resolved in cells, and cells only have a size after a resize
// (newKittyT in internal/ghosttyvt carries the same line).
func newKittyTerminal(t *testing.T, cols, rows int, opts ghosttyvt.Options) *ghosttyvt.Terminal {
	t.Helper()
	term, err := ghosttyvt.New(cols, rows, opts)
	if err != nil {
		t.Fatalf("ghosttyvt.New(%d,%d,%+v): %v", cols, rows, opts, err)
	}
	t.Cleanup(term.Close)
	term.Resize(cols, rows)
	return term
}

func newMirror(t *testing.T, cols, rows int, opts ghosttyvt.Options) *mirror {
	t.Helper()
	worker := newKittyTerminal(t, cols, rows, opts)
	// The client stands in for the frontend's model: same size, no kitty.
	client := newKittyTerminal(t, cols, rows, ghosttyvt.Options{MaxScrollback: opts.MaxScrollback})

	feed := newWireFeeder(worker, 0)
	if feed == nil {
		t.Fatalf("newWireFeeder returned nil for a live terminal")
	}
	t.Cleanup(feed.close)
	return &mirror{worker: worker, client: client, feed: feed}
}

// write feeds one chunk the way the read loop does and applies the produced
// wire bytes to the client — through writeAsClient, so this harness models the
// frontend the same way the corpus and the fuzz targets do.
func (m *mirror) write(chunk string) {
	wire, resync := m.feed.feed([]byte(chunk))
	m.lastWire = append([]byte(nil), wire...)
	m.lastResync = resync
	writeAsClient(m.client, &m.clientSeg, wire)
}

func (m *mirror) agree(t *testing.T, when string) {
	t.Helper()
	if got, want := m.client.PlainText(), m.worker.PlainText(); got != want {
		t.Errorf("%s: the client history diverged from the worker history\nworker:\n%s\nclient:\n%s",
			when, want, got)
	}
	// The viewport is a separate question from the history, and the one the user
	// looks at. On the primary screen a scroll only moves the boundary between
	// them, so the two texts stay identical while the visible screen is off by
	// however many rows the client failed to scroll.
	if got, want := m.client.ViewportText(), m.worker.ViewportText(); got != want {
		t.Errorf("%s: the client viewport diverged from the worker viewport\nworker:\n%s\nclient:\n%s",
			when, want, got)
	}
	wx, wy := m.worker.CursorPos()
	cx, cy := m.client.CursorPos()
	if wx != cx || wy != cy {
		t.Errorf("%s: cursor at (%d,%d) on the client, (%d,%d) on the worker", when, cx, cy, wx, wy)
	}
}

type mirrorCase struct {
	name       string
	cols, rows int
	chunks     []string
	// check runs after the last chunk, on state the grids alone do not show.
	check func(t *testing.T, m *mirror)
}

var mirrorCases = []mirrorCase{
	{
		name: "image placed at a cursor in the middle of the screen",
		cols: 20, rows: 8,
		chunks: []string{"\x1b[4;6Hxy", kittyPlaceRGB(1, 16, 32, "")},
		check: func(t *testing.T, m *mirror) {
			if len(m.feed.deltas) != 1 || len(m.feed.deltas[0].Added) != 1 {
				t.Fatalf("placement deltas = %+v, want one added placement", m.feed.deltas)
			}
			if got := m.feed.deltas[0].Added[0].ImageID; got != 1 {
				t.Errorf("added placement image id = %d, want 1", got)
			}
		},
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
		// Leaving the alternate screen prunes the placements it held, which
		// moves ghostty's stamp on bytes no APC accounted for. The check runs;
		// the delta is a pure removal; nothing resyncs. That exemption is the
		// case's point, so it is asserted rather than left implied — the mode
		// switch is on the wire already, the client never drew the image, and no
		// row came back when it went away.
		name: "image placed on the alternate screen, then back",
		cols: 20, rows: 8,
		chunks: []string{
			"primary line\r\n",
			"\x1b[?1049h\x1b[3;3Halt",
			kittyPlaceRGB(5, 16, 32, ""),
			"\x1b[?1049l",
		},
		check: func(t *testing.T, m *mirror) {
			if len(m.feed.deltas) != 1 || !onlyRemovals(m.feed.deltas[0]) {
				t.Fatalf("leaving the alternate screen produced deltas %+v, want one pure removal: the exemption is not the thing being exercised", m.feed.deltas)
			}
		},
	},
	{
		// The same exemption reached the other way: a delete the wire could not
		// describe. The bytes go out verbatim, ghostty retires the placement,
		// and the grids stay equal — so the chunk is silent even though the
		// stamp moved on bytes nothing accounted for.
		name: "an undescribed delete of a live placement",
		cols: 20, rows: 8,
		chunks: []string{
			"\x1b[2;2Hkeep",
			kittyPlaceRGB(54, 16, 32, ""),
			undescribed("\x1b_Ga=d,d=i,i=54\x1b\\") + " tail",
		},
		check: func(t *testing.T, m *mirror) {
			if len(m.feed.deltas) != 1 || !onlyRemovals(m.feed.deltas[0]) {
				t.Fatalf("the undescribed delete produced deltas %+v, want one pure removal", m.feed.deltas)
			}
			if len(m.feed.placements) != 0 {
				t.Errorf("observed placements after the delete = %+v, want none", m.feed.placements)
			}
		},
	},
	{
		name: "chunked transmission split across feed calls",
		cols: 20, rows: 8,
		chunks: splitEvery(kittyPlaceRGB(6, 16, 32, ""), 11),
	},
	{
		// The shape that puts a plain run inside the SEGMENTER's own buffer and
		// then makes it move: a trailing ESC is held as a possible introducer,
		// the next chunk carries text plus a half APC, and holding that half
		// shifts the buffer under the text that was just emitted. Anything the
		// wire keeps a pointer to instead of copying is clobbered here.
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
		check: func(t *testing.T, m *mirror) {
			if got, want := string(m.lastWire), string(wireST); got != want {
				t.Errorf("the delete produced wire bytes %q, want just the ST %q: it moved nothing on the grid, and the ST is not about the grid", got, want)
			}
			if len(m.feed.deltas) != 1 || len(m.feed.deltas[0].Removed) != 1 {
				t.Fatalf("delete deltas = %+v, want one removed placement", m.feed.deltas)
			}
			if got := m.feed.deltas[0].Removed[0].ImageID; got != 7 {
				t.Errorf("removed placement image id = %d, want 7", got)
			}
			if len(m.feed.placements) != 0 {
				t.Errorf("observed placements after the delete = %+v, want none", m.feed.placements)
			}
		},
	},
	{
		// The shape the ST on every strip exists for, on the one column where
		// it bites. A partial character is pending when the APC arrives; the
		// APC's ESC aborts the worker's decode into a replacement character and
		// the continuation byte that follows lands alone as a second one.
		// Without the ST the client still holds the lead byte, joins it to that
		// continuation, and prints one character where the worker printed two —
		// and since neither side then holds anything pending, nothing heals it.
		//
		// Two things have to line up for it to be reachable, and both are in
		// this case on purpose. The APC must move nothing, or the feeder
		// describes the movement with a CSI whose own ESC aborts the client's
		// decode by accident — so this is a delete, not a placement. And the
		// cursor must be on the last column, or the aborted character advances
		// it and that movement gets described for the same accidental reason.
		// Here the replacement character fills the final cell and leaves the
		// cursor put with a pending wrap, so there is nothing to describe and
		// the ST is the only ESC the client gets.
		name: "a character split around an apc on the last column",
		cols: 20, rows: 8,
		chunks: []string{strings.Repeat("0", 19) + "\xe1", "\x1b_Ga=d\x1b\\", "\xa5 done"},
	},
	{
		// The leak in the marker direction. The wire carries the marker and the
		// worker terminal does not, so the marker's leading ESC ends the
		// client's part-built character and would leave the worker still
		// decoding — joining the continuation and the space into one
		// replacement character where the client resolved two. The worker-side
		// ST substitute is what keeps them equal.
		//
		// On the wrap column, so a missing substitute costs a row as well as a
		// cell. The client model in writeAsClient substitutes an ST for the
		// marker rather than dropping it, which is what lets this case — and the
		// mirror fuzzer — see the difference at all.
		name: "a prompt marker splitting a character on the wrap column",
		cols: 20, rows: 8,
		chunks: []string{strings.Repeat("0", 20) + "\xe1", "\x1b]133;A\x1b\\", "\xa5 done"},
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
		check: func(t *testing.T, m *mirror) {
			blocks := m.feed.snapshotBlocks()
			if len(blocks) != 2 {
				t.Fatalf("snapshotBlocks() = %+v, want the finished block and the new prompt", blocks)
			}
			done := blocks[0]
			if done.Command == nil || *done.Command != "icat pic.png" {
				t.Errorf("command = %v, want the cmdline the C marker carried", done.Command)
			}
			if done.ExitCode == nil || *done.ExitCode != 0 {
				t.Errorf("exit code = %v, want 0", done.ExitCode)
			}
			if done.EndRow == nil || *done.EndRow <= done.PromptRow {
				t.Errorf("block rows = prompt %d end %v: the image's scroll was not carried into the block rows",
					done.PromptRow, done.EndRow)
			}
		},
	},
}

// The three delta shapes the end-of-feed check distinguishes. onlyRemovals is
// the one it lets pass in silence: a placement retired and nothing created,
// re-placed, or retransmitted.
func onlyRemovals(delta kittyPlacementDelta) bool {
	return len(delta.Removed) > 0 && len(delta.Added) == 0 && len(delta.Updated) == 0
}

func onlyAdditions(delta kittyPlacementDelta) bool {
	return len(delta.Added) > 0 && len(delta.Removed) == 0 && len(delta.Updated) == 0
}

func onlyUpdates(delta kittyPlacementDelta) bool {
	return len(delta.Updated) > 0 && len(delta.Added) == 0 && len(delta.Removed) == 0
}

// halfOf and restOf cut an escape in two at a fixed point well inside its
// payload, so the first piece can never terminate on its own.
func halfOf(s string) string { return s[:len(s)/2] }
func restOf(s string) string { return s[len(s)/2:] }

// splitEvery cuts s into n-byte chunks, which is how a real transmission
// arrives: PTY reads land wherever they land, mid-escape included.
func splitEvery(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func TestWireFeedKeepsTheClientGridEqualToTheWorkerGrid(t *testing.T) {
	for _, tc := range mirrorCases {
		t.Run(tc.name, func(t *testing.T) {
			baseline := ghosttyvt.LiveTrackedRefs()
			m := newMirror(t, tc.cols, tc.rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})

			for i, chunk := range tc.chunks {
				m.write(chunk)
				if m.lastResync != "" {
					t.Fatalf("chunk %d forced a resync (%s); every case here is synthesizable", i, m.lastResync)
				}
				m.agree(t, fmt.Sprintf("after chunk %d", i))
			}
			if tc.check != nil {
				tc.check(t, m)
			}

			// Every ref the feeder pins around an APC is freed inside the same
			// feed call; the block table's refs are freed by close. Anything
			// still live afterwards is a leak of native memory the process
			// never gets back.
			m.feed.close()
			if got := ghosttyvt.LiveTrackedRefs(); got != baseline {
				t.Errorf("LiveTrackedRefs() = %d after the case, want the %d it started at", got, baseline)
			}
		})
	}
}

// The shipping configuration. Ghostty refuses every transmission, so there is
// nothing to observe and nothing to synthesize, and the wire is the input with
// each APC replaced in position by an ST — one per APC, unconditionally, in the
// 7-bit form. That ST is the entire wire contribution of an image here, and it
// is there for the client's UTF-8 decoder rather than for its grid: the APC's
// leading ESC aborted a partial multi-byte character in the worker, so the wire
// must abort the same one at the same offset. See writeAPC.
//
// Deleting the append in writeAPC turns this red first and most legibly, which
// is the point of asserting the exact bytes rather than a count.
func TestWireFeedStripsAPCsWithKittyDisabled(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{})

	head := "before "
	tail := " after"
	m.write(head + kittyPlaceRGB(9, 16, 32, "") + kittyPlaceRGB(10, 8, 16, "") + tail)

	want := head + string(wireST) + string(wireST) + tail
	if got := string(m.lastWire); got != want {
		t.Errorf("wire = %q, want the plain bytes with an ST per stripped APC (%q)", got, want)
	}
	if m.lastResync != "" {
		t.Errorf("resync = %q with the protocol disabled, want none", m.lastResync)
	}
	if len(m.feed.deltas) != 0 {
		t.Errorf("placement deltas = %+v with the protocol disabled, want none", m.feed.deltas)
	}
	m.agree(t, "with kitty disabled")
}

// A chunk that ends mid-APC contributes nothing to the wire: the grid effect
// has not happened yet, so there is nothing to describe. This is what makes a
// snapshot taken mid-transmission safe — it serves the pre-placement grid, and
// the completing chunk carries a higher seq.
func TestWireFeedHoldsAnUnterminatedAPCOffTheWire(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	full := kittyPlaceRGB(11, 16, 32, "")
	cut := len(full) - 6

	m.write(full[:cut])
	if len(m.lastWire) != 0 {
		t.Fatalf("a half-transmitted image put %q on the wire", m.lastWire)
	}
	m.agree(t, "mid-transmission")

	m.write(full[cut:])
	m.agree(t, "after the terminator")
	if len(m.feed.deltas) != 1 || len(m.feed.deltas[0].Added) != 1 {
		t.Errorf("deltas after the terminator = %+v, want the placement", m.feed.deltas)
	}
}

// A chunk with no kitty in it is handed straight back, so the overwhelmingly
// common case — which is every chunk of every session while the feature is
// dark — costs no copy and no allocation. ANSI escapes are in the input on
// purpose: a terminal stream is full of them, and the fast path has to survive
// an ESC that introduces something else.
func TestWireFeedPassesAPlainChunkThroughByIdentity(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	chunk := []byte("\x1b[32mgreen\x1b[0m\r\n\x1b[2;3Hmoved")

	wire, resync := m.feed.feed(chunk)

	if resync != "" {
		t.Errorf("resync = %q for plain output", resync)
	}
	if len(wire) != len(chunk) || &wire[0] != &chunk[0] {
		t.Fatalf("plain output was rewritten: got %q, want the input slice itself", wire)
	}
}

// The escape hatch, on the case that reaches it: an image taller than the
// ALTERNATE screen, which keeps no history, so the cell the cursor sat on is
// destroyed by the scroll the image itself caused. Ghostty clamps a destroyed
// tracked ref to the top of what is left instead of invalidating it, so the
// measured scroll comes out short — five rows here against a real eight — and
// the client would keep three rows the worker discarded. Detecting that the
// anchor reached the top of history is what turns a silent divergence into a
// snapshot re-push.
//
// The grids are deliberately NOT asserted equal afterwards: the wire carries no
// synthesis for this chunk and the re-push is what makes the client whole. The
// ST still goes out, because it is unconditional — it costs nothing here and a
// rule with an exception is a rule someone has to re-derive.
func TestWireFeedResyncsWhenTheAnchorHitsTheTopOfHistory(t *testing.T) {
	m := newMirror(t, 20, 6, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})

	m.write("\x1b[?1049h")
	m.write("alt0\r\nalt1\r\nalt2\r\nalt3\r\nalt4\r\n\x1b[6;1Halt5")
	m.agree(t, "with the alternate screen filled")

	m.write(kittyPlaceRGB(12, 16, 16*8, ""))

	if m.lastResync != kittyResyncAnchorClamped {
		t.Fatalf("resync = %q, want %q: an 8-row image on a 6-row alternate screen destroys the anchor",
			m.lastResync, kittyResyncAnchorClamped)
	}
	if got, want := string(m.lastWire), string(wireST); got != want {
		t.Errorf("wire = %q for an unsynthesizable chunk, want just the ST %q: the snapshot re-push carries the truth", got, want)
	}
	if worker := m.worker.PlainText(); strings.TrimSpace(worker) != "" {
		t.Errorf("worker screen = %q, want it scrolled clear: the case does not exercise a lost anchor otherwise", worker)
	}
}

// The end-of-feed check's whole truth table, on streams that reach every row of
// it. It runs only when ghostty's kitty stamp moved on bytes the wire carried
// verbatim, and from there the question is not whether an image APPEARED but
// whether anything happened that could have scrolled the grid: creating or
// re-placing a placement can, retiring one cannot. So a delta that is nothing
// but removals is silent and everything else — an empty diff under a moved
// stamp included — is a snapshot re-push.
//
// Asserted on the reason and not only on the grids, because the grids cannot
// tell the two failures apart and one of the silent rows is silent by DESIGN.
// Where nothing should resync the grids are asserted too, which is the whole
// claim those rows make.
//
// Every row also states the delta shape it means to exercise. Without that a
// row passes for the wrong reason the day ghostty reports one of these
// differently — a re-place that came back as an Added placement would still
// resync, and would stop being the Updated case the row is named for.
func TestWireFeedResyncsOnEveryUnaccountedMutationButAPureRemoval(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
		// shape names what the last chunk's observations must have found, so a
		// row cannot pass while exercising a different branch of the rule.
		shape func(kittyPlacementDelta) bool
	}{
		{
			// Placed and deleted inside one chunk: the sets on both sides of the
			// diff are empty, so no observation can name what moved and the
			// stamp is the only witness. The cursor is two columns and one row
			// apart by the end.
			name: "a placement that appears and dies inside one chunk",
			chunks: []string{
				"\x1b[2;2Hkeep",
				undescribed(kittyPlaceRGB(47, 16, 32, "")) + undescribed("\x1b_Ga=d\x1b\\"),
			},
			want: kittyResyncStampWithoutDelta,
			// No shape: the point of the row is that there is no delta at all.
		},
		{
			// A live key put somewhere new. Nothing is added; the diff reports
			// Updated, and the placement moved the cursor on the worker alone.
			name: "a live placement re-placed at a new position",
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(52, 16, 32, ",p=7"),
				"\x1b[6;9Hmove" + undescribed("\x1b_Ga=p,i=52,p=7\x1b\\"),
			},
			want:  kittyResyncUndescribedImage,
			shape: onlyUpdates,
		},
		{
			// New pixels under a live key: ImageGeneration moves and the key
			// does not, which is Updated again.
			name: "an image retransmitted under a live placement id",
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(53, 16, 32, ""),
				undescribed(kittyTransmitRGB(53, 8, 16)),
			},
			want:  kittyResyncUndescribedImage,
			shape: onlyUpdates,
		},
		{
			// The original shape, kept in the table so widening the rule cannot
			// drop it: a placement created by an APC the wire carried verbatim.
			name:   "a placement created by an undescribed apc",
			chunks: []string{"\x1b[2;2Hkeep", undescribed(kittyPlaceRGB(55, 16, 32, ""))},
			want:   kittyResyncUndescribedImage,
			shape:  onlyAdditions,
		},
		{
			// The settle runs at every described dispatch too, and it must not
			// turn an ordinary one into a re-push: writeAPC accounts for its own
			// move, so by the time the next settle looks there is nothing left
			// unaccounted. This row is the guard on that.
			name:   "a placement created by a described apc",
			chunks: []string{"\x1b[2;2Hkeep", kittyPlaceRGB(60, 16, 32, "")},
			want:   "",
			shape:  onlyAdditions,
		},
		{
			name: "a live placement deleted by an undescribed apc",
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(54, 16, 32, ""),
				undescribed("\x1b_Ga=d,d=i,i=54\x1b\\") + " tail",
			},
			want:  "",
			shape: onlyRemovals,
		},
		{
			name: "live placements pruned by leaving the alternate screen",
			chunks: []string{
				"primary line\r\n",
				"\x1b[?1049h\x1b[3;3Halt",
				kittyPlaceRGB(56, 16, 32, ""),
				"\x1b[?1049l",
			},
			want:  "",
			shape: onlyRemovals,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
			for i, chunk := range tc.chunks[:len(tc.chunks)-1] {
				m.write(chunk)
				if m.lastResync != "" {
					t.Fatalf("chunk %d resynced (%s) before the chunk under test", i, m.lastResync)
				}
			}
			m.write(tc.chunks[len(tc.chunks)-1])

			if m.lastResync != tc.want {
				t.Fatalf("resync = %q, want %q", m.lastResync, tc.want)
			}
			if tc.shape == nil {
				if len(m.feed.deltas) != 0 {
					t.Fatalf("deltas = %+v, want none: the diff has to be blind here, or the stamp is not what fired", m.feed.deltas)
				}
			} else if len(m.feed.deltas) != 1 || !tc.shape(m.feed.deltas[0]) {
				t.Fatalf("deltas = %+v: not the shape this row is named for", m.feed.deltas)
			}
			if tc.want != "" {
				// The wire deliberately carries no synthesis for the chunk, so
				// the grids are allowed to differ; the snapshot re-push is what
				// makes the client whole.
				return
			}
			m.agree(t, "after a chunk that only retired placements")
		})
	}
}

// The accounting rule, at the shape that used to break it: an undescribed
// dispatch followed by a described one in the SAME chunk.
//
// writeAPC ends by taking the terminal's kitty stamp as its own, and the stamp
// is one number for the whole terminal — so a described APC used to absorb the
// undescribed dispatch that ran before it, leaving the end-of-feed check a
// stamp that already looked accounted for and the image on the worker's grid
// alone. Settling at writeAPC's entry is what splits the two.
//
// Two claims, and the second is the one a naive fix would miss. The chunk must
// resync for the image the wire could not carry, AND the APC that settled it
// must still be described in full: a resync says what the wire could not
// express, it is never a reason to stop expressing what it can. Pinned against
// a control that runs the same placement alone from the same cursor — the
// undescribed one carries C=1 so it moves nothing — which makes the expected
// wire exact rather than a shape.
func TestWireFeedStillDescribesTheAPCThatSettlesAnUndescribedOne(t *testing.T) {
	const prefix = "\x1b[2;2Hkeep"
	described := kittyPlaceRGB(59, 16, 32, "")
	silent := undescribed(kittyPlaceRGB(58, 16, 32, ",C=1"))

	control := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	control.write(prefix)
	control.write(described)
	if control.lastResync != "" {
		t.Fatalf("the control resynced (%s); it places one ordinary image", control.lastResync)
	}
	control.agree(t, "with one described placement")
	describedWire := string(control.lastWire)

	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	m.write(prefix)
	m.write(silent + described)

	if m.lastResync != kittyResyncUndescribedImage {
		t.Fatalf("resync = %q, want %q: the first placement reached the terminal on bytes the wire carried verbatim",
			m.lastResync, kittyResyncUndescribedImage)
	}
	if got, want := string(m.lastWire), silent+describedWire; got != want {
		t.Errorf("wire = %q,\nwant the undescribed bytes verbatim followed by exactly the description that placement gets on its own (%q)", got, want)
	}
}

// The receipt behind the kittyResyncScrollClamped threshold, and the reason it
// is the screen height rather than a guess. `r=N` makes a 2x2 image claim N
// rows, so the scroll a placement causes is dialable one row at a time; the
// tallest scroll a single SU still reproduces is the number the tripwire is set
// at.
//
// Measured on this ghostty pin, cursor on row 1, image placed there:
//
//	8-row screen:  r=14 -> SU 8 agrees,  r=15 -> SU 9 lost a row of history
//	12-row screen: r=22 -> SU 12 agrees, r=23 -> SU 13 lost a row of history
//
// Both sides are pinned because both are wrong to move. One row tighter and the
// wire would resync over a scroll it could have carried; one row looser and it
// would emit a clamped SU and diverge in silence, which is what this used to do.
func TestWireFeedSynthesizesTheLargestScrollOneSUCarries(t *testing.T) {
	for _, tc := range []struct {
		rows int
		// carried is the tallest image whose scroll one SU reproduces; overflow
		// is the next row up, where SU would be clamped.
		carried, overflow int
	}{
		{rows: 8, carried: 14, overflow: 15},
		{rows: 12, carried: 22, overflow: 23},
	} {
		t.Run(fmt.Sprintf("%d rows", tc.rows), func(t *testing.T) {
			// The resync is the placement chunk's; the trailing text after it is
			// only what makes the scroll reach history where it can be compared.
			place := func(rowCount int) (*mirror, string) {
				m := newMirror(t, 20, tc.rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
				m.write("\x1b[2;2Hkeep")
				m.write(kittyPlaceRGB(uint32(80+rowCount), 16, 32, fmt.Sprintf(",r=%d", rowCount)))
				resync := m.lastResync
				m.write("\r\ntail")
				return m, resync
			}

			at, resync := place(tc.carried)
			if resync != "" {
				t.Fatalf("r=%d resynced (%s) on a %d-row screen: one SU still carries that scroll",
					tc.carried, resync, tc.rows)
			}
			at.agree(t, fmt.Sprintf("after an r=%d placement", tc.carried))

			if _, resync := place(tc.overflow); resync != kittyResyncScrollClamped {
				t.Errorf("r=%d resync = %q, want %q: SU cannot carry a scroll taller than the %d-row screen",
					tc.overflow, resync, kittyResyncScrollClamped, tc.rows)
			}
		})
	}
}

// DECLRMM is the tripwire, and the same stream without it is the control. A
// scroll confined to the margin box moves text between rows without moving the
// rows, so the tracked pair reports nothing and the wire carries no SU for it —
// the client's text stays put under a cursor that agrees, which is a divergence
// no assertion on the cursor would catch.
//
// The control is what keeps this honest: it is the identical stream on a
// terminal with no margins set, and it must stay silent AND byte-equal. A
// tripwire that fired on every placement would pass the first half of this test
// and fail the second.
func TestWireFeedResyncsWhileLeftRightMarginsAreSet(t *testing.T) {
	const bottom = "\x1b[32;5Hxy"
	place := kittyPlaceRGB(65, 16, 32, "")

	control := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	control.write("\x1b[1;1Htop" + bottom)
	control.write(place)
	if control.lastResync != "" {
		t.Fatalf("the control resynced (%s): without margins this is an ordinary bottom-row placement", control.lastResync)
	}
	control.agree(t, "with no margins set")

	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	m.write("\x1b[1;1Htop\x1b[?69h\x1b[4;14s" + bottom)
	m.write(place)
	if m.lastResync != kittyResyncMarginMode {
		t.Fatalf("resync = %q, want %q: the placement scrolled the margin box and nothing measured it",
			m.lastResync, kittyResyncMarginMode)
	}
	// Described in full anyway: the cursor moves are the part of the measurement
	// margins do not spoil, and they are worth having until the snapshot lands.
	// The one thing missing against the control is its SU — the scroll that
	// happened inside the margin box and that nothing on this side could see,
	// which is the whole reason the reason string exists.
	if got, want := string(control.lastWire), string(wireST)+"\x1b[2S\x1b[1A\x1b[2C"; got != want {
		t.Fatalf("control wire = %q, want %q: without margins the same placement scrolls two rows and says so", got, want)
	}
	if got, want := string(m.lastWire), string(wireST)+"\x1b[1A\x1b[2C"; got != want {
		t.Errorf("wire = %q, want the control's cursor moves without its SU (%q): a resync is not a stop order",
			got, want)
	}
	if worker, client := m.worker.ViewportText(), m.client.ViewportText(); worker == client {
		t.Errorf("the grids agree, so the case no longer exercises an unmeasurable margin scroll:\n%s", worker)
	}
}

// Ghostty answers a support query from the terminal's own response channel, and
// the read loop forwards it to the program. Stripping the APC from the WIRE
// must not touch that: a program that asks whether images are supported still
// gets its answer from the one parser in the system.
func TestWireFeedKeepsKittyResponsesFlowingToTheProgram(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	m.worker.DrainResponses()

	m.write("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\")

	resp := string(m.worker.DrainResponses())
	if !strings.Contains(resp, "\x1b_Gi=31;OK") {
		t.Errorf("support query response = %q, want ghostty's OK for image 31", resp)
	}
	if got, want := string(m.lastWire), string(wireST); got != want {
		t.Errorf("wire = %q for the query APC, want just the ST %q: the query is answered to the program, never to the client", got, want)
	}
}

// The pre-ST writeAPC gives the WORKER is licensed by one property: from ground
// it does nothing to the grid except end a part-built character. Measured rather
// than asserted from the spec, because everything downstream — the cursor pin,
// the tracked ref, the early exit — is measured against the state it leaves.
//
// Checked at every column, because the one place it is NOT inert is the one that
// matters: on the last column the replacement character it resolves fills the
// final cell, and on the wrap column it commits the deferred wrap.
func TestWireFeedPreSTOnlyEndsTheDecode(t *testing.T) {
	for _, pending := range []string{"", "\xe1", "\xc2", "\xf0\x9f"} {
		for n := 17; n <= 21; n++ {
			prefix := strings.Repeat("0", n) + pending

			plain := newKittyTerminal(t, 20, 6, ghosttyvt.Options{})
			plain.Write([]byte(prefix))

			withST := newKittyTerminal(t, 20, 6, ghosttyvt.Options{})
			withST.Write([]byte(prefix))
			withST.Write(wireST)

			px, py := plain.CursorPos()
			sx, sy := withST.CursorPos()
			sameGrid := plain.PlainText() == withST.PlainText() && px == sx && py == sy

			if pending == "" {
				// Nothing held: the ST must be invisible.
				if !sameGrid {
					t.Errorf("n=%d, nothing pending: the ST moved the grid\nplain:  %q (%d,%d)\nwithST: %q (%d,%d)",
						n, plain.PlainText(), px, py, withST.PlainText(), sx, sy)
				}
				continue
			}
			// Something held: the ST must resolve it into exactly one
			// replacement character and nothing else.
			if sameGrid {
				t.Errorf("n=%d, %q pending: the ST changed nothing, so the decode was never ended", n, pending)
				continue
			}
			if got, want := len([]rune(strings.ReplaceAll(withST.PlainText(), "\n", ""))),
				len([]rune(strings.ReplaceAll(plain.PlainText(), "\n", "")))+1; got != want {
				t.Errorf("n=%d, %q pending: grid gained %d cells, want exactly 1 replacement character\nplain:  %q\nwithST: %q",
					n, pending, got-want+1, plain.PlainText(), withST.PlainText())
			}
		}
	}
}

// The ordering the fix rests on, at the shape that exposes it. On the wrap
// column the pre-ST is what moves the cursor — it commits the deferred wrap —
// and the APC that follows does nothing at all in the shipping configuration.
// Pinned AFTER the ST, the feeder therefore measures zero movement and takes the
// early exit, leaving the wire carrying the ST alone.
//
// Pinned before it, that same movement would be read as the image's, described
// on the wire, and applied a second time by a client that had already performed
// the abort itself — one row too far. This test is the guard on the ordering,
// not on the bytes.
func TestWireFeedPinsTheCursorAfterTheDecodeEnds(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{})

	m.write(strings.Repeat("0", 20) + "\xe1")
	m.write(kittyDirectRGB)

	if got, want := string(m.lastWire), string(wireST); got != want {
		t.Errorf("wire = %q, want the ST alone (%q): the APC moved nothing, so nothing should be described", got, want)
	}
	if m.lastResync != "" {
		t.Errorf("resync = %q, want none: the early exit handles this", m.lastResync)
	}
	if x, y := m.worker.CursorPos(); x != 1 || y != 1 {
		t.Errorf("worker cursor = (%d,%d), want (1,1): the abort commits the pending wrap", x, y)
	}
	m.agree(t, "after an APC on the wrap column")
}

// The worker-side substitute for a withheld marker must be written BEFORE the
// block table pins its position, or the block records the cell the cursor sat on
// before the decode ended rather than the one the marker refers to.
//
// Asserted on the pin, not on the grid: the grid agrees either way here, so a
// text-only check would pass with the write and the pin in the wrong order.
func TestWireFeedPinsTheBlockAfterTheDecodeEnds(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{})

	// A character left part-built on the wrap column, so ending it moves the
	// cursor to the next row — which is the row the prompt renders on.
	m.write(strings.Repeat("0", 20) + "\xe1")
	m.write("\x1b]133;A\x1b\\")

	blocks := m.feed.snapshotBlocks()
	if len(blocks) != 1 {
		t.Fatalf("snapshotBlocks() = %+v, want the one open prompt", blocks)
	}
	if got := blocks[0].PromptRow; got != 1 {
		t.Errorf("prompt row = %d, want 1: the marker refers to the row the cursor reached after the decode ended, not the row it left", got)
	}
	m.agree(t, "after a marker on the wrap column")
}
