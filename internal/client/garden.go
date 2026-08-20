package client

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

// The garden's client surface. Planting costs one call because the daemon
// already knows who is asking: the session id is enough to stamp the workspace,
// so an agent never has to name its own context.

// SeedPlant plants one seed. partOf, when set, plants it under that crown —
// born part of the plot.
func (c *Client) SeedPlant(sessionID, title, body, partOf, member, resumeID, resumeCwd, resumeAgent string) (*protocol.SeedPlantResult, error) {
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
	if partOf != "" {
		msg.PartOf = protocol.Ptr(partOf)
	}
	if resumeID != "" || resumeCwd != "" || resumeAgent != "" {
		msg.ResumeSessionID = protocol.Ptr(resumeID)
		msg.ResumeCwd = protocol.Ptr(resumeCwd)
		msg.ResumeAgent = protocol.Ptr(resumeAgent)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedPlantResult == nil {
		return nil, fmt.Errorf("the daemon accepted the planting but returned no seed")
	}
	return resp.SeedPlantResult, nil
}

// SeedSetResume atomically sets or clears the fallback identity used when a
// seed has no dispatch record.
func (c *Client) SeedSetResume(seedID, resumeID, cwd, agent string, clear bool) (*protocol.SeedSetResumeResult, error) {
	msg := protocol.SeedSetResumeMessage{Cmd: protocol.CmdSeedSetResume, SeedID: seedID}
	if clear {
		msg.Clear = protocol.Ptr(true)
	} else {
		msg.ResumeSessionID = protocol.Ptr(resumeID)
		msg.ResumeCwd = protocol.Ptr(cwd)
		msg.ResumeAgent = protocol.Ptr(agent)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedSetResumeResult == nil {
		return nil, fmt.Errorf("the daemon accepted the resume identity but returned no seed")
	}
	return resp.SeedSetResumeResult, nil
}

// SeedList reads the garden, newest first. stale narrows to the open seeds
// whose log has not moved for the window; staleWindowSeconds 0 takes the
// daemon's default.
func (c *Client) SeedList(sessionID string, stale bool, staleWindowSeconds int) (*protocol.SeedListResult, error) {
	msg := protocol.SeedListMessage{Cmd: protocol.CmdSeedList}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if stale {
		msg.Stale = protocol.Ptr(true)
	}
	if staleWindowSeconds > 0 {
		msg.StaleWindowSeconds = protocol.Ptr(staleWindowSeconds)
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

// SeedShow reads one seed and the newest entries on its log.
func (c *Client) SeedShow(sessionID, seedID string) (*protocol.SeedShowResult, error) {
	msg := protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: seedID}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedShowResult == nil {
		return nil, fmt.Errorf("the daemon answered without a seed")
	}
	return resp.SeedShowResult, nil
}

// SeedEdit replaces one seed's markdown body without moving its lifecycle or
// claim. An empty body is deliberate and clears the document.
func (c *Client) SeedEdit(seedID, body string) (*protocol.SeedEditResult, error) {
	resp, err := c.send(protocol.SeedEditMessage{Cmd: protocol.CmdSeedEdit, SeedID: seedID, Body: body})
	if err != nil {
		return nil, err
	}
	if resp.SeedEditResult == nil {
		return nil, fmt.Errorf("the daemon accepted the edit but returned no seed")
	}
	return resp.SeedEditResult, nil
}

// SeedTransition moves a seed through its life. The daemon decides whether the
// move is legal from the state the seed is in and refuses by name; nothing here
// pre-judges it, so the CLI and the app cannot disagree about the rules.
func (c *Client) SeedTransition(sessionID, seedID, verb, reason, member string, confirm bool) (*protocol.SeedTransitionResult, error) {
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
	if confirm {
		msg.Confirm = protocol.Ptr(true)
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

// SeedNote appends one entry to a seed's log. An empty kind is the plain
// entry; `handoff` writes it to whoever tends the seed next.
func (c *Client) SeedNote(sessionID, seedID, body, member, kind string, ring bool, artifact *protocol.SeedArtifactReference) (*protocol.SeedNoteResult, error) {
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body, Artifact: artifact}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	if kind != "" {
		msg.Kind = protocol.Ptr(kind)
	}
	if ring {
		msg.Ring = protocol.Ptr(true)
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

// SeedReady asks what can be tended now. Both arguments are overrides: with
// neither, the daemon answers for the whole garden — or the caller's plot,
// when the session was dispatched at a crown.
func (c *Client) SeedReady(sessionID, plot string, all bool) (*protocol.SeedReadyResult, error) {
	msg := protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if plot != "" {
		msg.Plot = protocol.Ptr(plot)
	}
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

// SeedNotes reads a seed's whole log, newest first. limit 0 takes the
// daemon's bound.
func (c *Client) SeedNotes(sessionID, seedID string, limit int) (*protocol.SeedNotesResult, error) {
	msg := protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: seedID}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedNotesResult == nil {
		return nil, fmt.Errorf("the daemon answered without a log")
	}
	return resp.SeedNotesResult, nil
}

// SeedWatch makes one session's explicit watch exactly as requested.
func (c *Client) SeedWatch(sessionID, seedID string, unwatch bool) (*protocol.SeedWatchResult, error) {
	msg := protocol.SeedWatchMessage{
		Cmd: protocol.CmdSeedWatch, SourceSessionID: sessionID, SeedID: seedID,
	}
	if unwatch {
		msg.Unwatch = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedWatchResult == nil {
		return nil, fmt.Errorf("the daemon accepted the watch change but returned no state")
	}
	return resp.SeedWatchResult, nil
}

// SeedPlot plants a whole plot in one move: the crown and its children with
// their sequencing edges. The daemon validates everything before writing
// anything.
func (c *Client) SeedPlot(sessionID, member string, spec protocol.SeedPlotMessage) (*protocol.SeedPlotResult, error) {
	spec.Cmd = protocol.CmdSeedPlot
	if sessionID != "" {
		spec.SourceSessionID = protocol.Ptr(sessionID)
	}
	if member != "" {
		spec.Member = protocol.Ptr(member)
	}
	resp, err := c.send(spec)
	if err != nil {
		return nil, err
	}
	if resp.SeedPlotResult == nil {
		return nil, fmt.Errorf("the daemon accepted the plot but returned no seeds")
	}
	return resp.SeedPlotResult, nil
}
