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

// Every watched agent runs the watcher for the same single reason: a turn the
// user halted is the one ending none of them report through any other channel.
func TestWatcherBehaviorsDetectAHaltedTurn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		behavior   TranscriptWatcherBehavior
		abort      string
		wantDetail string
		wantAt     string
		ignored    string
		// notAHalt ends a turn for the agent's own reasons. It must not settle the
		// session: the session is often working again a moment later.
		notAHalt string
	}{
		{
			name:       "claude",
			behavior:   &claudeTranscriptWatcherBehavior{},
			abort:      `{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"interruptedMessageId":"msg_01","timestamp":"2026-08-01T22:08:15.284Z"}`,
			wantDetail: "[Request interrupted by user]",
			wantAt:     "2026-08-01T22:08:15.284Z",
			ignored:    `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working on it"}]}}`,
		},
		{
			name:       "codex",
			behavior:   &codexTranscriptWatcherBehavior{},
			abort:      `{"type":"event_msg","timestamp":"2026-08-01T21:58:33.937Z","payload":{"type":"turn_aborted","reason":"interrupted"}}`,
			wantDetail: "interrupted",
			wantAt:     "2026-08-01T21:58:33.937Z",
			ignored:    `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"019f"}}`,
			notAHalt:   `{"type":"event_msg","payload":{"type":"turn_aborted","reason":"replaced"}}`,
		},
		{
			name:       "copilot",
			behavior:   newCopilotBehavior(),
			abort:      `{"type":"abort","timestamp":"2026-08-02T08:52:00.344Z","data":{"reason":"user_initiated"}}`,
			wantDetail: "user_initiated",
			wantAt:     "2026-08-02T08:52:00.344Z",
			ignored:    `{"type":"assistant.message","data":{"content":"working on it"}}`,
			notAHalt:   `{"type":"abort","data":{"reason":"tool_failure"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.behavior.HandleLine([]byte(tc.abort), time.Now(), protocol.SessionStateWorking)
			if !got.Aborted || got.AbortDetail != tc.wantDetail {
				t.Fatalf("got %+v, want the halt reported with detail %q", got, tc.wantDetail)
			}
			// Undated, the halt cannot be told from one replayed out of history, and
			// the watcher throws it away.
			if got.AbortAt.UTC().Format(time.RFC3339Nano) != tc.wantAt {
				t.Fatalf("abort at %s, want %s", got.AbortAt.UTC().Format(time.RFC3339Nano), tc.wantAt)
			}
			// The halt is a fact about the turn, not a state request: the resolver
			// weighs it against everything else, so the behavior must not also be
			// naming a state.
			if got.State != "" {
				t.Fatalf("state %q, want none: the behavior reports the fact, the resolver decides", got.State)
			}
			if got := tc.behavior.HandleLine([]byte(tc.ignored), time.Now(), protocol.SessionStateWorking); got.Aborted {
				t.Fatalf("an ordinary line was read as a halt: %+v", got)
			}
			if tc.notAHalt == "" {
				return
			}
			if got := tc.behavior.HandleLine([]byte(tc.notAHalt), time.Now(), protocol.SessionStateWorking); got.Aborted {
				t.Fatalf("a turn the agent ended on its own was read as a user halt: %+v", got)
			}
		})
	}
}

func newCopilotBehavior() TranscriptWatcherBehavior {
	b := &copilotTranscriptWatcherBehavior{}
	b.Reset()
	return b
}

// Copilot writes no assistant.turn_end after an abort. Its watcher keeps the turn
// bracket itself, so the abort has to close it — otherwise Tick pins the session
// working for the rest of the session's life, whoever caused the abort.
func TestCopilotAbortClosesTheTurnBracket(t *testing.T) {
	for _, abort := range []string{
		`{"type":"abort","data":{"reason":"user_initiated"}}`,
		`{"type":"abort","data":{"reason":"tool_failure"}}`,
	} {
		t.Run(abort, func(t *testing.T) {
			b := &copilotTranscriptWatcherBehavior{}
			b.Reset()

			now := time.Now()
			b.HandleLine([]byte(`{"type":"assistant.turn_start","data":{"turnId":"0"}}`), now, protocol.SessionStateWorking)
			b.HandleLine([]byte(`{"type":"tool.execution_start","data":{"toolCallId":"call_1","toolName":"bash"}}`), now, protocol.SessionStateWorking)
			if tick := b.Tick(now.Add(2*time.Second), protocol.SessionStateWorking); !tick.BlockClassification {
				t.Fatal("an open copilot turn should block classification")
			}

			b.HandleLine([]byte(abort), now.Add(3*time.Second), protocol.SessionStateWorking)

			tick := b.Tick(now.Add(4*time.Second), protocol.SessionStateWorking)
			if tick.BlockClassification {
				t.Fatal("the aborted turn is still pinning the session working")
			}
			if tick.State != "" {
				t.Fatalf("state %q, want none after an abort", tick.State)
			}
		})
	}
}

// Codex must not pick up the default behavior: that one classifies on the quiet
// window, which would race the Stop hook's verdict on the same turn.
func TestCodexWatcherNeverClassifies(t *testing.T) {
	behavior, ok := GetTranscriptWatcherBehavior(Get("codex"))
	if !ok {
		t.Fatal("codex has no watcher behavior; its halted turns would go unseen")
	}
	if _, isCodex := behavior.(*codexTranscriptWatcherBehavior); !isCodex {
		t.Fatalf("codex got %T, want its own behavior", behavior)
	}
	for _, state := range []protocol.SessionState{
		protocol.SessionStateIdle,
		protocol.SessionStateWorking,
		protocol.SessionStateWaitingInput,
	} {
		skip, reason := behavior.SkipClassification(state, "", time.Now())
		if !skip {
			t.Fatalf("codex would classify from state %q (%s); classification is hook-owned", state, reason)
		}
	}
}
