package prose

import (
	"fmt"
	"strings"
)

// nounStringRule reports a pile-up of consecutive nouns. Each one modifies the
// next, so the reader holds the whole stack before learning what the phrase is
// about. Unpacking one into a verb or a preposition costs a word and buys the
// sentence back.
type nounStringRule struct{}

func (nounStringRule) name() string { return "noun-string" }

func (nounStringRule) check(doc *Document, th Thresholds) []Finding {
	var out []Finding
	for _, sent := range doc.Sentences {
		// A heading is a label, and a label is a noun phrase by construction.
		if doc.Blocks[sent.Block].Kind == KindHeading {
			continue
		}
		// Punctuation is what ends a noun string, so this reads the raw token
		// stream. Over words alone, the comma-separated list "state, input,
		// tool_call" reads as three nouns in a row, which is a list rather
		// than a pile-up and is exactly what a reader finds easy.
		tokens := sent.Tokens
		for i := 0; i < len(tokens); {
			j := i
			for j < len(tokens) && isNoun(tokens[j]) {
				j++
			}
			if run := j - i; run >= th.NounStringLength {
				span := doc.Source[tokens[i].Start:tokens[j-1].End]
				out = append(out, doc.finding(
					"noun-string", tokens[i].Start, tokens[j-1].End,
					fmt.Sprintf("%d nouns in a row (%q) — the reader holds the whole stack before the phrase resolves; turn one into a verb or a preposition", run, collapseSpace(span)),
					"",
				))
			}
			i = max(j, i+1)
		}
	}
	return out
}

// passiveRule reports an instruction whose actor is nowhere in the sentence.
//
// The passive itself is not the defect. "The archive is verified against the
// lock" is right when the verifier is beside the point, and 1038 of those sit
// in prose Victor accepted — a rule against them reports the corpus, not a
// problem. What costs the reader is the passive that tells someone to do
// something and never says who: "the profile must be cleaned up" leaves them
// unable to tell whether it is their job. The obligation is the signal, so the
// rule fires on a modal governing a passive and on nothing else.
type passiveRule struct{}

func (passiveRule) name() string { return "passive-no-actor" }

// obligationModals say someone has to act. "can" and "may" describe what is
// possible, which is a property of the thing rather than a job for a person,
// so they are not here.
var obligationModals = map[string]bool{
	"must": true, "should": true, "shall": true, "ought": true, "needs": true,
}

// adjectivalParticiples read as descriptions of a state rather than reports of
// an action, so a missing actor costs the reader nothing even under a modal.
var adjectivalParticiples = map[string]bool{
	"done": true, "gone": true, "known": true, "meant": true, "supposed": true,
	"required": true, "expected": true, "based": true, "related": true,
	"limited": true, "interested": true, "involved": true, "concerned": true,
	"tied": true, "bound": true, "aware": true, "worth": true, "left": true,
}

func (passiveRule) check(doc *Document, _ Thresholds) []Finding {
	var out []Finding
	for _, sent := range doc.Sentences {
		tokens := sent.words()
		for i, t := range tokens {
			if t.Tag != "MD" || !obligationModals[lower(t)] {
				continue
			}
			beAt, ok := beFormAfter(tokens, i)
			if !ok {
				continue
			}
			participle, at, ok := participleAfter(tokens, beAt)
			if !ok || adjectivalParticiples[lower(participle)] {
				continue
			}
			if agentFollows(tokens, at) {
				continue
			}
			span := doc.Source[t.Start:participle.End]
			out = append(out, doc.finding(
				"passive-no-actor", t.Start, participle.End,
				fmt.Sprintf("%q tells someone to act without saying who — name the actor", collapseSpace(span)),
				"",
			))
		}
	}
	return out
}

// beFormAfter finds the "be" a modal governs, across an intervening adverb.
func beFormAfter(tokens []Token, modalAt int) (int, bool) {
	for j := modalAt + 1; j < len(tokens) && j <= modalAt+3; j++ {
		if isBeForm(tokens[j]) {
			return j, true
		}
		if isAdverb(tokens[j]) {
			continue
		}
		return 0, false
	}
	return 0, false
}

