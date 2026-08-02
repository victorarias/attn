//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// Randomized search for a stream the feeder rewrites into something the client
// reads differently. The corpus next door pins the cases we thought of; this
// looks for the ones we did not.
//
// The property is the same one the mirror gate asserts, weakened only by the
// escape hatch: after an arbitrary stream split into arbitrary chunks, either
// some chunk reported a resync — the wire carried nothing for it and the
// snapshot re-push makes the client whole — or the two terminals agree on their
// whole text, their viewport, and the cursor. Anything else is a silent
// divergence between the worker grid and the client's, which is the bug class
// the phase exists to remove.
//
// Seeds carry real kitty escapes so mutation lands inside a protocol the
// terminal still parses; random bytes alone would spend the whole budget on
// streams with no APC in them at all.
//
// This is the search that drove kittyseg.go's framing rules. It used to reach,
// in about a second, a class of malformed stream where a byte-pattern search
// for the introducer disagreed with ghostty's parser about which bytes belong
// to a kitty APC — an APC pattern inside a string ghostty was consuming
// opaquely, or a C1 control that ended the APC early. The segmenter now tracks
// where ghostty's parser stands and extracts only from ground, with every
// transition measured against the terminal rather than read off the spec, and
// the two smallest streams it found are corpus entries next door.
//
// What it reaches now is a different kind of defect: divergences in what the
// feed path DOES with a correctly framed sequence, not in where it cuts one.
// Those belong to the configuration that has kitty live, which is why the
// property runs as two targets — see FuzzKittyWireMirrorShipping, the gate, and
// FuzzKittyWireMirror, which is knowingly red on the A4 defects.
//
// The client in both is a native terminal standing in for the frontend's wasm
// model, fed through writeAsClient rather than written raw. The two ghostty
// builds are different pins and disagree about OSC 133; writing the wire
// straight into a native terminal would fail this property on correct code.

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// fuzzKittyMaxInput bounds one input so a mutation that grows the payload
// cannot walk the segmenter to its 72 MiB tripwire inside the fuzzing loop.
// Real transmissions chunk at 4 KiB; 64 KiB clears any single escape a seed can
// grow into while keeping an iteration cheap enough to run millions of times.
const fuzzKittyMaxInput = 64 << 10

const (
	fuzzKittyCols = 20
	fuzzKittyRows = 8
)

// fuzzKittyFlush is fed as a final chunk so the comparison happens on a
// quiesced stream. Both scanners in the feed path hold a trailing partial
// escape back from the TERMINAL while the wire already carries it, so a stream
// that stops mid-escape leaves the worker a few bytes behind the client by
// design — a state no attach can observe, since the next chunk resolves it. ST
// resolves every hold: it terminates an open APC or OSC, and its bytes are
// neither an introducer prefix nor a possible continuation, so nothing is held
// after it on either side.
var fuzzKittyFlush = []byte("\x1b\\")

// FuzzKittyWireMirrorShipping is the gate: it runs the mirror property in the
// configuration production actually runs, with the worker's kitty storage limit
// at zero. Ghostty refuses every transmission there, so nothing is stamped and
// writeAPC returns early — the property under test is the DISPOSAL, which is
// what ships today: which bytes reach the terminal, which reach the wire, and
// whether the two grids still agree afterwards.
//
// This one must stay green. A counterexample here is a live defect, not a
// deferred one.
func FuzzKittyWireMirrorShipping(f *testing.F) {
	fuzzKittyWireMirror(f, 0)
}

// FuzzKittyWireMirror runs the same property with kitty LIVE, which is the
// configuration A4 flips on. It exercises synthesis — the observed scroll and
// cursor written in an APC's place — and it is known red on the two defects
// recorded under A4 in docs/plans/2026-08-02-terminal-kitty-images.md: an
// undescribed image the placement diff cannot see because it appeared and died
// inside one chunk, and the `Updated`-blind end-of-feed check. Both need a
// decision that belongs to the flip, so this target is NOT a gate yet and is not
// run with -fuzz in CI. Its seeds still run on every `go test`, which is what
// keeps the recorded corpus honest without reddening the build.
func FuzzKittyWireMirror(f *testing.F) {
	fuzzKittyWireMirror(f, mirrorStorageLimit)
}

