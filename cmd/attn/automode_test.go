package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// The pattern an agent types is the whole payload, and it routinely contains
// spaces and wildcards. These pin that flags never eat part of it.

func TestAutoModeStripFlagsKeepsThePattern(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"git", "push", "origin*"}, "git push origin*"},
		{[]string{"--json", "git", "push", "origin*"}, "git push origin*"},
		{[]string{"git", "push", "origin*", "--json"}, "git push origin*"},
	}
	for _, tc := range cases {
		got := strings.Join(stripFlags(tc.args), " ")
		if got != tc.want {
			t.Errorf("stripFlags(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// --limit takes a value, so its argument must not survive as a positional and
// be read as part of a pattern.
func TestAutoModeStripFlagsDropsAValuedFlagsArgument(t *testing.T) {
	if got := stripFlags([]string{"--limit", "5"}); len(got) != 0 {
		t.Fatalf("stripFlags dropped %v, want nothing left", got)
	}
	if got := stripFlags([]string{"--limit=5", "extra"}); len(got) != 1 || got[0] != "extra" {
		t.Fatalf("stripFlags = %v, want only the positional", got)
	}
}

func TestAutoModeTakeStringFlagReadsBothForms(t *testing.T) {
	for _, args := range [][]string{{"--limit", "5"}, {"--limit=5"}} {
		value, ok := takeStringFlag(args, "--limit")
		if !ok || value != "5" {
			t.Errorf("takeStringFlag(%v) = %q ok=%t", args, value, ok)
		}
	}
	if _, ok := takeStringFlag([]string{"--json"}, "--limit"); ok {
		t.Error("takeStringFlag found a flag that is not there")
	}
}

// The denials feed is what a person or an agent reads to see what auto mode
// refused, so every column has to be there: when, which session, who decided,
// the blocked call, and the reason.
func TestAutoModeDenialsRenderEveryColumn(t *testing.T) {
	var out bytes.Buffer
	writeAutoModeDenials(&out, []protocol.AutoModeDenialInfo{{
		ID:        2,
		SessionID: "pi-1",
		Tool:      "bash",
		Signature: "bash: curl https://example.com",
		Reason:    "the user never asked to reach that host",
		Rule:      "classifier-2a",
		CreatedAt: "2026-08-17T10:00:00Z",
	}}, "")
	rendered := out.String()
	for _, want := range []string{
		"2026-08-17T10:00:00Z", "pi-1", "classifier-2a",
		"bash: curl https://example.com", "the user never asked to reach that host",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("denials output is missing %q:\n%s", want, rendered)
		}
	}
}

func TestAutoModeDenialsSayWhenThereAreNone(t *testing.T) {
	var out bytes.Buffer
	writeAutoModeDenials(&out, nil, "")
	if !strings.Contains(out.String(), "no denials recorded") {
		t.Errorf("empty feed rendered as %q", out.String())
	}
}

// A feed that clipped and did not say so reads as a whole episode, which is the
// failure the ledger exists to end.
func TestAutoModeDenialsNameWhatTheLedgerLost(t *testing.T) {
	var out bytes.Buffer
	writeAutoModeDenials(&out, nil, "3 older denials were dropped when the local ledger rotated")
	rendered := out.String()
	if !strings.Contains(rendered, "no denials recorded") {
		t.Errorf("the feed itself went missing: %q", rendered)
	}
	if !strings.Contains(rendered, "note: 3 older denials were dropped") {
		t.Errorf("the note is missing: %q", rendered)
	}
}