// participleAfter finds the past participle a be-verb governs, reachable
// across the adverbs that can sit between them ("is never written").
func participleAfter(tokens []Token, beAt int) (Token, int, bool) {
	for j := beAt + 1; j < len(tokens) && j <= beAt+3; j++ {
		if tokens[j].Tag == "VBN" {
			return tokens[j], j, true
		}
		if isAdverb(tokens[j]) || tokens[j].Tag == "RP" {
			continue
		}
		return Token{}, 0, false
	}
	return Token{}, 0, false
}

// agentFollows reports whether a by-phrase names the actor. It stops at the
// clause boundary, so the "by" of a later clause does not excuse this one.
func agentFollows(tokens []Token, participleAt int) bool {
	for j := participleAt + 1; j < len(tokens) && j <= participleAt+6; j++ {
		switch {
		case lower(tokens[j]) == "by":
			return true
		case isVerb(tokens[j]) && !isAuxiliaryForm(tokens[j]):
			return false
		}
	}
	return false
}

// expletiveRule reports a sentence that opens on a placeholder. "There are
// three cases" spends its first two words saying nothing; the subject arrives
// late and the verb is always "to be".
type expletiveRule struct{}

func (expletiveRule) name() string { return "expletive-opener" }

func (expletiveRule) check(doc *Document, _ Thresholds) []Finding {
	var out []Finding
	for _, sent := range doc.Sentences {
		tokens := sent.words()
		if len(tokens) < 2 || !isBeForm(tokens[1]) {
			continue
		}
		first := strings.ToLower(tokens[0].Text)
		switch {
		case first == "there":
		case first == "it" && clauseMarkerFollows(tokens):
		default:
			continue
		}
		span := doc.Source[tokens[0].Start:tokens[1].End]
		out = append(out, doc.finding(
			"expletive-opener", tokens[0].Start, tokens[1].End,
			fmt.Sprintf("%q opens on a placeholder — start with the subject", collapseSpace(span)),
			"",
		))
	}
	return out
}

// clauseMarkerFollows separates the expletive "it" ("it is clear that …") from
// the pronoun "it" ("it works"), which is fine.
//
// The marker has to be the complement of the opening "is", and two things keep
// it there. A finite verb ends the scan, because it carries a clause with its
// own subject and anything past it belongs to that clause. And "to" counts only
// when an infinitive follows it, which is what the expletive always takes ("it
// is time to go"); a stranded preposition is not a marker. Without both, a
// sentence's last word decided the rule — "it is slower than the path we
// compared it to" opens on a real pronoun and fired anyway. Participles and
// gerunds do not end the scan: they are still the opening predicate ("it is
// claimed that …", "it is worth checking whether …").
func clauseMarkerFollows(tokens []Token) bool {
	for i, t := range tokens[2:] {
		switch lower(t) {
		case "that", "whether", "which":
			return true
		case "to":
			if infinitiveFollows(tokens[2+i:]) {
				return true
			}
		}
		switch t.Tag {
		case "VBZ", "VBD", "VBP":
			return false
		}
	}
	return false
}

// infinitiveFollows reports whether "to" heads an infinitive, reachable across
// the adverb that can split it.
func infinitiveFollows(tokens []Token) bool {
	for _, t := range tokens[1:] {
		if isAdverb(t) {
			continue
		}
		return t.Tag == "VB"
	}
	return false
}

// longSentenceRule is the length tripwire: generous by design, so only a
// sentence past anything in the healthy corpus reaches it. A tighter number
// would push toward staccato, which the anti-gaming rules then have to undo.
type longSentenceRule struct{}

func (longSentenceRule) name() string { return "long-sentence" }

func (longSentenceRule) check(doc *Document, th Thresholds) []Finding {
	var out []Finding
	for _, sent := range doc.Sentences {
		if doc.Blocks[sent.Block].Kind == KindHeading {
			continue
		}
		if n := sent.wordCount(); n > th.LongSentenceWords {
			out = append(out, doc.finding(
				"long-sentence", sent.Start, sent.End,
				fmt.Sprintf("%d words in one sentence, past the %d-word tripwire — find the second idea in it and give it its own sentence", n, th.LongSentenceWords),
				"",
			))
		}
	}
	return out
}
