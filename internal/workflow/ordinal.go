package workflow

import (
	"strconv"
	"strings"
)

// segKind classifies a single structural segment of an ordinal path.
type segKind int

const (
	// segPhase records a phase(title) progress boundary. NOTE: per the R-spec
	// (§2.1), the phase is NOT part of the cache identity — renaming a phase must
	// not invalidate calls. We therefore record a phase *sequence number* (not the
	// title) so two identical call-sites in different phases get distinct ordinals,
	// while a rename (same sequence) is inert.
	segPhase segKind = iota
	// segParallelSlot records the 0-based index of a thunk in parallel(thunks).
	segParallelSlot
	// segPipelineItem records the 0-based index of an item in pipeline(items, ...).
	segPipelineItem
	// segStage records the 0-based index of a stage in a pipeline.
	segStage
	// segCallsite records the lexical call-site of an agent() call plus a
	// per-(prefix,callsite) loop counter to disambiguate repeated invocations.
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

// segment is one structural step from the run root toward a call site.
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

// OrdinalPath is an immutable snapshot of the structural descent to a single
// agent() call. The same logical call yields the same OrdinalPath on every
// re-run, independent of promise-resolution timing.
type OrdinalPath struct {
	segs []segment
}

// String returns the canonical "/"-joined encoding used as the journal key.
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

// clone returns a deep copy so callers can hold a stable snapshot.
func (p OrdinalPath) clone() OrdinalPath {
	cp := make([]segment, len(p.segs))
	copy(cp, p.segs)
	return OrdinalPath{segs: cp}
}

// pathStack is the engine-owned, loop-goroutine-only structural-path context.
// It is mutated synchronously during the descent into parallel/pipeline bodies
// and read synchronously at each agent() invocation. It is NOT stored in goja.
type pathStack struct {
	segs []segment

	// callCounter disambiguates repeated invocations of the same call-site. It is
	// keyed by (current-prefix | callsite) so a loop body re-entered under a
	// different slot/item counts independently. The map is snapshotted/restored
	// around each push/pop so counters are lexically scoped to their subtree —
	// this is what makes the counter a pure function of the structural descent.
	callCounter map[string]int

	phaseSeq int // monotonic phase sequence number

	// phaseTitle is the DISPLAY title of the current phase. It rides alongside
	// phaseSeq across await boundaries (so a post-await agent() reads the phase
	// in effect at its structural position) but is NEVER part of the ordinal:
	// identity stays positional/sequential, so renaming a phase cannot invalidate
	// a cached call.
	phaseTitle string
}

func newPathStack() *pathStack {
	return &pathStack{callCounter: map[string]int{}}
}

// prefix returns the canonical encoding of the current descent (without any
// trailing callsite segment).
func (ps *pathStack) prefix() string {
	if len(ps.segs) == 0 {
		return ""
	}
	parts := make([]string, len(ps.segs))
	for i, s := range ps.segs {
		parts[i] = s.String()
	}
	return strings.Join(parts, "/")
}

// pushPop is the marker handle returned by push; calling it restores the stack
// (segments + callCounter map) to its pre-push state.
type pushPop func()

// push appends a structural marker and snapshots the counter scope. The returned
// closure pops the marker and restores the counters captured before the push, so
// counters are scoped to the subtree just like the path itself.
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

// replace swaps in an entirely new descent path (used by stage/slot closures that
// re-establish their captured path at async resolution time) and returns a
// closure restoring the previous path + counters.
func (ps *pathStack) replace(newSegs []segment) pushPop {
	savedSegs := ps.segs
	savedCounters := ps.callCounter
	cp := make([]segment, len(newSegs))
	copy(cp, newSegs)
	ps.segs = cp
	// A re-established path gets a fresh counter scope: the captured path already
	// uniquely identifies the subtree, and any agent() inside the callback counts
	// from 0 within that re-established scope.
	ps.callCounter = map[string]int{}
	return func() {
		ps.segs = savedSegs
		ps.callCounter = savedCounters
	}
}

// snapshot returns an immutable copy of the current descent segments. Callers use
// it both to capture a closure's path (parallel/pipeline construction) and as the
// base for an agent() ordinal.
func (ps *pathStack) snapshot() []segment {
	cp := make([]segment, len(ps.segs))
	copy(cp, ps.segs)
	return cp
}

// stackState is a deep, immutable copy of the ENTIRE descent context (segments +
// counter scope + phase sequence). It is the unit the AsyncContextTracker carries
// across await boundaries so a continuation reads the same structural ordinal it
// would have read had it run synchronously. Restoring it is what makes the
// post-await agent() ordinal a pure function of structural position, not of
// promise-resolution timing.
type stackState struct {
	segs       []segment
	counters   map[string]int
	phaseSeq   int
	phaseTitle string
}

// captureState returns a deep copy of the full stack state for the tracker to
// stash on a pending continuation.
func (ps *pathStack) captureState() stackState {
	segs := make([]segment, len(ps.segs))
	copy(segs, ps.segs)
	counters := make(map[string]int, len(ps.callCounter))
	for k, v := range ps.callCounter {
		counters[k] = v
	}
	return stackState{segs: segs, counters: counters, phaseSeq: ps.phaseSeq, phaseTitle: ps.phaseTitle}
}

// restoreState installs a previously-captured state. It deep-copies the captured
// maps/slices so a restored continuation cannot mutate another continuation's
// snapshot (each Resumed gets its own fresh, mutable working copy).
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
// segment, advancing the per-(prefix,site) loop counter. This is the single point
// where an agent() call's ordinal is fixed.
func (ps *pathStack) ordinalFor(site string) OrdinalPath {
	key := ps.prefix() + "|" + site
	counter := ps.callCounter[key]
	ps.callCounter[key] = counter + 1

	segs := make([]segment, 0, len(ps.segs)+1)
	segs = append(segs, ps.segs...)
	segs = append(segs, segment{kind: segCallsite, index: counter, site: site})
	return OrdinalPath{segs: segs}
}

// setPhase replaces (or sets) the leading phase segment with the next sequence
// number. Phases live at the top of the path. The title is stored for DISPLAY
// only (phaseTitle) and is intentionally NOT part of the ordinal: identity is
// positional/sequential, not label-based, so renaming a phase never invalidates
// a cached call.
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

// currentPhase returns the DISPLAY title of the phase currently in effect (""
// before any phase() call). Read synchronously at agent() dispatch so each call
// records the phase active at its structural position.
func (ps *pathStack) currentPhase() string {
	return ps.phaseTitle
}
