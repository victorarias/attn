package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Auto mode's daemon surface, split across two transports on purpose.
//
// This file holds the unix-socket half — the one `attn automode` and therefore
// every agent can reach. It reads, it edits environment prose, and it records
// proposals. Nothing here can change the patterns or models a pi session
// launches with; ws_automode.go holds the app-only half that can.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.

const automodeDenialsDefaultLimit = 20

func (d *Daemon) sendAutoModeResponse(conn net.Conn, resp protocol.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("automode: writing response: %v", err)
	}
}

// requireAutoModeStore refuses every verb on a daemon with no database rather
// than answering with the defaults, which would read as a configured machine.
func (d *Daemon) requireAutoModeStore(conn net.Conn) bool {
	if d.store == nil {
		d.sendError(conn, "no database")
		return false
	}
	return true
}

func (d *Daemon) handleAutoModeShow(conn net.Conn, _ *protocol.AutoModeShowMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	proposals, err := d.store.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok: true,
		AutomodeShowResult: &protocol.AutoModeShowResult{
			Config:    autoModeConfigInfo(cfg),
			Proposals: autoModeProposalInfos(proposals),
		},
	})
}

func (d *Daemon) handleAutoModeEnvAdd(conn net.Conn, msg *protocol.AutoModeEnvAddMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		d.sendError(conn, "automode_env_add: text is required")
		return
	}
	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	updated, err := d.store.SetAutoModeEnvironment(append(cfg.Environment, text), time.Now())
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok:                true,
		AutomodeEnvResult: &protocol.AutoModeEnvResult{Environment: updated.Environment},
	})
}

func (d *Daemon) handleAutoModeEnvRemove(conn net.Conn, msg *protocol.AutoModeEnvRemoveMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	index := msg.Index
	if index < 0 || index >= len(cfg.Environment) {
		d.sendError(conn, fmt.Sprintf(
			"automode_env_remove: index %d is out of range; there are %d environment entries",
			index, len(cfg.Environment)))
		return
	}
	entries := append([]string{}, cfg.Environment[:index]...)
	entries = append(entries, cfg.Environment[index+1:]...)
	updated, err := d.store.SetAutoModeEnvironment(entries, time.Now())
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok:                true,
		AutomodeEnvResult: &protocol.AutoModeEnvResult{Environment: updated.Environment},
	})
}

// handleAutoModePropose records a proposal and nothing else. A caller that
// expected an allow to take effect gets a proposal id back; the CLI says so in
// as many words, because a silent "recorded, inert" is how an agent concludes
// its own rule is live.
func (d *Daemon) handleAutoModePropose(conn net.Conn, msg *protocol.AutoModeProposeMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	proposal, err := d.store.CreateAutoModeProposal(
		strings.TrimSpace(msg.Kind),
		strings.TrimSpace(protocol.Deref(msg.Target)),
		strings.TrimSpace(msg.Value),
		strings.TrimSpace(protocol.Deref(msg.ProposedBy)),
		time.Now(),
	)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok: true,
		AutomodeProposeResult: &protocol.AutoModeProposeResult{
			Proposal: autoModeProposalInfo(proposal),
		},
	})
}

func (d *Daemon) handleAutoModeDenials(conn net.Conn, msg *protocol.AutoModeDenialsMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	limit := automodeDenialsDefaultLimit
	if msg.Limit != nil && *msg.Limit > 0 {
		limit = *msg.Limit
	}
	denials, err := d.store.ListAutoModeDenials(limit)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok: true,
		AutomodeDenialsResult: &protocol.AutoModeDenialsResult{
			Denials: autoModeDenialInfos(denials),
		},
	})
}

func autoModeConfigInfo(cfg automode.Config) protocol.AutoModeConfigInfo {
	return protocol.AutoModeConfigInfo{
		EnabledDefault:  cfg.EnabledDefault,
		Environment:     nonNilStrings(cfg.Environment),
		Allow:           nonNilStrings(cfg.Allow),
		HardDeny:        nonNilStrings(cfg.HardDeny),
		ClassifierModel: cfg.ClassifierModel,
		EscalationModel: cfg.EscalationModel,
	}
}

func autoModeProposalInfo(p store.AutoModeProposal) protocol.AutoModeProposalInfo {
	return protocol.AutoModeProposalInfo{
		ID:         int(p.ID),
		Kind:       p.Kind,
		Target:     p.Target,
		Value:      p.Value,
		ProposedBy: p.ProposedBy,
		State:      p.State,
		CreatedAt:  formatAutoModeStamp(p.CreatedAt),
		ResolvedAt: formatAutoModeStamp(p.ResolvedAt),
	}
}

func autoModeProposalInfos(proposals []store.AutoModeProposal) []protocol.AutoModeProposalInfo {
	out := make([]protocol.AutoModeProposalInfo, 0, len(proposals))
	for _, p := range proposals {
		out = append(out, autoModeProposalInfo(p))
	}
	return out
}

func autoModeDenialInfos(denials []store.AutoModeDenial) []protocol.AutoModeDenialInfo {
	out := make([]protocol.AutoModeDenialInfo, 0, len(denials))
	for _, denial := range denials {
		out = append(out, protocol.AutoModeDenialInfo{
			ID:        int(denial.ID),
			SessionID: denial.SessionID,
			Tool:      denial.Tool,
			Signature: denial.Signature,
			Reason:    denial.Reason,
			CreatedAt: formatAutoModeStamp(denial.CreatedAt),
		})
	}
	return out
}

func formatAutoModeStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
