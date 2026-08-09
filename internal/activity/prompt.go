package activity

import (
	"os"
	"strings"
)

// Input is everything a run sees. The three fields are not interchangeable and
// the prompt must keep them apart: State is ground truth from attn's own
// deterministic classifier, Window is what actually happened, and Previous is
// only context.
type Input struct {
	// State is the session's current daemon state. It is authoritative: a line
	// that describes active work for a blocked session is wrong no matter how
	// well it reads, which is the exact defect an earlier spike produced when
	// state was withheld from the prompt.
	State string
	// StateReason is the resolver's reason, when it owns the state.
	StateReason string
	// Window is the rendered transcript delta since the last line.
	Window string
	// Previous is the last activity line generated for this session. It is the
	// only anchor available without a full-file scan, and it is what lets a
	// state-change breakthrough produce a useful line from a near-empty window.
	// It is also the echo risk: fed back forever, a stale subject stays alive and
	// reads plausibly, so the template must say the window wins.
	Previous string
}

// Template is a named prompt variant. Templates live on disk under
// prompts/activity/ so iterating one needs no rebuild.
type Template struct {
	Name string
	Body string
}

// SystemMarker splits a template into the half that never varies (the role, the
// rules, the output contract) and the half that carries this run's data. The
// first half is sent as the agent CLI's system prompt, which REPLACES the CLI's
// own — the single largest cost lever on a run this small, worth ~22K tokens of
// billed prefix.
//
// A template without the marker is all user prompt, and the CLI keeps its
// default system prompt. That still works, it just costs the full prefix.
const SystemMarker = "{{USER}}"

// Rendered is a prompt ready to send: two parts, because they travel as
// different CLI arguments.
type Rendered struct {
	System string
	User   string
}

// Chars is the whole prompt's size, for cost and budget reporting.
func (r Rendered) Chars() int { return len(r.System) + len(r.User) }

// LoadTemplate reads a prompt variant from disk.
func LoadTemplate(name, path string) (Template, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	return Template{Name: name, Body: string(body)}, nil
}

// Render substitutes the input into the template. Placeholders are literal
// tokens rather than text/template actions so a prompt author can write braces,
// JSON, and code samples without escaping anything.
//
//	{{STATE}}          the session's current state
//	{{STATE_REASON}}   why the resolver reached it, or "unspecified"
//	{{PREVIOUS}}       the last line, or "(none — this is the first)"
//	{{WINDOW}}         the rendered transcript delta
//
// SystemMarker splits the result into its two parts.
func (t Template) Render(in Input) Rendered {
	reason := strings.TrimSpace(in.StateReason)
	if reason == "" {
		reason = "unspecified"
	}
	previous := strings.TrimSpace(in.Previous)
	if previous == "" {
		previous = "(none — this is the first line for this session)"
	}
	window := strings.TrimSpace(in.Window)
	if window == "" {
		window = "(nothing new since the last line)"
	}
	replacer := strings.NewReplacer(
		"{{STATE}}", strings.TrimSpace(in.State),
		"{{STATE_REASON}}", reason,
		"{{PREVIOUS}}", previous,
		"{{WINDOW}}", window,
	)
	body := replacer.Replace(t.Body)
	system, user, split := strings.Cut(body, SystemMarker)
	if !split {
		return Rendered{User: strings.TrimSpace(body)}
	}
	return Rendered{System: strings.TrimSpace(system), User: strings.TrimSpace(user)}
}
