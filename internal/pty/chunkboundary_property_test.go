package pty

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Chunk-boundary invariance: what the Go scanners report must not depend on
// where the read loop happened to cut the stream.
//
// This is the invariant behind shipped bug #737. A PTY read returns whatever the
// kernel had, so an escape routinely straddles a boundary; a scanner that
// decides differently on either side of one produces a stream the worker's
// terminal and the client's terminal disagree about, and the two grids drift
// apart silently from that byte on.
//
// The existing batteries already sweep every split point — but only of the
// inputs somebody wrote down. That is the half these properties add: the inputs
// themselves are generated, from a grammar of the fragments that make boundaries
// interesting (a lone ESC, half an introducer, a marker prefix that diverges on
// its last byte, a UTF-8 character with its continuation bytes elsewhere, the
// C1 controls that abort a string), and rapid picks the boundaries. Nothing here
// needs an oracle: the whole-input run IS the expected answer, and every
// chunking of that input has to match it.

// streamFragments are the pieces a generated stream is built from. Each one is
// either a complete thing, or deliberately half of one, so concatenating a few
// of them produces the sequences that only exist across a boundary: an ESC that
// turns out to introduce nothing, a `133;` prefix that turns out to be a title,
// an APC that is never terminated.
var streamFragments = []string{
	// Ordinary output, including the multi-byte characters whose continuation
	// bytes must not be separated from their lead by an extraction.
	"hello",
	"a",
	"",
	"\r\n",
	"é",            // 2-byte
	"⠀",            // 3-byte, and the braille blank a spinner prints
	"🙂",            // 4-byte
	"\xe1",         // a lead byte with its continuations missing
	"\xa5",         // an orphaned continuation byte
	"\x07",         // BEL, which ends an OSC and nothing else
	"\x18", "\x1a", // CAN and SUB, which abort a string wherever it stands
	"\x80", "\x9c", // an executed C1, and C1 ST
	"\x90", "\x9b", "\x9d", "\x9e", "\x9f", // the C1 introducers
	"\x1b",   // a lone ESC: the byte after it decides everything
	"\x1b[",  // half a CSI
	"\x1b]",  // an OSC introducer with nothing after it
	"\x1b_",  // half an APC introducer
	"\x1b_G", // a kitty introducer with no payload yet
	"\x1b(B", // an escape with an intermediate byte
	"\x1b\\", // ST on its own
	"\x1b[0m",
	"\x1b]0;window title\x07",
	"\x1b]0;title with \x1b in it\x07",
	"\x1b]777;notify;Claude Code;waiting\x07",
	"\x1b]13",        // a marker prefix cut mid-way
	"\x1b]133",       // and one byte further
	"\x1b]133;",      // the full prefix, body still to come
	"\x1b]134;x\x07", // an OSC whose code diverges from 133 on its last digit
	"\x1b]133;A\x07",
	"\x1b]133;B\x1b\\",
	"\x1b]133;C;cmdline=ls -la\x07",
	"\x1b]133;D;0\x07",
	"\x1b]133;D;127\x1b\\",
	"\x1b]133;A",      // an unterminated marker
	"\x1b]133;A\x1b[", // a marker a stray ESC abandons
	kittyIntro + "a=T,f=24,s=1,v=1;QQ==" + kittyST,
	kittyIntro + "a=T,f=100,m=1;iVBORw0K" + kittyST,
	kittyIntro + "a=T,f=24;AA\x07BB" + kittyST, // a BEL inside a kitty payload
	kittyIntro + "m=1;GgoAAABJ",                // an unterminated APC
	kittyIntro + "i=1;AA\x9c",                  // one terminated by C1 ST
	kittyIntro + "i=2;AA\x18",                  // one a control aborts
	kittyST,
}

// drawStream builds an input out of fragments. Short on purpose: the boundary
// cases live in the joins between fragments, not in volume, and every scanner
// here has a tripwire measured in kilobytes or megabytes that a long generated
// stream would start bumping into for reasons that have nothing to do with
// chunking.
func drawStream(t *rapid.T) string {
	var b strings.Builder
	for range rapid.IntRange(0, 8).Draw(t, "fragments") {
		b.WriteString(rapid.SampledFrom(streamFragments).Draw(t, "fragment"))
	}
	return b.String()
}

// drawChunking cuts input at rapid-chosen offsets. The empty chunking (one
// whole chunk) is included: a Feed of the whole input has to agree with itself.
func drawChunking(t *rapid.T, input string) []string {
	if len(input) < 2 {
		return []string{input}
	}
	cuts := rapid.SliceOfNDistinct(
		rapid.IntRange(1, len(input)-1),
		0, min(6, len(input)),
		func(i int) int { return i },
	).Draw(t, "cuts")
	slices.Sort(cuts)

	chunks := make([]string, 0, len(cuts)+1)
	prev := 0
	for _, cut := range cuts {
		chunks = append(chunks, input[prev:cut])
		prev = cut
	}
	return append(chunks, input[prev:])
}

// TestFeedSegmenterIsChunkBoundaryInvariant is the property the worker's whole
// extraction story rests on. The segmenter decides where a sequence begins and
// ends, and it removes bytes from one side of the feed on the strength of that
// decision; a decision that changes with the boundary is a grid divergence.
func TestFeedSegmenterIsChunkBoundaryInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input := drawStream(t)
		wantEmissions, wantPending := runKittySegmenter(t, []string{input})

		chunks := drawChunking(t, input)
		gotEmissions, gotPending := runKittySegmenter(t, chunks)

		if !kittyEmissionsEqual(gotEmissions, wantEmissions) {
			t.Fatalf("chunked as %q:\n got: %s\nwant: %s",
				chunks, formatKittyEmissions(gotEmissions), formatKittyEmissions(wantEmissions))
		}
		if gotPending != wantPending {
			t.Fatalf("chunked as %q: holds %q, want %q", chunks, gotPending, wantPending)
		}

		// The invariant this file's header names: every byte is accounted for
		// exactly once. Asserted on the chunked run, where a hold can drop or
		// double one.
		var rebuilt strings.Builder
		for _, e := range gotEmissions {
			rebuilt.WriteString(e.bytes)
		}
		rebuilt.WriteString(gotPending)
		if rebuilt.String() != input {
			t.Fatalf("chunked as %q: emissions rebuild %q, want %q", chunks, rebuilt.String(), input)
		}
	})
}

// TestOSCScannerIsChunkBoundaryInvariant is the same property for the read-only
// scanner. It consumes nothing, so a boundary cannot corrupt the stream here —
// but a sequence it fails to see across one is a window title that never
// updates, or a notification that never fires.
//
// Bounded by oscScanMaxPending on purpose. Past that tripwire the scanner
// abandons what it is holding, and where the abandon lands DOES depend on the
// chunking: a whole-input Feed weighs the whole sequence at once, a chunked one
// weighs it as it arrives. Generated streams stay two orders of magnitude below
// it, which is also where every real title and notification sits.
func TestOSCScannerIsChunkBoundaryInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input := drawStream(t)
		if len(input) > oscScanMaxPending/2 {
			t.Fatalf("a generated stream is %d bytes, past half the scanner's %d-byte abandon tripwire; "+
				"this property does not hold up there, so the fragment pool has to stay under it",
				len(input), oscScanMaxPending)
		}
		want := scanAll(input)

		chunks := drawChunking(t, input)
		got := scanAll(chunks...)

		if len(got) != len(want) {
			t.Fatalf("chunked as %q: got %+v, want %+v", chunks, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("chunked as %q, sequence %d: got %+v, want %+v", chunks, i, got[i], want[i])
			}
		}
	})
}
