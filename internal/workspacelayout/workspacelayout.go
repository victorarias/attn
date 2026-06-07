package workspacelayout

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

const (
	DefaultPaneTitle  = "Agent"
	DefaultSplitRatio = 0.5
)

type Direction string

const (
	DirectionVertical   Direction = "vertical"
	DirectionHorizontal Direction = "horizontal"
)

type PaneKind string

const (
	PaneKindAgent PaneKind = "agent"
)

// TileKind labels a docked tile by the surface it renders. The layout package
// treats it as an opaque token: tiles are persisted by where they sit and how
// big they are, not by what they display. Rendering is entirely a client
// concern, so new kinds need no daemon change.
type TileKind string

const (
	// TileKindMarkdown is the first tile consumer. More kinds can be docked
	// without touching this package.
	TileKindMarkdown TileKind = "markdown"
	TileKindBrowser  TileKind = "browser"
)

type PaneStatus string

const (
	PaneStatusSpawning PaneStatus = "spawning"
	PaneStatusReady    PaneStatus = "ready"
	PaneStatusFailed   PaneStatus = "failed"
)

type Pane struct {
	PaneID    string
	RuntimeID string
	SessionID string
	Kind      PaneKind
	Title     string
	Status    PaneStatus
	Error     string
}

type Node struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
	// TileID and TileKind describe a docked tile leaf (Type == "tile").
	// Tiles are first-class layout citizens alongside agent panes: they take
	// real space, resize through the same split machinery, and persist with the
	// layout. TileKind is opaque to the daemon (see TileKind).
	TileID   string `json:"tile_id,omitempty"`
	TileKind string `json:"tile_kind,omitempty"`
	// TileParams is opaque to this package: it persists and reproduces with
	// the layout, but the daemon's layout machinery never interprets it. A
	// consumer (e.g. the markdown content service) reads it — for markdown it
	// holds the absolute path of the file the tile renders.
	TileParams string    `json:"tile_params,omitempty"`
	SplitID    string    `json:"split_id,omitempty"`
	Direction  Direction `json:"direction,omitempty"`
	Ratio      float64   `json:"ratio,omitempty"`
	// RatioLocked marks a split whose ratio the user set explicitly (by
	// dragging the divider) or that anchors a tile. Locked ratios survive
	// normalization instead of being rebalanced back to an equal split.
	RatioLocked bool   `json:"ratio_locked,omitempty"`
	Children    []Node `json:"children,omitempty"`
}

type WorkspaceLayout struct {
	WorkspaceID  string
	ActivePaneID string
	Layout       Node
	Panes        []Pane
	UpdatedAt    string
}

func DefaultLayout(paneID string) Node {
	return Node{
		Type:   "pane",
		PaneID: paneID,
	}
}

func DefaultWorkspaceLayout(workspaceID, paneID, sessionID string) WorkspaceLayout {
	return WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: paneID,
		Layout:       DefaultLayout(paneID),
		Panes: []Pane{
			{
				PaneID:    paneID,
				RuntimeID: sessionID,
				SessionID: sessionID,
				Kind:      PaneKindAgent,
				Title:     DefaultPaneTitle,
				Status:    PaneStatusReady,
			},
		},
	}
}

