package workspacelayout

import (
	"math"
	"slices"
	"testing"
)

func TestDockPanelAfterPaneCreatesLockedSplit(t *testing.T) {
	root, ok := DockPanel(
		DefaultLayout("pane-root"),
		"pane-root",
		DirectionVertical,
		false, // panel lands to the right of the anchor
		"split-md",
		"panel-md",
		string(PanelKindMarkdown),
		"",
		0.68,
	)
	if !ok {
		t.Fatal("DockPanel did not change layout")
	}
	if root.Type != "split" || len(root.Children) != 2 {
		t.Fatalf("unexpected root: %+v", root)
	}
	if !root.RatioLocked {
		t.Fatal("panel split must be ratio-locked so the panel keeps its size")
	}
	if root.Children[0].Type != "pane" || root.Children[0].PaneID != "pane-root" {
		t.Fatalf("children[0] = %+v, want pane-root", root.Children[0])
	}
	panel := root.Children[1]
	if panel.Type != "panel" || panel.PanelID != "panel-md" || panel.PanelKind != string(PanelKindMarkdown) {
		t.Fatalf("children[1] = %+v, want markdown panel leaf", panel)
	}
}

func TestDockPanelBeforePaneLandsOnLeft(t *testing.T) {
	root, ok := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionVertical, true, "split-md", "panel-md", "markdown", "", 0.32)
	if !ok {
		t.Fatal("DockPanel did not change layout")
	}
	if root.Children[0].Type != "panel" || root.Children[1].PaneID != "pane-root" {
		t.Fatalf("before=true should place panel as children[0]; got %+v", root.Children)
	}
}

