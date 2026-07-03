package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ConversationSlice is a small, classification-ready reduction of a (possibly
// huge) agent transcript: the human's original request, any later corrections,
// the most recent compaction summary (Claude only), and the agent's most
// recent status turns. It intentionally carries no tool-call/activity trace.
type ConversationSlice struct {
	Brief      string   // first genuine human turn (the delegation prompt), capped
	Rescoping  []string // later genuine human turns (corrections / scope changes), capped, oldest->newest
	Summary    string   // most recent compaction summary text, if any (Claude), capped
	AgentTurns []string // last N agent text turns, capped, oldest->newest
	HumanCount int      // total genuine human turns seen (pre-cap)
	AgentCount int      // total agent text turns seen (pre-cap)
}

// Empty reports whether no human/agent text or compaction summary was found.
func (s ConversationSlice) Empty() bool {
	return s.Brief == "" && len(s.Rescoping) == 0 && s.Summary == "" && len(s.AgentTurns) == 0
}

// Render renders the slice as a labeled prompt block, omitting empty sections.
func (s ConversationSlice) Render() string {
	var sections []string
	if s.Brief != "" {
		sections = append(sections, "## TICKET BRIEF (first human turn)\n"+s.Brief)
	}
	if len(s.Rescoping) > 0 {
		sections = append(sections, "## LATER HUMAN TURNS (re-scoping)\n"+strings.Join(s.Rescoping, "\n---\n"))
	}
	if s.Summary != "" {
		sections = append(sections, "## COMPACTION SUMMARY (most recent)\n"+s.Summary)
	}
	if len(s.AgentTurns) > 0 {
		sections = append(sections, "## AGENT'S LAST STATUS TURNS\n"+strings.Join(s.AgentTurns, "\n---\n"))
	}
	return strings.Join(sections, "\n\n")
}

// SliceOptions bounds how much of a transcript ExtractConversationSlice keeps.
// Any field that is <= 0 falls back to the DefaultSliceOptions value.
type SliceOptions struct {
	MaxRescopingTurns int // most recent human turns AFTER the brief (default 4)
	MaxAgentTurns     int // most recent agent text turns (default 6)
	TurnCharCap       int // per-turn char cap for brief/rescoping/agent (default 3000)
	SummaryCharCap    int // larger cap for the compaction summary (default 12000)
}

// DefaultSliceOptions returns the default SliceOptions.
func DefaultSliceOptions() SliceOptions {
	return SliceOptions{
		MaxRescopingTurns: 4,
		MaxAgentTurns:     6,
		TurnCharCap:       3000,
		SummaryCharCap:    12000,
	}
}

func resolveSliceOptions(opts SliceOptions) SliceOptions {
	def := DefaultSliceOptions()
	if opts.MaxRescopingTurns <= 0 {
		opts.MaxRescopingTurns = def.MaxRescopingTurns
	}
	if opts.MaxAgentTurns <= 0 {
		opts.MaxAgentTurns = def.MaxAgentTurns
	}
	if opts.TurnCharCap <= 0 {
		opts.TurnCharCap = def.TurnCharCap
	}
	if opts.SummaryCharCap <= 0 {
		opts.SummaryCharCap = def.SummaryCharCap
	}
	return opts
}

// sliceLineOrigin captures the Claude "origin" field used to distinguish
// genuine human turns from injected/tool-authored user-role content.
type sliceLineOrigin struct {
	Kind string `json:"kind"`
}

// sliceLine is a superset shape covering the Claude, Codex, and Copilot line
// formats. Unknown fields are ignored by encoding/json, so a single unmarshal
// is enough to try all three shapes for every line.
type sliceLine struct {
	// Claude
	Type             string           `json:"type"`
	IsCompactSummary bool             `json:"isCompactSummary"`
	Origin           *sliceLineOrigin `json:"origin"`
	Message          struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	// Codex (event_msg envelope only; response_item is intentionally ignored
	// here so the same turn is not double-counted)
	Payload json.RawMessage `json:"payload"`

	// Copilot
	Data struct {
		Content string `json:"content"`
	} `json:"data"`
}

// sliceBuilder accumulates a bounded ConversationSlice over a single
// streaming pass. It never retains more than a small, capped amount of text
// regardless of transcript size.
type sliceBuilder struct {
	opts SliceOptions

	haveFirst     bool
	firstHuman    string
	lastHumanText string
	tailHuman     []string // bounded tail of human turns after the first

	lastSummary string

	tailAgent []string // bounded tail of agent turns

	humanCount int
	agentCount int
}

func (b *sliceBuilder) addHuman(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if text == b.lastHumanText {
		// consecutive duplicate (e.g. a resent Codex user_message) - do not
		// double-count or double-store it.
		return
	}
	b.lastHumanText = text
	b.humanCount++

	if !b.haveFirst {
		b.firstHuman = text
		b.haveFirst = true
		return
	}

	b.tailHuman = append(b.tailHuman, text)
	if len(b.tailHuman) > b.opts.MaxRescopingTurns {
		b.tailHuman = b.tailHuman[len(b.tailHuman)-b.opts.MaxRescopingTurns:]
	}
}

func (b *sliceBuilder) addAgent(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.agentCount++

	b.tailAgent = append(b.tailAgent, text)
	if len(b.tailAgent) > b.opts.MaxAgentTurns {
		b.tailAgent = b.tailAgent[len(b.tailAgent)-b.opts.MaxAgentTurns:]
	}
}

func (b *sliceBuilder) setSummary(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.lastSummary = text
}

func (b *sliceBuilder) processLine(line []byte) {
	var e sliceLine
	if err := json.Unmarshal(line, &e); err != nil {
		return
	}

	switch e.Type {
	case "user":
		if e.IsCompactSummary {
			b.setSummary(extractTextContent(e.Message.Content))
			return
		}
		if e.Origin == nil || e.Origin.Kind == "human" {
			b.addHuman(extractTextContent(e.Message.Content))
		}
		return
	case "assistant":
		b.addAgent(extractTextContent(e.Message.Content))
		return
	case "event_msg":
		var payload codexEventMessage
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return
		}
		switch payload.Type {
		case "user_message":
			b.addHuman(payload.Message)
		case "agent_message":
			b.addAgent(payload.Message)
		}
		return
	case "user.message":
		b.addHuman(e.Data.Content)
		return
	case "assistant.message":
		b.addAgent(e.Data.Content)
		return
	}
}

