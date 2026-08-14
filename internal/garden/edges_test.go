package garden

import (
	"strings"
	"testing"
)

// seedWith is the shorthand every table below builds rows from: an open,
// unheld, workspace-less seed that a case then bends.
func seedWith(id string, edges ...Edge) Seed {
	return Seed{ID: id, Title: id, Status: StatusPlanted, Edges: edges}
}

func blocks(to string) Edge { return Edge{Kind: EdgeBlocks, To: to} }
func partOf(to string) Edge { return Edge{Kind: EdgePartOf, To: to} }
func ids(seeds []Seed) []string {
	out := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		out = append(out, seed.ID)
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// noSession is the liveness answer for a garden whose tenders name no session.
func noSession(string) bool { return false }

// Ready is the whole point of edges: what can be tended right now, answered at
// query time so harvesting a blocker frees its dependent with nobody clearing
// anything.
func TestReady(t *testing.T) {
	held := func(seed Seed, session, member string) Seed {
		seed.Status = StatusGrowing
		seed.TenderSession = session
		seed.TenderMember = member
		return seed
	}

	tests := []struct {
		name  string
		seeds []Seed
		live  func(string) bool
		want  []string
	}{
		{
			name:  "a plain planted seed is ready",
			seeds: []Seed{seedWith("s-a")},
			want:  []string{"s-a"},
		},
		{
			name:  "an unclosed blocker holds its dependent back",
			seeds: []Seed{seedWith("s-a", blocks("s-b")), seedWith("s-b")},
			want:  []string{"s-a"},
		},
		{
			name: "harvesting the blocker surfaces the dependent",
			seeds: []Seed{
				func() Seed { s := seedWith("s-a", blocks("s-b")); s.Status = StatusHarvested; return s }(),
				seedWith("s-b"),
			},
			want: []string{"s-b"},
		},
		{
			name: "a withered blocker stops blocking too",
			seeds: []Seed{
				func() Seed { s := seedWith("s-a", blocks("s-b")); s.Status = StatusWithered; return s }(),
				seedWith("s-b"),
			},
			want: []string{"s-b"},
		},
		{
			name: "a parked blocker still blocks — parking is a pause, not an answer",
			seeds: []Seed{
				func() Seed { s := seedWith("s-a", blocks("s-b")); s.Status = StatusDormant; return s }(),
				seedWith("s-b"),
			},
			want: []string{},
		},
		{
			name:  "every blocker must go, not just one",
			seeds: []Seed{seedWith("s-a", blocks("s-c")), seedWith("s-b", blocks("s-c")), seedWith("s-c")},
			want:  []string{"s-a", "s-b"},
		},
		{
			name:  "a crown is not ready — its work is its children",
			seeds: []Seed{seedWith("s-child", partOf("s-crown")), seedWith("s-crown")},
			want:  []string{"s-child"},
		},
		{
			name: "a crown stays out of ready when its children close: it is finished by harvesting it, not by tending it",
			seeds: []Seed{
				func() Seed { s := seedWith("s-child", partOf("s-crown")); s.Status = StatusHarvested; return s }(),
				seedWith("s-crown"),
			},
			want: []string{},
		},
		{
			name:  "the chain the plan names: A blocks B, B part-of C leaves only A",
			seeds: []Seed{seedWith("s-a", blocks("s-b")), seedWith("s-b", partOf("s-c")), seedWith("s-c")},
			want:  []string{"s-a"},
		},
		{
			name:  "a closed seed is never ready",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Status = StatusHarvested; return s }()},
			want:  []string{},
		},
		{
			name:  "a parked seed is never ready",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Status = StatusDormant; return s }()},
			want:  []string{},
		},
		{
			name:  "a gate wants a person, not an agent picking up work",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Gate = true; return s }()},
			want:  []string{},
		},
		{
			name:  "a packet is a shape waiting to be sown",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Template = true; return s }()},
			want:  []string{},
		},
		{
			name: "a seed under a packet is not work either",
			seeds: []Seed{
				func() Seed { s := seedWith("s-packet"); s.Template = true; return s }(),
				seedWith("s-step", partOf("s-packet")),
			},
			want: []string{},
		},
		{
			name:  "a live session holds its seed",
			seeds: []Seed{held(seedWith("s-a"), "sess-1", "")},
			live:  func(id string) bool { return id == "sess-1" },
			want:  []string{},
		},
		{
			name:  "a session the daemon no longer knows releases its seed",
			seeds: []Seed{held(seedWith("s-a"), "sess-gone", "")},
			live:  func(string) bool { return false },
			want:  []string{"s-a"},
		},
		{
			name:  "a member-only tender always holds: attn cannot tell that a person walked away",
			seeds: []Seed{held(seedWith("s-a"), "", "victor")},
			live:  func(string) bool { return false },
			want:  []string{},
		},
		{
			name:  "a stale tender name on an untended seed does not hold it",
			seeds: []Seed{seedWith("s-a")},
			want:  []string{"s-a"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			live := test.live
			if live == nil {
				live = noSession
			}
			got := ids(Ready(test.seeds, live))
			if !equal(got, test.want) {
				t.Fatalf("Ready = %v, want %v", got, test.want)
			}
		})
	}
}

