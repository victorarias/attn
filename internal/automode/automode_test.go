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

func TestParseModelListReadsAnOrderedLayer(t *testing.T) {
	models, err := ParseModelList(" vendor/primary , vendor/fallback ")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 2 || models[0] != "vendor/primary" || models[1] != "vendor/fallback" {
		t.Fatalf("models = %v, want the pair in order", models)
	}
	if FormatModelList(models) != "vendor/primary,vendor/fallback" {
		t.Errorf("format = %q", FormatModelList(models))
	}
}

func TestParseModelListRefusesWhatNoLayerCouldRunOn(t *testing.T) {
	for name, value := range map[string]string{
		"nothing named":        "  ",
		"only separators":      ",,",
		"not a pair":           "glm-5.3",
		"one entry not a pair": "vendor/ok,glm-5.3",
		"the same model twice": "vendor/ok,vendor/ok",
	} {
		if _, err := ParseModelList(value); err == nil {
			t.Errorf("%s (%q) was accepted", name, value)
		}
	}
}

func TestValidateProposalTakesAModelListAndNamesTheLayer(t *testing.T) {
	if err := ValidateProposal(KindModel, TargetClassifier, "vendor/a,vendor/b"); err != nil {
		t.Fatalf("a two-model list was refused: %v", err)
	}
	if err := ValidateProposal(KindModel, "", "vendor/a"); err == nil {
		t.Fatal("a model proposal with no target was accepted")
	}
	if err := ValidateProposal(KindModel, TargetEscalation, "vendor/a,oops"); err == nil {
		t.Fatal("a list with an entry that is not a provider/id pair was accepted")
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
		"classifier_models", "escalation_models",
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

func TestShippedHardDenyCoversAutoModesOwnSurfaces(t *testing.T) {
	patterns := ShippedHardDeny("29849")
	joined := strings.Join(patterns, "\n")
	// The verbs that write: environment prose feeds the classifier's own prompt,
	// and the rest file rows in the human's review list.
	for _, verb := range []string{"env", "allow", "deny", "model"} {
		if !strings.Contains(joined, "attn automode "+verb) {
			t.Errorf("shipped hard deny does not cover `attn automode %s`: %v", verb, patterns)
		}
	}
	// The read-only verbs stay reachable: a denied agent explaining what stopped
	// it is behavior the plan asks for.
	for _, verb := range []string{"show", "denials"} {
		if strings.Contains(joined, "attn automode "+verb) {
			t.Errorf("shipped hard deny covers the read-only verb %q: %v", verb, patterns)
		}
	}
	if !strings.Contains(joined, "localhost:29849") {
		t.Errorf("shipped hard deny does not name the daemon's own port: %v", patterns)
	}
	for _, pattern := range patterns {
		if IsBroadPattern(pattern) {
			t.Errorf("shipped hard deny %q names nothing", pattern)
		}
	}
}

func TestShippedHardDenyWithoutAPortNamesOnlyTheCLI(t *testing.T) {
	for _, pattern := range ShippedHardDeny("") {
		if strings.Contains(pattern, ":") {
			t.Errorf("pattern %q names a port when none was given", pattern)
		}
	}
}

func TestResolveHardDenyPutsShippedFirstAndDropsDuplicates(t *testing.T) {
	shipped := ShippedHardDeny("9849")
	resolved := ResolveHardDeny("9849", []string{"ssh prod*", shipped[0], "ssh prod*"})
	if len(resolved) != len(shipped)+1 {
		t.Fatalf("resolved = %v, want the shipped set plus one stored pattern", resolved)
	}
	for i, pattern := range shipped {
		if resolved[i] != pattern {
			t.Fatalf("resolved[%d] = %q, want the shipped %q", i, resolved[i], pattern)
		}
	}
	stored := StripShippedHardDeny("9849", resolved)
	if len(stored) != 1 || stored[0] != "ssh prod*" {
		t.Errorf("stripped = %v, want only the stored pattern", stored)
	}
}
