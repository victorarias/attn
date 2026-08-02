package pty

// Kitty graphics APC segmenter — the outer split of the worker's PTY feed.
//
// The feed path nests two segmenters. This one runs OUTSIDE, cutting the raw
// stream into plain runs and complete kitty graphics APCs; the OSC 133
// segmenter (osc133.go) runs INSIDE, over the plain runs only. The nesting is
// not cosmetic — the two layers dispose of their bytes differently. An OSC 133
// marker goes nowhere: stripped from the terminal feed and from the wire. A
// kitty APC goes to the terminal, because ghostty is the system's only kitty
// parser, but not to the wire, which carries layout bytes synthesized from
// what ghostty did instead. Only the outer layer can route a whole APC that
// way, and running it outside also keeps an opaque payload out of the inner
// scanner's reach.
//
// This file is the ONLY place in attn that knows how a kitty escape is
// delimited, and it knows nothing else about the protocol. Ids, chunked
// transmissions, deletes, placement geometry, z-order — all of that is read
// back out of ghostty's authoritative state, never re-derived here ("observe,
// never interpret", docs/plans/2026-08-02-terminal-kitty-images.md).
//
// The invariant everything here serves: for any input, split into any sequence
// of chunks, the emissions concatenate back to that input byte for byte, in
// order — minus only a tail held for the next Feed. The terminal is fed every
// byte exactly once; the wire is fed everything except the extracted APCs. A
// byte dropped, doubled, or reordered here is a silent divergence between the
// worker grid and the client grid, which is the bug class this plan exists to
// remove.

import "bytes"

// kittyAPCIntroducer is ESC _ G — APC plus kitty graphics' protocol byte.
// Ghostty identifies kitty on that G alone, with the command following
// immediately and no separator after it (src/terminal/apc.zig), so the
// introducer is exactly these three bytes.
var kittyAPCIntroducer = []byte{0x1b, 0x5f, 0x47}

// kittySegMaxPendingBytes bounds what one unterminated APC can make the
// segmenter buffer. It is a tripwire, not a correctness cliff: past it the held
// bytes are emitted as plain, so they reach the terminal and the wire alike and
// the two grids stay consistent — the only cost is an unstripped APC on the
// wire.
//
// That consistency is also why the number must sit ABOVE what ghostty accepts
// rather than below it. Ghostty's built-in kitty APC cap is 65 MiB
// (Protocol.defaultMaxBytes(.kitty) in src/terminal/apc.zig, at the native pin
// ab0b9da), measured over the payload after the `;`
// (src/terminal/kitty/graphics_command.zig); attn never overrides it, and on
// overflow ghostty drops the whole command rather than rendering part of one.
// 72 MiB clears that cap with room for the control section and framing, so an
// escape long enough to trip this threshold carries a payload ghostty has
// already refused. Reaching it at all means a broken producer: kitty's own
// convention chunks a transmission into escapes of 4 KiB or less.
const kittySegMaxPendingBytes = 72 * 1024 * 1024

// kittyAPCSegmenter splits the PTY byte stream into plain segments and
// complete kitty graphics APCs (ESC _ G ... ESC \). It buffers a partial
// APC (or a partial introducer) across Feed calls.
type kittyAPCSegmenter struct {
	pending []byte
	// resume is how far into pending the body scan has already looked, and
	// doubles as the marker for what pending holds: zero means a suffix that
	// might still become an introducer, non-zero means an open APC that begins
	// at pending[0] and needs no rescanning below this offset.
	resume int
}

// Feed reports the chunk as ordered emissions: emit(plain, nil) for a run of
// non-kitty bytes, emit(nil, apc) for one complete kitty APC (its FULL bytes,
// introducer through terminator). Exactly one argument is non-nil per call, and
// a plain run is never empty.
//
// An emitted slice is only valid for the duration of its emit call: it aliases
// either the caller's chunk or the segmenter's own buffer, both of which the
// next call reuses. Copy anything that has to outlive the callback.
//
// Fast path, matching the osc133Segmenter contract: a chunk holding no ESC
// while nothing is pending produces exactly one emit(chunk, nil) that passes
// the input slice through — no copy, no allocation.
//
// Cost is amortized O(len(chunk)) even while an APC stays open across many
// calls: the chunk is appended to the buffer in place and only the new bytes
// are scanned. Anything proportional to the bytes already buffered turns the
// walk to the tripwire quadratic, and the tripwire is 72 MiB.
func (s *kittyAPCSegmenter) Feed(chunk []byte, emit func(plain []byte, apc []byte)) {
	if len(s.pending) == 0 && indexOfByte(chunk, oscESC) < 0 {
		if len(chunk) > 0 {
			emit(chunk, nil)
		}
		return
	}

	// carried says whether buffer aliases s.pending, which decides whether
	// holding bytes back at the end costs a copy.
	carried := len(s.pending) > 0
	buffer := chunk
	// open is an APC that started in an earlier chunk: it begins at buffer[0]
	// and its body scan continues where the last call stopped.
	open := false
	bodyFrom := 0
	if carried {
		s.pending = append(s.pending, chunk...)
		buffer = s.pending
		open = s.resume > 0
		bodyFrom = s.resume
	}

	// plainStart trails searchFrom: bytes the scan rejects as an APC keep
	// accumulating into the current plain run instead of ending it.
	plainStart := 0
	searchFrom := 0

	for {
		start := 0
		if !open {
			start = indexOfKittyIntroducer(buffer, searchFrom)
			if start < 0 {
				// Hold back only the suffix that could still become an
				// introducer once the next chunk lands; flush everything
				// before it.
				hold := kittyPartialIntroducerSuffixLength(buffer)
				flushEnd := len(buffer) - hold
				if flushEnd > plainStart {
					emit(buffer[plainStart:flushEnd], nil)
				}
				s.hold(buffer, carried, flushEnd, flushEnd)
				return
			}
			bodyFrom = start + len(kittyAPCIntroducer)
		}
		open = false

		pos, status := scanKittyAPCBody(buffer, bodyFrom)
		switch status {
		case kittyAPCTerminated:
			if start > plainStart {
				emit(buffer[plainStart:start], nil)
			}
			emit(nil, buffer[start:pos])
			plainStart = pos
			searchFrom = pos

		case kittyAPCAbandoned:
			// The producer gave up mid-sequence, so the introducer and
			// everything after it are ordinary bytes. Resume at the stray ESC,
			// which may itself introduce the next APC.
			searchFrom = pos

		default: // kittyAPCIncomplete
			if len(buffer)-start > kittySegMaxPendingBytes {
				emit(buffer[plainStart:], nil)
				s.release()
				return
			}
			if start > plainStart {
				emit(buffer[plainStart:start], nil)
			}
			s.hold(buffer, carried, start, pos)
			return
		}
	}
}

