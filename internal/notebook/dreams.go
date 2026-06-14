package notebook

import (
	"path"
	"sort"
	"strings"
	"time"
)

// Dreaming harvest model.
//
// The dreaming pass consolidates durable memory in two phases. The first —
// "harvest" — is deterministic and LLM-free: it scans recent inputs (journals,
// workspace-context snapshots, closed dispatches), extracts candidate signals,
// and deduplicates them by content. The second — "promote" — is the gated LLM
// pass that distills surviving candidates into grounded memory notes.
//
// This file holds the harvest's pure core: the candidate/signal types, the
// content-keyed merge that turns many sightings of the same fact into one
// candidate with an occurrence count, and the journal block extractor. The
// daemon supplies the workspace-context and dispatch signals (they depend on
// store types) and orchestrates the scan; everything here is store-agnostic and
// unit-testable in isolation.

// Signal source kinds — where a harvested signal originated.
const (
	SignalSourceJournal  = "journal"
	SignalSourceContext  = "context"
	SignalSourceDispatch = "dispatch"
)

// maxSnippetWords bounds a candidate snippet to roughly ~160 tokens (a snippet is
// a pointer back to the full source via Sources, not a copy of it). Word-bounding
// is a deliberate approximation of token-bounding — close enough to keep
// candidates.json compact without a tokenizer dependency.
const maxSnippetWords = 120

// maxSnippetRunes is a hard backstop on snippet length. The word cap alone lets a
// single pathological token (a base64 blob, minified JSON — plausible in an
// externally-synced .md) through unbounded, since it counts as one "word"; this
// clamps such input. It sits well above any normal ~120-word block so real prose
// is never clipped by it. Dedup is unaffected — signalKey hashes the full text.
const maxSnippetRunes = 2000

// minSignalRunes filters out trivially short blocks (a bare list marker, a stray
// word) that carry no durable signal. Measured in runes so multibyte text isn't
// unfairly truncated.
const minSignalRunes = 16

// DreamSignal is one raw sighting of a potential durable fact, emitted by a
// source scanner. Many signals collapse into one DreamCandidate when their
// normalized text matches.
type DreamSignal struct {
	// Source is the originating scanner (journal|context|dispatch).
	Source string
	// Title is an optional human label (a journal date, a dispatch label).
	Title string
	// Text is the raw signal text; it is normalized for the dedup key and
	// word-bounded for the stored snippet.
	Text string
	// SourceRef is a resolvable provenance pointer the promote phase grounds
	// against (a root-absolute note path, "dispatch:<id>", "context:<workspace>").
	SourceRef string
	// Context is the distinct-context label (a workspace id, a journal date) used
	// to measure recurrence-across-contexts, the strongest durability signal.
	Context string
	// Seen is an ISO timestamp/date for recency (the journal date, a dispatch's
	// reported-at). May be empty.
	Seen string
}

