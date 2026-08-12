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

// SeedShow reads one seed.
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
