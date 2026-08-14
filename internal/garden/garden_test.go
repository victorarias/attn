package garden

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/docstore"
	"pgregory.net/rapid"
)

func TestNewIDIsStorableAndQualifiable(t *testing.T) {
	seen := map[string]bool{}
	for range 500 {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if err := ValidateID(id); err != nil {
			t.Fatalf("NewID minted %q, which ValidateID rejects: %v", id, err)
		}
		// The docstore is where a seed id has to survive: it is the document id.
		if err := docstore.ValidateDocumentID(id); err != nil {
			t.Fatalf("NewID minted %q, which the document store refuses: %v", id, err)
		}
		// Fully-qualified-ready: `<daemon-id>/<local-id>` is minted only at a
		// boundary, and a local id carrying a slash would make the two forms
		// ambiguous. The docstore charset already forbids it; assert it here so a
		// change to the alphabet cannot quietly break the arc's pre-commitment.
		if strings.Contains(id, "/") {
			t.Fatalf("NewID minted %q, which contains the qualified-form separator", id)
		}
		seen[id] = true
	}
	if len(seen) < 400 {
		t.Fatalf("500 minted ids collapsed to %d distinct values; the alphabet or the modulo is wrong", len(seen))
	}
}

func TestValidateIDNamesTheShape(t *testing.T) {
	for _, bad := range []string{"", "s-", "s-abc", "s-abcdefg", "x-abcdef", "s-abcdei", "s-ABCDEF", "d-1/s-abcdef"} {
		err := ValidateID(bad)
		if err == nil {
			t.Fatalf("ValidateID(%q) accepted an id that is not one", bad)
		}
		if !strings.Contains(err.Error(), "seed id") {
			t.Fatalf("ValidateID(%q) refused without naming what it wanted: %v", bad, err)
		}
	}
	if err := ValidateID("s-7k3f9m"); err != nil {
		t.Fatalf("ValidateID refused a well-formed id: %v", err)
	}
}

func TestStepSlug(t *testing.T) {
	cases := map[string]string{
		"Plant and see":                    "plant-and-see",
		"  Edges & ready!  ":               "edges-ready",
		"The plan lives in the garden":     "the-plan-lives-in-the-garden",
		"attn seed plant (one line)":       "attn-seed-plant-one-line",
		"...":                              "seed",
		"Slice 5 — plots and dispatch":     "slice-5-plots-and-dispatch",
		"CamelCase Title With 123 Numbers": "camelcase-title-with-123-numbers",
	}
	for title, want := range cases {
		if got := StepSlug(title); got != want {
			t.Fatalf("StepSlug(%q) = %q, want %q", title, got, want)
		}
	}
}

// The slug is derived from arbitrary agent-written titles, so its shape is a
// property rather than a list of examples: whatever goes in, what comes out has
// to be a usable name.
func TestStepSlugShapeHolds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		title := rapid.String().Draw(t, "title")
		slug := StepSlug(title)
		if slug == "" {
			t.Fatalf("StepSlug(%q) produced an empty slug", title)
		}
		if len([]rune(slug)) > MaxSlugChars {
			t.Fatalf("StepSlug(%q) produced %d characters, past the %d cap", title, len([]rune(slug)), MaxSlugChars)
		}
		if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
			t.Fatalf("StepSlug(%q) = %q, which is edged with a dash", title, slug)
		}
		if strings.Contains(slug, "--") {
			t.Fatalf("StepSlug(%q) = %q, which doubles a dash", title, slug)
		}
		for _, r := range slug {
			isLower := r >= 'a' && r <= 'z'
			isDigit := r >= '0' && r <= '9'
			if !isLower && !isDigit && r != '-' {
				t.Fatalf("StepSlug(%q) = %q, which holds %q", title, slug, string(r))
			}
		}
	})
}

