package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestReviewLoopHarness_ContinueThenConverge(t *testing.T) {
	h := NewReviewLoopHarness(t, ReviewLoopHarnessScenario{
		Name:           "continue-then-converge",
		SessionID:      "harness-loop-1",
		RepoPath:       "/tmp/harness-loop-1",
		Prompt:         "Review this repository.",
		IterationLimit: 2,
		Iterations: []ReviewLoopHarnessIteration{
			{
				Outcome: reviewLoopOutcome{
					LoopDecision:       protocol.ReviewLoopDecisionContinue,
					Summary:            "First pass found one safe fix.",
					ChangesMade:        true,
					FilesTouched:       []string{"main.go"},
					QuestionsForUser:   nil,
					BlockingReason:     "",
					SuggestedNextFocus: "Run another pass.",
				},
				AssistantTrace: "assistant trace 1",
			},
			{
				Outcome: reviewLoopOutcome{
					LoopDecision:       protocol.ReviewLoopDecisionConverged,
					Summary:            "Second pass converged.",
					ChangesMade:        false,
					FilesTouched:       nil,
					QuestionsForUser:   nil,
					BlockingReason:     "",
					SuggestedNextFocus: "",
				},
				AssistantTrace: "assistant trace 2",
			},
		},
	})
	defer h.Close()

	run := h.StartLoop()
	final := h.WaitForStatus(protocol.ReviewLoopRunStatusCompleted, 5*time.Second)

	if final.LoopID != run.LoopID {
		t.Fatalf("final loop id = %q, want %q", final.LoopID, run.LoopID)
	}
	if final.IterationCount != 2 {
		t.Fatalf("final iteration_count = %d, want 2", final.IterationCount)
	}

	timeline := h.Timeline()
	if len(timeline) == 0 {
		t.Fatal("timeline is empty")
	}
	assertTimelineContains(t, timeline, "executor_started")
	assertTimelineContains(t, timeline, "executor_completed")
	assertTimelineContains(t, timeline, "broadcast_review_loop_updated")
}

func TestReviewLoopHarness_AwaitUserThenResumeSameLoop(t *testing.T) {
	h := NewReviewLoopHarness(t, ReviewLoopHarnessScenario{
		Name:           "await-user-resume",
		SessionID:      "harness-loop-2",
		RepoPath:       "/tmp/harness-loop-2",
		Prompt:         "Review this repository.",
		IterationLimit: 2,
		Iterations: []ReviewLoopHarnessIteration{
			{
				Outcome: reviewLoopOutcome{
					LoopDecision:       protocol.ReviewLoopDecisionNeedsUserInput,
					Summary:            "Need one clarification.",
					ChangesMade:        false,
					FilesTouched:       nil,
					QuestionsForUser:   []string{"Should retry exhaustion be shown in the UI?"},
					BlockingReason:     "Retry UX is ambiguous.",
					SuggestedNextFocus: "",
				},
				AssistantTrace: "assistant trace 1",
			},
			{
				Outcome: reviewLoopOutcome{
					LoopDecision:       protocol.ReviewLoopDecisionConverged,
					Summary:            "Applied the clarification.",
					ChangesMade:        true,
					FilesTouched:       []string{"internal/daemon/websocket.go"},
					QuestionsForUser:   nil,
					BlockingReason:     "",
					SuggestedNextFocus: "",
				},
				AssistantTrace: "assistant trace 2",
				ExpectedAnswer: "Yes, show it in the UI.",
			},
		},
	})
	defer h.Close()

	initial := h.StartLoop()
	awaiting := h.WaitForStatus(protocol.ReviewLoopRunStatusAwaitingUser, 5*time.Second)
	if awaiting.PendingInteraction == nil {
		t.Fatal("awaiting_user run missing pending interaction")
	}

	resumed := h.AnswerPending("Yes, show it in the UI.")
	final := h.WaitForStatus(protocol.ReviewLoopRunStatusCompleted, 5*time.Second)

	if awaiting.LoopID != initial.LoopID || resumed.LoopID != initial.LoopID || final.LoopID != initial.LoopID {
		t.Fatalf("loop id changed across resume: start=%q awaiting=%q resumed=%q final=%q", initial.LoopID, awaiting.LoopID, resumed.LoopID, final.LoopID)
	}

	timeline := h.Timeline()
	assertTimelineContains(t, timeline, "answer_submitted")
	assertTimelineContains(t, timeline, "store_iteration_snapshot")
	if !strings.Contains(h.TimelineJSON(), `"type": "answer_submitted"`) {
		t.Fatalf("timeline json missing answer_submitted event:\n%s", h.TimelineJSON())
	}
}

func assertTimelineContains(t *testing.T, timeline []ReviewLoopTimelineEvent, eventType string) {
	t.Helper()
	for _, event := range timeline {
		if event.Type == eventType {
			return
		}
	}
	t.Fatalf("timeline missing event type %q: %#v", eventType, timeline)
}