func EncodeLayout(node Node) (string, error) {
	data, err := json.Marshal(node)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func DecodeLayout(layoutJSON string) (Node, error) {
	if strings.TrimSpace(layoutJSON) == "" {
		return Node{}, nil
	}
	var node Node
	if err := json.Unmarshal([]byte(layoutJSON), &node); err != nil {
		return Node{}, err
	}
	return node, nil
}

func NormalizeWorkspaceLayout(snapshot WorkspaceLayout) WorkspaceLayout {
	normalized := snapshot

	panesByID := make(map[string]Pane, len(normalized.Panes))
	for _, pane := range normalized.Panes {
		paneID := strings.TrimSpace(pane.PaneID)
		if paneID == "" {
			continue
		}
		runtimeID := strings.TrimSpace(pane.RuntimeID)
		if runtimeID == "" {
			continue
		}
		sessionID := strings.TrimSpace(pane.SessionID)
		if sessionID == "" {
			continue
		}
		title := strings.TrimSpace(pane.Title)
		if title == "" {
			title = paneID
		}
		status := pane.Status
		if status == "" {
			status = PaneStatusReady
		}
		if status != PaneStatusSpawning && status != PaneStatusReady && status != PaneStatusFailed {
			status = PaneStatusReady
		}
		panesByID[paneID] = Pane{
			PaneID:    paneID,
			RuntimeID: runtimeID,
			SessionID: sessionID,
			Kind:      PaneKindAgent,
			Title:     title,
			Status:    status,
			Error:     strings.TrimSpace(pane.Error),
		}
	}

	normalized.Layout = NormalizeLayout(normalized.Layout, panesByID)
	paneIDs := PaneIDs(normalized.Layout)
	normalized.Panes = make([]Pane, 0, len(paneIDs))
	for _, paneID := range paneIDs {
		if pane, ok := panesByID[paneID]; ok {
			normalized.Panes = append(normalized.Panes, pane)
		}
	}
	if !slices.Contains(paneIDs, normalized.ActivePaneID) {
		normalized.ActivePaneID = ""
		if len(paneIDs) > 0 {
			normalized.ActivePaneID = paneIDs[0]
		}
	}
	return normalized
}

func NormalizeLayout(node Node, panesByID map[string]Pane) Node {
	normalized, empty := normalizeNode(node, panesByID)
	if empty {
		return fallbackLayout(panesByID)
	}
	return rebalanceSplitChains(normalized)
}

func fallbackLayout(panesByID map[string]Pane) Node {
	paneIDs := make([]string, 0, len(panesByID))
	for paneID := range panesByID {
		paneIDs = append(paneIDs, paneID)
	}
	sort.Strings(paneIDs)
	if len(paneIDs) == 0 {
		return Node{}
	}
	return DefaultLayout(paneIDs[0])
}

func rebalanceSplitChains(node Node) Node {
	if node.Type != "split" || len(node.Children) < 2 {
		return node
	}

	firstChild := rebalanceSplitChains(node.Children[0])
	secondChild := rebalanceSplitChains(node.Children[1])
	node.Children = []Node{firstChild, secondChild}

	// A user-set ratio is authoritative; never rebalance it back to equal.
	if node.RatioLocked {
		return node
	}

	firstSpan := splitChainSpanCount(firstChild, node.Direction)
	secondSpan := splitChainSpanCount(secondChild, node.Direction)
	if totalSpan := firstSpan + secondSpan; totalSpan > 0 {
		node.Ratio = float64(firstSpan) / float64(totalSpan)
	}
	return node
}

func splitChainSpanCount(node Node, direction Direction) int {
	// A locked split is an opaque unit: its children keep the user's ratio, so
	// an enclosing chain must not redistribute space through it.
	if node.Type != "split" || node.Direction != direction || len(node.Children) < 2 || node.RatioLocked {
		return 1
	}
	return splitChainSpanCount(node.Children[0], direction) + splitChainSpanCount(node.Children[1], direction)
}

func normalizeNode(node Node, panesByID map[string]Pane) (Node, bool) {
	switch node.Type {
	case "pane":
		if _, ok := panesByID[node.PaneID]; !ok {
			return Node{}, true
		}
		return Node{
			Type:   "pane",
			PaneID: node.PaneID,
		}, false
	case "tile":
		tileID := strings.TrimSpace(node.TileID)
		tileKind := strings.TrimSpace(node.TileKind)
		// A tile with no identity or kind is meaningless; drop it so an
		// orphaned tile can't wedge the layout. Tiles are otherwise never
		// pruned by pane bookkeeping — they have no entry in panesByID.
		if tileID == "" || tileKind == "" {
			return Node{}, true
		}
		return Node{
			Type:       "tile",
			TileID:     tileID,
			TileKind:   tileKind,
			TileParams: node.TileParams,
		}, false
	case "split":
		children := make([]Node, 0, 2)
		for _, child := range node.Children {
			next, empty := normalizeNode(child, panesByID)
			if !empty {
				children = append(children, next)
			}
		}
		switch len(children) {
		case 0:
			return Node{}, true
		case 1:
			return children[0], false
		default:
			direction := node.Direction
			if direction != DirectionVertical && direction != DirectionHorizontal {
				direction = DirectionVertical
			}
			ratio := node.Ratio
			if ratio <= 0 || ratio >= 1 {
				ratio = DefaultSplitRatio
			}
			splitID := strings.TrimSpace(node.SplitID)
			if splitID == "" {
				splitID = "split"
			}
			return Node{
				Type:        "split",
				SplitID:     splitID,
				Direction:   direction,
				Ratio:       ratio,
				RatioLocked: node.RatioLocked,
				Children:    children[:2],
			}, false
		}
	default:
		return Node{}, true
	}
}

func Split(node Node, targetPaneID, newPaneID, splitID string, direction Direction, ratio float64) (Node, bool) {
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}

	switch node.Type {
	case "pane":
		if node.PaneID != targetPaneID {
			return node, false
		}
		return Node{
			Type:      "split",
			SplitID:   splitID,
			Direction: direction,
			Ratio:     ratio,
			Children: []Node{
				{Type: "pane", PaneID: targetPaneID},
				{Type: "pane", PaneID: newPaneID},
			},
		}, true
	case "split":
		children := make([]Node, len(node.Children))
		copy(children, node.Children)
		for i, child := range children {
			next, changed := Split(child, targetPaneID, newPaneID, splitID, direction, ratio)
			if changed {
				children[i] = next
				node.Children = children
				return node, true
			}
		}
	}
	return node, false
}

