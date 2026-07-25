package pty

// OSC 133 segmenter — the production implementation of osc133Segmenter.
//
// Semantics-identical port of app/src/utils/terminalOsc133.ts. Parity is
// enforced by the shared fixture corpus testdata/osc133_segmenter_corpus.json,
// consumed by BOTH osc133_test.go here and a frontend parity test. The client
// parser is the reference; keep the two in lockstep (see the corpus header).
//
// Unlike the client parser, which keeps each marker's bytes in its segment and
// writes them through to the terminal, this segmenter STRIPS the marker bytes:
// emit carries only the plain bytes BEFORE each marker, then the marker itself.
// OSC 133 sequences produce no cells, so the terminal grid is identical either
// way, and stripping keeps the marks out of the VT dump — the server-
// authoritative restore reconstructs blocks from structured data, never by
// re-emitting OSC 133 into the dump.

import (
	"net/url"
	"strconv"
	"strings"
)

// osc133Prefix is ESC ] 1 3 3 ; — the shell-integration OSC introducer.
var osc133Prefix = []byte{0x1b, 0x5d, 0x31, 0x33, 0x33, 0x3b}

const (
	oscBEL       = 0x07
	oscESC       = 0x1b
	oscBackslash = 0x5c
	// osc133MaxPendingBytes abandons a never-terminated marker so a broken
	// producer cannot make the segmenter buffer output forever.
	osc133MaxPendingBytes = 4096
)

// osc133ScanSegmenter is the stateful production segmenter. It buffers a
// partial marker (or a partial marker prefix) across Feed calls in pending.
type osc133ScanSegmenter struct {
	pending []byte
}

// Feed implements osc133Segmenter. See the interface doc in blockfeed.go for
// the emit contract.
func (s *osc133ScanSegmenter) Feed(chunk []byte, emit func([]byte, *osc133Marker)) {
	// Fast path: nothing held back and no ESC in the chunk — the whole chunk is
	// plain output, passed through with no copy and no allocation.
	if len(s.pending) == 0 && indexOfByte(chunk, oscESC) < 0 {
		if len(chunk) > 0 {
			emit(chunk, nil)
		}
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

	segmentStart := 0
	searchFrom := 0

	for {
		markerStart := indexOfOsc133Prefix(buffer, searchFrom)
		if markerStart < 0 {
			// No complete prefix ahead. Hold back only the suffix that could be
			// the start of a marker split across the next chunk; flush the rest.
			hold := osc133PartialPrefixSuffixLength(buffer)
			flushEnd := len(buffer) - hold
			if flushEnd > segmentStart {
				emit(buffer[segmentStart:flushEnd], nil)
			}
			if hold > 0 {
				s.pending = append([]byte(nil), buffer[flushEnd:]...)
			}
			return
		}

		// Find the terminator: BEL or ESC \ (7-bit ST).
		terminatorEnd := -1
		for i := markerStart + len(osc133Prefix); i < len(buffer); i++ {
			if buffer[i] == oscBEL {
				terminatorEnd = i + 1
				break
			}
			if buffer[i] == oscESC && i+1 < len(buffer) && buffer[i+1] == oscBackslash {
				terminatorEnd = i + 2
				break
			}
		}
		if terminatorEnd == -1 {
			if len(buffer)-markerStart > osc133MaxPendingBytes {
				// Broken marker: give up and pass everything through.
				emit(buffer[segmentStart:], nil)
				return
			}
			if markerStart > segmentStart {
				emit(buffer[segmentStart:markerStart], nil)
			}
			s.pending = append([]byte(nil), buffer[markerStart:]...)
			return
		}

		payloadEnd := terminatorEnd - 1
		if buffer[terminatorEnd-1] != oscBEL {
			payloadEnd = terminatorEnd - 2
		}
		payload := string(buffer[markerStart+len(osc133Prefix) : payloadEnd])
		marker := osc133MarkerFromPayload(payload)

		// Emit the plain bytes before the marker, tagged with the marker; the
		// marker's own bytes are dropped (stripped from the terminal grid).
		emit(buffer[segmentStart:markerStart], marker)

		segmentStart = terminatorEnd
		searchFrom = terminatorEnd
	}
}

func indexOfByte(b []byte, target byte) int {
	for i, c := range b {
		if c == target {
			return i
		}
	}
	return -1
}

func indexOfOsc133Prefix(buffer []byte, from int) int {
	last := len(buffer) - len(osc133Prefix)
	for i := from; i <= last; i++ {
		if buffer[i] != oscESC {
			continue
		}
		matched := true
		for j := 1; j < len(osc133Prefix); j++ {
			if buffer[i+j] != osc133Prefix[j] {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

// osc133PartialPrefixSuffixLength returns the length of the longest buffer
// suffix that is a strict prefix of the marker introducer — bytes to hold back
// in case the next chunk completes it.
func osc133PartialPrefixSuffixLength(buffer []byte) int {
	max := len(osc133Prefix) - 1
	if len(buffer) < max {
		max = len(buffer)
	}
	for length := max; length > 0; length-- {
		matched := true
		for i := 0; i < length; i++ {
			if buffer[len(buffer)-length+i] != osc133Prefix[i] {
				matched = false
				break
			}
		}
		if matched {
			return length
		}
	}
	return 0
}

// osc133MarkerFromPayload maps an OSC 133 payload (the bytes between the
// introducer and the terminator) to a marker. nil for an unknown subtype: the
// sequence is still consumed, it just produces no block-lifecycle event.
func osc133MarkerFromPayload(payload string) *osc133Marker {
	if payload == "" {
		return nil
	}
	switch payload[0] {
	case 'A':
		return &osc133Marker{Kind: osc133PromptStart}
	case 'B':
		return &osc133Marker{Kind: osc133InputStart}
	case 'C':
		var cmdline *string
		rest := ""
		if len(payload) > 2 {
			rest = payload[2:]
		}
		for _, part := range strings.Split(rest, ";") {
			switch {
			case strings.HasPrefix(part, "cmdline_url="):
				// decodeURIComponent equivalent: percent-decode without
				// treating '+' as space (url.PathUnescape, not QueryUnescape).
				if dec, err := url.PathUnescape(part[len("cmdline_url="):]); err == nil {
					c := dec
					cmdline = &c
				} else {
					cmdline = nil
				}
			case strings.HasPrefix(part, "cmdline=") && cmdline == nil:
				c := part[len("cmdline="):]
				cmdline = &c
			}
		}
		return &osc133Marker{Kind: osc133PreExec, Cmdline: cmdline}
	case 'D':
		var exitCode *int32
		rest := ""
		if len(payload) > 2 {
			rest = payload[2:]
		}
		if v, ok := parseInt10Prefix(rest); ok {
			exitCode = &v
		}
		return &osc133Marker{Kind: osc133CommandEnd, ExitCode: exitCode}
	default:
		return nil
	}
}

// parseInt10Prefix mirrors JS parseInt(s, 10): skip leading ASCII whitespace,
// take an optional sign and the leading run of digits; anything else means NaN
// (ok=false). Keeps exit-code parsing byte-for-byte with the client parser.
func parseInt10Prefix(s string) (int32, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f' || s[i] == '\v') {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digitStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digitStart {
		return 0, false
	}
	n, err := strconv.Atoi(s[start:i])
	if err != nil {
		return 0, false
	}
	return int32(n), true
}
