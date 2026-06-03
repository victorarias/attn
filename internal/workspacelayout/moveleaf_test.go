package workspacelayout

import (
	"math"
	"slices"
	"testing"
)

func twoPaneTree() Node {
	return Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{Type: "pane", PaneID: "pane-b"},
		},
	}
}

func TestMoveLeafRelocatesPaneBesidePane(t *testing.T) {
	// Move pane-a to the right of pane-b: a|b -> b|a.
	moved, ok := MoveLeaf(twoPaneTree(), "pane-a", "pane-b", "split-x", DirectionVertical, false, 0.4)
	if !ok {
		t.Fatal("MoveLeaf did not change layout")
	}
	if moved.Type != "split" || moved.Children[0].PaneID != "pane-b" || moved.Children[1].PaneID != "pane-a" {
		t.Fatalf("children = %+v, want [pane-b, pane-a]", moved.Children)
	}
	if ids := PaneIDs(moved); len(ids) != 2 || !slices.Contains(ids, "pane-a") || !slices.Contains(ids, "pane-b") {
		t.Fatalf("pane ids = %v, want exactly pane-a and pane-b (no duplicates)", ids)
	}
}

func TestMoveLeafLocksDroppedRatioSoSizeSticks(t *testing.T) {
	moved, ok := MoveLeaf(twoPaneTree(), "pane-a", "pane-b", "split-x", DirectionVertical, false, 0.4)
	if !ok {
		t.Fatal("MoveLeaf did not change layout")
	}
	if !moved.RatioLocked || math.Abs(moved.Ratio-0.4) > 1e-9 {
		t.Fatalf("new split = {locked:%v ratio:%v}, want locked at 0.4 so the drop size survives", moved.RatioLocked, moved.Ratio)
	}
	// And the lock must survive normalization (otherwise the depth gesture is lost).
	normalized := NormalizeWorkspaceLayout(WorkspaceLayout{
		WorkspaceID: "ws",
		Layout:      moved,
		Panes: []Pane{
			{PaneID: "pane-a", RuntimeID: "rt-a", SessionID: "s-a", Status: PaneStatusReady},
			{PaneID: "pane-b", RuntimeID: "rt-b", SessionID: "s-b", Status: PaneStatusReady},
		},
	})
	if math.Abs(normalized.Layout.Ratio-0.4) > 1e-9 {
		t.Fatalf("normalized ratio = %v, want 0.4 preserved", normalized.Layout.Ratio)
	}
}

func TestMoveLeafSelfDropIsNoOp(t *testing.T) {
	tree := twoPaneTree()
	moved, ok := MoveLeaf(tree, "pane-a", "pane-a", "split-x", DirectionVertical, false, 0.4)
	if ok {
		t.Fatal("dropping a leaf on itself must be a no-op")
	}
	if moved.Children[0].PaneID != "pane-a" || moved.Children[1].PaneID != "pane-b" {
		t.Fatalf("tree changed on self-drop: %+v", moved.Children)
	}
}

func TestMoveLeafOnlyLeafCannotMove(t *testing.T) {
	if _, ok := MoveLeaf(DefaultLayout("solo"), "solo", "", "split-x", DirectionVertical, true, 0.4); ok {
		t.Fatal("container-docking the only leaf must fail: removing it leaves nothing to wrap")
	}
	if _, ok := MoveLeaf(DefaultLayout("solo"), "solo", "ghost", "split-x", DirectionVertical, true, 0.4); ok {
		t.Fatal("moving the only leaf must fail")
	}
}

func TestMoveLeafUnknownLeafFails(t *testing.T) {
	if _, ok := MoveLeaf(twoPaneTree(), "ghost", "pane-a", "split-x", DirectionVertical, false, 0.4); ok {
		t.Fatal("moving a leaf that isn't in the tree must fail")
	}
}

func TestMoveLeafContainerDockWrapsRoot(t *testing.T) {
	// Left column (lt over lb) beside a full-height right pane. Container-dock
	// pane-r to the left: it should span the full height, pushing the column right.
	tree := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{
				Type:      "split",
				SplitID:   "left",
				Direction: DirectionHorizontal,
				Ratio:     DefaultSplitRatio,
				Children: []Node{
					{Type: "pane", PaneID: "pane-lt"},
					{Type: "pane", PaneID: "pane-lb"},
				},
			},
			{Type: "pane", PaneID: "pane-r"},
		},
	}
	moved, ok := MoveLeaf(tree, "pane-r", "", "split-c", DirectionVertical, true, 0.3)
	if !ok {
		t.Fatal("container dock did not change layout")
	}
	if moved.Type != "split" || moved.Direction != DirectionVertical {
		t.Fatalf("root = %+v, want a vertical split", moved)
	}
	if moved.Children[0].PaneID != "pane-r" {
		t.Fatalf("children[0] = %+v, want pane-r spanning the full left edge", moved.Children[0])
	}
	left := moved.Children[1]
	if left.Type != "split" || left.SplitID != "left" || left.Children[0].PaneID != "pane-lt" || left.Children[1].PaneID != "pane-lb" {
		t.Fatalf("children[1] = %+v, want the intact lt/lb column", left)
	}
}

