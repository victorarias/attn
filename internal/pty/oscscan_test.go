package pty

import (
	"fmt"
	"strings"
	"testing"
)

type scannedOSC struct {
	code    int
	payload string
}

func scanAll(chunks ...string) []scannedOSC {
	var s oscScanner
	var out []scannedOSC
	for _, chunk := range chunks {
		s.Feed([]byte(chunk), func(code int, payload string) {
			out = append(out, scannedOSC{code, payload})
		})
	}
	return out
}

func assertScanned(t *testing.T, got []scannedOSC, want ...scannedOSC) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d sequences %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequence %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestOSCScannerReadsBothTerminators(t *testing.T) {
	assertScanned(t,
		scanAll("\x1b]0;title\x07"),
		scannedOSC{0, "title"})
	assertScanned(t,
		scanAll("\x1b]0;title\x1b\\"),
		scannedOSC{0, "title"})
}

func TestOSCScannerIgnoresPlainOutput(t *testing.T) {
	if got := scanAll("hello world\nno escapes here"); got != nil {
		t.Fatalf("got %+v, want nothing", got)
	}
}

func TestOSCScannerReadsSeveralInOneChunk(t *testing.T) {
	assertScanned(t,
		scanAll("a\x1b]0;one\x07b\x1b]777;notify;App;msg\x07c"),
		scannedOSC{0, "one"},
		scannedOSC{777, "notify;App;msg"})
}

// The read loop hands over whatever the PTY returned, so a sequence routinely
// straddles a chunk boundary. Splitting one at every byte is the only honest way
// to prove it survives.
func TestOSCScannerSurvivesEverySplitPoint(t *testing.T) {
	const stream = "before\x1b]0;⠀ running\x07after\x1b]777;notify;Claude Code;waiting\x07tail"
	want := []scannedOSC{{0, "⠀ running"}, {777, "notify;Claude Code;waiting"}}
	for split := range len(stream) + 1 {
		got := scanAll(stream[:split], stream[split:])
		if len(got) != len(want) {
			t.Fatalf("split at %d: got %+v, want %+v", split, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("split at %d, sequence %d: got %+v, want %+v", split, i, got[i], want[i])
			}
		}
	}
}

func TestOSCScannerHandlesOneBytePerCall(t *testing.T) {
	const stream = "\x1b]0;⠀ x\x07"
	chunks := make([]string, 0, len(stream))
	for i := range len(stream) {
		chunks = append(chunks, stream[i:i+1])
	}
	assertScanned(t, scanAll(chunks...), scannedOSC{0, "⠀ x"})
}

func TestOSCScannerReportsBareCodeWithNoPayload(t *testing.T) {
	assertScanned(t, scanAll("\x1b]0\x07"), scannedOSC{0, ""})
}

func TestOSCScannerSkipsNonNumericIntroducers(t *testing.T) {
	if got := scanAll("\x1b]hello;there\x07"); got != nil {
		t.Fatalf("got %+v, want nothing", got)
	}
}

// A stray ESC inside an OSC means the producer abandoned the sequence. Scanning
// past it looking for a terminator that will never come would swallow every
// later sequence in the stream.
func TestOSCScannerRecoversFromAnAbandonedSequence(t *testing.T) {
	assertScanned(t,
		scanAll("\x1b]0;broken\x1b[0m\x1b]0;good\x07"),
		scannedOSC{0, "good"})
}

// A producer that never terminates an OSC must not make the scanner buffer the
// rest of the session. Past the cap it gives up on that sequence and keeps
// scanning, so a later well-formed one is still seen.
func TestOSCScannerAbandonsAnOversizedSequence(t *testing.T) {
	flood := "\x1b]0;" + strings.Repeat("x", oscScanMaxPending+64)
	assertScanned(t,
		scanAll(flood, "\x1b]0;recovered\x07"),
		scannedOSC{0, "recovered"})
}

func TestOSCScannerHoldsALogingEscape(t *testing.T) {
	assertScanned(t, scanAll("text\x1b", "]0;title\x07"), scannedOSC{0, "title"})
}

func TestOSCScannerRejectsAnAbsurdCode(t *testing.T) {
	if got := scanAll(fmt.Sprintf("\x1b]%s;x\x07", strings.Repeat("9", 12))); got != nil {
		t.Fatalf("got %+v, want nothing", got)
	}
}

// OSC 133 rides the same stream and is parsed elsewhere; this scanner must
// report it without disturbing anything, since it only reads.
func TestOSCScannerSeesOSC133WithoutConsumingIt(t *testing.T) {
	chunk := []byte("\x1b]133;A\x07prompt")
	before := string(chunk)
	got := scanAll(string(chunk))
	assertScanned(t, got, scannedOSC{133, "A"})
	if string(chunk) != before {
		t.Fatalf("the scanner modified the chunk: %q", string(chunk))
	}
}
