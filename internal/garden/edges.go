package garden

import (
	"fmt"
	"slices"
	"strings"
)

// Edges are the garden's structure and `ready` is the question they answer:
// what can be tended right now. Both are pure functions over a set of seeds, so
// the whole rule set is one readable table and one table-driven test; the daemon
// reads the garden, calls in here, and writes back what it is handed.

// Edge kinds. `blocks` and `part-of` are the two a caller may link; the others
// are declared so a body written by a later slice stays readable, and so a kind
// that arrives later is additive rather than a migration.
const (
	EdgeBlocks         = "blocks"
	EdgePartOf         = "part-of"
	EdgeSownFrom       = "sown-from"
	EdgeDiscoveredFrom = "discovered-from"
	EdgeRelatesTo      = "relates-to"
)

// LinkableKinds is what `attn seed link` accepts, in the order a refusal lists
// them.
var LinkableKinds = []string{EdgeBlocks, EdgePartOf}

// ParseEdgeKind reads a kind off the command line, naming the whole set when it
// is not one — including the kinds that exist in the schema but cannot be
// linked yet, so a caller reading the type list is not told they do not exist.
func ParseEdgeKind(raw string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(raw))
	if slices.Contains(LinkableKinds, kind) {
		return kind, nil
	}
	if slices.Contains([]string{EdgeSownFrom, EdgeDiscoveredFrom, EdgeRelatesTo}, kind) {
		return "", fmt.Errorf("%q is a real edge kind but nothing links one yet; the kinds you can link are %s",
			kind, strings.Join(LinkableKinds, " and "))
	}
	return "", fmt.Errorf("%q is not how two seeds relate; the kinds are %s", raw, strings.Join(LinkableKinds, " and "))
}

// Link adds one edge to from and hands back the seed as it should be stored. The
// second return is false when the edge was already there — nothing to write, and
// the caller says so rather than publishing a fact about a garden that did not
// move.
//
// seeds is the whole garden because a cycle is a property of the graph and not
// of the two seeds named: `a blocks b` is legal until b already blocks a through
// any chain.
func Link(seeds []Seed, fromID, kind, toID string) (Seed, bool, error) {
	from, to, err := linkEnds(seeds, fromID, kind, toID)
	if err != nil {
		return Seed{}, false, err
	}
	if slices.Contains(from.Edges, Edge{Kind: kind, To: to.ID}) {
		return from, false, nil
	}
	if kind == EdgePartOf {
		if parent, ok := parentOf(from); ok {
			return Seed{}, false, fmt.Errorf(
				"%s is already part of %s, and a seed sits in one plot: `attn seed unlink %s part-of %s` first, then link it to %s",
				from.ID, parent, from.ID, parent, to.ID)
		}
	}
	if path := reaches(seeds, to.ID, kind, from.ID); len(path) > 0 {
		return Seed{}, false, cycleRefusal(from.ID, kind, to.ID, path)
	}
	next := from
	next.Edges = append(slices.Clone(from.Edges), Edge{Kind: kind, To: to.ID})
	return next, true, nil
}

// Unlink removes one edge, refusing when it is not there and naming what the
// seed is actually linked to — an unlink that silently does nothing reads as a
// removal that happened.
func Unlink(seeds []Seed, fromID, kind, toID string) (Seed, error) {
	from, to, err := linkEnds(seeds, fromID, kind, toID)
	if err != nil {
		return Seed{}, err
	}
	edge := Edge{Kind: kind, To: to.ID}
	index := slices.Index(from.Edges, edge)
	if index < 0 {
		return Seed{}, fmt.Errorf("%s does not %s %s%s", from.ID, kind, to.ID, edgeInventory(from))
	}
	next := from
	next.Edges = slices.Delete(slices.Clone(from.Edges), index, index+1)
	return next, nil
}

