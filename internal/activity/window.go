// Package activity turns a session's recent transcript into the input for a
// one-line "what is this agent doing right now" summary.
//
// The window is a delta: everything appended since the last line was generated,
// read forward from a stored cursor. That is both the trigger (a cursor that has
// not moved means there is nothing new to say) and the input, which is why the
// two are the same value.
//
// Design and the measurements behind every number here:
// docs/plans/2026-08-07-session-activity.md
package activity

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/transcript"
)

// Clip budgets per event kind, in characters. Thinking gets the largest budget
// because it is where an agent states intent and it carries roughly twice the
// volume of assistant prose, but it is also the longest content type (p95 3,520
// chars, max 20,443 measured), so it needs the hardest ceiling in absolute
// terms. Tool results are kept even when they did not error — "42 passed, 3
// failed" is exactly the outcome a line should reflect — and the clip is what
// stops one giant result crowding out the rest of the window.
const (
	ClipThinking   = 600
	ClipAssistant  = 400
	ClipUser       = 300
	ClipToolCall   = 200
	ClipToolResult = 150
)

// Tripwires, set past the observed maximum so only something broken touches
// them. Measured across 617 active 3-minute windows: 64 events and 12,408
// rendered chars at the maximum. A cold start or a raised throttle can legitimately
// produce more, which is why these are generous rather than tight.
const (
	MaxEvents = 200
	MaxChars  = 32000
)

// Window is one bounded read of what a session did since its last activity line.
type Window struct {
	Events []transcript.Event
	// NextCursor is where the next window starts. It advances even across events
	// this window dropped, so a dropped burst is never re-read.
	NextCursor string
	// Report says what was left out. A caller that ignores it will present a
	// silently short window as if it were the whole story.
	Report Report
}

// Report records what a window read left out, so a limit that gets hit names
// itself instead of vanishing.
type Report struct {
	// DroppedOld counts events discarded because the window exceeded MaxEvents
	// or MaxChars. The newest are kept: this is a question about now.
	DroppedOld int
	// TotalEvents is how many events the read produced before capping.
	TotalEvents int
	// HitEventCap and HitCharCap name which tripwire fired.
	HitEventCap bool
	HitCharCap  bool
}

// Truncated reports whether anything was left out.
func (r Report) Truncated() bool { return r.DroppedOld > 0 }

// String renders the report for a log line or a prompt note, naming the limit,
// its value, and the ask — an agent can act on that; "some events were dropped"
// is not actionable.
func (r Report) String() string {
	if !r.Truncated() {
		return ""
	}
	switch {
	case r.HitEventCap:
		return fmt.Sprintf("dropped %d of %d events (max_events=%d)", r.DroppedOld, r.TotalEvents, MaxEvents)
	case r.HitCharCap:
		return fmt.Sprintf("dropped %d of %d events (max_chars=%d)", r.DroppedOld, r.TotalEvents, MaxChars)
	default:
		return fmt.Sprintf("dropped %d of %d events", r.DroppedOld, r.TotalEvents)
	}
}

// Empty reports whether the window carries nothing to summarize.
func (w Window) Empty() bool { return len(w.Events) == 0 }

// Read returns everything appended to the transcript after cursor, capped
// newest-first. An empty cursor reads from the start, which a caller should
// avoid on a large transcript: seed at head instead and let the next real
// movement produce the first line.
func Read(path, agent, cursor string) (Window, error) {
	page, err := transcript.ReadEventPage(path, agent, cursor, MaxEvents+1)
	if err != nil {
		return Window{}, err
	}
	window := Window{Events: page.Events, NextCursor: page.NextCursor}
	window.Report.TotalEvents = len(page.Events)
	window.cap()
	return window, nil
}

// SeedCursor returns a cursor positioned at the transcript's head without
// producing a window. It is the cold-start path: reading a large transcript from
// byte 0 to make a first line would cost a full scan (1.37s on the largest live
// transcript measured) and would summarize the session's whole history rather
// than the last few moments. Seed instead, and let the next real movement
// produce the first line. It is also the recovery path for ErrCursorMismatch,
// which is normal rather than exceptional — Claude compaction rewrites the file.
func SeedCursor(path string) (string, error) {
	return transcript.HeadCursor(path)
}

// cap trims the window to the tripwires, keeping the newest events. The cursor
// is left untouched: it already points past everything read, so a dropped burst
// is dropped once rather than re-read on the next pass.
func (w *Window) cap() {
	if len(w.Events) > MaxEvents {
		w.Report.DroppedOld += len(w.Events) - MaxEvents
		w.Report.HitEventCap = true
		w.Events = w.Events[len(w.Events)-MaxEvents:]
	}
	total := 0
	keepFrom := len(w.Events)
	for i := len(w.Events) - 1; i >= 0; i-- {
		size := len(clip(w.Events[i])) + 24 // + the rendered label
		if total+size > MaxChars {
			break
		}
		total += size
		keepFrom = i
	}
	if keepFrom > 0 {
		w.Report.DroppedOld += keepFrom
		w.Report.HitCharCap = true
		w.Events = w.Events[keepFrom:]
	}
}

// clipFor returns the character budget for an event kind.
func clipFor(kind string) int {
	switch kind {
	case transcript.EventKindThinking:
		return ClipThinking
	case transcript.EventKindAssistant:
		return ClipAssistant
	case transcript.EventKindUser:
		return ClipUser
	case transcript.EventKindToolCall:
		return ClipToolCall
	default:
		return ClipToolResult
	}
}

// clip collapses whitespace and cuts an event's text to its kind's budget.
func clip(event transcript.Event) string {
	text := strings.Join(strings.Fields(event.Text), " ")
	budget := clipFor(event.Kind)
	if len(text) <= budget {
		return text
	}
	return text[:budget] + "…"
}

// Render turns the window into the labeled block the model reads, oldest first.
// Labels are the event kinds themselves so the model can tell the agent's own
// reasoning ("thinking") from what the user was shown ("assistant") — they mean
// different things when deciding what an agent is up to.
func (w Window) Render() string {
	var b strings.Builder
	for _, event := range w.Events {
		text := clip(event)
		if text == "" {
			continue
		}
		switch event.Kind {
		case transcript.EventKindToolCall:
			fmt.Fprintf(&b, "tool_call %s: %s\n", event.ToolName, text)
		case transcript.EventKindToolResult:
			if event.IsError {
				fmt.Fprintf(&b, "tool_result ERROR: %s\n", text)
				continue
			}
			fmt.Fprintf(&b, "tool_result: %s\n", text)
		default:
			fmt.Fprintf(&b, "%s: %s\n", event.Kind, text)
		}
	}
	if note := w.Report.String(); note != "" {
		fmt.Fprintf(&b, "\n[window truncated: %s]\n", note)
	}
	return strings.TrimRight(b.String(), "\n")
}
