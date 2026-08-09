package activity

import (
	_ "embed"
	"os"
	"strings"
)

// Input is everything a run sees; the fields are not interchangeable.
type Input struct {
	// State is authoritative: a line describing active work for a blocked
	// session is wrong however it reads.
	State string
	// StateReason is the resolver's reason, when it owns the state.
	StateReason string
	// Window is the rendered transcript delta since the last line.
	Window string
	// Previous is the last line for this session: the only anchor without a
	// full-file scan, and the echo risk the template counters.
	Previous string
}

// Template is a named prompt variant, on disk so iterating one needs no rebuild.
type Template struct {
	Name string
	Body string
}

// SystemMarker splits a template into its invariant half and this run's data.
// The first half REPLACES the CLI's own system prompt, worth ~22K tokens of
// billed prefix; without the marker the CLI keeps its default prefix.
const SystemMarker = "{{USER}}"

// Rendered is a prompt ready to send; the parts travel as different CLI arguments.
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

// Render substitutes {{STATE}}, {{STATE_REASON}}, {{PREVIOUS}} and {{WINDOW}}
// into the template, then splits the result on SystemMarker. Placeholders are
// literal tokens, not text/template actions, so prompts carry braces unescaped.
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

// Baseline is the shipped prompt, embedded so a run never depends on disk.
//
//go:embed prompts/baseline.md
var baselineBody string

// Baseline returns the shipped template.
func Baseline() Template { return Template{Name: "baseline", Body: baselineBody} }
