package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDaemon_ReviewLoopRunsToCompletionWithSDKExecutor(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19947")

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	var mu sync.Mutex
	callCount := 0
	d.reviewLoopExec = func(_ context.Context, _ *protocol.ReviewLoopRun, _ string) (*reviewLoopOutcome, string, string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		switch callCount {
		case 1:
			return &reviewLoopOutcome{
				LoopDecision:       protocol.ReviewLoopDecisionContinue,
				Summary:            "First pass found a small issue",
				ChangesMade:        true,
				FilesTouched:       []string{"main.go"},
				QuestionsForUser:   nil,
				BlockingReason:     "",
				SuggestedNextFocus: "Run one more pass",
			}, "assistant trace 1", `{"loop_decision":"continue"}`, "continue", nil
		default:
			return &reviewLoopOutcome{
				LoopDecision:       protocol.ReviewLoopDecisionConverged,
				Summary:            "Second pass converged",
				ChangesMade:        false,
				FilesTouched:       nil,
				QuestionsForUser:   nil,
				BlockingReason:     "",
				SuggestedNextFocus: "",
			}, "assistant trace 2", `{"loop_decision":"converged"}`, "converged", nil
		}
	}

	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("loop-sess", "Loop", "/tmp/loop"); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	run, err := c.StartReviewLoop("loop-sess", "full-review", "Review this repo", 2)
	if err != nil {
		t.Fatalf("StartReviewLoop error: %v", err)
	}
	if run == nil {
		t.Fatal("StartReviewLoop returned nil run")
	}

	final := waitForReviewLoopRunStatus(t, c, "loop-sess", protocol.ReviewLoopRunStatusCompleted, 5*time.Second)
	if final.IterationCount != 2 {
		t.Fatalf("final iteration_count = %d, want 2", final.IterationCount)
	}
	if protocol.Deref(final.LastDecision) != protocol.ReviewLoopDecisionConverged {
		t.Fatalf("last decision = %q, want converged", protocol.Deref(final.LastDecision))
	}
}

func TestDaemon_ReviewLoopAwaitingUserAnswerResumesSameLoop(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19948")

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	var mu sync.Mutex
	callCount := 0
	d.reviewLoopExec = func(_ context.Context, _ *protocol.ReviewLoopRun, _ string) (*reviewLoopOutcome, string, string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		switch callCount {
		case 1:
			return &reviewLoopOutcome{
				LoopDecision:       protocol.ReviewLoopDecisionNeedsUserInput,
				Summary:            "Need clarification",
				ChangesMade:        false,
				FilesTouched:       nil,
				QuestionsForUser:   []string{"Should retry exhaustion be surfaced in the UI?"},
				BlockingReason:     "Retry UX is ambiguous",
				SuggestedNextFocus: "",
			}, "assistant trace 1", `{"loop_decision":"needs_user_input"}`, "needs_user_input", nil
		default:
			return &reviewLoopOutcome{
				LoopDecision:       protocol.ReviewLoopDecisionConverged,
				Summary:            "Applied the clarification",
				ChangesMade:        true,
				FilesTouched:       []string{"internal/daemon/websocket.go"},
				QuestionsForUser:   nil,
				BlockingReason:     "",
				SuggestedNextFocus: "",
			}, "assistant trace 2", `{"loop_decision":"converged"}`, "converged", nil
		}
	}

	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("loop-sess", "Loop", "/tmp/loop"); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	initial, err := c.StartReviewLoop("loop-sess", "", "Review this repo", 2)
	if err != nil {
		t.Fatalf("StartReviewLoop error: %v", err)
	}
	if initial == nil {
		t.Fatal("StartReviewLoop returned nil run")
	}

	awaiting := waitForReviewLoopRunStatus(t, c, "loop-sess", protocol.ReviewLoopRunStatusAwaitingUser, 5*time.Second)
	if awaiting.PendingInteraction == nil {
		t.Fatal("pending interaction = nil, want question")
	}
	if awaiting.PendingInteraction.Question == "" {
		t.Fatal("pending interaction question empty")
	}
	loopID := awaiting.LoopID
	interactionID := awaiting.PendingInteraction.ID

	resumed, err := c.AnswerReviewLoop(loopID, interactionID, "Yes, surface it in the UI.")
	if err != nil {
		t.Fatalf("AnswerReviewLoop error: %v", err)
	}
	if resumed == nil {
		t.Fatal("AnswerReviewLoop returned nil run")
	}
	if resumed.LoopID != loopID {
		t.Fatalf("resumed loop id = %q, want %q", resumed.LoopID, loopID)
	}

	final := waitForReviewLoopRunStatus(t, c, "loop-sess", protocol.ReviewLoopRunStatusCompleted, 5*time.Second)
	if final.LoopID != loopID {
		t.Fatalf("final loop id = %q, want %q", final.LoopID, loopID)
	}
	if final.IterationCount != 2 {
		t.Fatalf("final iteration_count = %d, want 2", final.IterationCount)
	}
}