// SetSplitRatio sets and locks the ratio of the split identified by splitID.
// The returned bool reports whether a matching split was found. The ratio is
// clamped to a small margin so neither side can collapse to zero.
func SetSplitRatio(node Node, splitID string, ratio float64) (Node, bool) {
	const margin = 0.05
	if ratio < margin {
		ratio = margin
	} else if ratio > 1-margin {
		ratio = 1 - margin
	}
	if node.Type != "split" || len(node.Children) < 2 {
		return node, false
	}
	if node.SplitID == splitID {
		node.Ratio = ratio
		node.RatioLocked = true
		return node, true
	}
	children := make([]Node, len(node.Children))
	copy(children, node.Children)
	for i, child := range children {
		next, changed := SetSplitRatio(child, splitID, ratio)
		if changed {
			children[i] = next
			node.Children = children
			return node, true
		}
	}
	return node, false
}

func Remove(node Node, paneID string) (Node, bool) {
	next, removed, empty := removeNode(node, paneID)
	if !removed {
		return node, false
	}
	if empty {
		return Node{}, true
	}
	return next, true
}

func removeNode(node Node, leafID string) (Node, bool, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID == leafID {
			return Node{}, true, true
		}
		return node, false, false
	case "tile":
		if node.TileID == leafID {
			return Node{}, true, true
		}
		return node, false, false
	case "split":
		children := make([]Node, 0, 2)
		removed := false
		for _, child := range node.Children {
			next, childRemoved, empty := removeNode(child, leafID)
			removed = removed || childRemoved
			if !empty {
				children = append(children, next)
			}
		}
		switch len(children) {
		case 0:
			return Node{}, removed, true
		case 1:
			return children[0], removed, false
		default:
			node.Children = children[:2]
			return node, removed, false
		}
	default:
		return Node{}, false, true
	}
}

func HasPane(node Node, paneID string) bool {
	switch node.Type {
	case "pane":
		return node.PaneID == paneID
	case "split":
		for _, child := range node.Children {
			if HasPane(child, paneID) {
				return true
			}
		}
	}
	return false
}

func PaneIDs(node Node) []string {
	var ids []string
	collectPaneIDs(node, &ids)
	return ids
}

func collectPaneIDs(node Node, ids *[]string) {
	switch node.Type {
	case "pane":
		*ids = append(*ids, node.PaneID)
	case "split":
		for _, child := range node.Children {
			collectPaneIDs(child, ids)
		}
	}
}

