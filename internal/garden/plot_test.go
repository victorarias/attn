package garden

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// readySet is the garden's own readiness answer, keyed by id — the shape
// progress counts against, so the two cannot disagree.
func readySet(seeds []Seed) map[string]bool {
	ready := map[string]bool{}
	for _, seed := range Ready(seeds, nil) {
		ready[seed.ID] = true
	}
	return ready
}

// child is one plot member: part-of the crown, planted, unheld.
func child(id, crown, status string, blocks ...string) Seed {
	seed := Seed{ID: id, Title: id, Status: status, Edges: []Edge{{Kind: EdgePartOf, To: crown}}}
	for _, target := range blocks {
		seed.Edges = append(seed.Edges, Edge{Kind: EdgeBlocks, To: target})
	}
	return seed
}

// heldBy claims a seed for a crew member, which is what growing means: a
// growing seed with nobody on it would be offered as ready.
func heldBy(seed Seed, member string) Seed {
	seed.TenderMember = member
	return seed
}

// A crown row's whole job is saying whether the plot is draining and where it
// is stuck, so the counts have to come from the same readiness answer the
// ready command gives.
func TestPlotProgressCountsWhereTheChildrenStand(t *testing.T) {
	seeds := []Seed{
		{ID: "s-crown", Title: "crown", Status: StatusPlanted},
		child("s-done", "s-crown", StatusHarvested),
		child("s-gone", "s-crown", StatusWithered),
		heldBy(child("s-held", "s-crown", StatusGrowing), "trellis"),
		child("s-parked", "s-crown", StatusDormant),
		child("s-open", "s-crown", StatusPlanted),
		child("s-late", "s-crown", StatusPlanted),
		{ID: "s-elsewhere", Title: "elsewhere", Status: StatusPlanted},
	}
	// s-open holds s-late back, so exactly one child is blocked.
	seeds[5].Edges = append(seeds[5].Edges, Edge{Kind: EdgeBlocks, To: "s-late"})

	got := PlotProgress(seeds, "s-crown", readySet(seeds))
	want := Progress{Total: 6, Done: 1, Withered: 1, Growing: 1, Dormant: 1, Ready: 1, Blocked: 1}
	if got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

// The crown is not its own child: counting it would make an untouched plot read
// as one open item before anybody planted anything into it.
func TestPlotProgressDoesNotCountTheCrown(t *testing.T) {
	seeds := []Seed{{ID: "s-crown", Title: "crown", Status: StatusPlanted}}
	if got := PlotProgress(seeds, "s-crown", readySet(seeds)); got.Total != 0 {
		t.Fatalf("an empty plot counts %d, want 0: %+v", got.Total, got)
	}
}

// A plot is a tree, not one level: a grandchild is the plot's work as much as a
// child, and a listing that stopped at depth one would report a plot as done
// while its leaves are still open.
func TestPlotProgressReachesTheWholeTree(t *testing.T) {
	seeds := []Seed{
		{ID: "s-crown", Title: "crown", Status: StatusPlanted},
		child("s-mid", "s-crown", StatusPlanted),
		child("s-leaf", "s-mid", StatusPlanted),
	}
	if got := PlotProgress(seeds, "s-crown", readySet(seeds)); got.Total != 2 {
		t.Fatalf("progress = %+v, want both descendants counted", got)
	}
}

// Stale is measured against the window, and the boundary is inclusive so a
// seed that has sat exactly the window is named rather than missed.
func TestStaleNamesOnlyOpenSeedsPastTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		seeds := []Seed{
			{ID: "s-quiet", Status: StatusPlanted},
			{ID: "s-exactly", Status: StatusPlanted},
			{ID: "s-fresh", Status: StatusPlanted},
			{ID: "s-closed", Status: StatusHarvested},
			{ID: "s-unknown", Status: StatusPlanted},
		}
		moved := map[string]time.Time{
			"s-quiet":   now.Add(-30 * 24 * time.Hour),
			"s-exactly": now.Add(-DefaultStaleWindow),
			"s-fresh":   now.Add(-time.Hour),
			"s-closed":  now.Add(-30 * 24 * time.Hour),
		}

		got := Stale(seeds, moved, DefaultStaleWindow, now)
		if len(got) != 2 || got[0].ID != "s-quiet" || got[1].ID != "s-exactly" {
			t.Fatalf("stale = %+v, want the two quiet open seeds", got)
		}
	})
}

