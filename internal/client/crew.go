package client

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

// CrewList reads the crew roster: every registered member, awake or asleep.
func (c *Client) CrewList() (*protocol.CrewListResult, error) {
	resp, err := c.send(protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
	if err != nil {
		return nil, err
	}
	if resp.CrewListResult == nil {
		return nil, fmt.Errorf("the daemon answered without a roster")
	}
	return resp.CrewListResult, nil
}
