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
			// cursor sits on absolute row 0, and a placement that scrolls pushes
			// the anchor cell into retained history — where the pin keeps
			// reporting a real row, so the scroll is fully visible and the wire
			// carries it. The clamp guard deliberately does not fire here; the
			// alternate-screen entry below is the one it exists for.
			name: "first line of a fresh primary screen, image tall enough to scroll",
			cols: 20, rows: 8,
			chunks: []string{kittyPlaceRGB(22, 16, 160, "")},
		},
		{
			// The two smallest streams the fuzzer found against the old
			// byte-pattern segmenter, kept as corpus entries because they are
			// the exact shapes a framing regression reintroduces. Here the ESC
			// backslash ends the SOS string, NOT an APC: cutting the APC pattern
			// out took the SOS's terminator with it and the client swallowed
			// everything after.
			name: "an apc pattern inside an sos string",
			cols: 20, rows: 8,
			chunks: []string{"\x1bX\x1b_G\x1b\\0 done"},
		},
		{
			// And here a C1 control ends the APC for ghostty but did not for the
			// segmenter, so the wire lost the bytes that print.
			name: "a c1 control ends an apc before its terminator",
			cols: 20, rows: 8,
			chunks: []string{"\x1b_Ga=T,i=41,f=24,t=d,s=8,v=16;\x840 done\x1b\\"},
		},
		{
			// The same disagreement one layer up. The first APC is never
			// terminated: the ESC that abandons it is the second one's
			// introducer, so cutting the second out would take the first's exit
			// with it and neither can be extracted. Every byte goes to the wire
			// verbatim, which keeps the parsers in step — but ghostty still
			// dispatches the second command and places its image, and the client
			// cannot, so the feeder resyncs. Reachable only once kitty storage is
			// live.
			name: "an apc opened by the escape that abandoned the previous one",
			cols: 20, rows: 8,
			chunks: []string{"A" + kittyIntro + "a=T;AA" + kittyPlaceRGB(43, 8, 16, "") + "B"},
		},
		{
			// An APC ghostty DOES parse as kitty but the segmenter must not cut
			// out, because the ESC that introduces it is also what cancels the
			// unfinished CSI before it. The bytes go to the wire whole, which
			// keeps the two parsers in step — but the client cannot parse kitty,
			// so the image the worker placed is on one grid only and the feeder
			// resyncs. Reachable only once kitty storage is live.
			name: "an apc that cancels an unfinished csi",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[1" + kittyPlaceRGB(44, 8, 16, "") + " done"},
		},
		{
			// A placement that appears and DIES inside one chunk. Both APCs are
			// undescribed, so the wire carries their bytes verbatim and the
			// client does nothing with either; ghostty places the image, moves
			// the cursor past it, and then deletes it. The set before the chunk
			// and the set after are both empty, so the placement diff sees
			// nothing at all — the generation stamp, which moved four times, is
			// the only witness that anything happened.
			name: "an undescribed image displayed and deleted in one chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				undescribed(kittyPlaceRGB(47, 16, 32, "")) + undescribed("\x1b_Ga=d\x1b\\"),
			},
		},
		{
			// A live placement PUT somewhere new by an undescribed APC. The
			// {ImageID, PlacementID} key does not move, so the diff reports it
			// as Updated and nothing as Added — the shape a check keyed on
			// appearance alone is blind to, while the placement advances the
			// worker's cursor two columns and a row past the client's.
			name: "an undescribed re-place of a live placement at a new position",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(52, 16, 32, ",p=7"),
				"\x1b[6;9Hmove" + undescribed("\x1b_Ga=p,i=52,p=7\x1b\\"),
			},
		},
		{
			// New pixels under a live placement id: ImageGeneration moves and
			// the key does not, so this is Updated as well — and the placement's
			// footprint shrinks from 2x2 cells to 1x1 with the image. Nothing
			// scrolls here and the resync is charged anyway: the end-of-feed
			// check cannot tell a retransmission from a re-place, and an
			// undescribed APC is rare enough to pay for the difference.
			name: "an undescribed retransmission under a live placement id",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(53, 16, 32, ""),
				undescribed(kittyTransmitRGB(53, 8, 16)),
			},
		},
		{
			// The stamp is claimed per dispatch, not per chunk. Ghostty
			// DISPATCHES a kitty command when a stray ESC ends the APC, so the
			// image here lands from bytes the segmenter had to replay as plain
			// — and the extractable, empty APC that follows in the same chunk
			// would take the whole stamp move as its own if it did not settle
			// the books first. Found by FuzzKittyWireMirror, where it left the
			// worker at (2,1) against a client that never moved.
			name: "an undescribed image, then an extractable apc in the same chunk",
			cols: 20, rows: 8,
			chunks: []string{
				strings.TrimSuffix(kittyPlaceRGB(57, 16, 32, ""), "\x1b\\") + "\x1bi" + "\x1b_G\x1b\\",
			},
		},
		{
			// The general shape: one undescribed placement and one described
			// placement in a single chunk. The chunk resyncs for the first, and
			// the second is still described on the wire — a resync is a
			// statement about what the wire could not carry, never a reason to
			// stop carrying what it can.
			name: "an undescribed placement and a described one in the same chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				undescribed(kittyPlaceRGB(58, 16, 32, ",C=1")) + kittyPlaceRGB(59, 16, 32, ""),
			},
		},
		{
			// The exemption, pinned green. An undescribed DELETE moves the stamp
			// too, and its diff is nothing but a removal: retiring a placement
			// gives back no rows, so nothing scrolled, and the client learns the
			// set emptied from the placement fan-out rather than from the wire.
			// The grids agree, so this entry is replayed rather than exempt —
			// which is what makes a rule that resyncs on prunes fail here.
			name: "an undescribed delete of a live placement",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(54, 16, 32, ""),
				undescribed("\x1b_Ga=d,d=i,i=54\x1b\\") + " tail",
			},
		},
		{
			// Left/right margins plus origin mode, which is where an absolute
			// column move went wrong: the worker reports a column counted from
			// the screen edge and a client with DECLRMM on reads `CHA` from the
			// LEFT MARGIN, so the same number means two places. Recorded at
			// worker column 11 against a client at 13 before synthesis went
			// relative. Margins 4..14 with the cursor inside them.
			//
			// These three now record a resync rather than a replayed grid: the
			// margin tripwire fires on any described dispatch while DECLRMM is
			// on, and it does not ask whether this particular one scrolled the
			// box. What they pin is that the tripwire covers the mode wherever
			// it appears — inside margins, outside them, with origin mode and
			// without. The relative column move they were written for is pinned
			// by every non-margin entry in this file, and it still governs a
			// client whose mode read fails.
			name: "placement inside left and right margins under origin mode",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[?69h\x1b[4;14s\x1b[?6h\x1b[3;2Hxy",
				kittyPlaceRGB(62, 16, 32, ""),
			},
		},
		{
			// The same margins with origin mode OFF, and measured rather than
			// assumed: this one does NOT displace an absolute column — under
			// the absolute CHA synthesis used to emit, its replay landed on the
			// worker's column. Margins alone are not enough; it takes origin
			// mode with them. Kept so the two modes are pinned apart instead of
			// only ever tested together, and so the day ghostty makes DECLRMM
			// bite on its own the corpus says so.
			name: "placement inside margins with origin mode off",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[?69h\x1b[4;14s\x1b[6;6Hxy",
				kittyPlaceRGB(63, 16, 32, ""),
			},
		},
		{
			// A placement wide enough to push the cursor toward the right
			// margin, which is the case a relative move has to carry as far as
			// an absolute one did: the step is measured from where the client's
			// own cursor already stands, not from either edge.
			name: "wide placement pushing the cursor right inside margins",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[?69h\x1b[2;18s\x1b[?6h\x1b[2;2Hxy",
				kittyPlaceRGB(64, 48, 32, ""),
			},
		},
		{
			// The margin tripwire on the stream that exposed the class: a
			// placement at the bottom of the margin box scrolls the columns
			// inside it, which the row-based measurement cannot see. `top`
			// outside the box is the tell — it stays put on the worker while
			// the text inside the box climbs. Resync-exempt from replay, which
			// is what the resync is for.
			name: "placement scrolling the box while left and right margins are set",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[1;1Htop\x1b[?69h\x1b[4;14s\x1b[32;5Hxy",
				kittyPlaceRGB(65, 16, 32, ""),
			},
		},
		{
			// kitty's `r=` makes a 2x2 image claim 15 rows on an 8-row screen.
			// This used to scroll proportionally — 9 rows into history where one
			// SU could only carry 8 — and was the shape that reached
			// kittyResyncScrollClamped. On this ghostty pin the scroll no longer
			// tracks the claimed row count and stays inside the screen, so one
			// SU carries it and nothing resyncs. Kept at the same shape: it is
			// the stream that would trip the tripwire again if that changed. The
			// trailing text is what makes the scroll reach history at all.
			name: "placement claiming far more rows than the screen holds",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(66, 16, 32, ",r=15"),
				"\r\ntail",
			},
		},
		{
			// The pending-wrap tripwire, on the shape FuzzKittyWireMirror found
			// (62f19a45d7a5c8c7): exactly a screen width fills the row and leaves
			// the wrap deferred, and the placement consumes it on the worker
			// alone. The trailing character is what makes the difference visible
			// — it wraps on the client and overwrites the last column on the
			// worker. Resync-exempt from replay, which is what the resync is for.
			name: "placement on a row that is already full",
			cols: 20, rows: 8,
			chunks: []string{
				strings.Repeat("0", 20),
				kittyPlaceRGB(67, 8, 16, ""),
				"0",
			},
		},
		{
			// The mode has to survive a chunk boundary in every one of these
			// states, which is the whole reason it is carried on the segmenter
			// rather than recomputed per call. The APC pattern inside the OSC is
			// text to ghostty; the one after it is a real kitty APC introduced
			// from escape state, so it reaches the terminal whole and resyncs.
			name: "a foreign string split across feed chunks around an apc pattern",
			cols: 20, rows: 8,
			chunks: []string{"\x1b]0;ti", "tle\x1b_Ga=T,i=42;AA", "\x07", kittyPlaceRGB(45, 8, 16, ""), " done"},
		},
		{
			// The two streams that blocked the whole-path soak, both found by
			// the fuzzer against the OSC 133 byte-pattern scanner and neither
			// carrying a kitty escape at all. Here the scanner stepped over a
			// stray ESC hunting for a terminator and swallowed what ghostty
			// printed.
			name: "a marker cut short by a stray escape",
			cols: 20, rows: 8,
			chunks: []string{"\x1b]133;A\x1b0Z done"},
		},
		{
			// And here it stripped a marker whose ESC ] was never in ground —
			// the lone ESC before it means ghostty is mid-escape, so removing
			// the marker removed that escape's exit too.
			name: "a marker whose introducer was never in ground",
			cols: 20, rows: 8,
			chunks: []string{"\x1b\x1b]133;A\x1b\\00 done"},
		},
		{
			// The permanent shape of the decoder leak, on the column where it
			// bites. `\xe1` opens a three-byte character; the APC's ESC ends that
			// decode for the worker, which resolves it as a replacement
			// character, and `\xa5` then arrives as a stray continuation and
			// becomes a second one. Send nothing in the APC's place and the client
			// never sees an ESC, so it keeps holding `\xe1` and joins the
			// continuation into a different character — permanently, with no later
			// byte able to heal it.
			//
			// The cursor sits on the LAST column deliberately. Anywhere else the
			// ended character advances the cursor, the feeder observes that and
			// synthesizes a CHA, and the CHA's own ESC ends the client's decode by
			// accident — so the defect is invisible. Here the replacement character
			// lands in the final cell and leaves the cursor where it was, pending
			// wrap, so observed movement is zero and the wire ST is the only ESC
			// the client gets.
			name: "a character split around a stripped apc at the last column",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 19) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			// The transient half of the same defect: nothing follows the APC, so
			// the client is left holding an incomplete sequence the worker has
			// already resolved. An attach would paper over this one — the dump is
			// the worker's — but the wire should not need rescuing.
			name: "an incomplete character left pending by a stripped apc at the last column",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 19) + "\xe1", kittyDirectRGB},
		},
		{
			// Zeros ladder, 18: two columns short of the wrap: the ended character advances the
			// cursor, the feeder describes the movement, and the described
			// column is absolute so applying it after the client's own abort is
			// idempotent.
			name: "the zeros ladder at 18, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 18) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			// Zeros ladder, 19: the last column: the replacement character fills the final cell and
			// leaves the cursor put with a pending wrap, so nothing is
			// synthesized and the wire ST is the only ESC.
			name: "the zeros ladder at 19, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 19) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			// Zeros ladder, 20: the wrap column, where the abort COMMITS the deferred wrap and moves
			// the cursor to the next row. That movement is the image's only if
			// the abort happens before the feeder pins the cursor; pinned after,
			// it is described on the wire and the client — which performs the
			// same abort itself — applies it a second time and lands a row low.
			name: "the zeros ladder at 20, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 20) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			// Zeros ladder, 21: one past the wrap: the wrap is already committed before the APC
			// arrives, so the abort is an ordinary in-row advance again.
			name: "the zeros ladder at 21, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 21) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			// The marker's leading ESC ends a part-built character. Both models
			// receive the same marker bytes, so this entry asserts their decoders
			// resolve the two replacement characters identically.
			name: "a prompt marker splitting a character",
			cols: 20, rows: 8,
			chunks: []string{"000\xe1", "\x1b]133;A\x1b\\", "\xa5 done"},
		},
		{
			// The same, at the wrap column, where the ended character also
			// commits the deferred wrap — so a worker that kept decoding would
			// differ by a row as well as a cell.
			name: "a prompt marker splitting a character at the wrap column",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 20) + "\xe1", "\x1b]133;A\x1b\\", "\xa5 done"},
		},
		{
			// A C1-terminated APC. The worker consumes 0x9c as ST, but the wire
			// replacement is always the 7-bit form: 0x9c alone on the wire is
			// not an ST to the client, it is a stray UTF-8 continuation byte
			// that would put a replacement character on the grid.
			name: "a c1-terminated apc still leaves the seven-bit st",
			cols: 20, rows: 8,
			chunks: []string{"ab", kittyIntro + "a=T,f=24,s=2,v=2;QUJDRA==\x9c", " done"},
		},
		{
			// A prompt drawn straight after output that did not end in a
			// newline — the shape every shell produces when a command's last
			// write has no trailing \n.
			//
			// `OSC 133;A` is not grid-inert: with the cursor mid-line it breaks
			// the line because a prompt starts on a fresh one. The worker and
			// app share a Ghostty source pin and both consume the marker, so this
			// entry tripwires that agreement against the real WASM model.
			name: "a prompt marker after output with no trailing newline",
			cols: 20, rows: 8,
			chunks: []string{"out", "\x1b]133;A\x1b\\", "$ ls\r\n", "\x1b]133;D;0\x07"},
		},
		{
			// A marker split across feed chunks in every state it can hold
			// bytes in — mid-introducer, mid-payload, and on a partial
			// terminator — beside a real image, so the wire rewrite and the
			// marker handling have to interleave correctly.
			name: "markers split across feed chunks around an image",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b]13", "3;A\x07$ \x1b]133;C;cmdline_url=ic",
				"at\x07", kittyPlaceRGB(46, 16, 32, ""),
				"\r\n\x1b]133;D;0\x1b", "\\done",
			},
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
	feeder := newWireFeeder(worker, 0, nil, 0)
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

// writeAsClient writes wire bytes into a native terminal standing in for the
// frontend's model. The two runtimes share ghostty-vt.pin, so the honest model
// is the raw wire. The real WASM replay remains the authority.
func writeAsClient(client *ghosttyvt.Terminal, wire []byte) {
	client.Write(wire)
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
		writeAsClient(client, wire)
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

// --- The framing half of the same parity question -------------------------
//
// The corpus above proves the synthesized bytes mean the same thing to both
// runtimes. This proves the segmenter cuts them out in the right place, which
// is a question only ghostty's own parser can answer.
//
// Extraction removes bytes from the wire, so it is safe exactly when the
// removed run is a whole sequence to both parsers. The leading ESC of a kitty
// APC does double duty outside ground — it also ends whatever was open — so
// taking the APC away would take that exit with it. kittyseg.go therefore
// tracks where ghostty's parser stands and extracts only from ground. Every
// transition in that machine is measured, and this is the measurement: for a
// large set of byte sequences, the segmenter's idea of ground must equal
// ghostty's, byte for byte.

// kittyGroundProbeBytes is the alphabet the exhaustive walk below explores. It
// carries one byte from every class the machine distinguishes: the string and
// CSI introducers in both their 7-bit and raw C1 forms, the terminators, the
// aborting controls, an escape intermediate, a final, and ordinary text.
var kittyGroundProbeBytes = []byte{
	0x00, 0x07, 0x18, 0x1a, 0x1b, 0x20, 0x28, 0x30, 0x47, 0x50, 0x5b, 0x5c,
	0x5d, 0x5e, 0x5f, 0x6d, 0x7f, 0x80, 0x84, 0x90, 0x98, 0x9b, 0x9c, 0x9d,
	0x9e, 0x9f, 0xff,
}

// kittyGroundNamedPrefixes puts the walk down inside each parser state so the
// full 256-byte sweep that follows starts somewhere interesting rather than
// only in ground.
var kittyGroundNamedPrefixes = []string{
	"",
	"\x1b",
	"\x1b\x1b",
	"\x1b(",
	"\x1b[1",
	"\x1b]0;t",
	"\x1bP1$r",
	"\x1bXsos",
	"\x1b^pm",
	"\x1b_Zvendor",
	"\x1b_Ga=T,f=24;AA",
	"\x1b_Ga=T;AA\x1b",
	"\x1b_Ga=T;AA\x18",
	"\x1b]0;t\x1b",
	"\x1b[1\x1b",
	// OSC 133 shapes. The marker states hold bytes, so a disagreement about
	// where one ends is a disagreement about which bytes were removed: partway
	// through the introducer, past it, and inside the payload.
	"\x1b]1",
	"\x1b]133",
	"\x1b]133;",
	"\x1b]133;A",
	"\x1b]133;C;cmdline_url=ma",
	"\x1b]133;A\x1b",
	"\x1b\x1b]133;A",
}

// ghosttyInGround reports whether ghostty's parser is in ground after input, by
// the only signal the API exposes: a printable ADVANCES THE CURSOR there and
// nowhere else — a string swallows it, an escape or CSI consumes it as a final.
//
// The cursor, not the printed text: the input is arbitrary under fuzzing and may
// contain the probe character itself, which a "did it appear on screen" test
// cannot tell apart from one the probe wrote. The CR normalizes the column
// first, so the one printable that MOVES the cursor as a final byte (CSI Z,
// cursor backward tab) has nowhere left to go and cannot read as ground.
func ghosttyInGround(t *testing.T, input string) bool {
	t.Helper()
	term, err := ghosttyvt.New(20, 4, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}
	defer term.Close()
	term.Write([]byte(input))
	term.Write([]byte("\r"))
	beforeCol, beforeRow := term.CursorPos()
	term.Write([]byte("Z"))
	afterCol, afterRow := term.CursorPos()
	return afterCol != beforeCol || afterRow != beforeRow
}

// segmenterInGround runs input through a fresh segmenter and reports whether it
// would extract an APC introduced by the very next byte. Held bytes count as
// not-ground: a held ESC is one the segmenter has not classified yet, and
// ghostty has already left ground on it.
func segmenterInGround(t *testing.T, input string) bool {
	t.Helper()
	var seg feedSegmenter
	rebuilt := make([]byte, 0, len(input))
	seg.Feed([]byte(input), func(e feedSegment) {
		rebuilt = append(rebuilt, e.Bytes...)
	})
	// The byte-exactness invariant, checked on every case for free: a machine
	// that tracked state correctly while losing a byte would still be a silent
	// divergence.
	if got := string(rebuilt) + string(seg.pending); got != input {
		t.Fatalf("emissions rebuild %q, want %q", got, input)
	}
	return seg.mode == kittySegGround && len(seg.pending) == 0
}

func assertGroundAgrees(t *testing.T, input string) {
	t.Helper()
	want := ghosttyInGround(t, input)
	if got := segmenterInGround(t, input); got != want {
		t.Errorf("after %q: segmenter ground=%v, ghostty ground=%v", input, got, want)
	}
}

// TestKittySegmenterGroundMatchesGhostty is the falsification gate for every
// transition in kittyseg.go's machine. A rule that is wrong about which byte
// ends which sequence shows up here as a disagreement on the exact input that
// exposes it, which is also the input to add to the corpus.
//
// Do not read a pass here as "the segmenter is right". It asks two questions —
// does the machine agree with ghostty about where a sequence ENDS, and did
// every byte survive — and neither one can see which DISPOSITION a byte got.
// Change a rule so a marker is extracted where it should have been replayed as
// plain, and this gate stays green: the parser lands in the same state either
// way and the bytes still reconstruct. Measured, not assumed — flipping the
// CAN/SUB rule in kittySegOSC133Body left every assertion here passing, and
// only the battery next door went red. Exhaustive over inputs is not
// exhaustive over behavior; the battery and the parity corpus are what pin
// which bytes go to the terminal and which go to the wire.
func TestKittySegmenterGroundMatchesGhostty(t *testing.T) {
	// Every byte, from every named state: the per-state exit sets.
	for _, prefix := range kittyGroundNamedPrefixes {
		for b := range 0x100 {
			assertGroundAgrees(t, prefix+string([]byte{byte(b)}))
		}
	}
	// Two bytes from every named state: enough to open a sequence from inside
	// another one and then close it, which is the only way to tell a C1
	// introducer apart from a byte the current sequence swallows.
	for _, prefix := range kittyGroundNamedPrefixes {
		for _, a := range kittyGroundProbeBytes {
			for _, b := range kittyGroundProbeBytes {
				assertGroundAgrees(t, prefix+string([]byte{a, b}))
			}
		}
	}
	// Every three-byte sequence over the interesting alphabet: the transitions
	// BETWEEN states, including the ones no named prefix reaches.
	for _, a := range kittyGroundProbeBytes {
		for _, b := range kittyGroundProbeBytes {
			for _, c := range kittyGroundProbeBytes {
				assertGroundAgrees(t, string([]byte{a, b, c}))
			}
		}
	}
	// The introducer is three bytes long, so the sequences that matter most are
	// the ones that reach a kitty APC and then leave it.
	for _, a := range kittyGroundProbeBytes {
		for _, b := range kittyGroundProbeBytes {
			assertGroundAgrees(t, "\x1b_G"+string([]byte{a, b}))
			assertGroundAgrees(t, "\x1b_Ga=T;AA"+string([]byte{a, b}))
			assertGroundAgrees(t, string([]byte{a, b})+"\x1b_G")
		}
	}
}
