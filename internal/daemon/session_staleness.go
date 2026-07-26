package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

// Idle staleness: a finished turn that nobody has looked at since it finished.
//
// It is a mark, not a state. The session is idle and stays idle; what the mark
// adds is that the idle answer is no longer fresh. Nothing consumes it yet — the
// attention projection is what will turn it into a reason to unsettle a session
// — so today it exists to be observed, and to make the read time something the
// daemon actually tracks rather than something a later phase has to invent.
//
// Like the state reason, it is not persisted. It is derived every tick from the
// session's own `state_since` plus a read time that only means anything for the
// daemon that observed the reads.

// sessionReadTimes holds when each session's output was last seen by the user.
type sessionReadTimes struct {
	mu    sync.Mutex
	read  map[string]time.Time
	stale map[string]bool
}

func newSessionReadTimes() *sessionReadTimes {
	return &sessionReadTimes{read: make(map[string]time.Time), stale: make(map[string]bool)}
}

func (r *sessionReadTimes) markRead(sessionID string, at time.Time) {
	if r == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.read[sessionID]; ok && !at.After(existing) {
		return
	}
	r.read[sessionID] = at
}

func (r *sessionReadTimes) lastRead(sessionID string) time.Time {
	if r == nil {
		return time.Time{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.read[sessionID]
}

// setStale files the mark and reports whether it differs from the one already
// held, so a tick that changes nothing broadcasts nothing.
func (r *sessionReadTimes) setStale(sessionID string, stale bool) bool {
	if r == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stale[sessionID] == stale {
		return false
	}
	if stale {
		r.stale[sessionID] = true
	} else {
		delete(r.stale, sessionID)
	}
	return true
}

func (r *sessionReadTimes) isStale(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stale[sessionID]
}

func (r *sessionReadTimes) forget(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.read, sessionID)
	delete(r.stale, sessionID)
}

func (d *Daemon) readTimes() *sessionReadTimes {
	d.sessionReadTimesOnce.Do(func() {
		d.sessionReadTimes = newSessionReadTimes()
	})
	return d.sessionReadTimes
}

// markSessionRead records that the user has seen this session's output now.
func (d *Daemon) markSessionRead(sessionID string, at time.Time) {
	d.readTimes().markRead(sessionID, at)
}

// refreshIdleStaleness recomputes one session's mark and broadcasts a change.
//
// The session the UI is currently showing is read continuously, not at the
// moment it was selected: a turn that finishes while Victor is watching it has
// been seen, and there is no second `session_visualized` to say so.
func (d *Daemon) refreshIdleStaleness(session *protocol.Session, policy sessionstate.Policy, now time.Time) {
	if session == nil {
		return
	}
	if session.ID == d.currentlySelectedSession() {
		d.markSessionRead(session.ID, now)
	}
	stateSince := protocol.Timestamp(session.StateSince).Time()
	stale := sessionstate.IdleStale(session.State, stateSince, d.readTimes().lastRead(session.ID), policy, now)
	if d.readTimes().setStale(session.ID, stale) {
		d.broadcastSessionStateChanged(session.ID)
	}
}

// decorateSessionWithIdleStale attaches the mark to an outgoing session. The
// field is omitted rather than sent false: absent and not-stale are the same
// claim, and a wire that says so twice invites a client to tell them apart.
func (d *Daemon) decorateSessionWithIdleStale(clone *protocol.Session) {
	if clone == nil {
		return
	}
	clone.IdleStale = nil
	if d.readTimes().isStale(clone.ID) {
		clone.IdleStale = protocol.Ptr(true)
	}
}
