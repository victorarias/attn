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
//
// Delimiting an escape is not pattern matching, which is why this file carries
// a parser state machine. Extraction REMOVES bytes from the wire, so it is safe
// only when the removed run is a whole sequence to both parsers at once. The
// leading ESC of a kitty APC does double duty whenever the stream is not in
// ground: it also ends the OSC, string, CSI or escape that was open. Taking the
// APC away then takes that exit with it, and the client sits inside a sequence
// the worker has already left — the two grids diverge from the next byte on,
// silently, with no resync to catch it.
//
// So the machine answers exactly one question: would ghostty's parser be in
// ground when this ESC arrives? Extraction happens only there, and only for a
// sequence that ends at ST. Every other byte — a kitty APC opened mid-sequence,
// one a control byte cut short, one still unterminated at the tripwire — is
// replayed to both sides as plain, which is always safe because both ends are
// ghostty and parse identical bytes identically.
//
// Every transition below was MEASURED against the native terminal rather than
// read off the VT spec, and TestKittySegmenterGroundMatchesGhostty holds them
// to it: for a large set of byte sequences, this machine's idea of ground must
// equal ghostty's. ghostty's sets are not the ones the spec suggests. C1 ST
// ends every string EXCEPT an OSC; BEL ends only an OSC; half the C1 range
// aborts a string; a raw C1 introducer opens a sequence from escape and CSI but
// is inert in ground, and inside a string only three of the six introduce at
// all. Change a rule here only with a measurement to point at.

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

// kittySegMode is where ghostty's parser stands after everything fed so far.
// The segmenter tracks it to answer one question — is the stream in ground? —
// and holds no bytes on any account but kittySegKitty.
type kittySegMode uint8

const (
	// kittySegGround: printing. The only mode an extraction can start in.
	kittySegGround kittySegMode = iota
	// kittySegEscape: an ESC that cannot introduce an extractable APC, either
	// because ESC _ G did not follow or because it arrived outside ground.
	kittySegEscape
	// kittySegEscapeIntermediate: an escape that has taken an intermediate byte
	// (ESC ( B and friends). Measured: once one lands, the string introducers
	// stop introducing — ESC ( ] is a complete, if meaningless, escape and the
	// stream is back in ground, where plain ESC ] would have opened an OSC.
	kittySegEscapeIntermediate
	// kittySegCSI: inside ESC [ … final.
	kittySegCSI
	// kittySegOSC: inside ESC ] …, which ends on its own byte set.
	kittySegOSC
	// kittySegOpaque: inside a DCS, SOS, PM or APC string — including a kitty
	// APC this segmenter has decided it cannot extract.
	kittySegOpaque
	// kittySegKitty: inside an extractable kitty APC. The only mode whose bytes
	// are buffered rather than emitted, because extraction needs the whole
	// sequence in hand.
	kittySegKitty
)

// c1Executed reports the C1 bytes ghostty executes as controls and returns to
// ground from escape, CSI, and every string state.
//
// Measured: from escape, CSI, DCS, SOS, PM, APC and a kitty APC alike, exactly
// 80-8f, 91-97, 99-9a and 9c drop back to ground. The four holes are the C1
// introducers — 90 DCS, 98 SOS, 9b CSI, 9d OSC, 9e PM, 9f APC — which open a
// sequence instead, and 9d-9f sit past this range's end.
func c1Executed(b byte) bool {
	switch {
	case b >= 0x80 && b <= 0x8f:
		return true
	case b >= 0x91 && b <= 0x97:
		return true
	case b >= 0x99 && b <= 0x9a:
		return true
	case b == 0x9c:
		return true
	}
	return false
}

// kittySegAborts reports the single bytes that end a string sequence short of
// its terminator.
//
// Measured: CAN and SUB abort everywhere, and a string additionally ends on
// every c1Executed byte. BEL is deliberately absent — it ends an OSC and
// nothing else, which is why a BEL inside a kitty payload is an ordinary byte
// and this is not the OSC scanner.
func kittySegAborts(b byte) bool {
	return b == 0x18 || b == 0x1a || c1Executed(b)
}

// kittySegOpensInsideString reports the sequence a raw C1 introducer opens from
// inside an already-open DCS, PM, APC or kitty string.
//
// Measured, and asymmetric — this is not the same set the escape state honours.
// 90 (DCS), 9b (CSI) and 9d (OSC) cut the open string short and introduce their
// own: a kitty command carrying one in its control section dispatches truncated
// at that byte, and the text after it is swallowed by the new sequence instead
// of printing. 98 (SOS), 9e (PM) and 9f (APC) do neither — the same command
// still parses whole around them — so inside a string they are payload. An OSC
// honours none of the six, which is why kittySegOSC is its own mode.
func kittySegOpensInsideString(b byte) (kittySegMode, bool) {
	switch b {
	case 0x90:
		return kittySegOpaque, true
	case 0x9b:
		return kittySegCSI, true
	case 0x9d:
		return kittySegOSC, true
	}
	return 0, false
}

