package workspacelayout

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// The example tests in workspacelayout_test.go, tile_test.go and moveleaf_test.go
// pin the shapes we thought of: a split here, a dock there, the three or four
// move cases that once broke. This one explores the shapes we did not. A real
// workspace is not one operation, it is a day of them — split, dock a markdown
// tile, drag a pane across the tree, close something, drag it back — and the
// layout that wedges is the one reached by a sequence nobody wrote down.
//
// rapid drives a random sequence of the real operations and re-checks the whole
// tree contract after every single one, so a failure names the shortest sequence
// that reaches the broken tree rather than the tree itself.

// tileMeta is what the model expects a docked tile to still carry. Kind, params
// and session id are opaque to this package but must survive every move: a tile
// that loses its params renders the no-selection picker, which reads to the user
// as the file having been closed.
type tileMeta struct {
	kind    string
	params  string
	session string
}

// layoutModel is the shadow state the property checks the tree against: which
// leaves should exist, and what each tile should still hold.
type layoutModel struct {
	tree  Node
	panes map[string]bool
	tiles map[string]tileMeta
	next  int
}

func (m *layoutModel) mint(prefix string) string {
	m.next++
	return fmt.Sprintf("%s%d", prefix, m.next)
}

// leafIDs is every leaf the model believes is in the tree, in a stable order so
// a rapid draw over it reproduces.
func (m *layoutModel) leafIDs() []string {
	ids := make([]string, 0, len(m.panes)+len(m.tiles))
	for id := range m.panes {
		ids = append(ids, id)
	}
	for id := range m.tiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *layoutModel) paneIDs() []string {
	ids := make([]string, 0, len(m.panes))
	for id := range m.panes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *layoutModel) tileIDs() []string {
	ids := make([]string, 0, len(m.tiles))
	for id := range m.tiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// cloneNode deep-copies a tree so an operation can be checked for having left
// its input alone. Every function here takes a Node by value and returns a new
// one, which only holds if the children slices are copied rather than written
// through — a caller that still holds the pre-operation layout (the daemon reads
// a snapshot, edits it, and writes it back) must see what it read.
func cloneNode(node Node) Node {
	out := node
	if node.Children == nil {
		return out
	}
	out.Children = make([]Node, len(node.Children))
	for i, child := range node.Children {
		out.Children[i] = cloneNode(child)
	}
	return out
}

// checkWellFormed asserts the structural contract every consumer relies on: the
// renderer walks this tree, the daemon derives pane bookkeeping from it, and
// both assume a split is a split.
func checkWellFormed(t *rapid.T, node Node, path string) {
	switch node.Type {
	case "pane":
		if node.PaneID == "" {
			t.Fatalf("%s: a pane leaf has no pane id", path)
		}
		if len(node.Children) > 0 {
			t.Fatalf("%s: a pane leaf has %d children", path, len(node.Children))
		}
	case "tile":
		if node.TileID == "" || node.TileKind == "" {
			t.Fatalf("%s: a tile leaf has id %q and kind %q; both are required", path, node.TileID, node.TileKind)
		}
		if len(node.Children) > 0 {
			t.Fatalf("%s: a tile leaf has %d children", path, len(node.Children))
		}
	case "split":
		if len(node.Children) != 2 {
			t.Fatalf("%s: a split has %d children, want exactly 2", path, len(node.Children))
		}
		if node.Direction != DirectionVertical && node.Direction != DirectionHorizontal {
			t.Fatalf("%s: a split has direction %q", path, node.Direction)
		}
		if !(node.Ratio > 0 && node.Ratio < 1) {
			t.Fatalf("%s: a split has ratio %v, which collapses a side", path, node.Ratio)
		}
		checkWellFormed(t, node.Children[0], path+".0")
		checkWellFormed(t, node.Children[1], path+".1")
	default:
		t.Fatalf("%s: node type %q is not a pane, tile or split", path, node.Type)
	}
}

// splitIDs collects every split id in the tree so an op can aim at one.
func splitIDs(node Node) []string {
	var ids []string
	var walk func(Node)
	walk = func(n Node) {
		if n.Type != "split" {
			return
		}
		ids = append(ids, n.SplitID)
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(node)
	sort.Strings(ids)
	return ids
}

func findSplit(node Node, splitID string) (Node, bool) {
	if node.Type != "split" {
		return Node{}, false
	}
	if node.SplitID == splitID {
		return node, true
	}
	for _, child := range node.Children {
		if found, ok := findSplit(child, splitID); ok {
			return found, true
		}
	}
	return Node{}, false
}

var (
	directions   = []Direction{DirectionVertical, DirectionHorizontal, "", "diagonal"}
	tileKinds    = []string{string(TileKindMarkdown), string(TileKindBrowser), string(TileKindSeed), string(TileKindNotebook), "  markdown  "}
	tileParams   = []string{"", "/tmp/notes.md", "  /tmp/spaced.md  ", "https://example.test"}
	tileSessions = []string{"", "sess-a", "  sess-b  "}
)

// drawRatio spans past both ends so the clamping every constructor does is part
// of what is under test: a ratio outside (0,1) collapses a pane to nothing.
func drawRatio(t *rapid.T, label string) float64 {
	return rapid.Float64Range(-1, 2).Draw(t, label)
}

func TestLayoutStaysAWellFormedTreeUnderRandomOperations(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		root := "p0"
		m := &layoutModel{
			tree:  DefaultLayout(root),
			panes: map[string]bool{root: true},
			tiles: map[string]tileMeta{},
		}

		// apply runs one operation and holds it to the contract every operation
		// here shares: it returns a new tree and does not write through the one
		// it was given.
		apply := func(t *rapid.T, op func(Node) (Node, bool)) (Node, bool) {
			before := cloneNode(m.tree)
			next, ok := op(m.tree)
			if !reflect.DeepEqual(m.tree, before) {
				t.Fatalf("the operation modified the layout it was given:\n got: %+v\nwant: %+v", m.tree, before)
			}
			return next, ok
		}

		t.Repeat(map[string]func(*rapid.T){
			// Cmd+D: split a terminal pane in two.
			"split_pane": func(t *rapid.T) {
				panes := m.paneIDs()
				if len(panes) == 0 {
					t.Skip("every terminal pane is closed; only tiles are left")
				}
				target := rapid.SampledFrom(panes).Draw(t, "target")
				newPane := m.mint("p")
				splitID := m.mint("s")
				direction := rapid.SampledFrom(directions).Draw(t, "direction")
				ratio := drawRatio(t, "ratio")

				next, ok := apply(t, func(n Node) (Node, bool) {
					return Split(n, target, newPane, splitID, direction, ratio)
				})
				if !ok {
					t.Fatalf("Split on pane %q, which is in the tree, reported no change", target)
				}
				m.tree = next
				m.panes[newPane] = true
			},

			// Dock a tile beside a leaf — or, for a tile already in the tree,
			// move it there, since docking doubles as a move.
			"dock_tile": func(t *rapid.T) {
				anchor := rapid.SampledFrom(m.leafIDs()).Draw(t, "anchor")
				candidates := append([]string{""}, m.tileIDs()...)
				tileID := rapid.SampledFrom(candidates).Draw(t, "tile")
				if tileID == "" {
					tileID = m.mint("t")
				}
				if tileID == anchor {
					t.Skip("a tile cannot be docked against itself")
				}
				kind := rapid.SampledFrom(tileKinds).Draw(t, "kind")
				params := rapid.SampledFrom(tileParams).Draw(t, "params")
				session := rapid.SampledFrom(tileSessions).Draw(t, "session")
				splitID := m.mint("s")
				direction := rapid.SampledFrom(directions).Draw(t, "direction")
				before := rapid.Bool().Draw(t, "before")
				ratio := drawRatio(t, "ratio")

				next, ok := apply(t, func(n Node) (Node, bool) {
					return DockTile(n, anchor, direction, before, splitID, tileID, kind, params, session, ratio)
				})
				if !ok {
					t.Fatalf("DockTile of %q against leaf %q, which is in the tree, was refused", tileID, anchor)
				}
				m.tree = next

				// An empty session id carries the tile's existing binding
				// forward, so moving a tile never silently unbinds it.
				want := tileMeta{kind: strings.TrimSpace(kind), params: strings.TrimSpace(params), session: strings.TrimSpace(session)}
				if want.session == "" {
					if prev, ok := m.tiles[tileID]; ok {
						want.session = prev.session
					}
				}
				m.tiles[tileID] = want
			},

			// Drag a pane or tile somewhere else in the tree, including onto the
			// workspace itself (an empty anchor), which re-roots the layout.
			"move_leaf": func(t *rapid.T) {
				leaves := m.leafIDs()
				if len(leaves) < 2 {
					t.Skip("a move needs something to move against")
				}
				leaf := rapid.SampledFrom(leaves).Draw(t, "leaf")
				anchor := rapid.SampledFrom(append([]string{""}, leaves...)).Draw(t, "anchor")
				splitID := m.mint("s")
				direction := rapid.SampledFrom(directions).Draw(t, "direction")
				before := rapid.Bool().Draw(t, "before")
				ratio := drawRatio(t, "ratio")

				next, ok := apply(t, func(n Node) (Node, bool) {
					return MoveLeaf(n, leaf, anchor, splitID, direction, before, ratio)
				})
				if leaf == anchor {
					if ok {
						t.Fatalf("MoveLeaf dropped leaf %q on itself and reported a change", leaf)
					}
					t.Skip("dropping a leaf on itself is a no-op")
				}
				if !ok {
					t.Fatalf("MoveLeaf of %q against %q, both in the tree, was refused", leaf, anchor)
				}
				m.tree = next
			},

			// Close a pane, or undock a tile. The last leaf is left alone: a
			// layout with nothing in it is the workspace being torn down, which
			// is the caller's decision and not this tree's state.
			"remove_leaf": func(t *rapid.T) {
				leaves := m.leafIDs()
				if len(leaves) < 2 {
					t.Skip("keeping the last leaf; an empty layout ends the workspace")
				}
				leaf := rapid.SampledFrom(leaves).Draw(t, "leaf")

				next, ok := apply(t, func(n Node) (Node, bool) { return Remove(n, leaf) })
				if !ok {
					t.Fatalf("Remove of %q, which is in the tree, reported nothing removed", leaf)
				}
				m.tree = next
				delete(m.panes, leaf)
				delete(m.tiles, leaf)
			},

			// A tile consumer rebinds an open tile: the browser tile follows a
			// navigation, a markdown tile follows the file it was opened from.
			"update_tile": func(t *rapid.T) {
				ids := m.tileIDs()
				if len(ids) == 0 {
					t.Skip("no tile to rebind yet")
				}
				tileID := rapid.SampledFrom(ids).Draw(t, "tile")
				params := rapid.SampledFrom(tileParams).Draw(t, "params")
				session := rapid.SampledFrom(tileSessions).Draw(t, "session")

				next, ok := apply(t, func(n Node) (Node, bool) {
					return UpdateTileParams(n, tileID, params)
				})
				if !ok {
					t.Fatalf("UpdateTileParams on tile %q, which is in the tree, was refused", tileID)
				}
				m.tree = next

				next, ok = apply(t, func(n Node) (Node, bool) {
					return UpdateTileSessionID(n, tileID, session)
				})
				if !ok {
					t.Fatalf("UpdateTileSessionID on tile %q, which is in the tree, was refused", tileID)
				}
				m.tree = next

				meta := m.tiles[tileID]
				meta.params = strings.TrimSpace(params)
				meta.session = strings.TrimSpace(session)
				m.tiles[tileID] = meta
			},

			// Drag a divider. The ratio is clamped so neither side collapses,
			// and locked so normalization stops rebalancing that split.
			"set_ratio": func(t *rapid.T) {
				ids := splitIDs(m.tree)
				if len(ids) == 0 {
					t.Skip("no split to resize yet")
				}
				splitID := rapid.SampledFrom(ids).Draw(t, "split")
				ratio := drawRatio(t, "ratio")

				next, ok := apply(t, func(n Node) (Node, bool) { return SetSplitRatio(n, splitID, ratio) })
				if !ok {
					t.Fatalf("SetSplitRatio on split %q, which is in the tree, was refused", splitID)
				}
				m.tree = next

				split, found := findSplit(m.tree, splitID)
				if !found {
					t.Fatalf("split %q disappeared from the tree when its ratio was set", splitID)
				}
				if !split.RatioLocked {
					t.Fatalf("split %q was resized by hand but is not locked; normalization will rebalance it away", splitID)
				}
				if split.Ratio < 0.05 || split.Ratio > 0.95 {
					t.Fatalf("split %q was set to %v, outside the collapse margin", splitID, split.Ratio)
				}
			},

			// The daemon normalizes before persisting and after loading, so
			// every tree above has to survive a round through it unchanged in
			// content — and a second round unchanged in full.
			"normalize": func(t *rapid.T) {
				snapshot := WorkspaceLayout{
					WorkspaceID:  "ws",
					ActivePaneID: rapid.SampledFrom(append([]string{"", "gone"}, m.leafIDs()...)).Draw(t, "active"),
					Layout:       m.tree,
					Panes:        modelPanes(m),
				}
				normalized := NormalizeWorkspaceLayout(snapshot)
				again := NormalizeWorkspaceLayout(normalized)
				if !reflect.DeepEqual(normalized, again) {
					t.Fatalf("normalization is not idempotent:\nonce: %+v\ntwice: %+v", normalized, again)
				}

				panes := PaneIDs(normalized.Layout)
				if normalized.ActivePaneID == "" {
					if len(panes) > 0 {
						t.Fatalf("no active pane, but the layout holds %v", panes)
					}
				} else if !slices.Contains(panes, normalized.ActivePaneID) {
					t.Fatalf("active pane %q is not in the layout %v", normalized.ActivePaneID, panes)
				}
				if len(normalized.Panes) != len(panes) {
					t.Fatalf("layout holds %d panes but the record carries %d", len(panes), len(normalized.Panes))
				}
				m.tree = normalized.Layout
			},

			// Runs before and after every action above.
			"": func(t *rapid.T) {
				checkWellFormed(t, m.tree, "root")

				panes := PaneIDs(m.tree)
				tiles := TileIDs(m.tree)
				seen := make(map[string]bool, len(panes)+len(tiles))
				for _, id := range append(append([]string{}, panes...), tiles...) {
					if seen[id] {
						t.Fatalf("leaf %q appears twice in the tree; panes %v tiles %v", id, panes, tiles)
					}
					seen[id] = true
				}

				sort.Strings(panes)
				sort.Strings(tiles)
				if want := m.paneIDs(); !sameIDs(panes, want) {
					t.Fatalf("tree holds panes %v, want %v", panes, want)
				}
				if want := m.tileIDs(); !sameIDs(tiles, want) {
					t.Fatalf("tree holds tiles %v, want %v", tiles, want)
				}

				// A tile's kind, params and session survive every move: they are
				// the tile's whole identity to the consumer that renders it.
				for _, leaf := range TileLeaves(m.tree) {
					want := m.tiles[leaf.TileID]
					got := tileMeta{kind: leaf.TileKind, params: leaf.TileParams, session: leaf.TileSessionID}
					if got != want {
						t.Fatalf("tile %q carries %+v, want %+v", leaf.TileID, got, want)
					}
				}
			},
		})
	})
}

