package pty

// The worker's PTY feed segmenter — the ONE place that decides where a
// sequence begins and ends.
//
// It cuts the raw stream into three kinds of run, which differ only in how the
// consumer disposes of them:
//
//   - plain output, which goes to the terminal AND to the wire;
//   - a complete kitty graphics APC, which goes to the TERMINAL only — ghostty
//     is the system's only kitty parser — while the wire carries layout bytes
//     synthesized from what ghostty did with it;
//   - a complete OSC 133 marker, which goes to the WIRE only, where the
//     client's own parser reads it, while the worker's terminal is kept clean
//     and the block table gets the parsed marker instead.
//
// The two extractions used to live in nested segmenters, an outer one for kitty
// and an inner byte-pattern scan for OSC 133. They are one machine now because
// they ask the same question and only a parser can answer it (see below); two
// answers meant two chances to be wrong, and the inner one was.
//
// This file is the only place in attn that knows how a kitty escape or an OSC
// 133 marker is DELIMITED, and it knows nothing else about either protocol.
// Kitty ids, chunked transmissions, deletes, placement geometry, z-order are
// all read back out of ghostty's authoritative state ("observe, never
// interpret", docs/plans/2026-08-02-terminal-kitty-images.md); a marker's
// meaning is parsed in osc133.go, from the payload this file hands it.
//
// The invariant everything here serves: for any input, split into any sequence
// of chunks, the emissions concatenate back to that input byte for byte, in
// order — minus only a tail held for the next Feed. Every byte is accounted for
// exactly once, and which side of the feed it lands on is the emission's kind.
// A byte dropped, doubled, or reordered here is a silent divergence between the
// worker grid and the client grid, which is the bug class this plan exists to
// remove.
//
// Delimiting an escape is not pattern matching, which is why this file carries
// a parser state machine. Extraction REMOVES bytes from one side of the feed,
// so it is safe only when the removed run is a whole sequence to both parsers
// at once. The leading ESC of a kitty APC or an OSC 133 marker does double duty
// whenever the stream is not in ground: it also ends the OSC, string, CSI or
// escape that was open. Taking the sequence away then takes that exit with it,
// and one side sits inside a sequence the other has already left — the two
// grids diverge from the next byte on, silently, with no resync to catch it.
//
// So the machine answers exactly one question: would ghostty's parser be in
// ground when this ESC arrives? Extraction happens only there, and only for a
// sequence that reaches its terminator. Everything else — a sequence opened
// mid-sequence, one a control byte cut short, one still unterminated at the
// tripwire — is replayed to both sides as plain, which is always safe because
// both ends are ghostty and parse identical bytes identically.
//
// Every transition below was MEASURED against the native terminal rather than
// read off the VT spec, and TestKittySegmenterGroundMatchesGhostty holds them
// to it: for a large set of byte sequences, this machine's idea of ground must
// equal ghostty's. ghostty's sets are not the ones the spec suggests. C1 ST
// ends every string EXCEPT an OSC; BEL ends only an OSC; half the C1 range
// aborts a string; a raw C1 introducer opens a sequence from escape and CSI but
// is inert in ground, and inside a string only three of the six introduce at
// all. Change a rule here only with a measurement to point at.

func indexOfByte(b []byte, target byte) int {
	for i, c := range b {
		if c == target {
			return i
		}
	}
	return -1
}

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

// osc133MarkerMaxPendingBytes is the same tripwire for a held OSC 133 marker,
// and the same disposal past it: the bytes replay as plain, so both sides see
// them and only the block event is lost.
//
// Receipt. The largest marker anything can legitimately emit is a C marker,
// whose payload is a percent-encoded command line: ESC ] 133 ; C ;
// cmdline_url= … terminator, about 22 bytes of framing around the command. A
// command line that can actually run is bounded by ARG_MAX, measured at 1 MiB
// on this machine (`getconf ARG_MAX` = 1048576) and lower on Linux, where
// MAX_ARG_STRLEN caps a single string at 128 KiB. Encoding can triple a byte
// (`%3B`), so 3 MiB is the ceiling for a command that could be executed at all
// — and attn's own emitter is far under it, escaping only six characters
// (shell_startup.go) and observed at 61 bytes across the recorded fish corpus.
// 16 MiB clears the ceiling five times over. Reaching it means a producer that
// opened a marker and never closed it.
const osc133MarkerMaxPendingBytes = 16 * 1024 * 1024

