package workspacelayout

import (
	"math"
	"slices"
	"testing"
)

func TestDockTileAfterPaneCreatesLockedSplit(t *testing.T) {
	root, ok := DockTile(
		DefaultLayout("pane-root"),
		"pane-root",
		DirectionVertical,
		false, // tile lands to the right of the anchor
		"split-md",
		"tile-md",
		string(TileKindMarkdown),
		"",
		0.68,
	)
	if !ok {
		t.Fatal("DockTile did not change layout")
	}
	if root.Type != "split" || len(root.Children) != 2 {
		t.Fatalf("unexpected root: %+v", root)
	}
	if !root.RatioLocked {
		t.Fatal("tile split must be ratio-locked so the tile keeps its size")
	}
	if root.Children[0].Type != "pane" || root.Children[0].PaneID != "pane-root" {
		t.Fatalf("children[0] = %+v, want pane-root", root.Children[0])
	}
	tile := root.Children[1]
	if tile.Type != "tile" || tile.TileID != "tile-md" || tile.TileKind != string(TileKindMarkdown) {
		t.Fatalf("children[1] = %+v, want markdown tile leaf", tile)
	}
}

func TestDockTileBeforePaneLandsOnLeft(t *testing.T) {
	root, ok := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, true, "split-md", "tile-md", "markdown", "", 0.32)
	if !ok {
		t.Fatal("DockTile did not change layout")
	}
	if root.Children[0].Type != "tile" || root.Children[1].PaneID != "pane-root" {
		t.Fatalf("before=true should place tile as children[0]; got %+v", root.Children)
	}
}

func TestTileFractionByIDReturnsTileShare(t *testing.T) {
	right, ok := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-right", "tile-right", "markdown", "", 0.68)
	if !ok {
		t.Fatal("right dock failed")
	}
	if fraction, ok := TileFractionByID(right, "tile-right"); !ok || math.Abs(fraction-0.32) > 1e-9 {
		t.Fatalf("right tile fraction = (%v, %v), want (0.32, true)", fraction, ok)
	}

	left, ok := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, true, "split-left", "tile-left", "markdown", "", 0.32)
	if !ok {
		t.Fatal("left dock failed")
	}
	if fraction, ok := TileFractionByID(left, "tile-left"); !ok || math.Abs(fraction-0.32) > 1e-9 {
		t.Fatalf("left tile fraction = (%v, %v), want (0.32, true)", fraction, ok)
	}
}

func TestLayoutEmpty(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want bool
	}{
		{"zero value", Node{}, true},
		{"single pane", DefaultLayout("pane-1"), false},
		{
			"tile-only (sessionless)",
			Node{Type: "tile", TileID: "tile-md", TileKind: "markdown"},
			false,
		},
		{
			"pane beside tile",
			Node{
				Type:      "split",
				SplitID:   "root",
				Direction: DirectionVertical,
				Ratio:     DefaultSplitRatio,
				Children: []Node{
					{Type: "pane", PaneID: "pane-a"},
					{Type: "tile", TileID: "tile-md", TileKind: "markdown"},
				},
			},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LayoutEmpty(tc.node); got != tc.want {
				t.Fatalf("LayoutEmpty(%+v) = %v, want %v", tc.node, got, tc.want)
			}
		})
	}
}

func TestDockTileBetweenPanes(t *testing.T) {
	// tile1 | tile2 → docking to the right of tile1 yields tile1 | md | tile2.
	tree := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{Type: "pane", PaneID: "pane-b"},
		},
	}
	next, ok := DockTile(tree, "pane-a", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.6)
	if !ok {
		t.Fatal("DockTile did not change layout")
	}
	left := next.Children[0]
	if left.Type != "split" || left.Children[0].PaneID != "pane-a" || left.Children[1].Type != "tile" {
		t.Fatalf("left subtree = %+v, want [pane-a, tile]", left)
	}
	if next.Children[1].PaneID != "pane-b" {
		t.Fatalf("right child = %+v, want pane-b untouched", next.Children[1])
	}
}

func TestDockTileHorizontalStacks(t *testing.T) {
	root, ok := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionHorizontal, false, "split-md", "tile-md", "markdown", "", 0.7)
	if !ok {
		t.Fatal("DockTile did not change layout")
	}
	if root.Direction != DirectionHorizontal {
		t.Fatalf("direction = %q, want horizontal", root.Direction)
	}
}

