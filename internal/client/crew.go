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

// CrewWake starts a member's day. agent is optional; empty launches the
// default harness.
func (c *Client) CrewWake(member, agent string) (*protocol.CrewWakeResult, error) {
	msg := protocol.CrewWakeMessage{Cmd: protocol.CmdCrewWake, Member: member}
	if agent != "" {
		msg.Agent = protocol.Ptr(agent)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.CrewWakeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a wake result")
	}
	return resp.CrewWakeResult, nil
}

// CrewSet records where a member's sessions launch and what its charter is
// about. A nil field is left as it was.
func (c *Client) CrewSet(member string, cwd *string, awarenessDirs []string) (*protocol.CrewSetResult, error) {
	resp, err := c.send(protocol.CrewSetMessage{
		Cmd: protocol.CmdCrewSet, Member: member, Cwd: cwd, AwarenessDirs: awarenessDirs,
	})
	if err != nil {
		return nil, err
	}
	if resp.CrewSetResult == nil {
		return nil, fmt.Errorf("the daemon answered without a member")
	}
	return resp.CrewSetResult, nil
}

// CrewPrime asks what a launching session must be primed with to be its
// member. Every launch asks; a session that is nobody gets an empty answer.
func (c *Client) CrewPrime(sessionID string) (*protocol.CrewPrimeResult, error) {
	resp, err := c.send(protocol.CrewPrimeMessage{Cmd: protocol.CmdCrewPrime, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	if resp.CrewPrimeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a priming")
	}
	return resp.CrewPrimeResult, nil
}