// linkEnds resolves both ends of a link and refuses the two ways a pair of ids
// cannot be one.
func linkEnds(seeds []Seed, fromID, kind, toID string) (Seed, Seed, error) {
	from, ok := find(seeds, fromID)
	if !ok {
		return Seed{}, Seed{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", fromID)
	}
	to, ok := find(seeds, toID)
	if !ok {
		return Seed{}, Seed{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", toID)
	}
	if from.ID == to.ID {
		return Seed{}, Seed{}, fmt.Errorf("%s cannot %s itself", from.ID, kind)
	}
	return from, to, nil
}

// edgeInventory renders what a seed is linked to, for a refusal that would
// otherwise leave the caller guessing which edge they meant.
func edgeInventory(seed Seed) string {
	if len(seed.Edges) == 0 {
		return "; it has no edges at all"
	}
	rendered := make([]string, 0, len(seed.Edges))
	for _, edge := range seed.Edges {
		rendered = append(rendered, fmt.Sprintf("%s %s", edge.Kind, edge.To))
	}
	return fmt.Sprintf("; it is linked %s", strings.Join(rendered, ", "))
}

// cycleRefusal names both seeds, the chain that already runs between them, and
// the edge to remove. A cycle in `blocks` is a deadlock — neither seed ever
// becomes ready — and a cycle in `part-of` is a plot inside itself.
func cycleRefusal(fromID, kind, toID string, path []string) error {
	chain := strings.Join(append([]string{toID}, path...), " → ")
	if kind == EdgePartOf {
		return fmt.Errorf(
			"%s is already inside %s (%s), so making %s part of %s would put the plot inside itself.\n"+
				"Unlink an edge in that chain first: attn seed unlink %s part-of %s",
			toID, fromID, chain, fromID, toID, toID, path[0])
	}
	return fmt.Errorf(
		"%s already blocks %s (%s), so %s blocking %s would deadlock them — neither would ever be ready.\n"+
			"Unlink an edge in that chain first: attn seed unlink %s blocks %s",
		toID, fromID, chain, fromID, toID, toID, path[0])
}

// reaches walks edges of one kind from start and returns the rest of the path to
// target, or nil when there is none. The visited set is what keeps a body that
// already carries a cycle from looping here forever.
func reaches(seeds []Seed, start, kind, target string) []string {
	index := byID(seeds)
	visited := map[string]bool{start: true}
	var walk func(id string) []string
	walk = func(id string) []string {
		for _, edge := range index[id].Edges {
			if edge.Kind != kind {
				continue
			}
			if edge.To == target {
				return []string{target}
			}
			if visited[edge.To] {
				continue
			}
			visited[edge.To] = true
			if rest := walk(edge.To); rest != nil {
				return append([]string{edge.To}, rest...)
			}
		}
		return nil
	}
	return walk(start)
}

// Ready reports which seeds can be tended right now, in the order they were
// given. Truth at query time: nothing is stored, so harvesting a blocker makes
// its dependent ready at the next call.
//
// A seed is ready when it is open (not closed, not parked), nothing is part of
// it — a crown's work is its children, not the crown — no unclosed seed blocks
// it, it is neither a packet nor under one, it is not a gate (a gate wants a
// person and opens a turn, which is its own later slice), and no live tender
// holds it.
//
// sessionLive answers whether a tender's session is still one the daemon knows;
// Tender.Holds is the rule it feeds, shared with the claim `tend` makes, so a
// seed offered here is one `tend` accepts.
func Ready(seeds []Seed, sessionLive func(sessionID string) bool) []Seed {
	index := byID(seeds)
	blocked := blockedIDs(seeds)
	parents := parentIDs(seeds)
	ready := make([]Seed, 0, len(seeds))
	for _, seed := range seeds {
		switch {
		case Closed(seed.Status), seed.Status == StatusDormant:
			continue
		case parents[seed.ID], blocked[seed.ID], seed.Gate:
			continue
		case underTemplate(index, seed):
			continue
		}
		if seed.Tender().Holds(sessionLive) {
			continue
		}
		ready = append(ready, seed)
	}
	return ready
}

// Blockers names the unclosed seeds that block one seed, in the order they were
// given. It is what a surface renders beside a seed that is not ready.
func Blockers(seeds []Seed, id string) []string {
	out := []string{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		for _, edge := range seed.Edges {
			if edge.Kind == EdgeBlocks && edge.To == id {
				out = append(out, seed.ID)
			}
		}
	}
	return out
}

// InPlot is the seeds of one plot: its crown and everything part-of it,
// transitively. The crown itself is included — it is a seed like any other, and
// the readiness rules decide what a caller may tend.
func InPlot(seeds []Seed, crownID string) []Seed {
	inside := map[string]bool{crownID: true}
	// Repeat until nothing new joins: a child may be listed before its parent,
	// and one pass would then miss the grandchildren.
	for grew := true; grew; {
		grew = false
		for _, seed := range seeds {
			if inside[seed.ID] {
				continue
			}
			if parent, ok := parentOf(seed); ok && inside[parent] {
				inside[seed.ID] = true
				grew = true
			}
		}
	}
	out := make([]Seed, 0, len(inside))
	for _, seed := range seeds {
		if inside[seed.ID] {
			out = append(out, seed)
		}
	}
	return out
}

// Relation is one edge as it reads from a seed's own point of view: outbound as
// it is stored, inbound named the other way round, so `show` answers both "what
// does this block" and "what blocks this".
type Relation struct {
	Label string
	Seed  string
}

// Inbound labels. Only the two linkable kinds have a name from the other side;
// anything else is reported as inbound rather than given a word this repo does
// not use yet.
var inboundLabels = map[string]string{
	EdgeBlocks: "blocked-by",
	EdgePartOf: "has-part",
}

func inboundLabel(kind string) string {
	if label, ok := inboundLabels[kind]; ok {
		return label
	}
	return "inbound " + kind
}

// Relations lists one seed's edges in both directions, outbound first.
func Relations(seeds []Seed, id string) []Relation {
	out := []Relation{}
	if seed, ok := find(seeds, id); ok {
		for _, edge := range seed.Edges {
			out = append(out, Relation{Label: edge.Kind, Seed: edge.To})
		}
	}
	for _, seed := range seeds {
		if seed.ID == id {
			continue
		}
		for _, edge := range seed.Edges {
			if edge.To == id {
				out = append(out, Relation{Label: inboundLabel(edge.Kind), Seed: seed.ID})
			}
		}
	}
	return out
}

// TreeRow is one line of `attn seed ls --tree`: a seed and how deep it sits
// under its crown.
type TreeRow struct {
	Seed  Seed
	Depth int
}

// Tree orders seeds by the `part-of` hierarchy, parents before their children,
// keeping the given order within each level. A seed whose parent is not in the
// list renders at the top: a scoped list must show everything it holds, even a
// child whose crown lives in another workspace.
func Tree(seeds []Seed) []TreeRow {
	children := map[string][]Seed{}
	roots := make([]Seed, 0, len(seeds))
	present := byID(seeds)
	for _, seed := range seeds {
		parent, ok := parentOf(seed)
		if _, inScope := present[parent]; ok && inScope {
			children[parent] = append(children[parent], seed)
			continue
		}
		roots = append(roots, seed)
	}
	rows := make([]TreeRow, 0, len(seeds))
	placed := map[string]bool{}
	var place func(seed Seed, depth int)
	place = func(seed Seed, depth int) {
		// A stored cycle would otherwise recurse forever; the seeds it holds are
		// then unreachable from any root and land at the end of the list.
		if placed[seed.ID] {
			return
		}
		placed[seed.ID] = true
		rows = append(rows, TreeRow{Seed: seed, Depth: depth})
		for _, child := range children[seed.ID] {
			place(child, depth+1)
		}
	}
	for _, root := range roots {
		place(root, 0)
	}
	for _, seed := range seeds {
		place(seed, 0)
	}
	return rows
}

// parentOf is the seed's crown, if it sits in a plot. A seed has at most one:
// Link refuses the second.
func parentOf(seed Seed) (string, bool) {
	for _, edge := range seed.Edges {
		if edge.Kind == EdgePartOf {
			return edge.To, true
		}
	}
	return "", false
}

// underTemplate reports whether a seed is a packet or lives inside one — a
// packet's subtree is a shape waiting to be sown, not work anybody tends.
func underTemplate(index map[string]Seed, seed Seed) bool {
	seen := map[string]bool{}
	for {
		if seed.Template {
			return true
		}
		parent, ok := parentOf(seed)
		if !ok || seen[parent] {
			return false
		}
		seen[parent] = true
		seed, ok = index[parent]
		if !ok {
			return false
		}
	}
}

func blockedIDs(seeds []Seed) map[string]bool {
	blocked := map[string]bool{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		for _, edge := range seed.Edges {
			if edge.Kind == EdgeBlocks {
				blocked[edge.To] = true
			}
		}
	}
	return blocked
}

// parentIDs is every seed something is part of — the crowns.
func parentIDs(seeds []Seed) map[string]bool {
	parents := map[string]bool{}
	for _, seed := range seeds {
		if parent, ok := parentOf(seed); ok {
			parents[parent] = true
		}
	}
	return parents
}

func byID(seeds []Seed) map[string]Seed {
	index := make(map[string]Seed, len(seeds))
	for _, seed := range seeds {
		index[seed.ID] = seed
	}
	return index
}

func find(seeds []Seed, id string) (Seed, bool) {
	for _, seed := range seeds {
		if seed.ID == id {
			return seed, true
		}
	}
	return Seed{}, false
}