// A seed with no movement evidence at all is not evidence of neglect. Judging
// it stale would name seeds on nothing.
func TestStaleSkipsASeedItHasNoEvidenceFor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		seeds := []Seed{{ID: "s-unknown", Status: StatusPlanted}}
		if got := Stale(seeds, map[string]time.Time{}, time.Hour, time.Now()); len(got) != 0 {
			t.Fatalf("stale = %+v, want nothing: there is no trail to judge", got)
		}
	})
}

func TestParsePlotSpecReadsAWholePlot(t *testing.T) {
	spec, err := ParsePlotSpec([]byte(`{
		"title": "ship the thing",
		"body": "# the plan",
		"children": [
			{"title": "first step", "body": "do it"},
			{"title": "second step", "blocks": []},
			{"title": "third step"}
		]
	}`))
	if err != nil {
		t.Fatalf("ParsePlotSpec: %v", err)
	}
	if spec.Title != "ship the thing" || len(spec.Children) != 3 {
		t.Fatalf("parsed = %+v", spec)
	}
	// Children are parallel by default: a payload that names no blocks carries
	// no sequencing at all.
	for _, child := range spec.Children {
		if len(child.Blocks) != 0 {
			t.Fatalf("a child with no blocks came back sequenced: %+v", child)
		}
	}
}

// Everything a plot payload can get wrong is refused before anything is
// written, and the refusal names what to change.
func TestParsePlotSpecRefusesWhatCannotBePlanted(t *testing.T) {
	cases := map[string]struct {
		payload string
		wants   []string
	}{
		"a typo'd key would silently drop the sequencing": {
			`{"title":"t","children":[{"title":"a","block":["b"]}]}`,
			[]string{"not a plot payload", "blocks"},
		},
		"no children is not a plot": {
			`{"title":"t","children":[]}`,
			[]string{"attn seed plant"},
		},
		"a blank crown title": {
			`{"title":"   ","children":[{"title":"a"}]}`,
			[]string{"crown"},
		},
		"two children deriving one slug": {
			`{"title":"t","children":[{"title":"Do the thing"},{"title":"do the THING"}]}`,
			[]string{"do-the-thing", "retitle"},
		},
		"blocks naming no sibling": {
			`{"title":"t","children":[{"title":"a","blocks":["nobody"]}]}`,
			[]string{"nobody", "no sibling's step slug", "a"},
		},
		"blocks that cycle": {
			`{"title":"t","children":[{"title":"a","blocks":["b"]},{"title":"b","blocks":["a"]}]}`,
			[]string{"cycle", "a", "b"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePlotSpec([]byte(tc.payload))
			if err == nil {
				t.Fatal("planted anyway")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// A cycle three children long is still a cycle; a chain three children long is
// not. The walk has to tell them apart.
func TestValidatePlotSpecSeparatesAChainFromACycle(t *testing.T) {
	chain := PlotSpec{Title: "t", Children: []PlotChildSpec{
		{Title: "a", Blocks: []string{"b"}},
		{Title: "b", Blocks: []string{"c"}},
		{Title: "c"},
	}}
	if err := ValidatePlotSpec(chain); err != nil {
		t.Fatalf("a three-step chain was refused: %v", err)
	}
	cycle := chain
	cycle.Children = append([]PlotChildSpec{}, chain.Children...)
	cycle.Children[2] = PlotChildSpec{Title: "c", Blocks: []string{"a"}}
	if err := ValidatePlotSpec(cycle); err == nil {
		t.Fatal("a three-step cycle was accepted")
	}
}

// The payload is what an agent writes by hand, so its shape has to survive a
// round trip through the documented JSON.
func TestPlotSpecRoundTripsThroughItsPayload(t *testing.T) {
	want := PlotSpec{Title: "t", Body: "b", Children: []PlotChildSpec{
		{Title: "a", Body: "ab", Blocks: []string{"b"}},
		{Title: "b"},
	}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := ParsePlotSpec(raw)
	if err != nil {
		t.Fatalf("ParsePlotSpec: %v", err)
	}
	if got.Title != want.Title || len(got.Children) != 2 || got.Children[0].Blocks[0] != "b" {
		t.Fatalf("round trip lost the plot: %+v", got)
	}
}
