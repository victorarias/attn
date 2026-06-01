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

// PanelKind labels a docked panel by the surface it renders. The layout package
// treats it as an opaque token: panels are persisted by where they sit and how
// big they are, not by what they display. Rendering is entirely a client
// concern, so new kinds need no daemon change.
type PanelKind string

const (
	// PanelKindMarkdown is the first panel consumer. More kinds can be docked
	// without touching this package.
	PanelKindMarkdown PanelKind = "markdown"
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
	// PanelID and PanelKind describe a docked panel leaf (Type == "panel").
	// Panels are first-class layout citizens alongside agent panes: they take
	// real space, resize through the same split machinery, and persist with the
	// layout. PanelKind is opaque to the daemon (see PanelKind).
	PanelID   string `json:"panel_id,omitempty"`
	PanelKind string `json:"panel_kind,omitempty"`
	// PanelParams is opaque to this package: it persists and reproduces with
	// the layout, but the daemon's layout machinery never interprets it. A
	// consumer (e.g. the markdown content service) reads it — for markdown it
	// holds the absolute path of the file the panel renders.
	PanelParams string    `json:"panel_params,omitempty"`
	SplitID     string    `json:"split_id,omitempty"`
	Direction   Direction `json:"direction,omitempty"`
	Ratio       float64   `json:"ratio,omitempty"`
	// RatioLocked marks a split whose ratio the user set explicitly (by
	// dragging the divider) or that anchors a panel. Locked ratios survive
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
	case "panel":
		panelID := strings.TrimSpace(node.PanelID)
		panelKind := strings.TrimSpace(node.PanelKind)
		// A panel with no identity or kind is meaningless; drop it so an
		// orphaned panel can't wedge the layout. Panels are otherwise never
		// pruned by pane bookkeeping — they have no entry in panesByID.
		if panelID == "" || panelKind == "" {
			return Node{}, true
		}
		return Node{
			Type:        "panel",
			PanelID:     panelID,
			PanelKind:   panelKind,
			PanelParams: node.PanelParams,
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
	case "panel":
		if node.PanelID == leafID {
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

// HasPanel reports whether a docked panel with the given id exists in the tree.
func HasPanel(node Node, panelID string) bool {
	switch node.Type {
	case "panel":
		return node.PanelID == panelID
	case "split":
		for _, child := range node.Children {
			if HasPanel(child, panelID) {
				return true
			}
		}
	}
	return false
}

// PanelIDs returns the ids of every docked panel in the tree.
func PanelIDs(node Node) []string {
	var ids []string
	collectPanelIDs(node, &ids)
	return ids
}

func collectPanelIDs(node Node, ids *[]string) {
	switch node.Type {
	case "panel":
		*ids = append(*ids, node.PanelID)
	case "split":
		for _, child := range node.Children {
			collectPanelIDs(child, ids)
		}
	}
}

// hasLeaf reports whether a leaf (pane or panel) with the given id exists.
func hasLeaf(node Node, leafID string) bool {
	return HasPane(node, leafID) || HasPanel(node, leafID)
}

// PanelParamsByID returns the opaque params of the panel with the given id.
// The bool reports whether such a panel exists.
func PanelParamsByID(node Node, panelID string) (string, bool) {
	switch node.Type {
	case "panel":
		if node.PanelID == panelID {
			return node.PanelParams, true
		}
	case "split":
		for _, child := range node.Children {
			if params, ok := PanelParamsByID(child, panelID); ok {
				return params, true
			}
		}
	}
	return "", false
}

// PanelLeaf is a flattened view of a docked panel for consumers that need to
// act on panels (e.g. the markdown content service) without walking the tree.
type PanelLeaf struct {
	PanelID     string
	PanelKind   string
	PanelParams string
}

// PanelLeaves returns every docked panel in the tree as a flat slice.
func PanelLeaves(node Node) []PanelLeaf {
	var leaves []PanelLeaf
	collectPanelLeaves(node, &leaves)
	return leaves
}

func collectPanelLeaves(node Node, leaves *[]PanelLeaf) {
	switch node.Type {
	case "panel":
		*leaves = append(*leaves, PanelLeaf{
			PanelID:     node.PanelID,
			PanelKind:   node.PanelKind,
			PanelParams: node.PanelParams,
		})
	case "split":
		for _, child := range node.Children {
			collectPanelLeaves(child, leaves)
		}
	}
}

// DockPanel inserts (or moves) a panel leaf beside the anchor leaf. Docking is
// idempotent and doubles as a move: any existing instance of panelID is removed
// first, then the panel is re-inserted at the new anchor. `before` controls
// which side of the anchor the panel lands on (children[0] when true), and
// `direction` whether the new split is side-by-side (vertical) or stacked
// (horizontal). `ratio` is the children[0] fraction, like every other split.
//
// The anchor may be any leaf — a terminal pane or another panel — so panels can
// be docked between existing panes or next to one another. The new split is
// RatioLocked so a panel keeps its size instead of being equalized with
// terminals during normalization.
func DockPanel(node Node, anchorID string, direction Direction, before bool, splitID, panelID, panelKind, panelParams string, ratio float64) (Node, bool) {
	panelID = strings.TrimSpace(panelID)
	panelKind = strings.TrimSpace(panelKind)
	anchorID = strings.TrimSpace(anchorID)
	if panelID == "" || panelKind == "" || anchorID == "" || anchorID == panelID {
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
	if HasPane(node, panelID) {
		return node, false
	}

	// Move semantics: drop any existing instance so a re-dock relocates rather
	// than duplicates the panel.
	cleaned := node
	if next, removed := Remove(node, panelID); removed {
		cleaned = next
	}
	if cleaned.Type == "" || !hasLeaf(cleaned, anchorID) {
		return node, false
	}

	panel := Node{Type: "panel", PanelID: panelID, PanelKind: panelKind, PanelParams: strings.TrimSpace(panelParams)}
	next, ok := insertBesideLeaf(cleaned, anchorID, direction, before, splitID, ratio, panel)
	if !ok {
		return node, false
	}
	return next, true
}

func insertBesideLeaf(node Node, anchorID string, direction Direction, before bool, splitID string, ratio float64, panel Node) (Node, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID != anchorID {
			return node, false
		}
		return panelSplit(node, panel, direction, before, splitID, ratio), true
	case "panel":
		if node.PanelID != anchorID {
			return node, false
		}
		return panelSplit(node, panel, direction, before, splitID, ratio), true
	case "split":
		children := make([]Node, len(node.Children))
		copy(children, node.Children)
		for i, child := range children {
			next, changed := insertBesideLeaf(child, anchorID, direction, before, splitID, ratio, panel)
			if changed {
				children[i] = next
				node.Children = children
				return node, true
			}
		}
	}
	return node, false
}

func panelSplit(anchor, panel Node, direction Direction, before bool, splitID string, ratio float64) Node {
	children := []Node{anchor, panel}
	if before {
		children = []Node{panel, anchor}
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

// UndockPanel removes a docked panel from the tree, collapsing the split that
// held it so its sibling reclaims the space. The bool reports whether a panel
// was found and removed.
func UndockPanel(node Node, panelID string) (Node, bool) {
	if !HasPanel(node, strings.TrimSpace(panelID)) {
		return node, false
	}
	next, _ := Remove(node, strings.TrimSpace(panelID))
	return next, true
}
