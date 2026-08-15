package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const crewRequestedSleepPrompt = "[attn] Victor is asking you to close your day and sleep. Finish what you need to settle, write your handoff letter, and file it with `attn handoff --sleep -m \"<your letter>\"`. This is consented closure, not a hard stop; nobody wakes behind you when the letter lands."

func (d *Daemon) handleCrewSleep(conn net.Conn, msg *protocol.CrewSleepMessage) {
	result, err := d.crewSleep(strings.TrimSpace(msg.Member))
	if err != nil {
		d.sendCrewError(conn, "sleep", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewSleepResult: result})
}

func (d *Daemon) handleCrewSleepWS(client *wsClient, msg *protocol.CrewSleepMessage) {
	result, err := d.crewSleep(strings.TrimSpace(msg.Member))
	response := protocol.CrewSleepResultMessage{
		Event:     protocol.EventCrewSleepResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member = protocol.Ptr(result.Member)
		response.SessionID = result.SessionID
		response.AlreadyAsleep = protocol.Ptr(result.AlreadyAsleep)
		response.DeliveryStatus = result.DeliveryStatus
		response.Detail = protocol.Ptr(result.Detail)
	}
	d.sendToClient(client, response)
}

// crewSleep asks a member to close its own day. The words are persisted before
// delivery and use the agent-message doorbell, but carry no sender session:
// this is Victor's request, not one agent speaking with his authority.
func (d *Daemon) crewSleep(name string) (*protocol.CrewSleepResult, error) {
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()

	member, _, err := d.crewMember(name)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(member.BindingSession)
	if sessionID == "" {
		return &protocol.CrewSleepResult{
			Member:        member.ID,
			AlreadyAsleep: true,
			Detail:        fmt.Sprintf("%s is already asleep; no sleep request was sent", crew.DisplayName(member.ID)),
		}, nil
	}
	live, err := d.crewSessionActuallyLive(sessionID)
	if err != nil {
		return nil, fmt.Errorf("check %s's bound session %s: %w", crew.DisplayName(member.ID), shortSessionID(sessionID), err)
	}
	if !live {
		if _, err := d.releaseCrewBinding(member.ID, sessionID); err != nil {
			return nil, fmt.Errorf("release %s's exited session %s: %w", crew.DisplayName(member.ID), shortSessionID(sessionID), err)
		}
		d.noteCrewExitedSession(member.ID, sessionID)
		return &protocol.CrewSleepResult{
			Member:        member.ID,
			AlreadyAsleep: true,
			Detail: fmt.Sprintf("%s is already asleep; previous session %s had exited and its binding was released; no sleep request was sent",
				crew.DisplayName(member.ID), shortSessionID(sessionID)),
		}, nil
	}

	record := store.AgentMessage{
		ID:              uuid.NewString(),
		TargetSessionID: sessionID,
		Content:         crewRequestedSleepPrompt,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	if err := d.store.EnqueueAgentMessage(record); err != nil {
		return nil, fmt.Errorf("record %s's sleep request: %w", crew.DisplayName(member.ID), err)
	}
	d.noteQueuedAgentMessage(sessionID)

	status := protocol.AgentMsgStatusDelivered
	detail := fmt.Sprintf("asked %s in session %s to write its handoff and file it with `attn handoff --sleep`", crew.DisplayName(member.ID), shortSessionID(sessionID))
	if err := d.deliverAgentMessage(record); err != nil {
		status = protocol.AgentMsgStatusQueued
		detail = agentMessageQueuedDetail(err)
	}
	return &protocol.CrewSleepResult{
		Member:         member.ID,
		SessionID:      protocol.Ptr(sessionID),
		DeliveryStatus: protocol.Ptr(status),
		Detail:         detail,
	}, nil
}