// A cycle is a property of the graph, not of the pair being linked, so the
// refusal has to walk it — and say enough that the caller can undo it.
func TestLinkRefusals(t *testing.T) {
	tests := []struct {
		name    string
		seeds   []Seed
		from    string
		kind    string
		to      string
		wants   []string
		changed bool
		ok      bool
	}{
		{
			name:  "a first edge is written",
			seeds: []Seed{seedWith("s-a"), seedWith("s-b")},
			from:  "s-a", kind: EdgeBlocks, to: "s-b",
			changed: true, ok: true,
		},
		{
			name:  "the same edge twice writes nothing",
			seeds: []Seed{seedWith("s-a", blocks("s-b")), seedWith("s-b")},
			from:  "s-a", kind: EdgeBlocks, to: "s-b",
			changed: false, ok: true,
		},
		{
			name:  "a seed cannot block itself",
			seeds: []Seed{seedWith("s-a")},
			from:  "s-a", kind: EdgeBlocks, to: "s-a",
			wants: []string{"s-a", "cannot"},
		},
		{
			name:  "an unknown seed is named, with the way to list the garden",
			seeds: []Seed{seedWith("s-a")},
			from:  "s-a", kind: EdgeBlocks, to: "s-nope",
			wants: []string{"s-nope", "attn seed ls"},
		},
		{
			name:  "a direct blocks cycle names both seeds and the way out",
			seeds: []Seed{seedWith("s-a"), seedWith("s-b", blocks("s-a"))},
			from:  "s-a", kind: EdgeBlocks, to: "s-b",
			wants: []string{"s-a", "s-b", "deadlock", "attn seed unlink s-b blocks s-a"},
		},
		{
			name: "an indirect blocks cycle names the chain it found",
			seeds: []Seed{
				seedWith("s-a"),
				seedWith("s-b", blocks("s-c")),
				seedWith("s-c", blocks("s-a")),
			},
			from: "s-a", kind: EdgeBlocks, to: "s-b",
			wants: []string{"s-b → s-c → s-a", "attn seed unlink s-b blocks s-c"},
		},
		{
			name:  "a part-of cycle is a plot inside itself",
			seeds: []Seed{seedWith("s-crown", partOf("s-a")), seedWith("s-a")},
			from:  "s-a", kind: EdgePartOf, to: "s-crown",
			wants: []string{"plot inside itself", "attn seed unlink s-crown part-of s-a"},
		},
		{
			name:  "a second plot is refused, naming the one it is in",
			seeds: []Seed{seedWith("s-a", partOf("s-one")), seedWith("s-one"), seedWith("s-two")},
			from:  "s-a", kind: EdgePartOf, to: "s-two",
			wants: []string{"already part of s-one", "attn seed unlink s-a part-of s-one", "s-two"},
		},
		{
			name:  "blocking in both directions is fine when it is not a cycle",
			seeds: []Seed{seedWith("s-a", blocks("s-b")), seedWith("s-b"), seedWith("s-c")},
			from:  "s-a", kind: EdgeBlocks, to: "s-c",
			changed: true, ok: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seed, changed, err := Link(test.seeds, test.from, test.kind, test.to)
			if !test.ok {
				if err == nil {
					t.Fatalf("Link succeeded, want a refusal naming %v", test.wants)
				}
				for _, want := range test.wants {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal %q does not name %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Link: %v", err)
			}
			if changed != test.changed {
				t.Fatalf("changed = %v, want %v", changed, test.changed)
			}
			if test.changed && !hasEdge(seed, test.kind, test.to) {
				t.Fatalf("edge %s %s missing from %v", test.kind, test.to, seed.Edges)
			}
		})
	}
}