func TestPanelFractionByIDReturnsPanelShare(t *testing.T) {
	right, ok := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-right", "panel-right", "markdown", "", 0.68)
	if !ok {
		t.Fatal("right dock failed")
	}
	if fraction, ok := PanelFractionByID(right, "panel-right"); !ok || math.Abs(fraction-0.32) > 1e-9 {
		t.Fatalf("right panel fraction = (%v, %v), want (0.32, true)", fraction, ok)
	}

	left, ok := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionVertical, true, "split-left", "panel-left", "markdown", "", 0.32)
	if !ok {
		t.Fatal("left dock failed")
	}
	if fraction, ok := PanelFractionByID(left, "panel-left"); !ok || math.Abs(fraction-0.32) > 1e-9 {
		t.Fatalf("left panel fraction = (%v, %v), want (0.32, true)", fraction, ok)
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
			"panel-only (sessionless)",
			Node{Type: "panel", PanelID: "panel-md", PanelKind: "markdown"},
			false,
		},
		{
			"pane beside panel",
			Node{
				Type:      "split",
				SplitID:   "root",
				Direction: DirectionVertical,
				Ratio:     DefaultSplitRatio,
				Children: []Node{
					{Type: "pane", PaneID: "pane-a"},
					{Type: "panel", PanelID: "panel-md", PanelKind: "markdown"},
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

func TestDockPanelBetweenPanes(t *testing.T) {
	// panel1 | panel2 → docking to the right of panel1 yields panel1 | md | panel2.
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
	next, ok := DockPanel(tree, "pane-a", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.6)
	if !ok {
		t.Fatal("DockPanel did not change layout")
	}
	left := next.Children[0]
	if left.Type != "split" || left.Children[0].PaneID != "pane-a" || left.Children[1].Type != "panel" {
		t.Fatalf("left subtree = %+v, want [pane-a, panel]", left)
	}
	if next.Children[1].PaneID != "pane-b" {
		t.Fatalf("right child = %+v, want pane-b untouched", next.Children[1])
	}
}

func TestDockPanelHorizontalStacks(t *testing.T) {
	root, ok := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionHorizontal, false, "split-md", "panel-md", "markdown", "", 0.7)
	if !ok {
		t.Fatal("DockPanel did not change layout")
	}
	if root.Direction != DirectionHorizontal {
		t.Fatalf("direction = %q, want horizontal", root.Direction)
	}
}

func TestDockPanelMovesExistingInstance(t *testing.T) {
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
	docked, ok := DockPanel(tree, "pane-a", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.6)
	if !ok {
		t.Fatal("first dock failed")
	}
	moved, ok := DockPanel(docked, "pane-b", DirectionVertical, false, "split-md2", "panel-md", "markdown", "", 0.6)
	if !ok {
		t.Fatal("re-dock (move) failed")
	}
	if ids := PanelIDs(moved); len(ids) != 1 || ids[0] != "panel-md" {
		t.Fatalf("panel ids after move = %v, want exactly one panel-md", ids)
	}
	// pane-a should no longer share a split with the panel; pane-b should.
	if moved.Children[0].Type != "pane" || moved.Children[0].PaneID != "pane-a" {
		t.Fatalf("children[0] = %+v, want bare pane-a after move", moved.Children[0])
	}
	right := moved.Children[1]
	if right.Type != "split" || right.Children[0].PaneID != "pane-b" || right.Children[1].PanelID != "panel-md" {
		t.Fatalf("children[1] = %+v, want [pane-b, panel-md]", right)
	}
}

func TestDockPanelUnknownAnchorFails(t *testing.T) {
	tree := DefaultLayout("pane-root")
	next, ok := DockPanel(tree, "pane-missing", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.6)
	if ok {
		t.Fatal("dock against a missing anchor should fail")
	}
	if next.PaneID != "pane-root" {
		t.Fatalf("layout mutated on failure: %+v", next)
	}
}

func TestDockPanelRejectsSelfAnchorAndEmptyFields(t *testing.T) {
	tree := DefaultLayout("pane-root")
	if _, ok := DockPanel(tree, "panel-md", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.6); ok {
		t.Fatal("anchoring a panel to itself must fail")
	}
	if _, ok := DockPanel(tree, "pane-root", DirectionVertical, false, "split-md", "", "markdown", "", 0.6); ok {
		t.Fatal("empty panel id must fail")
	}
	if _, ok := DockPanel(tree, "pane-root", DirectionVertical, false, "split-md", "panel-md", "", "", 0.6); ok {
		t.Fatal("empty panel kind must fail")
	}
}

func TestDockPanelRejectsPaneIDCollision(t *testing.T) {
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
	next, ok := DockPanel(tree, "pane-a", DirectionVertical, false, "split-md", "pane-b", "markdown", "", 0.6)
	if ok {
		t.Fatal("panel id matching a terminal pane must be rejected")
	}
	if !HasPane(next, "pane-a") || !HasPane(next, "pane-b") || HasPanel(next, "pane-b") {
		t.Fatalf("layout mutated after pane id collision: %+v", next)
	}
}

func TestDockPanelPersistsPanelParams(t *testing.T) {
	path := "/Users/me/project/README.md"
	docked, ok := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "panel-md", "markdown", path, 0.68)
	if !ok {
		t.Fatal("dock failed")
	}
	if params, ok := PanelParamsByID(docked, "panel-md"); !ok || params != path {
		t.Fatalf("PanelParamsByID = (%q, %v), want (%q, true)", params, ok, path)
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
	if params, _ := PanelParamsByID(snapshot.Layout, "panel-md"); params != path {
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
	if params, _ := PanelParamsByID(decoded, "panel-md"); params != path {
		t.Fatalf("params lost in encode/decode: %q", params)
	}

	// Re-docking (move) with new params retargets the same panel.
	moved, ok := DockPanel(decoded, "pane-root", DirectionVertical, false, "split-md", "panel-md", "markdown", "/other/notes.md", 0.5)
	if !ok {
		t.Fatal("re-dock failed")
	}
	if params, _ := PanelParamsByID(moved, "panel-md"); params != "/other/notes.md" {
		t.Fatalf("re-dock did not retarget params: %q", params)
	}
	if leaves := PanelLeaves(moved); len(leaves) != 1 {
		t.Fatalf("re-dock should keep a single panel, got %d", len(leaves))
	}
}

func TestUndockPanelCollapsesSplit(t *testing.T) {
	docked, ok := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.68)
	if !ok {
		t.Fatal("dock failed")
	}
	undocked, ok := UndockPanel(docked, "panel-md")
	if !ok {
		t.Fatal("undock did not report a change")
	}
	if undocked.Type != "pane" || undocked.PaneID != "pane-root" {
		t.Fatalf("undock should collapse back to the lone pane; got %+v", undocked)
	}
	if HasPanel(undocked, "panel-md") {
		t.Fatal("panel still present after undock")
	}
	if _, ok := UndockPanel(undocked, "panel-md"); ok {
		t.Fatal("undocking a missing panel should report no change")
	}
}

func TestNormalizeWorkspaceLayoutPreservesPanelLeaf(t *testing.T) {
	docked, _ := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.68)
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout:       docked,
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if !HasPanel(normalized.Layout, "panel-md") {
		t.Fatal("panel pruned during normalization")
	}
	// Panels are not agent panes: pane bookkeeping must ignore them.
	if ids := PaneIDs(normalized.Layout); !slices.Equal(ids, []string{"pane-root"}) {
		t.Fatalf("pane ids = %v, want only pane-root (panel excluded)", ids)
	}
	if len(normalized.Panes) != 1 {
		t.Fatalf("normalized panes = %+v, want only the agent pane", normalized.Panes)
	}
}

func TestNormalizeDropsMalformedPanel(t *testing.T) {
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
				{Type: "panel", PanelID: "panel-md"}, // missing kind → malformed
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}
	normalized := NormalizeWorkspaceLayout(snapshot)
	if HasPanel(normalized.Layout, "panel-md") {
		t.Fatal("malformed panel should be dropped during normalization")
	}
	if normalized.Layout.Type != "pane" || normalized.Layout.PaneID != "pane-root" {
		t.Fatalf("layout should collapse to the lone pane; got %+v", normalized.Layout)
	}
}

func TestDockedPanelRatioSurvivesEncodeDecode(t *testing.T) {
	docked, _ := DockPanel(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.71)
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
	panel := decoded.Children[1]
	if panel.Type != "panel" || panel.PanelID != "panel-md" || panel.PanelKind != "markdown" {
		t.Fatalf("decoded panel leaf = %+v, want markdown panel", panel)
	}
}

func TestDockedPanelIsOpaqueToTerminalRebalance(t *testing.T) {
	// A panel docked into a chain must not be redistributed when a sibling
	// terminal split rebalances. Build pane-a | md, then split pane-a in two.
	docked, _ := DockPanel(DefaultLayout("pane-a"), "pane-a", DirectionVertical, false, "split-md", "panel-md", "markdown", "", 0.7)
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
		t.Fatalf("panel split ratio = %v (locked=%v), want preserved 0.7", normalized.Layout.Ratio, normalized.Layout.RatioLocked)
	}
}
