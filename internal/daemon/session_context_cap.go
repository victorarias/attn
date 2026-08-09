package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// handleSetSessionContextWindowCap pins a per-session context-window cap
// (tokens), or clears the pin with cap 0. The pin outranks the chief and
// per-agent default settings in launchContextWindowCap, and a changed pin on a
// live session reloads the agent in place (resume-preserving) so it takes
// effect now rather than at some future respawn the user cannot see.
func (d *Daemon) handleSetSessionContextWindowCap(client *wsClient, msg *protocol.SetSessionContextWindowCapMessage) {
	err := d.setSessionContextWindowCap(msg.SessionID, msg.Cap)
	result := protocol.SessionContextWindowCapResultMessage{
		Event:     protocol.EventSessionContextWindowCapResult,
		SessionID: msg.SessionID,
		Cap:       msg.Cap,
		Success:   err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

// setSessionContextWindowCap is the one place the cap pin is written. The cap
// only reaches the agent at launch (Claude reads CLAUDE_CODE_AUTO_COMPACT_WINDOW
// from its environment, codex takes a config override), so after storing a
// changed pin it kicks off the same in-place reload a chief promotion uses —
// the running process cannot be re-capped, but its resume-respawn can.
func (d *Daemon) setSessionContextWindowCap(sessionID string, cap int) error {
	if d == nil || d.store == nil {
		return fmt.Errorf("store unavailable")
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return fmt.Errorf("missing session_id")
	}
	session := d.store.Get(id)
	if session == nil {
		return fmt.Errorf("session not found")
	}
	if cap != 0 {
		if cap < contextWindowCapMin || cap > contextWindowCapMax {
			return fmt.Errorf("context window cap must be 0 (no cap) or between %d and %d tokens; got %d", contextWindowCapMin, contextWindowCapMax, cap)
		}
	}
	// Only the built-in claude/codex launch paths carry the cap to the agent
	// (env var / config override). Accepting it for a shell or a plugin driver
	// would store a pin that silently never applies.
	switch normalizeSpawnAgent(string(session.Agent)) {
	case string(protocol.SessionAgentClaude), string(protocol.SessionAgentCodex):
	default:
		return fmt.Errorf("agent %q takes no context-window cap; only claude and codex launches carry one", session.Agent)
	}
	// Idempotent: a repeated pin neither publishes a fact nothing acts on nor
	// kill-respawns an innocent agent.
	if protocol.Deref(session.ContextWindowCap) == cap {
		return nil
	}
	if !d.store.SetSessionContextWindowCap(id, cap) {
		return fmt.Errorf("persist context-window cap failed")
	}
	d.publishFact(FactSessionCapChanged, id, nil)
	if d.sessionHasLiveWorker(id) {
		go d.reloadSessionAgent(id)
	}
	return nil
}
