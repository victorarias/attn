package prosegate

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

var (
	boldRe   = regexp.MustCompile(`\*\*[^*]+\*\*`)
	headRe   = regexp.MustCompile(`(?m)^\s*#{1,6}\s`)
	bulletRe = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+\.)\s`)
	paraRe   = regexp.MustCompile(`\n\s*\n`)
)

// Is anything about the shape of a message predictive, if the style features
// are not?
func TestScratchShape(t *testing.T) {
	recs := loadSilent(t)
	pos, neg := map[string][]float64{}, map[string][]float64{}
	for _, r := range recs {
		vals, w := Measure(r.Text)
		if w < MinWords {
			continue
		}
		s := StructureOf(r.Text)
		f := map[string]float64{
			"raw_words":     float64(w),
			"raw_chars":     float64(len(r.Text)),
			"headings":      float64(s.Headings),
			"list_items":    float64(s.ListItems),
			"fenced":        float64(s.FencedBlocks),
			"tables":        float64(s.Tables),
			"paragraphs":    float64(len(paraRe.FindAllString(r.Text, -1))),
			"bold_runs":     float64(len(boldRe.FindAllString(r.Text, -1))),
			"bold_per100w":  float64(len(boldRe.FindAllString(r.Text, -1))) / float64(w) * 100,
			"heads_per100w": float64(headRe.NumSubexp()+len(headRe.FindAllString(r.Text, -1))) / float64(w) * 100,
			"bullets_frac":  float64(len(bulletRe.FindAllString(r.Text, -1))) / float64(strings.Count(r.Text, "\n")+1),
			"long_words_8":  vals["long_words_8"],
		}
		for k, v := range f {
			if r.Label == "complaint" {
				pos[k] = append(pos[k], v)
			} else {
				neg[k] = append(neg[k], v)
			}
		}
	}
	keys := []string{"raw_words", "raw_chars", "paragraphs", "headings", "list_items", "fenced", "tables", "bold_runs", "bold_per100w", "heads_per100w", "bullets_frac", "long_words_8"}
	fmt.Printf("%-16s %8s %10s %10s\n", "feature", "AUC", "mean cmpl", "mean silent")
	mean := func(xs []float64) float64 {
		s := 0.0
		for _, x := range xs {
			s += x
		}
		return s / float64(len(xs))
	}
	for _, k := range keys {
		fmt.Printf("%-16s %8.3f %10.2f %10.2f\n", k, auc(pos[k], neg[k]), mean(pos[k]), mean(neg[k]))
	}
	fmt.Printf("\nn complaint=%d  n silent=%d\n", len(pos["raw_words"]), len(neg["raw_words"]))
}