func TestDockTileMovesExistingInstance(t *testing.T) {
	tree := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{Type: "pane", PaneID: "pane-b"},
		},
	}
	docked, ok := DockTile(tree, "pane-a", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.6)
	if !ok {
		t.Fatal("first dock failed")
	}
	moved, ok := DockTile(docked, "pane-b", DirectionVertical, false, "split-md2", "tile-md", "markdown", "", 0.6)
	if !ok {
		t.Fatal("re-dock (move) failed")
	}
	if ids := TileIDs(moved); len(ids) != 1 || ids[0] != "tile-md" {
		t.Fatalf("tile ids after move = %v, want exactly one tile-md", ids)
	}
	// pane-a should no longer share a split with the tile; pane-b should.
	if moved.Children[0].Type != "pane" || moved.Children[0].PaneID != "pane-a" {
		t.Fatalf("children[0] = %+v, want bare pane-a after move", moved.Children[0])
	}
	right := moved.Children[1]
	if right.Type != "split" || right.Children[0].PaneID != "pane-b" || right.Children[1].TileID != "tile-md" {
		t.Fatalf("children[1] = %+v, want [pane-b, tile-md]", right)
	}
}

func TestDockTileUnknownAnchorFails(t *testing.T) {
	tree := DefaultLayout("pane-root")
	next, ok := DockTile(tree, "pane-missing", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.6)
	if ok {
		t.Fatal("dock against a missing anchor should fail")
	}
	if next.PaneID != "pane-root" {
		t.Fatalf("layout mutated on failure: %+v", next)
	}
}

func TestDockTileRejectsSelfAnchorAndEmptyFields(t *testing.T) {
	tree := DefaultLayout("pane-root")
	if _, ok := DockTile(tree, "tile-md", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.6); ok {
		t.Fatal("anchoring a tile to itself must fail")
	}
	if _, ok := DockTile(tree, "pane-root", DirectionVertical, false, "split-md", "", "markdown", "", 0.6); ok {
		t.Fatal("empty tile id must fail")
	}
	if _, ok := DockTile(tree, "pane-root", DirectionVertical, false, "split-md", "tile-md", "", "", 0.6); ok {
		t.Fatal("empty tile kind must fail")
	}
}

func TestDockTileRejectsPaneIDCollision(t *testing.T) {
	tree := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{Type: "pane", PaneID: "pane-b"},
		},
	}
	next, ok := DockTile(tree, "pane-a", DirectionVertical, false, "split-md", "pane-b", "markdown", "", 0.6)
	if ok {
		t.Fatal("tile id matching a terminal pane must be rejected")
	}
	if !HasPane(next, "pane-a") || !HasPane(next, "pane-b") || HasTile(next, "pane-b") {
		t.Fatalf("layout mutated after pane id collision: %+v", next)
	}
}

func TestDockTilePersistsTileParams(t *testing.T) {
	path := "/Users/me/project/README.md"
	docked, ok := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", path, 0.68)
	if !ok {
		t.Fatal("dock failed")
	}
	if params, ok := TileParamsByID(docked, "tile-md"); !ok || params != path {
		t.Fatalf("TileParamsByID = (%q, %v), want (%q, true)", params, ok, path)
	}

	// Params survive normalization and an encode/decode round-trip.
	snapshot := NormalizeWorkspaceLayout(WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout:       docked,
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	})
	if params, _ := TileParamsByID(snapshot.Layout, "tile-md"); params != path {
		t.Fatalf("params lost in normalization: %q", params)
	}
	encoded, err := EncodeLayout(snapshot.Layout)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if params, _ := TileParamsByID(decoded, "tile-md"); params != path {
		t.Fatalf("params lost in encode/decode: %q", params)
	}

	// Re-docking (move) with new params retargets the same tile.
	moved, ok := DockTile(decoded, "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", "/other/notes.md", 0.5)
	if !ok {
		t.Fatal("re-dock failed")
	}
	if params, _ := TileParamsByID(moved, "tile-md"); params != "/other/notes.md" {
		t.Fatalf("re-dock did not retarget params: %q", params)
	}
	if leaves := TileLeaves(moved); len(leaves) != 1 {
		t.Fatalf("re-dock should keep a single tile, got %d", len(leaves))
	}
}

