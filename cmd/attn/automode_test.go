package main

import (
	"strings"
	"testing"
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
