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
// The seed corpus is green. A `-fuzz` soak is NOT: it reaches, in about a
// second, a class of malformed stream where the segmenter's byte-pattern search
// disagrees with ghostty's parser about which bytes belong to a kitty APC —
// `\x1bX\x1b_G\x1b\\0` (an APC pattern inside an SOS string ghostty is
// consuming opaquely) and `\x1b_G\x840\x1b\\` (a C1 control that aborts the APC
// for ghostty but not for the segmenter) are the two smallest. Both leave the
// worker and the client on different grids with no resync, in the shipping
// zero-storage-limit configuration. They are open findings against kittyseg.go,
// not harness bugs.

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

func FuzzKittyWireMirror(f *testing.F) {
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
		worker := newKittyTerminal(t, fuzzKittyCols, fuzzKittyRows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
		client := newKittyTerminal(t, fuzzKittyCols, fuzzKittyRows, ghosttyvt.Options{})
		feeder := newWireFeeder(worker)
		if feeder == nil {
			t.Fatalf("newWireFeeder returned nil for a live terminal")
		}

		resynced := ""
		feed := func(chunk []byte) {
			wire, resync := feeder.feed(chunk)
			client.Write(wire)
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
