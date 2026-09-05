package client

import (
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) DelegationRoles() (*protocol.DelegationRolesResult, error) {
	response, err := c.send(protocol.DelegationRolesMessage{Cmd: protocol.CmdDelegationRoles})
	if err != nil {
		return nil, err
	}
	if response.DelegationRoles == nil {
		return nil, fmt.Errorf("daemon returned no delegation roles response")
	}
	return response.DelegationRoles, nil
}
