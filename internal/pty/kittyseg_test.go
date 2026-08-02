package pty

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const (
	kittyIntro = "\x1b_G"
	kittyST    = "\x1b\\"
)

// Escapes the battery reuses. Control sections are shaped like real emitter
// output (icat/chafa/timg) so a case that breaks reads like something a user
// would hit, but nothing here parses them — only their boundaries matter.
var (
	kittyDirectRGB = kittyIntro + "a=T,f=24,s=2,v=2;QUJDRA==" + kittyST
	kittyPNGChunk1 = kittyIntro + "a=T,f=100,m=1;iVBORw0K" + kittyST
	kittyPNGChunk2 = kittyIntro + "m=1;GgoAAABJ" + kittyST
	kittyPNGFinal  = kittyIntro + "m=0;" + kittyST
	kittyQuery     = kittyIntro + "a=q,i=31,s=1,v=1,f=24;AAAA" + kittyST
	kittyEmpty     = kittyIntro + kittyST
	kittyBig       = kittyIntro + "a=T,f=32,s=8,v=8;" + strings.Repeat("QUJD", 64) + kittyST
	kittyWithBEL   = kittyIntro + "a=T,f=24;AA\x07BB" + kittyST
	kittyRecovered = kittyIntro + "i=9;BB" + kittyST
)

// kittyEmission is one emit call, normalized: adjacent plain runs are merged.
// How Feed groups plain bytes is not part of the contract — a chunk boundary
// legitimately cuts a run in two — but their order and content are, and so is
// every extracted APC.
type kittyEmission struct {
	apc   bool
	bytes string
}

func plainEmission(s string) kittyEmission { return kittyEmission{bytes: s} }
func apcEmission(s string) kittyEmission   { return kittyEmission{apc: true, bytes: s} }

func (e kittyEmission) String() string {
	if e.apc {
		return fmt.Sprintf("apc(%q)", e.bytes)
	}
	return fmt.Sprintf("plain(%q)", e.bytes)
}

type kittySegCase struct {
	name  string
	input string
	want  []kittyEmission
	// pending is what the segmenter still holds when the stream stops here.
	pending string
}

