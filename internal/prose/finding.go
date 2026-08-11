package prose

import (
	"cmp"
	"slices"
)

// Layer names which half of the gate produced a finding. The judge (slice 2)
// fills in LayerJudge; nothing else about the shape changes when it arrives.
type Layer string

const (
	LayerDeterministic Layer = "deterministic"
	LayerJudge         Layer = "judge"
)

// Finding is the whole output contract: a span of prose, the rule that
// objected, and what the writer should do about it.
type Finding struct {
	Rule  string `json:"rule"`
	Layer Layer  `json:"layer"`

	// File is the path as the caller named it, or "-" for stdin.
	File string `json:"file"`
	// Line and Column are 1-indexed; Column counts bytes into the line.
	Line   int `json:"line"`
	Column int `json:"column"`
	// Start and End are byte offsets into the source, so a caller that has the
	// bytes can highlight the span without re-deriving it from Line/Column.
	Start int `json:"start"`
	End   int `json:"end"`

	// Span is the offending text, verbatim from the source.
	Span string `json:"span"`
	// Objection says what is wrong, in words a writer can act on.
	Objection string `json:"objection"`
	// Suggestion is a concrete rewrite when the rule can name one.
	Suggestion string `json:"suggestion,omitempty"`
}

// sortFindings orders findings the way a reader walks a file: down the page,
// then left to right, then by rule so a tie is stable across runs.
func sortFindings(findings []Finding) {
	slices.SortStableFunc(findings, func(a, b Finding) int {
		if c := cmp.Compare(a.Start, b.Start); c != 0 {
			return c
		}
		if c := cmp.Compare(a.End, b.End); c != 0 {
			return c
		}
		return cmp.Compare(a.Rule, b.Rule)
	})
}
