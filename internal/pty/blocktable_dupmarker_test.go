package pty

import "testing"

// Duplicate / redraw OSC 133 markers are ordinary in real shells: a prompt
// redraw re-emits 133;A, and a multi-line edit can re-emit 133;B or 133;C for
// the same block. Each of those re-pins a position, replacing a *sharedRef the
// block already holds. The corpus only walks well-formed A→B→C→D cycles, so it
// proves cleanup of the LAST retained refs and nothing about the ones a repeat
// displaces — exactly the leak these tests pin down.
//
// The contract asserted here is total: by the time Close returns, every ref the
// table was ever handed has been freed exactly once. Free counts, not liveness,
// are the assertion — fakeBlockRef counts Frees the way ghosttyvt.LiveTrackedRefs
// counts native refs.

// applyAndCount runs markers through a fresh table, then closes it, and reports
// how many refs were created and how many Frees landed.
func applyAndCount(t *testing.T, markers []osc133Marker) (created, freed int) {
	t.Helper()
	bt := newBlockTable()
	for i, m := range markers {
		created++
		bt.ApplyMarker(m, &fakeBlockRef{x: 0, y: i, freed: &freed}, false)
	}
	bt.Close()
	return created, freed
}

func TestBlockTableRepeatedMarkersFreeReplacedRefs(t *testing.T) {
	cmd := "echo hi"
	zero := int32(0)

	cases := []struct {
		name    string
		markers []osc133Marker
	}{
		{
			// A redrawn prompt with no command in between: the second A
			// replaces bt.pending outright. The displaced block's promptRef is
			// unreachable afterwards, so ApplyMarker is the only place it can
			// be released.
			name: "repeated prompt-start",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133PromptStart},
				{Kind: osc133PromptStart},
			},
		},
		{
			// Re-pinning the input position overwrites inputRef in place.
			name: "repeated input-start",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133InputStart},
				{Kind: osc133InputStart},
				{Kind: osc133InputStart},
			},
		},
		{
			// A repeated pre-exec never reaches a replace: the second C trips
			// self-heal (hasCommand is already set), closing the block and
			// opening a fresh one. Kept to pin that the heal path itself
			// balances every ref it moves between the two blocks.
			name: "repeated pre-exec",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133InputStart},
				{Kind: osc133PreExec, Cmdline: &cmd},
				{Kind: osc133PreExec, Cmdline: &cmd},
			},
		},
		{
			// A full cycle with every marker doubled — the shape a redrawing
			// prompt actually produces.
			name: "every marker doubled",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133PromptStart},
				{Kind: osc133InputStart},
				{Kind: osc133InputStart},
				{Kind: osc133PreExec, Cmdline: &cmd},
				{Kind: osc133PreExec, Cmdline: &cmd},
				{Kind: osc133CommandEnd, ExitCode: &zero},
				{Kind: osc133CommandEnd, ExitCode: &zero},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, freed := applyAndCount(t, tc.markers)
			if freed != created {
				t.Fatalf("freed %d of %d refs after Close; %d leaked", freed, created, created-freed)
			}
		})
	}
}

// A repeat must not double-free the ref it displaces either: the replaced
// sharedRef is released once, and a later Close must not release it again.
// fakeBlockRef counts every Free, so a double-free shows up as freed > created.
func TestBlockTableRepeatedMarkersDoNotDoubleFree(t *testing.T) {
	cmd := "echo hi"
	created, freed := applyAndCount(t, []osc133Marker{
		{Kind: osc133PromptStart},
		{Kind: osc133PromptStart},
		{Kind: osc133InputStart},
		{Kind: osc133InputStart},
		{Kind: osc133PreExec, Cmdline: &cmd},
		{Kind: osc133PreExec, Cmdline: &cmd},
	})
	if freed > created {
		t.Fatalf("freed %d refs but only %d were created: %d double-free(s)", freed, created, freed-created)
	}
	if freed != created {
		t.Fatalf("freed %d of %d refs after Close; %d leaked", freed, created, created-freed)
	}
}

// A repeated marker replaces a position; it must not also strand the block. The
// surviving block reports the LATEST pin for each marker, which is what a
// redraw means — the older row is stale.
func TestBlockTableRepeatedMarkersKeepLatestPosition(t *testing.T) {
	cmd := "echo hi"
	zero := int32(0)
	freed := 0
	bt := newBlockTable()

	// Rows chosen so each repeat pins a different row than its predecessor.
	steps := []struct {
		marker osc133Marker
		row    int
	}{
		{osc133Marker{Kind: osc133PromptStart}, 1},
		{osc133Marker{Kind: osc133PromptStart}, 5}, // prompt redrawn lower
		{osc133Marker{Kind: osc133InputStart}, 6},
		{osc133Marker{Kind: osc133InputStart}, 7}, // input re-pinned
		{osc133Marker{Kind: osc133PreExec, Cmdline: &cmd}, 8},
		{osc133Marker{Kind: osc133CommandEnd, ExitCode: &zero}, 9},
	}
	for _, s := range steps {
		bt.ApplyMarker(s.marker, &fakeBlockRef{x: 0, y: s.row, freed: &freed}, false)
	}

	snap := bt.SnapshotBlocks()
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d blocks, want 1: %s", len(snap), mustJSON(snap))
	}
	got := snap[0]
	if got.PromptRow != 5 {
		t.Fatalf("promptRow = %d, want 5 (the redrawn prompt, not the stale row 1)", got.PromptRow)
	}
	if got.InputRow == nil || *got.InputRow != 7 {
		t.Fatalf("inputRow = %s, want 7 (the re-pinned input)", mustJSON(got.InputRow))
	}
	if got.OutputStartRow == nil || *got.OutputStartRow != 8 {
		t.Fatalf("outputStartRow = %s, want 8", mustJSON(got.OutputStartRow))
	}

	bt.Close()
	if freed != len(steps) {
		t.Fatalf("freed %d of %d refs after Close; %d leaked", freed, len(steps), len(steps)-freed)
	}
}
