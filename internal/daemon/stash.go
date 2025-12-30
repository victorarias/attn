package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleCheckDirtyWS(client *wsClient, msg *protocol.CheckDirtyMessage) {
	go func() {
		dirty, err := git.IsDirty(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventCheckDirtyResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Dirty = protocol.Ptr(dirty)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleStashWS(client *wsClient, msg *protocol.StashMessage) {
	go func() {
		err := git.Stash(msg.Repo, msg.Message)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventStashResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Stash failed for %s: %v", msg.Repo, err)
		} else {
			d.logf("Stash succeeded for %s", msg.Repo)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleStashPopWS(client *wsClient, msg *protocol.StashPopMessage) {
	go func() {
		err := git.StashPop(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventStashPopResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			// Check if it's a conflict error
			if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "CONFLICT") {
				result.Conflict = protocol.Ptr(true)
			}
			d.logf("StashPop failed for %s: %v", msg.Repo, err)
		} else {
			d.logf("StashPop succeeded for %s", msg.Repo)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleCheckAttnStashWS(client *wsClient, msg *protocol.CheckAttnStashMessage) {
	go func() {
		found, ref, err := git.FindAttnStash(msg.Repo, msg.Branch)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventCheckAttnStashResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Found = protocol.Ptr(found)
			if ref != "" {
				result.StashRef = protocol.Ptr(ref)
			}
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleCommitWIPWS(client *wsClient, msg *protocol.CommitWIPMessage) {
	go func() {
		err := git.CommitWIP(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventCommitWIPResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("CommitWIP failed for %s: %v", msg.Repo, err)
		} else {
			d.logf("CommitWIP succeeded for %s", msg.Repo)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleGetDefaultBranchWS(client *wsClient, msg *protocol.GetDefaultBranchMessage) {
	go func() {
		branch, err := git.GetDefaultBranch(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventGetDefaultBranchResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Branch = protocol.Ptr(branch)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleFetchRemotesWS(client *wsClient, msg *protocol.FetchRemotesMessage) {
	go func() {
		err := git.FetchRemotes(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventFetchRemotesResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("FetchRemotes failed for %s: %v", msg.Repo, err)
		} else {
			d.logf("FetchRemotes succeeded for %s", msg.Repo)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleListRemoteBranchesWS(client *wsClient, msg *protocol.ListRemoteBranchesMessage) {
	go func() {
		branches, err := git.ListRemoteBranches(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventListRemoteBranchesResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			branchList := make([]protocol.Branch, len(branches))
			for i, b := range branches {
				branchList[i] = protocol.Branch{Name: b}
			}
			result.Branches = branchList
		}
		d.sendToClient(client, result)
	}()
}
