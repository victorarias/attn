package delegationprefs

import (
	"reflect"
	"testing"
)

func TestResolvePreservesRoleAndAppliesRequestOverrides(t *testing.T) {
	base := Selection{Harness: "codex", Model: "everyday", Effort: "medium"}
	c := Config{Enabled: true, Revision: 3, Roles: []Role{{ID: "build", Name: "Build", Enabled: true, Instructions: "Implement", StoppingPoint: "Return for review", DefaultChoiceID: "normal", Choices: []Choice{{ID: "normal", Name: "Everyday", Selection: base}, {ID: "hard", Name: "Demanding", When: "Verification is difficult", Selection: Selection{Harness: "claude", Model: "strong", Effort: "high"}}}}}, Fallback: Fallback{Selection: Selection{Harness: "copilot"}, Instructions: "Unmatched only"}}
	ptr := func(s string) *string { return &s }
	for _, tc := range []struct {
		name    string
		request Request
		want    Selection
	}{
		{"default", Request{Role: "build"}, base},
		{"same model keeps effort", Request{Role: "build", Model: ptr("everyday")}, base},
		{"effort", Request{Role: "build", Effort: ptr("high")}, Selection{Harness: "codex", Model: "everyday", Effort: "high"}},
		{"model clears effort", Request{Role: "build", Model: ptr("different")}, Selection{Harness: "codex", Model: "different"}},
		{"explicit pair", Request{Role: "build", Model: ptr("different"), Effort: ptr("high")}, Selection{Harness: "codex", Model: "different", Effort: "high"}},
		{"alternative", Request{Role: "build", Choice: "hard"}, Selection{Harness: "claude", Model: "strong", Effort: "high"}},
		{"new harness", Request{Role: "build", Harness: ptr("copilot")}, Selection{Harness: "copilot"}},
		{"fallback", Request{Fallback: true}, Selection{Harness: "copilot"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(c, tc.request)
			if err != nil {
				t.Fatal(err)
			}
			if got.Selection != tc.want {
				t.Fatalf("got %+v want %+v", got.Selection, tc.want)
			}
			if tc.request.Fallback {
				if got.Instructions != "Unmatched only" || got.StoppingPoint != "" {
					t.Fatal(got)
				}
			} else if got.Instructions != "Implement" || got.StoppingPoint != "Return for review" {
				t.Fatal(got)
			}
		})
	}
	if !reflect.DeepEqual(c.Roles[0].Choices[0].Selection, base) {
		t.Fatal("request changed saved choice")
	}
	stale := 2
	for _, r := range []Request{{}, {Role: "missing"}, {Role: "build", Choice: "missing"}, {Role: "build", Fallback: true}, {Role: "build", Revision: &stale}} {
		if _, err := Resolve(c, r); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	c.Enabled = false
	if _, err := Resolve(c, Request{Role: "build"}); err == nil {
		t.Fatal("disabled config resolved")
	}
}

func TestActiveHidesIncompleteRolesChoicesAndFallback(t *testing.T) {
	config := Config{
		Enabled:  true,
		Revision: 8,
		Roles: []Role{
			{ID: "build", Name: "Build", Enabled: true, DefaultChoiceID: "default", Choices: []Choice{
				{ID: "default", Name: "Default", Selection: Selection{Harness: "codex"}},
				{ID: "missing-harness", Name: "Missing harness", When: "Never", Selection: Selection{}},
				{ID: "missing-condition", Name: "Missing condition", Selection: Selection{Harness: "claude"}},
				{ID: "hard", Name: "Hard", When: "Verification is difficult", Selection: Selection{Harness: "claude"}},
			}},
			{ID: "unfinished", Name: "Unfinished", Enabled: true, DefaultChoiceID: "default", Choices: []Choice{{ID: "default", Name: "Default"}}},
			{ID: "disabled", Name: "Disabled", Enabled: false, DefaultChoiceID: "default", Choices: []Choice{{ID: "default", Name: "Default", Selection: Selection{Harness: "pi"}}}},
		},
	}

	got := Active(config)
	if got.Revision != 8 || len(got.Roles) != 1 || got.Roles[0].ID != "build" {
		t.Fatalf("active roles = %+v", got)
	}
	if choices := got.Roles[0].Choices; len(choices) != 2 || choices[0].ID != "default" || choices[1].ID != "hard" {
		t.Fatalf("active choices = %+v", choices)
	}
	if got.Fallback != nil {
		t.Fatalf("incomplete fallback = %+v", got.Fallback)
	}
	if len(config.Roles[0].Choices) != 4 {
		t.Fatal("filtering changed the saved configuration")
	}

	config.Fallback.Selection.Harness = "copilot"
	if got := Active(config); got.Fallback == nil || got.Fallback.Selection.Harness != "copilot" {
		t.Fatalf("configured fallback = %+v", got.Fallback)
	}
}
