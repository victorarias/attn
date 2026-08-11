package prose

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"testing"
)

// TestCalibrationReceipts prints the distributions behind every number in
// DefaultThresholds, across all three labelled sets. It is the receipt: the
// plan doc quotes what this prints, and the next person to argue with a
// threshold runs it rather than guessing.
//
// It asserts nothing. TestHealthyCorpusStaysQuiet and TestDenseCorpusTrips do
// the asserting; this one exists to be read.
func TestCalibrationReceipts(t *testing.T) {
	th := DefaultThresholds()
	for _, set := range []string{setHealthy, setSwept, setDense} {
		docs := corpus(t, set)
		m := measure(docs, th)

		t.Logf("── %s: %d documents, %d sentences, %d words", set, len(docs), len(m.lengths), m.words)
		t.Logf("   sentence length      %s", describe(m.lengths))
		t.Logf("   idea density (>=%dw) %s", th.IdeaDensityMinWords, describe(m.densities))
		t.Logf("   longest noun run     %s", describe(m.nounRuns))
		t.Logf("   paragraph length CV  %s", describe(m.blockCVs))
		t.Logf("   staccato: longest run of short sentences%s", staccatoProfile(docs))
		t.Logf("   lost thread: longest zero-overlap run = %d", longestLostThread(docs))
		t.Logf("   findings per 1000 words at DefaultThresholds: %s", perThousand(m.perRule, m.words))
	}
}

type measurements struct {
	lengths, densities, nounRuns, blockCVs []float64
	words                                  int
	perRule                                map[string]int
}

func measure(docs []*Document, th Thresholds) measurements {
	m := measurements{perRule: map[string]int{}}
	for _, doc := range docs {
		for _, sent := range doc.Sentences {
			if doc.Blocks[sent.Block].Kind == KindHeading {
				continue
			}
			words := sent.words()
			m.words += len(words)
			m.lengths = append(m.lengths, float64(len(words)))
			if len(words) >= th.IdeaDensityMinWords {
				m.densities = append(m.densities, float64(countPropositions(words))/float64(len(words)))
			}
			m.nounRuns = append(m.nounRuns, float64(longestNounRun(sent.Tokens)))
		}
		for _, block := range doc.Blocks {
			if block.Kind != KindParagraph || len(block.Sentences) < th.FlatRhythmMinSentences {
				continue
			}
			sizes := make([]float64, len(block.Sentences))
			for i, idx := range block.Sentences {
				sizes[i] = float64(doc.Sentences[idx].wordCount())
			}
			if cv, ok := coefficientOfVariation(sizes); ok {
				m.blockCVs = append(m.blockCVs, cv)
			}
		}
		for _, f := range CheckDocument(doc, Options{Thresholds: th}) {
			m.perRule[f.Rule]++
		}
	}
	return m
}

// TestHealthyCorpusStaysQuiet is the tripwire property: a limit set past where
// healthy prose goes is a limit healthy prose never feels. The numeric rules
// must be silent on every document Victor has accepted.
//
// The pattern rules are not here. They have no threshold to set past anything:
// "There is" either opens the sentence or it does not, and their rate on the
// healthy corpus is a receipt in the plan doc rather than a bound.
func TestHealthyCorpusStaysQuiet(t *testing.T) {
	numeric := []string{"long-sentence", "idea-density", "noun-string", "staccato-run", "flat-rhythm", "lost-thread"}
	for _, set := range []string{setHealthy, setSwept} {
		for _, doc := range corpus(t, set) {
			for _, f := range CheckDocument(doc, Options{Only: numeric}) {
				t.Errorf("%s corpus trips %s at %s:%d — the threshold is wrong, not the prose:\n    %s\n    %s",
					set, f.Rule, f.File, f.Line, f.Span, f.Objection)
			}
		}
	}
}

