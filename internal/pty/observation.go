package pty

import "time"

// Source names where a state observation came from. The daemon arbitrates
// between sources rather than taking the last writer, so it has to know which
// source spoke — a bare state name cannot be arbitrated, only obeyed.
type Source string

const (
	// SourceWorkerInfo is the daemon's periodic worker `info` poll reporting the
	// worker's last known state, not a fresh terminal observation.
	SourceWorkerInfo Source = "worker_info"
	// SourceHeartbeat is the OSC 0 window-title glyph the agent repaints while
	// its turn runs. A level, not an edge: it says whether the agent is running
	// right now, and nothing about why it stopped.
	SourceHeartbeat Source = "heartbeat"
	// SourceUnknown is a state that crossed the worker RPC without a source,
	// i.e. from a worker older than the field.
	SourceUnknown Source = "unknown"
)

// ClaimsProtocolState reports whether an observation's Claim is a protocol state
// name at all. The heartbeat's is not — it is a level in the source's own
// vocabulary ("busy") — so it cannot be cached, deduped, compared against a
// state, or applied as one. It is recorded, and the resolver decides what it
// means.
//
// It answers authority as well as vocabulary, because the two now coincide: the
// only sources left that name a state are the worker poll and a pre-source
// worker's bare state, and both are how a session leaves `launching`, which the
// resolver deliberately does not own — it holds until the agent first speaks, and
// no evidence bears on it. Every source that describes what the agent is *doing*
// is a level or an edge in the evidence table, arbitrated with the rest. A future
// source that names a state without that authority is what would split this back
// into two predicates.
func (s Source) ClaimsProtocolState() bool {
	return s != SourceHeartbeat
}

// Observation is one piece of state evidence from the PTY layer. Claim is what
// the source believes (a protocol state name); Detail is why, in human terms,
// for diagnostics; At is when it was observed, which matters because an
// observation can reach the daemon well after the fact.
//
// Claim alone is deliberately not enough to act on: two sources can claim the
// same state for unrelated reasons, and the same source's claim means something
// different depending on how stale it is.
type Observation struct {
	Source Source
	Claim  string
	Detail string
	At     time.Time
}

func newObservation(source Source, claim, detail string, at time.Time) Observation {
	return Observation{Source: source, Claim: claim, Detail: detail, At: at}
}
