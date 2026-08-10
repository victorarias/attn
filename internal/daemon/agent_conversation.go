package daemon

import (
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
)

type agentConversationObservation struct {
	SessionID      string `json:"-"`
	NativeID       string `json:"-"`
	TranscriptPath string `json:"transcript_path,omitempty"`
}

func (d *Daemon) handleObserveAgentConversation(conn net.Conn, msg *protocol.SetSessionResumeIDMessage) {
	observation := agentConversationObservation{
		SessionID:      strings.TrimSpace(msg.ID),
		NativeID:       strings.TrimSpace(msg.ResumeSessionID),
		TranscriptPath: strings.TrimSpace(protocol.Deref(msg.TranscriptPath)),
	}
	if observation.SessionID == "" {
		d.sendError(conn, "missing id")
		return
	}
	if observation.NativeID == "" {
		d.sendError(conn, "missing resume_session_id")
		return
	}
	d.observeOrQueueAgentConversation(observation)
	d.sendOK(conn)
}

// observeOrQueueAgentConversation closes the registration race: a provider can
// emit SessionStart before the spawn path has committed the attn session row.
// The observation remains the same authoritative signal; applying it is merely
// delayed until the row exists.
func (d *Daemon) observeOrQueueAgentConversation(observation agentConversationObservation) {
	d.pendingConversationMu.Lock()
	if d.store.Get(observation.SessionID) == nil {
		if d.pendingConversation == nil {
			d.pendingConversation = make(map[string]agentConversationObservation)
		}
		d.pendingConversation[observation.SessionID] = observation
		d.pendingConversationMu.Unlock()
		return
	}
	d.pendingConversationMu.Unlock()
	d.observeAgentConversation(observation)
}

func (d *Daemon) consumePendingAgentConversation(sessionID string) (agentConversationObservation, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return agentConversationObservation{}, false
	}
	d.pendingConversationMu.Lock()
	defer d.pendingConversationMu.Unlock()
	observation, ok := d.pendingConversation[sessionID]
	delete(d.pendingConversation, sessionID)
	return observation, ok
}

// observeAgentConversation is the authoritative transition boundary. Durable
// state commits before the fact is published, so every consumer can re-read a
// complete binding and no consumer owns a fragment of the transition.
func (d *Daemon) observeAgentConversation(observation agentConversationObservation) {
	changed, err := d.store.TransitionSessionConversation(observation.SessionID, observation.NativeID)
	if err != nil {
		d.logf("agent conversation: transition failed session=%s native=%s: %v", observation.SessionID, observation.NativeID, err)
		return
	}
	if !changed {
		return
	}

	d.resetSessionActivityRuntime(observation.SessionID)
	d.publishFact(FactSessionConversationChanged, observation.SessionID, observation)
}

func (d *Daemon) resetSessionActivityRuntime(sessionID string) {
	d.sessionActivityRunsMu.Lock()
	delete(d.sessionActivityRuns, sessionID)
	d.sessionActivityRunsMu.Unlock()
}

func (d *Daemon) subscribeAgentConversationFacts() {
	if d.eventBus == nil || d.conversationUnsubHooks != nil {
		return
	}
	d.conversationUnsubHooks = d.eventBus.Subscribe(
		bus.Filter{FactSessionConversationChanged},
		d.rebindTranscriptWatcherForConversation,
	)
}

func (d *Daemon) unsubscribeAgentConversationFacts() {
	if d.conversationUnsubHooks != nil {
		d.conversationUnsubHooks()
		d.conversationUnsubHooks = nil
	}
}

// rebindTranscriptWatcherForConversation is a runtime projection of the
// committed binding. It is deliberately cheap and publishes nothing while the
// bus fan-out lock is held.
func (d *Daemon) rebindTranscriptWatcherForConversation(event bus.Event) {
	session := d.store.Get(event.Subject)
	if session == nil || !isTranscriptWatchedAgent(session.Agent) {
		return
	}
	var observation agentConversationObservation
	if err := event.Decode(&observation); err != nil {
		d.logf("agent conversation: decode fact for session %s: %v", event.Subject, err)
	}
	d.startTranscriptWatcherAtPath(
		session.ID,
		session.Agent,
		session.Directory,
		time.Now(),
		observation.TranscriptPath,
	)
}
