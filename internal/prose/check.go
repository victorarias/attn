package prose

// rule is one objection the deterministic layer knows how to raise.
type rule interface {
	name() string
	check(*Document, Thresholds) []Finding
}

// Options configure a check. The zero value is usable: default thresholds, no
// vocabulary.
type Options struct {
	Thresholds Thresholds
	Vocabulary *Vocabulary
	// Only, when non-empty, restricts the run to the named rules. It exists
	// for calibration, which measures one rule's distribution at a time.
	Only []string
}

// RuleNames lists every rule the deterministic layer can raise, in the order
// they run.
func RuleNames() []string {
	var names []string
	for _, r := range rules(nil) {
		names = append(names, r.name())
	}
	return names
}

func rules(vocab *Vocabulary) []rule {
	return []rule{
		hiddenVerbRule{},
		nounStringRule{},
		passiveRule{},
		expletiveRule{},
		rejectWordRule{vocab: vocab},
		longSentenceRule{},
		ideaDensityRule{},
		staccatoRule{},
		flatRhythmRule{},
		lostThreadRule{},
	}
}

// Check reads a Markdown source and reports what a writer should change.
//
// Nothing here reaches the network, spawns a process, or reads anything but
// the vocabulary the caller pointed it at.
func Check(file string, source []byte, opts Options) ([]Finding, error) {
	doc, err := Parse(file, source)
	if err != nil {
		return nil, err
	}
	return CheckDocument(doc, opts), nil
}

// CheckDocument runs the rules over an already-parsed document. Calibration
// parses a corpus once and sweeps thresholds over it.
func CheckDocument(doc *Document, opts Options) []Finding {
	if opts.Thresholds == (Thresholds{}) {
		opts.Thresholds = DefaultThresholds()
	}
	wanted := map[string]bool{}
	for _, name := range opts.Only {
		wanted[name] = true
	}

	var findings []Finding
	for _, r := range rules(opts.Vocabulary) {
		if len(wanted) > 0 && !wanted[r.name()] {
			continue
		}
		findings = append(findings, r.check(doc, opts.Thresholds)...)
	}
	sortFindings(findings)
	return findings
}
