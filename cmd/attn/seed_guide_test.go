package main

import (
	"strings"
	"testing"
)

// The guide is where the craft lives; the references point here instead of
// carrying it. It has to actually hold that craft, or the pointer is a dead end.
func TestSeedGuideCarriesTheCraft(t *testing.T) {
	var b strings.Builder
	writeSeedGuide(&b)
	guide := b.String()
	for _, section := range []string{
		"WRITING A BODY",
		"DELIVERABLE TYPES BEND THE SHAPE",
		"ARTIFACTS",
		"HANDOFFS AND STEERING",
	} {
		if !strings.Contains(guide, section) {
			t.Fatalf("guide is missing %q", section)
		}
	}
	for _, phrase := range []string{
		"zero warm context",
		"A verification contract.",
		"Evidence decides when to harvest",
		"attn seed attach",
		"--handoff",
		"attn agent msg",
	} {
		if !strings.Contains(guide, phrase) {
			t.Fatalf("guide does not carry %q:\n%s", phrase, guide)
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
