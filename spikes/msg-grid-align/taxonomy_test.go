package msgalign

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestFailureTaxonomy prints every row that did not round-trip exactly, so the
// verdict can name the failure modes instead of just reporting a percentage.
func TestFailureTaxonomy(t *testing.T) {
	for _, fx := range loadFixtures(t) {
		a := Align(fx.Markdown, fx.GridRows)
		if a.FirstRow < 0 {
			fmt.Printf("\n### %s — NO ALIGNMENT (message not on screen)\n", fx.Name)
			fmt.Printf("    markdown was %d chars / %d tokens\n", len(fx.Markdown), len(a.Src))
			continue
		}

		var rows []int
		for r := range a.Rows {
			rows = append(rows, r)
		}
		sort.Ints(rows)

		type problem struct {
			row  int
			kind string
			grid string
			got  string
		}
		var problems []problem
		for _, r := range rows {
			q := a.QuoteRows(r, r)
			gridTok := rowTokens(fx.GridRows[r])
			if !q.OK {
				problems = append(problems, problem{r, "REFUSED: " + q.Why, strings.Join(gridTok, " "), ""})
				continue
			}
			gotTok := quoteTokens(q.Text)
			if equalTokens(gridTok, gotTok) {
				continue
			}
			kind := "MISMATCH"
			switch {
			case len(gotTok) > len(gridTok):
				kind = fmt.Sprintf("QUOTE TOO WIDE (+%d tokens)", len(gotTok)-len(gridTok))
			case len(gotTok) < len(gridTok):
				kind = fmt.Sprintf("QUOTE TOO NARROW (-%d tokens)", len(gridTok)-len(gotTok))
			}
			problems = append(problems, problem{r, kind, strings.Join(gridTok, " "), strings.Join(gotTok, " ")})
		}

		// Rows inside the message span that resolved to nothing at all.
		var unresolved []int
		for r := a.FirstRow; r <= a.LastRow; r++ {
			if _, ok := a.Rows[r]; ok {
				continue
			}
			if strings.TrimSpace(fx.GridRows[r]) == "" {
				continue // blank separator row, expected
			}
			unresolved = append(unresolved, r)
		}

		if len(problems) == 0 && len(unresolved) == 0 {
			fmt.Printf("\n### %s — clean (%d rows, span %d-%d)\n", fx.Name, len(rows), a.FirstRow, a.LastRow)
			continue
		}

		fmt.Printf("\n### %s — %d problem rows, %d unresolved non-blank rows in span %d-%d\n",
			fx.Name, len(problems), len(unresolved), a.FirstRow, a.LastRow)
		for _, p := range problems {
			fmt.Printf("  row %3d  %s\n", p.row, p.kind)
			fmt.Printf("           grid : %s\n", trunc(p.grid, 110))
			if p.got != "" {
				fmt.Printf("           quote: %s\n", trunc(p.got, 110))
			}
		}
		for _, r := range unresolved {
			fmt.Printf("  row %3d  UNRESOLVED (in span, non-blank)\n", r)
			fmt.Printf("           grid : %s\n", trunc(strings.TrimSpace(fx.GridRows[r]), 110))
		}
	}
	fmt.Println()
}
