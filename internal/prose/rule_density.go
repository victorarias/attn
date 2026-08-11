package prose

import "fmt"

// ideaDensityRule reports a sentence carrying more propositions than its words
// can hold — the metric that says *overloaded* where length says only *long*.
//
// The count is a Go port of CPIDR's core: propositions are the verbs,
// adjectives, adverbs, prepositions, conjunctions and numbers, minus the
// tokens CPIDR's adjustment rules exclude because they belong to a
// proposition already counted. Density is propositions over words. Turkstra
// and Covington measured that count against hand-coded propositions at r=0.97
// on retold-narrative transcripts:
// https://link.springer.com/article/10.3758/BRM.40.2.540
//
// The ratio is what makes chopping a poor escape from this rule. Splitting a
// sentence moves both words and propositions into the halves, so each half
// keeps roughly the density of the whole. It is not free — the conjunctions a
// split deletes are propositions, and three measured rewrites lost 0.03 to
// 0.09 — but that is a fraction of the headroom between what accepted prose
// reaches and the tripwire, and it is the opposite of a length rule, which a
// split satisfies outright. See TestIdeaDensityBarelyMovesUnderChopping.
type ideaDensityRule struct{}

func (ideaDensityRule) name() string { return "idea-density" }

func (ideaDensityRule) check(doc *Document, th Thresholds) []Finding {
	var out []Finding
	for _, sent := range doc.Sentences {
		if doc.Blocks[sent.Block].Kind == KindHeading {
			continue
		}
		words := sent.words()
		if len(words) < th.IdeaDensityMinWords {
			continue
		}
		props := countPropositions(words)
		density := float64(props) / float64(len(words))
		if density <= th.IdeaDensityMax {
			continue
		}
		out = append(out, doc.finding(
			"idea-density", sent.Start, sent.End,
			fmt.Sprintf("%d propositions in %d words (%.2f per word, past %.2f) — this sentence is carrying several ideas at once; separate them rather than shortening it",
				props, len(words), density, th.IdeaDensityMax),
			"",
		))
	}
	return out
}

// countPropositions counts the propositions in one sentence's word tokens.
//
// The adjustment rules implemented here, each of which CPIDR also applies:
//
//   - an auxiliary be/have/do governing another verb belongs to that verb's
//     proposition, not its own ("has verified" is one);
//   - determiners and articles carry no proposition;
//   - a modal does carry one, because it asserts something the bare verb does
//     not.
//
// One simplification: Penn tags both the infinitive marker and the preposition
// "to" as TO, and nothing here counts TO. CPIDR counts the preposition, so
// this undercounts by one wherever "to" is directional. It undercounts every
// sentence the same way, and the threshold is calibrated on these numbers, so
// the effect is absorbed rather than hidden.
//
// The rules CPIDR applies that are not here need a parse tree rather than a
// tag sequence, and the plan cuts anything needing a real parser.
func countPropositions(words []Token) int {
	count := 0
	for i, t := range words {
		switch {
		case t.Tag == "MD":
			count++
		case isVerb(t):
			if isAuxiliaryForm(t) && verbFollows(words, i) {
				continue
			}
			count++
		case isAdjective(t), isAdverb(t):
			count++
		case t.Tag == "IN", t.Tag == "CC", t.Tag == "CD":
			count++
		case t.Tag == "WRB", t.Tag == "WDT", t.Tag == "WP":
			count++
		case t.Tag == "PRP$", t.Tag == "POS":
			count++
		}
	}
	return count
}

// verbFollows reports whether an auxiliary governs a later verb, reachable
// across the adverbs and negation that can sit between them.
func verbFollows(words []Token, auxAt int) bool {
	for j := auxAt + 1; j < len(words) && j <= auxAt+3; j++ {
		if isVerb(words[j]) {
			return true
		}
		if isAdverb(words[j]) || words[j].Tag == "RP" {
			continue
		}
		return false
	}
	return false
}