// hold keeps buffer[from:] for the next Feed, with resumeAt the absolute index
// the body scan should continue from (pass from itself when the bytes are not
// an open APC). Keeping an APC that already sits at the front of the buffer it
// is growing into costs nothing, which is what makes a long transmission
// linear.
func (s *kittyAPCSegmenter) hold(buffer []byte, carried bool, from, resumeAt int) {
	if from >= len(buffer) {
		s.release()
		return
	}
	if carried && resumeAt > from {
		if from > 0 {
			s.pending = s.pending[:copy(s.pending, buffer[from:])]
		}
		s.resume = resumeAt - from
		return
	}
	// Everything else held here is at most a partial introducer, so copying it
	// into a fresh slice costs two bytes and hands back whatever capacity an
	// APC that just finished in this same call had grown to.
	s.pending = append([]byte(nil), buffer[from:]...)
	s.resume = resumeAt - from
}

// release drops the buffer instead of keeping its capacity for the next
// sequence. A finished APC may have grown to megabytes, and a session that
// once carried one would otherwise hold that memory for its whole life.
func (s *kittyAPCSegmenter) release() {
	s.pending = nil
	s.resume = 0
}

// indexOfKittyIntroducer finds the next ESC _ G at or after from.
func indexOfKittyIntroducer(buffer []byte, from int) int {
	last := len(buffer) - len(kittyAPCIntroducer)
	for i := from; i <= last; i++ {
		if buffer[i] == oscESC &&
			buffer[i+1] == kittyAPCIntroducer[1] &&
			buffer[i+2] == kittyAPCIntroducer[2] {
			return i
		}
	}
	return -1
}

type kittyAPCStatus int

const (
	// kittyAPCIncomplete: a later chunk may still terminate this APC.
	kittyAPCIncomplete kittyAPCStatus = iota
	// kittyAPCTerminated: a complete APC.
	kittyAPCTerminated
	// kittyAPCAbandoned: an ESC inside the body that does not start ST. An APC
	// cannot contain one, so the producer gave up on this sequence — and
	// waiting for a terminator that will never come would hide every later APC
	// in the stream.
	kittyAPCAbandoned
)

// scanKittyAPCBody walks the APC body starting at from and returns one index
// whose meaning follows the status: just past the ST when terminated, the stray
// ESC itself when abandoned (it may introduce the next APC), and where a later
// call must pick the scan back up when incomplete.
//
// That last one is the whole reason a resume offset is safe. Every ESC before
// the end has been judged already; only an ESC in the final position is
// undecided, because the byte that would make it ST has not arrived. So the
// scan resumes either at that trailing ESC or at the end, and never re-reads a
// byte it has ruled on.
//
// ESC \ is the only terminator. BEL ends an OSC but not an APC, so a BEL in the
// payload is an ordinary byte — the reason this is not the OSC scanner.
func scanKittyAPCBody(buffer []byte, from int) (int, kittyAPCStatus) {
	for i := from; i < len(buffer); i++ {
		if buffer[i] != oscESC {
			continue
		}
		if i+1 >= len(buffer) {
			return i, kittyAPCIncomplete
		}
		if buffer[i+1] == oscBackslash {
			return i + 2, kittyAPCTerminated
		}
		return i, kittyAPCAbandoned
	}
	return len(buffer), kittyAPCIncomplete
}

// kittyPartialIntroducerSuffixLength returns the length of the longest buffer
// suffix that is a strict prefix of the introducer — the bytes to hold back in
// case the next chunk completes it.
func kittyPartialIntroducerSuffixLength(buffer []byte) int {
	longest := len(kittyAPCIntroducer) - 1
	if len(buffer) < longest {
		longest = len(buffer)
	}
	for length := longest; length > 0; length-- {
		if bytes.Equal(buffer[len(buffer)-length:], kittyAPCIntroducer[:length]) {
			return length
		}
	}
	return 0
}