// kittySegMode is where ghostty's parser stands after everything fed so far.
// The segmenter tracks it to answer one question — is the stream in ground? —
// and holds bytes only in the modes holding() names.
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
	// kittySegOSC133Prefix: inside an OSC that opened in GROUND and still
	// matches osc133Prefix, so it may yet turn out to be a marker. Holds its
	// bytes — no prefix of a marker this segmenter removes may reach the
	// terminal ahead of the removal — but only ever the five that are still
	// undecided. The first byte that diverges drops the hold and the run
	// carries on as an ordinary OSC, so a title write never stalls the feed.
	kittySegOSC133Prefix
	// kittySegOSC133Body: the marker prefix matched; holding to the terminator.
	kittySegOSC133Body
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

// feedSegKind says how the consumer must dispose of an emission's bytes.
type feedSegKind uint8

const (
	// feedSegPlain: ordinary output. To the terminal and to the wire.
	feedSegPlain feedSegKind = iota
	// feedSegKittyAPC: one complete kitty graphics APC, introducer through
	// terminator. To the terminal only; the wire carries synthesized layout
	// bytes in its place.
	feedSegKittyAPC
	// feedSegOSC133: one complete OSC 133 marker, introducer through
	// terminator. To the wire only; the worker's terminal never sees it and
	// the block table takes the parsed marker instead. OSC 133 produces no
	// cells, so keeping it off one side costs the grids nothing.
	feedSegOSC133
)

// feedSegment is one emission. Bytes is never empty.
//
// The slice is valid only for the duration of its callback: it aliases either
// the caller's chunk or the segmenter's own buffer, both of which the next call
// reuses. Copy anything that has to outlive the callback.
type feedSegment struct {
	Kind  feedSegKind
	Bytes []byte
	// Marker is what a feedSegOSC133 emission means — nil for a subtype no
	// block event is defined for, in which case the bytes are still consumed.
	// Always nil for the other kinds.
	Marker *osc133Marker
}

// feedSegmenter splits the PTY byte stream into the three dispositions above.
// It buffers a partial extractable sequence (or a partial introducer) across
// Feed calls, and carries the parser mode across them.
type feedSegmenter struct {
	mode    kittySegMode
	pending []byte
	// resume is how far into pending the scan has already looked. Meaningful
	// only in the modes that hold body bytes; pending in kittySegGround is a
	// suffix that might still become an introducer and is rescanned from the
	// front, which costs two bytes.
	resume int
}

// holding reports whether the mode is one whose bytes are buffered rather than
// emitted, which is what an extraction needs and what a resumed scan continues.
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
// the tripwire: both ends are still inside it, with nothing here that will
// terminate it.
func (m kittySegMode) abandoned() kittySegMode {
	if m == kittySegKitty {
		return kittySegOpaque
	}
	return kittySegOSC
}