// TestDenseCorpusSeparation pins what the labelled pair actually shows, which
// is not what the plan assumed.
//
// The pattern rules separate it: prose Victor deleted opens on "There is" at
// 0.82 findings per 1000 words and the prose he replaced it with never does.
// The numeric rules separate nothing — measured, the two sides have the same
// sentence lengths and the same idea density. #818 did not remove overloaded
// sentences; it removed whole paragraphs of narration from a surface whose
// standard is one or two lines, and a per-sentence metric cannot see that.
//
// The test asserts the separation that exists and records the absence of the
// rest, so that a later change claiming the numeric rules catch #818-style
// density has to argue with a number.
func TestDenseCorpusSeparation(t *testing.T) {
	dense := rulesFiring(t, setDense)
	swept := rulesFiring(t, setSwept)

	if dense["expletive-opener"] == 0 || swept["expletive-opener"] != 0 {
		t.Errorf("expletive-opener no longer separates the pair: dense=%d swept=%d",
			dense["expletive-opener"], swept["expletive-opener"])
	}
	for _, rule := range []string{"long-sentence", "idea-density", "noun-string", "staccato-run", "flat-rhythm", "lost-thread"} {
		if dense[rule] != 0 {
			t.Logf("note: %s now fires %d times on the dense corpus; it did not when calibrated", rule, dense[rule])
		}
	}
	t.Logf("dense=%v swept=%v", dense, swept)
}

func rulesFiring(t *testing.T, set string) map[string]int {
	t.Helper()
	hit := map[string]int{}
	for _, doc := range corpus(t, set) {
		for _, f := range CheckDocument(doc, Options{}) {
			hit[f.Rule]++
		}
	}
	return hit
}

func longestNounRun(tokens []Token) int {
	best, run := 0, 0
	for _, t := range tokens {
		if isNoun(t) {
			run++
			best = max(best, run)
			continue
		}
		run = 0
	}
	return best
}

// staccatoProfile reports, for a range of "short sentence" definitions, the
// longest run of consecutive short sentences the set contains. The shipped
// pair must sit past every one the healthy sets reach.
func staccatoProfile(docs []*Document) string {
	out := ""
	for _, maxWords := range []int{6, 8, 9, 10, 12} {
		best := 0
		for _, doc := range docs {
			for _, block := range doc.Blocks {
				if block.Kind != KindParagraph {
					continue
				}
				run := 0
				for _, idx := range block.Sentences {
					if doc.Sentences[idx].wordCount() <= maxWords {
						run++
						best = max(best, run)
						continue
					}
					run = 0
				}
			}
		}
		out += fmt.Sprintf(" <=%dw:%d", maxWords, best)
	}
	return out
}

func longestLostThread(docs []*Document) int {
	best := 0
	for _, doc := range docs {
		for _, block := range doc.Blocks {
			if block.Kind != KindParagraph {
				continue
			}
			run := 0
			for i := 1; i < len(block.Sentences); i++ {
				if sharesThread(doc.Sentences[block.Sentences[i-1]], doc.Sentences[block.Sentences[i]]) {
					run = 0
					continue
				}
				run++
				best = max(best, run)
			}
		}
	}
	return best
}

func perThousand(counts map[string]int, words int) string {
	if words == 0 {
		return "(no words)"
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for _, name := range names {
		out += fmt.Sprintf(" %s:%.2f", name, 1000*float64(counts[name])/float64(words))
	}
	if out == "" {
		return " (none)"
	}
	return out
}

func describe(xs []float64) string {
	if len(xs) == 0 {
		return "(no samples)"
	}
	sorted := slices.Clone(xs)
	sort.Float64s(sorted)
	return fmt.Sprintf("n=%d min=%.2f p50=%.2f p90=%.2f p99=%.2f max=%.2f",
		len(sorted), sorted[0], quantile(sorted, 0.50), quantile(sorted, 0.90),
		quantile(sorted, 0.99), sorted[len(sorted)-1])
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(idx, len(sorted)-1))]
}
