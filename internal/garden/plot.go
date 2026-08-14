package garden

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// A plot in motion. Progress is what a crown row answers at a glance — how far
// the plot has drained — and Stale is the judgment query: which seeds claim
// attention they are not getting. Both are pure functions over seeds, like the
// rest of this package, so every surface renders the same answer.

// Progress is one plot's children counted by where they stand. The crown is not
// counted — its work is its children, and it closes by harvest, not by tending.
//
// Ready and Blocked overlap nothing: ready is open with no open blocker and no
// holder, blocked is open with at least one open blocker. Together with Growing
// and Dormant they still do not partition the open set — a planted seed whose
// plot-mate holds the only open path is neither — so Total is carried rather
// than derived.
type Progress struct {
	Total    int
	Done     int
	Withered int
	Growing  int
	Dormant  int
	Ready    int
	Blocked  int
}

// PlotProgress counts one crown's plot. ready is the garden-wide readiness
// answer (from Ready), so progress and `attn seed ready` cannot disagree.
func PlotProgress(seeds []Seed, crownID string, ready map[string]bool) Progress {
	blocked := blockedIDs(seeds)
	var p Progress
	for _, seed := range InPlot(seeds, crownID) {
		if seed.ID == crownID {
			continue
		}
		p.Total++
		switch {
		case seed.Status == StatusHarvested:
			p.Done++
		case seed.Status == StatusWithered:
			p.Withered++
		case seed.Status == StatusGrowing:
			p.Growing++
		case seed.Status == StatusDormant:
			p.Dormant++
		}
		if ready[seed.ID] {
			p.Ready++
		}
		if !Closed(seed.Status) && blocked[seed.ID] {
			p.Blocked++
		}
	}
	return p
}

// DefaultStaleWindow is how long an open seed may sit without trail movement
// before `attn seed ls --stale` names it. Measured 2026-08-14 against
// production ticket activity (the nearest real trail): 276 gaps between
// consecutive activity on the same ticket; p50 0.3h, p99 45h, max 356h. Healthy
// continued work essentially never pauses more than two days, so a week is a
// tripwire ~3.7× past the p99 — a seed quiet that long is claiming attention it
// is not getting.
const DefaultStaleWindow = 7 * 24 * time.Hour

// Stale reports the open seeds whose trail has not moved within window, in the
// order given. lastMoved is the newest movement per seed — the document's own
// updated stamp or its newest note, whichever is later; a seed missing from it
// is skipped rather than judged on no evidence. Stale is a query, never a
// reaper: a person (or later a crew member) decides what withers.
func Stale(seeds []Seed, lastMoved map[string]time.Time, window time.Duration, now time.Time) []Seed {
	out := []Seed{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		moved, known := lastMoved[seed.ID]
		if !known {
			continue
		}
		if now.Sub(moved) >= window {
			out = append(out, seed)
		}
	}
	return out
}

// PlotChildSpec is one child in a plot planting. Blocks names sibling step
// slugs this child holds back — the only sequencing a plot carries; children
// are parallel by default.
type PlotChildSpec struct {
	Title  string   `json:"title"`
	Body   string   `json:"body,omitempty"`
	Blocks []string `json:"blocks,omitempty"`
}

// PlotSpec is a whole plot as one payload: the crown and its children, planted
// together so an agent captures a chunk of work in one command.
type PlotSpec struct {
	Title    string          `json:"title"`
	Body     string          `json:"body,omitempty"`
	Children []PlotChildSpec `json:"children"`
}

// ParsePlotSpec reads a plot payload off the command line. Unknown keys are
// refused here — a typo'd "block" silently planting an unsequenced plot is the
// kind of quiet wrong this surface must not do.
func ParsePlotSpec(raw []byte) (PlotSpec, error) {
	var spec PlotSpec
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return PlotSpec{}, fmt.Errorf("this is not a plot payload: %w (the shape is {\"title\": …, \"body\": …, \"children\": [{\"title\": …, \"blocks\": [\"<sibling step slug>\"]}]})", err)
	}
	if err := ValidatePlotSpec(spec); err != nil {
		return PlotSpec{}, err
	}
	return spec, nil
}

// ValidatePlotSpec refuses everything that cannot be planted, before anything
// is written: bad titles or bodies, duplicate step slugs (blocks address
// siblings by slug, so two children may not share one), blocks naming no
// sibling, and blocks that cycle.
func ValidatePlotSpec(spec PlotSpec) error {
	if err := ValidatePlant(spec.Title, spec.Body); err != nil {
		return fmt.Errorf("crown: %w", err)
	}
	if len(spec.Children) == 0 {
		return fmt.Errorf("a plot is a crown with children and this payload has none; to plant one seed use `attn seed plant`")
	}
	slugs := make(map[string]int, len(spec.Children))
	for i, child := range spec.Children {
		if err := ValidatePlant(child.Title, child.Body); err != nil {
			return fmt.Errorf("child %d: %w", i+1, err)
		}
		slug := StepSlug(child.Title)
		if prev, taken := slugs[slug]; taken {
			return fmt.Errorf("children %d and %d both derive the step slug %q; blocks address siblings by slug, so retitle one", prev+1, i+1, slug)
		}
		slugs[slug] = i
	}
	for i, child := range spec.Children {
		for _, target := range child.Blocks {
			if _, known := slugs[strings.TrimSpace(target)]; !known {
				return fmt.Errorf("child %d blocks %q, which is no sibling's step slug; the slugs here are %s", i+1, target, strings.Join(slugList(spec.Children), ", "))
			}
		}
	}
	if from, to, cyclic := blocksCycle(spec.Children, slugs); cyclic {
		return fmt.Errorf("the blocks edges cycle through %q and %q, so neither child would ever be ready; sequence one way only", from, to)
	}
	return nil
}

func slugList(children []PlotChildSpec) []string {
	out := make([]string, 0, len(children))
	for _, child := range children {
		out = append(out, StepSlug(child.Title))
	}
	return out
}

// blocksCycle walks the sibling blocks graph and reports one edge on a cycle.
func blocksCycle(children []PlotChildSpec, slugs map[string]int) (string, string, bool) {
	const unseen, visiting, done = 0, 1, 2
	state := make([]int, len(children))
	var walk func(i int) (string, string, bool)
	walk = func(i int) (string, string, bool) {
		state[i] = visiting
		for _, target := range children[i].Blocks {
			j := slugs[strings.TrimSpace(target)]
			if state[j] == visiting {
				return StepSlug(children[i].Title), StepSlug(children[j].Title), true
			}
			if state[j] == unseen {
				if from, to, cyclic := walk(j); cyclic {
					return from, to, true
				}
			}
		}
		state[i] = done
		return "", "", false
	}
	for i := range children {
		if state[i] == unseen {
			if from, to, cyclic := walk(i); cyclic {
				return from, to, true
			}
		}
	}
	return "", "", false
}
