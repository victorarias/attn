// Measures candidate prose features on each corpus pair (the message Victor
// objected to vs the revision he accepted) and reports how often each feature
// moved in the expected direction. Paired within-case, so document length and
// topic cancel out. A feature that does not separate here does not ship.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
)

type window struct {
	Index    int    `json:"index"`
	Dense    string `json:"dense"`
	Revision string `json:"revision"`
}

var (
	codeFence = regexp.MustCompile("(?s)```.*?```")
	inlineTick = regexp.MustCompile("`[^`]*`")
	wordRe    = regexp.MustCompile(`[A-Za-z][A-Za-z'-]*`)
	sentSplit = regexp.MustCompile(`(?m)[.!?]+[\s$]|\n{2,}|\n[-*+] |\n#+ `)

	boldRe      = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	boldLeadRe  = regexp.MustCompile(`(?m)^\s*(?:[-*+]\s+|\d+\.\s+)?\*\*([^*]+)\*\*`)
	nominalRe   = regexp.MustCompile(`(?i)\b\w{4,}(tion|sion|ment|ance|ence|ity|ness|ism)s?\b`)
	nounOfNoun  = regexp.MustCompile(`(?i)\bthe \w+ of (?:the|a|an|its|our|your|their) \w+`)
	expletiveRe = regexp.MustCompile(`(?i)(^|[.!?—:]\s+|\n)\s*(there (is|are|was|were)|it (is|was|'s) \w+ that)\b`)
	passiveRe   = regexp.MustCompile(`(?i)\b(is|are|was|were|be|been|being|gets?|got)\s+(\w+ed|done|made|built|known|seen|given|taken|shown|held|kept|left|meant|sent|set|put|read|run)\b`)
	adverbLyRe  = regexp.MustCompile(`(?i)\b\w{4,}ly\b`)
	rawNumRe    = regexp.MustCompile(`\b\d+(\.\d+)?\s*(ms|s|m|h|MB|GB|KB|%|×|x)\b`)
	contrastRe  = regexp.MustCompile(`(?i)\b(not (just|only|merely) [^,.—]{2,40}[,—] (but|it)|isn'?t [^,.—]{2,40}[,—] it'?s|rather than)\b`)
	emDashRe    = regexp.MustCompile(`—`)
	colonRe     = regexp.MustCompile(`[;:]`)
	parenRe     = regexp.MustCompile(`\([^)]{6,}\)`)
	prepRe      = regexp.MustCompile(`(?i)\b(of|in|on|for|with|by|at|from|into|through|over|under|between|across|against|within|upon|toward|towards|beyond)\b`)
	beVerbRe    = regexp.MustCompile(`(?i)\b(is|are|was|were|be|been|being|'s|'re)\b`)
	subordRe    = regexp.MustCompile(`(?i)\b(which|that|because|since|while|whereas|although|though|unless|whether|so that|such that|given that)\b`)
	cleftRe     = regexp.MustCompile(`(?i)\b(what [\w\s]{2,30} (is|does|means|buys|costs) is|the (thing|point|question|answer|reason) (is|here is)|it'?s? \w+ that)\b`)
	firstPersonRe = regexp.MustCompile(`(?i)\b(i|i'?m|i'?d|i'?ll|i'?ve|me|my|we|we'?re|our|you|you'?re|your)\b`)

	hedges = []string{"honestly", "arguably", "somewhat", "fairly", "rather", "quite",
		"perhaps", "possibly", "presumably", "seems", "appears", "tends to", "a bit",
		"modest", "borderline", "roughly", "more or less", "to be fair", "in practice",
		"worth noting", "worth naming", "to be clear", "if anything", "that said"}

	aiisms = []string{"genuinely", "precisely", "exactly the", "crucially", "notably",
		"importantly", "fundamentally", "essentially", "effectively", "meaningfully",
		"robust", "leverage", "delve", "underscore", "nuanced", "holistic", "seamless",
		"the real ", "the whole point", "which is the thing", "and that's the", "here's the thing",
		"it's worth", "the honest", "let me be", "i'd rather", "fair question", "good catch",
		"you're right", "great question", "that's the tension", "the tension"}

	preambles = []string{"fair question", "good catch", "great question", "you're right",
		"honestly", "to be clear", "short version", "the short answer", "let me", "i'd rather",
		"so —", "so,", "right —", "okay,", "ok,"}
)