func capText(s string, n int) string {
	s = strings.TrimSpace(s)
	if n > 0 && len(s) > n {
		return s[:n] + fmt.Sprintf("\n...[truncated, %d chars total]", len(s))
	}
	return s
}

func capTexts(items []string, n int) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = capText(s, n)
	}
	return out
}

func (b *sliceBuilder) toSlice(opts SliceOptions) ConversationSlice {
	return ConversationSlice{
		Brief:      capText(b.firstHuman, opts.TurnCharCap),
		Rescoping:  capTexts(b.tailHuman, opts.TurnCharCap),
		Summary:    capText(b.lastSummary, opts.SummaryCharCap),
		AgentTurns: capTexts(b.tailAgent, opts.TurnCharCap),
		HumanCount: b.humanCount,
		AgentCount: b.agentCount,
	}
}

// ExtractConversationSlice reads a JSONL transcript (Claude, Codex, or
// Copilot shaped) in a single streaming pass and reduces it to a small,
// bounded ConversationSlice suitable for downstream classification. Memory
// use is bounded regardless of transcript size or line length. Only a
// file-open/IO error returns a non-nil error; malformed or unrecognized
// lines are skipped silently.
func ExtractConversationSlice(path string, opts SliceOptions) (ConversationSlice, error) {
	opts = resolveSliceOptions(opts)

	file, err := os.Open(path)
	if err != nil {
		return ConversationSlice{}, err
	}
	defer file.Close()

	b := &sliceBuilder{opts: opts}

	if err := readJSONLLines(file, b.processLine); err != nil {
		return ConversationSlice{}, err
	}

	return b.toSlice(opts), nil
}