// HasTile reports whether a docked tile with the given id exists in the tree.
func HasTile(node Node, tileID string) bool {
	switch node.Type {
	case "tile":
		return node.TileID == tileID
	case "split":
		for _, child := range node.Children {
			if HasTile(child, tileID) {
				return true
			}
		}
	}
	return false
}

// TileIDs returns the ids of every docked tile in the tree.
func TileIDs(node Node) []string {
	var ids []string
	collectTileIDs(node, &ids)
	return ids
}

func collectTileIDs(node Node, ids *[]string) {
	switch node.Type {
	case "tile":
		*ids = append(*ids, node.TileID)
	case "split":
		for _, child := range node.Children {
			collectTileIDs(child, ids)
		}
	}
}

// hasLeaf reports whether a leaf (pane or tile) with the given id exists.
func hasLeaf(node Node, leafID string) bool {
	return HasPane(node, leafID) || HasTile(node, leafID)
}

// LayoutEmpty reports whether a layout holds no leaves at all — neither terminal
// panes nor docked tiles. A workspace is torn down only when its layout is
// empty: a tile the user deliberately left behind keeps the workspace alive
// even after its last terminal closes. Run this on a normalized layout, where
// orphaned/invalid leaves have already been pruned.
func LayoutEmpty(node Node) bool {
	return len(PaneIDs(node)) == 0 && len(TileIDs(node)) == 0
}

// findLeaf returns the leaf (pane or tile) with the given id so a move can
// re-insert it elsewhere with its identity intact — and, for tiles, its kind
// and params. The bool reports whether such a leaf exists. Leaves carry no
// children, so the returned node is self-contained.
func findLeaf(node Node, leafID string) (Node, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID == leafID {
			return node, true
		}
	case "tile":
		if node.TileID == leafID {
			return node, true
		}
	case "split":
		for _, child := range node.Children {
			if leaf, ok := findLeaf(child, leafID); ok {
				return leaf, true
			}
		}
	}
	return Node{}, false
}

// TileParamsByID returns the opaque params of the tile with the given id.
// The bool reports whether such a tile exists.
func TileParamsByID(node Node, tileID string) (string, bool) {
	switch node.Type {
	case "tile":
		if node.TileID == tileID {
			return node.TileParams, true
		}
	case "split":
		for _, child := range node.Children {
			if params, ok := TileParamsByID(child, tileID); ok {
				return params, true
			}
		}
	}
	return "", false
}

// UpdateTileParams replaces the opaque params for an existing tile.
func UpdateTileParams(node Node, tileID, tileParams string) (Node, bool) {
	switch node.Type {
	case "tile":
		if node.TileID != tileID {
			return node, false
		}
		node.TileParams = strings.TrimSpace(tileParams)
		return node, true
	case "split":
		for index, child := range node.Children {
			updated, ok := UpdateTileParams(child, tileID, tileParams)
			if !ok {
				continue
			}
			node.Children[index] = updated
			return node, true
		}
	}
	return node, false
}

// TileFractionByID returns the share of its immediate split occupied by a
// tile. Docking uses this when moving an existing tile so a user resize
// survives re-docking.
func TileFractionByID(node Node, tileID string) (float64, bool) {
	if node.Type != "split" {
		return 0, false
	}
	if len(node.Children) == 2 {
		if node.Children[0].Type == "tile" && node.Children[0].TileID == tileID {
			return node.Ratio, true
		}
		if node.Children[1].Type == "tile" && node.Children[1].TileID == tileID {
			return 1 - node.Ratio, true
		}
	}
	for _, child := range node.Children {
		if fraction, ok := TileFractionByID(child, tileID); ok {
			return fraction, true
		}
	}
	return 0, false
}

// TileLeaf is a flattened view of a docked tile for consumers that need to
// act on tiles (e.g. the markdown content service) without walking the tree.
type TileLeaf struct {
	TileID     string
	TileKind   string
	TileParams string
}

// TileLeaves returns every docked tile in the tree as a flat slice.
func TileLeaves(node Node) []TileLeaf {
	var leaves []TileLeaf
	collectTileLeaves(node, &leaves)
	return leaves
}