func strip(s string) string {
	s = codeFence.ReplaceAllString(s, " ")
	s = inlineTick.ReplaceAllString(s, " ")
	return s
}

func words(s string) []string { return wordRe.FindAllString(s, -1) }

func sentences(s string) []string {
	var out []string
	for _, part := range sentSplit.Split(s, -1) {
		p := strings.TrimSpace(part)
		if len(words(p)) >= 3 {
			out = append(out, p)
		}
	}
	return out
}

func paragraphs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\n\n") {
		if t := strings.TrimSpace(p); len(words(t)) >= 5 {
			out = append(out, t)
		}
	}
	return out
}

func countAny(lower string, needles []string) int {
	n := 0
	for _, w := range needles {
		n += strings.Count(lower, w)
	}
	return n
}

// feats returns each candidate feature normalized so a longer document does not
// score worse for free.
func feats(raw string) map[string]float64 {
	s := strip(raw)
	w := words(s)
	nw := float64(len(w))
	if nw < 20 {
		return nil
	}
	per100 := func(n int) float64 { return float64(n) / nw * 100 }
	lower := strings.ToLower(s)

	sents := sentences(s)
	var lens []float64
	for _, x := range sents {
		lens = append(lens, float64(len(words(x))))
	}
	sort.Float64s(lens)
	mean, sd := 0.0, 0.0
	for _, l := range lens {
		mean += l
	}
	if len(lens) > 0 {
		mean /= float64(len(lens))
		for _, l := range lens {
			sd += (l - mean) * (l - mean)
		}
		sd = math.Sqrt(sd / float64(len(lens)))
	}
	pct := func(p float64) float64 {
		if len(lens) == 0 {
			return 0
		}
		return lens[int(math.Min(float64(len(lens)-1), p*float64(len(lens))))]
	}

	var paraWords []float64
	for _, p := range paragraphs(s) {
		paraWords = append(paraWords, float64(len(words(p))))
	}
	paraMean := 0.0
	for _, p := range paraWords {
		paraMean += p
	}
	if len(paraWords) > 0 {
		paraMean /= float64(len(paraWords))
	}

	longSent := 0
	for _, l := range lens {
		if l > 30 {
			longSent++
		}
	}

	preamble := 0.0
	head := strings.ToLower(strings.TrimSpace(s))
	if len(head) > 80 {
		head = head[:80]
	}
	for _, p := range preambles {
		if strings.HasPrefix(head, p) {
			preamble = 1
			break
		}
	}

	// Nominal style piles abstract nouns onto prepositional phrases ("the honest
	// ceiling of the direct benefit"); plain prose puts a concrete subject on a
	// verb. Both are countable without a tagger.
	var lenSum, long8, long11 float64
	for _, x := range w {
		lenSum += float64(len(x))
		if len(x) >= 8 {
			long8++
		}
		if len(x) >= 11 {
			long11++
		}
	}
	commas := float64(strings.Count(s, ","))
	perSent := func(n float64) float64 {
		if len(sents) == 0 {
			return 0
		}
		return n / float64(len(sents))
	}

	return map[string]float64{
		"word_len_mean":     lenSum / nw,
		"long_words_8":      long8 / nw * 100,
		"long_words_11":     long11 / nw * 100,
		"preposition":       per100(len(prepRe.FindAllString(s, -1))),
		"be_verb":           per100(len(beVerbRe.FindAllString(s, -1))),
		"subordinator":      perSent(float64(len(subordRe.FindAllString(s, -1)))),
		"commas_per_sent":   perSent(commas),
		"cleft":             per100(len(cleftRe.FindAllString(s, -1))),
		"first_person":      per100(len(firstPersonRe.FindAllString(s, -1))),
		"sent_mean_words":   mean,
		"sent_p90_words":    pct(0.9),
		"sent_max_words":    pct(1.0),
		"sent_sd_words":     sd,
		"long_sents_per100": per100(longSent),
		"para_mean_words":   paraMean,
		"em_dash":           per100(len(emDashRe.FindAllString(s, -1))),
		"bold":              per100(len(boldRe.FindAllString(raw, -1))),
		"bold_lead":         per100(len(boldLeadRe.FindAllString(raw, -1))),
		"nominalization":    per100(len(nominalRe.FindAllString(s, -1))),
		"noun_of_noun":      per100(len(nounOfNoun.FindAllString(s, -1))),
		"expletive":         per100(len(expletiveRe.FindAllString(s, -1))),
		"passive":           per100(len(passiveRe.FindAllString(s, -1))),
		"adverb_ly":         per100(len(adverbLyRe.FindAllString(s, -1))),
		"raw_measure":       per100(len(rawNumRe.FindAllString(s, -1))),
		"contrast_tic":      per100(len(contrastRe.FindAllString(s, -1))),
		"colon_semicolon":   per100(len(colonRe.FindAllString(s, -1))),
		"parenthetical":     per100(len(parenRe.FindAllString(s, -1))),
		"hedge":             per100(countAny(lower, hedges)),
		"aiism":             per100(countAny(lower, aiisms)),
		"meta_preamble":     preamble,
		"total_words":       nw,
	}
}

