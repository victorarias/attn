package prosegate

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// The calibration corpus is real conversation text and this repository is
// public, so it lives outside the tree. Point ATTN_PROSE_CORPUS at the JSONL to
// run this; without it the test skips, and CI stays green without the data.
const corpusEnv = "ATTN_PROSE_CORPUS"

type corpusCase struct {
	Index    int    `json:"index"`
	Dense    string `json:"dense"`    // the message Victor objected to
	Revision string `json:"revision"` // the revision he accepted
}

// The thresholds only mean something if they still separate the two piles. This
// is the test that fails when someone tunes a number to silence a false
// positive: the gate must keep catching the prose Victor complained about
// without nagging the prose he accepted.
func TestSeparatesTheCorpus(t *testing.T) {
	path := os.Getenv(corpusEnv)
	if path == "" {
		t.Skipf("set %s to the calibration corpus to run this", corpusEnv)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	var dense, accepted, denseFired, acceptedFired int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var c corpusCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("corpus line: %v", err)
		}
		if v := Check(c.Dense, Default()); !v.Abstained {
			dense++
			if v.Tripped {
				denseFired++
			}
		}
		if v := Check(c.Revision, Default()); !v.Abstained {
			accepted++
			if v.Tripped {
				acceptedFired++
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if dense == 0 || accepted == 0 {
		t.Fatalf("corpus produced no judgeable documents (dense=%d accepted=%d)", dense, accepted)
	}

	recall := float64(denseFired) / float64(dense) * 100
	falsePositive := float64(acceptedFired) / float64(accepted) * 100
	t.Logf("recall %.0f%% (%d/%d dense), false positive %.0f%% (%d/%d accepted)",
		recall, denseFired, dense, falsePositive, acceptedFired, accepted)

	// Bounds, not exact figures: the corpus grows as Victor complains again.
	// In-sample recall reads a few points below the honest leave-one-out
	// estimate (80% recall, 20% false positive from TestRecalibrate), because
	// every accepted document here helped set the bar it is being judged
	// against. Trust the LOO figures; these bounds only catch regressions.
	if recall < 70 {
		t.Errorf("recall fell to %.0f%%; the gate stopped catching prose Victor rejected", recall)
	}
	if falsePositive > 30 {
		t.Errorf("false positive rose to %.0f%%; the gate is nagging prose Victor accepted", falsePositive)
	}
}
