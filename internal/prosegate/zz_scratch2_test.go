package prosegate

import (
	"fmt"
	"sort"
	"testing"
)

// AUC via rank statistic: P(random complaint scores higher than random silent).
// 0.5 is a coin flip.
func auc(pos, neg []float64) float64 {
	type pt struct {
		v   float64
		pos bool
	}
	all := make([]pt, 0, len(pos)+len(neg))
	for _, v := range pos {
		all = append(all, pt{v, true})
	}
	for _, v := range neg {
		all = append(all, pt{v, false})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })
	var rankSum float64
	for i, p := range all {
		if p.pos {
			rankSum += float64(i + 1)
		}
	}
	n1, n2 := float64(len(pos)), float64(len(neg))
	return (rankSum - n1*(n1+1)/2) / (n1 * n2)
}

func TestScratchSeparation(t *testing.T) {
	recs := loadSilent(t)
	pos := map[string][]float64{}
	neg := map[string][]float64{}
	for _, r := range recs {
		vals, w := Measure(r.Text)
		if w < MinWords {
			continue
		}
		for k, v := range vals {
			if r.Label == "complaint" {
				pos[k] = append(pos[k], v)
			} else {
				neg[k] = append(neg[k], v)
			}
		}
	}
	var names []string
	for k := range DefaultThresholds() {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Printf("%-20s %8s %10s %10s\n", "feature", "AUC", "mean cmpl", "mean silent")
	for _, n := range names {
		mean := func(xs []float64) float64 {
			s := 0.0
			for _, x := range xs {
				s += x
			}
			return s / float64(len(xs))
		}
		fmt.Printf("%-20s %8.3f %10.2f %10.2f\n", n, auc(pos[n], neg[n]), mean(pos[n]), mean(neg[n]))
	}

	// What would a p99-of-silent calibration buy?
	fmt.Println("\nthresholds at percentiles of silently-accepted prose:")
	for _, p := range []float64{0.90, 0.95, 0.99} {
		th := map[string]float64{}
		for _, n := range names {
			xs := append([]float64(nil), neg[n]...)
			sort.Float64s(xs)
			th[n] = xs[min(len(xs)-1, int(p*float64(len(xs))))]
		}
		cfg := Config{Nudge: "x", Thresholds: th, MinWords: MinWords}
		var sf, st, cf, ct int
		for _, r := range recs {
			v := Check(r.Text, cfg)
			if v.Abstained {
				continue
			}
			if r.Label == "complaint" {
				ct++
				if v.Tripped {
					cf++
				}
			} else {
				st++
				if v.Tripped {
					sf++
				}
			}
		}
		fmt.Printf("  p%-4.0f  fires on %5.1f%% of silent, %5.1f%% of complaints\n",
			p*100, 100*float64(sf)/float64(st), 100*float64(cf)/float64(ct))
	}
}
