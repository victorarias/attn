package pty

// The worker's PTY feed segmenter — the ONE place that decides where a kitty
// APC or OSC 133 marker begins and ends; meaning is read elsewhere ("observe,
// never interpret", docs/plans/2026-08-02-terminal-kitty-images.md).
//
// Invariant: for any input, split into any chunks, the emissions concatenate
// back to the input byte for byte, in order — minus only a tail held for the
// next Feed. Extraction removes bytes from one side, so it is safe only from
// GROUND with a reached terminator: outside ground the sequence's leading ESC
// also exits the open sequence, and taking it away silently diverges the two
// grids. Everything else replays to both sides as plain.
//
// Every transition here was MEASURED against the native terminal, not read off
// the VT spec — ghostty's sets differ from the spec's — and
// TestKittySegmenterGroundMatchesGhostty holds this machine's idea of ground
// equal to ghostty's. Change a rule only with a measurement to point at.

func indexOfByte(b []byte, target byte) int {
	for i, c := range b {
		if c == target {
			return i
		}
	}
	return -1
}

// kittyAPCIntroducer is ESC _ G — APC plus kitty graphics' protocol byte.
// Ghostty identifies kitty on that G alone (src/terminal/apc.zig).
var kittyAPCIntroducer = []byte{0x1b, 0x5f, 0x47}

// kittySegMaxPendingBytes bounds what one unterminated APC can buffer — a
// tripwire, not a correctness cliff: past it the held bytes replay as plain to
// both sides. It must sit ABOVE what ghostty accepts: ghostty's kitty APC cap
// is 65 MiB (Protocol.defaultMaxBytes(.kitty), src/terminal/apc.zig at native
// pin ab0b9da), over the post-`;` payload; 72 MiB clears that plus framing, so
// tripping it means a payload ghostty already refused.
const kittySegMaxPendingBytes = 72 * 1024 * 1024

// osc133MarkerMaxPendingBytes is the same tripwire for a held OSC 133 marker.
// Receipt: the largest legitimate marker is a C marker carrying a
// percent-encoded command line; ARG_MAX measured 1 MiB here (`getconf
// ARG_MAX` = 1048576), encoding can triple a byte, so 3 MiB is the ceiling for
// a runnable command — attn's own emitter was observed at 61 bytes. 16 MiB
// clears it five times over.
const osc133MarkerMaxPendingBytes = 16 * 1024 * 1024

// kittySegMode is where ghostty's parser stands after everything fed so far.
// Bytes are held only in the modes holding() names.
type kittySegMode uint8

const (
	// kittySegGround: printing. The only mode an extraction can start in.
	kittySegGround kittySegMode = iota
	// kittySegEscape: an ESC that cannot introduce an extractable APC.
	kittySegEscape
	// kittySegEscapeIntermediate: an escape that has taken an intermediate byte.
	// Measured: once one lands, the string introducers stop introducing.
	kittySegEscapeIntermediate
	// kittySegCSI: inside ESC [ … final.
	kittySegCSI
	// kittySegOSC: inside ESC ] …, which ends on its own byte set.
	kittySegOSC
	// kittySegOSC133Prefix: inside an OSC opened in GROUND that still matches
	// osc133Prefix. Holds only the undecided bytes; the first divergent byte
	// drops the hold, so a title write never stalls the feed.
	kittySegOSC133Prefix
	// kittySegOSC133Body: the marker prefix matched; holding to the terminator.
	kittySegOSC133Body
	// kittySegOpaque: inside a DCS, SOS, PM or APC string — including a kitty
	// APC this segmenter has decided it cannot extract.
	kittySegOpaque
	// kittySegKitty: inside an extractable kitty APC, buffered whole.
	kittySegKitty
)

// c1Executed reports the C1 bytes ghostty executes as controls, returning to
// ground from escape, CSI, and every string state. Measured: exactly 80-8f,
// 91-97, 99-9a and 9c; the holes are the C1 introducers.
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
// its terminator. Measured: CAN and SUB abort everywhere, plus every
// c1Executed byte. BEL is deliberately absent — it ends only an OSC.
func kittySegAborts(b byte) bool {
	return b == 0x18 || b == 0x1a || c1Executed(b)
}

