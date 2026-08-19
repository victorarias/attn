package transcript

import (
	"encoding/json"
	"strings"
)

// How full a live session's context is, read off the same provider records the
// cost extractor reads. The two quantities are different and must not be
// confused: TokenUsage SUMS a message's iterations because every one of them was
// billed, while occupancy is the size of the LAST request's prompt — what the
// session is carrying right now, and what the harness compacts when it grows
// past its own limit.

// ContextObservation is what one record says about a session's context.
type ContextObservation struct {
	// Tokens is the prompt the model was handed: everything the session carries,
	// cached or not.
	Tokens int64
	// Window is the harness's own context window, or 0 when the record does not
	// state one. Codex reports it on every token_count; Claude's transcript never
	// mentions it, which is why the budget cannot be a fraction of it.
	Window int64
}

// SupportsContextOccupancy reports whether attn can read context fill from this
// harness's transcript. It tracks SupportsUsage on purpose: both answers come
// from the same provider records, and an agent attn cannot cost is an agent
// attn cannot measure the context of either.
func SupportsContextOccupancy(agent string) bool { return SupportsUsage(agent) }

// ContextOccupancy reads one complete provider JSONL record. It is stateless:
// every record that carries an occupancy carries the whole absolute figure, so
// the newest reading wins and a replayed record says the same thing twice.
func ContextOccupancy(agent string, line []byte) (ContextObservation, bool) {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude":
		return claudeContextOccupancy(line)
	case "codex":
		return codexContextOccupancy(line)
	default:
		return ContextObservation{}, false
	}
}

func claudeContextOccupancy(line []byte) (ContextObservation, bool) {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Usage *claudeUsageFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" || entry.Message.Usage == nil {
		return ContextObservation{}, false
	}
	fields := *entry.Message.Usage
	// A message that made several requests reports each one; the last is the
	// prompt the session is actually carrying. Adding them would count the same
	// carried context once per request and read as full long before it is.
	if n := len(fields.Iterations); n > 0 {
		fields = fields.Iterations[n-1]
	}
	tokens := fields.InputTokens + fields.CacheReadInputTokens + fields.CacheCreationInputTokens
	if fields.InputTokens < 0 || fields.CacheReadInputTokens < 0 || fields.CacheCreationInputTokens < 0 || tokens <= 0 {
		return ContextObservation{}, false
	}
	return ContextObservation{Tokens: tokens}, true
}

func codexContextOccupancy(line []byte) (ContextObservation, bool) {
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Type != "event_msg" {
		return ContextObservation{}, false
	}
	var payload struct {
		Type string `json:"type"`
		Info *struct {
			LastTokenUsage struct {
				// Codex's input_tokens already includes cached_input_tokens, so it is
				// the whole prompt on its own.
				InputTokens int64 `json:"input_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	}
	if json.Unmarshal(envelope.Payload, &payload) != nil || payload.Type != "token_count" || payload.Info == nil {
		return ContextObservation{}, false
	}
	tokens := payload.Info.LastTokenUsage.InputTokens
	if tokens <= 0 {
		return ContextObservation{}, false
	}
	window := payload.Info.ModelContextWindow
	if window < 0 {
		window = 0
	}
	return ContextObservation{Tokens: tokens, Window: window}, true
}
