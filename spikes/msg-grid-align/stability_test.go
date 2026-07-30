package msgalign

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// renderAt replays a fixture's captured ANSI through the same VT core the worker
// uses, optionally resizing afterwards. Resizing after the write is exactly what
// production does when the user drags the window: ghostty reflows the cells that
// are already there.
func renderAt(t *testing.T, ansiB64 string, cols, rows, resizeCols, resizeRows int) []string {
	t.Helper()
	ansi, err := base64.StdEncoding.DecodeString(ansiB64)
	if err != nil {
		t.Fatalf("decode ansi: %v", err)
	}
	term, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}
	defer term.Close()
	term.Write(ansi)
	if resizeCols != cols || resizeRows != rows {
		term.Resize(resizeCols, resizeRows)
	}
	text := strings.TrimRight(term.PlainText(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// outcome classifies what happened to one annotation after a perturbation.
type outcome struct{ survived, refused, wrong int }

func (o outcome) String() string {
	return fmt.Sprintf("survived=%-4d safe-refusal=%-4d WRONG=%d", o.survived, o.refused, o.wrong)
}

// checkPerturbation re-resolves every baseline annotation against a perturbed
// grid. An annotation is anchored to markdown offsets, so the test is: do those
// offsets still project onto rows that quote back the same text?
func checkPerturbation(md string, baseline Alignment, perturbed []string) outcome {
	after := Align(md, perturbed)

	var rows []int
	for r, span := range baseline.Rows {
		if span.Confidence() >= ConfidentRow {
			rows = append(rows, r)
		}
	}
	sort.Ints(rows)

	var o outcome
	for _, r := range rows {
		want := baseline.QuoteRows(r, r)
		if !want.OK {
			continue
		}
		first, last, ok := after.RowsForOffsets(want.Start, want.End)
		if !ok {
			o.refused++
			continue
		}
		got := after.QuoteRows(first, last)
		if !got.OK {
			o.refused++
			continue
		}
		// The perturbed rows may cover a little more or less than the original
		// row did (a reflow moves words between rows), so the contract is
		// containment of the anchored text, not row-for-row equality.
		if strings.Contains(normalizeWhitespace(got.Text), normalizeWhitespace(want.Text)) ||
			strings.Contains(normalizeWhitespace(want.Text), normalizeWhitespace(got.Text)) {
			o.survived++
			continue
		}
		o.wrong++
	}
	return o
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// countDiffRows reports how much a perturbation actually moved the grid, so a
// PASS on an unchanged grid cannot be mistaken for evidence.
func countDiffRows(before, after []string) int {
	n := len(before)
	if len(after) > n {
		n = len(after)
	}
	diff := 0
	for i := 0; i < n; i++ {
		var a, b string
		if i < len(before) {
			a = before[i]
		}
		if i < len(after) {
			b = after[i]
		}
		if a != b {
			diff++
		}
	}
	return diff
}

// TestStability is the second half of the spike: an anchor is only useful if it
// keeps pointing at the same words after the terminal changes shape.
func TestStability(t *testing.T) {
	fixtures := loadFixtures(t)

	fmt.Println()
	fmt.Println("=== stability: does an anchored annotation still resolve to the same text? ===")
	fmt.Println("(ROWSΔ is how many grid rows the perturbation actually changed — a run with")
	fmt.Println(" ROWSΔ=0 proves nothing, so it is reported alongside the outcome.)")
	fmt.Printf("%-24s %-22s %6s  %s\n", "FIXTURE", "PERTURBATION", "ROWSΔ", "OUTCOME")

	totalWrong := 0
	for _, fx := range fixtures {
		baseline := Align(fx.Markdown, fx.GridRows)
		if baseline.FirstRow < 0 {
			fmt.Printf("%-24s %-22s (no baseline alignment)\n", fx.Name, "-")
			continue
		}

		perturbations := []struct {
			name       string
			cols, rows int
		}{
			{"height -12", fx.Cols, fx.Rows - 12},
			{"height +12", fx.Cols, fx.Rows + 12},
			{"width -20", fx.Cols - 20, fx.Rows},
			{"width -60", fx.Cols - 60, fx.Rows},
			{"width +20", fx.Cols + 20, fx.Rows},
		}

		for _, p := range perturbations {
			if p.cols < 40 || p.rows < 8 {
				continue
			}
			grid := renderAt(t, fx.ANSIB64, fx.Cols, fx.Rows, p.cols, p.rows)
			o := checkPerturbation(fx.Markdown, baseline, grid)
			totalWrong += o.wrong
			fmt.Printf("%-24s %-22s %6d  %s\n", fx.Name, p.name, countDiffRows(fx.GridRows, grid), o)
		}

		// Scroll is structurally moot: alignment runs over the whole buffer, so
		// row indices are re-derived rather than stored. Shifting every row
		// proves no row index is baked into the anchor.
		shifted := append([]string{"", "$ unrelated command", ""}, fx.GridRows...)
		o := checkPerturbation(fx.Markdown, baseline, shifted)
		totalWrong += o.wrong
		fmt.Printf("%-24s %-22s %6d  %s\n", fx.Name, "rows shifted by 3",
			countDiffRows(fx.GridRows, shifted), o)
	}
	fmt.Println()

	// The only unacceptable outcome is quoting the wrong text at the agent.
	if totalWrong > 0 {
		t.Errorf("%d annotations resolved to the WRONG text after a perturbation", totalWrong)
	}
}