func hasEdge(seed Seed, kind, to string) bool {
	for _, edge := range seed.Edges {
		if edge.Kind == kind && edge.To == to {
			return true
		}
	}
	return false
}

// Link must not mutate the garden it was handed: the daemon writes back only the
// seed it gets, and an in-place append would silently edit the read set.
func TestLinkLeavesTheGardenAlone(t *testing.T) {
	seeds := []Seed{seedWith("s-a", blocks("s-x")), seedWith("s-b")}
	if _, _, err := Link(seeds, "s-a", EdgeBlocks, "s-b"); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(seeds[0].Edges) != 1 {
		t.Fatalf("the source seed grew an edge: %v", seeds[0].Edges)
	}
}

func TestUnlink(t *testing.T) {
	seeds := []Seed{seedWith("s-a", blocks("s-b"), partOf("s-c")), seedWith("s-b"), seedWith("s-c")}

	next, err := Unlink(seeds, "s-a", EdgeBlocks, "s-b")
	if err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if hasEdge(next, EdgeBlocks, "s-b") || !hasEdge(next, EdgePartOf, "s-c") {
		t.Fatalf("Unlink removed the wrong edge: %v", next.Edges)
	}
	if len(seeds[0].Edges) != 2 {
		t.Fatalf("Unlink edited the garden it read: %v", seeds[0].Edges)
	}

	// An unlink that silently does nothing reads as a removal that happened, so
	// the refusal has to say what the seed is actually linked to.
	_, err = Unlink(seeds, "s-b", EdgeBlocks, "s-c")
	if err == nil {
		t.Fatal("unlinking an edge that is not there succeeded")
	}
	for _, want := range []string{"s-b does not blocks s-c", "no edges at all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func TestParseEdgeKind(t *testing.T) {
	if kind, err := ParseEdgeKind(" BLOCKS "); err != nil || kind != EdgeBlocks {
		t.Fatalf("ParseEdgeKind(BLOCKS) = %q, %v", kind, err)
	}
	// A kind the schema knows but nothing links yet must not read as a typo.
	_, err := ParseEdgeKind(EdgeRelatesTo)
	if err == nil || !strings.Contains(err.Error(), "real edge kind") {
		t.Fatalf("relates-to refusal = %v", err)
	}
	_, err = ParseEdgeKind("blokcs")
	if err == nil || !strings.Contains(err.Error(), "blocks and part-of") {
		t.Fatalf("typo refusal = %v", err)
	}
}

// Tree is what `attn seed ls --tree` renders: parents before children, and
// nothing dropped — a scoped list holds children whose crown is out of scope,
// and a garden that already stored a cycle still has to render.
func TestTree(t *testing.T) {
	tests := []struct {
		name   string
		seeds  []Seed
		rows   []string
		depths []int
	}{
		{
			name:   "children follow their crown",
			seeds:  []Seed{seedWith("s-crown"), seedWith("s-one", partOf("s-crown")), seedWith("s-two", partOf("s-crown"))},
			rows:   []string{"s-crown", "s-one", "s-two"},
			depths: []int{0, 1, 1},
		},
		{
			name:   "a child listed before its crown still lands under it",
			seeds:  []Seed{seedWith("s-child", partOf("s-crown")), seedWith("s-crown")},
			rows:   []string{"s-crown", "s-child"},
			depths: []int{0, 1},
		},
		{
			name: "depth follows the chain",
			seeds: []Seed{
				seedWith("s-crown"),
				seedWith("s-mid", partOf("s-crown")),
				seedWith("s-leaf", partOf("s-mid")),
			},
			rows:   []string{"s-crown", "s-mid", "s-leaf"},
			depths: []int{0, 1, 2},
		},
		{
			name:   "a child whose crown is out of scope renders at the top rather than vanishing",
			seeds:  []Seed{seedWith("s-child", partOf("s-elsewhere"))},
			rows:   []string{"s-child"},
			depths: []int{0},
		},
		{
			name:   "a stored cycle renders instead of recursing forever",
			seeds:  []Seed{seedWith("s-a", partOf("s-b")), seedWith("s-b", partOf("s-a"))},
			rows:   []string{"s-a", "s-b"},
			depths: []int{0, 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows := Tree(test.seeds)
			got := make([]string, 0, len(rows))
			depths := make([]int, 0, len(rows))
			for _, row := range rows {
				got = append(got, row.Seed.ID)
				depths = append(depths, row.Depth)
			}
			if !equal(got, test.rows) {
				t.Fatalf("Tree = %v, want %v", got, test.rows)
			}
			for i := range depths {
				if depths[i] != test.depths[i] {
					t.Fatalf("depths = %v, want %v", depths, test.depths)
				}
			}
		})
	}
}

func TestInPlot(t *testing.T) {
	seeds := []Seed{
		seedWith("s-leaf", partOf("s-mid")),
		seedWith("s-mid", partOf("s-crown")),
		seedWith("s-crown"),
		seedWith("s-outside"),
	}
	got := ids(InPlot(seeds, "s-crown"))
	if !equal(got, []string{"s-leaf", "s-mid", "s-crown"}) {
		t.Fatalf("InPlot = %v", got)
	}
	if got := ids(InPlot(seeds, "s-outside")); !equal(got, []string{"s-outside"}) {
		t.Fatalf("InPlot(leaf crown) = %v", got)
	}
}

// `show` answers both directions, because an edge is stored on one side only and
// the seed being read is as often the other one.
func TestRelations(t *testing.T) {
	seeds := []Seed{
		seedWith("s-a", blocks("s-b")),
		seedWith("s-b", partOf("s-c")),
		seedWith("s-c"),
	}
	got := Relations(seeds, "s-b")
	want := []Relation{
		{Label: EdgePartOf, Seed: "s-c"},
		{Label: "blocked-by", Seed: "s-a"},
	}
	if len(got) != len(want) {
		t.Fatalf("Relations = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Relations = %v, want %v", got, want)
		}
	}
	if got := Relations(seeds, "s-c"); len(got) != 1 || got[0] != (Relation{Label: "has-part", Seed: "s-b"}) {
		t.Fatalf("Relations(crown) = %v", got)
	}
}

func TestBlockers(t *testing.T) {
	seeds := []Seed{
		seedWith("s-a", blocks("s-c")),
		func() Seed { s := seedWith("s-b", blocks("s-c")); s.Status = StatusHarvested; return s }(),
		seedWith("s-c"),
	}
	if got := Blockers(seeds, "s-c"); !equal(got, []string{"s-a"}) {
		t.Fatalf("Blockers = %v, want [s-a]", got)
	}
	if got := Blockers(seeds, "s-a"); len(got) != 0 {
		t.Fatalf("Blockers(unblocked) = %v", got)
	}
}