func collectTileLeaves(node Node, leaves *[]TileLeaf) {
	switch node.Type {
	case "tile":
		*leaves = append(*leaves, TileLeaf{
			TileID:     node.TileID,
			TileKind:   node.TileKind,
			TileParams: node.TileParams,
		})
	case "split":
		for _, child := range node.Children {
			collectTileLeaves(child, leaves)
		}
	}
}

// DockTile inserts (or moves) a tile leaf beside the anchor leaf. Docking is
// idempotent and doubles as a move: any existing instance of tileID is removed
// first, then the tile is re-inserted at the new anchor. `before` controls
// which side of the anchor the tile lands on (children[0] when true), and
// `direction` whether the new split is side-by-side (vertical) or stacked
// (horizontal). `ratio` is the children[0] fraction, like every other split.
//
// The anchor may be any leaf — a terminal pane or another tile — so tiles can
// be docked between existing panes or next to one another. The new split is
// RatioLocked so a tile keeps its size instead of being equalized with
// terminals during normalization.
func DockTile(node Node, anchorID string, direction Direction, before bool, splitID, tileID, tileKind, tileParams string, ratio float64) (Node, bool) {
	tileID = strings.TrimSpace(tileID)
	tileKind = strings.TrimSpace(tileKind)
	anchorID = strings.TrimSpace(anchorID)
	if tileID == "" || tileKind == "" || anchorID == "" || anchorID == tileID {
		return node, false
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}
	if strings.TrimSpace(splitID) == "" {
		splitID = "split"
	}
	if HasPane(node, tileID) {
		return node, false
	}

	// Move semantics: drop any existing instance so a re-dock relocates rather
	// than duplicates the tile.
	cleaned := node
	if next, removed := Remove(node, tileID); removed {
		cleaned = next
	}
	if cleaned.Type == "" || !hasLeaf(cleaned, anchorID) {
		return node, false
	}

	tile := Node{Type: "tile", TileID: tileID, TileKind: tileKind, TileParams: strings.TrimSpace(tileParams)}
	next, ok := insertBesideLeaf(cleaned, anchorID, direction, before, splitID, ratio, tile)
	if !ok {
		return node, false
	}
	return next, true
}

func insertBesideLeaf(node Node, anchorID string, direction Direction, before bool, splitID string, ratio float64, tile Node) (Node, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID != anchorID {
			return node, false
		}
		return lockedSplit(node, tile, direction, before, splitID, ratio), true
	case "tile":
		if node.TileID != anchorID {
			return node, false
		}
		return lockedSplit(node, tile, direction, before, splitID, ratio), true
	case "split":
		children := make([]Node, len(node.Children))
		copy(children, node.Children)
		for i, child := range children {
			next, changed := insertBesideLeaf(child, anchorID, direction, before, splitID, ratio, tile)
			if changed {
				children[i] = next
				node.Children = children
				return node, true
			}
		}
	}
	return node, false
}

// lockedSplit pairs an existing subtree with an incoming leaf under a new split.
// The split is ratio-locked because the ratio came from an explicit user gesture
// — a tile dock, or a leaf dropped at a chosen depth — so normalization keeps it
// instead of rebalancing the pair back to an equal split. `before` places the
// incoming leaf as children[0] (its left/top side).
func lockedSplit(existing, incoming Node, direction Direction, before bool, splitID string, ratio float64) Node {
	children := []Node{existing, incoming}
	if before {
		children = []Node{incoming, existing}
	}
	return Node{
		Type:        "split",
		SplitID:     splitID,
		Direction:   direction,
		Ratio:       ratio,
		RatioLocked: true,
		Children:    children,
	}
}

