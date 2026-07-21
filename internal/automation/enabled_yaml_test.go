package automation

import (
	"strings"
	"testing"
)

func TestSetEnabledInYAMLFlipsTopLevelValue(t *testing.T) {
	got, err := SetEnabledInYAML([]byte("enabled: true\nname: foo\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "enabled: false\nname: foo\n" {
		t.Fatalf("got %q", got)
	}
	got, err = SetEnabledInYAML(got, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "enabled: true\nname: foo\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetEnabledInYAMLPreservesTrailingComment(t *testing.T) {
	got, err := SetEnabledInYAML([]byte("enabled: true  # keep this\nname: foo\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "enabled: false  # keep this\nname: foo\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetEnabledInYAMLLeavesNestedEnabledAlone(t *testing.T) {
	for _, doc := range []string{
		"launch:\n  enabled: true\nenabled: true\nname: foo\n",
		"policy:\n  enabled: true\nenabled: true\nname: foo\n",
	} {
		got, err := SetEnabledInYAML([]byte(doc), false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "  enabled: true\n") {
			t.Fatalf("nested enabled was rewritten: got %q from %q", got, doc)
		}
		if !strings.Contains(string(got), "\nenabled: false\n") {
			t.Fatalf("top-level enabled was not rewritten: got %q from %q", got, doc)
		}
	}
}

func TestSetEnabledInYAMLLeavesEnabledInsideCommentOrStringAlone(t *testing.T) {
	doc := "# note: enabled: true is the default\nname: \"enabled: true is mentioned here too\"\nenabled: true\n"
	got, err := SetEnabledInYAML([]byte(doc), false)
	if err != nil {
		t.Fatal(err)
	}
	want := "# note: enabled: true is the default\nname: \"enabled: true is mentioned here too\"\nenabled: false\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetEnabledInYAMLInsertsMissingKey(t *testing.T) {
	got, err := SetEnabledInYAML([]byte("name: foo\nprompt: do it\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "name: foo\nprompt: do it\nenabled: true\n" {
		t.Fatalf("got %q", got)
	}

	// No trailing newline on the original document: the inserted line must
	// still land on its own line, not glued onto the prior one.
	got, err = SetEnabledInYAML([]byte("name: foo"), false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "name: foo\nenabled: false\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetEnabledInYAMLPreservesHeadCommentsAndBlankLines(t *testing.T) {
	doc := "# head comment\n\napi_version: attn.dev/automations/v1alpha1\n\nenabled: true\n\nname: foo\n"
	got, err := SetEnabledInYAML([]byte(doc), false)
	if err != nil {
		t.Fatal(err)
	}
	want := "# head comment\n\napi_version: attn.dev/automations/v1alpha1\n\nenabled: false\n\nname: foo\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetEnabledInYAMLRejectsNonMapping(t *testing.T) {
	for _, doc := range []string{
		"not a mapping\n",
		"- one\n- two\n",
		"",
		"enabled: [true\n",
	} {
		if _, err := SetEnabledInYAML([]byte(doc), true); err == nil {
			t.Fatalf("expected an error for %q", doc)
		}
	}
}

// TestSetEnabledInYAMLOutputStillParsesAsADefinition is the property that
// matters in production, as opposed to the byte-level assertions above: the
// rewritten spec_yaml is what the editor loads and what Save re-parses, so a
// rewrite that produced text ParseDefinitionYAML rejects would leave the
// definition uneditable — reachable by nothing more than clicking the panel's
// toggle. Pins that a toggle round-trip stays a legal definition, keeps every
// other field, and lands the new enabled value in the canonical JSON too (not
// just the YAML text, which is what the split brain this fixes looked like).
func TestSetEnabledInYAMLOutputStillParsesAsADefinition(t *testing.T) {
	dir := t.TempDir()
	raw := strings.ReplaceAll(`# why this automation exists
api_version: attn.dev/automations/v1alpha1
id: round-trip
name: Round trip
enabled: true  # flipped by the panel toggle
trigger: {type: manual}
prompt: Inspect.
launch: {driver: codex}
location: {type: directory, path: PATH}
policy: {continuity: fresh}
`, `PATH`, dir)

	disabled, err := SetEnabledInYAML([]byte(raw), false)
	if err != nil {
		t.Fatal(err)
	}
	spec, canonical, err := ParseDefinitionYAML(disabled)
	if err != nil {
		t.Fatalf("toggled-off yaml no longer parses as a definition: %v\n%s", err, disabled)
	}
	if spec.Enabled {
		t.Fatalf("spec.Enabled = true after toggling off\n%s", disabled)
	}
	if !strings.Contains(string(canonical), `"enabled":false`) {
		t.Fatalf("canonical json did not pick up the new value: %s", canonical)
	}
	if spec.ID != "round-trip" || spec.Name != "Round trip" || !strings.Contains(spec.Prompt, "Inspect.") {
		t.Fatalf("unrelated fields moved: id=%q name=%q prompt=%q", spec.ID, spec.Name, spec.Prompt)
	}
	if !strings.Contains(string(disabled), "# why this automation exists") ||
		!strings.Contains(string(disabled), "# flipped by the panel toggle") {
		t.Fatalf("comments lost in the rewrite:\n%s", disabled)
	}

	// And back, so a disable/enable cycle is not one-way.
	reEnabled, err := SetEnabledInYAML(disabled, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(reEnabled) != raw {
		t.Fatalf("toggling off then on did not return the original document:\ngot  %q\nwant %q", reEnabled, raw)
	}
}
