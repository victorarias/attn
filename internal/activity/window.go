// Package activity turns a session's recent transcript into the input for a
// one-line "what is this agent doing right now" summary. The window is a delta
// read forward from a stored cursor: both the trigger and the input.
//
// Design and measurements: docs/plans/2026-08-07-session-activity.md
package activity

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/transcript"
)

// Clip budgets per event kind, in characters. Thinking carries the intent and
// the most volume, and is also the longest content type (measured: p95 3,520
// chars, max 20,443), so it gets the largest budget and the hardest ceiling.
const (
	ClipThinking   = 600
	ClipAssistant  = 400
	ClipUser       = 300
	ClipToolCall   = 200
	ClipToolResult = 150
)

// Tripwires set past the observed maximum. Measured across 617 active 3-minute
// windows: 64 events and 12,408 rendered chars at the maximum.
const (
	MaxEvents = 200
	MaxChars  = 32000
)

// Window is one bounded read of what a session did since its last activity line.
type Window struct {
	Events []transcript.Event
	// NextCursor advances across dropped events too, so a burst is never re-read.
	NextCursor string
	// Report says what was left out; ignoring it presents a short window as whole.
	Report Report
}

// Report records what a read left out, so a limit that fires names itself.
type Report struct {
	// DroppedOld counts events discarded at MaxEvents/MaxChars; the newest are kept.
	DroppedOld int
	// TotalEvents is how many events the read produced before capping.
	TotalEvents int
	// HitEventCap and HitCharCap name which tripwire fired.
	HitEventCap bool
	HitCharCap  bool
}

// Truncated reports whether anything was left out.
func (r Report) Truncated() bool { return r.DroppedOld > 0 }

// String renders the report naming the limit, its value, and the ask.
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

// MaxPages bounds how far Read walks to reach the end of a delta. A tripwire:
// the largest measured delta across a working day is well under one page.
const MaxPages = 50

// ErrDeltaTooLarge reports a delta MaxPages could not reach the end of; the
// caller re-seeds instead.
var ErrDeltaTooLarge = fmt.Errorf("activity: delta exceeds %d pages of %d events", MaxPages, MaxEvents)

// Read returns the NEWEST events appended after cursor, walking forward because
// a large delta's first page is its oldest part. An empty cursor reads from the
// start; prefer SeedCursor on a large transcript.
func Read(path, agent, cursor string) (Window, error) {
	window := Window{}
	at := cursor
	for page := 0; ; page++ {
		if page >= MaxPages {
			return Window{}, ErrDeltaTooLarge
		}
		read, err := transcript.ReadEventPage(path, agent, at, MaxEvents+1)
		if err != nil {
			return Window{}, err
		}
		window.Report.TotalEvents += len(read.Events)
		window.NextCursor = read.NextCursor
		// Rolling tail: holding the whole delta would size memory to the backlog.
		window.Events = tail(append(window.Events, read.Events...), MaxEvents+1)
		if read.AtEnd {
			break
		}
		at = read.NextCursor
	}
	window.cap()
	return window, nil
}

// tail keeps the last n elements, reusing the backing array.
func tail(events []transcript.Event, n int) []transcript.Event {
	if len(events) <= n {
		return events
	}
	return append(events[:0], events[len(events)-n:]...)
}

// SeedCursor positions a cursor at the transcript head: the cold-start path (a
// full scan measured 1.37s on the largest live transcript) and the recovery for
// ErrCursorMismatch, which Claude compaction makes routine.
func SeedCursor(path string) (string, error) {
	return transcript.HeadCursor(path)
}

// cap trims to the tripwires, newest kept. The cursor is left untouched, so a
// dropped burst is dropped once rather than re-read.
func (w *Window) cap() {
	// Counted against everything the delta held, not against the tail.
	if w.Report.TotalEvents > MaxEvents {
		w.Report.DroppedOld += w.Report.TotalEvents - MaxEvents
		w.Report.HitEventCap = true
	}
	if len(w.Events) > MaxEvents {
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

// Render is the labeled block the model reads, oldest first; the labels are the
// event kinds, which keep "thinking" apart from "assistant".
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