func TestDaemon_GetReviewLoopRunByLoopID(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19950")

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	d.reviewLoopExec = func(_ context.Context, _ *protocol.ReviewLoopRun, _ string) (*reviewLoopOutcome, string, string, string, error) {
		return &reviewLoopOutcome{
			LoopDecision:       protocol.ReviewLoopDecisionConverged,
			Summary:            "Converged immediately",
			ChangesMade:        false,
			FilesTouched:       nil,
			QuestionsForUser:   nil,
			BlockingReason:     "",
			SuggestedNextFocus: "",
		}, "assistant trace", `{"loop_decision":"converged"}`, "converged", nil
	}

	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("loop-sess", "Loop", "/tmp/loop"); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	run, err := c.StartReviewLoop("loop-sess", "", "Review this repo", 1)
	if err != nil {
		t.Fatalf("StartReviewLoop error: %v", err)
	}
	if run == nil {
		t.Fatal("StartReviewLoop returned nil run")
	}

	final := waitForReviewLoopRunStatus(t, c, "loop-sess", protocol.ReviewLoopRunStatusCompleted, 5*time.Second)
	byLoopID, err := c.GetReviewLoopRun(final.LoopID)
	if err != nil {
		t.Fatalf("GetReviewLoopRun(%s) error: %v", final.LoopID, err)
	}
	if byLoopID == nil {
		t.Fatal("GetReviewLoopRun returned nil run")
	}
	if byLoopID.LoopID != final.LoopID {
		t.Fatalf("GetReviewLoopRun loop id = %q, want %q", byLoopID.LoopID, final.LoopID)
	}
	if byLoopID.LatestIteration == nil {
		t.Fatal("GetReviewLoopRun latest_iteration = nil, want latest iteration")
	}
	if protocol.Deref(byLoopID.LatestIteration.ResultText) != "converged" {
		t.Fatalf("latest iteration result_text = %q, want converged", protocol.Deref(byLoopID.LatestIteration.ResultText))
	}
}

func TestDaemon_ReviewLoopConvergedBeforeLimitStillRunsNextPass(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19951")

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	var mu sync.Mutex
	callCount := 0
	d.reviewLoopExec = func(_ context.Context, _ *protocol.ReviewLoopRun, _ string) (*reviewLoopOutcome, string, string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		switch callCount {
		case 1:
			return &reviewLoopOutcome{
				LoopDecision:       protocol.ReviewLoopDecisionConverged,
				Summary:            "First pass looks clean.",
				ChangesMade:        false,
				FilesTouched:       nil,
				QuestionsForUser:   nil,
				BlockingReason:     "",
				SuggestedNextFocus: "",
			}, "assistant trace 1", `{"loop_decision":"converged"}`, "converged", nil
		default:
			return &reviewLoopOutcome{
				LoopDecision:       protocol.ReviewLoopDecisionConverged,
				Summary:            "Second fresh pass also converged.",
				ChangesMade:        false,
				FilesTouched:       nil,
				QuestionsForUser:   nil,
				BlockingReason:     "",
				SuggestedNextFocus: "",
			}, "assistant trace 2", `{"loop_decision":"converged"}`, "converged", nil
		}
	}

	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("loop-sess", "Loop", "/tmp/loop"); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	run, err := c.StartReviewLoop("loop-sess", "", "Review this repo", 2)
	if err != nil {
		t.Fatalf("StartReviewLoop error: %v", err)
	}
	if run == nil {
		t.Fatal("StartReviewLoop returned nil run")
	}

	final := waitForReviewLoopRunStatus(t, c, "loop-sess", protocol.ReviewLoopRunStatusCompleted, 5*time.Second)
	if final.IterationCount != 2 {
		t.Fatalf("final iteration_count = %d, want 2", final.IterationCount)
	}
	if protocol.Deref(final.StopReason) != reviewLoopStopReasonIterationLimitReached {
		t.Fatalf("final stop_reason = %q, want %q", protocol.Deref(final.StopReason), reviewLoopStopReasonIterationLimitReached)
	}
}

func waitForReviewLoopRunStatus(t *testing.T, c *client.Client, sessionID string, want protocol.ReviewLoopRunStatus, timeout time.Duration) *protocol.ReviewLoopRun {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := c.GetReviewLoopState(sessionID)
		if err != nil {
			t.Fatalf("GetReviewLoopState(%s) error: %v", sessionID, err)
		}
		if run != nil && run.Status == want {
			return run
		}
		time.Sleep(25 * time.Millisecond)
	}

	run, err := c.GetReviewLoopState(sessionID)
	if err != nil {
		t.Fatalf("GetReviewLoopState(final %s) error: %v", sessionID, err)
	}
	if run == nil {
		t.Fatalf("review loop for %s missing while waiting for %q", sessionID, want)
	}
	t.Fatalf("review loop status = %q, want %q", run.Status, want)
	return nil
}
