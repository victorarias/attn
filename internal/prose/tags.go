package prose

import (
	"strings"
	"unicode"
)

// Penn Treebank tag predicates. The tagger produces Penn, and the rules read
// it directly rather than widening to Universal POS first: the distinctions
// that matter here — VBN versus VBD for the passive, NNP versus NN for a noun
// string — are exactly the ones the widening throws away.

// isNoun also insists the token holds a letter. The tagger reaches for NN when
// it has nothing else to say, so an arrow, a plus, or an em dash between two
// nouns comes back tagged NN and joins them into a noun string that the reader
// never saw.
func isNoun(t Token) bool {
	switch t.Tag {
	case "NN", "NNS", "NNP", "NNPS":
		return hasLetter(t.Text)
	}
	return false
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func isVerb(t Token) bool { return strings.HasPrefix(t.Tag, "VB") }

func isAdjective(t Token) bool {
	switch t.Tag {
	case "JJ", "JJR", "JJS":
		return true
	}
	return false
}

func isAdverb(t Token) bool {
	switch t.Tag {
	case "RB", "RBR", "RBS":
		return true
	}
	return false
}

// beForms are the copula and its inflections: the left half of every passive,
// and an auxiliary that carries no proposition of its own.
var beForms = map[string]bool{
	"be": true, "am": true, "is": true, "are": true, "was": true,
	"were": true, "been": true, "being": true, "'s": true, "'re": true,
	"'m": true,
}

// otherAuxiliaries are have/do: auxiliary when a verb follows, and the
// sentence's own predicate when none does.
var otherAuxiliaries = map[string]bool{
	"have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "'ve": true, "'d": true,
}

func isBeForm(t Token) bool { return isVerb(t) && beForms[strings.ToLower(t.Text)] }

func isAuxiliaryForm(t Token) bool {
	if !isVerb(t) {
		return false
	}
	lower := strings.ToLower(t.Text)
	return beForms[lower] || otherAuxiliaries[lower]
}

func lower(t Token) string { return strings.ToLower(t.Text) }
