package crew

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func fullPriming() Priming {
	return Priming{
		Member:        "trellis",
		HomeDir:       "/home/victor/.attn/crew/trellis",
		CharterPath:   "/home/victor/.attn/crew/trellis/CHARTER.md",
		CWD:           "/home/victor/projects/attn",
		AwarenessDirs: []string{"/home/victor/projects/pi", "/home/victor/notes"},
		Charter:       "# trellis\n\nI care about the shape of the work.",
		Handoff:       "The epic is half landed; #901 is waiting on review.",
		HandoffName:   "2026-08-13T22-20Z-trellis.md",
		OlderHandoffs: []string{"2026-08-12T21-00Z-trellis.md", "2026-08-11T20-00Z-trellis.md"},
	}
}

// The block is the whole of what a woken member knows about being itself: its
// name, where it lives, what it launched into, its charter, the letter left for
// it, and how to leave the next one.
func TestPriming_BlockCarriesEverythingAWokenMemberNeeds(t *testing.T) {
	block := fullPriming().Block()

	for _, want := range []string{
		"You are **trellis**",
		"/home/victor/.attn/crew/trellis",
		"/home/victor/projects/attn",
		"/home/victor/projects/pi",
		"/home/victor/notes",
		"## Your charter (/home/victor/.attn/crew/trellis/CHARTER.md)",
		"I care about the shape of the work.",
		"## Your predecessor's letter (2026-08-13T22-20Z-trellis.md)",
		"#901 is waiting on review",
		"2026-08-12T21-00Z-trellis.md",
		"handoffs/<UTC timestamp>-trellis.md",
		"attn seed note <id> --handoff",
		"Only one session is trellis at a time",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not carry %q", want)
		}
	}
}

// A session nobody woke as a member is primed with nothing: the crew block is
// the identity, so an unbound session must not receive a truncated version of
// one.
func TestPriming_AnUnboundSessionGetsNoBlock(t *testing.T) {
	if got := (Priming{HomeDir: "/somewhere", Charter: "# nobody"}).Block(); got != "" {
		t.Fatalf("an unbound session was primed with %q", got)
	}
}

// A member with no charter and no letter is on its first day, and the block says
// so with what to do about it rather than leaving two silent gaps.
func TestPriming_AFirstDayNamesWhatIsMissing(t *testing.T) {
	block := Priming{Member: "sable", HomeDir: "/homes/sable"}.Block()

	for _, want := range []string{
		"this is your first day",
		"/homes/sable/CHARTER.md",
		"No letter was left for you",
		"/homes/sable/handoffs",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("a first day does not say %q", want)
		}
	}
}

// A file nothing like a real one is cut rather than inlined whole, and the cut
// says where the rest is — a member told half a charter with no path has no way
// back to the other half.
func TestPriming_AnOversizeFileIsCutWithWhereTheRestIs(t *testing.T) {
	p := fullPriming()
	p.Charter = strings.Repeat("charter. ", CharterLimit/4)
	block := p.Block()

	if len(block) > CharterLimit+HandoffLimit+8000 {
		t.Fatalf("block is %d bytes; the limits did not bound it", len(block))
	}
	if !strings.Contains(block, "[cut at ") || !strings.Contains(block, p.CharterPath) {
		t.Error("the cut does not say where the whole file is")
	}
}

// Markdown carries any Unicode, so a cut lands on a rune boundary — a block
// ending mid-rune is invalid UTF-8 in a system prompt.
func TestPriming_ACutLandsOnARuneBoundary(t *testing.T) {
	p := Priming{Member: "keel", HomeDir: "/homes/keel", CharterPath: "/homes/keel/CHARTER.md"}
	// 3-byte runes never align with the limit, so a naive slice splits one.
	p.Charter = strings.Repeat("日", CharterLimit)

	block := p.Block()
	if !utf8.ValidString(block) {
		t.Fatal("the cut split a rune: the block is not valid UTF-8")
	}
	if !strings.Contains(block, "[cut at ") {
		t.Fatal("an oversize charter was not cut")
	}
}

// The file names are UTC timestamps, so lexicographic order is chronological.
// Freshest first is what makes names[0] the letter to inline.
func TestPriming_HandoffNamesSortFreshestFirst(t *testing.T) {
	names := []string{
		"2026-08-11T20-00Z-keel.md",
		"2026-08-13T22-10Z-keel.md",
		"2026-08-12T21-00Z-keel.md",
	}
	SortHandoffNames(names)
	if names[0] != "2026-08-13T22-10Z-keel.md" || names[2] != "2026-08-11T20-00Z-keel.md" {
		t.Fatalf("sorted to %v, want freshest first", names)
	}
}