// kittySegOpensC1 reports the sequence a raw C1 introducer opens from escape or
// CSI state, where all six introduce — measured: ESC 98 swallows what follows
// until C1 ST and is not ended by a would-be final, so it opens a string even
// though the same byte is payload inside one. From GROUND they open nothing at
// all: the stream is UTF-8, so ghostty decodes them to U+FFFD and keeps
// printing.
func kittySegOpensC1(b byte) (kittySegMode, bool) {
	switch b {
	case 0x98, 0x9e, 0x9f:
		return kittySegOpaque, true
	}
	return kittySegOpensInsideString(b)
}

// kittySegOpens7Bit reports the sequence a byte opens from escape state, the
// only mode where the 7-bit forms introduce anything: inside a CSI or a string
// they are ordinary finals and payload, and after an intermediate byte they
// stop introducing entirely (see kittySegEscapeIntermediate).
func kittySegOpens7Bit(b byte) (kittySegMode, bool) {
	switch b {
	case 'P', 'X', '^', '_':
		return kittySegOpaque, true
	case ']':
		return kittySegOSC, true
	case '[':
		return kittySegCSI, true
	}
	return kittySegOpensC1(b)
}

// kittyAPCSegmenter splits the PTY byte stream into plain segments and
// extractable kitty graphics APCs. It buffers a partial APC (or a partial
// introducer) across Feed calls, and carries the parser mode across them.
type kittyAPCSegmenter struct {
	mode    kittySegMode
	pending []byte
	// resume is how far into pending the APC body scan has already looked.
	// Meaningful only in kittySegKitty, the one mode that holds body bytes;
	// pending in kittySegGround is a suffix that might still become an
	// introducer and is rescanned from the front, which costs two bytes.
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
// while the stream is in ground and nothing is pending produces exactly one
// emit(chunk, nil) that passes the input slice through — no copy, no
// allocation. Ground is part of the condition because it is the only mode an
// ESC-free chunk cannot move: measured, every non-ESC byte keeps ghostty in
// ground, while inside a string a bare C1 or CAN still ends it.
//
// Cost is amortized O(len(chunk)) even while an APC stays open across many
// calls: the chunk is appended to the buffer in place and only the new bytes
// are scanned. Anything proportional to the bytes already buffered turns the
// walk to the tripwire quadratic, and the tripwire is 72 MiB.
func (s *kittyAPCSegmenter) Feed(chunk []byte, emit func(plain []byte, apc []byte)) {
	if s.mode == kittySegGround && len(s.pending) == 0 && indexOfByte(chunk, oscESC) < 0 {
		if len(chunk) > 0 {
			emit(chunk, nil)
		}
		return
	}

	// carried says whether buffer aliases s.pending, which decides whether
	// holding bytes back at the end costs a copy.
	carried := len(s.pending) > 0
	buffer := chunk
	// apcStart is where an extractable APC began, or -1 for none. An APC open
	// from an earlier chunk begins at buffer[0] and its scan continues where
	// the last call stopped.
	apcStart := -1
	i := 0
	if carried {
		s.pending = append(s.pending, chunk...)
		buffer = s.pending
		if s.mode == kittySegKitty {
			apcStart = 0
			i = s.resume
		}
	}

	// plainStart trails i: every byte the machine walks past without extracting
	// keeps accumulating into the current plain run instead of ending it.
	plainStart := 0

scan:
	for i < len(buffer) {
		b := buffer[i]
		switch s.mode {
		case kittySegGround:
			if b != oscESC {
				// Measured: in ground every byte but ESC leaves the parser in
				// ground, C1 included.
				i++
				continue
			}
			// Deciding whether this ESC introduces an extractable APC needs two
			// more bytes. Hold when they have not arrived: no prefix of
			// ESC _ G may reach the wire ahead of the extraction that removes
			// it.
			if i+1 >= len(buffer) || (buffer[i+1] == kittyAPCIntroducer[1] && i+2 >= len(buffer)) {
				if i > plainStart {
					emit(buffer[plainStart:i], nil)
				}
				s.hold(buffer, carried, i, i)
				return
			}
			if buffer[i+1] == kittyAPCIntroducer[1] && buffer[i+2] == kittyAPCIntroducer[2] {
				apcStart = i
				s.mode = kittySegKitty
				i += len(kittyAPCIntroducer)
				continue
			}
			s.mode = kittySegEscape
			i++

		case kittySegEscape, kittySegEscapeIntermediate:
			if mode, ok := kittySegOpensC1(b); ok {
				s.mode = mode
				i++
				continue
			}
			if s.mode == kittySegEscape {
				if mode, ok := kittySegOpens7Bit(b); ok {
					s.mode = mode
					i++
					continue
				}
			}
			switch {
			case b == oscESC:
				// Measured: ESC ESC is not ground — the second ESC restarts the
				// escape rather than completing the first, and drops whatever
				// intermediates the first had collected.
				s.mode = kittySegEscape
			case b == 0x18 || b == 0x1a || c1Executed(b):
				s.mode = kittySegGround
			case b >= 0x20 && b <= 0x2f:
				s.mode = kittySegEscapeIntermediate
			case b >= 0x30 && b <= 0x7e:
				// A final byte, whatever introducers apply here having been
				// taken out of this span already. Measured: from a bare escape,
				// 30-4f, 51-57, 59-5a, 5c and 60-7e return to ground — that
				// span minus P X [ ] ^ _ — and after an intermediate all of
				// 30-7e does.
				s.mode = kittySegGround
			default:
				// C0 controls, DEL and a0-ff all leave the parser mid-escape.
			}
			i++

		case kittySegCSI:
			switch {
			case b == oscESC:
				// Measured: an ESC cancels the CSI and starts a new escape, so
				// what follows is executed rather than swallowed.
				s.mode = kittySegEscape
			case b == 0x18 || b == 0x1a:
				s.mode = kittySegGround
			case b >= 0x80:
				if mode, ok := kittySegOpensC1(b); ok {
					s.mode = mode
				} else if c1Executed(b) {
					s.mode = kittySegGround
				}
			case b >= 0x40 && b <= 0x7e:
				// A final byte. Unlike escape, the 7-bit letters open nothing
				// here — measured, CSI returns to ground on all of 40-7e.
				s.mode = kittySegGround
			}
			i++

		case kittySegOSC:
			switch b {
			case oscESC:
				s.mode = kittySegEscape
			case 0x07, 0x18, 0x1a:
				// Measured: an OSC ends on BEL, CAN and SUB and on NOTHING
				// else. C1 ST does not end one, and a raw C1 introducer inside
				// one is payload rather than a new sequence — both of which the
				// opaque strings do the other way round. That is why ghostty's
				// OSC is a mode here and not a flavour of kittySegOpaque.
				s.mode = kittySegGround
			}
			i++

		case kittySegOpaque:
			switch {
			case b == oscESC:
				s.mode = kittySegEscape
			case kittySegAborts(b):
				s.mode = kittySegGround
			default:
				if mode, ok := kittySegOpensInsideString(b); ok {
					s.mode = mode
				}
			}
			i++

		case kittySegKitty:
			switch {
			case b == oscESC:
				if i+1 >= len(buffer) {
					// ST may still be arriving; hold and resume on this ESC.
					break scan
				}
				if buffer[i+1] == oscBackslash {
					if apcStart > plainStart {
						emit(buffer[plainStart:apcStart], nil)
					}
					emit(nil, buffer[apcStart:i+2])
					i += 2
					plainStart = i
					apcStart = -1
					s.mode = kittySegGround
					continue
				}
				// The ESC ends the APC for ghostty and opens a new escape.
				// Extracting now would take that exit off the wire, so the
				// whole sequence stays in the plain run instead: this is where
				// an abandoned APC is disposed of, by replaying its bytes to
				// both sides unchanged.
				apcStart = -1
				s.mode = kittySegEscape
				i++
			case b == 0x9c:
				// Measured: C1 ST terminates a kitty APC exactly as ESC \ does,
				// dispatching the command — so it is cut and stripped the same
				// way, one byte instead of two.
				if apcStart > plainStart {
					emit(buffer[plainStart:apcStart], nil)
				}
				i++
				emit(nil, buffer[apcStart:i])
				plainStart = i
				apcStart = -1
				s.mode = kittySegGround
			case kittySegAborts(b):
				// A control that ends the APC without being a terminator. The
				// aborting byte has its own effect on the grid (IND scrolls,
				// CAN prints nothing), which synthesis cannot observe, so the
				// sequence is replayed as plain and both parsers cut it in the
				// same place.
				apcStart = -1
				s.mode = kittySegGround
				i++
			default:
				if mode, ok := kittySegOpensInsideString(b); ok {
					// The APC ends here too, into a sequence rather than into
					// ground. Same disposal: replay it all as plain.
					apcStart = -1
					s.mode = mode
				}
				i++
			}
		}
	}

	if apcStart >= 0 {
		if len(buffer)-apcStart > kittySegMaxPendingBytes {
			emit(buffer[plainStart:], nil)
			s.release()
			// Both parsers are now inside an APC neither will see terminated
			// here; the stream is opaque until something ends it.
			s.mode = kittySegOpaque
			return
		}
		if apcStart > plainStart {
			emit(buffer[plainStart:apcStart], nil)
		}
		s.hold(buffer, carried, apcStart, i)
		return
	}
	if len(buffer) > plainStart {
		emit(buffer[plainStart:], nil)
	}
	s.release()
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
// once carried one would otherwise hold that memory for its whole life. The
// parser mode is not part of what it drops: the stream keeps running.
func (s *kittyAPCSegmenter) release() {
	s.pending = nil
	s.resume = 0
}
