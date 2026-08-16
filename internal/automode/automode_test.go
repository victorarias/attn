package automode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsBroadPatternMatchesTheTypeScriptRule(t *testing.T) {
	broad := []string{"*", "**", "?", " * ", "* ?", "", "\t*"}
	for _, pattern := range broad {
		if !IsBroadPattern(pattern) {
			t.Errorf("IsBroadPattern(%q) = false, want true", pattern)
		}
	}
	narrow := []string{"git push*", "rm -rf /*", "bash curl*", "a", "*.sh"}
	for _, pattern := range narrow {
		if IsBroadPattern(pattern) {
			t.Errorf("IsBroadPattern(%q) = true, want false", pattern)
		}
	}
}

func TestValidateProposalRefusesABroadAllow(t *testing.T) {
	err := ValidateProposal(KindAllow, "", "*")
	if err == nil {
		t.Fatal("a broad allow pattern was accepted")
	}
	if !strings.Contains(err.Error(), "broad allow pattern") {
		t.Fatalf("error does not name the limit: %v", err)
	}
}

// A blanket hard-deny refuses everything, which is the safe direction — only
// allow is guarded.
func TestValidateProposalAcceptsABroadDeny(t *testing.T) {
	if err := ValidateProposal(KindDeny, "", "*"); err != nil {
		t.Fatalf("broad deny refused: %v", err)
	}
}

func TestValidateProposalChecksModelShape(t *testing.T) {
	if err := ValidateProposal(KindModel, TargetClassifier, "opencode-go/glm-5.3"); err != nil {
		t.Fatalf("valid model refused: %v", err)
	}
	for _, tc := range []struct{ target, value string }{
		{"", "opencode-go/glm-5.3"},
		{"main", "opencode-go/glm-5.3"},
		{TargetClassifier, "glm-5.3"},
		{TargetEscalation, ""},
	} {
		if err := ValidateProposal(KindModel, tc.target, tc.value); err == nil {
			t.Errorf("ValidateProposal(model, %q, %q) was accepted", tc.target, tc.value)
		}
	}
}

func TestValidateProposalRejectsUnknownKinds(t *testing.T) {
	if err := ValidateProposal("promote", "", "anything"); err == nil {
		t.Fatal("unknown proposal kind was accepted")
	}
}

// The JSON here IS plugins/attn-pi/automode/config.ts's RawAutoModeConfig. A
// field renamed on one side without the other silently drops to the pi-side
// default, so the names are pinned by a test rather than by memory.
func TestConfigMarshalsIntoThePiSideShape(t *testing.T) {
	raw, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{
		"enabled_default", "environment", "allow", "hard_deny",
		"classifier_model", "escalation_model",
	}
	if len(fields) != len(want) {
		t.Fatalf("config has %d fields, want exactly %d: %s", len(fields), len(want), raw)
	}
	for _, name := range want {
		if _, ok := fields[name]; !ok {
			t.Errorf("config is missing %q: %s", name, raw)
		}
	}
}
