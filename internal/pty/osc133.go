package pty

// OSC 133 marker MEANING — what a marker's payload says, once the feed
// segmenter (kittyseg.go) has decided where the marker begins and ends.
//
// Semantics-identical port of app/src/utils/terminalOsc133.ts. Parity is
// enforced by the shared fixture corpus testdata/osc133_segmenter_corpus.json,
// consumed by BOTH osc133_test.go here and a frontend parity test. The client
// parser is the reference; keep the two in lockstep (see the corpus header).
//
// The two parsers dispose of a marker's bytes differently, which is why only
// the meaning is shared. The client keeps the bytes in its stream and writes
// them through to its terminal; the worker strips them, so the marker never
// reaches the terminal the VT dump is serialized from. OSC 133 produces no
// cells, so the two grids are identical either way.

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
)

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
