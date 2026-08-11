package prose

// Thresholds carries every number the rules compare against.
//
// A limit without a measurement is a landmine, so each one is set past where
// the healthy corpus goes rather than at a round figure someone liked. The
// corpus is prose Victor has accepted — docs/plans and docs/vision on main —
// measured by `go test ./internal/prose -run TestCalibration -v`, which prints
// the distribution behind every number here. Re-run it when a threshold is
// argued with; do not exempt a document that trips a rule.
type Thresholds struct {
	// LongSentenceWords is the per-sentence length tripwire: generous on
	// purpose, because a blanket length rule is the documented push toward
	// staccato. Only sentences past anything in the healthy corpus trip it.
	LongSentenceWords int

	// IdeaDensityMax is propositions per word, CPIDR-style. It says
	// "overloaded" where length says only "long", and it survives chopping: a
	// split sentence keeps its propositions and its words, so the ratio holds.
	IdeaDensityMax float64
	// IdeaDensityMinWords keeps the density rule off short sentences, where a
	// single extra preposition swings the ratio and the reader is fine anyway.
	IdeaDensityMinWords int

	// NounStringLength is how many consecutive nouns read as a pile-up.
	NounStringLength int

	// StaccatoMaxWords and StaccatoRunLength are the anti-gaming pair for the
	// length rule: an agent that chops to satisfy LongSentenceWords lands
	// here instead.
	StaccatoMaxWords  int
	StaccatoRunLength int

	// FlatRhythmMinSentences is the shortest passage whose rhythm is worth
	// judging; FlatRhythmMinCV is the coefficient of variation of sentence
	// length below which every sentence is the same size.
	FlatRhythmMinSentences int
	FlatRhythmMinCV        float64

	// LostThreadRun is how many consecutive sentence pairs may share no
	// referent before the paragraph has stopped being about one thing.
	// Chopped prose loses its connectives and its shared subjects, so this
	// fires exactly where the length rule is being gamed.
	LostThreadRun int
}

// DefaultThresholds is the calibrated set, each number one step past what the
// healthy corpus reaches. Receipts — the distributions these came from, and
// what the labelled dense corpus did or did not separate — are in the plan
// doc's Decisions section and reproduced by TestCalibrationReceipts.
func DefaultThresholds() Thresholds {
	return Thresholds{
		LongSentenceWords:      120,  // healthy max 115
		IdeaDensityMax:         0.85, // healthy max 0.83, p99 0.68
		IdeaDensityMinWords:    14,
		NounStringLength:       7, // healthy max run 6, p99 4
		StaccatoMaxWords:       9, // healthy longest run of <=9-word sentences: 4
		StaccatoRunLength:      5,
		FlatRhythmMinSentences: 5,
		FlatRhythmMinCV:        0.16, // healthy min 0.17
		LostThreadRun:          6,    // healthy longest zero-overlap run 5
	}
}
