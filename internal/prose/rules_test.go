package prose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fires reports which rules object to a snippet, so each case below states the
// prose and the verdict and nothing else.
func fires(t *testing.T, rule, source string) []Finding {
	t.Helper()
	findings, err := Check("t.md", []byte(source), Options{Only: []string{rule}})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return findings
}

func TestRulePrecision(t *testing.T) {
	for _, tc := range []struct {
		rule string
		// caught is prose the rule must object to; spared is prose it must
		// not, and every entry there came from the corpus or from a false
		// positive the rule used to have.
		caught, spared []string
	}{
		{
			rule: "hidden-verb",
			caught: []string{
				"The reviewer will give consideration to the whole diff.",
				"The daemon performs an evaluation of every candidate before it decides.",
			},
			spared: []string{
				"The opener takes an extension and resolves it.",
				"The schema reached migration 51 last week.",
				"The platform gives extensions a place to run.",
				"The daemon makes a snapshot every time the list changes.",
			},
		},
		{
			rule: "passive-no-actor",
			caught: []string{
				"The profile must be cleaned up afterwards.",
				"The threshold should not be raised without a measurement.",
			},
			spared: []string{
				"The archive is verified against the lock before anything links it.",
				"The consumer's cursor was advanced by the projection.",
				"The migration must be applied by whoever runs the release.",
				"Everything here is already done.",
				"The image can be stored when the limit allows it.",
			},
		},
		{
			rule: "expletive-opener",
			caught: []string{
				"There are three cases the classifier has to separate.",
				"It is the daemon that decides, not the worker.",
			},
			spared: []string{
				"It works the same way on every platform.",
				"There the reader finally learns who writes the record.",
				"The daemon is there before any client connects.",
			},
		},
		{
			rule: "noun-string",
			caught: []string{
				"The subsystem lifecycle coordination layer configuration state machine handler runs first.",
			},
			spared: []string{
				"The protocol version bump lands with the generated types.",
				"The states are launching, working, pending, waiting, idle, unknown and scheduled.",
				"# Session state machine transition handler receipt",
			},
		},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			for _, source := range tc.caught {
				if len(fires(t, tc.rule, source)) == 0 {
					t.Errorf("missed: %s", source)
				}
			}
			for _, source := range tc.spared {
				if found := fires(t, tc.rule, source); len(found) > 0 {
					t.Errorf("false positive on %q: %s", source, found[0].Objection)
				}
			}
		})
	}
}

// TestIdeaDensityBarelyMovesUnderChopping is the property the metric exists
// for, stated as what was actually measured rather than as the stronger claim
// the plan started with.
//
// Splitting a sentence does lower its density a little, because the
// conjunctions the split deletes are themselves propositions. Across three
// realistic rewrites the cost is 0.03 to 0.09, against a 0.17 gap between what
// accepted prose reaches (p99 0.68) and the tripwire (0.85) — so chopping
// cannot carry a sentence under the line, where halving its length obviously
// would. And what chopping removes is exactly what lost-thread and
// staccato-run measure.
func TestIdeaDensityBarelyMovesUnderChopping(t *testing.T) {
	// Bound at 0.12, comfortably past the 0.09 worst case measured below and
	// well under the 0.17 headroom the threshold has. A rewrite that moves
	// density further than this means the count has started following
	// sentence boundaries, which is the failure the whole metric exists to
	// avoid.
	const bound = 0.12
	for _, pair := range [][2]string{
		{
			"The daemon reads the record and writes it back through the projection, " +
				"because the hub is ephemeral and cannot hold the state itself.",
			"The daemon reads the record. It writes the record back through the projection. " +
				"The hub is ephemeral. It cannot hold the state itself.",
		},
		{
			"Restore is server-authoritative, so the daemon worker serializes the terminal " +
				"and the attach serves its dump as the sole payload, which means a " +
				"snapshot-less attach keeps whatever the client already has.",
			"Restore is server-authoritative. The daemon worker serializes the terminal. " +
				"The attach serves its dump as the sole payload. A snapshot-less attach " +
				"keeps whatever the client already has.",
		},
		{
			"Retention trims past the age window but never past an enabled consumer's " +
				"cursor, while disabled consumers do not pin the log and resume at head " +
				"with a logged gap.",
			"Retention trims past the age window. It never trims past an enabled " +
				"consumer's cursor. Disabled consumers do not pin the log. They resume " +
				"at head with a logged gap.",
		},
	} {
		whole, chopped := documentDensity(t, pair[0]), documentDensity(t, pair[1])
		if delta := whole - chopped; delta > bound || delta < -bound {
			t.Errorf("chopping moved density %.3f → %.3f (%+.3f, bound %.2f):\n    %s", whole, chopped, chopped-whole, bound, pair[0])
		}
	}
}

