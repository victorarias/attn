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

// notificationKindAutoModeDenied marks the notification a refused call raises.
const notificationKindAutoModeDenied = "automode_denied"

// recordAutoModeDenial is where a session's refusal becomes something the user
// can see: a row in the denials log, a notification naming what was blocked and
// why, and one `automode.denied` fact carrying the session it happened in.
//
// Nothing here is recurring. A session that denies nothing writes nothing,
// publishes nothing, and draws nothing.
func (d *Daemon) recordAutoModeDenial(params pluginReportAutoModeDenialParams) error {
	if d.store == nil {
		return fmt.Errorf("no database")
	}
	sessionID := strings.TrimSpace(params.SessionID)
	at := time.Now()
	if stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(params.At)); err == nil {
		at = stamp
	}
	stored, dropped, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
		SessionID: sessionID,
		Tool:      strings.TrimSpace(params.Tool),
		Signature: strings.TrimSpace(params.Action),
		Reason:    strings.TrimSpace(params.Reason),
		Rule:      strings.TrimSpace(params.Rule),
	}, at)
	if err != nil {
		return err
	}
	if dropped > 0 {
		d.logf("automode: denial log is at its %d-row cap; dropped %d oldest", store.AutoModeDenialRows, dropped)
	}
	// The notification is best effort, by design. The denial itself is already
	// enforced and the row is durable, so a failed notification write costs the
	// user this one surface — `attn automode denials` still lists it — and the
	// report is not worth failing over a surface. The fact stays with the
	// notification because pushing it alone would re-push a list the denial
	// never reached. The log line names the denial either way, so a row is never
	// the only trace of it.
	notification := ""
	record, err := d.store.AddNotification(autoModeDenialNotification(d.sessionLabel(sessionID), stored), time.Now())
	if err != nil {
		d.logf("automode: add denial notification for session %s: %v", sessionID, err)
	} else {
		notification = record.ID
	}
	d.logf("automode: denied session=%s rule=%s action=%q notification=%s",
		sessionID, stored.Rule, stored.Signature, notification)
	if notification == "" {
		return nil
	}
	// One fact, not two: the notification is how this denial is surfaced, and
	// automode.denied's projection is what pushes it. Its subject is the session
	// because that is the entity a denial is about.
	d.publishFact(FactAutoModeDenied, sessionID, nil)
	return nil
}

// sessionLabel names a session the way the user does, falling back to its id
// when the session is gone by the time the report lands.
func (d *Daemon) sessionLabel(sessionID string) string {
	if session := d.store.Get(sessionID); session != nil && strings.TrimSpace(session.Label) != "" {
		return session.Label
	}
	return sessionID
}

func autoModeDenialNotification(label string, denial store.AutoModeDenial) store.NotificationRecord {
	return store.NotificationRecord{
		Kind: notificationKindAutoModeDenied,
		// Auto mode working as designed: the agent got the reason, adapted or
		// asked, and the run went on. Worth seeing, never worth interrupting for.
		Severity:   store.NotificationInfo,
		Title:      fmt.Sprintf("Auto mode blocked a call in %s", label),
		Body:       denial.Signature,
		Detail:     fmt.Sprintf("%s (%s)", denial.Reason, denial.Rule),
		SourceKind: "session",
		SourceID:   denial.SessionID,
	}
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
			Rule:      denial.Rule,
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
