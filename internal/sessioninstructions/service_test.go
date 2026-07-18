package sessioninstructions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

type fakeStore struct {
	session *protocol.Session
	resume  string
}

func (s fakeStore) Get(string) *protocol.Session     { return s.session }
func (s fakeStore) GetResumeSessionID(string) string { return s.resume }

type fakeFinder struct{ path string }

func (f fakeFinder) FindTranscriptForResume(string) string { return f.path }

type fakeModel struct {
	answers  []ModelAnswer
	errs     []error
	requests []ModelRequest
}

func (m *fakeModel) Run(_ context.Context, request ModelRequest) (ModelAnswer, error) {
	m.requests = append(m.requests, request)
	i := len(m.requests) - 1
	if i < len(m.errs) && m.errs[i] != nil {
		return ModelAnswer{}, m.errs[i]
	}
	return m.answers[i], nil
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testService(path string, model *fakeModel) Service {
	return Service{Store: fakeStore{session: &protocol.Session{ID: "target", Agent: protocol.SessionAgentCodex}, resume: "native-target"}, Finder: fakeFinder{path: path}, Model: model}
}

func TestAskProjectsOnlyConversationAndCopiesExactQuotes(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"session_meta","payload":{"id":"native-target"}}`,
		`{"timestamp":"2026-07-18T10:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Please create PR #571. Do not merge it."}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"shell"}}`,
		`{"timestamp":"2026-07-18T10:01:00Z","type":"event_msg","payload":{"type":"agent_message","message":"The PR is ready. Should I merge it?"}}`,
		`{"timestamp":"2026-07-18T10:02:00Z","type":"event_msg","payload":{"type":"user_message","message":"Yes, do it."}}`,
	)
	model := &fakeModel{answers: []ModelAnswer{{Answer: "Yes. The user authorized the merge.", Evidence: []CandidateEvidence{{TurnID: "turn-3", Quote: "Yes, do it."}, {TurnID: "turn-2", Quote: "Should I merge it?"}}}}}
	result, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "Was merging PR #571 authorized?"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasoningEffort != lowEffort || result.Model != ModelName {
		t.Fatalf("result model = %+v", result)
	}
	if len(model.requests) != 1 || len(model.requests[0].Conversation) != 3 {
		t.Fatalf("projection = %+v", model.requests)
	}
	if result.Evidence[0].TurnID != "turn-2" || result.Evidence[1].TurnID != "turn-3" {
		t.Fatalf("evidence order = %+v", result.Evidence)
	}
	if result.Evidence[1].Quote != "Yes, do it." || !strings.HasPrefix(result.TranscriptFingerprint, "sha256:") {
		t.Fatalf("result = %+v", result)
	}
}

func TestAskRetriesInvalidEvidenceAtMedium(t *testing.T) {
	path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"user_message","message":"Yes, do it."}}`)
	model := &fakeModel{answers: []ModelAnswer{
		{Answer: "Yes.", Evidence: []CandidateEvidence{{TurnID: "turn-9", Quote: "Yes"}}},
		{Answer: "Yes.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "Yes, do it."}}},
	}}
	result, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "Was it authorized?"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasoningEffort != mediumEffort || len(model.requests) != 2 {
		t.Fatalf("result=%+v calls=%d", result, len(model.requests))
	}
	if model.requests[0].Effort != lowEffort || model.requests[1].Effort != mediumEffort || len(model.requests[1].PreviousValidationErrors) == 0 {
		t.Fatalf("requests=%+v", model.requests)
	}
}

func TestAskUnclearIsSuccessful(t *testing.T) {
	path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"user_message","message":"We discussed deployment timing."}}`)
	model := &fakeModel{answers: []ModelAnswer{{Answer: "Unclear. The conversation does not say whether deployment was approved.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "deployment timing"}}}}}
	result, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "Was deployment approved?"})
	if err != nil || !strings.HasPrefix(result.Answer, "Unclear.") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAskReturnsNegativeAnswer(t *testing.T) {
	path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"user_message","message":"Do not merge the PR."}}`)
	model := &fakeModel{answers: []ModelAnswer{{Answer: "No. The user prohibited merging.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "Do not merge"}}}}}
	result, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "Was merging PR #571 authorized?"})
	if err != nil || !strings.HasPrefix(result.Answer, "No.") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAskNormalizesOneWordYesNoAndUnclear(t *testing.T) {
	for _, answer := range []string{"Yes", "No", "Unclear"} {
		t.Run(answer, func(t *testing.T) {
			path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"user_message","message":"hello"}}`)
			model := &fakeModel{answers: []ModelAnswer{{Answer: answer, Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "hello"}}}}}
			result, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "What did the user say?"})
			if err != nil || result.Answer != answer+"." {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestAskRejectsAssistantOnlyAuthorization(t *testing.T) {
	path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"agent_message","message":"I merged PR #571."}}`)
	model := &fakeModel{answers: []ModelAnswer{
		{Answer: "Yes.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "merged PR #571"}}},
		{Answer: "Yes.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "merged PR #571"}}},
	}}
	_, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "Was merging PR #571 authorized?"})
	var sessionErr *Error
	if !errors.As(err, &sessionErr) || sessionErr.Code != "invalid_evidence" {
		t.Fatalf("err=%v", err)
	}
}

func TestAskAllowsAssistantEvidenceForAssistantStatementQuestion(t *testing.T) {
	path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"agent_message","message":"I merged PR #571."}}`)
	model := &fakeModel{answers: []ModelAnswer{{Answer: "Yes. The agent said it merged PR #571.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "merged PR #571"}}}}}
	result, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "Did the agent say it merged PR #571?"})
	if err != nil || !strings.HasPrefix(result.Answer, "Yes.") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAskFailsClosedForMalformedAmbiguousOversizedAndModelFailure(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		model := &fakeModel{}
		_, err := testService(writeTranscript(t, `not-json`), model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "What happened?"})
		assertCode(t, err, "transcript_unavailable")
	})
	t.Run("missing", func(t *testing.T) {
		_, err := testService(filepath.Join(t.TempDir(), "missing.jsonl"), &fakeModel{}).Ask(context.Background(), Request{TargetSessionID: "target", Question: "What happened?"})
		assertCode(t, err, "transcript_unavailable")
	})
	t.Run("ambiguous evidence", func(t *testing.T) {
		path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"user_message","message":"yes yes"}}`)
		model := &fakeModel{answers: []ModelAnswer{{Answer: "Yes.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "yes"}}}, {Answer: "Yes.", Evidence: []CandidateEvidence{{TurnID: "turn-1", Quote: "yes"}}}}}
		_, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "Was it authorized?"})
		assertCode(t, err, "invalid_evidence")
	})
	t.Run("oversized", func(t *testing.T) {
		path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"user_message","message":"abcdefghij"}}`)
		service := testService(path, &fakeModel{})
		service.ConversationMaxChars = 2
		_, err := service.Ask(context.Background(), Request{TargetSessionID: "target", Question: "What happened?"})
		assertCode(t, err, "conversation_too_large")
	})
	t.Run("model failure", func(t *testing.T) {
		path := writeTranscript(t, `{"type":"event_msg","payload":{"type":"user_message","message":"hello"}}`)
		model := &fakeModel{errs: []error{errors.New("timeout")}}
		_, err := testService(path, model).Ask(context.Background(), Request{TargetSessionID: "target", Question: "What happened?"})
		assertCode(t, err, "model_unavailable")
	})
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var sessionErr *Error
	if !errors.As(err, &sessionErr) || sessionErr.Code != code {
		t.Fatalf("err=%v, want code %s", err, code)
	}
}
