package main

import (
	"fmt"
	"sort"
	"strings"
)

// tripwires reports, for each candidate gate, how much dense prose it catches
// when the threshold is set past every accepted document. Leave-one-out: each
// document is scored against a threshold calibrated on the other 16, so a
// document never sets the bar it is judged by.
type pair struct{ dense, rev map[string]float64 }

func tripwires(pairs []pair) {
	gates := []string{"long_words_8", "long_words_11", "word_len_mean", "preposition",
		"sent_mean_words", "long_sents_per100", "commas_per_sent", "parenthetical", "em_dash"}

	fmt.Printf("\n%-20s %10s %12s %12s\n", "gate (leave-one-out)", "threshold", "dense fires", "accepted fires")
	fmt.Println(strings.Repeat("-", 58))

	// A margin above the accepted maximum keeps the bar a tripwire rather than a
	// line the best-behaved accepted document happens to sit on.
	const margin = 1.02

	fired := make([]bool, len(pairs))
	firedGood := make([]bool, len(pairs))
	for _, g := range gates {
		denseHits, goodHits := 0, 0
		var thresholds []float64
		for i := range pairs {
			max := 0.0
			for j := range pairs {
				if i == j {
					continue
				}
				if v := pairs[j].rev[g]; v > max {
					max = v
				}
			}
			th := max * margin
			thresholds = append(thresholds, th)
			if pairs[i].dense[g] > th {
				denseHits++
				fired[i] = true
			}
			if pairs[i].rev[g] > th {
				firedGood[i] = true
				goodHits++
			}
		}
		sort.Float64s(thresholds)
		fmt.Printf("%-20s %10.2f %11d%% %11d%%\n", g, thresholds[len(thresholds)/2],
			denseHits*100/len(pairs), goodHits*100/len(pairs))
	}

	any := 0
	for _, f := range fired {
		if f {
			any++
		}
	}
	anyGood := 0
	for _, f := range firedGood {
		if f {
			anyGood++
		}
	}
	fmt.Printf("\nUNION: fires on %d/%d dense (%d%%) and %d/%d accepted (%d%%)\n", any, len(pairs), any*100/len(pairs), anyGood, len(pairs), anyGood*100/len(pairs))
}
