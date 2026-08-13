package client

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

// The garden's client surface. Planting costs one call because the daemon
// already knows who is asking: the session id is enough to stamp the workspace,
// so an agent never has to name its own context.

// SeedPlant plants one seed. workspaceID nil takes the calling session's
// workspace; a non-nil value overrides it.
func (c *Client) SeedPlant(sessionID, title, body string, workspaceID *string, member string) (*protocol.SeedPlantResult, error) {
	msg := protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: title}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if body != "" {
		msg.Body = protocol.Ptr(body)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	msg.WorkspaceID = workspaceID
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedPlantResult == nil {
		return nil, fmt.Errorf("the daemon accepted the planting but returned no seed")
	}
	return resp.SeedPlantResult, nil
}

// SeedList reads the garden. all overrides scoping; otherwise workspaceID, and
// otherwise the calling session's workspace.
func (c *Client) SeedList(sessionID string, workspaceID *string, all bool) (*protocol.SeedListResult, error) {
	msg := protocol.SeedListMessage{Cmd: protocol.CmdSeedList}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	msg.WorkspaceID = workspaceID
	if all {
		msg.All = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedListResult == nil {
		return nil, fmt.Errorf("the daemon answered without a seed list")
	}
	return resp.SeedListResult, nil
}

// SeedShow reads one seed and the newest entries on its trail.
func (c *Client) SeedShow(seedID string) (*protocol.SeedShowResult, error) {
	resp, err := c.send(protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: seedID})
	if err != nil {
		return nil, err
	}
	if resp.SeedShowResult == nil {
		return nil, fmt.Errorf("the daemon answered without a seed")
	}
	return resp.SeedShowResult, nil
}

// SeedTransition moves a seed through its life. The daemon decides whether the
// move is legal from the state the seed is in and refuses by name; nothing here
// pre-judges it, so the CLI and the app cannot disagree about the rules.
func (c *Client) SeedTransition(sessionID, seedID, verb, reason, member string) (*protocol.SeedTransitionResult, error) {
	msg := protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: verb}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if reason != "" {
		msg.Reason = protocol.Ptr(reason)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedTransitionResult == nil {
		return nil, fmt.Errorf("the daemon accepted the move but returned no seed")
	}
	return resp.SeedTransitionResult, nil
}

// SeedNote appends one entry to a seed's trail. An empty kind is the plain
// entry; `handoff` writes it to whoever tends the seed next.
func (c *Client) SeedNote(sessionID, seedID, body, member, kind string) (*protocol.SeedNoteResult, error) {
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	if kind != "" {
		msg.Kind = protocol.Ptr(kind)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedNoteResult == nil {
		return nil, fmt.Errorf("the daemon accepted the note but returned nothing")
	}
	return resp.SeedNoteResult, nil
}

// SeedLink adds one edge between two seeds, or removes it when unlink is set.
// The daemon decides what may be linked — an edge onto a seed that is not
// planted, a second crown, a cycle — so the CLI and the app cannot disagree.
func (c *Client) SeedLink(seedID, kind, toSeedID string, unlink bool) (*protocol.SeedLinkResult, error) {
	msg := protocol.SeedLinkMessage{
		Cmd: protocol.CmdSeedLink, SeedID: seedID, Kind: kind, ToSeedID: toSeedID,
	}
	if unlink {
		msg.Unlink = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedLinkResult == nil {
		return nil, fmt.Errorf("the daemon accepted the edge but returned no seed")
	}
	return resp.SeedLinkResult, nil
}

// SeedReady asks what can be tended now. Every argument is an override: with
// none of them the daemon scopes the answer to the calling session's workspace.
func (c *Client) SeedReady(sessionID, plot string, workspaceID *string, all bool) (*protocol.SeedReadyResult, error) {
	msg := protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if plot != "" {
		msg.Plot = protocol.Ptr(plot)
	}
	msg.WorkspaceID = workspaceID
	if all {
		msg.All = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedReadyResult == nil {
		return nil, fmt.Errorf("the daemon answered without a ready list")
	}
	return resp.SeedReadyResult, nil
}

// SeedNotes reads a seed's whole trail, newest first. limit 0 takes the
// daemon's bound.
func (c *Client) SeedNotes(seedID string, limit int) (*protocol.SeedNotesResult, error) {
	msg := protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: seedID}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedNotesResult == nil {
		return nil, fmt.Errorf("the daemon answered without a trail")
	}
	return resp.SeedNotesResult, nil
}