// DreamCandidate is one deduplicated harvest signal: a unit of source material
// the promote phase may later distill into durable memory. Candidates are keyed
// by normalized content, so the same fact seen in multiple places accumulates
// Sources/Contexts and an Occurrences count instead of duplicating.
type DreamCandidate struct {
	// SignalKey is the content hash that identifies this candidate (the dedup key).
	SignalKey string `json:"signal_key"`
	// Source is the scanner that first surfaced this candidate.
	Source string `json:"source"`
	// Title is an optional human label carried from the first sighting that had one.
	Title string `json:"title,omitempty"`
	// Snippet is the word-bounded representative text.
	Snippet string `json:"snippet"`
	// Sources holds the distinct resolvable provenance refs (sorted, unique).
	Sources []string `json:"sources"`
	// Contexts holds the distinct context labels (sorted, unique).
	Contexts []string `json:"contexts"`
	// Occurrences == len(Sources): how many distinct source units carry this fact.
	Occurrences int `json:"occurrences"`
	// FirstSeen / LastSeen bound the candidate's sightings (ISO; may be empty).
	FirstSeen string `json:"first_seen,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
}

// DistinctContexts reports how many distinct contexts carry this candidate — the
// promote phase's recurrence gate ("appears in >= N distinct contexts").
func (c DreamCandidate) DistinctContexts() int { return len(c.Contexts) }

// DreamCandidateSet accumulates signals and merges them by normalized content.
// Not safe for concurrent use; the harvest scans sources serially.
type DreamCandidateSet struct {
	byKey map[string]*DreamCandidate
	order []string
}

// NewDreamCandidateSet returns an empty set ready to Add signals.
func NewDreamCandidateSet() *DreamCandidateSet {
	return &DreamCandidateSet{byKey: make(map[string]*DreamCandidate)}
}

// LoadDreamCandidateSet rebuilds a set from previously persisted candidates so a
// run can merge fresh signals into the accumulated state. Re-adding a signal whose
// SourceRef is already recorded is idempotent, so re-harvesting the same source
// into a loaded set never inflates a candidate's occurrence count. Candidates with
// no SignalKey or a duplicate key (corrupt state) are skipped.
func LoadDreamCandidateSet(cands []DreamCandidate) *DreamCandidateSet {
	s := NewDreamCandidateSet()
	for i := range cands {
		key := strings.TrimSpace(cands[i].SignalKey)
		if key == "" {
			continue
		}
		if _, ok := s.byKey[key]; ok {
			continue
		}
		c := cands[i]
		s.byKey[key] = &c
		s.order = append(s.order, key)
	}
	return s
}

// Add merges one signal into the set. A signal whose text is blank (after
// normalization) is ignored. Re-adding the same SourceRef is idempotent, so a
// scanner may safely re-scan a source without inflating Occurrences.
func (s *DreamCandidateSet) Add(sig DreamSignal) {
	text := strings.TrimSpace(sig.Text)
	if text == "" {
		return
	}
	key := signalKey(text)
	if key == "" {
		return
	}
	c := s.byKey[key]
	if c == nil {
		c = &DreamCandidate{
			SignalKey: key,
			Source:    sig.Source,
			Title:     strings.TrimSpace(sig.Title),
			Snippet:   boundedSnippet(text),
			FirstSeen: sig.Seen,
			LastSeen:  sig.Seen,
		}
		s.byKey[key] = c
		s.order = append(s.order, key)
	}
	if ref := strings.TrimSpace(sig.SourceRef); ref != "" {
		c.Sources = insertUniqueSorted(c.Sources, ref)
		c.Occurrences = len(c.Sources)
	}
	if ctx := strings.TrimSpace(sig.Context); ctx != "" {
		c.Contexts = insertUniqueSorted(c.Contexts, ctx)
	}
	if c.Title == "" {
		c.Title = strings.TrimSpace(sig.Title)
	}
	if sig.Seen != "" {
		// Compare chronologically, not lexically: a candidate merges sightings in
		// mixed formats — date-only journal dates, UTC context timestamps, and
		// dispatch timestamps that may carry a local UTC offset — and a raw string
		// compare of an offset-bearing timestamp against a Z timestamp is wrong.
		if c.FirstSeen == "" || seenBefore(sig.Seen, c.FirstSeen) {
			c.FirstSeen = sig.Seen
		}
		if c.LastSeen == "" || seenBefore(c.LastSeen, sig.Seen) {
			c.LastSeen = sig.Seen
		}
	}
}

// seenBefore reports whether sighting timestamp x is chronologically before y.
// It parses RFC3339(Nano) and date-only ("2006-01-02", treated as UTC midnight)
// forms; if either value is unparseable it falls back to a lexical compare so a
// surprising format never panics or silently swaps the window.
func seenBefore(x, y string) bool {
	tx, okx := parseSeen(x)
	ty, oky := parseSeen(y)
	if okx && oky {
		return tx.Before(ty)
	}
	return x < y
}

func parseSeen(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Candidates returns the merged candidates ordered by durability signal: most
// occurrences first, then most distinct contexts, then signal key for a stable
// total order.
func (s *DreamCandidateSet) Candidates() []DreamCandidate {
	out := make([]DreamCandidate, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, *s.byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences > out[j].Occurrences
		}
		if len(out[i].Contexts) != len(out[j].Contexts) {
			return len(out[i].Contexts) > len(out[j].Contexts)
		}
		return out[i].SignalKey < out[j].SignalKey
	})
	return out
}

// Len reports how many distinct candidates the set holds.
func (s *DreamCandidateSet) Len() int { return len(s.order) }

// ExtractJournalSignals splits a journal note's body into block-level signals.
// Each blank-line-delimited block becomes one signal (heading-only blocks and
// blocks below the minimum length are skipped). The note path is the source ref
// at file granularity — a fact repeated within one day counts once; the same
// fact on a different day is a second, recurring sighting — and the date is both
// the seen-timestamp and the distinct-context label, so recurrence is measured
// across days.
func ExtractJournalSignals(rel, dateISO, body string) []DreamSignal {
	var out []DreamSignal
	ref := rel
	if !strings.HasPrefix(ref, "/") {
		ref = "/" + ref
	}
	context := SignalSourceJournal
	if dateISO != "" {
		context = SignalSourceJournal + ":" + dateISO
	}
	for _, block := range splitBlocks(body) {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" || isHeadingOnly(trimmed) {
			continue
		}
		if len([]rune(trimmed)) < minSignalRunes {
			continue
		}
		out = append(out, DreamSignal{
			Source:    SignalSourceJournal,
			Title:     dateISO,
			Text:      trimmed,
			SourceRef: ref,
			Context:   context,
			Seen:      dateISO,
		})
	}
	return out
}

// JournalDateFromPath extracts the YYYY-MM-DD label from a journal note path
// ("journal/2026-06-13.md" -> "2026-06-13"). A non-date basename yields an empty
// label (the blocks still harvest; they just carry no date context).
func JournalDateFromPath(rel string) string {
	base := strings.TrimSuffix(path.Base(rel), ".md")
	if journalDateRE.MatchString(base) {
		return base
	}
	return ""
}

// signalKey is the dedup key: the hash of the normalized text. Empty when the
// text normalizes to nothing.
func signalKey(text string) string {
	norm := normalizeSignal(text)
	if norm == "" {
		return ""
	}
	return Hash([]byte(norm))
}

// normalizeSignal canonicalizes signal text so cosmetically-different sightings
// of the same fact share a key: per line it strips leading list/quote/heading
// markers, lowercases, then collapses all whitespace to single spaces and trims
// trailing sentence punctuation (so "X." and "X" are one fact). It is
// deliberately conservative — it never drops interior words — so distinct facts
// stay distinct.
func normalizeSignal(text string) string {
	var parts []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-*>#+ \t")
		if line == "" {
			continue
		}
		parts = append(parts, strings.ToLower(line))
	}
	collapsed := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	return strings.TrimRight(collapsed, " .,:;!?")
}

// boundedSnippet returns text bounded to maxSnippetWords AND maxSnippetRunes,
// appending an ellipsis when truncated by either bound. Whitespace runs are
// collapsed to keep the snippet compact.
func boundedSnippet(text string) string {
	words := strings.Fields(text)
	truncated := len(words) > maxSnippetWords
	if truncated {
		words = words[:maxSnippetWords]
	}
	out := strings.Join(words, " ")
	if r := []rune(out); len(r) > maxSnippetRunes {
		out = strings.TrimRight(string(r[:maxSnippetRunes]), " ")
		truncated = true
	}
	if truncated {
		out += " …"
	}
	return out
}

// splitBlocks splits markdown text into blank-line-delimited blocks.
func splitBlocks(body string) []string {
	lines := strings.Split(body, "\n")
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
}

// isHeadingOnly reports whether a block is a single markdown heading line (the
// journal's "# date" title or a lone section heading) with no body.
func isHeadingOnly(block string) bool {
	if strings.Contains(block, "\n") {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(block), "#")
}

// insertUniqueSorted inserts v into the sorted slice s if absent, keeping it
// sorted and deduplicated.
func insertUniqueSorted(s []string, v string) []string {
	i := sort.SearchStrings(s, v)
	if i < len(s) && s[i] == v {
		return s
	}
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}
