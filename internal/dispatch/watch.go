package dispatch

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	defaultWatchInterval  = time.Second
	defaultWatchMaxErrors = 5
	// watchErrorExitCode is returned when the watch gives up after repeated
	// fetch failures (e.g. the daemon went away).
	watchErrorExitCode = 1
	// summaryLineLimit keeps a single event on a single line so the consuming
	// Monitor batches cleanly; long summaries are truncated with an ellipsis.
	summaryLineLimit = 280
)

// Fetcher returns the current snapshot of the watched dispatch. found is false
// when the dispatch is not present (yet, or any longer); err is reserved for
// transport failures (a missing dispatch is found=false, not an error).
type Fetcher func() (dispatch *protocol.ChiefOfStaffDispatch, found bool, err error)

// WatchOptions tunes RunWatch. The zero value is valid (sensible defaults).
type WatchOptions struct {
	// DispatchID is used only to format diagnostics when the dispatch is gone or
	// the watch aborts before any snapshot was seen.
	DispatchID string
	// Interval between polls. Defaults to one second.
	Interval time.Duration
	// MaxErrors is the number of consecutive transport failures tolerated before
	// the watch aborts. Defaults to five.
	MaxErrors int
	// Sleep is injectable for tests; defaults to time.Sleep.
	Sleep func(time.Duration)
}

// RunWatch drives the classify -> emit -> exit loop. It writes one line per
// meaningful event to out (flushed per line by writing directly to an unbuffered
// stream) and returns the process exit code: 0 for a clean terminal event,
// non-zero for a failure terminal event. It blocks until the dispatch reaches a
// terminal state — it never returns silently on a dead dispatch.
func RunWatch(fetch Fetcher, out io.Writer, opts WatchOptions) int {
	sleep := opts.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	maxErrors := opts.MaxErrors
	if maxErrors <= 0 {
		maxErrors = defaultWatchMaxErrors
	}

	em := &emitter{dispatchID: opts.DispatchID}
	consecutiveErrors := 0
	for {
		d, found, err := fetch()
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxErrors {
				fmt.Fprintln(out, formatAbortLine(opts.DispatchID, err))
				return watchErrorExitCode
			}
			sleep(interval)
			continue
		}
		consecutiveErrors = 0

		line, emit, done, code := em.step(d, found)
		if emit {
			fmt.Fprintln(out, line)
		}
		if done {
			return code
		}
		sleep(interval)
	}
}

// emitter holds the cross-poll dedupe state so the same event is emitted once,
// while a genuinely new event (a different kind or a fresh summary) re-emits.
type emitter struct {
	dispatchID string
	seen       bool
	lastSig    string
	emitted    bool
}

func (e *emitter) step(d *protocol.ChiefOfStaffDispatch, found bool) (line string, emit, done bool, code int) {
	if !found || d == nil {
		// The dispatch is gone. Records are durable in production, so this only
		// happens if one was explicitly deleted (or never existed). Either way,
		// never hang: emit a neutral terminal and exit. Absence is not failure.
		reason := "dispatch_gone"
		if !e.seen {
			reason = "not_found"
		}
		return formatGoneLine(e.dispatchID, reason), true, true, 0
	}
	e.seen = true

	ev := Classify(*d)
	if ev.Kind == KindNone {
		return "", false, false, 0
	}

	sig := eventSignature(ev)
	if sig == e.lastSig && e.emitted {
		// Already surfaced this exact event; stay quiet but still honor terminal.
		return "", false, ev.Terminal, ev.ExitCode
	}
	e.lastSig = sig
	e.emitted = true
	return FormatLine(*d, ev), true, ev.Terminal, ev.ExitCode
}

func eventSignature(ev Event) string {
	return string(ev.Kind) + "\x00" + ev.Reason + "\x00" + ev.Summary + "\x00" + ev.NextAction
}

// FormatLine renders one event as a single human-readable line:
//
//	[done] refactor-badges (a1b2c3d4) · implemented the cache; tests green → ready to merge
//
// The kind tag leads so the chief can skim; the short id disambiguates; the gist
// and next action carry the actionable content. Newlines are collapsed and long
// text is truncated so a line stays a line.
func FormatLine(d protocol.ChiefOfStaffDispatch, ev Event) string {
	label := trim(d.Label)
	if label == "" {
		label = "dispatch"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s (%s)", ev.Kind, label, shortID(d.ID))

	summary := oneLine(ev.Summary)
	if summary == "" {
		summary = defaultSummary(ev.Reason)
	}
	if summary != "" {
		b.WriteString(" · ")
		b.WriteString(summary)
	}
	if next := oneLine(ev.NextAction); next != "" {
		b.WriteString(" → ")
		b.WriteString(next)
	}
	return b.String()
}

func formatGoneLine(dispatchID, reason string) string {
	return fmt.Sprintf("[%s] dispatch (%s) · %s", KindEnded, shortID(dispatchID), defaultSummary(reason))
}

func formatAbortLine(dispatchID string, err error) string {
	return fmt.Sprintf("[%s] dispatch (%s) · watch aborted: %s",
		KindFailed, shortID(dispatchID), oneLine(err.Error()))
}

func defaultSummary(reason string) string {
	switch reason {
	case "session_crashed":
		return "session was cut off mid-run without a structured report (crashed or killed)"
	case "session_closed":
		return "session ended without a structured report"
	case "session_idle":
		return "agent stopped without a structured report"
	case "awaiting_input":
		return "agent is waiting for direction (no report filed)"
	case "dispatch_gone":
		return "dispatch record is no longer present"
	case "not_found":
		return "dispatch not found"
	default:
		return ""
	}
}

func shortID(id string) string {
	id = trim(id)
	if id == "" {
		return "?"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// oneLine collapses all interior whitespace runs to single spaces and truncates
// to keep one event on one line. Truncation counts runes (not bytes) so it never
// splits a multi-byte character.
func oneLine(s string) string {
	s = trim(strings.Join(strings.Fields(s), " "))
	if r := []rune(s); len(r) > summaryLineLimit {
		return string(r[:summaryLineLimit-1]) + "…"
	}
	return s
}

func trim(s string) string { return strings.TrimSpace(s) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
