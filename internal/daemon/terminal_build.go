package daemon

import (
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// decorateSessionWithTerminalBuild flags a session whose pty-worker holds a
// different libghostty-vt than this daemon.
//
// A worker process outlives an install, so updating the app under a running
// session leaves the worker parsing the PTY with the old terminal while the app
// renders with the new one. They stop agreeing about the grid: synthesized
// layout bytes, kitty placements, and OSC 133 block rows are all computed on the
// worker's model and replayed into the app's. The snapshot path already refuses
// mismatched bytes, so a drifted pane cannot even self-heal on resync. The app
// answers by offering a reload, which is what replaces the worker.
//
// The format tag is derived from the two ghostty locks, not from the attn
// version, so an ordinary release leaves it byte-identical and disturbs nothing.
// Only a ghostty bump moves it.
func (d *Daemon) decorateSessionWithTerminalBuild(clone *protocol.Session) {
	if clone == nil {
		return
	}
	clone.TerminalBuildStale = nil
	provider, ok := d.ptyBackend.(ptybackend.TerminalBuildProvider)
	if !ok {
		return
	}
	format, known := provider.SessionTerminalBuild(clone.ID)
	if !known || format == buildinfo.SnapshotFormat {
		return
	}
	// An empty format lands here too, and that is the point: every worker built
	// before the field existed reports nothing, and those are exactly the
	// sessions the first bump after this change strands.
	clone.TerminalBuildStale = protocol.Ptr(true)
}
