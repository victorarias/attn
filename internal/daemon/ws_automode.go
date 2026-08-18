package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
)

// Auto mode's app-only half: the read the settings section renders, and the two
// verbs that resolve a proposal.
//
// Promotion exists here and on no other transport. That is not an oversight to
// be tidied up later by adding a CLI verb — it is the whole security design. An
// agent reaches the unix socket; a human reaches the app. Only what a human
// touches may change the policy a session runs under.

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