func fuzzKittyWireMirror(f *testing.F, storageLimit uint64) {
	for _, in := range kittyCorpusInputs() {
		f.Add([]byte(strings.Join(in.chunks, "")), uint16(len(in.chunks[0])))
	}
	for _, tc := range mirrorCases {
		f.Add([]byte(strings.Join(tc.chunks, "")), uint16(64))
	}

	f.Fuzz(func(t *testing.T, data []byte, chunkSize uint16) {
		if len(data) > fuzzKittyMaxInput {
			data = data[:fuzzKittyMaxInput]
		}
		size := int(chunkSize%4096) + 1
		baseline := ghosttyvt.LiveTrackedRefs()
		worker := newKittyTerminal(t, fuzzKittyCols, fuzzKittyRows, ghosttyvt.Options{KittyImageStorageLimit: storageLimit})
		client := newKittyTerminal(t, fuzzKittyCols, fuzzKittyRows, ghosttyvt.Options{})
		feeder := newWireFeeder(worker)
		if feeder == nil {
			t.Fatalf("newWireFeeder returned nil for a live terminal")
		}

		resynced := ""
		var clientSeg feedSegmenter
		feed := func(chunk []byte) {
			wire, resync := feeder.feed(chunk)
			writeAsClient(client, &clientSeg, wire)
			if resync != "" && resynced == "" {
				resynced = resync
			}
		}
		for start := 0; start < len(data); start += size {
			feed(data[start:min(start+size, len(data))])
		}
		feed(fuzzKittyFlush)

		if resynced == "" {
			if got, want := client.PlainText(), worker.PlainText(); got != want {
				t.Errorf("history diverged with no resync (chunk size %d)\nworker:\n%s\nclient:\n%s", size, want, got)
			}
			if got, want := client.ViewportText(), worker.ViewportText(); got != want {
				t.Errorf("viewport diverged with no resync (chunk size %d)\nworker:\n%s\nclient:\n%s", size, want, got)
			}
			wx, wy := worker.CursorPos()
			cx, cy := client.CursorPos()
			if wx != cx || wy != cy {
				t.Errorf("cursor diverged with no resync (chunk size %d): client (%d,%d), worker (%d,%d)", size, cx, cy, wx, wy)
			}
		}

		// The feeder pins a tracked ref around every APC and the block table
		// holds more; all of them are the terminal's native memory, which the
		// process never gets back if a path forgets to free one.
		feeder.close()
		if got := ghosttyvt.LiveTrackedRefs(); got != baseline {
			t.Errorf("LiveTrackedRefs() = %d after the run, want the %d it started at", got, baseline)
		}
	})
}

// FuzzKittySegmenterFraming soaks the segmenter's framing rules on their own,
// without the rest of the feed path in the loop.
//
// It exists because framing and disposal fail differently, and a whole-path
// counterexample does not say which one broke. This target asks the one question
// kittyseg.go answers — would ghostty's parser be in ground after these bytes? —
// and asserts the segmenter agrees, under an arbitrary chunking, while
// reconstructing every byte it was handed. A framing regression lands here as a
// minimal input naming the exact transition, rather than as a grid diff two
// layers away.
//
// Both halves matter. Agreement alone would pass for a segmenter that tracked
// state perfectly and dropped a byte; reconstruction alone would pass for one
// that never extracted anything.
func FuzzKittySegmenterFraming(f *testing.F) {
	for _, c := range kittySegBattery {
		f.Add([]byte(c.input), uint16(3))
	}
	for _, in := range kittyCorpusInputs() {
		f.Add([]byte(strings.Join(in.chunks, "")), uint16(len(in.chunks[0])))
	}
	for _, prefix := range kittyGroundNamedPrefixes {
		f.Add([]byte(prefix), uint16(1))
	}

	f.Fuzz(func(t *testing.T, data []byte, chunkSize uint16) {
		if len(data) > fuzzKittyMaxInput {
			data = data[:fuzzKittyMaxInput]
		}
		size := int(chunkSize%4096) + 1

		var seg feedSegmenter
		rebuilt := make([]byte, 0, len(data))
		for start := 0; start < len(data); start += size {
			chunk := data[start:min(start+size, len(data))]
			// A copy per chunk, because an emission may alias the chunk and the
			// segmenter is allowed to reuse its own buffer afterwards.
			seg.Feed(append([]byte(nil), chunk...), func(e feedSegment) {
				rebuilt = append(rebuilt, e.Bytes...)
			})
		}
		if got := string(rebuilt) + string(seg.pending); got != string(data) {
			t.Fatalf("emissions rebuild %q, want %q (chunk size %d)", got, data, size)
		}

		want := ghosttyInGround(t, string(data))
		got := seg.mode == kittySegGround && len(seg.pending) == 0
		if got != want {
			t.Errorf("after %q (chunk size %d): segmenter ground=%v, ghostty ground=%v", data, size, got, want)
		}
	})
}