func TestMoveLeafMovesTilePreservingKindAndParams(t *testing.T) {
	tree := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "tile", TileID: "md1", TileKind: string(TileKindMarkdown), TileParams: "/a.md"},
			{Type: "tile", TileID: "md2", TileKind: string(TileKindMarkdown), TileParams: "/b.md"},
		},
	}
	moved, ok := MoveLeaf(tree, "md1", "md2", "split-x", DirectionHorizontal, false, 0.5)
	if !ok {
		t.Fatal("tile-on-tile move did not change layout")
	}
	// md2 stays put; md1 lands beside it carrying its kind + params.
	right := moved.Children[1]
	if right.Type != "tile" || right.TileID != "md1" {
		t.Fatalf("children[1] = %+v, want relocated md1 tile", right)
	}
	if right.TileKind != string(TileKindMarkdown) || right.TileParams != "/a.md" {
		t.Fatalf("relocated tile lost data: kind=%q params=%q", right.TileKind, right.TileParams)
	}
	if ids := TileIDs(moved); len(ids) != 2 {
		t.Fatalf("tile ids = %v, want exactly two (no duplicate from the move)", ids)
	}
}

func TestMoveLeafBetweenLayoutsRenamesConflictingTileID(t *testing.T) {
	source := Node{Type: "tile", TileID: "md", TileKind: string(TileKindMarkdown), TileParams: "/source.md"}
	target := Node{Type: "tile", TileID: "md", TileKind: string(TileKindMarkdown), TileParams: "/target.md"}

	moved, ok := MoveLeafBetweenLayouts(source, target, "md", "", "split-x", DirectionVertical, true, 0.32, "abc123")
	if !ok {
		t.Fatal("MoveLeafBetweenLayouts did not move conflicting tile")
	}
	if !LayoutEmpty(moved.SourceLayout) {
		t.Fatalf("source layout = %+v, want empty after moving only tile", moved.SourceLayout)
	}
	if moved.FinalLeafID != "md-abc123" {
		t.Fatalf("FinalLeafID = %q, want md-abc123", moved.FinalLeafID)
	}
	if ids := TileIDs(moved.TargetLayout); len(ids) != 2 || !slices.Contains(ids, "md") || !slices.Contains(ids, "md-abc123") {
		t.Fatalf("target tile ids = %v, want original and renamed tile", ids)
	}
	if params, ok := TileParamsByID(moved.TargetLayout, "md-abc123"); !ok || params != "/source.md" {
		t.Fatalf("renamed tile params = (%q, %v), want /source.md,true", params, ok)
	}
}

func TestMoveLeafCollapsesSourceSplitWhenNested(t *testing.T) {
	// a | (b / c). Move c next to a; the right column collapses to bare b.
	tree := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{
				Type:      "split",
				SplitID:   "right",
				Direction: DirectionHorizontal,
				Ratio:     DefaultSplitRatio,
				Children: []Node{
					{Type: "pane", PaneID: "pane-b"},
					{Type: "pane", PaneID: "pane-c"},
				},
			},
		},
	}
	moved, ok := MoveLeaf(tree, "pane-c", "pane-a", "split-x", DirectionVertical, true, 0.4)
	if !ok {
		t.Fatal("nested move did not change layout")
	}
	if moved.Children[1].PaneID != "pane-b" {
		t.Fatalf("children[1] = %+v, want the right column collapsed to bare pane-b", moved.Children[1])
	}
	host := moved.Children[0]
	if host.Type != "split" || host.Children[0].PaneID != "pane-c" || host.Children[1].PaneID != "pane-a" {
		t.Fatalf("children[0] = %+v, want [pane-c, pane-a]", host)
	}
	if ids := PaneIDs(moved); len(ids) != 3 {
		t.Fatalf("pane ids = %v, want all three present exactly once", ids)
	}
}