// kittySegOpensInsideString reports the sequence a raw C1 introducer opens from
// inside an open DCS, PM, APC or kitty string. Measured, and asymmetric from
// the escape state: 90/9b/9d cut the string short and introduce their own;
// 98/9e/9f are payload. An OSC honours none of the six — hence its own mode.
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
// CSI state, where all six introduce (measured). From GROUND they open
// nothing: the stream is UTF-8, so they decode to U+FFFD and print.
func kittySegOpensC1(b byte) (kittySegMode, bool) {
	switch b {
	case 0x98, 0x9e, 0x9f:
		return kittySegOpaque, true
	}
	return kittySegOpensInsideString(b)
}

// kittySegOpens7Bit reports the sequence a byte opens from escape state — the
// only mode where the 7-bit forms introduce anything.
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

// feedSegKind says how the consumer must dispose of an emission's bytes.
type feedSegKind uint8

const (
	// feedSegPlain: ordinary output. To the terminal and to the wire.
	feedSegPlain feedSegKind = iota
	// feedSegKittyAPC: one complete kitty APC, introducer through terminator.
	// Terminal only; the wire carries synthesized layout bytes instead.
	feedSegKittyAPC
	// feedSegOSC133: one complete OSC 133 marker. Wire only; the block table
	// takes the parsed marker. OSC 133 produces no cells, so the grids agree.
	feedSegOSC133
)

// feedSegment is one emission. Bytes is never empty and is valid only for the
// duration of its callback — it aliases a buffer the next call reuses.
type feedSegment struct {
	Kind  feedSegKind
	Bytes []byte
	// Marker is what a feedSegOSC133 emission means — nil for a subtype no
	// block event is defined for (bytes still consumed).
	Marker *osc133Marker
}

// feedSegmenter splits the PTY byte stream into the three dispositions above,
// carrying a partial sequence and the parser mode across Feed calls.
type feedSegmenter struct {
	mode    kittySegMode
	pending []byte
	// resume is how far into pending the scan has already looked. Meaningful
	// only in holding modes.
	resume int
}

// holding reports whether the mode buffers its bytes rather than emitting them.
func (m kittySegMode) holding() bool {
	return m == kittySegKitty || m == kittySegOSC133Prefix || m == kittySegOSC133Body
}

// maxPending is the tripwire for the sequence this mode is holding.
func (m kittySegMode) maxPending() int {
	if m == kittySegKitty {
		return kittySegMaxPendingBytes
	}
	return osc133MarkerMaxPendingBytes
}

// abandoned is where the parser stands once a held sequence is given up on at
// the tripwire: both ends are still inside it.
func (m kittySegMode) abandoned() kittySegMode {
	if m == kittySegKitty {
		return kittySegOpaque
	}
	return kittySegOSC
}

