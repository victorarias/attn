// Package statetrace records the state observations a session received and what
// happened to each one.
//
// "Sometimes the color gets stuck" is unfalsifiable today: the daemon logs the
// transitions it accepted and says nothing about the ones it dropped, so a
// session sitting on a wrong color leaves no trace of whether the right
// observation never arrived, arrived and was vetoed before reaching the store,
// or arrived and lost to a newer writer. A stuck color is exactly the case where
// nothing is being applied, so a log of applied transitions is blind to it.
//
// The recorder keeps a capped per-session ring of every observation, including
// the rejected ones, with the reason for the rejection. It is pure bookkeeping:
// nothing here influences which state a session ends up in, and it holds no
// reference to the store. Phase 0 of the state-detection plan records into it
// from every existing source without changing arbitration, so the current
// behavior is captured as a baseline before the resolver replaces it.
package statetrace

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultCapacity is how many observations are kept per session. A session
// producing an observation every second holds a bit over three minutes of
// history, which covers the window a human takes to notice a wrong color and go
// looking.
const DefaultCapacity = 256

// Outcome is what became of an observation. The three rejection outcomes are
// kept apart because they point at different bugs: a veto means a driver's
// transition filter refused it, a discard means the store's own commit rule
// refused it, and a skip means the source itself decided it had nothing to say.
type Outcome string

const (
	// OutcomeApplied means the state reached the store and was committed.
	OutcomeApplied Outcome = "applied"
	// OutcomeDiscarded means the observation reached applyState and the store's
	// commit rule refused it (a stale classifier result, a losing plugin CAS).
	OutcomeDiscarded Outcome = "discarded"
	// OutcomeVetoed means something rejected the observation before it ever
	// reached applyState.
	OutcomeVetoed Outcome = "vetoed"
	// OutcomeSkipped means the source produced no claim at all — it looked and
	// decided there was nothing to report.
	OutcomeSkipped Outcome = "skipped"
)

// Observation is one recorded piece of state evidence and its fate.
//
// ObservedAt and RecordedAt are both kept: an observation can reach the daemon
// well after the moment it describes (the worker RPC hop, a slow classifier),
// and the gap between the two is itself diagnostic.
type Observation struct {
	// Source names where the evidence came from — a pty source name, a hook, the
	// classifier. Sources are free-form strings because they cross package
	// boundaries that must not depend on this one.
	Source string
	// Claim is the state the source argued for, or "" for a skip.
	Claim string
	// Detail is the human-readable why, carried verbatim from the source.
	Detail string
	// Cause names the applyState commit rule the observation travelled under, or
	// "" when it never got that far.
	Cause string
	// Outcome is what happened to it.
	Outcome Outcome
	// Reason explains a non-applied outcome.
	Reason string
	// ObservedAt is when the source saw what it is reporting.
	ObservedAt time.Time
	// RecordedAt is when the daemon acted on it.
	RecordedAt time.Time
}

// LogLine renders one observation as a single daemon-log line. It is the form
// the daemon writes on every recorded observation, so a log tail carries the
// same trace the ring does for sessions that have already been forgotten.
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

// Recorder holds one ring per session. It is safe for concurrent use: every
// state source in the daemon writes to it from its own goroutine.
type Recorder struct {
	mu       sync.Mutex
	capacity int
	rings    map[string]*ring
}

// New returns a recorder keeping capacity observations per session. A capacity
// of zero or less falls back to DefaultCapacity rather than silently recording
// nothing.
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

// Record appends an observation, evicting the oldest once the ring is full. A
// nil recorder records nothing, so callers need no guard.
func (r *Recorder) Record(sessionID string, obs Observation) {
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
	target := r.rings[sessionID]
	if target == nil {
		target = newRing(r.capacity)
		r.rings[sessionID] = target
	}
	target.push(obs)
}

// Observations returns the session's recorded observations, oldest first. The
// slice is a copy; the caller may hold it while more arrive.
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

// Forget drops a session's ring. Called when a session is unregistered so the
// recorder does not grow without bound across a long-lived daemon.
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

func (r *ring) snapshot() []Observation {
	out := make([]Observation, 0, r.size)
	capacity := len(r.items)
	for i := range r.size {
		out = append(out, r.items[(r.start+i)%capacity])
	}
	return out
}
