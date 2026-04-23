package sessionlayout

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

const (
	MainPaneID        = "main"
	DefaultPaneTitle  = "Session"
	DefaultSplitRatio = 0.5
)

type Direction string

const (
	DirectionVertical   Direction = "vertical"
	DirectionHorizontal Direction = "horizontal"
)

type PaneKind string

const (
	PaneKindMain  PaneKind = "main"
	PaneKindShell PaneKind = "shell"
)

type Pane struct {
	PaneID    string
	RuntimeID string
	Kind      PaneKind
	Title     string
}

type Node struct {
	Type      string    `json:"type"`
	PaneID    string    `json:"pane_id,omitempty"`
	SplitID   string    `json:"split_id,omitempty"`
	Direction Direction `json:"direction,omitempty"`
	Ratio     float64   `json:"ratio,omitempty"`
	Children  []Node    `json:"children,omitempty"`
}

type SessionLayout struct {
	SessionID    string
	ActivePaneID string
	Layout       Node
	Panes        []Pane
	UpdatedAt    string
}

func DefaultLayout() Node {
	return Node{
		Type:   "pane",
		PaneID: MainPaneID,
	}
}

func DefaultSessionLayout(sessionID string) SessionLayout {
	return SessionLayout{
		SessionID:    sessionID,
		ActivePaneID: MainPaneID,
		Layout:       DefaultLayout(),
		Panes: []Pane{
			{
				PaneID:    MainPaneID,
				RuntimeID: sessionID,
				Kind:      PaneKindMain,
				Title:     DefaultPaneTitle,
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
		return DefaultLayout(), nil
	}
	var node Node
	if err := json.Unmarshal([]byte(layoutJSON), &node); err != nil {
		return DefaultLayout(), err
	}
	return node, nil
}

func NormalizeSessionLayout(snapshot SessionLayout, mainRuntimeID string) SessionLayout {
	normalized := snapshot
	if normalized.SessionID == "" {
		normalized.SessionID = strings.TrimSpace(mainRuntimeID)
	}

	panesByID := make(map[string]Pane, len(normalized.Panes)+1)
	panesByID[MainPaneID] = Pane{
		PaneID:    MainPaneID,
		RuntimeID: strings.TrimSpace(mainRuntimeID),
		Kind:      PaneKindMain,
		Title:     DefaultPaneTitle,
	}

	for _, pane := range normalized.Panes {
		paneID := strings.TrimSpace(pane.PaneID)
		if paneID == "" || paneID == MainPaneID {
			continue
		}
		runtimeID := strings.TrimSpace(pane.RuntimeID)
		if runtimeID == "" {
			continue
		}
		title := strings.TrimSpace(pane.Title)
		if title == "" {
			title = paneID
		}
		panesByID[paneID] = Pane{
			PaneID:    paneID,
			RuntimeID: runtimeID,
			Kind:      PaneKindShell,
			Title:     title,
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
		normalized.ActivePaneID = MainPaneID
	}
	if !slices.Contains(paneIDs, normalized.ActivePaneID) && len(paneIDs) > 0 {
		normalized.ActivePaneID = paneIDs[0]
	}
	return normalized
}

func NormalizeLayout(node Node, panesByID map[string]Pane) Node {
	normalized, empty := normalizeNode(node, panesByID)
	if empty {
		return DefaultLayout()
	}
	return rebalanceSplitChains(normalized)
}

func rebalanceSplitChains(node Node) Node {
	if node.Type != "split" || len(node.Children) < 2 {
		return node
	}

	firstChild := rebalanceSplitChains(node.Children[0])
	secondChild := rebalanceSplitChains(node.Children[1])
	node.Children = []Node{firstChild, secondChild}

	firstSpan := splitChainSpanCount(firstChild, node.Direction)
	secondSpan := splitChainSpanCount(secondChild, node.Direction)
	if totalSpan := firstSpan + secondSpan; totalSpan > 0 {
		node.Ratio = float64(firstSpan) / float64(totalSpan)
	}
	return node
}

func splitChainSpanCount(node Node, direction Direction) int {
	if node.Type != "split" || node.Direction != direction || len(node.Children) < 2 {
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
				Type:      "split",
				SplitID:   splitID,
				Direction: direction,
				Ratio:     ratio,
				Children:  children[:2],
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

func Remove(node Node, paneID string) (Node, bool) {
	next, removed, empty := removeNode(node, paneID)
	if !removed {
		return node, false
	}
	if empty {
		return DefaultLayout(), true
	}
	return next, true
}

func removeNode(node Node, paneID string) (Node, bool, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID == paneID {
			return Node{}, true, true
		}
		return node, false, false
	case "split":
		children := make([]Node, 0, 2)
		removed := false
		for _, child := range node.Children {
			next, childRemoved, empty := removeNode(child, paneID)
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

func SortedShellRuntimeIDs(snapshot SessionLayout) []string {
	runtimeIDs := make([]string, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		if pane.Kind != PaneKindShell {
			continue
		}
		runtimeID := strings.TrimSpace(pane.RuntimeID)
		if runtimeID == "" {
			continue
		}
		runtimeIDs = append(runtimeIDs, runtimeID)
	}
	sort.Strings(runtimeIDs)
	return runtimeIDs
}