// MoveLeaf relocates an existing leaf (pane or tile) so it sits beside anchorID
// on the given side. When anchorID is empty the leaf docks against the whole
// workspace: the entire remaining layout becomes one side of a new root split and
// the moved leaf the other (a "container" dock). The moved leaf keeps its
// identity, and for tiles its kind and params. The new split is ratio-locked
// because the ratio came from the user's drop, so normalization preserves the
// chosen size. `ratio` is the children[0] fraction, matching DockTile.
//
// It is a no-op (returns the input and false) when the move can't or shouldn't
// happen:
//   - leafID is empty, or equals anchorID (dropping a leaf on itself)
//   - leafID is not in the tree
//   - leafID is the only leaf, so removing it would leave nothing to dock against
//   - anchorID is non-empty but missing once the leaf is pulled out
func MoveLeaf(node Node, leafID, anchorID, splitID string, direction Direction, before bool, ratio float64) (Node, bool) {
	leafID = strings.TrimSpace(leafID)
	anchorID = strings.TrimSpace(anchorID)
	if leafID == "" || leafID == anchorID {
		return node, false
	}
	moved, found := findLeaf(node, leafID)
	if !found {
		return node, false
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}
	if strings.TrimSpace(splitID) == "" {
		splitID = "split"
	}

	cleaned, removed := Remove(node, leafID)
	if !removed || cleaned.Type == "" {
		// The leaf was the only thing in the tree; there's nowhere to move it.
		return node, false
	}

	if anchorID == "" {
		return lockedSplit(cleaned, moved, direction, before, splitID, ratio), true
	}
	if !hasLeaf(cleaned, anchorID) {
		return node, false
	}
	next, ok := insertBesideLeaf(cleaned, anchorID, direction, before, splitID, ratio, moved)
	if !ok {
		return node, false
	}
	return next, true
}

type MoveBetweenLayoutsResult struct {
	SourceLayout Node
	TargetLayout Node
	Leaf         Node
	FinalLeafID  string
}

// MoveLeafBetweenLayouts removes a leaf from source and inserts it into target.
// It is used for cross-workspace moves where the pane/tile metadata lives
// outside the tree and must be carried by the caller. If the target already has
// a leaf with the moved id, conflictSuffix is appended to the moved leaf id
// before insertion. An empty target layout accepts the moved leaf as its root.
func MoveLeafBetweenLayouts(source, target Node, leafID, anchorID, splitID string, direction Direction, before bool, ratio float64, conflictSuffix string) (MoveBetweenLayoutsResult, bool) {
	leafID = strings.TrimSpace(leafID)
	anchorID = strings.TrimSpace(anchorID)
	if leafID == "" {
		return MoveBetweenLayoutsResult{}, false
	}
	moved, found := findLeaf(source, leafID)
	if !found {
		return MoveBetweenLayoutsResult{}, false
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}
	if strings.TrimSpace(splitID) == "" {
		splitID = "split"
	}

	cleanedSource, removed := Remove(source, leafID)
	if !removed {
		return MoveBetweenLayoutsResult{}, false
	}

	finalLeafID := leafID
	if hasLeaf(target, finalLeafID) {
		suffix := strings.TrimSpace(conflictSuffix)
		if suffix == "" {
			suffix = "moved"
		}
		finalLeafID = finalLeafID + "-" + suffix
		if moved.Type == "pane" {
			moved.PaneID = finalLeafID
		} else if moved.Type == "tile" {
			moved.TileID = finalLeafID
		}
	}
	if hasLeaf(target, finalLeafID) {
		return MoveBetweenLayoutsResult{}, false
	}

	var nextTarget Node
	if target.Type == "" || LayoutEmpty(target) {
		nextTarget = moved
	} else if anchorID == "" {
		nextTarget = lockedSplit(target, moved, direction, before, splitID, ratio)
	} else {
		if !hasLeaf(target, anchorID) {
			return MoveBetweenLayoutsResult{}, false
		}
		inserted, ok := insertBesideLeaf(target, anchorID, direction, before, splitID, ratio, moved)
		if !ok {
			return MoveBetweenLayoutsResult{}, false
		}
		nextTarget = inserted
	}

	return MoveBetweenLayoutsResult{
		SourceLayout: cleanedSource,
		TargetLayout: nextTarget,
		Leaf:         moved,
		FinalLeafID:  finalLeafID,
	}, true
}

// UndockTile removes a docked tile from the tree, collapsing the split that
// held it so its sibling reclaims the space. The bool reports whether a tile
// was found and removed.
func UndockTile(node Node, tileID string) (Node, bool) {
	if !HasTile(node, strings.TrimSpace(tileID)) {
		return node, false
	}
	next, _ := Remove(node, strings.TrimSpace(tileID))
	return next, true
}