// Feed reports the chunk as ordered emissions, in stream order.
//
// Fast path: a chunk holding no ESC while the stream is in ground and nothing
// is pending produces exactly one plain emission that passes the input slice
// through — no copy, no allocation. That is every chunk of every session that
// prints ordinary output. Ground is part of the condition because it is the
// only mode an ESC-free chunk cannot move: measured, every non-ESC byte keeps
// ghostty in ground, while inside a string a bare C1 or CAN still ends it. A
// raw C1 introducer is inert in ground — the stream is UTF-8, so 0x9d decodes
// to U+FFFD and prints rather than opening an OSC.
//
// Cost is amortized O(len(chunk)) even while a sequence stays open across many
// calls: the chunk is appended to the buffer in place and only the new bytes
// are scanned. Anything proportional to the bytes already buffered turns the
// walk to the tripwire quadratic, and the tripwire is 72 MiB.
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
	// holdStart is where the extractable sequence being held began, or -1 for
	// none. Only one can be open at a time. A sequence open from an earlier
	// chunk begins at buffer[0] and its scan continues where the last call
	// stopped.
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

	// emitPlain closes the run that ends where an extraction begins.
	emitPlain := func(from, to int) {
		if to > from {
			emit(feedSegment{Kind: feedSegPlain, Bytes: buffer[from:to]})
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
			// Deciding what this ESC introduces needs the byte after it, and for
			// a kitty APC the one after that. Hold when they have not arrived:
			// no prefix of a sequence this segmenter removes may reach the far
			// side ahead of the removal.
			if i+1 >= len(buffer) || (buffer[i+1] == kittyAPCIntroducer[1] && i+2 >= len(buffer)) {
				emitPlain(plainStart, i)
				s.hold(buffer, carried, i, i)
				return
			}
			switch {
			case buffer[i+1] == ']':
				// An OSC opened in ground is the only kind that can be a
				// marker. Whether it IS one takes four more bytes to know, and
				// kittySegOSC133Prefix is where that is decided.
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

		case kittySegOSC133Prefix:
			if b == osc133Prefix[i-holdStart] {
				i++
				if i-holdStart == len(osc133Prefix) {
					s.mode = kittySegOSC133Body
				}
				continue
			}
			// Not a marker. These are an ordinary OSC's bytes, which this
			// segmenter does not touch: drop the hold so they stay in the plain
			// run, and read this byte again as OSC payload.
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
				// Measured: a stray ESC makes ghostty DISPATCH the marker and
				// open a new escape, so cutting here would in fact be framing-
				// safe. It is still replayed as plain, because the client runs
				// its own parser over the wire (terminalOsc133.ts) and that one
				// knows only BEL and ST — stripping a marker it would not have
				// recognised leaves the two block tables disagreeing about a
				// command only one of them saw. Identical bytes to both sides
				// costs a block boundary on a malformed stream and nothing else.
				holdStart = -1
				s.mode = kittySegEscape
				i++
			case b == 0x18 || b == 0x1a:
				// Measured: CAN and SUB also DISPATCH the marker rather than
				// aborting it, and leave the parser in ground. Same disposal,
				// same reason.
				holdStart = -1
				s.mode = kittySegGround
				i++
			default:
				// Measured: an OSC swallows everything else — C1 ST and every
				// C1 introducer included. See kittySegOSC.
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
				// Extracting now would take that exit off the wire, so the
				// whole sequence stays in the plain run instead: this is where
				// an abandoned APC is disposed of, by replaying its bytes to
				// both sides unchanged.
				holdStart = -1
				s.mode = kittySegEscape
				i++
			case b == 0x9c:
				// Measured: C1 ST terminates a kitty APC exactly as ESC \ does,
				// dispatching the command — so it is cut and stripped the same
				// way, one byte instead of two.
				emitPlain(plainStart, holdStart)
				i++
				emit(feedSegment{Kind: feedSegKittyAPC, Bytes: buffer[holdStart:i]})
				plainStart = i
				holdStart = -1
				s.mode = kittySegGround
			case kittySegAborts(b):
				// A control that ends the APC without being a terminator. The
				// aborting byte has its own effect on the grid (IND scrolls,
				// CAN prints nothing), which synthesis cannot observe, so the
				// sequence is replayed as plain and both parsers cut it in the
				// same place.
				holdStart = -1
				s.mode = kittySegGround
				i++
			default:
				if mode, ok := kittySegOpensInsideString(b); ok {
					// The APC ends here too, into a sequence rather than into
					// ground. Same disposal: replay it all as plain.
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
			// Both parsers are now inside a sequence neither will see
			// terminated here; the stream stays opaque until something ends it.
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
// introducer through terminator, and the payload between them is what gives it
// meaning.
//
// Only the two extracting terminators may reach here — BEL, or the two bytes of
// ST after a matched prefix — which is what makes the payload slice safe. A
// third terminator added to kittySegOSC133Body without widening this would index
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

// hold keeps buffer[from:] for the next Feed, with resumeAt the absolute index
// the body scan should continue from (pass from itself when the bytes are not
// an open sequence). Keeping one that already sits at the front of the buffer it
// is growing into costs nothing, which is what makes a long transmission
// linear.
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
func (s *feedSegmenter) release() {
	s.pending = nil
	s.resume = 0
}
