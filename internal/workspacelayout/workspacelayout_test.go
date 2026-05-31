package workspacelayout

import (
	"math"
	"testing"
)

func TestSplitTargetsOnlyRequestedPane(t *testing.T) {
	root, changed := Split(
		DefaultLayout("pane-root"),
		"pane-root",
		"pane-a",
		"split-root",
		DirectionVertical,
		DefaultSplitRatio,
	)
	if !changed {
		t.Fatal("first split did not change layout")
	}

	root, changed = Split(
		root,
		"pane-a",
		"pane-b",
		"split-right",
		DirectionHorizontal,
		DefaultSplitRatio,
	)
	if !changed {
		t.Fatal("second split did not change layout")
	}

	if root.Type != "split" || len(root.Children) != 2 {
		t.Fatalf("unexpected root layout: %+v", root)
	}
	right := root.Children[1]
	if right.Type != "split" || len(right.Children) != 2 {
		t.Fatalf("unexpected nested layout: %+v", right)
	}
	if right.Children[0].PaneID != "pane-a" || right.Children[1].PaneID != "pane-b" {
		t.Fatalf("nested split children = %+v, want pane-a/pane-b", right.Children)
	}
}

func TestRemoveCollapsesParentSplit(t *testing.T) {
	root := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "pane", PaneID: "pane-root"},
			{
				Type:      "split",
				SplitID:   "nested",
				Direction: DirectionHorizontal,
				Ratio:     DefaultSplitRatio,
				Children: []Node{
					{Type: "pane", PaneID: "pane-a"},
					{Type: "pane", PaneID: "pane-b"},
				},
			},
		},
	}

	next, removed := Remove(root, "pane-a")
	if !removed {
		t.Fatal("remove did not report change")
	}
	if next.Type != "split" || len(next.Children) != 2 {
		t.Fatalf("unexpected normalized layout: %+v", next)
	}
	if next.Children[1].Type != "pane" || next.Children[1].PaneID != "pane-b" {
		t.Fatalf("collapsed child = %+v, want pane-b", next.Children[1])
	}
}

