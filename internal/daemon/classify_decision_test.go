package daemon

import (
	"errors"
	"fmt"
	"testing"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClassifyPreTranscript(t *testing.T) {
	cases := []struct {
		name              string
		pendingTodos      int
		transcriptEnabled bool
		classifierEnabled bool
		stop              stopClassification
		wantAction        classifyAction
		wantState         string
	}{
		{
			// An agent sitting out its own build mid-plan has open todos precisely
			// because it is not finished; reading them as "waiting on the user"
			// would ring on every yielded stop of a multi-step task.
			name:              "a yield ignores pending todos and reads the transcript",
			pendingTodos:      3,
			transcriptEnabled: true,
			classifierEnabled: true,
			stop:              stopClassification{yielded: true},
			wantAction:        classifyReadTranscript,
		},
		{
			// No judgment is possible, so nothing is filed: settling idle here
			// would queue a turn the payload says will resume on its own.
			name:              "a yield with no classifier files nothing",
			transcriptEnabled: true,
			classifierEnabled: false,
			stop:              stopClassification{yielded: true},
			wantAction:        classifySkip,
		},
		{
			name:              "pending todos outrank everything",
			pendingTodos:      1,
			transcriptEnabled: true,
			classifierEnabled: true,
			wantAction:        classifyApply,
			wantState:         protocol.StateWaitingInput,
		},
		{
			name:              "pending todos win even with both capabilities off",
			pendingTodos:      2,
			transcriptEnabled: false,
			classifierEnabled: false,
			wantAction:        classifyApply,
			wantState:         protocol.StateWaitingInput,
		},
		{
			name:              "transcript disabled settles idle",
			transcriptEnabled: false,
			classifierEnabled: true,
			wantAction:        classifyApply,
			wantState:         protocol.StateIdle,
		},
		{
			name:              "classifier disabled settles idle",
			transcriptEnabled: true,
			classifierEnabled: false,
			wantAction:        classifyApply,
			wantState:         protocol.StateIdle,
		},
		{
			name:              "nothing decided yet reads the transcript",
			transcriptEnabled: true,
			classifierEnabled: true,
			wantAction:        classifyReadTranscript,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPreTranscript(tc.pendingTodos, tc.transcriptEnabled, tc.classifierEnabled, tc.stop)
			if got.action != tc.wantAction || got.state != tc.wantState {
				t.Fatalf("classifyPreTranscript() = %+v, want action %v state %q", got, tc.wantAction, tc.wantState)
			}
			if got.reason == "" && got.action == classifyApply {
				t.Fatal("an applied state must carry a reason")
			}
		})
	}
}

func TestClassifyPostTranscript(t *testing.T) {
	cases := []struct {
		name        string
		lastMessage string
		err         error
		stop        stopClassification
		wantAction  classifyAction
		wantState   string
		wantReason  string
	}{
		{
			name:       "no new assistant turn writes nothing",
			err:        fmt.Errorf("wrapped: %w", agentdriver.ErrNoNewAssistantTurn),
			wantAction: classifySkip,
			wantReason: "no_new_assistant_turn",
		},
		{
			// A yield's non-answers all file nothing: unknown or idle here would
			// move a turn the payload says will resume into the user's queue.
			name:       "a yield's read error files nothing",
			err:        errors.New("permission denied"),
			stop:       stopClassification{yielded: true},
			wantAction: classifySkip,
			wantReason: "yield_transcript_parse_error",
		},
		{
			name:       "a yield's empty message files nothing",
			stop:       stopClassification{yielded: true},
			wantAction: classifySkip,
			wantReason: "yield_empty_message",
		},
		{
			name:       "any other read error is unknown",
			err:        errors.New("permission denied"),
			wantAction: classifyApply,
			wantState:  protocol.StateUnknown,
			wantReason: "transcript_parse_error",
		},
		{
			name:       "empty message settles idle without a classifier call",
			wantAction: classifyApply,
			wantState:  protocol.StateIdle,
			wantReason: "empty_last_message",
		},
		{
			name:        "whitespace-only message counts as empty",
			lastMessage: "  \n\t ",
			wantAction:  classifyApply,
			wantState:   protocol.StateIdle,
			wantReason:  "empty_last_message",
		},
		{
			name:        "real message goes to the classifier",
			lastMessage: "Done. Want me to open the PR?",
			wantAction:  classifyRunClassifier,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPostTranscript(tc.lastMessage, tc.err, tc.stop)
			if got.action != tc.wantAction || got.state != tc.wantState || got.reason != tc.wantReason {
				t.Fatalf("classifyPostTranscript() = %+v, want action %v state %q reason %q",
					got, tc.wantAction, tc.wantState, tc.wantReason)
			}
		})
	}
}

func TestClassifyVerdict(t *testing.T) {
	cases := []struct {
		name       string
		state      string
		err        error
		stop       stopClassification
		wantState  string
		wantReason string
	}{
		{
			name:       "a parked verdict on a yield passes through",
			state:      classifier.VerdictParked,
			stop:       stopClassification{yielded: true},
			wantState:  classifier.VerdictParked,
			wantReason: "classifier",
		},
		{
			// PARKED's precondition — the harness-facts line — is only in a yield's
			// input; answered without it, the judge misapplied the rule. Unknown
			// files no evidence, so the session settles on its own fallback.
			name:       "a parked verdict without a yield is no verdict",
			state:      classifier.VerdictParked,
			wantState:  protocol.StateUnknown,
			wantReason: "classifier_parked_without_yield",
		},
		{
			name:       "classifier failure is unknown, attributed to us",
			state:      "",
			err:        errors.New("timeout"),
			wantState:  protocol.StateUnknown,
			wantReason: "classifier_error",
		},
		{
			name:       "an unknown answer is attributed to the classifier",
			state:      protocol.StateUnknown,
			wantState:  protocol.StateUnknown,
			wantReason: "classifier_unknown_response",
		},
		{
			name:       "a real answer passes through",
			state:      protocol.StateWaitingInput,
			wantState:  protocol.StateWaitingInput,
			wantReason: "classifier",
		},
		{
			name:       "a failed call outranks whatever state came back with it",
			state:      protocol.StateIdle,
			err:        errors.New("timeout"),
			wantState:  protocol.StateUnknown,
			wantReason: "classifier_error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyVerdict(tc.state, tc.err, tc.stop)
			if got.action != classifyApply || got.state != tc.wantState || got.reason != tc.wantReason {
				t.Fatalf("classifyVerdict() = %+v, want state %q reason %q", got, tc.wantState, tc.wantReason)
			}
		})
	}
}
