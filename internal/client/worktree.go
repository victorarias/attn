package client

import (
	"errors"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) WorktreeList(mainRepo string, limit int) (*protocol.WorktreeListResult, error) {
	msg := protocol.WorktreeListMessage{Cmd: protocol.CmdWorktreeList}
	if mainRepo != "" {
		msg.MainRepo = protocol.Ptr(mainRepo)
	}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.WorktreeListResult == nil {
		return nil, errors.New("daemon returned no worktree list")
	}
	return resp.WorktreeListResult, nil
}

func (c *Client) WorktreeKeep(path string, keep bool) (*protocol.WorktreeKeepResult, error) {
	resp, err := c.send(protocol.WorktreeKeepMessage{
		Cmd: protocol.CmdWorktreeKeep, Path: path, Keep: keep,
	})
	if err != nil {
		return nil, err
	}
	if resp.WorktreeKeepResult == nil {
		return nil, errors.New("daemon returned no worktree")
	}
	return resp.WorktreeKeepResult, nil
}

func (c *Client) WorktreeSweepLog(mainRepo string, limit int) (*protocol.WorktreeSweepLogResult, error) {
	msg := protocol.WorktreeSweepLogMessage{Cmd: protocol.CmdWorktreeSweepLog}
	if mainRepo != "" {
		msg.MainRepo = protocol.Ptr(mainRepo)
	}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.WorktreeSweepLogResult == nil {
		return nil, errors.New("daemon returned no sweep log")
	}
	return resp.WorktreeSweepLogResult, nil
}

func (c *Client) WorktreeRefresh() (*protocol.WorktreeRefreshResult, error) {
	resp, err := c.send(protocol.WorktreeRefreshMessage{Cmd: protocol.CmdWorktreeRefresh})
	if err != nil {
		return nil, err
	}
	if resp.WorktreeRefreshResult == nil {
		return nil, errors.New("daemon returned no refresh result")
	}
	return resp.WorktreeRefreshResult, nil
}

func (c *Client) DeleteWorktree(path string, force bool) error {
	_, err := c.send(protocol.DeleteWorktreeMessage{
		Cmd: protocol.CmdDeleteWorktree, Path: path, Force: protocol.Ptr(force),
	})
	return err
}
