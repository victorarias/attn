package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
)

// Auto mode's app-only half: the read the settings section renders, the two
// verbs that resolve a proposal, and the two that edit a pattern list directly.
//
// All five exist here and on no other transport. That is not an oversight to be
// tidied up later by adding a CLI verb — it is the whole security design. An
// agent reaches the unix socket; a human reaches the app. Only what a human
// touches may change the policy a session runs under, and direct editing does
// not weaken that: it happens at the same boundary promotion does.

func (d *Daemon) handleAutoModeGet(client *wsClient, msg *protocol.AutoModeGetMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeGet, "automode_get is missing a request id")
		return
	}
	result := protocol.AutoModeStateResultMessage{
		Event:     protocol.EventAutoModeStateResult,
		RequestID: requestID,
	}
	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	proposals, err := d.store.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.reconcileAutoModeDenialLedger()
	denials, err := d.store.ListAutoModeDenials(automodeDenialsDefaultLimit)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Config = autoModeConfigInfo(cfg)
	result.Proposals = autoModeProposalInfos(proposals)
	result.Denials = autoModeDenialInfos(denials)
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutoModePromote(client *wsClient, msg *protocol.AutoModePromoteMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModePromote, "automode_promote is missing a request id")
		return
	}
	result := protocol.AutoModePromoteResultMessage{
		Event:     protocol.EventAutoModePromoteResult,
		RequestID: requestID,
	}
	proposal, cfg, err := d.store.PromoteAutoModeProposal(int64(msg.ID), time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.logf("automode: promoted proposal %d (%s %s)", proposal.ID, proposal.Kind, proposal.Value)
	info := autoModeProposalInfo(proposal)
	config := autoModeConfigInfo(cfg)
	result.Proposal = &info
	result.Config = &config
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutoModeDiscard(client *wsClient, msg *protocol.AutoModeDiscardMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeDiscard, "automode_discard is missing a request id")
		return
	}
	result := protocol.AutoModeDiscardResultMessage{
		Event:     protocol.EventAutoModeDiscardResult,
		RequestID: requestID,
	}
	proposal, err := d.store.DiscardAutoModeProposal(int64(msg.ID), time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	info := autoModeProposalInfo(proposal)
	result.Proposal = &info
	result.Success = true
	d.sendToClient(client, result)
}

// handleAutoModePatternAdd and handleAutoModePatternRemove are the human's
// direct write into the allow and hard-deny lists. The store validates the
// pattern and refuses removing a shipped hard deny; both failures come back as
// this result's error so the section can print them beside the input that
// caused them.
func (d *Daemon) handleAutoModePatternAdd(client *wsClient, msg *protocol.AutoModePatternAddMessage) {
	d.editAutoModePattern(client, protocol.CmdAutoModePatternAdd, msg.RequestID, msg.List, msg.Pattern,
		d.store.AddAutoModePattern)
}

func (d *Daemon) handleAutoModePatternRemove(client *wsClient, msg *protocol.AutoModePatternRemoveMessage) {
	d.editAutoModePattern(client, protocol.CmdAutoModePatternRemove, msg.RequestID, msg.List, msg.Pattern,
		d.store.RemoveAutoModePattern)
}

func (d *Daemon) editAutoModePattern(
	client *wsClient,
	cmd, requestID, list, pattern string,
	edit func(list, pattern string, now time.Time) (automode.Config, error),
) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		d.sendCommandError(client, cmd, cmd+" is missing a request id")
		return
	}
	result := protocol.AutoModePatternResultMessage{
		Event:     protocol.EventAutoModePatternResult,
		RequestID: requestID,
	}
	cfg, err := edit(list, pattern, time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.logf("automode: %s %s %q", cmd, list, pattern)
	info := autoModeConfigInfo(cfg)
	result.Config = &info
	result.Success = true
	d.sendToClient(client, result)
}
