package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// ReviewLoopHarnessScenario defines a scripted daemon-level review loop scenario.
type ReviewLoopHarnessScenario struct {
	Name           string
	SessionID      string
	RepoPath       string
	Prompt         string
	IterationLimit int
	Iterations     []ReviewLoopHarnessIteration
}

// ReviewLoopHarnessIteration defines one scripted executor response.
type ReviewLoopHarnessIteration struct {
	Delay          time.Duration
	Outcome        reviewLoopOutcome
	AssistantTrace string
	StructuredJSON string
	ExecutionError error
	ExpectedAnswer string
}

// ReviewLoopTimelineEvent is a compact timeline record for harness runs.
type ReviewLoopTimelineEvent struct {
	At        string `json:"at"`
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	LoopID    string `json:"loop_id,omitempty"`
	Status    string `json:"status,omitempty"`
	Iteration int    `json:"iteration,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ReviewLoopHarness drives the daemon through a scripted review-loop scenario.
type ReviewLoopHarness struct {
	t        *testing.T
	scenario ReviewLoopHarnessScenario
	daemon   *Daemon
	recorder *BroadcastRecorder
	client   *client.Client
	sockPath string

	mu                 sync.Mutex
	executorCallIndex  int
	seenBroadcastCount int
	timeline           []ReviewLoopTimelineEvent
}

// NewReviewLoopHarness creates and starts a daemon-backed review loop harness.
func NewReviewLoopHarness(t *testing.T, scenario ReviewLoopHarnessScenario) *ReviewLoopHarness {
	t.Helper()

	if strings.TrimSpace(scenario.SessionID) == "" {
		scenario.SessionID = "loop-harness-session"
	}
	if strings.TrimSpace(scenario.RepoPath) == "" {
		scenario.RepoPath = "/tmp/review-loop-harness"
	}
	if strings.TrimSpace(scenario.Prompt) == "" {
		scenario.Prompt = "Review this repository."
	}
	if scenario.IterationLimit <= 0 {
		scenario.IterationLimit = len(scenario.Iterations)
		if scenario.IterationLimit <= 0 {
			scenario.IterationLimit = 1
		}
	}

	port, err := freeHarnessTCPPort()
	if err != nil {
		t.Fatalf("allocate harness ws port: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", port)
	sockPath := filepath.Join(reviewLoopHarnessTempDir(t), "review-loop-harness.sock")
	daemon := NewForTesting(sockPath)
	recorder := NewBroadcastRecorder()

	h := &ReviewLoopHarness{
		t:        t,
		scenario: scenario,
		daemon:   daemon,
		recorder: recorder,
		sockPath: sockPath,
	}
	daemon.reviewLoopExec = h.executor
	daemon.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		recorder.Record(event)
	}

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- daemon.Start()
	}()
	deadline := time.Now().Add(5 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sockPath, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			started = true
			break
		}
		select {
		case err := <-startErrCh:
			t.Fatalf("review loop harness daemon failed to start: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !started {
		select {
		case err := <-startErrCh:
			t.Fatalf("review loop harness daemon failed to start: %v", err)
		default:
		}
		t.Fatalf("socket %s not ready after 5s", sockPath)
	}

	h.client = client.New(sockPath)
	if err := h.client.Register(scenario.SessionID, "Review Loop Harness", scenario.RepoPath); err != nil {
		daemon.Stop()
		t.Fatalf("Register(%s) error: %v", scenario.SessionID, err)
	}
	h.record("session_registered", scenario.SessionID, "", "", 0, scenario.RepoPath)
	return h
}

// Close stops the harness daemon.
func (h *ReviewLoopHarness) Close() {
	if h == nil || h.daemon == nil {
		return
	}
	h.daemon.Stop()
}

// StartLoop triggers the review loop for the harness session.
func (h *ReviewLoopHarness) StartLoop() *protocol.ReviewLoopRun {
	h.t.Helper()
	run, err := h.client.StartReviewLoop(h.scenario.SessionID, "harness", h.scenario.Prompt, h.scenario.IterationLimit)
	if err != nil {
		h.t.Fatalf("StartReviewLoop(%s) error: %v", h.scenario.SessionID, err)
	}
	if run == nil {
		h.t.Fatalf("StartReviewLoop(%s) returned nil run", h.scenario.SessionID)
	}
	h.record("loop_started", h.scenario.SessionID, run.LoopID, string(run.Status), run.IterationCount, h.scenario.Prompt)
	h.captureBroadcasts()
	return run
}

// WaitForStatus waits for the current session loop to reach the desired run status.
func (h *ReviewLoopHarness) WaitForStatus(want protocol.ReviewLoopRunStatus, timeout time.Duration) *protocol.ReviewLoopRun {
	h.t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.captureBroadcasts()
		run, err := h.client.GetReviewLoopState(h.scenario.SessionID)
		if err != nil {
			h.t.Fatalf("GetReviewLoopState(%s) error: %v", h.scenario.SessionID, err)
		}
		if run != nil && run.Status == want {
			h.record("status_reached", run.SourceSessionID, run.LoopID, string(run.Status), run.IterationCount, "")
			h.captureStoreState(run)
			return run
		}
		time.Sleep(25 * time.Millisecond)
	}

	run, err := h.client.GetReviewLoopState(h.scenario.SessionID)
	if err != nil {
		h.t.Fatalf("GetReviewLoopState(final %s) error: %v", h.scenario.SessionID, err)
	}
	if run == nil {
		h.t.Fatalf("review loop for %s missing while waiting for %q", h.scenario.SessionID, want)
	}
	h.t.Fatalf("review loop status = %q, want %q", run.Status, want)
	return nil
}

// AnswerPending submits an answer for the current pending interaction.
func (h *ReviewLoopHarness) AnswerPending(answer string) *protocol.ReviewLoopRun {
	h.t.Helper()

	run := h.WaitForStatus(protocol.ReviewLoopRunStatusAwaitingUser, 5*time.Second)
	if run.PendingInteraction == nil {
		h.t.Fatal("pending interaction = nil, want pending question")
	}
	updated, err := h.client.AnswerReviewLoop(run.LoopID, run.PendingInteraction.ID, answer)
	if err != nil {
		h.t.Fatalf("AnswerReviewLoop(%s) error: %v", run.LoopID, err)
	}
	if updated == nil {
		h.t.Fatalf("AnswerReviewLoop(%s) returned nil run", run.LoopID)
	}
	h.record("answer_submitted", updated.SourceSessionID, updated.LoopID, string(updated.Status), updated.IterationCount, answer)
	h.captureBroadcasts()
	return updated
}

// Timeline returns a copy of the harness timeline.
func (h *ReviewLoopHarness) Timeline() []ReviewLoopTimelineEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ReviewLoopTimelineEvent, len(h.timeline))
	copy(out, h.timeline)
	return out
}

// TimelineJSON returns the timeline as pretty JSON for debugging artifacts.
func (h *ReviewLoopHarness) TimelineJSON() string {
	data, err := json.MarshalIndent(h.Timeline(), "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(data)
}

func (h *ReviewLoopHarness) executor(_ context.Context, run *protocol.ReviewLoopRun, prompt string) (*reviewLoopOutcome, string, string, string, error) {
	h.mu.Lock()
	index := h.executorCallIndex
	h.executorCallIndex++
	h.mu.Unlock()

	if index >= len(h.scenario.Iterations) {
		return nil, "", "", "", fmt.Errorf("review loop harness %q exhausted scripted iterations at call %d", h.scenario.Name, index+1)
	}

	step := h.scenario.Iterations[index]
	h.record("executor_started", run.SourceSessionID, run.LoopID, string(run.Status), run.IterationCount+1, prompt)

	if step.ExpectedAnswer != "" {
		interaction, err := h.latestAnsweredInteraction(run.LoopID)
		if err != nil {
			return nil, "", "", "", err
		}
		if interaction == nil {
			return nil, "", "", "", fmt.Errorf("expected answered interaction %q but none found", step.ExpectedAnswer)
		}
		if got := strings.TrimSpace(protocol.Deref(interaction.Answer)); got != step.ExpectedAnswer {
			return nil, "", "", "", fmt.Errorf("expected answer %q, got %q", step.ExpectedAnswer, got)
		}
	}

	if step.Delay > 0 {
		time.Sleep(step.Delay)
	}
	if step.ExecutionError != nil {
		h.record("executor_error", run.SourceSessionID, run.LoopID, "", run.IterationCount+1, step.ExecutionError.Error())
		return nil, "", "", "", step.ExecutionError
	}

	structured := strings.TrimSpace(step.StructuredJSON)
	if structured == "" {
		data, err := json.Marshal(step.Outcome)
		if err != nil {
			return nil, "", "", "", err
		}
		structured = string(data)
	}
	h.record("executor_completed", run.SourceSessionID, run.LoopID, string(step.Outcome.LoopDecision), run.IterationCount+1, step.Outcome.Summary)
	return &step.Outcome, step.AssistantTrace, structured, string(step.Outcome.LoopDecision), nil
}

func (h *ReviewLoopHarness) latestAnsweredInteraction(loopID string) (*protocol.ReviewLoopInteraction, error) {
	interactions, err := h.daemon.store.ListReviewLoopInteractions(loopID)
	if err != nil {
		return nil, err
	}
	for i := len(interactions) - 1; i >= 0; i-- {
		interaction := interactions[i]
		if interaction != nil && interaction.Status == protocol.ReviewLoopInteractionStatusAnswered {
			return interaction, nil
		}
	}
	return nil, nil
}

func (h *ReviewLoopHarness) captureBroadcasts() {
	events := h.recorder.Events()

	h.mu.Lock()
	start := h.seenBroadcastCount
	h.seenBroadcastCount = len(events)
	h.mu.Unlock()

	for _, event := range events[start:] {
		if event == nil {
			continue
		}
		switch event.Event {
		case protocol.EventReviewLoopUpdated:
			run := event.ReviewLoopRun
			sessionID := strings.TrimSpace(protocol.Deref(event.SessionID))
			loopID := ""
			status := ""
			iteration := 0
			if run != nil {
				loopID = run.LoopID
				status = string(run.Status)
				iteration = run.IterationCount
				if sessionID == "" {
					sessionID = run.SourceSessionID
				}
			}
			h.record("broadcast_review_loop_updated", sessionID, loopID, status, iteration, "")
		case protocol.EventSessionStateChanged:
			if event.Session != nil {
				h.record("broadcast_session_state_changed", event.Session.ID, "", string(event.Session.State), 0, "")
			}
		}
	}
}

func (h *ReviewLoopHarness) captureStoreState(run *protocol.ReviewLoopRun) {
	if run == nil {
		return
	}
	iterations, err := h.daemon.store.ListReviewLoopIterations(run.LoopID)
	if err != nil {
		h.record("store_snapshot_error", run.SourceSessionID, run.LoopID, "", 0, err.Error())
		return
	}
	for _, iteration := range iterations {
		if iteration == nil {
			continue
		}
		h.record("store_iteration_snapshot", run.SourceSessionID, run.LoopID, string(iteration.Status), iteration.IterationNumber, protocol.Deref(iteration.Summary))
	}
}

func (h *ReviewLoopHarness) record(eventType, sessionID, loopID, status string, iteration int, detail string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timeline = append(h.timeline, ReviewLoopTimelineEvent{
		At:        string(protocol.TimestampNow()),
		Type:      eventType,
		SessionID: sessionID,
		LoopID:    loopID,
		Status:    status,
		Iteration: iteration,
		Detail:    detail,
	})
}

func freeHarnessTCPPort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("unexpected listener addr %T", listener.Addr())
	}
	return fmt.Sprintf("%d", addr.Port), nil
}

func reviewLoopHarnessTempDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "attn-loop-h-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
