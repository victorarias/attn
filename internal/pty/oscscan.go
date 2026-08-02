package pty

// A read-only OSC scanner.
//
// The feed segmenter in kittyseg.go cannot be reused for this: it *strips* the
// OSC 133 markers it finds, so that the server-authoritative restore rebuilds
// blocks from structured data rather than by re-emitting markers into the VT
// dump — and so that the marker's own grid effect stays off the worker's
// terminal, which matters because native ghostty breaks the line on a mid-line
// `OSC 133;A` and the wasm build the app renders does not (see wirefeed.go).
// OSC 0 (window title) and OSC 777
// (desktop notification) are different — the title is real terminal state the
// client must keep, so nothing here may alter the byte stream. This scanner only
// looks.
//
// It is also deliberately not corpus-locked to the frontend parser the way the
// 133 segmenter is: nothing on the client side parses these, so there is no
// parity contract to hold.

// oscScanMaxPending abandons a never-terminated sequence so a producer that
// emits an unterminated OSC cannot make the scanner buffer forever. A window
// title is short; anything past this is not a title.
const oscScanMaxPending = 4096

// oscScanner finds complete `ESC ] <code> ; <payload> (BEL | ESC \)` sequences
// across chunk boundaries. It reports what it saw and consumes nothing.
type oscScanner struct {
	// pending holds a sequence that started in an earlier chunk and has not been
	// terminated yet.
	pending []byte
}

// Feed reports every complete OSC sequence in chunk, in order. code is the
// numeric introducer (0, 133, 777, …) and payload is everything between the
// first `;` and the terminator. A sequence with no `;` reports an empty payload.
func (s *oscScanner) Feed(chunk []byte, emit func(code int, payload string)) {
	if len(s.pending) == 0 && indexOfByte(chunk, oscESC) < 0 {
		return
	}

	var buffer []byte
	if len(s.pending) > 0 {
		buffer = make([]byte, 0, len(s.pending)+len(chunk))
		buffer = append(buffer, s.pending...)
		buffer = append(buffer, chunk...)
		s.pending = nil
	} else {
		buffer = chunk
	}

	from := 0
	for {
		start := indexOfOSCIntroducer(buffer, from)
		if start < 0 {
			// Hold back a trailing lone ESC: the `]` may arrive in the next chunk.
			if len(buffer) > 0 && buffer[len(buffer)-1] == oscESC {
				s.pending = []byte{oscESC}
			}
			return
		}

		body, next, status := scanOSCBody(buffer, start+2)
		switch status {
		case oscScanTerminated:
			if code, payload, ok := splitOSCBody(body); ok {
				emit(code, payload)
			}
			from = next
		case oscScanAbandoned:
			// A stray ESC: the producer gave up on this sequence. Resume at the
			// ESC, which may itself introduce the next one.
			from = next
		default: // oscScanIncomplete
			if len(buffer)-start > oscScanMaxPending {
				// Unterminated and oversized: drop it rather than hold the bytes,
				// and resume past the introducer so a later well-formed sequence
				// in the same stream is still seen.
				from = start + 2
				continue
			}
			s.pending = append([]byte(nil), buffer[start:]...)
			return
		}
	}
}

// indexOfOSCIntroducer finds the next `ESC ]`.
func indexOfOSCIntroducer(buffer []byte, from int) int {
	for i := from; i+1 < len(buffer); i++ {
		if buffer[i] == oscESC && buffer[i+1] == ']' {
			return i
		}
	}
	return -1
}

type oscScanStatus int

const (
	// oscScanIncomplete: the sequence may still be terminated by a later chunk.
	oscScanIncomplete oscScanStatus = iota
	// oscScanTerminated: a complete sequence.
	oscScanTerminated
	// oscScanAbandoned: a stray ESC that does not start ST. An OSC cannot contain
	// one, so the producer gave up on this sequence — and waiting for a
	// terminator that will never come would hide every later sequence in the
	// stream.
	oscScanAbandoned
)

// scanOSCBody returns the bytes between the introducer and the terminator. For
// oscScanTerminated, next is the index just past the terminator; for
// oscScanAbandoned it is where to resume scanning (the stray ESC itself, which
// may introduce the next sequence).
func scanOSCBody(buffer []byte, from int) ([]byte, int, oscScanStatus) {
	for i := from; i < len(buffer); i++ {
		switch buffer[i] {
		case oscBEL:
			return buffer[from:i], i + 1, oscScanTerminated
		case oscESC:
			if i+1 >= len(buffer) {
				return nil, 0, oscScanIncomplete
			}
			if buffer[i+1] == oscBackslash {
				return buffer[from:i], i + 2, oscScanTerminated
			}
			return nil, i, oscScanAbandoned
		}
	}
	return nil, 0, oscScanIncomplete
}

// splitOSCBody parses `<digits>;<payload>` (or a bare `<digits>`). ok is false
// for a body whose introducer is not numeric, which is not an OSC we can route.
func splitOSCBody(body []byte) (int, string, bool) {
	code := 0
	digits := 0
	for i := 0; i < len(body); i++ {
		switch {
		case body[i] >= '0' && body[i] <= '9':
			code = code*10 + int(body[i]-'0')
			digits++
			if digits > 9 {
				return 0, "", false
			}
		case body[i] == ';':
			if digits == 0 {
				return 0, "", false
			}
			return code, string(body[i+1:]), true
		default:
			return 0, "", false
		}
	}
	if digits == 0 {
		return 0, "", false
	}
	return code, "", true
}
