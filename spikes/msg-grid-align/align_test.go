package msgalign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "msgalign-spike")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	config.ScopeTestEnvironment(dir)
	os.Exit(m.Run())
}

type fixture struct {
	Name         string   `json:"name"`
	Agent        string   `json:"agent"`
	Cols         int      `json:"cols"`
	Rows         int      `json:"rows"`
	ANSIB64      string   `json:"ansi_b64"`
	GridRows     []string `json:"grid_rows"`
	ViewportRows []string `json:"viewport_rows"`
	Markdown     string   `json:"markdown"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	paths, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var out []fixture
	for _, p := range paths {
		blob, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var fx fixture
		if err := json.Unmarshal(blob, &fx); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		out = append(out, fx)
	}
	if len(out) == 0 {
		t.Skip("no fixtures; run: go run ./spikes/msg-grid-align/capture -list")
	}
	return out
}

// rowTokens is the normalized word sequence a row actually shows.
func rowTokens(row string) []string {
	var out []string
	for _, g := range TokenizeGrid([]string{row}) {
		out = append(out, g.Norm)
	}
	return out
}

func quoteTokens(quote string) []string {
	var out []string
	for _, s := range TokenizeMarkdown(quote) {
		out = append(out, s.Norm)
	}
	return out
}

func equalTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestReportAlignment is the spike's measurement. It prints the table that the
// verdict in NOTES.md is written from.
func TestReportAlignment(t *testing.T) {
	fixtures := loadFixtures(t)

	fmt.Println()
	fmt.Println("=== forward alignment (markdown -> grid rows) ===")
	fmt.Printf("%-24s %-7s %5s %6s %6s %7s %8s %6s %5s %9s\n",
		"FIXTURE", "AGENT", "COLS", "SRCTOK", "GRIDTK", "MATCHED", "ROWSPAN", "ROWS", "INV", "RECALL")

	for _, fx := range fixtures {
		a := Align(fx.Markdown, fx.GridRows)

		// Recall is measured over the rows the message was attributed to, not
		// the whole screen: the grid legitimately holds other content.
		matchedInSpan, totalInSpan := 0, 0
		for r, span := range a.Rows {
			if r < a.FirstRow || r > a.LastRow {
				continue
			}
			matchedInSpan += span.Matched
			totalInSpan += span.Total
		}
		recall := 0.0
		if totalInSpan > 0 {
			recall = float64(matchedInSpan) / float64(totalInSpan)
		}

		span := "-"
		if a.FirstRow >= 0 {
			span = fmt.Sprintf("%d-%d", a.FirstRow, a.LastRow)
		}
		fmt.Printf("%-24s %-7s %5d %6d %6d %7d %8s %6d %5d %8.1f%%\n",
			fx.Name, fx.Agent, fx.Cols, len(a.Src), len(a.Grid), len(a.Pairs),
			span, len(a.Rows), a.Inversions, recall*100)
	}

	fmt.Println()
	fmt.Println("=== reverse mapping (grid row -> verbatim markdown quote) ===")
	fmt.Printf("%-24s %6s %6s %7s %8s  %s\n", "FIXTURE", "ROWS", "EXACT", "REFUSED", "EXACT%", "WORST NON-EXACT ROW")

	for _, fx := range fixtures {
		a := Align(fx.Markdown, fx.GridRows)

		var rowsSorted []int
		for r := range a.Rows {
			rowsSorted = append(rowsSorted, r)
		}
		sort.Ints(rowsSorted)

		exact, refused := 0, 0
		worst := ""
		for _, r := range rowsSorted {
			q := a.QuoteRows(r, r)
			if !q.OK {
				refused++
				continue
			}
			if equalTokens(rowTokens(fx.GridRows[r]), quoteTokens(q.Text)) {
				exact++
				continue
			}
			if worst == "" {
				worst = fmt.Sprintf("row %d: grid=%q quote=%q",
					r, trunc(strings.Join(rowTokens(fx.GridRows[r]), " "), 40),
					trunc(strings.Join(quoteTokens(q.Text), " "), 40))
			}
		}
		pct := 0.0
		if len(rowsSorted) > 0 {
			pct = float64(exact) / float64(len(rowsSorted)) * 100
		}
		fmt.Printf("%-24s %6d %6d %7d %7.1f%%  %s\n", fx.Name, len(rowsSorted), exact, refused, pct, worst)
	}

	fmt.Println()
	fmt.Println("=== multi-row selections (what a real annotation spans) ===")
	fmt.Printf("%-24s %6s %6s %7s %8s\n", "FIXTURE", "TRIED", "EXACT", "REFUSED", "EXACT%")
	for _, fx := range fixtures {
		a := Align(fx.Markdown, fx.GridRows)
		if a.FirstRow < 0 {
			fmt.Printf("%-24s %6d %6d %7d %7s\n", fx.Name, 0, 0, 0, "-")
			continue
		}
		tried, exact, refused := 0, 0, 0
		for _, width := range []int{2, 3, 5} {
			for start := a.FirstRow; start+width-1 <= a.LastRow; start++ {
				end := start + width - 1
				tried++
				q := a.QuoteRows(start, end)
				if !q.OK {
					refused++
					continue
				}
				var want []string
				for r := start; r <= end; r++ {
					if _, ok := a.Rows[r]; !ok {
						continue
					}
					want = append(want, rowTokens(fx.GridRows[r])...)
				}
				if equalTokens(want, quoteTokens(q.Text)) {
					exact++
				}
			}
		}
		pct := 0.0
		if tried > 0 {
			pct = float64(exact) / float64(tried) * 100
		}
		fmt.Printf("%-24s %6d %6d %7d %7.1f%%\n", fx.Name, tried, exact, refused, pct)
	}
	fmt.Println()
}

// TestAlignmentIsMonotonic guards the property that actually protects the user:
// a resolved row order that jumps backwards means we are quoting the wrong text.
func TestAlignmentIsMonotonic(t *testing.T) {
	for _, fx := range loadFixtures(t) {
		a := Align(fx.Markdown, fx.GridRows)
		if a.FirstRow < 0 {
			continue
		}
		if a.Inversions > 0 {
			t.Errorf("%s: %d row-order inversions in the resolved span %d-%d",
				fx.Name, a.Inversions, a.FirstRow, a.LastRow)
		}
	}
}

// TestQuoteNeverExceedsMessage checks the quote stays inside the markdown.
func TestQuoteNeverExceedsMessage(t *testing.T) {
	for _, fx := range loadFixtures(t) {
		a := Align(fx.Markdown, fx.GridRows)
		for r := range a.Rows {
			q := a.QuoteRows(r, r)
			if !q.OK {
				continue
			}
			if q.Start < 0 || q.End > len(fx.Markdown) || q.Start > q.End {
				t.Errorf("%s row %d: quote offsets [%d,%d) out of bounds for %d-char markdown",
					fx.Name, r, q.Start, q.End, len(fx.Markdown))
			}
		}
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
