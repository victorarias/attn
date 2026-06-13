package notebook

import (
	"strings"
	"testing"
)

// The same fact written two cosmetically-different ways (bullet marker, case,
// whitespace) collapses into one candidate. Each distinct source unit counts
// once, so occurrences and distinct contexts both reach 2.
func TestDreamCandidateSetMergesCosmeticVariants(t *testing.T) {
	set := NewDreamCandidateSet()
	set.Add(DreamSignal{
		Source: SignalSourceContext, Text: "- Daemon owns every notebook write.",
		SourceRef: "context:ws-a", Context: "workspace:ws-a", Seen: "2026-06-10",
	})
	set.Add(DreamSignal{
		Source: SignalSourceContext, Text: "* daemon owns   every notebook write",
		SourceRef: "context:ws-b", Context: "workspace:ws-b", Seen: "2026-06-12",
	})

	got := set.Candidates()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 (cosmetic variants merge); got %+v", len(got), got)
	}
	c := got[0]
	if c.Occurrences != 2 {
		t.Fatalf("occurrences = %d, want 2", c.Occurrences)
	}
	if c.DistinctContexts() != 2 {
		t.Fatalf("distinct contexts = %d, want 2", c.DistinctContexts())
	}
	if c.FirstSeen != "2026-06-10" || c.LastSeen != "2026-06-12" {
		t.Fatalf("seen window = [%s, %s], want [2026-06-10, 2026-06-12]", c.FirstSeen, c.LastSeen)
	}
	if len(c.Sources) != 2 {
		t.Fatalf("sources = %v, want 2 distinct refs", c.Sources)
	}
}

// Re-adding the same source ref is idempotent: a scanner can re-scan a source
// without inflating occurrences (the merge is keyed by source ref, set-union).
func TestDreamCandidateSetReAddIsIdempotent(t *testing.T) {
	set := NewDreamCandidateSet()
	sig := DreamSignal{
		Source: SignalSourceJournal, Text: "A durable decision worth remembering.",
		SourceRef: "/journal/2026-06-10.md", Context: "journal:2026-06-10", Seen: "2026-06-10",
	}
	set.Add(sig)
	set.Add(sig)

	got := set.Candidates()
	if len(got) != 1 || got[0].Occurrences != 1 {
		t.Fatalf("candidates = %+v, want one candidate with occurrences=1", got)
	}
}

// Genuinely different facts stay separate — normalization is conservative and
// never drops words.
func TestDreamCandidateSetKeepsDistinctFactsSeparate(t *testing.T) {
	set := NewDreamCandidateSet()
	set.Add(DreamSignal{Source: SignalSourceJournal, Text: "Notebook is filesystem-canonical.", SourceRef: "/journal/a.md", Context: "journal:a"})
	set.Add(DreamSignal{Source: SignalSourceJournal, Text: "Dreaming is off by default.", SourceRef: "/journal/b.md", Context: "journal:b"})
	if got := set.Candidates(); len(got) != 2 {
		t.Fatalf("candidates = %d, want 2 distinct facts", len(got))
	}
}

// A blank/marker-only signal carries no durable content and is dropped.
func TestDreamCandidateSetDropsEmptySignals(t *testing.T) {
	set := NewDreamCandidateSet()
	set.Add(DreamSignal{Source: SignalSourceJournal, Text: "   \n- \n#", SourceRef: "/journal/a.md"})
	if got := set.Candidates(); len(got) != 0 {
		t.Fatalf("candidates = %d, want 0 for an empty signal", len(got))
	}
}

// Candidates are ordered by durability signal: more occurrences first, then more
// distinct contexts.
func TestDreamCandidatesOrderedByDurabilitySignal(t *testing.T) {
	set := NewDreamCandidateSet()
	// One occurrence.
	set.Add(DreamSignal{Source: SignalSourceJournal, Text: "rare fact", SourceRef: "/journal/a.md", Context: "journal:a"})
	// Three occurrences across three contexts.
	for i, ref := range []string{"context:ws-a", "context:ws-b", "context:ws-c"} {
		_ = i
		set.Add(DreamSignal{Source: SignalSourceContext, Text: "recurring fact", SourceRef: ref, Context: "workspace:" + ref})
	}
	got := set.Candidates()
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2", len(got))
	}
	if got[0].Occurrences != 3 {
		t.Fatalf("first candidate occurrences = %d, want the 3× recurring fact first", got[0].Occurrences)
	}
}

