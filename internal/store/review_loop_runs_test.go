package store

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestReviewLoopRunHierarchyCRUD(t *testing.T) {
	s := New()
	insertTestSession(t, s, "sess-loop-1")

	run := &protocol.ReviewLoopRun{
		LoopID:             "loop-1",
		SourceSessionID:    "sess-loop-1",
		RepoPath:           "/tmp/repo",
		Status:             protocol.ReviewLoopRunStatusRunning,
		PresetID:           protocol.Ptr("full-review-fix"),
		CustomPrompt:       protocol.Ptr("Review for correctness"),
		ResolvedPrompt:     "Review for correctness with structured output",
		HandoffPayloadJson: protocol.Ptr(`{"summary":"initial handoff"}`),
		IterationCount:     1,
		IterationLimit:     3,
		LastDecision:       protocol.Ptr(protocol.ReviewLoopDecisionContinue),
		LastResultSummary:  protocol.Ptr("Continuing after first pass"),
		CreatedAt:          "2026-03-06T10:00:00Z",
		UpdatedAt:          "2026-03-06T10:01:00Z",
	}
	if err := s.UpsertReviewLoopRun(run); err != nil {
		t.Fatalf("UpsertReviewLoopRun() error = %v", err)
	}

	gotRun, err := s.GetReviewLoopRun("loop-1")
	if err != nil {
		t.Fatalf("GetReviewLoopRun() error = %v", err)
	}
	if gotRun == nil {
		t.Fatal("GetReviewLoopRun() = nil, want run")
	}
	if gotRun.Status != protocol.ReviewLoopRunStatusRunning {
		t.Fatalf("Status = %q, want %q", gotRun.Status, protocol.ReviewLoopRunStatusRunning)
	}
	if protocol.Deref(gotRun.LastResultSummary) != "Continuing after first pass" {
		t.Fatalf("LastResultSummary = %q, want continuing summary", protocol.Deref(gotRun.LastResultSummary))
	}

	iteration := &protocol.ReviewLoopIteration{
		ID:                   "iter-1",
		LoopID:               "loop-1",
		IterationNumber:      1,
		Status:               protocol.ReviewLoopIterationStatusAwaitingUser,
		Decision:             protocol.Ptr(protocol.ReviewLoopDecisionNeedsUserInput),
		Summary:              protocol.Ptr("Need one clarification"),
		ChangesMade:          protocol.Ptr(false),
		FilesTouched:         []string{"internal/daemon/websocket.go"},
		BlockingReason:       protocol.Ptr("Retry behavior is not specified"),
		SuggestedNextFocus:   protocol.Ptr("Clarify retry UX"),
		StructuredOutputJson: protocol.Ptr(`{"loop_decision":"needs_user_input"}`),
		StartedAt:            "2026-03-06T10:00:30Z",
		CompletedAt:          protocol.Ptr("2026-03-06T10:01:00Z"),
	}
	if err := s.UpsertReviewLoopIteration(iteration); err != nil {
		t.Fatalf("UpsertReviewLoopIteration() error = %v", err)
	}

	gotIteration, err := s.GetReviewLoopIteration("iter-1")
	if err != nil {
		t.Fatalf("GetReviewLoopIteration() error = %v", err)
	}
	if gotIteration == nil {
		t.Fatal("GetReviewLoopIteration() = nil, want iteration")
	}
	if gotIteration.Status != protocol.ReviewLoopIterationStatusAwaitingUser {
		t.Fatalf("Iteration Status = %q, want %q", gotIteration.Status, protocol.ReviewLoopIterationStatusAwaitingUser)
	}
	if len(gotIteration.FilesTouched) != 1 || gotIteration.FilesTouched[0] != "internal/daemon/websocket.go" {
		t.Fatalf("FilesTouched = %#v, want single touched file", gotIteration.FilesTouched)
	}

	interaction := &protocol.ReviewLoopInteraction{
		ID:          "interaction-1",
		LoopID:      "loop-1",
		IterationID: protocol.Ptr("iter-1"),
		Kind:        "question_answer",
		Question:    "Should retry exhaustion surface in the UI?",
		Status:      protocol.ReviewLoopInteractionStatusPending,
		CreatedAt:   "2026-03-06T10:01:00Z",
	}
	if err := s.UpsertReviewLoopInteraction(interaction); err != nil {
		t.Fatalf("UpsertReviewLoopInteraction() error = %v", err)
	}

	run.PendingInteractionID = protocol.Ptr("interaction-1")
	run.Status = protocol.ReviewLoopRunStatusAwaitingUser
	run.LastDecision = protocol.Ptr(protocol.ReviewLoopDecisionNeedsUserInput)
	run.UpdatedAt = "2026-03-06T10:01:00Z"
	if err := s.UpsertReviewLoopRun(run); err != nil {
		t.Fatalf("UpsertReviewLoopRun(awaiting_user) error = %v", err)
	}

	gotActive, err := s.GetActiveReviewLoopRunForSession("sess-loop-1")
	if err != nil {
		t.Fatalf("GetActiveReviewLoopRunForSession() error = %v", err)
	}
	if gotActive == nil {
		t.Fatal("GetActiveReviewLoopRunForSession() = nil, want active run")
	}
	if gotActive.LoopID != "loop-1" {
		t.Fatalf("active loop id = %q, want loop-1", gotActive.LoopID)
	}
	if gotActive.Status != protocol.ReviewLoopRunStatusAwaitingUser {
		t.Fatalf("active status = %q, want awaiting_user", gotActive.Status)
	}

	gotInteraction, err := s.GetReviewLoopInteraction("interaction-1")
	if err != nil {
		t.Fatalf("GetReviewLoopInteraction() error = %v", err)
	}
	if gotInteraction == nil {
		t.Fatal("GetReviewLoopInteraction() = nil, want interaction")
	}
	if gotInteraction.Question != "Should retry exhaustion surface in the UI?" {
		t.Fatalf("Question = %q, want persisted question", gotInteraction.Question)
	}

	iterations, err := s.ListReviewLoopIterations("loop-1")
	if err != nil {
		t.Fatalf("ListReviewLoopIterations() error = %v", err)
	}
	if len(iterations) != 1 {
		t.Fatalf("ListReviewLoopIterations() len = %d, want 1", len(iterations))
	}

	interactions, err := s.ListReviewLoopInteractions("loop-1")
	if err != nil {
		t.Fatalf("ListReviewLoopInteractions() error = %v", err)
	}
	if len(interactions) != 1 {
		t.Fatalf("ListReviewLoopInteractions() len = %d, want 1", len(interactions))
	}

	if err := s.DeleteReviewLoopRun("loop-1"); err != nil {
		t.Fatalf("DeleteReviewLoopRun() error = %v", err)
	}
	gotRun, err = s.GetReviewLoopRun("loop-1")
	if err != nil {
		t.Fatalf("GetReviewLoopRun(after delete) error = %v", err)
	}
	if gotRun != nil {
		t.Fatalf("GetReviewLoopRun(after delete) = %#v, want nil", gotRun)
	}
	gotIteration, err = s.GetReviewLoopIteration("iter-1")
	if err != nil {
		t.Fatalf("GetReviewLoopIteration(after delete) error = %v", err)
	}
	if gotIteration != nil {
		t.Fatalf("GetReviewLoopIteration(after delete) = %#v, want nil", gotIteration)
	}
	gotInteraction, err = s.GetReviewLoopInteraction("interaction-1")
	if err != nil {
		t.Fatalf("GetReviewLoopInteraction(after delete) error = %v", err)
	}
	if gotInteraction != nil {
		t.Fatalf("GetReviewLoopInteraction(after delete) = %#v, want nil", gotInteraction)
	}
}

