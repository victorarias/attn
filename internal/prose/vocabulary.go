package prose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Vocabulary is the user-curated word list: terms to object to, and terms that
// override an objection.
//
// The file format is Vale's Vocab — one entry per line, `#` starts a comment,
// and an entry is a case-insensitive regular expression matched at word
// boundaries. Vale's own vocabularies therefore drop in unchanged, which is
// the one piece of that ecosystem worth carrying: the rule DSL is not, because
// the rules that matter here — density, rhythm, cohesion — are computed and no
// Vale rule type expresses them.
//
// A reject entry is the only rule in the gate whose content is a matter of
// taste. That is why it is a file the user owns rather than a list in the
// binary: the gate can be wrong about a word, and the fix is an edit, not a
// release.
type Vocabulary struct {
	Dir    string
	reject []vocabEntry
	accept []*regexp.Regexp
}

type vocabEntry struct {
	pattern *regexp.Regexp
	source  string
}

// vocabFileNames are the two files a vocabulary directory may hold. accept.txt
// is the way out: a term matched by a reject pattern but named in accept.txt
// produces no finding, so a curated list can carve out the one place a word is
// right without anyone editing the reject pattern.
const (
	rejectFile = "reject.txt"
	acceptFile = "accept.txt"
)

// LoadVocabulary reads a vocabulary directory. A missing directory is not an
// error: most files are checked without one.
func LoadVocabulary(dir string) (*Vocabulary, error) {
	v := &Vocabulary{Dir: dir}
	if dir == "" {
		return v, nil
	}
	reject, err := readVocabFile(filepath.Join(dir, rejectFile))
	if err != nil {
		return nil, err
	}
	for _, line := range reject {
		pattern, err := compileVocabEntry(line)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, rejectFile), err)
		}
		v.reject = append(v.reject, vocabEntry{pattern: pattern, source: line})
	}
	accept, err := readVocabFile(filepath.Join(dir, acceptFile))
	if err != nil {
		return nil, err
	}
	for _, line := range accept {
		pattern, err := compileVocabEntry(line)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, acceptFile), err)
		}
		v.accept = append(v.accept, pattern)
	}
	return v, nil
}

func readVocabFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

func compileVocabEntry(entry string) (*regexp.Regexp, error) {
	return regexp.Compile(`(?i)\b(?:` + entry + `)\b`)
}

// FindVocabulary walks up from a path looking for the vocabulary a repository
// keeps for itself. Returns "" when there is none.
func FindVocabulary(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		candidate := filepath.Join(dir, "docs", "prose", "vocabulary")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// rejectWordRule reports a term the vocabulary rejects.
type rejectWordRule struct{ vocab *Vocabulary }

func (rejectWordRule) name() string { return "reject-word" }

func (r rejectWordRule) check(doc *Document, _ Thresholds) []Finding {
	if r.vocab == nil || len(r.vocab.reject) == 0 {
		return nil
	}
	var out []Finding
	for _, sent := range doc.Sentences {
		flat, owner := flattenTokens(sent.Tokens)
		exempt := r.vocab.acceptedRanges(flat)
		for _, entry := range r.vocab.reject {
			for _, match := range entry.pattern.FindAllStringIndex(flat, -1) {
				if within(exempt, match) {
					continue
				}
				first, last := owner[match[0]], owner[match[1]-1]
				start, end := sent.Tokens[first].Start, sent.Tokens[last].End
				if start >= end {
					continue
				}
				out = append(out, doc.finding(
					"reject-word", start, end,
					fmt.Sprintf("%q is on this project's reject list (%s) — say it plainly", collapseSpace(doc.Source[start:end]), entry.source),
					"",
				))
			}
		}
	}
	return out
}

// flattenTokens joins a sentence's tokens with single spaces and returns, for
// each byte of the result, the token it came from. Matching against this
// rather than against the raw sentence means a multi-word entry still matches
// across a line wrap, and every match maps back to exact source offsets.
func flattenTokens(tokens []Token) (string, []int) {
	var b strings.Builder
	var owner []int
	for i, t := range tokens {
		if i > 0 {
			b.WriteByte(' ')
			owner = append(owner, i-1)
		}
		b.WriteString(t.Text)
		for range len(t.Text) {
			owner = append(owner, i)
		}
	}
	return b.String(), owner
}

// acceptedRanges are the stretches of a sentence an accept entry covers.
//
// The exemption is contextual on purpose. "utilize" is the objection, so
// "utilize" cannot also be the exemption — an accept entry has to be able to
// name the phrase around the word ("utilize the", a product name, a quoted
// error string) rather than the word alone.
func (v *Vocabulary) acceptedRanges(sentence string) [][]int {
	var ranges [][]int
	for _, pattern := range v.accept {
		ranges = append(ranges, pattern.FindAllStringIndex(sentence, -1)...)
	}
	return ranges
}

func within(ranges [][]int, match []int) bool {
	for _, r := range ranges {
		if r[0] <= match[0] && match[1] <= r[1] {
			return true
		}
	}
	return false
}