// Feed reports the chunk as ordered emissions, in stream order.
//
// Fast path: an ESC-free chunk in ground with nothing pending passes the input
// slice through — no copy, no allocation. Ground is part of the condition
// because it is the only mode an ESC-free chunk cannot move (measured).
//
// Cost is amortized O(len(chunk)) even while a sequence stays open across many
// calls: only new bytes are scanned, or the walk to the 72 MiB tripwire goes
// quadratic.
func (s *feedSegmenter) Feed(chunk []byte, emit func(feedSegment)) {
	if s.mode == kittySegGround && len(s.pending) == 0 && indexOfByte(chunk, oscESC) < 0 {
		if len(chunk) > 0 {
			emit(feedSegment{Kind: feedSegPlain, Bytes: chunk})
		}
		return
	}

	// carried says whether buffer aliases s.pending, which decides whether
	// holding bytes back at the end costs a copy.
	carried := len(s.pending) > 0
	buffer := chunk
	// holdStart is where the held extractable sequence began, or -1 for none.
	holdStart := -1
	i := 0
	if carried {
		s.pending = append(s.pending, chunk...)
		buffer = s.pending
		if s.mode.holding() {
			holdStart = 0
			i = s.resume
		}
	}

	emitPlain := func(from, to int) {
		if to > from {
			emit(feedSegment{Kind: feedSegPlain, Bytes: buffer[from:to]})
		}
	}

	// plainStart trails i: walked-past bytes accumulate into the plain run.
	plainStart := 0

scan:
	for i < len(buffer) {
		b := buffer[i]
		switch s.mode {
		case kittySegGround:
			if b != oscESC {
				i++
				continue
			}
			// Deciding what this ESC introduces needs one byte after it, two
			// for a kitty APC. Hold when they have not arrived: no prefix of a
			// removed sequence may reach the far side ahead of the removal.
			if i+1 >= len(buffer) || (buffer[i+1] == kittyAPCIntroducer[1] && i+2 >= len(buffer)) {
				emitPlain(plainStart, i)
				s.hold(buffer, carried, i, i)
				return
			}
			switch {
			case buffer[i+1] == ']':
				// Only an OSC opened in ground can be a marker.
				holdStart = i
				s.mode = kittySegOSC133Prefix
				i += 2
			case buffer[i+1] == kittyAPCIntroducer[1] && buffer[i+2] == kittyAPCIntroducer[2]:
				holdStart = i
				s.mode = kittySegKitty
				i += len(kittyAPCIntroducer)
			default:
				s.mode = kittySegEscape
				i++
			}

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
				// Measured: ESC ESC restarts the escape and drops collected
				// intermediates — not ground.
				s.mode = kittySegEscape
			case b == 0x18 || b == 0x1a || c1Executed(b):
				s.mode = kittySegGround
			case b >= 0x20 && b <= 0x2f:
				s.mode = kittySegEscapeIntermediate
			case b >= 0x30 && b <= 0x7e:
				// A final byte. Measured: from a bare escape, 30-4f, 51-57,
				// 59-5a, 5c and 60-7e return to ground; after an intermediate
				// all of 30-7e does.
				s.mode = kittySegGround
			default:
				// C0 controls, DEL and a0-ff all leave the parser mid-escape.
			}
			i++

		case kittySegCSI:
			switch {
			case b == oscESC:
				// Measured: an ESC cancels the CSI and starts a new escape.
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
				// A final byte. Measured: CSI returns to ground on all of
				// 40-7e — the 7-bit letters open nothing here.
				s.mode = kittySegGround
			}
			i++

		case kittySegOSC:
			switch b {
			case oscESC:
				s.mode = kittySegEscape
			case 0x07, 0x18, 0x1a:
				// Measured: an OSC ends on BEL, CAN and SUB and on NOTHING
				// else — C1 ST does not end one, and a raw C1 introducer
				// inside is payload. The opposite of the opaque strings.
				s.mode = kittySegGround
			}
			i++

		case kittySegOSC133Prefix:
			if b == osc133Prefix[i-holdStart] {
				i++
				if i-holdStart == len(osc133Prefix) {
					s.mode = kittySegOSC133Body
				}
				continue
			}
			// Not a marker: drop the hold so the bytes stay in the plain run,
			// and read this byte again as OSC payload.
			holdStart = -1
			s.mode = kittySegOSC

		case kittySegOSC133Body:
			switch {
			case b == oscBEL:
				i++
				emitPlain(plainStart, holdStart)
				emitMarker(emit, buffer[holdStart:i])
				plainStart = i
				holdStart = -1
				s.mode = kittySegGround
			case b == oscESC:
				if i+1 >= len(buffer) {
					// ST may still be arriving; hold and resume on this ESC.
					break scan
				}
				if buffer[i+1] == oscBackslash {
					i += 2
					emitPlain(plainStart, holdStart)
					emitMarker(emit, buffer[holdStart:i])
					plainStart = i
					holdStart = -1
					s.mode = kittySegGround
					continue
				}
				// Measured: a stray ESC makes ghostty DISPATCH the marker, so
				// cutting would be framing-safe — but the client's parser
				// (terminalOsc133.ts) knows only BEL and ST, and stripping a
				// marker it would not recognise splits the two block tables.
				holdStart = -1
				s.mode = kittySegEscape
				i++
			case b == 0x18 || b == 0x1a:
				// Measured: CAN and SUB also DISPATCH the marker and leave
				// ground. Same disposal, same reason.
				holdStart = -1
				s.mode = kittySegGround
				i++
			default:
				// Measured: an OSC swallows everything else, C1 ST included.
				i++
			}

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
					emitPlain(plainStart, holdStart)
					emit(feedSegment{Kind: feedSegKittyAPC, Bytes: buffer[holdStart : i+2]})
					i += 2
					plainStart = i
					holdStart = -1
					s.mode = kittySegGround
					continue
				}
				// The ESC ends the APC for ghostty and opens a new escape.
				// Extracting would take that exit off the wire, so the whole
				// abandoned APC replays to both sides as plain.
				holdStart = -1
				s.mode = kittySegEscape
				i++
			case b == 0x9c:
				// Measured: C1 ST terminates a kitty APC exactly as ESC \ does.
				emitPlain(plainStart, holdStart)
				i++
				emit(feedSegment{Kind: feedSegKittyAPC, Bytes: buffer[holdStart:i]})
				plainStart = i
				holdStart = -1
				s.mode = kittySegGround
			case kittySegAborts(b):
				// The aborting byte has its own grid effect (IND scrolls) that
				// synthesis cannot observe; replay as plain so both parsers
				// cut in the same place.
				holdStart = -1
				s.mode = kittySegGround
				i++
			default:
				if mode, ok := kittySegOpensInsideString(b); ok {
					// The APC ends here too, into a sequence. Same disposal.
					holdStart = -1
					s.mode = mode
				}
				i++
			}
		}
	}

	if holdStart >= 0 {
		if len(buffer)-holdStart > s.mode.maxPending() {
			emitPlain(plainStart, len(buffer))
			s.release()
			// Both parsers are now inside a sequence nothing here terminates.
			s.mode = s.mode.abandoned()
			return
		}
		emitPlain(plainStart, holdStart)
		s.hold(buffer, carried, holdStart, i)
		return
	}
	emitPlain(plainStart, len(buffer))
	s.release()
}