func TestGetActiveReviewLoopRunForSession_IgnoresInactiveRuns(t *testing.T) {
	s := New()
	insertTestSession(t, s, "sess-loop-2")

	completed := &protocol.ReviewLoopRun{
		LoopID:          "loop-completed",
		SourceSessionID: "sess-loop-2",
		RepoPath:        "/tmp/repo",
		Status:          protocol.ReviewLoopRunStatusCompleted,
		ResolvedPrompt:  "done",
		IterationCount:  2,
		IterationLimit:  2,
		CreatedAt:       "2026-03-06T09:00:00Z",
		UpdatedAt:       "2026-03-06T09:10:00Z",
		CompletedAt:     protocol.Ptr("2026-03-06T09:10:00Z"),
	}
	if err := s.UpsertReviewLoopRun(completed); err != nil {
		t.Fatalf("UpsertReviewLoopRun(completed) error = %v", err)
	}

	running := &protocol.ReviewLoopRun{
		LoopID:          "loop-running",
		SourceSessionID: "sess-loop-2",
		RepoPath:        "/tmp/repo",
		Status:          protocol.ReviewLoopRunStatusRunning,
		ResolvedPrompt:  "running",
		IterationCount:  1,
		IterationLimit:  3,
		CreatedAt:       "2026-03-06T10:00:00Z",
		UpdatedAt:       "2026-03-06T10:00:30Z",
	}
	if err := s.UpsertReviewLoopRun(running); err != nil {
		t.Fatalf("UpsertReviewLoopRun(running) error = %v", err)
	}

	got, err := s.GetActiveReviewLoopRunForSession("sess-loop-2")
	if err != nil {
		t.Fatalf("GetActiveReviewLoopRunForSession() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetActiveReviewLoopRunForSession() = nil, want active run")
	}
	if got.LoopID != "loop-running" {
		t.Fatalf("active loop id = %q, want loop-running", got.LoopID)
	}
}

func insertTestSession(t *testing.T, s *Store, sessionID string) {
	t.Helper()
	s.Add(&protocol.Session{
		ID:             sessionID,
		Label:          "loop-test",
		Agent:          protocol.SessionAgentClaude,
		Directory:      "/tmp/repo",
		State:          protocol.StateIdle,
		StateSince:     "2026-03-06T09:00:00Z",
		StateUpdatedAt: "2026-03-06T09:00:00Z",
		LastSeen:       "2026-03-06T09:00:00Z",
		Muted:          false,
	})
	if got := s.Get(sessionID); got == nil {
		t.Fatalf("insert test session %q did not persist", sessionID)
	}
}
