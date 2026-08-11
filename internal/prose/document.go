package prose

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/jdkato/prose/v3/segment"
	"github.com/jdkato/prose/v3/tag"
	"github.com/jdkato/prose/v3/tokenize"
)

// Token is a word with its part-of-speech tag and its byte offset in the file.
type Token struct {
	Text string
	Tag  string
	// Start and End are byte offsets into the source. For a word that stood in
	// for markup — a code span — they cover the markup it replaced.
	Start int
	End   int
}

// Sentence is one sentence of prose, located in the file it came from.
type Sentence struct {
	Text   string
	Start  int
	End    int
	Tokens []Token
	Block  int
}

// Block is a paragraph, list item, or heading: the unit the rhythm rules
// judge, because rhythm is a property of a passage rather than a file.
type Block struct {
	Kind      BlockKind
	Sentences []int
}

// Document is a parsed file: its prose blocks, their sentences, and enough of
// the original bytes to quote and locate any span.
type Document struct {
	File   string
	Source string

	Blocks    []Block
	Sentences []Sentence

	lineStarts []int
}

// analyzer holds the models. Loading them costs about 20ms and 5MB, so they
// load once per process and are shared; both are safe for concurrent use.
var analyzer = sync.OnceValues(func() (*models, error) {
	seg, err := segment.New()
	if err != nil {
		return nil, fmt.Errorf("load sentence segmenter: %w", err)
	}
	tagger, err := tag.New(tag.WithLexicon(tag.Lexicon{
		codeSpanPlaceholder: "NN",
	}))
	if err != nil {
		return nil, fmt.Errorf("load part-of-speech tagger: %w", err)
	}
	return &models{seg: seg, tagger: tagger, tok: tokenize.New()}, nil
})

type models struct {
	seg    *segment.Segmenter
	tagger *tag.Tagger
	tok    *tokenize.Tokenizer
}

// Parse reads a Markdown source into the sentences the rules judge.
func Parse(file string, source []byte) (*Document, error) {
	m, err := analyzer()
	if err != nil {
		return nil, err
	}

	doc := &Document{
		File:       file,
		Source:     string(source),
		lineStarts: lineStarts(source),
	}

	for _, raw := range extractBlocks(source) {
		block := Block{Kind: raw.kind}
		for _, sent := range m.seg.Segment(raw.text) {
			start, end := raw.m.sourceRange(sent.Start, sent.Start+len(sent.Text))
			if start >= end {
				continue
			}
			block.Sentences = append(block.Sentences, len(doc.Sentences))
			doc.Sentences = append(doc.Sentences, Sentence{
				Text:   sent.Text,
				Start:  start,
				End:    end,
				Tokens: tagSentence(m, raw, sent.Start, sent.Text),
				Block:  len(doc.Blocks),
			})
		}
		if len(block.Sentences) > 0 {
			doc.Blocks = append(doc.Blocks, block)
		}
	}
	return doc, nil
}

// tagSentence tokenizes and tags one sentence, rebasing every token's offset
// from sentence coordinates back to the file.
func tagSentence(m *models, raw rawBlock, sentStart int, sentText string) []Token {
	tokens := m.tok.Tokenize(sentText)
	m.tagger.TagTokens(tokens)

	out := make([]Token, 0, len(tokens))
	for _, t := range tokens {
		inBlock := sentStart + t.Start
		start, end := raw.m.sourceRange(inBlock, inBlock+len(t.Text))
		out = append(out, Token{Text: t.Text, Tag: t.Tag, Start: start, End: end})
	}
	return out
}

// Position converts a byte offset into a 1-indexed line and column.
func (d *Document) Position(offset int) (line, column int) {
	lo, hi := 0, len(d.lineStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if d.lineStarts[mid] <= offset {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1, offset - d.lineStarts[lo] + 1
}

// finding builds a Finding for a source range, quoting the bytes themselves so
// the writer sees what they wrote rather than what the parser made of it.
func (d *Document) finding(rule string, start, end int, objection, suggestion string) Finding {
	start = max(0, min(start, len(d.Source)))
	end = max(start, min(end, len(d.Source)))
	line, column := d.Position(start)
	return Finding{
		Rule:       rule,
		Layer:      LayerDeterministic,
		File:       d.File,
		Line:       line,
		Column:     column,
		Start:      start,
		End:        end,
		Span:       collapseSpace(d.Source[start:end]),
		Objection:  objection,
		Suggestion: suggestion,
	}
}

func lineStarts(source []byte) []int {
	starts := []int{0}
	for i, b := range source {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// collapseSpace folds the newlines of a wrapped paragraph into single spaces so
// a quoted sentence prints on one line, and drops the bold and code markers
// the span may have cut through.
//
// A finding quotes the source bytes, which is what makes it findable in the
// file. Those bytes carry markup the linter never read: a span ending inside
// `must be **transcribed**` quotes the opening `**` and not its partner, which
// reads as a defect in the tool. Runs of two or more asterisks or underscores
// and backticks are markup wherever they appear, so removing them loses no
// prose. A single asterisk is left alone: it is balanced far more often than
// not, and it can be literal.
var markupMarkers = strings.NewReplacer("**", "", "__", "", "`", "")

func collapseSpace(s string) string {
	return markupMarkers.Replace(strings.Join(strings.Fields(s), " "))
}

// isWord reports whether a token is a word rather than punctuation. Tag is the
// wrong thing to test: Penn spells a bracket -LRB-, which is all letters.
func isWord(t Token) bool {
	for _, r := range t.Text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// words returns the word tokens of a sentence, punctuation dropped.
func (s Sentence) words() []Token {
	out := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if isWord(t) {
			out = append(out, t)
		}
	}
	return out
}

// wordCount is the sentence length every length-shaped rule measures.
func (s Sentence) wordCount() int { return len(s.words()) }