// Every operation in this package takes a Node by value and returns a new one.
// That is only true if the children slices are copied rather than written
// through — a Node value shares its children's backing array with every copy of
// itself, so writing into it reaches the caller's tree.
//
// The stateful property above holds every operation to this through apply. This
// one pins the two that once did not, UpdateTileParams and UpdateTileSessionID,
// on the smallest tree that shows it: a caller that keeps reading the layout it
// handed in must see what it read. The seed under testdata/rapid replays the
// counterexample rapid shrank to.
func TestLayoutOperationsDoNotModifyTheirInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tree, _ := DockTile(DefaultLayout("p0"), "p0", DirectionVertical, false, "s1",
			"t1", string(TileKindMarkdown), "", "", DefaultSplitRatio)

		params := rapid.SampledFrom(tileParams).Draw(t, "params")
		session := rapid.SampledFrom(tileSessions).Draw(t, "session")

		before := cloneNode(tree)
		if _, ok := UpdateTileParams(tree, "t1", params); !ok {
			t.Fatal("UpdateTileParams did not find the tile it was pointed at")
		}
		if !reflect.DeepEqual(tree, before) {
			t.Fatalf("UpdateTileParams wrote through its input:\n got: %+v\nwant: %+v", tree, before)
		}

		before = cloneNode(tree)
		if _, ok := UpdateTileSessionID(tree, "t1", session); !ok {
			t.Fatal("UpdateTileSessionID did not find the tile it was pointed at")
		}
		if !reflect.DeepEqual(tree, before) {
			t.Fatalf("UpdateTileSessionID wrote through its input:\n got: %+v\nwant: %+v", tree, before)
		}
	})
}

// modelPanes builds the pane records the daemon would have stored beside the
// tree, so normalization has something to reconcile against.
func modelPanes(m *layoutModel) []Pane {
	panes := make([]Pane, 0, len(m.panes))
	for _, id := range m.paneIDs() {
		panes = append(panes, Pane{
			PaneID:    id,
			RuntimeID: "rt-" + id,
			SessionID: "sess-" + id,
			Kind:      PaneKindAgent,
			Title:     DefaultPaneTitle,
			Status:    PaneStatusReady,
		})
	}
	return panes
}

// sameIDs compares two sorted id lists, treating a nil list and an empty one as
// the same thing: a tree with no tiles reports nil, and the model reports empty.
func sameIDs(got, want []string) bool {
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
