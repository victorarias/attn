package main

import (
	"strings"
	"testing"
)

// The guide is where the craft moved to when the launch-time block was cut back
// to rules. It has to actually hold that craft, or the pointer is a dead end.
func TestSeedGuideCarriesTheCraft(t *testing.T) {
	var b strings.Builder
	writeSeedGuide(&b)
	guide := b.String()
	for _, section := range []string{
		"WRITING A BODY",
		"WHAT THE DELIVERABLE CHANGES",
		"WHERE A SEED BELONGS",
		"EDIT, REPLANT, OR PLANT AGAIN",
		"A SEED WHOSE TENDER IS GONE",
		"PICKING UP FURTHER WORK",
		"ARTIFACTS",
		"HANDOFFS AND STEERING",
	} {
		if !strings.Contains(guide, section) {
			t.Fatalf("guide is missing %q", section)
		}
	}
}

// `attn seed --help` is how an agent finds the guide at all.
func TestSeedHelpNamesTheGuide(t *testing.T) {
	var b strings.Builder
	writeSeedHelp(&b)
	if !strings.Contains(b.String(), "guide") {
		t.Fatalf("seed help does not name the guide:\n%s", b.String())
	}
}
