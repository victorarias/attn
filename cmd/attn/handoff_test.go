package main

import (
	"strings"
	"testing"
)

func TestParseHandoffArgs_TakesTheLetterAndFallsBackToTheSessionEnv(t *testing.T) {
	parsed, err := parseHandoffArgs([]string{"-m", "Dear next trellis,"}, "session-1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.note != "Dear next trellis," {
		t.Errorf("note = %q", parsed.note)
	}
	if parsed.session != "session-1" {
		t.Errorf("session = %q, want the ATTN_SESSION_ID fallback", parsed.session)
	}
}

func TestParseHandoffArgs_AnExplicitSessionWins(t *testing.T) {
	parsed, err := parseHandoffArgs([]string{"-m", "x", "--session", "session-2"}, "session-1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.session != "session-2" {
		t.Errorf("session = %q, want the explicit one", parsed.session)
	}
}

// The letter is the handoff, so an empty one is refused before it costs a round
// trip — and the refusal says how to pipe a long one in.
func TestParseHandoffArgs_ThereIsNoHandoffWithoutALetter(t *testing.T) {
	for _, args := range [][]string{{}, {"-m", "  "}} {
		_, err := parseHandoffArgs(args, "session-1")
		if err == nil {
			t.Fatalf("parse(%v) was accepted", args)
		}
		if !strings.Contains(err.Error(), "-m -") {
			t.Errorf("the refusal %q does not say how to pipe a letter in", err)
		}
	}
}

// A letter typed as a positional is the shell eating the quotes, not a member
// naming something — say which flag it belongs in rather than filing a fragment.
func TestParseHandoffArgs_APositionalIsRefusedByName(t *testing.T) {
	_, err := parseHandoffArgs([]string{"-m", "x", "trellis"}, "session-1")
	if err == nil {
		t.Fatal("a positional argument was accepted")
	}
	if !strings.Contains(err.Error(), "trellis") {
		t.Errorf("the refusal %q does not name what it did not understand", err)
	}
}
