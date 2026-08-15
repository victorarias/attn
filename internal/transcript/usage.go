package transcript

import (
	"encoding/json"
	"math"
	"os"
	"strings"
)

// TokenUsage is one provider billing response, normalized into independently
// priced token classes. Key is stable when a transcript record is replayed, so
// a durable consumer can upsert the absolute counters without charging twice.
type TokenUsage struct {
	Key                          string
	Model                        string
	InputTokens                  int64
	OutputTokens                 int64
	CacheWrite5mTokens           int64
	CacheWrite1hTokens           int64
	CacheWriteUnclassifiedTokens int64
	CacheReadTokens              int64
}

// HasTokens distinguishes a real usage observation from Claude's synthetic
// zero-token messages and Codex's info:null rate-limit updates.
func (u TokenUsage) HasTokens() bool {
	return u.InputTokens > 0 ||
		u.OutputTokens > 0 ||
		u.CacheWrite5mTokens > 0 ||
		u.CacheWrite1hTokens > 0 ||
		u.CacheWriteUnclassifiedTokens > 0 ||
		u.CacheReadTokens > 0
}

// UsageExtractor carries the small amount of provider context that usage
// records omit. In particular, Codex announces the model on turn_context and
// reports tokens later on token_count.
type UsageExtractor struct {
	agent      string
	codexModel string
	last       TokenUsage
	hasLast    bool
}

func NewUsageExtractor(agent string) *UsageExtractor {
	return &UsageExtractor{agent: strings.ToLower(strings.TrimSpace(agent))}
}

// SupportsUsage reports whether attn knows how to extract token usage from
// this harness's transcript format.
func SupportsUsage(agent string) bool {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

// Observe reads one complete provider JSONL record. sourceKey must identify the
// source record stably across replay; Follower supplies its transcript
// fingerprint and byte offset. Repeated identical Claude content-block records
// are suppressed, while a revised absolute snapshot for the same message is
// emitted so a durable consumer can apply only its delta.
func (e *UsageExtractor) Observe(line []byte, sourceKey string) (TokenUsage, bool) {
	var usage TokenUsage
	var ok bool
	switch e.agent {
	case "claude":
		usage, ok = extractClaudeUsage(line)
	case "codex":
		usage, ok = e.extractCodexUsage(line, sourceKey)
	default:
		return TokenUsage{}, false
	}
	if !ok {
		return TokenUsage{}, false
	}
	if e.hasLast && e.last == usage {
		return TokenUsage{}, false
	}
	e.last = usage
	e.hasLast = true
	return usage, true
}

func (e *UsageExtractor) setCodexModel(model string) {
	e.codexModel = strings.TrimSpace(model)
}

// seedCodexModelBefore restores the model context when a follower bootstraps
// from the middle or tail of a rollout. The closest earlier turn_context owns
// every later token_count until another turn_context replaces it.
func (e *UsageExtractor) seedCodexModelBefore(f *os.File, before int64) error {
	if e.agent != "codex" {
		return nil
	}
	for before > 0 {
		line, start, ok, err := previousCompleteLine(f, before)
		if err != nil || !ok {
			return err
		}
		if model, isTurnContext := codexTurnContextModel(line); isTurnContext {
			e.setCodexModel(model)
			return nil
		}
		before = start
	}
	return nil
}

type claudeUsageFields struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreation            struct {
		Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	Iterations []claudeUsageFields `json:"iterations"`
}

func extractClaudeUsage(line []byte) (TokenUsage, bool) {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			ID    string             `json:"id"`
			Model string             `json:"model"`
			Usage *claudeUsageFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" {
		return TokenUsage{}, false
	}
	id := strings.TrimSpace(entry.Message.ID)
	if id == "" || entry.Message.Usage == nil {
		return TokenUsage{}, false
	}

	usage := TokenUsage{Key: "claude:" + id, Model: strings.TrimSpace(entry.Message.Model)}
	if len(entry.Message.Usage.Iterations) == 0 {
		if !addClaudeUsage(&usage, *entry.Message.Usage) {
			return TokenUsage{}, false
		}
		return usage, true
	}
	// Current Claude transcripts sometimes zero the top-level counters while
	// retaining the actual request in iterations. When iterations are present,
	// they are the authority; adding both would double-charge the common case
	// where the top level repeats the sole iteration.
	for _, iteration := range entry.Message.Usage.Iterations {
		if !addClaudeUsage(&usage, iteration) {
			return TokenUsage{}, false
		}
	}
	return usage, true
}

