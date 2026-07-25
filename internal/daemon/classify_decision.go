package daemon

import (
	"errors"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
)

// Deciding what a settled turn means is a sequence of rules interleaved with two
// slow, fallible steps: reading the transcript off disk and asking an LLM. The
// rules are here as pure functions so they can be read and tested as rules;
// classifySessionState is the shell that performs the IO between them and owns
// every store write.
//
// Each function answers with a classifyDecision: apply this state, or take the
// next IO step, or leave the state alone.

type classifyAction int

const (
	// classifyApply: the state is decided; apply it.
	classifyApply classifyAction = iota
	// classifyReadTranscript: the transcript must be read to decide.
	classifyReadTranscript
	// classifyRunClassifier: the last assistant message must go to the classifier.
	classifyRunClassifier
	// classifySkip: nothing to decide, and nothing to write.
	classifySkip
)

type classifyDecision struct {
	action classifyAction
	// state is meaningful only for classifyApply.
	state string
	// reason is the diagnostic label logged with the decision. The names match the
	// ones the previous inline log lines used ("transcript_parse_error",
	// "classifier_error", "classifier_unknown_response"), so a log search for a
	// past incident still finds them.
	reason string
}

// classifyPreTranscript decides from what the daemon already holds, before paying
// for any IO.
//
// pendingTodos outranks everything: a turn that stopped with unfinished todos is
// waiting on the user. transcriptEnabled / classifierEnabled are the per-agent
// capability gates; an agent with either disabled settles idle rather than being
// left in whatever state it was.
func classifyPreTranscript(pendingTodos int, transcriptEnabled, classifierEnabled bool) classifyDecision {
	switch {
	case pendingTodos > 0:
		return classifyDecision{action: classifyApply, state: protocol.StateWaitingInput, reason: "pending_todos"}
	case !transcriptEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "transcript_disabled"}
	case !classifierEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "classifier_disabled"}
	default:
		return classifyDecision{action: classifyReadTranscript}
	}
}

// classifyPostTranscript decides from the transcript read.
//
// ErrNoNewAssistantTurn means the turn we would classify has already been
// classified, so there is nothing to say — importantly not "unknown", which would
// overwrite a good state with a bad one. Any other read error is genuinely
// unknown. An empty last message means the agent said nothing, which settles idle
// without paying for a classifier call.
func classifyPostTranscript(lastMessage string, err error) classifyDecision {
	if err != nil {
		if errors.Is(err, agentdriver.ErrNoNewAssistantTurn) {
			return classifyDecision{action: classifySkip, reason: "no_new_assistant_turn"}
		}
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "transcript_parse_error"}
	}
	if strings.TrimSpace(lastMessage) == "" {
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "empty_last_message"}
	}
	return classifyDecision{action: classifyRunClassifier}
}

// classifyVerdict maps the classifier's answer to a state. A failed call and an
// unknown answer both land on unknown, but they are separate reasons: one is our
// plumbing failing, the other is the classifier declining to decide.
func classifyVerdict(state string, err error) classifyDecision {
	if err != nil {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_error"}
	}
	if state == protocol.StateUnknown {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_unknown_response"}
	}
	return classifyDecision{action: classifyApply, state: state, reason: "classifier"}
}