func main() {
	f, _ := os.Open(os.Args[1])
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)


	var pairs []pair
	for sc.Scan() {
		var w window
		if json.Unmarshal(sc.Bytes(), &w) != nil {
			continue
		}
		d, r := feats(w.Dense), feats(w.Revision)
		if d == nil || r == nil {
			continue
		}
		pairs = append(pairs, pair{d, r})
	}

	var names []string
	for k := range pairs[0].dense {
		names = append(names, k)
	}
	sort.Strings(names)

	fmt.Printf("%-20s %8s %8s %8s %10s %5s\n", "feature", "dense", "revised", "delta%", "dense>rev", "n")
	fmt.Println(strings.Repeat("-", 66))
	type row struct {
		name               string
		dm, rm, delta, win float64
		n                  int
	}
	var rows []row
	for _, n := range names {
		var dsum, rsum, wins, valid float64
		for _, p := range pairs {
			dsum += p.dense[n]
			rsum += p.rev[n]
			if p.dense[n] != p.rev[n] {
				valid++
				if p.dense[n] > p.rev[n] {
					wins++
				}
			}
		}
		np := float64(len(pairs))
		dm, rm := dsum/np, rsum/np
		delta := 0.0
		if rm != 0 {
			delta = (dm - rm) / rm * 100
		}
		win := 0.0
		if valid > 0 {
			win = wins / valid * 100
		}
		rows = append(rows, row{n, dm, rm, delta, win, int(valid)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].win > rows[j].win })
	for _, r := range rows {
		fmt.Printf("%-20s %8.2f %8.2f %+8.0f%% %8.0f%% %5d\n", r.name, r.dm, r.rm, r.delta, r.win, r.n)
	}
	fmt.Printf("\npairs=%d\n", len(pairs))
	tripwires(pairs)

	// Tripwires go past where the accepted prose lives, so only the dense side
	// touches them. Printing both distributions is the receipt for each number.
	fmt.Printf("\n%-20s %-28s %s\n", "feature", "ACCEPTED p50/p75/p90/max", "DENSE p50/p75/p90/max")
	fmt.Println(strings.Repeat("-", 76))
	for _, n := range []string{"long_words_8", "long_words_11", "word_len_mean", "preposition",
		"commas_per_sent", "parenthetical", "sent_mean_words", "contrast_tic", "em_dash",
		"sent_p90_words", "long_sents_per100", "bold"} {
		var good, dense []float64
		for _, p := range pairs {
			good = append(good, p.rev[n])
			dense = append(dense, p.dense[n])
		}
		sort.Float64s(good)
		sort.Float64s(dense)
		q := func(v []float64, p float64) float64 {
			return v[int(math.Min(float64(len(v)-1), p*float64(len(v))))]
		}
		fmt.Printf("%-20s %6.2f %6.2f %6.2f %6.2f    %6.2f %6.2f %6.2f %6.2f\n", n,
			q(good, 0.5), q(good, 0.75), q(good, 0.9), q(good, 1.0),
			q(dense, 0.5), q(dense, 0.75), q(dense, 0.9), q(dense, 1.0))
	}

	tripwires(pairs)
}

