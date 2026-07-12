package pty

import (
	"errors"
	"io"
	"testing"
	"time"
)

func TestContainsCPRQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "contains cpr query",
			data: []byte("\x1b[6n"),
			want: true,
		},
		{
			name: "ignores other dsr query",
			data: []byte("\x1b[5n"),
			want: false,
		},
		{
			name: "ignores malformed sequence",
			data: []byte("\x1b[6x"),
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsCPRQuery(tc.data); got != tc.want {
				t.Fatalf("containsCPRQuery() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScanOSCColorQueriesContainsCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		code int
		want bool
	}{
		{
			name: "contains osc 10 query",
			data: []byte("\x1b]10;?\x1b\\"),
			code: 10,
			want: true,
		},
		{
			name: "contains osc 11 query",
			data: []byte("\x1b]11;?\x07"),
			code: 11,
			want: true,
		},
		{
			name: "ignores different osc query",
			data: []byte("\x1b]11;?\x1b\\"),
			code: 10,
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			codes := scanOSCColorQueries(tc.data)
			got := false
			for _, c := range codes {
				if c == tc.code {
					got = true
					break
				}
			}
			if got != tc.want {
				t.Fatalf("scanOSCColorQueries(%q) contains %d = %v, want %v", tc.data, tc.code, got, tc.want)
			}
		})
	}
}

func TestDetectTerminalQueries(t *testing.T) {
	t.Parallel()

	queries := detectTerminalQueries([]byte("\x1b[6n...\x1b[c...\x1b]10;?\x1b\\...\x1b]11;?\x07...\x1b]12;?\x07"))
	if !queries.da1 || !queries.cpr || queries.osc10 != 1 || queries.osc11 != 1 || queries.osc12 != 1 {
		t.Fatalf("detectTerminalQueries() = %+v, want all queries detected once", queries)
	}
	if queries.da1BeforeCPR {
		t.Fatalf("detectTerminalQueries() = %+v, want da1BeforeCPR=false for CPR-first chunk", queries)
	}

	reversed := detectTerminalQueries([]byte("\x1b[0c...\x1b[6n"))
	if !reversed.da1BeforeCPR {
		t.Fatalf("detectTerminalQueries() = %+v, want da1BeforeCPR=true for DA1-first chunk", reversed)
	}

	// A chunk asking the same OSC color repeatedly must be counted, not just
	// detected — an under-count leaves later queries in the same chunk
	// unanswered.
	repeated := detectTerminalQueries([]byte("\x1b]11;?\x07\x1b]11;?\x07\x1b]11;?\x07"))
	if repeated.osc11 != 3 {
		t.Fatalf("detectTerminalQueries() osc11 = %d, want 3", repeated.osc11)
	}

	// An OSC color SET (no "?") must never be detected as a query.
	set := detectTerminalQueries([]byte("\x1b]11;#000000\x1b\\"))
	if set.osc11 != 0 {
		t.Fatalf("detectTerminalQueries() osc11 = %d, want 0 for a color SET", set.osc11)
	}
}

func readOf(b byte, n int) ptyRead {
	data := make([]byte, n)
	for i := range data {
		data[i] = b
	}
	return ptyRead{data: data}
}

func TestNextCoalescedReadLoneReadReturnsImmediately(t *testing.T) {
	t.Parallel()

	reads := make(chan ptyRead, 1)
	reads <- ptyRead{data: []byte("x")}

	// A huge window proves no timer wait happens on the interactive path:
	// the test would time out if a lone read were held for coalescing.
	data, err := nextCoalescedRead(reads, 100, time.Minute)
	if err != nil {
		t.Fatalf("nextCoalescedRead() error = %v", err)
	}
	if string(data) != "x" {
		t.Fatalf("nextCoalescedRead() = %q, want %q", data, "x")
	}
}

func TestNextCoalescedReadBatchesQueuedReadsUpToMax(t *testing.T) {
	t.Parallel()

	reads := make(chan ptyRead, 4)
	reads <- readOf('a', 4)
	reads <- readOf('b', 4)
	reads <- readOf('c', 4)
	reads <- readOf('d', 4)

	data, err := nextCoalescedRead(reads, 12, time.Minute)
	if err != nil {
		t.Fatalf("nextCoalescedRead() error = %v", err)
	}
	if string(data) != "aaaabbbbcccc" {
		t.Fatalf("nextCoalescedRead() = %q, want %q", data, "aaaabbbbcccc")
	}
	if got := len(reads); got != 1 {
		t.Fatalf("reads left in channel = %d, want 1", got)
	}
}

func TestNextCoalescedReadReturnsErrorWithBatchedData(t *testing.T) {
	t.Parallel()

	reads := make(chan ptyRead, 2)
	reads <- readOf('a', 4)
	reads <- ptyRead{err: io.EOF}

	data, err := nextCoalescedRead(reads, 100, time.Minute)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("nextCoalescedRead() error = %v, want io.EOF", err)
	}
	if string(data) != "aaaa" {
		t.Fatalf("nextCoalescedRead() = %q, want %q", data, "aaaa")
	}
}

func TestNextCoalescedReadFirstReadErrorReturnsImmediately(t *testing.T) {
	t.Parallel()

	reads := make(chan ptyRead, 1)
	reads <- ptyRead{data: []byte("tail"), err: io.EOF}

	data, err := nextCoalescedRead(reads, 100, time.Minute)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("nextCoalescedRead() error = %v, want io.EOF", err)
	}
	if string(data) != "tail" {
		t.Fatalf("nextCoalescedRead() = %q, want %q", data, "tail")
	}
}

func TestNextCoalescedReadWindowBoundsLatency(t *testing.T) {
	t.Parallel()

	reads := make(chan ptyRead, 2)
	reads <- readOf('a', 4)
	reads <- readOf('b', 4)

	start := time.Now()
	data, err := nextCoalescedRead(reads, 100, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("nextCoalescedRead() error = %v", err)
	}
	if string(data) != "aaaabbbb" {
		t.Fatalf("nextCoalescedRead() = %q, want %q", data, "aaaabbbb")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("nextCoalescedRead() blocked %v, want ~10ms window", elapsed)
	}
}