// Journal occurrences are file-granular: the same fact repeated within ONE day's
// note counts once (one source unit), but the same fact on TWO different days is a
// genuine recurrence (occurrences=2, two distinct date contexts). This is the
// core durability invariant the promote phase will gate on.
func TestExtractJournalSignalsFileGranularRecurrence(t *testing.T) {
	const fact = "Daemon owns every notebook write through one in-process store."

	// Same fact twice within a single day's note -> one source unit.
	sameDay := "# 2026-06-10\n\n" + fact + "\n\n" + fact + "\n"
	set := NewDreamCandidateSet()
	for _, s := range ExtractJournalSignals("journal/2026-06-10.md", "2026-06-10", sameDay) {
		set.Add(s)
	}
	got := set.Candidates()
	if len(got) != 1 || got[0].Occurrences != 1 {
		t.Fatalf("within-file repeat = %+v, want one candidate with occurrences=1 (file-granular)", got)
	}

	// Same fact on a second day -> a recurring candidate across two contexts.
	for _, s := range ExtractJournalSignals("journal/2026-06-11.md", "2026-06-11", "# 2026-06-11\n\n"+fact+"\n") {
		set.Add(s)
	}
	got = set.Candidates()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 (same fact merges across days)", len(got))
	}
	if got[0].Occurrences != 2 || got[0].DistinctContexts() != 2 {
		t.Fatalf("cross-day recurrence occurrences=%d contexts=%d, want 2/2", got[0].Occurrences, got[0].DistinctContexts())
	}
}

// With equal occurrences, the candidate seen in more distinct contexts ranks
// first (the recurrence-breadth tiebreaker).
func TestDreamCandidatesDistinctContextTiebreaker(t *testing.T) {
	set := NewDreamCandidateSet()
	// "narrow": 2 occurrences, but both from the same context label.
	set.Add(DreamSignal{Source: SignalSourceContext, Text: "narrow fact", SourceRef: "context:ws-a", Context: "workspace:ws-a"})
	set.Add(DreamSignal{Source: SignalSourceContext, Text: "narrow fact", SourceRef: "context:ws-a-mirror", Context: "workspace:ws-a"})
	// "broad": 2 occurrences across two distinct contexts.
	set.Add(DreamSignal{Source: SignalSourceContext, Text: "broad fact", SourceRef: "context:ws-b", Context: "workspace:ws-b"})
	set.Add(DreamSignal{Source: SignalSourceContext, Text: "broad fact", SourceRef: "context:ws-c", Context: "workspace:ws-c"})

	got := set.Candidates()
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2", len(got))
	}
	if got[0].Occurrences != got[1].Occurrences {
		t.Fatalf("precondition: both candidates should have equal occurrences, got %d vs %d", got[0].Occurrences, got[1].Occurrences)
	}
	if got[0].DistinctContexts() != 2 {
		t.Fatalf("with equal occurrences the broader-context candidate must rank first; got %+v", got)
	}
}