func TestValidatePlantNamesEveryRefusal(t *testing.T) {
	if err := ValidatePlant("  ", ""); err == nil || !strings.Contains(err.Error(), "attn seed plant") {
		t.Fatalf("an empty title was refused without showing the command: %v", err)
	}
	long := strings.Repeat("x", MaxTitleChars+1)
	err := ValidatePlant(long, "")
	if err == nil {
		t.Fatal("an over-long title was accepted")
	}
	// The rule the guide states: name the limit, its value, and the ask.
	for _, want := range []string{"401", "400"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("title refusal %q does not carry %q", err, want)
		}
	}
	err = ValidatePlant("fine", strings.Repeat("y", MaxBodyBytes+1))
	if err == nil {
		t.Fatal("an over-long body was accepted")
	}
	if !strings.Contains(err.Error(), "1048577") || !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("body refusal does not carry both the ask and the limit: %v", err)
	}
	if err := ValidatePlant("plant and see", "# a plan\n"); err != nil {
		t.Fatalf("a healthy planting was refused: %v", err)
	}
}

func TestEncodeAlwaysWritesEveryDeclaredField(t *testing.T) {
	raw, err := Seed{ID: "s-7k3f9m", Title: "plant and see", Status: StatusPlanted}.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("stored body is not an object: %v", err)
	}
	// A declared field missing from a body is a document a query cannot see: a
	// filter on a field no body carries matches nothing.
	for _, field := range SeedsSchema().Fields {
		if _, ok := body[field.Name]; !ok {
			t.Fatalf("declared field %q is absent from an encoded seed: %s", field.Name, raw)
		}
	}
	if _, ok := body["edges"]; !ok {
		t.Fatalf("edges is absent from an encoded seed: %s", raw)
	}
}

func TestDecodeToleratesAFieldItDoesNotKnow(t *testing.T) {
	seed, err := Decode([]byte(`{"id":"s-7k3f9m","title":"t","status":"planted","harvest_moon":true}`))
	if err != nil {
		t.Fatalf("Decode refused a body written by a later attn: %v", err)
	}
	if seed.ID != "s-7k3f9m" || seed.Status != StatusPlanted {
		t.Fatalf("Decode lost fields it does know: %+v", seed)
	}
}

func TestSeedRoundTrips(t *testing.T) {
	want := Seed{
		ID: "s-7k3f9m", Title: "plant and see", Body: "# a plan\n\nbody",
		Status: StatusPlanted, StepSlug: "plant-and-see",
		PlanterSession: "sess-1", PlanterMember: "trellis",
		Edges: []Edge{{Kind: "part-of", To: "s-aaaaaa"}},
		Vars:  []Var{{Name: "repo", Required: true}},
	}
	raw, err := want.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Title != want.Title || got.Body != want.Body || got.PlanterMember != want.PlanterMember {
		t.Fatalf("round trip changed the seed: %+v", got)
	}
	if len(got.Edges) != 1 || got.Edges[0].Kind != "part-of" || got.Vars[0].Name != "repo" {
		t.Fatalf("round trip lost the edge or the variable: %+v", got)
	}
}

func TestExportStampsTheFileAsGenerated(t *testing.T) {
	out := Export(Seed{ID: "s-7k3f9m", Title: "the garden", Body: "## alignment\n\ntext\n"})
	if !strings.HasPrefix(out, "# the garden\n") {
		t.Fatalf("export does not open with the title: %q", out)
	}
	if !strings.Contains(out, "edit the crown, not this file") {
		t.Fatalf("export is not stamped as generated: %q", out)
	}
	if !strings.Contains(out, "`s-7k3f9m`") {
		t.Fatalf("export does not name the crown it came from: %q", out)
	}
	if !strings.Contains(out, "## alignment") {
		t.Fatalf("export dropped the body: %q", out)
	}
}

func TestSchemasAreValid(t *testing.T) {
	for _, schema := range []docstore.CollectionSchema{SeedsSchema(), NotesSchema()} {
		if err := schema.Validate(); err != nil {
			t.Fatalf("%s/%s does not validate: %v", schema.Namespace, schema.Collection, err)
		}
	}
}
