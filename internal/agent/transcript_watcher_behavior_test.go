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

func TestShouldKeepCodexWorking(t *testing.T) {
	now := time.Now()
	if !shouldKeepCodexWorking(true, map[string]codexPendingTool{}, time.Time{}, now) {
		t.Fatal("open turn should keep codex in working")
	}
	if !shouldKeepCodexWorking(false, map[string]codexPendingTool{"call": {name: "exec_command", startedAt: now}}, time.Time{}, now) {
		t.Fatal("pending tool should keep codex in working")
	}
	if !shouldKeepCodexWorking(false, map[string]codexPendingTool{}, now.Add(-(codexActiveWindow - 500*time.Millisecond)), now) {
		t.Fatal("recent activity should keep codex in working")
	}
	if shouldKeepCodexWorking(false, map[string]codexPendingTool{}, now.Add(-(codexActiveWindow + 500*time.Millisecond)), now) {
		t.Fatal("stale activity with no turn/pending work should not force working")
	}
}

func TestShouldPromoteCodexNoOutputTurn(t *testing.T) {
	tests := []struct {
		name              string
		sawTurnStart      bool
		assistantMessages int
		state             protocol.SessionState
		want              bool
	}{
		{
			name:              "requires turn start signal",
			sawTurnStart:      false,
			assistantMessages: 0,
			state:             protocol.SessionStateWorking,
			want:              false,
		},
		{
			name:              "requires zero assistant messages",
			sawTurnStart:      true,
			assistantMessages: 1,
			state:             protocol.SessionStateWorking,
			want:              false,
		},
		{
			name:              "suppresses on pending approval",
			sawTurnStart:      true,
			assistantMessages: 0,
			state:             protocol.SessionStatePendingApproval,
			want:              false,
		},
		{
			name:              "suppresses on waiting_input",
			sawTurnStart:      true,
			assistantMessages: 0,
			state:             protocol.SessionStateWaitingInput,
			want:              false,
		},
		{
			name:              "promotes when observed start and no output",
			sawTurnStart:      true,
			assistantMessages: 0,
			state:             protocol.SessionStateWorking,
			want:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldPromoteCodexNoOutputTurn(tt.sawTurnStart, tt.assistantMessages, tt.state)
			if got != tt.want {
				t.Fatalf("shouldPromoteCodexNoOutputTurn(%v, %d, %s) = %v, want %v", tt.sawTurnStart, tt.assistantMessages, tt.state, got, tt.want)
			}
		})
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

	// Legacy RFC3339 (pre-Nano) should still parse and skip if fresh.
	recentRFC3339 := time.Now().Add(-5 * time.Second).Format(time.RFC3339)
	if skip, _ := b.SkipClassification(protocol.SessionStateWorking, recentRFC3339, time.Now()); !skip {
		t.Fatal("should skip with legacy RFC3339 timestamp that is still recent")
	}
}