var kittySegBattery = []kittySegCase{
	{
		name:  "plain output holds no kitty",
		input: "hello\r\nworld ✓",
		want:  []kittyEmission{plainEmission("hello\r\nworld ✓")},
	},
	{
		name:  "one complete apc alone",
		input: kittyDirectRGB,
		want:  []kittyEmission{apcEmission(kittyDirectRGB)},
	},
	{
		name:  "apc surrounded by text",
		input: "before" + kittyDirectRGB + "after",
		want: []kittyEmission{
			plainEmission("before"),
			apcEmission(kittyDirectRGB),
			plainEmission("after"),
		},
	},
	{
		name:  "back to back apcs of a chunked transmission",
		input: kittyPNGChunk1 + kittyPNGChunk2 + kittyPNGFinal,
		want: []kittyEmission{
			apcEmission(kittyPNGChunk1),
			apcEmission(kittyPNGChunk2),
			apcEmission(kittyPNGFinal),
		},
	},
	{
		name:  "apcs separated by text",
		input: "a" + kittyDirectRGB + "b" + kittyQuery + "c",
		want: []kittyEmission{
			plainEmission("a"),
			apcEmission(kittyDirectRGB),
			plainEmission("b"),
			apcEmission(kittyQuery),
			plainEmission("c"),
		},
	},
	{
		name:  "apc with an empty payload",
		input: "x" + kittyEmpty + "y",
		want: []kittyEmission{
			plainEmission("x"),
			apcEmission(kittyEmpty),
			plainEmission("y"),
		},
	},
	{
		name:  "apc with a payload longer than any introducer scan window",
		input: "head" + kittyBig + "tail",
		want: []kittyEmission{
			plainEmission("head"),
			apcEmission(kittyBig),
			plainEmission("tail"),
		},
	},
	{
		// BEL ends an OSC, not an APC. Treating it as a terminator would cut
		// the escape in half and ship the remainder to the wire as text.
		name:  "bel inside the payload is an ordinary byte",
		input: kittyWithBEL + "tail",
		want: []kittyEmission{
			apcEmission(kittyWithBEL),
			plainEmission("tail"),
		},
	},
	{
		name:  "backslash inside the payload does not terminate",
		input: kittyIntro + "a=T,f=24;A\\B" + kittyST,
		want:  []kittyEmission{apcEmission(kittyIntro + "a=T,f=24;A\\B" + kittyST)},
	},
	{
		name:  "the glyph apc is not ours",
		input: "x\x1b_25a1;GLYPH\x1b\\y",
		want:  []kittyEmission{plainEmission("x\x1b_25a1;GLYPH\x1b\\y")},
	},
	{
		name:  "an unknown apc protocol byte is not ours",
		input: "\x1b_Zwhatever\x1b\\",
		want:  []kittyEmission{plainEmission("\x1b_Zwhatever\x1b\\")},
	},
	{
		name:  "osc and csi sequences stay plain",
		input: "\x1b]0;title\x07\x1b[1;31mred\x1b[0m" + kittyQuery + "\x1b]133;A\x07",
		want: []kittyEmission{
			plainEmission("\x1b]0;title\x07\x1b[1;31mred\x1b[0m"),
			apcEmission(kittyQuery),
			plainEmission("\x1b]133;A\x07"),
		},
	},
	{
		name:  "esc st with no introducer stays plain",
		input: "a\x1b\\b",
		want:  []kittyEmission{plainEmission("a\x1b\\b")},
	},
	{
		name:  "an esc before the introducer stays plain",
		input: "\x1b" + kittyRecovered,
		want: []kittyEmission{
			plainEmission("\x1b"),
			apcEmission(kittyRecovered),
		},
	},
	{
		name:  "a lone esc between two apcs stays plain",
		input: kittyDirectRGB + "\x1b" + kittyRecovered,
		want: []kittyEmission{
			apcEmission(kittyDirectRGB),
			plainEmission("\x1b"),
			apcEmission(kittyRecovered),
		},
	},
	{
		// A stray ESC means the producer abandoned the sequence. Waiting for a
		// terminator that will never come would swallow the rest of the stream.
		name:  "a stray esc abandons the apc",
		input: kittyIntro + "a=T,f=24;AAAA\x1b[0mback to text",
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T,f=24;AAAA\x1b[0mback to text")},
	},
	{
		name:  "a stray esc that starts a new apc still yields that apc",
		input: kittyIntro + "a=T;AA" + kittyRecovered + "tail",
		want: []kittyEmission{
			plainEmission(kittyIntro + "a=T;AA"),
			apcEmission(kittyRecovered),
			plainEmission("tail"),
		},
	},
	{
		name:  "an abandoned apc does not hide a later one",
		input: kittyIntro + "a=T;AA\x1b[0m" + kittyRecovered + "tail",
		want: []kittyEmission{
			plainEmission(kittyIntro + "a=T;AA\x1b[0m"),
			apcEmission(kittyRecovered),
			plainEmission("tail"),
		},
	},
	{
		name:    "the stream ends on a lone esc",
		input:   "tail\x1b",
		want:    []kittyEmission{plainEmission("tail")},
		pending: "\x1b",
	},
	{
		name:    "the stream ends on a partial introducer",
		input:   "tail\x1b_",
		want:    []kittyEmission{plainEmission("tail")},
		pending: "\x1b_",
	},
	{
		name:    "the stream ends inside an apc payload",
		input:   "text" + kittyIntro + "a=T,f=24;AAAA",
		want:    []kittyEmission{plainEmission("text")},
		pending: kittyIntro + "a=T,f=24;AAAA",
	},
	{
		name:    "the stream ends on a partial terminator",
		input:   kittyIntro + "a=T;AA\x1b",
		pending: kittyIntro + "a=T;AA\x1b",
	},
	{
		name:    "an abandoned apc followed by a partial introducer",
		input:   kittyIntro + "a=T;AA\x1b_",
		want:    []kittyEmission{plainEmission(kittyIntro + "a=T;AA")},
		pending: "\x1b_",
	},
}

// runKittySegmenter feeds chunks through one segmenter and returns the
// normalized emissions plus whatever it still holds back. It also proves the
// segmenter never writes into the caller's chunk: the read loop hands over a
// buffer it reuses, and the same bytes are on their way to the wire.
func runKittySegmenter(t *testing.T, chunks []string) ([]kittyEmission, string) {
	t.Helper()
	seg := &kittyAPCSegmenter{}
	var out []kittyEmission
	for _, chunk := range chunks {
		out = feedKitty(t, seg, chunk, out)
	}
	return out, string(seg.pending)
}

// feedKitty pushes one chunk through seg, appending its emissions to out in
// normalized form and checking the per-call contract.
func feedKitty(t *testing.T, seg *kittyAPCSegmenter, chunk string, out []kittyEmission) []kittyEmission {
	t.Helper()
	input := []byte(chunk)
	seg.Feed(input, func(plain, apc []byte) {
		switch {
		case (plain == nil) == (apc == nil):
			t.Fatalf("emit wants exactly one argument, got plain=%q apc=%q", plain, apc)
		case apc != nil:
			out = append(out, apcEmission(string(apc)))
		case len(plain) == 0:
			t.Fatal("emit called with an empty plain run")
		default:
			if n := len(out); n > 0 && !out[n-1].apc {
				out[n-1].bytes += string(plain)
				return
			}
			out = append(out, plainEmission(string(plain)))
		}
	})
	if string(input) != chunk {
		t.Fatalf("the segmenter modified the chunk: %q, want %q", input, chunk)
	}
	return out
}

func formatKittyEmissions(e []kittyEmission) string {
	parts := make([]string, 0, len(e))
	for _, item := range e {
		parts = append(parts, item.String())
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func kittyEmissionsEqual(got, want []kittyEmission) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type kittyChunking struct {
	label  string
	chunks []string
}

// splitCycling cuts s into chunks whose sizes cycle through sizes, so a case is
// also seen with several boundaries at once rather than the single moving
// boundary of the every-offset sweep.
func splitCycling(s string, sizes ...int) []string {
	var chunks []string
	for i := 0; len(s) > 0; i++ {
		n := min(sizes[i%len(sizes)], len(s))
		chunks = append(chunks, s[:n])
		s = s[n:]
	}
	return chunks
}

// kittyChunkings enumerates the ways the read loop could hand this input over.
// A PTY read returns whatever the kernel had, so an escape routinely straddles
// a boundary and the segmenter must not care where the boundary fell.
func kittyChunkings(input string) []kittyChunking {
	out := []kittyChunking{{label: "whole", chunks: []string{input}}}
	for split := range len(input) + 1 {
		out = append(out, kittyChunking{
			label:  fmt.Sprintf("split at %d", split),
			chunks: []string{input[:split], input[split:]},
		})
	}
	byteAtATime := make([]string, 0, len(input))
	for i := range len(input) {
		byteAtATime = append(byteAtATime, input[i:i+1])
	}
	out = append(out, kittyChunking{label: "one byte per call", chunks: byteAtATime})
	for _, sizes := range [][]int{{1, 2, 3, 5, 8, 13}, {7, 1, 4, 2}, {2, 11, 3}} {
		out = append(out, kittyChunking{
			label:  fmt.Sprintf("cycling %v", sizes),
			chunks: splitCycling(input, sizes...),
		})
	}
	return out
}

// TestKittyAPCSegmenterBatteryUnderEveryChunking is the semantic spec (what
// counts as an APC and what does not) and the cross-chunk property at once:
// every chunking of a case must produce the one pinned result. Cross-chunk
// buffering is the entire reason this component exists, so a bug there has to
// fail here.
func TestKittyAPCSegmenterBatteryUnderEveryChunking(t *testing.T) {
	for _, c := range kittySegBattery {
		t.Run(c.name, func(t *testing.T) {
			for _, chunking := range kittyChunkings(c.input) {
				got, pending := runKittySegmenter(t, chunking.chunks)
				if !kittyEmissionsEqual(got, c.want) {
					t.Fatalf("%s:\n got: %s\nwant: %s",
						chunking.label, formatKittyEmissions(got), formatKittyEmissions(c.want))
				}
				if pending != c.pending {
					t.Fatalf("%s: pending %q, want %q", chunking.label, pending, c.pending)
				}
			}
		})
	}
}

// TestKittyAPCSegmenterEmitsEveryByteExactlyOnce is THE invariant: the
// emissions plus the held tail reconstruct the input byte for byte. The
// terminal is fed every emission in order, so a byte dropped on an abandon or
// duplicated on a hold is a silent divergence between the worker grid and the
// client grid.
func TestKittyAPCSegmenterEmitsEveryByteExactlyOnce(t *testing.T) {
	for _, c := range kittySegBattery {
		t.Run(c.name, func(t *testing.T) {
			for _, chunking := range kittyChunkings(c.input) {
				got, pending := runKittySegmenter(t, chunking.chunks)
				var rebuilt strings.Builder
				for _, e := range got {
					rebuilt.WriteString(e.bytes)
				}
				rebuilt.WriteString(pending)
				if rebuilt.String() != c.input {
					t.Fatalf("%s: emissions rebuild %q, want %q",
						chunking.label, rebuilt.String(), c.input)
				}
			}
		})
	}
}

// TestKittyAPCSegmenterJoinsAnAPCSplitAcrossChunks names the three splits that
// matter, so a failure says which boundary broke rather than only an offset.
func TestKittyAPCSegmenterJoinsAnAPCSplitAcrossChunks(t *testing.T) {
	apc := kittyIntro + "a=T,f=24,s=1,v=1;QQ==" + kittyST
	cases := []struct {
		name   string
		chunks []string
	}{
		{"mid introducer", []string{"text\x1b", apc[1:] + "tail"}},
		{"mid payload", []string{"text" + apc[:12], apc[12:] + "tail"}},
		{"mid st", []string{"text" + apc[:len(apc)-1], apc[len(apc)-1:] + "tail"}},
	}
	want := []kittyEmission{plainEmission("text"), apcEmission(apc), plainEmission("tail")}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, pending := runKittySegmenter(t, c.chunks)
			if !kittyEmissionsEqual(got, want) {
				t.Fatalf("got %s, want %s", formatKittyEmissions(got), formatKittyEmissions(want))
			}
			if pending != "" {
				t.Fatalf("pending %q, want nothing held", pending)
			}
		})
	}
}

