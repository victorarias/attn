// Package prosegate decides whether a piece of agent-written prose should be
// handed back to its author with a nudge to rewrite it plainly.
//
// It is a trigger, not a critic: it answers "does this need a rewrite?" and
// never says which words to change. The thresholds come from a corpus of real
// complaints paired with the revisions Victor accepted; the rules that did not
// separate those two piles are absent on purpose. Design and receipts:
// docs/plans/2026-08-10-prose-density-gate.md.
package prosegate

import (
	"regexp"
	"sort"
	"strings"
)

// DefaultNudge is the /bro wording plus two clauses about what must survive a
// rewrite. A nudge that only says "be concise" eventually deletes a diagram;
// measured on 100 real messages, one that stops at "keep diagrams and code"
// still drops numbers, file:line citations, and hedges — plainness eats the
// receipts. The rule is about losing information, not about touching markup.
const DefaultNudge = "Restate your last message. Stop using jargon and speak " +
	"coherently. State it more simply and concisely, like one human talking to " +
	"another. Keep what carries information the prose cannot — diagrams, tables, " +
	"code, commands, links. Improve them if you can; just don't drop them. Keep " +
	"every number, measurement, and file or line reference, and keep stated " +
	"uncertainty stated — a receipt or a caveat is information, not clutter."

// MinWords is where the gate abstains. 90% of the calibration corpus sits at or
// above 167 words; below this the per-100-word rates are one sentence wide.
const MinWords = 150

// DefaultThresholds were calibrated leave-one-out against the corpus using
// Measure below: each sits 2% above the highest value reached by any accepted
// document other than the one being judged. Together they fire on 80% of the
// prose Victor objected to and 20% of the prose he accepted. A legitimate
// message tripping one means the number is wrong — regenerate them with
// TestRecalibrate, do not hand-tune a threshold to silence one document.
func DefaultThresholds() map[string]float64 {
	return map[string]float64{
		"long_words_8":      14.40, // words of 8+ characters per 100 words
		"long_sents_per100": 0.96,  // sentences over 30 words, per 100 words
		"em_dash":           2.24,  // per 100 words
		"sent_mean_words":   19.53, // mean sentence length
		"word_len_mean":     4.77,  // mean word length in characters
		"preposition":       7.65,  // per 100 words; nominal style piles these up
	}
}

// Config carries the tunable surface. Zero value is not usable; use Default.
type Config struct {
	Nudge      string
	Thresholds map[string]float64
	MinWords   int
}

// Default returns the shipped configuration.
func Default() Config {
	return Config{Nudge: DefaultNudge, Thresholds: DefaultThresholds(), MinWords: MinWords}
}

// GateHit records one tripwire that fired, with the value that tripped it.
type GateHit struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

// Verdict is the whole output: whether to nudge, and the evidence.
type Verdict struct {
	Tripped   bool      `json:"tripped"`
	Abstained bool      `json:"abstained"` // too short to judge
	Words     int       `json:"words"`
	Gates     []GateHit `json:"gates,omitempty"`
	Nudge     string    `json:"nudge,omitempty"`
}

var (
	fenced      = regexp.MustCompile("(?s)```.*?```")
	inlineCode  = regexp.MustCompile("`[^`]*`")
	tableRow    = regexp.MustCompile(`(?m)^\s*\|.*\|\s*$`)
	commandLine = regexp.MustCompile(`(?m)^\s*[$>] .*$`)
	urlRe       = regexp.MustCompile(`https?://\S+`)
	mdLink      = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	wordRe      = regexp.MustCompile(`[A-Za-z][A-Za-z'-]*`)
	sentSplit   = regexp.MustCompile(`(?m)[.!?]+[\s$]|\n{2,}|\n[-*+] |\n#+ `)
	emDashRe    = regexp.MustCompile(`—`)
	prepRe      = regexp.MustCompile(`(?i)\b(of|in|on|for|with|by|at|from|into|through|over|under|between|across|against|within|upon|toward|towards|beyond)\b`)
)

// proseOnly strips everything that is not prose, so structure never trips a
// gate on its own. Fenced blocks cover ```mermaid; a wide table would otherwise
// read as long words in long lines.
func proseOnly(markdown string) string {
	s := fenced.ReplaceAllString(markdown, " ")
	s = tableRow.ReplaceAllString(s, " ")
	s = commandLine.ReplaceAllString(s, " ")
	s = mdLink.ReplaceAllString(s, "$1")
	s = urlRe.ReplaceAllString(s, " ")
	s = inlineCode.ReplaceAllString(s, " ")
	return s
}

func sentences(s string) []string {
	var out []string
	for _, part := range sentSplit.Split(s, -1) {
		if p := strings.TrimSpace(part); len(wordRe.FindAllString(p, -1)) >= 3 {
			out = append(out, p)
		}
	}
	return out
}

// Measure computes every gated feature over the prose in markdown.
func Measure(markdown string) (map[string]float64, int) {
	s := proseOnly(markdown)
	w := wordRe.FindAllString(s, -1)
	n := float64(len(w))
	if n == 0 {
		return nil, 0
	}

	var lenSum, long8 float64
	for _, x := range w {
		lenSum += float64(len(x))
		if len(x) >= 8 {
			long8++
		}
	}

	sents := sentences(s)
	var sentSum, longSents float64
	for _, x := range sents {
		c := float64(len(wordRe.FindAllString(x, -1)))
		sentSum += c
		if c > 30 {
			longSents++
		}
	}
	sentMean := 0.0
	if len(sents) > 0 {
		sentMean = sentSum / float64(len(sents))
	}

	per100 := func(c int) float64 { return float64(c) / n * 100 }
	return map[string]float64{
		"long_words_8":      long8 / n * 100,
		"long_sents_per100": longSents / n * 100,
		"em_dash":           per100(len(emDashRe.FindAllString(s, -1))),
		"sent_mean_words":   sentMean,
		"word_len_mean":     lenSum / n,
		"preposition":       per100(len(prepRe.FindAllString(s, -1))),
	}, len(w)
}

// Check reports whether markdown should be handed back for a plain rewrite.
func Check(markdown string, cfg Config) Verdict {
	values, words := Measure(markdown)
	v := Verdict{Words: words}
	if words < cfg.MinWords {
		v.Abstained = true
		return v
	}
	names := make([]string, 0, len(cfg.Thresholds))
	for name := range cfg.Thresholds {
		names = append(names, name)
	}
	sort.Strings(names) // stable order for --json and for tests
	for _, name := range names {
		if value, ok := values[name]; ok && value > cfg.Thresholds[name] {
			v.Gates = append(v.Gates, GateHit{Name: name, Value: value, Threshold: cfg.Thresholds[name]})
		}
	}
	if len(v.Gates) > 0 {
		v.Tripped = true
		v.Nudge = cfg.Nudge
	}
	return v
}