// ExtractJournalSignals splits a journal body into block-level signals, skipping
// the heading-only title and blocks below the minimum length, and grounds each
// signal at the note path (root-absolute) with the date as the context label.
func TestExtractJournalSignals(t *testing.T) {
	body := "# 2026-06-10\n\n" +
		"We split the dreaming PR along the deterministic versus LLM seam to keep it reviewable.\n\n" +
		"Short.\n\n" +
		"## A lone heading\n\n" +
		"Harvest scans journals, context snapshots, and closed dispatches into candidates.\n"

	sigs := ExtractJournalSignals("journal/2026-06-10.md", "2026-06-10", body)
	if len(sigs) != 2 {
		t.Fatalf("signals = %d, want 2 (heading-only and too-short blocks skipped): %+v", len(sigs), sigs)
	}
	for _, s := range sigs {
		if s.SourceRef != "/journal/2026-06-10.md" {
			t.Fatalf("source ref = %q, want root-absolute note path", s.SourceRef)
		}
		if s.Context != "journal:2026-06-10" {
			t.Fatalf("context = %q, want journal:2026-06-10", s.Context)
		}
		if s.Seen != "2026-06-10" {
			t.Fatalf("seen = %q, want the journal date", s.Seen)
		}
	}
}

func TestJournalDateFromPath(t *testing.T) {
	cases := map[string]string{
		"journal/2026-06-13.md":  "2026-06-13",
		"/journal/2026-01-01.md": "2026-01-01",
		"journal/notes.md":       "",
		"journal/2026-6-1.md":    "",
	}
	for in, want := range cases {
		if got := JournalDateFromPath(in); got != want {
			t.Fatalf("JournalDateFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The seen-window is chronological, not lexical: a candidate merging a date-only
// journal sighting, a UTC context sighting, and a dispatch sighting that carries a
// local UTC offset must report the truly-earliest and truly-latest sightings. A
// raw string compare would mis-pick the offset-bearing one (it sorts below a Z
// timestamp it actually post-dates).
func TestDreamCandidateSeenWindowIsChronological(t *testing.T) {
	set := NewDreamCandidateSet()
	const fact = "shared durable fact"
	// 08:00Z added first so it seeds both bounds; the offset sighting is 09:00Z
	// (chronologically latest) but sorts lexically below "…08:00:00Z".
	set.Add(DreamSignal{Source: SignalSourceContext, Text: fact, SourceRef: "context:a", Context: "a", Seen: "2026-06-10T08:00:00Z"})
	set.Add(DreamSignal{Source: SignalSourceJournal, Text: fact, SourceRef: "/journal/x.md", Context: "j", Seen: "2026-06-10"})
	set.Add(DreamSignal{Source: SignalSourceDispatch, Text: fact, SourceRef: "dispatch:1", Context: "d", Seen: "2026-06-10T02:00:00-07:00"})

	got := set.Candidates()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].FirstSeen != "2026-06-10" {
		t.Fatalf("FirstSeen = %q, want the date-only midnight (chronologically earliest)", got[0].FirstSeen)
	}
	if got[0].LastSeen != "2026-06-10T02:00:00-07:00" {
		t.Fatalf("LastSeen = %q, want the offset sighting (09:00Z, chronologically latest)", got[0].LastSeen)
	}
}

func TestBoundedSnippetClampsHugeSingleToken(t *testing.T) {
	huge := strings.Repeat("x", 500_000) // one "word", far over the rune cap
	got := boundedSnippet(huge)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a huge single token should be clamped with an ellipsis; got %d runes", len([]rune(got)))
	}
	// The clamped body (excluding the " …" suffix) must not exceed the rune cap.
	body := strings.TrimSuffix(got, " …")
	if n := len([]rune(body)); n > maxSnippetRunes {
		t.Fatalf("clamped snippet body = %d runes, want <= %d", n, maxSnippetRunes)
	}
}

func TestBoundedSnippetTruncatesAtWordBoundary(t *testing.T) {
	words := make([]string, maxSnippetWords+50)
	for i := range words {
		words[i] = "w"
	}
	long := strings.Join(words, " ")
	got := boundedSnippet(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected an ellipsis suffix on a truncated snippet; got %q", got)
	}
	// maxSnippetWords words + the ellipsis token.
	if n := len(strings.Fields(got)); n != maxSnippetWords+1 {
		t.Fatalf("snippet word count = %d, want %d + ellipsis", n, maxSnippetWords)
	}

	short := "just a few words"
	if got := boundedSnippet(short); got != short {
		t.Fatalf("short snippet = %q, want unchanged %q", got, short)
	}
}
