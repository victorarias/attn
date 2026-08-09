package pty

// A read-only OSC scanner. Not the feed segmenter (kittyseg.go): that one
// STRIPS what it finds, while OSC 0 (title) and OSC 777 (notification) are
// real terminal state the client must keep — this scanner only looks, and
// carries no parity contract with any frontend parser.

// oscScanMaxPending abandons a never-terminated sequence so a broken producer
// cannot make the scanner buffer forever; anything past this is not a title.
const oscScanMaxPending = 4096

// oscScanner finds complete `ESC ] <code> ; <payload> (BEL | ESC \)` sequences
// across chunk boundaries. It reports what it saw and consumes nothing.
type oscScanner struct {
	// pending holds a sequence begun in an earlier chunk, not yet terminated.
	pending []byte
}

// Feed reports every complete OSC sequence in chunk, in order. code is the
// numeric introducer (0, 133, 777, …) and payload is everything between the
// first `;` and the terminator (empty when there is no `;`).
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
			// Hold a trailing lone ESC: the `]` may arrive in the next chunk.
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
			// Resume at the stray ESC, which may introduce the next one.
			from = next
		default: // oscScanIncomplete
			if len(buffer)-start > oscScanMaxPending {
				// Drop the oversized hold; resume past the introducer so a
				// later well-formed sequence is still seen.
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
	// oscScanIncomplete: may still be terminated by a later chunk.
	oscScanIncomplete oscScanStatus = iota
	// oscScanTerminated: a complete sequence.
	oscScanTerminated
	// oscScanAbandoned: a stray ESC not starting ST — the producer gave up;
	// waiting for a terminator that never comes would hide later sequences.
	oscScanAbandoned
)

// scanOSCBody returns the bytes between the introducer and the terminator;
// next is just past the terminator, or the stray ESC to resume at (abandoned).
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
// for a non-numeric introducer, which is not an OSC we can route.
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
