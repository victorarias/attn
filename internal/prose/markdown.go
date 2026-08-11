package prose

import (
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// BlockKind distinguishes running prose from the fragments around it. Rules
// about rhythm — length, density, staccato, variance, cohesion — only make
// sense on running prose; a heading is a label, not a sentence.
type BlockKind string

const (
	// KindParagraph is running prose, including prose inside a blockquote.
	KindParagraph BlockKind = "paragraph"
	// KindListItem is a bullet's text. Sentence-level rules apply; rhythm
	// rules see each item as its own block and mostly find it too short to
	// judge, which is correct — a list is not a paragraph.
	KindListItem BlockKind = "list-item"
	// KindHeading is a title. Only word-level rules apply.
	KindHeading BlockKind = "heading"
)

// codeSpanPlaceholder stands in for `inline code` while the text is tagged.
// A code span reads as one noun in the sentence around it ("`attn prose check`
// is the base verb"), so collapsing it to a single noun is both what the
// grammar wants and what keeps a path like internal/foo/bar.go from
// tokenizing into a five-noun string. The placeholder never reaches a
// finding: findings quote the source bytes the span maps back to.
const codeSpanPlaceholder = "widget"

// piece maps a run of extracted block text back to the source it came from.
//
// A verbatim piece is a 1:1 byte run: source[srcStart+k] is text[textStart+k].
// A synthetic piece — a placeholder, or the space standing in for a skipped
// link target — has no such correspondence, so every offset inside it maps to
// the start of the source range it replaced.
type piece struct {
	textStart int
	textLen   int
	srcStart  int
	srcLen    int
	verbatim  bool
}

// mapped is block text plus the provenance needed to quote the source.
type mapped struct {
	text   strings.Builder
	pieces []piece
}

func (m *mapped) addVerbatim(s string, srcStart int) {
	if s == "" {
		return
	}
	m.pieces = append(m.pieces, piece{
		textStart: m.text.Len(),
		textLen:   len(s),
		srcStart:  srcStart,
		srcLen:    len(s),
		verbatim:  true,
	})
	m.text.WriteString(s)
}

func (m *mapped) addSynthetic(s string, srcStart, srcLen int) {
	if s == "" {
		return
	}
	m.pieces = append(m.pieces, piece{
		textStart: m.text.Len(),
		textLen:   len(s),
		srcStart:  srcStart,
		srcLen:    srcLen,
	})
	m.text.WriteString(s)
}

// sourceEnd is where the block has reached in the source so far. It anchors a
// synthetic piece whose own node carries no segment.
func (m *mapped) sourceEnd() int {
	if len(m.pieces) == 0 {
		return 0
	}
	last := m.pieces[len(m.pieces)-1]
	return last.srcStart + last.srcLen
}

// sourceRange maps a half-open range of block text back to a half-open range
// of source bytes. A range that starts or ends inside a synthetic piece
// widens to cover the whole source range that piece replaced, so a finding
// never quotes half of a code span.
func (m *mapped) sourceRange(textStart, textEnd int) (int, int) {
	if len(m.pieces) == 0 {
		return 0, 0
	}
	srcStart, okStart := -1, false
	srcEnd := 0
	for _, p := range m.pieces {
		pTextEnd := p.textStart + p.textLen
		if pTextEnd <= textStart || p.textStart >= textEnd {
			continue
		}
		var s, e int
		if p.verbatim {
			s = p.srcStart + max(0, textStart-p.textStart)
			e = p.srcStart + min(p.textLen, textEnd-p.textStart)
		} else {
			s, e = p.srcStart, p.srcStart+p.srcLen
		}
		if !okStart {
			srcStart, okStart = s, true
		}
		srcEnd = max(srcEnd, e)
	}
	if !okStart {
		return 0, 0
	}
	return srcStart, srcEnd
}

// rawBlock is one prose block as extracted from the Markdown: its text with
// markup collapsed, and the mapping back to the file.
type rawBlock struct {
	kind BlockKind
	text string
	m    *mapped
}

// markdownParser is GFM, because that is the dialect the repository's docs are
// written in. Without the table extension a table is not a table: its rows
// parse as paragraphs, and a pipe-delimited row lints as a 200-word sentence.
var markdownParser = sync.OnceValue(func() parser.Parser {
	return goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
})

// extractBlocks returns the prose blocks of a Markdown source in reading
// order. Code fences (mermaid among them), indented code, raw HTML, front
// matter, and link targets never appear: they are not prose and linting them
// produces nothing a writer can act on.
func extractBlocks(source []byte) []rawBlock {
	source = blankFrontMatter(source)

	root := markdownParser().Parse(text.NewReader(source))

	var blocks []rawBlock
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		blocks = collectBlocks(blocks, n, source)
	}
	return blocks
}

