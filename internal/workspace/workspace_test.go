package workspace

import (
	"math"
	"testing"
)

func TestSplitTargetsOnlyRequestedPane(t *testing.T) {
	root, changed := Split(
		DefaultLayout(),
		MainPaneID,
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
			{Type: "pane", PaneID: MainPaneID},
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

func TestNormalizeSnapshotPrunesMissingPanes(t *testing.T) {
	snapshot := Snapshot{
		SessionID:    "sess-1",
		ActivePaneID: "pane-gone",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     DefaultSplitRatio,
			Children: []Node{
				{Type: "pane", PaneID: MainPaneID},
				{Type: "pane", PaneID: "pane-gone"},
			},
		},
		Panes: []Pane{
			{PaneID: MainPaneID, RuntimeID: "sess-1", Kind: PaneKindMain, Title: DefaultPaneTitle},
		},
	}

	normalized := NormalizeSnapshot(snapshot, "sess-1")
	if normalized.Layout.Type != "pane" || normalized.Layout.PaneID != MainPaneID {
		t.Fatalf("normalized layout = %+v, want single main pane", normalized.Layout)
	}
	if normalized.ActivePaneID != MainPaneID {
		t.Fatalf("active pane = %q, want %q", normalized.ActivePaneID, MainPaneID)
	}
	if len(normalized.Panes) != 1 || normalized.Panes[0].PaneID != MainPaneID {
		t.Fatalf("normalized panes = %+v, want only main", normalized.Panes)
	}
}

func TestNormalizeSnapshotRebalancesSameDirectionChains(t *testing.T) {
	snapshot := Snapshot{
		SessionID:    "sess-1",
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
						{Type: "pane", PaneID: MainPaneID},
						{Type: "pane", PaneID: "pane-a"},
					},
				},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []Pane{
			{PaneID: MainPaneID, RuntimeID: "sess-1", Kind: PaneKindMain, Title: DefaultPaneTitle},
			{PaneID: "pane-a", RuntimeID: "runtime-a", Kind: PaneKindShell, Title: "Shell 1"},
			{PaneID: "pane-b", RuntimeID: "runtime-b", Kind: PaneKindShell, Title: "Shell 2"},
		},
	}

	normalized := NormalizeSnapshot(snapshot, "sess-1")
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

func TestNormalizeSnapshotRebalancesAfterRemovingPaneFromChain(t *testing.T) {
	snapshot := Snapshot{
		SessionID:    "sess-1",
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
						{Type: "pane", PaneID: MainPaneID},
						{Type: "pane", PaneID: "pane-gone"},
					},
				},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []Pane{
			{PaneID: MainPaneID, RuntimeID: "sess-1", Kind: PaneKindMain, Title: DefaultPaneTitle},
			{PaneID: "pane-b", RuntimeID: "runtime-b", Kind: PaneKindShell, Title: "Shell 2"},
		},
	}

	normalized := NormalizeSnapshot(snapshot, "sess-1")
	if normalized.Layout.Type != "split" {
		t.Fatalf("normalized layout = %+v, want split root", normalized.Layout)
	}
	if math.Abs(normalized.Layout.Ratio-0.5) > 1e-9 {
		t.Fatalf("root ratio = %v, want 0.5", normalized.Layout.Ratio)
	}
}
