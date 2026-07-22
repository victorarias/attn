package pty

import (
	"bytes"
	"testing"
)

// TestStripScannerOwnedResponses verifies the gap filter keeps exactly the
// query responses the scan-based responder does NOT emit (kitty CSI ? u, DECRQM
// reports, and anything unrecognized) while dropping the classes the scanner
// already answers (CPR, DA, OSC 10/11/12). This is what lets the worker forward
// only the gap in snapshot mode without double-answering a scanner-owned query.
func TestStripScannerOwnedResponses(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "kitty flags report is kept",
			in:   "\x1b[?0u",
			want: "\x1b[?0u",
		},
		{
			name: "CPR report is dropped",
			in:   "\x1b[10;15R",
			want: "",
		},
		{
			name: "DA1 report is dropped",
			in:   "\x1b[?62;22c",
			want: "",
		},
		{
			name: "OSC 11 color report is dropped",
			in:   "\x1b]11;rgb:1e1e/1e1e/1e1e\x07",
			want: "",
		},
		{
			name: "OSC 10 color report (ST-terminated) is dropped",
			in:   "\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\",
			want: "",
		},
		{
			name: "DECRQM report is kept",
			in:   "\x1b[?2026;2$y",
			want: "\x1b[?2026;2$y",
		},
		{
			name: "mixed stream keeps only the gap (codex bootstrap trio)",
			// CPR + DA1 (scanner-owned) interleaved with kitty (gap).
			in:   "\x1b[24;80R\x1b[?0u\x1b[?62;22c",
			want: "\x1b[?0u",
		},
		{
			name: "non-color OSC is kept",
			in:   "\x1b]52;c;Zm9v\x07",
			want: "\x1b]52;c;Zm9v\x07",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripScannerOwnedResponses([]byte(tc.in))
			if !bytes.Equal(got, []byte(tc.want)) {
				t.Errorf("stripScannerOwnedResponses(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
