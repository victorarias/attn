package daemon

import (
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

// The state reason: which resolver clause produced the colour a session is
// showing, carried to the client alongside the state itself.
//
// It exists because `unknown` is now a real answer rather than a shrug. A
// session whose evidence has stopped moving entirely is reported stuck, and a
// stuck badge with no reason is the same dead end as the stuck colour it
// replaces — the user can see that something is wrong and nothing about what.
//
// It is not persisted. The resolver recomputes it every tick from evidence that
// itself does not survive a restart, so a stored reason could only ever be a
// claim about a daemon that no longer exists.

// sessionStateReasons holds the most recent resolver reason per session.
type sessionStateReasons struct {
	mu      sync.Mutex
	reasons map[string]string
}

func newSessionStateReasons() *sessionStateReasons {
	return &sessionStateReasons{reasons: make(map[string]string)}
}

func (r *sessionStateReasons) set(sessionID, reason string) {
	if r == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasons[sessionID] = reason
}

func (r *sessionStateReasons) get(sessionID string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reasons[sessionID]
}

func (r *sessionStateReasons) forget(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.reasons, sessionID)
}

func (d *Daemon) stateReasons() *sessionStateReasons {
	d.sessionStateReasonOnce.Do(func() {
		d.sessionStateReason = newSessionStateReasons()
	})
	return d.sessionStateReason
}

// recordStateReason files the clause that produced the current resolution.
func (d *Daemon) recordStateReason(sessionID string, resolution sessionstate.Resolution) {
	d.stateReasons().set(sessionID, string(resolution.Reason))
}

// decorateSessionWithStateReason attaches the reason to an outgoing session.
//
// Only for states the resolver owns. A `launching` or `recoverable` session is
// in that state because the spawn or revive path put it there, and labelling it
// with whatever the resolver last thought would be a caption on the wrong photo.
func (d *Daemon) decorateSessionWithStateReason(clone *protocol.Session) {
	if clone == nil {
		return
	}
	clone.StateReason = nil
	if !resolverOwnedStates[clone.State] {
		return
	}
	if reason := d.stateReasons().get(clone.ID); reason != "" {
		clone.StateReason = protocol.Ptr(reason)
	}
}
