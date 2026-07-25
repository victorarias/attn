package pty

import "time"

// Source names where a state observation came from. The daemon arbitrates
// between sources rather than taking the last writer, so it has to know which
// source spoke — a bare state name cannot be arbitrated, only obeyed.
type Source string

const (
	// SourceScreen is the rendered-screen scrape in state_detector.go.
	SourceScreen Source = "screen"
	// SourceApproval is approvalResolver, watching the approval prompt appear
	// and leave the rendered screen.
	SourceApproval Source = "approval"
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

// EvidenceOnly reports whether a source records evidence without claiming a
// protocol state. Such an observation's Claim is in the source's own vocabulary
// ("busy"), so applying it as a state name would be meaningless — the
// daemon records it and moves on.
//
// This exists because the harness signals are wired ahead of the resolver that
// will weigh them: their traces are being compared against the current behavior
// before anything arbitrates on them. It goes away when the resolver lands.
func (s Source) EvidenceOnly() bool {
	switch s {
	case SourceHeartbeat:
		return true
	default:
		return false
	}
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
