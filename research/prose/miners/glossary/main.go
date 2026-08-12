// Tests whether glossary discipline separates the prose Victor objected to from
// the revision he accepted. Two measures: how much canonical vocabulary a
// message uses, and how much vocabulary it invents (bolded or quoted multi-word
// phrases that are not in the glossary). If neither separates, the glossary
// check is not the lever I claimed it was.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	headingRe = regexp.MustCompile(`(?m)^#{2,3}\s+(.+?)\s*$`)
	boldRe    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	quotedRe  = regexp.MustCompile(`[""]([^""]{3,60})[""]|"([^"]{3,60})"`)
	tickRe    = regexp.MustCompile("`([^`]{3,60})`")
	wordRe    = regexp.MustCompile(`[A-Za-z][A-Za-z'-]*`)
	codeFence = regexp.MustCompile("(?s)```.*?```")
	cleanTerm = regexp.MustCompile(`[^a-z0-9 -]`)
)

// normalize reduces a phrase to a comparable key: lowercase words only, leading
// article dropped, so "The Notebook" and "notebook" are the same term.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = cleanTerm.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	for _, a := range []string{"the ", "a ", "an ", "its ", "our "} {
		s = strings.TrimPrefix(s, a)
	}
	return s
}

func loadGlossary(path string) map[string]bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	text := string(raw)
	terms := map[string]bool{}
	add := func(s string) {
		t := normalize(s)
		n := len(strings.Fields(t))
		if n >= 1 && n <= 4 && len(t) >= 3 {
			terms[t] = true
		}
	}
	for _, m := range headingRe.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	for _, m := range boldRe.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	return terms
}

func measure(raw string, gloss map[string]bool) (canonical, coined, words float64, examples []string) {
	body := codeFence.ReplaceAllString(raw, " ")
	w := wordRe.FindAllString(body, -1)
	words = float64(len(w))
	if words < 20 {
		return 0, 0, 0, nil
	}
	lower := strings.ToLower(body)

	for term := range gloss {
		if strings.Contains(lower, term) {
			canonical += float64(strings.Count(lower, term))
		}
	}

	// Vocabulary the message sets off as a named thing. In the glossary it is
	// shared language; outside it, the reader has to decode a coinage.
	seen := map[string]bool{}
	for _, re := range []*regexp.Regexp{boldRe, quotedRe, tickRe} {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			phrase := m[1]
			if phrase == "" && len(m) > 2 {
				phrase = m[2]
			}
			t := normalize(phrase)
			n := len(strings.Fields(t))
			if n < 2 || n > 5 || gloss[t] || seen[t] {
				continue
			}
			seen[t] = true
			coined++
			if len(examples) < 4 {
				examples = append(examples, t)
			}
		}
	}
	return canonical / words * 100, coined / words * 100, words, examples
}

func main() {
	gloss := loadGlossary(os.Args[1])
	fmt.Printf("glossary terms loaded: %d\n\n", len(gloss))

	f, _ := os.Open(os.Args[2])
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	type row struct {
		idx                        int
		dCanon, rCanon             float64
		dCoin, rCoin               float64
		dEx                        []string
	}
	var rows []row
	for sc.Scan() {
		var w window
		if json.Unmarshal(sc.Bytes(), &w) != nil {
			continue
		}
		dc, dk, dw, dex := measure(w.Dense, gloss)
		rc, rk, rw, _ := measure(w.Revision, gloss)
		if dw == 0 || rw == 0 {
			continue
		}
		rows = append(rows, row{w.Index, dc, rc, dk, rk, dex})
	}

	var canonWins, coinWins, canonN, coinN float64
	var dcSum, rcSum, dkSum, rkSum float64
	for _, r := range rows {
		dcSum += r.dCanon
		rcSum += r.rCanon
		dkSum += r.dCoin
		rkSum += r.rCoin
		if r.dCanon != r.rCanon {
			canonN++
			// Claim: dense prose uses LESS canonical vocabulary.
			if r.dCanon < r.rCanon {
				canonWins++
			}
		}
		if r.dCoin != r.rCoin {
			coinN++
			// Claim: dense prose invents MORE vocabulary.
			if r.dCoin > r.rCoin {
				coinWins++
			}
		}
	}
	n := float64(len(rows))
	fmt.Printf("%-26s %8s %8s %10s %5s\n", "measure", "dense", "accepted", "claim-wins", "n")
	fmt.Println(strings.Repeat("-", 62))
	fmt.Printf("%-26s %8.2f %8.2f %9.0f%% %5.0f\n", "canonical terms /100w", dcSum/n, rcSum/n, canonWins/canonN*100, canonN)
	fmt.Printf("%-26s %8.2f %8.2f %9.0f%% %5.0f\n", "coined vocabulary /100w", dkSum/n, rkSum/n, coinWins/coinN*100, coinN)

	fmt.Printf("\npairs=%.0f\n\nSample coinages from the prose you objected to:\n", n)
	sort.Slice(rows, func(i, j int) bool { return rows[i].dCoin > rows[j].dCoin })
	for _, r := range rows[:min(6, len(rows))] {
		if len(r.dEx) > 0 {
			fmt.Printf("  #%-3d %s\n", r.idx, strings.Join(r.dEx, " | "))
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