// collectBlocks walks a block-level node, appending the prose blocks under it.
func collectBlocks(out []rawBlock, n ast.Node, source []byte) []rawBlock {
	switch node := n.(type) {
	case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock, *ast.ThematicBreak:
		return out

	case *east.Table:
		// A table cell is a label or a value, not running prose, and every
		// rhythm rule reads a row as one long sentence.
		return out

	case *ast.Paragraph:
		return appendBlock(out, KindParagraph, node, source)
	case *ast.TextBlock:
		return appendBlock(out, KindListItem, node, source)
	case *ast.Heading:
		return appendBlock(out, KindHeading, node, source)
	}

	// Containers — list, list item, blockquote, document — carry no text of
	// their own.
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = collectBlocks(out, c, source)
	}
	return out
}

func appendBlock(out []rawBlock, kind BlockKind, n ast.Node, source []byte) []rawBlock {
	m := &mapped{}
	appendInlines(m, n, source)
	body := m.text.String()
	if strings.TrimSpace(body) == "" {
		return out
	}
	return append(out, rawBlock{kind: kind, text: body, m: m})
}

// appendInlines flattens a block's inline children into mapped text.
func appendInlines(m *mapped, n ast.Node, source []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch node := c.(type) {
		case *ast.Text:
			seg := node.Segment
			m.addVerbatim(string(seg.Value(source)), seg.Start)
			if node.SoftLineBreak() || node.HardLineBreak() {
				m.addSynthetic(" ", seg.Stop, 0)
			}

		case *ast.CodeSpan:
			start, stop, ok := inlineRange(node, source)
			if !ok {
				continue
			}
			m.addSynthetic(codeSpanPlaceholder, start, stop-start)

		case *ast.AutoLink, *ast.RawHTML, *ast.Image:
			// A bare URL, raw HTML, and an image's alt text are not prose the
			// writer is shaping. Keep a space so the words either side do not
			// glue into one token.
			start, stop, ok := inlineRange(c, source)
			if !ok {
				start, stop = m.sourceEnd(), m.sourceEnd()
			}
			m.addSynthetic(" ", start, stop-start)

		default:
			// Emphasis, links, strikethrough: the markup is not prose but the
			// text inside it is. A link's destination is not in the child
			// stream at all, so walking children skips it for free.
			appendInlines(m, c, source)
		}
	}
}

// inlineRange returns the source range spanned by an inline node's leaf text.
func inlineRange(n ast.Node, source []byte) (int, int, bool) {
	start, stop, ok := -1, -1, false
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		if t, isText := node.(*ast.Text); isText {
			if !ok {
				start, ok = t.Segment.Start, true
			}
			stop = max(stop, t.Segment.Stop)
			return
		}
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(n)
	return start, stop, ok
}

// blankFrontMatter overwrites a leading YAML or TOML front-matter block with
// spaces, keeping newlines so every later byte keeps its offset and its line.
// goldmark has no front-matter concept: left alone, `---` opens a thematic
// break and the metadata below it lints as prose.
func blankFrontMatter(source []byte) []byte {
	fence, ok := frontMatterFence(source)
	if !ok {
		return source
	}
	rest := source[len(fence):]
	end := indexFence(rest, fence)
	if end < 0 {
		return source
	}
	out := make([]byte, len(source))
	copy(out, source)
	for i := range min(len(fence)+end+len(fence), len(out)) {
		if out[i] != '\n' {
			out[i] = ' '
		}
	}
	return out
}

func frontMatterFence(source []byte) (string, bool) {
	for _, fence := range []string{"---\n", "+++\n"} {
		if strings.HasPrefix(string(source), fence) {
			return fence, true
		}
	}
	return "", false
}

// indexFence finds a closing front-matter fence at the start of a line.
func indexFence(rest []byte, fence string) int {
	s := string(rest)
	closing := strings.TrimSuffix(fence, "\n")
	for offset := 0; offset < len(s); {
		lineEnd := strings.IndexByte(s[offset:], '\n')
		var line string
		if lineEnd < 0 {
			line = s[offset:]
		} else {
			line = s[offset : offset+lineEnd]
		}
		if strings.TrimRight(line, " \t") == closing {
			return offset
		}
		if lineEnd < 0 {
			return -1
		}
		offset += lineEnd + 1
	}
	return -1
}
