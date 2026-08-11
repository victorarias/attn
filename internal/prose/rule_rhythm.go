package prose

import (
	"fmt"
	"math"
	"strings"
)

// staccatoRule reports a run of short sentences.
//
// This is half the anti-gaming pair for long-sentence. An agent told only "too
// long" chops, and chopping produces prose that passes every length check and
// reads like a telegram. The rule fires on the run rather than the sentence: a
// short sentence is a good sentence, and five in a row is a defect.
type staccatoRule struct{}

func (staccatoRule) name() string { return "staccato-run" }

func (staccatoRule) check(doc *Document, th Thresholds) []Finding {
	var out []Finding
	for _, block := range doc.Blocks {
		if block.Kind != KindParagraph {
			continue
		}
		for i := 0; i < len(block.Sentences); {
			j := i
			for j < len(block.Sentences) && doc.Sentences[block.Sentences[j]].wordCount() <= th.StaccatoMaxWords {
				j++
			}
			if run := j - i; run >= th.StaccatoRunLength {
				first := doc.Sentences[block.Sentences[i]]
				last := doc.Sentences[block.Sentences[j-1]]
				out = append(out, doc.finding(
					"staccato-run", first.Start, last.End,
					fmt.Sprintf("%d sentences in a row of %d words or fewer — this is chopped, not simple; join the ones that share a subject", run, th.StaccatoMaxWords),
					"",
				))
			}
			i = max(j, i+1)
		}
	}
	return out
}

// flatRhythmRule reports a passage whose sentences are all the same size.
//
// The other half of the anti-gaming pair. Prose rewritten against a length
// target converges on the target: every sentence lands just under it, and the
// variation that tells a reader where the emphasis is disappears. The measure
// is the coefficient of variation of sentence length, which is scale-free —
// a paragraph of uniformly long sentences trips it exactly as a paragraph of
// uniformly short ones does.
type flatRhythmRule struct{}

func (flatRhythmRule) name() string { return "flat-rhythm" }

func (flatRhythmRule) check(doc *Document, th Thresholds) []Finding {
	var out []Finding
	for _, block := range doc.Blocks {
		if block.Kind != KindParagraph || len(block.Sentences) < th.FlatRhythmMinSentences {
			continue
		}
		lengths := make([]float64, len(block.Sentences))
		for i, idx := range block.Sentences {
			lengths[i] = float64(doc.Sentences[idx].wordCount())
		}
		cv, ok := coefficientOfVariation(lengths)
		if !ok || cv >= th.FlatRhythmMinCV {
			continue
		}
		first := doc.Sentences[block.Sentences[0]]
		last := doc.Sentences[block.Sentences[len(block.Sentences)-1]]
		out = append(out, doc.finding(
			"flat-rhythm", first.Start, last.End,
			fmt.Sprintf("%d sentences whose lengths vary by %.0f%% (floor %.0f%%) — every sentence is the same size, so nothing in the paragraph reads as more important than anything else",
				len(lengths), cv*100, th.FlatRhythmMinCV*100),
			"",
		))
	}
	return out
}

// coefficientOfVariation is the standard deviation over the mean. It reports
// false for a mean of zero, which has no meaningful spread.
func coefficientOfVariation(xs []float64) (float64, bool) {
	if len(xs) < 2 {
		return 0, false
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	if mean == 0 {
		return 0, false
	}
	variance := 0.0
	for _, x := range xs {
		variance += (x - mean) * (x - mean)
	}
	variance /= float64(len(xs) - 1)
	return math.Sqrt(variance) / mean, true
}

// lostThreadRule reports adjacent sentences that share nothing.
//
// Cohesion is what chopping destroys. A writer splitting one thought into
// three keeps the words but drops the connectives and the repeated subject
// that told the reader the three were about the same thing. This rule fires on
// a run of sentence pairs with no shared referent and no connective — which is
// exactly the shape prose takes when the length rule is being gamed.
type lostThreadRule struct{}

func (lostThreadRule) name() string { return "lost-thread" }

// connectives tie a sentence to the one before it. A sentence opening on one
// is carrying the thread explicitly, whatever its vocabulary.
var connectives = map[string]bool{
	"but": true, "so": true, "then": true, "however": true, "yet": true,
	"still": true, "instead": true, "therefore": true, "thus": true,
	"meanwhile": true, "otherwise": true, "besides": true, "also": true,
	"and": true, "or": true, "nor": true, "because": true, "since": true,
	"that": true, "this": true, "these": true, "those": true, "it": true,
	"they": true, "he": true, "she": true, "we": true, "its": true,
	"their": true, "which": true, "here": true, "there": true, "either": true,
}

func (lostThreadRule) check(doc *Document, th Thresholds) []Finding {
	var out []Finding
	for _, block := range doc.Blocks {
		if block.Kind != KindParagraph || len(block.Sentences) <= th.LostThreadRun {
			continue
		}
		run := 0
		for i := 1; i < len(block.Sentences); i++ {
			prev := doc.Sentences[block.Sentences[i-1]]
			curr := doc.Sentences[block.Sentences[i]]
			if sharesThread(prev, curr) {
				run = 0
				continue
			}
			run++
			if run < th.LostThreadRun {
				continue
			}
			first := doc.Sentences[block.Sentences[i-run]]
			out = append(out, doc.finding(
				"lost-thread", first.Start, curr.End,
				fmt.Sprintf("%d sentences in a row share no subject with the one before and open on no connective — the paragraph has lost its through-line", run),
				"",
			))
			run = 0
		}
	}
	return out
}

// sharesThread reports whether curr picks up something from prev: a repeated
// content word, or an opening connective that does the same job.
func sharesThread(prev, curr Sentence) bool {
	words := curr.words()
	if len(words) == 0 {
		return true
	}
	if connectives[lower(words[0])] {
		return true
	}
	seen := map[string]bool{}
	for _, t := range prev.words() {
		if isContent(t) {
			seen[stem(lower(t))] = true
		}
	}
	for _, t := range words {
		if isContent(t) && seen[stem(lower(t))] {
			return true
		}
	}
	return false
}

// isContent selects the words that carry a referent: what the sentence is
// about and what it says happened.
func isContent(t Token) bool { return isNoun(t) || (isVerb(t) && !isAuxiliaryForm(t)) }

// stem folds the inflections that would hide a repeated referent — "session"
// against "sessions", "verify" against "verified". It is deliberately crude:
// the comparison only has to agree with itself.
func stem(word string) string {
	if base, ok := strings.CutSuffix(word, "ies"); ok && len(base) > 2 {
		return base + "y"
	}
	for _, suffix := range []string{"es", "s", "ing", "ed"} {
		base, ok := strings.CutSuffix(word, suffix)
		if !ok || len(base) <= 2 {
			continue
		}
		// "verified" cuts to "verifi"; the verb it belongs to is "verify".
		if stripped, wasI := strings.CutSuffix(base, "i"); wasI {
			return stripped + "y"
		}
		return base
	}
	return word
}
