package daemon

import (
	"context"
	"sync"
)

// The PTY write fence serializes writes into one session's terminal.
//
// A doorbell is not a single write: the payload goes out as a bracketed paste
// and its Enter follows doorbellSubmitDelay later, because agent composers
// finalize a paste on a timing boundary (see doorbellSubmitDelay). That gap is
// an open splice window — a keystroke landing inside it would sit between the
// paste and the Enter, and the doorbell's own Enter would submit the user's
// half-typed line along with the prompt.
//
// So every write into a session's PTY takes that session's fence, and the
// doorbell holds it across its whole pair. A keystroke racing the gap is
// written after the Enter, never inside it: the doorbell submits exactly what
// it composed, and the user's line stays theirs, still in the composer.
//
// This is a different fence from doorbellMu, which serializes a doorbell
// against authoritative state commits. The two nest in one order only —
// doorbellMu, then the write fence — and nothing takes them the other way.

// ptyWriteFence returns the per-session write fence, creating it on first use.
func (d *Daemon) ptyWriteFence(sessionID string) *sync.Mutex {
	d.ptyWriteFencesMu.Lock()
	defer d.ptyWriteFencesMu.Unlock()
	if d.ptyWriteFences == nil {
		d.ptyWriteFences = make(map[string]*sync.Mutex)
	}
	fence, ok := d.ptyWriteFences[sessionID]
	if !ok {
		fence = &sync.Mutex{}
		d.ptyWriteFences[sessionID] = fence
	}
	return fence
}

// clearPTYWriteFence drops a removed session's fence.
func (d *Daemon) clearPTYWriteFence(sessionID string) {
	d.ptyWriteFencesMu.Lock()
	defer d.ptyWriteFencesMu.Unlock()
	delete(d.ptyWriteFences, sessionID)
}

// writePTY sends one payload to a session's PTY under its write fence. Every
// PTY write goes through here (or through withPTYWriteFence for a multi-write
// delivery) so nothing can interleave with a delivery in progress.
func (d *Daemon) writePTY(sessionID string, data []byte) error {
	fence := d.ptyWriteFence(sessionID)
	fence.Lock()
	defer fence.Unlock()
	return d.ptyBackend.Input(context.Background(), sessionID, data)
}

// withPTYWriteFence runs a multi-write delivery with the session's fence held
// for its whole duration. The callback must write via ptyBackend.Input
// directly: writePTY would deadlock on the fence this already holds.
func (d *Daemon) withPTYWriteFence(sessionID string, deliver func() error) error {
	fence := d.ptyWriteFence(sessionID)
	fence.Lock()
	defer fence.Unlock()
	return deliver()
}
