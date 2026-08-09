package workflow

import (
	"strconv"
	"strings"
)

// segKind classifies a single structural segment of an ordinal path.
type segKind int

const (
	// segPhase records a phase boundary as a *sequence number*, never the title:
	// a rename must not invalidate cached calls.
	segPhase segKind = iota
	segParallelSlot
	segPipelineItem
	segStage
	// segCallsite is the lexical call-site of an agent() call plus a
	// per-(prefix,callsite) loop counter disambiguating repeated invocations.
	segCallsite
)

func (k segKind) prefix() string {
	switch k {
	case segPhase:
		return "ph"
	case segParallelSlot:
		return "ps"
	case segPipelineItem:
		return "pi"
	case segStage:
		return "st"
	case segCallsite:
		return "cs"
	default:
		return "??"
	}
}

type segment struct {
	kind  segKind
	index int    // slot/item/stage/phase-seq index; loop counter for callsite
	site  string // for segCallsite only: the stable lexical call-site id
}

func (s segment) String() string {
	if s.kind == segCallsite {
		return "cs@" + s.site + "#" + strconv.Itoa(s.index)
	}
	return s.kind.prefix() + strconv.Itoa(s.index)
}

// OrdinalPath is an immutable snapshot of the structural descent to one agent()
// call: the same logical call yields the same path on every re-run, independent
// of promise-resolution timing.
type OrdinalPath struct {
	segs []segment
}

// String is the canonical "/"-joined encoding used as the journal key.
func (p OrdinalPath) String() string {
	if len(p.segs) == 0 {
		return ""
	}
	parts := make([]string, len(p.segs))
	for i, s := range p.segs {
		parts[i] = s.String()
	}
	return strings.Join(parts, "/")
}

func (p OrdinalPath) clone() OrdinalPath {
	cp := make([]segment, len(p.segs))
	copy(cp, p.segs)
	return OrdinalPath{segs: cp}
}

// pathStack is the engine-owned, loop-goroutine-only structural-path context,
// mutated and read synchronously. NOT stored in goja.
type pathStack struct {
	segs []segment

	// callCounter disambiguates repeated invocations of a call-site, keyed by
	// (current-prefix | callsite). Snapshotted/restored around each push/pop so
	// counters are lexically scoped to their subtree.
	callCounter map[string]int

	phaseSeq int
	// phaseTitle is DISPLAY-only; NEVER part of the ordinal, so renaming a phase
	// cannot invalidate a cached call.
	phaseTitle string
}

func newPathStack() *pathStack {
	return &pathStack{callCounter: map[string]int{}}
}

// prefix delegates to OrdinalPath.String() so the encoding has a single
// authority — a divergence silently corrupts journal cache identity.
func (ps *pathStack) prefix() string {
	return OrdinalPath{segs: ps.segs}.String()
}

// pushPop restores the stack (segments + callCounter) to its pre-push state.
type pushPop func()

// push appends a structural marker and snapshots the counter scope; the returned
// closure pops and restores, scoping counters to the subtree.
func (ps *pathStack) push(kind segKind, index int) pushPop {
	savedLen := len(ps.segs)
	savedCounters := make(map[string]int, len(ps.callCounter))
	for k, v := range ps.callCounter {
		savedCounters[k] = v
	}
	ps.segs = append(ps.segs, segment{kind: kind, index: index})
	return func() {
		ps.segs = ps.segs[:savedLen]
		ps.callCounter = savedCounters
	}
}

// replace swaps in a new descent path — stage/slot closures re-establishing
// their captured path at async resolution time — and returns a restoring closure.
func (ps *pathStack) replace(newSegs []segment) pushPop {
	savedSegs := ps.segs
	savedCounters := ps.callCounter
	cp := make([]segment, len(newSegs))
	copy(cp, newSegs)
	ps.segs = cp
	// Fresh scope: the captured path identifies the subtree, so calls inside the
	// callback count from 0.
	ps.callCounter = map[string]int{}
	return func() {
		ps.segs = savedSegs
		ps.callCounter = savedCounters
	}
}

func (ps *pathStack) snapshot() []segment {
	cp := make([]segment, len(ps.segs))
	copy(cp, ps.segs)
	return cp
}

// stackState is a deep copy of the descent context, carried by the
// AsyncContextTracker across await boundaries so a post-await ordinal is a pure
// function of structural position, not resolution timing.
type stackState struct {
	segs       []segment
	counters   map[string]int
	phaseSeq   int
	phaseTitle string
}

func (ps *pathStack) captureState() stackState {
	segs := make([]segment, len(ps.segs))
	copy(segs, ps.segs)
	counters := make(map[string]int, len(ps.callCounter))
	for k, v := range ps.callCounter {
		counters[k] = v
	}
	return stackState{segs: segs, counters: counters, phaseSeq: ps.phaseSeq, phaseTitle: ps.phaseTitle}
}

// restoreState deep-copies so a restored continuation cannot mutate another
// continuation's snapshot.
func (ps *pathStack) restoreState(s stackState) {
	segs := make([]segment, len(s.segs))
	copy(segs, s.segs)
	counters := make(map[string]int, len(s.counters))
	for k, v := range s.counters {
		counters[k] = v
	}
	ps.segs = segs
	ps.callCounter = counters
	ps.phaseSeq = s.phaseSeq
	ps.phaseTitle = s.phaseTitle
}

// ordinalFor reads the current path SYNCHRONOUSLY and appends the call-site
// segment — the single point where an agent() call's ordinal is fixed.
func (ps *pathStack) ordinalFor(site string) OrdinalPath {
	key := ps.prefix() + "|" + site
	counter := ps.callCounter[key]
	ps.callCounter[key] = counter + 1

	segs := make([]segment, 0, len(ps.segs)+1)
	segs = append(segs, ps.segs...)
	segs = append(segs, segment{kind: segCallsite, index: counter, site: site})
	return OrdinalPath{segs: segs}
}

// setPhase sets the leading phase segment to the next sequence number; the title
// is display-only, NOT part of the ordinal.
func (ps *pathStack) setPhase(title string) {
	ps.phaseSeq++
	seq := ps.phaseSeq
	ps.phaseTitle = title
	if len(ps.segs) > 0 && ps.segs[0].kind == segPhase {
		ps.segs[0].index = seq
		return
	}
	ps.segs = append([]segment{{kind: segPhase, index: seq}}, ps.segs...)
}

// currentPhase returns the display title in effect, read synchronously at dispatch.
func (ps *pathStack) currentPhase() string {
	return ps.phaseTitle
}
