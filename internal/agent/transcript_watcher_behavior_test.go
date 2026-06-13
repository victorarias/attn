package agent

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHasCopilotTranscriptPendingApproval(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"view-fast": {
			name:      "view",
			startedAt: now.Add(-10 * time.Second),
		},
		"bash-stalled": {
			name:      "bash",
			startedAt: now.Add(-(copilotToolStartGraceTime + 10*time.Millisecond)),
		},
	}

	if !hasCopilotTranscriptPendingApproval(pending, now, true) {
		t.Fatal("expected stalled bash tool to trigger pending approval")
	}
}

func TestHasCopilotTranscriptPendingApproval_CreateTool(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"create-stalled": {
			name:      "create",
			startedAt: now.Add(-(copilotToolStartGraceTime + 10*time.Millisecond)),
		},
	}

	if !hasCopilotTranscriptPendingApproval(pending, now, true) {
		t.Fatal("expected stalled create tool to trigger pending approval")
	}
}

func TestHasCopilotTranscriptPendingApproval_GraceWindow(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"bash-recent": {
			name:      "bash",
			startedAt: now.Add(-(copilotToolStartGraceTime - 50*time.Millisecond)),
		},
	}

	if hasCopilotTranscriptPendingApproval(pending, now, true) {
		t.Fatal("recent tool start should not trigger pending approval yet")
	}
}

func TestHasCopilotTranscriptPendingApproval_RequiresTurnOpen(t *testing.T) {
	now := time.Now()
	pending := map[string]copilotPendingTool{
		"bash-stalled": {
			name:      "bash",
			startedAt: now.Add(-(copilotToolStartGraceTime + 100*time.Millisecond)),
		},
	}

	if hasCopilotTranscriptPendingApproval(pending, now, false) {
		t.Fatal("closed turn should not trigger pending approval")
	}
}

func TestShouldPromoteTranscriptPending(t *testing.T) {
	if shouldPromoteTranscriptPending(protocol.SessionStateWorking) {
		t.Fatal("working state should not be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateIdle) {
		t.Fatal("idle state should be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateWaitingInput) {
		t.Fatal("waiting_input state should be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateUnknown) {
		t.Fatal("unknown state should be promoted to pending approval by transcript")
	}
	if !shouldPromoteTranscriptPending(protocol.SessionStateLaunching) {
		t.Fatal("launching state should be promoted to pending approval by transcript")
	}
	if shouldPromoteTranscriptPending(protocol.SessionStatePendingApproval) {
		t.Fatal("pending_approval state should not re-promote")
	}
}

func TestExtractTranscriptEventType(t *testing.T) {
	if got := extractTranscriptEventType([]byte(`{"type":"assistant.turn_start","data":{}}`)); got != "assistant.turn_start" {
		t.Fatalf("extractTranscriptEventType() = %q, want assistant.turn_start", got)
	}
	if got := extractTranscriptEventType([]byte(`not-json`)); got != "" {
		t.Fatalf("extractTranscriptEventType(non-json) = %q, want empty", got)
	}
}

func TestClaudeWatcherBehaviorSkipClassification(t *testing.T) {
	b := &claudeTranscriptWatcherBehavior{}

	recent := time.Now().Add(-10 * time.Second).Format(time.RFC3339Nano)
	stale := time.Now().Add(-3 * time.Minute).Format(time.RFC3339Nano)

	if skip, _ := b.SkipClassification(protocol.SessionStateWorking, recent, time.Now()); !skip {
		t.Fatal("should skip for recently-active working Claude session")
	}
	if skip, _ := b.SkipClassification(protocol.SessionStatePendingApproval, recent, time.Now()); !skip {
		t.Fatal("should skip for recently-active pending_approval Claude session")
	}
	if skip, _ := b.SkipClassification(protocol.SessionStateWorking, stale, time.Now()); skip {
		t.Fatal("should not skip for stale working Claude session")
	}
	if skip, _ := b.SkipClassification(protocol.SessionStateIdle, recent, time.Now()); skip {
		t.Fatal("should not skip for idle Claude session")
	}
	if skip, _ := b.SkipClassification(protocol.SessionStateWorking, "garbage", time.Now()); skip {
		t.Fatal("should not skip when LastSeen is unparseable")
	}

	// A scheduled session is parked on a cron/loop and must never be
	// reclassified by the watcher — UNCONDITIONALLY, regardless of hook
	// freshness, because parks routinely outlast the 2-minute stale threshold.
	if skip, _ := b.SkipClassification(protocol.SessionStateScheduled, recent, time.Now()); !skip {
		t.Fatal("should skip for scheduled session (recent hooks)")
	}
	if skip, _ := b.SkipClassification(protocol.SessionStateScheduled, stale, time.Now()); !skip {
		t.Fatal("should skip for scheduled session even when hooks are stale (long park)")
	}
	if skip, _ := b.SkipClassification(protocol.SessionStateScheduled, "garbage", time.Now()); !skip {
		t.Fatal("should skip for scheduled session even with unparseable LastSeen")
	}

	// Legacy RFC3339 (pre-Nano) should still parse and skip if fresh.
	recentRFC3339 := time.Now().Add(-5 * time.Second).Format(time.RFC3339)
	if skip, _ := b.SkipClassification(protocol.SessionStateWorking, recentRFC3339, time.Now()); !skip {
		t.Fatal("should skip with legacy RFC3339 timestamp that is still recent")
	}
}