func TestNormalizeWorkspaceLayoutPrunesMissingPanes(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-gone",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     DefaultSplitRatio,
			Children: []Node{
				{Type: "pane", PaneID: "pane-root"},
				{Type: "pane", PaneID: "pane-gone"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "pane" || normalized.Layout.PaneID != "pane-root" {
		t.Fatalf("normalized layout = %+v, want single agent pane", normalized.Layout)
	}
	if normalized.ActivePaneID != "pane-root" {
		t.Fatalf("active pane = %q, want pane-root", normalized.ActivePaneID)
	}
	if len(normalized.Panes) != 1 || normalized.Panes[0].PaneID != "pane-root" {
		t.Fatalf("normalized panes = %+v, want only pane-root", normalized.Panes)
	}
}

func TestNormalizeWorkspaceLayoutRebalancesSameDirectionChains(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-b",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     DefaultSplitRatio,
			Children: []Node{
				{
					Type:      "split",
					SplitID:   "left",
					Direction: DirectionVertical,
					Ratio:     DefaultSplitRatio,
					Children: []Node{
						{Type: "pane", PaneID: "pane-root"},
						{Type: "pane", PaneID: "pane-a"},
					},
				},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
			{PaneID: "pane-a", RuntimeID: "sess-a", SessionID: "sess-a", Kind: PaneKindAgent, Title: "Agent 1"},
			{PaneID: "pane-b", RuntimeID: "sess-b", SessionID: "sess-b", Kind: PaneKindAgent, Title: "Agent 2"},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "split" {
		t.Fatalf("normalized layout = %+v, want split root", normalized.Layout)
	}
	if math.Abs(normalized.Layout.Ratio-(2.0/3.0)) > 1e-9 {
		t.Fatalf("root ratio = %v, want %v", normalized.Layout.Ratio, 2.0/3.0)
	}
	left := normalized.Layout.Children[0]
	if left.Type != "split" || math.Abs(left.Ratio-0.5) > 1e-9 {
		t.Fatalf("left split = %+v, want balanced nested split", left)
	}
}

func TestNormalizeWorkspaceLayoutRebalancesAfterRemovingPaneFromChain(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-b",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     2.0 / 3.0,
			Children: []Node{
				{
					Type:      "split",
					SplitID:   "left",
					Direction: DirectionVertical,
					Ratio:     DefaultSplitRatio,
					Children: []Node{
						{Type: "pane", PaneID: "pane-root"},
						{Type: "pane", PaneID: "pane-gone"},
					},
				},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
			{PaneID: "pane-b", RuntimeID: "sess-b", SessionID: "sess-b", Kind: PaneKindAgent, Title: "Agent 2"},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "split" {
		t.Fatalf("normalized layout = %+v, want split root", normalized.Layout)
	}
	if math.Abs(normalized.Layout.Ratio-0.5) > 1e-9 {
		t.Fatalf("root ratio = %v, want 0.5", normalized.Layout.Ratio)
	}
}

func TestSetSplitRatioLocksMatchingSplit(t *testing.T) {
	root := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Ratio:     DefaultSplitRatio,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{Type: "pane", PaneID: "pane-b"},
		},
	}

	updated, ok := SetSplitRatio(root, "root", 0.7)
	if !ok {
		t.Fatalf("SetSplitRatio did not find split 'root'")
	}
	if !updated.RatioLocked {
		t.Fatalf("split should be locked after SetSplitRatio")
	}
	if math.Abs(updated.Ratio-0.7) > 1e-9 {
		t.Fatalf("ratio = %v, want 0.7", updated.Ratio)
	}

	if _, ok := SetSplitRatio(root, "missing", 0.7); ok {
		t.Fatalf("SetSplitRatio reported a match for a missing split")
	}
}

func TestSetSplitRatioClampsToMargin(t *testing.T) {
	root := Node{
		Type:      "split",
		SplitID:   "root",
		Direction: DirectionVertical,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{Type: "pane", PaneID: "pane-b"},
		},
	}

	low, _ := SetSplitRatio(root, "root", 0.0)
	if low.Ratio <= 0 || low.Ratio >= 0.5 {
		t.Fatalf("clamped low ratio = %v, want small positive margin", low.Ratio)
	}
	high, _ := SetSplitRatio(root, "root", 1.0)
	if high.Ratio >= 1 || high.Ratio <= 0.5 {
		t.Fatalf("clamped high ratio = %v, want margin below 1", high.Ratio)
	}
}

func TestNormalizeWorkspaceLayoutPreservesLockedRatio(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-a",
		Layout: Node{
			Type:        "split",
			SplitID:     "root",
			Direction:   DirectionVertical,
			Ratio:       0.72,
			RatioLocked: true,
			Children: []Node{
				{Type: "pane", PaneID: "pane-root"},
				{Type: "pane", PaneID: "pane-a"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
			{PaneID: "pane-a", RuntimeID: "sess-a", SessionID: "sess-a", Kind: PaneKindAgent, Title: "Agent 1"},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "split" {
		t.Fatalf("normalized layout = %+v, want split root", normalized.Layout)
	}
	if !normalized.Layout.RatioLocked {
		t.Fatalf("locked flag lost during normalization")
	}
	if math.Abs(normalized.Layout.Ratio-0.72) > 1e-9 {
		t.Fatalf("locked ratio = %v, want 0.72 (must not rebalance to 0.5)", normalized.Layout.Ratio)
	}
}

func TestLockedRatioSurvivesEncodeDecodeRoundTrip(t *testing.T) {
	root := Node{
		Type:        "split",
		SplitID:     "root",
		Direction:   DirectionVertical,
		Ratio:       0.33,
		RatioLocked: true,
		Children: []Node{
			{Type: "pane", PaneID: "pane-a"},
			{Type: "pane", PaneID: "pane-b"},
		},
	}

	encoded, err := EncodeLayout(root)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if !decoded.RatioLocked || math.Abs(decoded.Ratio-0.33) > 1e-9 {
		t.Fatalf("round-tripped node = %+v, want locked ratio 0.33", decoded)
	}
}
