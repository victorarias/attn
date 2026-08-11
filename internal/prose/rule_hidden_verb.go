package prose

import (
	"fmt"
	"strings"
)

// lightVerbs carry almost no meaning on their own. Paired with a nominalized
// noun they form a hidden verb: "give consideration to" is "consider" with
// three extra words and the action turned into a thing.
//
// Bare nominalizations are not the target. Technical prose needs
// "implementation" and "configuration" as nouns, and a rule against those
// fires on every healthy document in the corpus.
var lightVerbs = map[string]bool{
	"make": true, "makes": true, "made": true, "making": true,
	"give": true, "gives": true, "gave": true, "given": true, "giving": true,
	"take": true, "takes": true, "took": true, "taken": true, "taking": true,
	"provide": true, "provides": true, "provided": true, "providing": true,
	"perform": true, "performs": true, "performed": true, "performing": true,
	"conduct": true, "conducts": true, "conducted": true, "conducting": true,
	"undertake": true, "undertakes": true, "undertook": true, "undertaking": true,
	"achieve": true, "achieves": true, "achieved": true, "achieving": true,
	"reach": true, "reaches": true, "reached": true, "reaching": true,
	"effect": true, "effects": true, "effected": true,
}

// verbForNominal is the rule, not a decoration on it: a light verb next to a
// nominalization is a hidden verb only when a plain verb says the same thing,
// and this table is where that is known.
//
// A suffix test looks like the general version of this and is not. Measured on
// the healthy corpus, "-tion/-sion/-ment/-ity" matched "takes an extension",
// "reached production" and "makes a particular session" — noun suffixes on
// nouns that mean what they say. Every entry here produces a finding that
// names the verb to use instead, which is also the only kind of finding this
// rule can defend.
var verbForNominal = map[string]string{
	"consideration":  "consider",
	"decision":       "decide",
	"determination":  "determine",
	"assumption":     "assume",
	"discussion":     "discuss",
	"application":    "apply",
	"provision":      "provide",
	"evaluation":     "evaluate",
	"examination":    "examine",
	"implementation": "implement",
	"announcement":   "announce",
	"agreement":      "agree",
	"assessment":     "assess",
	"adjustment":     "adjust",
	"improvement":    "improve",
	"measurement":    "measure",
	"statement":      "state",
	"arrangement":    "arrange",
	"comparison":     "compare",
	"contribution":   "contribute",
	"description":    "describe",
	"explanation":    "explain",
	"indication":     "indicate",
	"introduction":   "introduce",
	"investigation":  "investigate",
	"modification":   "modify",
	"notification":   "notify",
	"observation":    "observe",
	"preparation":    "prepare",
	"presentation":   "present",
	"recommendation": "recommend",
	"reduction":      "reduce",
	"resolution":     "resolve",
	"revision":       "revise",
	"selection":      "select",
	"separation":     "separate",
	"specification":  "specify",
	"suggestion":     "suggest",
	"verification":   "verify",
	"validation":     "validate",
	"correction":     "correct",
	"creation":       "create",
	"deletion":       "delete",
	"detection":      "detect",
	"extension":      "extend",
	"inspection":     "inspect",
	"migration":      "migrate",
	"reference":      "refer",
	"reliance":       "rely",
	"acceptance":     "accept",
}

type hiddenVerbRule struct{}

func (hiddenVerbRule) name() string { return "hidden-verb" }

func (hiddenVerbRule) check(doc *Document, _ Thresholds) []Finding {
	var out []Finding
	for _, sent := range doc.Sentences {
		tokens := sent.words()
		for i, t := range tokens {
			if !isVerb(t) || !lightVerbs[lower(t)] {
				continue
			}
			noun, verb, at, ok := nominalAfter(tokens, i)
			if !ok || !prepositionFollows(tokens, at) {
				continue
			}
			out = append(out, doc.finding(
				"hidden-verb", t.Start, noun.End,
				fmt.Sprintf("%q buries the verb in a noun", collapseSpace(doc.Source[t.Start:noun.End])),
				verb,
			))
		}
	}
	return out
}

// nominalAfter finds the nominalization a light verb is acting on — the next
// noun, reachable across only the words that can sit between them — and the
// verb it is hiding.
func nominalAfter(tokens []Token, verbAt int) (Token, string, int, bool) {
	for j := verbAt + 1; j < len(tokens) && j <= verbAt+3; j++ {
		t := tokens[j]
		if isNoun(t) {
			verb, ok := verbForNominal[singular(lower(t))]
			return t, verb, j, ok
		}
		switch t.Tag {
		case "DT", "JJ", "PRP$", "CD":
			continue
		default:
			return Token{}, "", 0, false
		}
	}
	return Token{}, "", 0, false
}

// prepositionFollows is what separates a hidden verb from a noun that means
// what it says. The real object of the buried verb has to attach through a
// preposition — "give consideration to the fact", "perform an evaluation of
// every candidate" — because the noun has taken the object slot.
//
// Without this the rule reported "takes an extension", "reached migration 51"
// and "gives extensions" on prose Victor accepted: the same nouns, used as
// nouns. Every one of those disappears here, and the plan's own example
// survives.
func prepositionFollows(tokens []Token, nounAt int) bool {
	if nounAt+1 >= len(tokens) {
		return false
	}
	next := tokens[nounAt+1]
	return next.Tag == "IN" || next.Tag == "TO"
}

// singular folds the plural a nominalization takes when the light verb governs
// several of them ("makes the deletions").
func singular(word string) string {
	if base, ok := strings.CutSuffix(word, "s"); ok && len(base) > 3 {
		return base
	}
	return word
}
