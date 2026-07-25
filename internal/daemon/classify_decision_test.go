package daemon

import (
	"errors"
	"fmt"
	"testing"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClassifyPreTranscript(t *testing.T) {
	cases := []struct {
		name              string
		pendingTodos      int
		transcriptEnabled bool
		classifierEnabled bool
		wantAction        classifyAction
		wantState         string
	}{
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
			got := classifyPreTranscript(tc.pendingTodos, tc.transcriptEnabled, tc.classifierEnabled)
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
			got := classifyPostTranscript(tc.lastMessage, tc.err)
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
		wantState  string
		wantReason string
	}{
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
			got := classifyVerdict(tc.state, tc.err)
			if got.action != classifyApply || got.state != tc.wantState || got.reason != tc.wantReason {
				t.Fatalf("classifyVerdict() = %+v, want state %q reason %q", got, tc.wantState, tc.wantReason)
			}
		})
	}
}
