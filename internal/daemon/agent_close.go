package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The same limit a harvest reason carries: both answer "why did this work end",
// and one number for the pair beats two that drift apart.
const agentCloseReasonMaxChars = garden.MaxReasonChars

// A session tending more than one seed is unusual but legal, and every one of
// them has a reader who needs to know its agent is gone.
const agentCloseTendedSeedLimit = 100

func (d *Daemon) handleAgentClose(conn net.Conn, msg *protocol.AgentCloseMessage) {
	caller, errCode := d.resolveSessionByIDOrPrefix(msg.SourceSessionID)
	if caller == nil {
		d.replyAgentMsgError(conn, "sender_"+errCode, fmt.Sprintf(
			"the caller %q is not a session on this daemon; a close is attributed to the session that asked for it",
			strings.TrimSpace(msg.SourceSessionID)))
		return
	}

	reason := strings.TrimSpace(msg.Reason)
	switch {
	case reason == "":
		d.replyAgentMsgError(conn, "close_reason_required",
			"a close needs a reason: the ledger keeps the row, and the reason is all the next reader gets")
		return
	case len(reason) > agentCloseReasonMaxChars:
		d.replyAgentMsgError(conn, "close_reason_required", fmt.Sprintf(
			"that reason is %d characters and the limit is %d; say why it is done and put the detail on the seed",
			len(reason), agentCloseReasonMaxChars))
		return
	}

	target, refusal := d.resolveAgentCloseTarget(msg)
	if refusal != nil {
		d.replyAgentMsgError(conn, refusal.code, refusal.message)
		return
	}

	rule, err := d.agentCloseRule(caller, target)
	if err != nil {
		d.replyAgentMsgError(conn, "close_not_authorized", err.Error())
		return
	}
	if protectErr := d.sessionCloseError(target.ID); protectErr != nil {
		d.replyAgentMsgError(conn, "session_close_protected", protectErr.Error())
		return
	}

	d.logf("agent close: session %s closes %s as %s: %s", caller.ID, target.ID, rule, reason)
	closing, err := d.beginSessionClose(target.ID, store.SessionClose{By: caller.ID, Reason: reason}, nil)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	result := &protocol.AgentCloseResult{
		TargetSessionID: target.ID,
		Label:           sessionDisplayName(target),
		Reason:          reason,
		Rule:            rule,
		SeedIds:         d.noteCloseOnTendedSeeds(target, caller, rule, reason),
	}
	// Answering before the kill: a session closing itself is the process holding
	// this connection, and it never reads a reply written after its own SIGTERM.
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentCloseResult: result})
	d.finishSessionClose(target.ID, closing)
}

type agentCloseRefusal struct {
	code    string
	message string
}

func (d *Daemon) resolveAgentCloseTarget(msg *protocol.AgentCloseMessage) (*protocol.Session, *agentCloseRefusal) {
	reference := strings.TrimSpace(msg.TargetSessionID)
	if seedID := strings.TrimSpace(protocol.Deref(msg.TargetSeedID)); seedID != "" {
		if reference != "" {
			return nil, &agentCloseRefusal{"ambiguous_target",
				"a close ends one session; name a session or a seed, not both"}
		}
		tender, err := d.seedTenderSession(seedID)
		if err != nil {
			return nil, &agentCloseRefusal{"seed_untended", err.Error()}
		}
		reference = tender
	}
	target, errCode := d.resolveSessionByIDOrPrefix(reference)
	switch {
	case target != nil:
		return target, nil
	case errCode == "ambiguous_session":
		return nil, &agentCloseRefusal{errCode, fmt.Sprintf(
			"%q matches more than one session; give more of the id (`attn agent list --json` carries full ids)", reference)}
	default:
		return nil, &agentCloseRefusal{errCode, fmt.Sprintf(
			"no session matches %q; `attn agent list` names the sessions on this daemon", reference)}
	}
}

// One hop, deliberately: a grandchild is reachable through its own dispatcher,
// and walking the chain would make a mistaken close unbounded.
func (d *Daemon) agentCloseRule(caller, target *protocol.Session) (protocol.AgentCloseRule, error) {
	if caller.ID == target.ID {
		return protocol.AgentCloseRuleSelf, nil
	}
	if d.isChiefOfStaffSession(caller.ID) {
		return protocol.AgentCloseRuleChiefOfStaff, nil
	}
	dispatcher := ""
	if dispatch, ok := d.gardenDispatch(target.ID); ok {
		dispatcher = strings.TrimSpace(dispatch.DispatcherSession)
	}
	if dispatcher != "" && dispatcher == caller.ID {
		return protocol.AgentCloseRuleDispatcher, nil
	}
	const rules = "a session may close itself and the sessions it dispatched, and the chief of staff may close any"
	if dispatcher == "" {
		return "", fmt.Errorf("%s. Session %s was not dispatched by anyone, so only it and the chief of staff can close it",
			rules, shortSessionID(target.ID))
	}
	return "", fmt.Errorf("%s. Session %s was dispatched by session %s, not by you",
		rules, shortSessionID(target.ID), shortSessionID(dispatcher))
}

// The seed does not move: its tender is a closed session, and whoever reads it
// next decides whether to take it or park it.
func (d *Daemon) noteCloseOnTendedSeeds(
	target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string,
) []string {
	noted := []string{}
	if err := d.requireHome(garden.Surface); err != nil {
		return noted
	}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Filters:    []docstore.Filter{{Field: "tender_session", Op: docstore.OpEq, Value: target.ID}},
		Limit:      agentCloseTendedSeedLimit,
	})
	if err != nil {
		d.logf("agent close: reading the seeds %s tended: %v", target.ID, err)
		return noted
	}
	body := agentCloseSeedNote(target, caller, rule, reason)
	for _, doc := range read.Documents {
		if _, err := d.appendSeedNote(doc.ID, body, caller.ID, "", garden.NoteKindNote, nil); err != nil {
			d.logf("agent close: noting the close of %s on %s: %v", target.ID, doc.ID, err)
			continue
		}
		noted = append(noted, doc.ID)
	}
	return noted
}

// The label alone would not find the session again, and the seed outlives the
// session that was tending it.
func agentCloseSessionRef(session *protocol.Session) string {
	if label := strings.TrimSpace(session.Label); label != "" {
		return fmt.Sprintf("%s (%s)", label, shortSessionID(session.ID))
	}
	return shortSessionID(session.ID)
}

func agentCloseSeedNote(target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string) string {
	closer := agentCloseSessionRef(caller)
	switch rule {
	case protocol.AgentCloseRuleSelf:
		closer = "itself"
	case protocol.AgentCloseRuleChiefOfStaff:
		closer = "the chief of staff, " + closer
	case protocol.AgentCloseRuleDispatcher:
		closer = "its dispatcher, " + closer
	}
	return fmt.Sprintf(
		"Session %s was closed by %s while tending this seed. Reason: %s\n\n"+
			"The seed did not move. It still names that session as its tender, so whoever comes next "+
			"takes it or parks it. `attn session show %s` reads the closed row back.",
		agentCloseSessionRef(target), closer, reason, shortSessionID(target.ID))
}
