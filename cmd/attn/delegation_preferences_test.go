package main

import (
	"github.com/victorarias/attn/internal/protocol"
	"testing"
)

func TestDelegateRoleRequestOptions(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "source-session")
	parsed, err := parseDelegateArgs([]string{"--brief", "Build this", "--role", "build", "--choice", "hard", "--preferences-revision", "9", "--model", "custom", "--effort", "high"})
	if err != nil {
		t.Fatal(err)
	}
	o := parsed.options
	if o.Role != "build" || o.Choice != "hard" || protocol.Deref(o.PreferencesRevision) != 9 || protocol.Deref(o.ModelOverride) != "custom" || protocol.Deref(o.EffortOverride) != "high" {
		t.Fatalf("%+v", o)
	}
	parsed, err = parseDelegateArgs([]string{"--brief", "Task", "--fallback", "--effort", "default"})
	if err != nil || !parsed.options.Fallback || parsed.options.EffortOverride == nil || *parsed.options.EffortOverride != "" {
		t.Fatalf("%+v %v", parsed, err)
	}
	for _, flags := range [][]string{{"--role", "build", "--fallback"}, {"--choice", "hard", "--model", "x"}, {"--model", "x", "--provider", "p"}, {"--model", "x", "--preferences-revision", "3"}} {
		if _, err := parseDelegateArgs(append([]string{"--brief", "Task"}, flags...)); err == nil {
			t.Fatalf("accepted %v", flags)
		}
	}
}