func documentDensity(t *testing.T, source string) float64 {
	t.Helper()
	doc, err := Parse("t.md", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	props, words := 0, 0
	for _, sent := range doc.Sentences {
		w := sent.words()
		words += len(w)
		props += countPropositions(w)
	}
	if words == 0 {
		t.Fatal("no words")
	}
	return float64(props) / float64(words)
}

// TestAuxiliaryIsOnePropositionWithItsVerb pins the CPIDR adjustment that
// keeps "has verified" from counting twice.
func TestAuxiliaryIsOnePropositionWithItsVerb(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   int
	}{
		{"The runner has verified the archive.", 1},
		{"The runner verified the archive.", 1},
		{"The archive is small.", 2}, // copula plus the adjective it asserts
	} {
		doc, err := Parse("t.md", []byte(tc.source))
		if err != nil {
			t.Fatal(err)
		}
		if got := countPropositions(doc.Sentences[0].words()); got != tc.want {
			t.Errorf("%q counted %d propositions, want %d", tc.source, got, tc.want)
		}
	}
}

func TestVocabulary(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(rejectFile, "# a comment\nutilize\nin order to\n")
	write(acceptFile, "# the way out\nutilize the\n")

	vocab, err := LoadVocabulary(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	findings, err := Check("t.md", []byte("We utilize a queue in order to hold the work, and we utilize the queue twice.\n"),
		Options{Vocabulary: vocab, Only: []string{"reject-word"}})
	if err != nil {
		t.Fatal(err)
	}
	var spans []string
	for _, f := range findings {
		spans = append(spans, f.Span)
	}
	// "utilize the" is accepted, so only the first "utilize" and the multi-word
	// entry survive.
	if got := strings.Join(spans, "|"); got != "utilize|in order to" {
		t.Errorf("findings were %q, want %q", got, "utilize|in order to")
	}
}

func TestMissingVocabularyIsNotAnError(t *testing.T) {
	vocab, err := LoadVocabulary(filepath.Join(t.TempDir(), "nothing-here"))
	if err != nil {
		t.Fatalf("a missing vocabulary directory must not be an error: %v", err)
	}
	if len(vocab.reject) != 0 {
		t.Errorf("a missing vocabulary somehow loaded %d entries", len(vocab.reject))
	}
}

func TestBadVocabularyEntryNamesItsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, rejectFile), []byte("this is ([ not a regex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadVocabulary(dir)
	if err == nil {
		t.Fatal("a malformed entry loaded silently")
	}
	if !strings.Contains(err.Error(), rejectFile) {
		t.Errorf("the error does not name the file to fix: %v", err)
	}
}

// TestRuleNamesAreStable: the names are the JSON contract and the --rule flag,
// so a rename is a breaking change and should read as one in the diff.
func TestRuleNamesAreStable(t *testing.T) {
	want := []string{
		"hidden-verb", "noun-string", "passive-no-actor", "expletive-opener",
		"reject-word", "long-sentence", "idea-density", "staccato-run",
		"flat-rhythm", "lost-thread",
	}
	got := RuleNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rule names are now %v, want %v", got, want)
	}
}

func TestEmptyAndBinaryInputDoNotPanic(t *testing.T) {
	for _, source := range []string{"", "\n\n\n", "\x00\x01\x02", "# ", strings.Repeat("a ", 5000)} {
		if _, err := Check("t.md", []byte(source), Options{}); err != nil {
			t.Errorf("Check(%q) failed: %v", truncate(source), err)
		}
	}
}

func truncate(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}
