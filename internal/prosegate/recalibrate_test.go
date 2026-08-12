package prosegate

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// Regenerates the thresholds in DefaultThresholds from the corpus, using this
// package's own Measure so the numbers match the code that ships. Run it after
// changing proseOnly or adding a gate, then paste the printed values:
//
//	ATTN_PROSE_CORPUS=... go test ./internal/prosegate -run Recalibrate -v
//
// Leave-one-out: each threshold sits just above the highest value any *other*
// accepted document reached, so no document sets the bar it is judged by. The
// printed figure is the median across the folds.
func TestRecalibrate(t *testing.T) {
	path := os.Getenv(corpusEnv)
	if path == "" {
		t.Skipf("set %s to the calibration corpus to run this", corpusEnv)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	type sample struct{ dense, accepted map[string]float64 }
	var samples []sample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var c corpusCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("corpus line: %v", err)
		}
		d, dw := Measure(c.Dense)
		a, aw := Measure(c.Revision)
		if dw < MinWords || aw < MinWords {
			continue
		}
		samples = append(samples, sample{d, a})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if len(samples) < 5 {
		t.Fatalf("only %d judgeable pairs; too few to calibrate", len(samples))
	}

	const margin = 1.02
	names := make([]string, 0, len(DefaultThresholds()))
	for n := range DefaultThresholds() {
		names = append(names, n)
	}
	sort.Strings(names)

	t.Logf("pairs=%d", len(samples))
	fired := make([]bool, len(samples))
	firedGood := make([]bool, len(samples))
	for _, name := range names {
		var folds []float64
		denseHits, goodHits := 0, 0
		for i := range samples {
			max := 0.0
			for j := range samples {
				if i != j && samples[j].accepted[name] > max {
					max = samples[j].accepted[name]
				}
			}
			th := max * margin
			folds = append(folds, th)
			if samples[i].dense[name] > th {
				denseHits++
				fired[i] = true
			}
			if samples[i].accepted[name] > th {
				goodHits++
				firedGood[i] = true
			}
		}
		sort.Float64s(folds)
		t.Logf("%-20s %8.2f   dense %2d/%d   accepted %2d/%d",
			name, folds[len(folds)/2], denseHits, len(samples), goodHits, len(samples))
	}

	count := func(b []bool) int {
		n := 0
		for _, v := range b {
			if v {
				n++
			}
		}
		return n
	}
	t.Logf("UNION: dense %d/%d, accepted %d/%d",
		count(fired), len(samples), count(firedGood), len(samples))
}
