// Package msgalign is SPIKE-ONLY throwaway code. It answers one question:
// given an agent's last assistant message as markdown (from the transcript) and
// the rendered terminal grid (from the worker's parsed terminal), can we map
// between the two well enough to (a) light up the message's rows in place and
// (b) turn a user-selected row range back into an exact quote of the agent's
// own markdown?
//
// The design under test is "the transcript span is the anchor, the grid is a
// projection of it" — so the mapping that actually matters is the REVERSE one:
// grid rows -> markdown offsets.
//
// Approach: both sides are reduced to word tokens carrying provenance (source
// offsets on one side, row/column on the other), then aligned as sequences.
// Word-level alignment makes every rendering transform we observed irrelevant
// by construction: soft wrapping is just a word boundary, indentation and blank
// lines vanish, box-drawing table chrome and code-fence markers drop out as
// empty tokens, and stripped markdown emphasis never enters the comparison.
package msgalign

import (
	"strings"
	"unicode"
)

// SrcToken is one word of the markdown, with its offsets in the ORIGINAL
// markdown (not the normalized form) so a recovered quote is verbatim source.
type SrcToken struct {
	Norm  string
	Start int
	End   int
}

// GridToken is one word of a rendered row.
type GridToken struct {
	Norm   string
	Row    int
	Col    int
	EndCol int
}

// markdown syntax and TUI chrome that never survives into (or never appears in)
// the other side's text. Stripped from both sides before comparison.
const cutRunes = "*_`~#|>" + // markdown emphasis / code / heading / table / quote
	"┌─┬┐│├┼┤└┴┘━┃┏┓┗┛" + // box-drawing the TUI draws tables with
	"⏺✻❯⎿·▪●○" // agent/TUI marker glyphs

