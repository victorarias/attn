package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestRealClaudeReviewLoop_ProducesStructuredOutcome(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ATTN_RUN_REAL_CLAUDE_REVIEW_LOOP")) != "1" {
		t.Skip("set ATTN_RUN_REAL_CLAUDE_REVIEW_LOOP=1 to run real Claude review-loop tests")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not found on PATH")
	}

	repoPath := createTinySDKReviewLoopRepo(t)
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate ws port: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", strconvString(port))

	sockPath := filepath.Join(reviewLoopHarnessTempDir(t), "real-claude-loop.sock")
	d := NewForTesting(sockPath)
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("daemon exited: %v", err)
		}
	}()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("real-claude-loop", "Real Claude Loop", repoPath); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	run, err := c.StartReviewLoop("real-claude-loop", "real-claude", "Review this tiny repository. Keep changes minimal and safe. Use the required structured output.", 1)
	if err != nil {
		t.Fatalf("StartReviewLoop error: %v", err)
	}
	if run == nil {
		t.Fatal("StartReviewLoop returned nil run")
	}

	final := waitForReviewLoopRunTerminalState(t, c, "real-claude-loop", 4*time.Minute)
	if final.IterationCount != 1 {
		t.Fatalf("final iteration_count = %d, want 1", final.IterationCount)
	}
	if final.LastDecision == nil {
		t.Fatalf("final last_decision = nil, want structured outcome")
	}
	if protocol.Deref(final.LastResultSummary) == "" {
		t.Fatalf("final last_result_summary empty, want structured summary")
	}

	iterations, err := d.store.ListReviewLoopIterations(final.LoopID)
	if err != nil {
		t.Fatalf("ListReviewLoopIterations(%s) error: %v", final.LoopID, err)
	}
	if len(iterations) != 1 {
		t.Fatalf("iteration count in store = %d, want 1", len(iterations))
	}
	if protocol.Deref(iterations[0].StructuredOutputJson) == "" {
		t.Fatalf("structured_output_json empty, want persisted structured result")
	}
}

func TestRealClaudeReviewLoop_AwaitUserThenResume(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ATTN_RUN_REAL_CLAUDE_REVIEW_LOOP")) != "1" {
		t.Skip("set ATTN_RUN_REAL_CLAUDE_REVIEW_LOOP=1 to run real Claude review-loop tests")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not found on PATH")
	}

	repoPath := createTinySDKReviewLoopRepo(t)
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate ws port: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", strconvString(port))

	sockPath := filepath.Join(reviewLoopHarnessTempDir(t), "real-claude-loop-await.sock")
	d := NewForTesting(sockPath)
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("daemon exited: %v", err)
		}
	}()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("real-claude-await", "Real Claude Await", repoPath); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	prompt := "Before doing any code changes, ask exactly one concrete clarification question using loop_decision needs_user_input. After you receive the answer, continue and converge in the next iteration."
	run, err := c.StartReviewLoop("real-claude-await", "real-claude-await", prompt, 2)
	if err != nil {
		t.Fatalf("StartReviewLoop error: %v", err)
	}
	if run == nil {
		t.Fatal("StartReviewLoop returned nil run")
	}

	awaiting := waitForReviewLoopRunStatus(t, c, "real-claude-await", protocol.ReviewLoopRunStatusAwaitingUser, 4*time.Minute)
	if awaiting.PendingInteraction == nil {
		t.Fatal("awaiting_user run missing pending interaction")
	}
	loopID := awaiting.LoopID

	resumed, err := c.AnswerReviewLoop(loopID, awaiting.PendingInteraction.ID, "Prefer the simpler user-facing behavior.")
	if err != nil {
		t.Fatalf("AnswerReviewLoop error: %v", err)
	}
	if resumed == nil {
		t.Fatal("AnswerReviewLoop returned nil run")
	}
	if resumed.LoopID != loopID {
		t.Fatalf("resumed loop id = %q, want %q", resumed.LoopID, loopID)
	}

	final := waitForReviewLoopRunTerminalState(t, c, "real-claude-await", 4*time.Minute)
	if final.LoopID != loopID {
		t.Fatalf("final loop id = %q, want %q", final.LoopID, loopID)
	}
	if final.IterationCount < 2 {
		t.Fatalf("final iteration_count = %d, want at least 2 after resume", final.IterationCount)
	}
}

func waitForReviewLoopRunTerminalState(t *testing.T, c *client.Client, sessionID string, timeout time.Duration) *protocol.ReviewLoopRun {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := c.GetReviewLoopState(sessionID)
		if err != nil {
			t.Fatalf("GetReviewLoopState(%s) error: %v", sessionID, err)
		}
		if run != nil {
			switch run.Status {
			case protocol.ReviewLoopRunStatusCompleted, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopRunStatusStopped:
				return run
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	run, err := c.GetReviewLoopState(sessionID)
	if err != nil {
		t.Fatalf("GetReviewLoopState(final %s) error: %v", sessionID, err)
	}
	if run == nil {
		t.Fatalf("review loop for %s missing while waiting for terminal state", sessionID)
	}
	t.Fatalf("review loop status = %q, want completed/error/stopped", run.Status)
	return nil
}

func createTinySDKReviewLoopRepo(t *testing.T) string {
	t.Helper()
	repoPath := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", strings.Join(args, " "), err, string(output))
		}
	}

	run("git", "init")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test User")

	initial := "package main\n\nimport \"fmt\"\n\nfunc greet(name string) string {\n\treturn fmt.Sprintf(\"hello %s\", name)\n}\n"
	if err := os.WriteFile(filepath.Join(repoPath, "main.go"), []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "initial")

	modified := "package main\n\nimport \"fmt\"\n\nfunc greet(name string) string {\n\tif name == \"\" {\n\t\treturn \"hello \"\n\t}\n\treturn fmt.Sprintf(\"hello %s\", name)\n}\n"
	if err := os.WriteFile(filepath.Join(repoPath, "main.go"), []byte(modified), 0o644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	return repoPath
}

func strconvString(v int) string {
	return strings.TrimSpace(fmt.Sprintf("%d", v))
}
