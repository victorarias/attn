package prose

import (
	"strings"
	"testing"
)

// TestSpansLocateTheirText is the invariant everything else rests on: a
// finding's offsets have to point at the bytes it quotes, or every file:line
// in the output is a lie. Markup makes this easy to lose — a span crossing a
// code fence, an emphasis marker, or a wrapped line all map through pieces
// rather than by adding an offset.
func TestSpansLocateTheirText(t *testing.T) {
	source := []byte(`# A heading

There is a paragraph with ` + "`some/code/span.go`" + ` inside it and **bold
text** wrapped across a line, and it is the case that the determination must
be made about all of it.

- There is a list item too.
`)
	doc, err := Parse("t.md", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, sent := range doc.Sentences {
		if sent.Start < 0 || sent.End > len(source) || sent.Start >= sent.End {
			t.Fatalf("sentence %q has offsets %d..%d outside 0..%d", sent.Text, sent.Start, sent.End, len(source))
		}
		for _, tok := range sent.Tokens {
			if tok.Start < 0 || tok.End > len(source) || tok.Start > tok.End {
				t.Errorf("token %q has offsets %d..%d outside 0..%d", tok.Text, tok.Start, tok.End, len(source))
				continue
			}
			if tok.Start < sent.Start || tok.End > sent.End {
				t.Errorf("token %q at %d..%d escapes its sentence at %d..%d", tok.Text, tok.Start, tok.End, sent.Start, sent.End)
			}
		}
	}
}

func TestPositionCountsLinesAndColumns(t *testing.T) {
	doc, err := Parse("t.md", []byte("one\ntwo\nthree\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offset     int
		line, col  int
		annotation string
	}{
		{0, 1, 1, "first byte"},
		{3, 1, 4, "the newline ending line 1"},
		{4, 2, 1, "first byte of line 2"},
		{8, 3, 1, "first byte of line 3"},
		{12, 3, 5, "last byte of line 3"},
	} {
		line, col := doc.Position(tc.offset)
		if line != tc.line || col != tc.col {
			t.Errorf("Position(%d) = %d:%d, want %d:%d (%s)", tc.offset, line, col, tc.line, tc.col, tc.annotation)
		}
	}
}

func TestNonProseIsNotExtracted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
	}{
		{"fenced code", "Prose.\n\n```go\nfunc consideration() {}\n```\n"},
		{"tilde fence", "Prose.\n\n~~~\nthere is a consideration\n~~~\n"},
		{"mermaid", "Prose.\n\n```mermaid\nflowchart LR\n  A[there is a node] --> B\n```\n"},
		{"indented code", "Prose.\n\n    there is a consideration here\n"},
		{"yaml front matter", "---\nsummary: there is a consideration\n---\n\nProse.\n"},
		{"toml front matter", "+++\nsummary = \"there is a consideration\"\n+++\n\nProse.\n"},
		{"html block", "Prose.\n\n<!-- there is a consideration -->\n"},
		{"table", "Prose.\n\n| a | b |\n| --- | --- |\n| there is a consideration | x |\n"},
		{"link target", "See [the doc](docs/there-is-a-consideration.md).\n"},
		{"bare url", "See https://example.com/there-is-a-consideration for more.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse("t.md", []byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			for _, sent := range doc.Sentences {
				if strings.Contains(strings.ToLower(sent.Text), "consideration") ||
					strings.Contains(strings.ToLower(sent.Text), "there is a node") {
					t.Errorf("%s was linted as prose: %q", tc.name, collapseSpace(sent.Text))
				}
			}
		})
	}
}

// TestUnterminatedFrontMatterIsLeftAlone: a document opening on a thematic
// break is not a document with front matter, and blanking to the end of the
// file would silently swallow it.
func TestUnterminatedFrontMatterIsLeftAlone(t *testing.T) {
	doc, err := Parse("t.md", []byte("---\n\nThere is a paragraph here.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sentences) == 0 {
		t.Fatal("the paragraph after an unterminated fence was swallowed")
	}
}

// TestCodeSpanIsOneNoun: a path inside backticks reads as one noun, not as the
// five its slashes would tokenize into.
func TestCodeSpanIsOneNoun(t *testing.T) {
	source := []byte("The daemon reads `internal/daemon/session/state/machine.go` at boot.\n")
	doc, err := Parse("t.md", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sentences) != 1 {
		t.Fatalf("expected one sentence, got %d", len(doc.Sentences))
	}
	if run := longestNounRunIn(doc.Sentences[0].Tokens); run > 2 {
		t.Errorf("a code span produced a noun run of %d; it should count as one noun", run)
	}
	// The placeholder must never surface: it maps back to the code span, so a
	// finding quotes what the file says.
	found := false
	for _, tok := range doc.Sentences[0].Tokens {
		if tok.Text != codeSpanPlaceholder {
			continue
		}
		found = true
		if got := string(source[tok.Start:tok.End]); got != "internal/daemon/session/state/machine.go" {
			t.Errorf("the placeholder maps to %q, not to the code span's text", got)
		}
	}
	if !found {
		t.Error("the code span did not become a placeholder token")
	}
}

func longestNounRunIn(tokens []Token) int {
	best, run := 0, 0
	for _, t := range tokens {
		if isNoun(t) {
			run++
			best = max(best, run)
			continue
		}
		run = 0
	}
	return best
}