// normalize reduces a raw word to its comparable form. A word that is pure
// syntax or chrome normalizes to "" and is dropped.
func normalize(word string) string {
	var b strings.Builder
	for _, r := range word {
		if strings.ContainsRune(cutRunes, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TokenizeMarkdown emits source tokens in document order.
func TokenizeMarkdown(md string) []SrcToken {
	var out []SrcToken
	runes := []rune(md)
	byteOff := make([]int, len(runes)+1)
	off := 0
	for i, r := range runes {
		byteOff[i] = off
		off += len(string(r))
	}
	byteOff[len(runes)] = off

	i := 0
	for i < len(runes) {
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && !unicode.IsSpace(runes[j]) {
			j++
		}
		if n := normalize(string(runes[i:j])); n != "" {
			out = append(out, SrcToken{Norm: n, Start: byteOff[i], End: byteOff[j]})
		}
		i = j
	}
	return out
}

// TokenizeGrid emits grid tokens in reading order (row-major).
func TokenizeGrid(rows []string) []GridToken {
	var out []GridToken
	for r, row := range rows {
		runes := []rune(row)
		i := 0
		for i < len(runes) {
			if unicode.IsSpace(runes[i]) {
				i++
				continue
			}
			j := i
			for j < len(runes) && !unicode.IsSpace(runes[j]) {
				j++
			}
			if n := normalize(string(runes[i:j])); n != "" {
				out = append(out, GridToken{Norm: n, Row: r, Col: i, EndCol: j})
			}
			i = j
		}
	}
	return out
}

// Pair is one aligned token pair.
type Pair struct {
	SrcIdx  int
	GridIdx int
}

// align returns the longest common subsequence of the two token streams as
// index pairs. LCS (rather than a greedy scan) because repeated words are
// common in prose and a greedy match latches onto the wrong occurrence.
func align(src []SrcToken, grid []GridToken) []Pair {
	n, m := len(src), len(grid)
	if n == 0 || m == 0 {
		return nil
	}

	// (n+1)*(m+1) int32 table. The spike's corpus keeps this small; a
	// production implementation would band or anchor this (see NOTES.md).
	table := make([][]int32, n+1)
	for i := range table {
		table[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if src[i].Norm == grid[j].Norm {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}

	var pairs []Pair
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case src[i].Norm == grid[j].Norm:
			pairs = append(pairs, Pair{SrcIdx: i, GridIdx: j})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			j++
		}
	}
	return pairs
}

// RowSpan is the markdown range one grid row was resolved to.
type RowSpan struct {
	Row int
	// Start/End are byte offsets into the original markdown.
	Start int
	End   int
	// Matched is how many of this row's tokens aligned; Total is how many it has.
	Matched int
	Total   int
}

// Confidence is the fraction of the row's tokens that aligned.
func (r RowSpan) Confidence() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Matched) / float64(r.Total)
}

// Alignment is the full result for one (markdown, grid) pair.
type Alignment struct {
	Markdown string
	Src      []SrcToken
	Grid     []GridToken
	Pairs    []Pair
	// Rows is keyed by grid row, for rows that resolved to at least one token.
	Rows map[int]RowSpan
	// FirstRow/LastRow bound the rows attributed to the message.
	FirstRow int
	LastRow  int
	// Inversions counts rows whose resolved start offset goes backwards
	// relative to the previous resolved row — the signature of a wrong match.
	Inversions int
}

// Align maps a rendered grid onto the markdown it was rendered from.
func Align(markdown string, rows []string) Alignment {
	src := TokenizeMarkdown(markdown)
	grid := TokenizeGrid(rows)
	pairs := align(src, grid)

	totals := map[int]int{}
	for _, g := range grid {
		totals[g.Row]++
	}

	spans := map[int]RowSpan{}
	for _, p := range pairs {
		row := grid[p.GridIdx].Row
		s := src[p.SrcIdx]
		cur, ok := spans[row]
		if !ok {
			cur = RowSpan{Row: row, Start: s.Start, End: s.End, Total: totals[row]}
		}
		if s.Start < cur.Start {
			cur.Start = s.Start
		}
		if s.End > cur.End {
			cur.End = s.End
		}
		cur.Matched++
		spans[row] = cur
	}

	a := Alignment{
		Markdown: markdown,
		Src:      src,
		Grid:     grid,
		Pairs:    pairs,
		Rows:     spans,
		FirstRow: -1,
		LastRow:  -1,
	}

	// The span is bounded by CONFIDENT rows only. Neighbouring content —
	// notably the user's own turns above and below — shares enough common
	// words with the message to pick up one or two chance matches, and letting
	// those set the boundary would spotlight text the agent never wrote.
	prevStart := -1
	for r := 0; r < len(rows); r++ {
		span, ok := spans[r]
		if !ok || span.Confidence() < ConfidentRow {
			continue
		}
		if a.FirstRow < 0 {
			a.FirstRow = r
		}
		a.LastRow = r
		if prevStart >= 0 && span.Start < prevStart {
			a.Inversions++
		}
		prevStart = span.Start
	}
	return a
}

// ConfidentRow is the share of a row's words that must align for the row to be
// treated as part of the message.
const ConfidentRow = 0.6

// SpanRows returns the inclusive row range to highlight. It is contiguous on
// purpose: rows carrying no message words — blank separators, and the
// box-drawing borders the TUI renders markdown tables with — sit inside the
// message and must be lit up with it, even though they resolve to no offsets.
func (a Alignment) SpanRows() (first, last int, ok bool) {
	if a.FirstRow < 0 {
		return 0, 0, false
	}
	return a.FirstRow, a.LastRow, true
}

// RowsForOffsets is the forward projection: given a markdown range an
// annotation is anchored to, which grid rows currently show it. This is the
// operation that makes the anchor survive reflow — rows are re-derived, never
// stored.
func (a Alignment) RowsForOffsets(start, end int) (first, last int, ok bool) {
	first, last = -1, -1
	for r, span := range a.Rows {
		if span.Confidence() < ConfidentRow {
			continue
		}
		if span.End <= start || span.Start >= end {
			continue
		}
		if first < 0 || r < first {
			first = r
		}
		if r > last {
			last = r
		}
	}
	return first, last, first >= 0
}

// Quote is what a selection resolves to: the verbatim markdown the user pointed
// at, or a refusal.
type Quote struct {
	Text  string
	Start int
	End   int
	OK    bool
	// Why explains a refusal.
	Why string
}

// QuoteRows is the product-critical operation: the user selected grid rows
// [startRow, endRow] and we must hand the agent an exact quote of its own
// markdown. It refuses rather than returning a wrong span — the same contract
// extractBlock already uses for command blocks.
func (a Alignment) QuoteRows(startRow, endRow int) Quote {
	start, end := -1, -1
	matched, total := 0, 0
	for r := startRow; r <= endRow; r++ {
		span, ok := a.Rows[r]
		if !ok {
			continue
		}
		matched += span.Matched
		total += span.Total
		if start < 0 || span.Start < start {
			start = span.Start
		}
		if span.End > end {
			end = span.End
		}
	}
	if start < 0 {
		return Quote{Why: "no row in the selection resolved to the message"}
	}
	// A selection where most visible words did not align means the rows hold
	// something other than the message (tool output, TUI chrome, a redraw).
	if total > 0 && float64(matched)/float64(total) < 0.6 {
		return Quote{Why: "selection aligned too weakly to quote safely"}
	}
	return Quote{Text: a.Markdown[start:end], Start: start, End: end, OK: true}
}