func TestUpdateTileParamsPreservesLayout(t *testing.T) {
	docked, ok := DockTile(
		DefaultLayout("pane-root"),
		"pane-root",
		DirectionVertical,
		false,
		"split-browser",
		"tile-browser",
		"browser",
		"https://first.example",
		0.68,
	)
	if !ok {
		t.Fatal("dock failed")
	}

	updated, ok := UpdateTileParams(docked, "tile-browser", " https://second.example/docs ")
	if !ok {
		t.Fatal("update failed")
	}
	if params, _ := TileParamsByID(updated, "tile-browser"); params != "https://second.example/docs" {
		t.Fatalf("updated params = %q", params)
	}
	if updated.SplitID != docked.SplitID || updated.Ratio != docked.Ratio || updated.RatioLocked != docked.RatioLocked {
		t.Fatalf("layout metadata changed: before=%+v after=%+v", docked, updated)
	}

	unchanged, ok := UpdateTileParams(updated, "missing", "https://missing.example")
	if ok {
		t.Fatal("missing tile update unexpectedly succeeded")
	}
	if params, _ := TileParamsByID(unchanged, "tile-browser"); params != "https://second.example/docs" {
		t.Fatalf("missing update changed existing tile params: %q", params)
	}
}

func TestUndockTileCollapsesSplit(t *testing.T) {
	docked, ok := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.68)
	if !ok {
		t.Fatal("dock failed")
	}
	undocked, ok := UndockTile(docked, "tile-md")
	if !ok {
		t.Fatal("undock did not report a change")
	}
	if undocked.Type != "pane" || undocked.PaneID != "pane-root" {
		t.Fatalf("undock should collapse back to the lone pane; got %+v", undocked)
	}
	if HasTile(undocked, "tile-md") {
		t.Fatal("tile still present after undock")
	}
	if _, ok := UndockTile(undocked, "tile-md"); ok {
		t.Fatal("undocking a missing tile should report no change")
	}
}

func TestNormalizeWorkspaceLayoutPreservesTileLeaf(t *testing.T) {
	docked, _ := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.68)
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout:       docked,
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if !HasTile(normalized.Layout, "tile-md") {
		t.Fatal("tile pruned during normalization")
	}
	// Tiles are not agent panes: pane bookkeeping must ignore them.
	if ids := PaneIDs(normalized.Layout); !slices.Equal(ids, []string{"pane-root"}) {
		t.Fatalf("pane ids = %v, want only pane-root (tile excluded)", ids)
	}
	if len(normalized.Panes) != 1 {
		t.Fatalf("normalized panes = %+v, want only the agent pane", normalized.Panes)
	}
}

func TestNormalizeDropsMalformedTile(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     DefaultSplitRatio,
			Children: []Node{
				{Type: "pane", PaneID: "pane-root"},
				{Type: "tile", TileID: "tile-md"}, // missing kind → malformed
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}
	normalized := NormalizeWorkspaceLayout(snapshot)
	if HasTile(normalized.Layout, "tile-md") {
		t.Fatal("malformed tile should be dropped during normalization")
	}
	if normalized.Layout.Type != "pane" || normalized.Layout.PaneID != "pane-root" {
		t.Fatalf("layout should collapse to the lone pane; got %+v", normalized.Layout)
	}
}

func TestDockedTileRatioSurvivesEncodeDecode(t *testing.T) {
	docked, _ := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.71)
	encoded, err := EncodeLayout(docked)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if !decoded.RatioLocked || math.Abs(decoded.Ratio-0.71) > 1e-9 {
		t.Fatalf("decoded split = %+v, want locked ratio 0.71", decoded)
	}
	tile := decoded.Children[1]
	if tile.Type != "tile" || tile.TileID != "tile-md" || tile.TileKind != "markdown" {
		t.Fatalf("decoded tile leaf = %+v, want markdown tile", tile)
	}
}

func TestDockedTileIsOpaqueToTerminalRebalance(t *testing.T) {
	// A tile docked into a chain must not be redistributed when a sibling
	// terminal split rebalances. Build pane-a | md, then split pane-a in two.
	docked, _ := DockTile(DefaultLayout("pane-a"), "pane-a", DirectionVertical, false, "split-md", "tile-md", "markdown", "", 0.7)
	withSecond, changed := Split(docked, "pane-a", "pane-b", "split-terminals", DirectionVertical, DefaultSplitRatio)
	if !changed {
		t.Fatal("splitting the terminal pane failed")
	}
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-a",
		Layout:       withSecond,
		Panes: []Pane{
			{PaneID: "pane-a", RuntimeID: "sess-a", SessionID: "sess-a", Kind: PaneKindAgent, Title: "A"},
			{PaneID: "pane-b", RuntimeID: "sess-b", SessionID: "sess-b", Kind: PaneKindAgent, Title: "B"},
		},
	}
	normalized := NormalizeWorkspaceLayout(snapshot)
	if !normalized.Layout.RatioLocked || math.Abs(normalized.Layout.Ratio-0.7) > 1e-9 {
		t.Fatalf("tile split ratio = %v (locked=%v), want preserved 0.7", normalized.Layout.Ratio, normalized.Layout.RatioLocked)
	}
}
