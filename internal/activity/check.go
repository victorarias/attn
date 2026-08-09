package activity

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxLineRunes bounds a rendered activity line. A dashboard row is glanced at,
// not read: past this it stops being scannable and starts being a sentence.
const MaxLineRunes = 90

// blockedStates are the states where the agent is not doing anything — it is
// waiting on the user. A line claiming active work in one of these is wrong
// regardless of how well it reads.
var blockedStates = map[string]bool{
	"pending_approval": true,
	"waiting_input":    true,
	"idle":             true,
	"recoverable":      true,
}

// blockedVocabulary is the set of words that signal a line acknowledged the
// session is not working. Deliberately generous: the check exists to catch a
// line that confidently narrates activity for a blocked agent, not to police
// phrasing.
var blockedVocabulary = []string{
	"await", "waiting", "wait", "blocked", "needs", "asks", "asked", "approval",
	"approve", "idle", "done", "finished", "complete", "ready", "paused",
	"stopped", "halted", "stuck", "pending", "requires", "question", "confirm",
	"review", "died", "crashed", "stalled",
}

// Violation is one failed check on a generated line.
type Violation struct {
	Check   string
	Message string
}

// Check runs every deterministic assertion against a generated line. These do
// not judge whether a line is *good* — no automated check can — but they make
// the failures already observed in practice loud instead of silent. Quality
// stays a human read of the report's side-by-side output.
func Check(line, state string) []Violation {
	var violations []Violation
	add := func(check, format string, args ...any) {
		violations = append(violations, Violation{Check: check, Message: fmt.Sprintf(format, args...)})
	}

	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		add("nonempty", "line is empty")
		return violations
	}

	if n := utf8.RuneCountInString(trimmed); n > MaxLineRunes {
		add("length", "line is %d runes, max_line_runes=%d", n, MaxLineRunes)
	}
	if strings.Contains(trimmed, "\n") {
		add("single_line", "line contains a newline")
	}
	if strings.HasSuffix(trimmed, ".") {
		add("no_trailing_period", "line ends with a period")
	}
	if (strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`)) ||
		(strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'")) {
		add("no_quotes", "line is wrapped in quotes")
	}
	// A preamble means the model answered the request instead of doing it. The
	// colon test is narrow on purpose: "Fixing auth: token refresh" is a fine
	// line, so only a leading conversational stem counts.
	for _, stem := range []string{"the agent", "this agent", "here is", "here's", "summary", "activity", "status"} {
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, stem+":") || strings.HasPrefix(lower, stem+" is") {
			add("no_preamble", "line opens with a preamble: %q", stem)
			break
		}
	}

	// The load-bearing check. An earlier spike produced a fluent, entirely wrong
	// line for a pending_approval session because the prompt withheld the state;
	// the line described work the agent had already stopped doing. Anything that
	// regresses that must fail here.
	if blockedStates[strings.TrimSpace(state)] {
		lower := strings.ToLower(trimmed)
		acknowledged := false
		for _, word := range blockedVocabulary {
			if strings.Contains(lower, word) {
				acknowledged = true
				break
			}
		}
		if !acknowledged {
			add("state_consistency",
				"state=%s (agent is not working) but the line does not say so", state)
		}
	}

	return violations
}
