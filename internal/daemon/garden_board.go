package daemon

import (
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// The garden board's write path. Moving a seed and writing on its log were
// unix-socket commands only — the app could read the garden and never touch it
// — so the board prototype puts the same two commands on the WebSocket. The
// rules are not restated here: both handlers call the very code the CLI calls
// and only differ in how the answer travels back.
//
// The actor is unnamed. A move from the board is the user's own, and the app
// holds no session or crew identity to sign it with; the four verbs the board
// offers are the four that need no tender, so an unnamed actor is legal rather
// than merely tolerated. `tend` is not reachable from here, which is why the
// board's Growing column dispatches an agent instead of claiming a seed.
//
// Prototype: docs/plans/2026-08-20-garden-kanban-board-prototype.md.

func (d *Daemon) handleSeedTransitionWS(client *wsClient, msg *protocol.SeedTransitionMessage) {
	result := protocol.SeedTransitionResultMessage{
		Event:     protocol.EventSeedTransitionResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	verb, err := garden.ParseVerb(msg.Verb)
	if err != nil {
		fail(err)
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	seed, doc, err := d.applySeedTransition(msg.SeedID, verb, garden.Tender{}, protocol.Deref(msg.Reason))
	if err != nil {
		fail(err)
		return
	}
	wire := seedToProtocol(seed, doc, false)
	if read, err := d.readGarden(); err == nil {
		wire.Ready = read.ready[seed.ID]
		if progress, ok := read.progress(seed.ID); ok {
			wire.PlotProgress = progress
		}
	}
	d.mirrorSeedMoveOntoTicket("", seed.ID, verb, protocol.Deref(msg.Reason))
	d.ringSeedActivity(seed.ID, gardenRingEvents[verb], "")
	result.Seed = &wire
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleSeedNoteWS(client *wsClient, msg *protocol.SeedNoteMessage) {
	result := protocol.SeedNoteResultMessage{
		Event:     protocol.EventSeedNoteResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	note, err := d.appendSeedNote(
		msg.SeedID,
		msg.Body,
		"",
		protocol.Deref(msg.Member),
		protocol.Deref(msg.Kind),
		artifactFromProtocol(msg.Artifact),
	)
	if err != nil {
		fail(err)
		return
	}
	d.mirrorSeedNoteOntoTicket("", msg.SeedID, note.Body)
	if protocol.Deref(msg.Ring) {
		d.ringSeedActivity(msg.SeedID, "note", "")
	}
	result.Note = &note
	result.Success = true
	d.sendToClient(client, result)
}