// emitMarker reports one complete OSC 133 marker: raw is its FULL bytes,
// introducer through terminator. Only the two extracting terminators may reach
// here — BEL, or two-byte ST — a third added without widening this would index
// backwards past the introducer.
func emitMarker(emit func(feedSegment), raw []byte) {
	payloadEnd := len(raw) - 1
	if raw[len(raw)-1] != oscBEL {
		// ST, two bytes.
		payloadEnd--
	}
	emit(feedSegment{
		Kind:   feedSegOSC133,
		Bytes:  raw,
		Marker: osc133MarkerFromPayload(string(raw[len(osc133Prefix):payloadEnd])),
	})
}

// hold keeps buffer[from:] for the next Feed, resumeAt being the absolute
// index the body scan continues from (pass from when the bytes are not an open
// sequence). Keeping one already at the front of its own growing buffer costs
// nothing — that is what makes a long transmission linear.
func (s *feedSegmenter) hold(buffer []byte, carried bool, from, resumeAt int) {
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
	// Everything else held here is small; copying into a fresh slice releases
	// whatever capacity a just-finished APC had grown to.
	s.pending = append([]byte(nil), buffer[from:]...)
	s.resume = resumeAt - from
}

// release drops the buffer instead of keeping its capacity: a finished APC may
// have grown to megabytes, held otherwise for the session's whole life. The
// parser mode is untouched.
func (s *feedSegmenter) release() {
	s.pending = nil
	s.resume = 0
}
