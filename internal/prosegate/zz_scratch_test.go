package prosegate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
)

type silentRec struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

func loadSilent(t *testing.T) []silentRec {
	f, err := os.Open(os.Getenv("ATTN_SILENT_CORPUS"))
	if err != nil {
		t.Skip("no silent corpus")
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var out []silentRec
	for sc.Scan() {
		var r silentRec
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func TestScratchSilent(t *testing.T) {
	recs := loadSilent(t)
	var silentJudged, silentFired, compJudged, compFired int
	gateHits := map[string]int{}
	percentile := map[string][]float64{}

	for _, r := range recs {
		v := Check(r.Text, Default())
		if v.Abstained {
			continue
		}
		if r.Label == "silent" {
			silentJudged++
			if v.Tripped {
				silentFired++
				for _, g := range v.Gates {
					gateHits[g.Name]++
				}
			}
			vals, _ := Measure(r.Text)
			for k, x := range vals {
				percentile[k] = append(percentile[k], x)
			}
		} else {
			compJudged++
			if v.Tripped {
				compFired++
			}
		}
	}
	fmt.Printf("silent   judged %4d  fired %4d (%.1f%%)\n", silentJudged, silentFired, 100*float64(silentFired)/float64(silentJudged))
	fmt.Printf("complaint judged %4d  fired %4d (%.1f%%)\n", compJudged, compFired, 100*float64(compFired)/float64(compJudged))
	fmt.Println("\nwhich gate fires on silently-accepted prose:")
	type kv struct {
		k string
		n int
	}
	var hits []kv
	for k, n := range gateHits {
		hits = append(hits, kv{k, n})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].n > hits[j].n })
	for _, h := range hits {
		fmt.Printf("  %-20s %4d  (%.1f%% of judged)\n", h.k, h.n, 100*float64(h.n)/float64(silentJudged))
	}

	fmt.Println("\ncurrent threshold vs percentiles of silently-accepted prose:")
	var names []string
	for k := range DefaultThresholds() {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Printf("%-20s %8s %8s %8s %8s %8s %8s\n", "feature", "current", "p50", "p90", "p95", "p99", "p99.5")
	for _, name := range names {
		xs := percentile[name]
		sort.Float64s(xs)
		at := func(p float64) float64 { return xs[min(len(xs)-1, int(p*float64(len(xs))))] }
		fmt.Printf("%-20s %8.2f %8.2f %8.2f %8.2f %8.2f %8.2f\n",
			name, DefaultThresholds()[name], at(0.50), at(0.90), at(0.95), at(0.99), at(0.995))
	}
}