func addClaudeUsage(dst *TokenUsage, src claudeUsageFields) bool {
	values := [...]int64{
		src.InputTokens,
		src.OutputTokens,
		src.CacheCreationInputTokens,
		src.CacheReadInputTokens,
		src.CacheCreation.Ephemeral5mInputTokens,
		src.CacheCreation.Ephemeral1hInputTokens,
	}
	for _, value := range values {
		if value < 0 {
			return false
		}
	}

	classifiedWrite := src.CacheCreation.Ephemeral5mInputTokens + src.CacheCreation.Ephemeral1hInputTokens
	if classifiedWrite < 0 { // int64 overflow
		return false
	}
	unclassifiedWrite := src.CacheCreationInputTokens - classifiedWrite
	if unclassifiedWrite < 0 {
		// Some transcript versions carry the breakdown even when the aggregate is
		// zero. The priced classes are still complete; there is no residual.
		unclassifiedWrite = 0
	}
	updates := []struct {
		dst   *int64
		value int64
	}{
		{&dst.InputTokens, src.InputTokens},
		{&dst.OutputTokens, src.OutputTokens},
		{&dst.CacheWrite5mTokens, src.CacheCreation.Ephemeral5mInputTokens},
		{&dst.CacheWrite1hTokens, src.CacheCreation.Ephemeral1hInputTokens},
		{&dst.CacheWriteUnclassifiedTokens, unclassifiedWrite},
		{&dst.CacheReadTokens, src.CacheReadInputTokens},
	}
	for _, update := range updates {
		if update.value > math.MaxInt64-*update.dst {
			return false
		}
	}
	for _, update := range updates {
		*update.dst += update.value
	}
	return true
}

func (e *UsageExtractor) extractCodexUsage(line []byte, sourceKey string) (TokenUsage, bool) {
	if model, ok := codexTurnContextModel(line); ok {
		e.setCodexModel(model)
		return TokenUsage{}, false
	}
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return TokenUsage{}, false
	}
	if envelope.Type != "event_msg" || strings.TrimSpace(sourceKey) == "" {
		return TokenUsage{}, false
	}
	var payload struct {
		Type string `json:"type"`
		Info *struct {
			LastTokenUsage struct {
				InputTokens       int64 `json:"input_tokens"`
				CachedInputTokens int64 `json:"cached_input_tokens"`
				OutputTokens      int64 `json:"output_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	}
	if json.Unmarshal(envelope.Payload, &payload) != nil || payload.Type != "token_count" || payload.Info == nil {
		return TokenUsage{}, false
	}
	last := payload.Info.LastTokenUsage
	if last.InputTokens < 0 || last.CachedInputTokens < 0 || last.OutputTokens < 0 || last.CachedInputTokens > last.InputTokens {
		return TokenUsage{}, false
	}
	// Captured Codex totals equal input_tokens + output_tokens; the reported
	// reasoning_output_tokens is an output breakdown, so pricing it again would double-charge.
	return TokenUsage{
		Key:             "codex:" + strings.TrimSpace(sourceKey),
		Model:           e.codexModel,
		InputTokens:     last.InputTokens - last.CachedInputTokens,
		OutputTokens:    last.OutputTokens,
		CacheReadTokens: last.CachedInputTokens,
	}, true
}

func codexTurnContextModel(line []byte) (string, bool) {
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Type != "turn_context" {
		return "", false
	}
	var payload struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(envelope.Payload, &payload) != nil {
		return "", false
	}
	return payload.Model, true
}
