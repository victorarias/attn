package workspace

import "testing"

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
