// Package statetrace keeps a capped per-session ring of every state
// observation a session received — including rejected ones, with the reason —
// so a stuck state is diagnosable. Pure bookkeeping: nothing here influences
// which state a session ends up in, and it holds no reference to the store.
package statetrace

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultCapacity is how many observations are kept per session — at one per
// second, a bit over three minutes of history.
const DefaultCapacity = 256

// Outcome is what became of an observation.
type Outcome string

const (
	// OutcomeApplied means the state reached the store and was committed.
	OutcomeApplied Outcome = "applied"
	// OutcomeDiscarded means applyState's commit rule refused it.
	OutcomeDiscarded Outcome = "discarded"
	// OutcomeVetoed means it was rejected before ever reaching applyState.
	OutcomeVetoed Outcome = "vetoed"
	// OutcomeSkipped means the source produced no claim at all.
	OutcomeSkipped Outcome = "skipped"
	// OutcomeObserved means evidence only: its source does not drive state.
	OutcomeObserved Outcome = "observed"
)

// Observation is one recorded piece of state evidence and its fate. The gap
// between ObservedAt and RecordedAt is itself diagnostic.
type Observation struct {
	// Source is a free-form name for where the evidence came from.
	Source string
	// Claim is the state the source argued for, or "" for a skip.
	Claim string
	// Detail is the human-readable why, carried verbatim from the source.
	Detail string
	// Cause names the applyState commit rule, or "" when it never got that far.
	Cause string
	// Outcome is what happened to it.
	Outcome Outcome
	// Reason explains a non-applied outcome.
	Reason string
	// ObservedAt is when the source saw what it is reporting.
	ObservedAt time.Time
	// RecordedAt is when the daemon acted on it.
	RecordedAt time.Time
	// Repeats counts identical observations collapsed into this entry: a level
	// source restating itself once a second would otherwise evict the ring's
	// whole history within a minute.
	Repeats int
}

// sameEvidenceAs reports whether two observations are collapsible; timestamps
// deliberately do not count.
func (o Observation) sameEvidenceAs(other Observation) bool {
	return o.Source == other.Source &&
		o.Claim == other.Claim &&
		o.Cause == other.Cause &&
		o.Outcome == other.Outcome &&
		o.Reason == other.Reason &&
		o.Detail == other.Detail
}

// LogLine renders one observation as a single daemon-log line, so a log tail
// carries the trace for sessions whose ring is already forgotten.
func (o Observation) LogLine(sessionID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "state trace: session=%s source=%s claim=%s outcome=%s", sessionID, orDash(o.Source), orDash(o.Claim), orDash(string(o.Outcome)))
	if o.Cause != "" {
		fmt.Fprintf(&b, " cause=%s", o.Cause)
	}
	if o.Reason != "" {
		fmt.Fprintf(&b, " reason=%s", o.Reason)
	}
	if o.Detail != "" {
		fmt.Fprintf(&b, " detail=%q", o.Detail)
	}
	if !o.ObservedAt.IsZero() {
		fmt.Fprintf(&b, " observed_at=%s", o.ObservedAt.Format(time.RFC3339Nano))
		if !o.RecordedAt.IsZero() {
			fmt.Fprintf(&b, " lag=%s", o.RecordedAt.Sub(o.ObservedAt).Round(time.Millisecond))
		}
	}
	return b.String()
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// Recorder holds one ring per session; safe for concurrent use.
type Recorder struct {
	mu       sync.Mutex
	capacity int
	rings    map[string]*ring
}

// New returns a recorder keeping capacity observations per session; zero or
// less falls back to DefaultCapacity.
func New(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Recorder{capacity: capacity, rings: make(map[string]*ring)}
}

// Capacity is how many observations a session's ring holds.
func (r *Recorder) Capacity() int {
	if r == nil {
		return 0
	}
	return r.capacity
}

// Record appends an observation, evicting the oldest once the ring is full; a
// nil recorder records nothing.
func (r *Recorder) Record(sessionID string, obs Observation) {
	r.RecordIf(sessionID, obs, nil)
}

// RecordIf appends only when admit says the session still deserves a ring (nil
// always appends). admit runs under the recorder's lock so the check is atomic
// with Forget — checking before Record can recreate a ring nothing will ever
// forget. admit must not call the recorder or take a lock held while calling in.
func (r *Recorder) RecordIf(sessionID string, obs Observation, admit func() bool) {
	if r == nil || sessionID == "" {
		return
	}
	if obs.RecordedAt.IsZero() {
		obs.RecordedAt = time.Now()
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = obs.RecordedAt
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if admit != nil && !admit() {
		return
	}
	target := r.rings[sessionID]
	if target == nil {
		target = newRing(r.capacity)
		r.rings[sessionID] = target
	}
	if last := target.newest(); last != nil && last.sameEvidenceAs(obs) {
		last.Repeats++
		last.RecordedAt = obs.RecordedAt
		last.ObservedAt = obs.ObservedAt
		return
	}
	target.push(obs)
}

// Observations returns a copy of the session's observations, oldest first.
func (r *Recorder) Observations(sessionID string) []Observation {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target := r.rings[sessionID]
	if target == nil {
		return nil
	}
	return target.snapshot()
}

// SessionCount is how many sessions hold a ring, so a ring leak is assertable.
func (r *Recorder) SessionCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rings)
}

// Forget drops a session's ring so the recorder does not grow without bound.
func (r *Recorder) Forget(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rings, sessionID)
}

// ring is a fixed-size FIFO of observations.
type ring struct {
	items []Observation
	// start indexes the oldest item once the ring has wrapped.
	start int
	size  int
}

func newRing(capacity int) *ring {
	return &ring{items: make([]Observation, capacity)}
}

func (r *ring) push(obs Observation) {
	capacity := len(r.items)
	if r.size < capacity {
		r.items[(r.start+r.size)%capacity] = obs
		r.size++
		return
	}
	r.items[r.start] = obs
	r.start = (r.start + 1) % capacity
}

// newest returns a pointer into the ring's own storage (nil when empty) so a
// collapsing repeat can update it in place.
func (r *ring) newest() *Observation {
	if r.size == 0 {
		return nil
	}
	return &r.items[(r.start+r.size-1)%len(r.items)]
}

func (r *ring) snapshot() []Observation {
	out := make([]Observation, 0, r.size)
	capacity := len(r.items)
	for i := range r.size {
		out = append(out, r.items[(r.start+i)%capacity])
	}
	return out
}