// TestKittyAPCSegmenterFastPathPassesTheChunkThrough pins the no-copy contract
// osc133Segmenter also carries: the overwhelmingly common chunk is plain
// output, and it must reach the terminal without an allocation.
func TestKittyAPCSegmenterFastPathPassesTheChunkThrough(t *testing.T) {
	var seg kittyAPCSegmenter
	chunk := []byte("plain output with no escapes\r\n")
	calls := 0
	seg.Feed(chunk, func(plain, apc []byte) {
		calls++
		if apc != nil {
			t.Fatalf("a plain chunk produced an apc: %q", apc)
		}
		if len(plain) != len(chunk) || &plain[0] != &chunk[0] {
			t.Fatalf("a plain chunk was copied: emitted %q, want the input slice itself", plain)
		}
	})
	if calls != 1 {
		t.Fatalf("a plain chunk produced %d emissions, want 1", calls)
	}
	if seg.pending != nil {
		t.Fatalf("a plain chunk left %q pending", seg.pending)
	}
}

// TestKittyAPCSegmenterAbandonsAnOversizedAPC exercises the tripwire: a
// producer that never terminates an APC must not make the worker buffer the
// rest of the session. Past the threshold the held bytes are flushed as plain —
// never dropped — and the segmenter keeps working on what follows.
func TestKittyAPCSegmenterAbandonsAnOversizedAPC(t *testing.T) {
	const head = kittyIntro + "a=T,f=24,s=4096,v=4096;"
	flood := make([]byte, len(head)+kittySegMaxPendingBytes+1)
	copy(flood, head)
	for i := len(head); i < len(flood); i++ {
		flood[i] = 'A'
	}

	var seg kittyAPCSegmenter
	consumed := 0
	seg.Feed(flood, func(plain, apc []byte) {
		if apc != nil {
			t.Fatalf("an oversized apc was extracted (%d bytes)", len(apc))
		}
		if !bytes.Equal(plain, flood[consumed:consumed+len(plain)]) {
			t.Fatalf("plain run at %d does not match the input", consumed)
		}
		consumed += len(plain)
	})
	if consumed != len(flood) {
		t.Fatalf("flushed %d bytes of the oversized apc, want all %d", consumed, len(flood))
	}
	if seg.pending != nil {
		t.Fatalf("the oversized apc left %d bytes pending, want none", len(seg.pending))
	}

	emissions := feedKitty(t, &seg, kittyRecovered, nil)
	if !kittyEmissionsEqual(emissions, []kittyEmission{apcEmission(kittyRecovered)}) {
		t.Fatalf("after abandoning, got %s, want the next apc", formatKittyEmissions(emissions))
	}
	if seg.pending != nil {
		t.Fatalf("pending %q, want nothing held", seg.pending)
	}
}

// BenchmarkKittyAPCSegmenterUnterminatedAPC walks an APC that never terminates
// toward the tripwire in PTY-sized reads — the shape a `cat` of a captured,
// truncated kitty stream produces. It is the cost of the pending path with the
// APC still open, which is the only path where per-Feed work can depend on
// everything already buffered rather than on the chunk. The sizes are there to
// show the growth rate: work per byte must not rise with the size.
func BenchmarkKittyAPCSegmenterUnterminatedAPC(b *testing.B) {
	const chunkSize = 4096
	head := []byte(kittyIntro + "a=T,f=24,s=4096,v=4096;")
	chunk := bytes.Repeat([]byte("A"), chunkSize)

	for _, size := range []int{4 << 20, 16 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				var seg kittyAPCSegmenter
				var emitted int
				sink := func(plain, apc []byte) { emitted += len(plain) + len(apc) }
				seg.Feed(head, sink)
				for fed := 0; fed < size; fed += chunkSize {
					seg.Feed(chunk, sink)
				}
				if emitted != 0 {
					b.Fatalf("an unterminated apc emitted %d bytes before the tripwire", emitted)
				}
			}
		})
	}
}
