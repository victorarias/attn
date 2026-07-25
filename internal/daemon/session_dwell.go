package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// The dwell gate: how long a resolved transition must keep being the answer
// before anyone is told about it.
//
// It exists for one shape — an approval addressed to a guardian rather than to
// the user. Codex routes permission requests to its auto_review reviewer and
// claude to its permission classifier, both of which answer in well under a
// second, and publishing the request the instant it appears paints an
// attention-demanding color on every tool call of a run nobody is watching. The
// dwell is not a delay on genuine requests: with no reviewer in the loop the
// user *is* the reviewer and `sessionstate.DwellFor` returns zero.
//
// It lives here rather than in the resolver because it is not a question about
// the evidence. The resolver is pure and answers "what is true now"; whether an
// answer has been true for long enough is a fact about the sequence of answers,
// which only the thing running the tick can know.

// dwellGate tracks, per session, the transition currently waiting out its dwell.
type dwellGate struct {
	mu      sync.Mutex
	pending map[string]dwellPending
}

type dwellPending struct {
	state protocol.SessionState
	since time.Time
}

func newDwellGate() *dwellGate {
	return &dwellGate{pending: make(map[string]dwellPending)}
}

// ready reports whether a transition into state may be published now.
//
// A dwell of zero always passes and clears any wait in progress: an agent that
// drops its reviewer mid-session must not keep serving out a dwell the new mode
// no longer asks for.
//
// A state different from the one being waited on replaces it rather than
// extending it. That is what cancels the wait when the guardian answers: the
// approval is retired by the agent's next busy frame, the resolver starts
// answering `working`, and the pending approval is dropped without ever having
// been shown.
func (g *dwellGate) ready(sessionID string, state protocol.SessionState, dwell time.Duration, now time.Time) bool {
	if g == nil || strings.TrimSpace(sessionID) == "" {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if dwell <= 0 {
		delete(g.pending, sessionID)
		return true
	}
	pending, ok := g.pending[sessionID]
	if !ok || pending.state != state {
		g.pending[sessionID] = dwellPending{state: state, since: now}
		return false
	}
	if now.Sub(pending.since) < dwell {
		return false
	}
	delete(g.pending, sessionID)
	return true
}

// clear drops any wait in progress. Called whenever the resolver stops proposing
// a transition at all, so a later one starts its dwell from scratch instead of
// inheriting an abandoned clock.
func (g *dwellGate) clear(sessionID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, sessionID)
}
