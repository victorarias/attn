package attention

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAggregator_Aggregate(t *testing.T) {
	now := time.Now()
	older := now.Add(-5 * time.Minute)
	oldest := now.Add(-10 * time.Minute)

	sessions := []protocol.Session{
		{
			ID:         "sess-1",
			Label:      "working-session",
			State:      protocol.SessionStateWorking,
			StateSince: protocol.NewTimestamp(now).String(),
			Muted:      false,
		},
		{
			ID:         "sess-2",
			Label:      "waiting-session",
			State:      protocol.SessionStateWaitingInput,
			StateSince: protocol.NewTimestamp(older).String(),
			Muted:      false,
		},
		{
			ID:         "sess-3",
			Label:      "muted-waiting",
			State:      protocol.SessionStateWaitingInput,
			StateSince: protocol.NewTimestamp(now).String(),
			Muted:      true,
		},
	}

	prs := []protocol.PR{
		{
			ID:          "github.com:owner/repo#1",
			Title:       "Active PR",
			State:       protocol.PRStateWaiting,
			Reason:      protocol.PRReasonReviewNeeded,
			Repo:        "owner/repo",
			LastUpdated: protocol.NewTimestamp(oldest).String(),
			Muted:       false,
		},
		{
			ID:          "github.com:owner/repo#2",
			Title:       "Muted PR",
			State:       protocol.PRStateWaiting,
			Reason:      protocol.PRReasonCIFailed,
			Repo:        "owner/repo",
			LastUpdated: protocol.NewTimestamp(now).String(),
			Muted:       true,
		},
		{
			ID:          "github.com:owner/muted-repo#3",
			Title:       "PR in muted repo",
			State:       protocol.PRStateWaiting,
			Reason:      protocol.PRReasonReadyToMerge,
			Repo:        "owner/muted-repo",
			LastUpdated: protocol.NewTimestamp(now).String(),
			Muted:       false,
		},
	}

	repos := []protocol.RepoState{
		{Repo: "owner/muted-repo", Muted: true},
	}

	agg := NewAggregator(repos, nil)
	result := agg.Aggregate(sessions, prs)

	// Should have 2 items needing attention: sess-2 and github.com:owner/repo#1
	if result.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", result.TotalCount)
	}

	if result.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", result.SessionCount)
	}

	if result.PRCount != 1 {
		t.Errorf("PRCount = %d, want 1", result.PRCount)
	}

	// Items should be sorted by Since (oldest first)
	// oldest is the PR, then older is the session
	if len(result.Items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(result.Items))
	}

	if result.Items[0].Kind != "pr" {
		t.Errorf("First item should be PR (oldest), got %s", result.Items[0].Kind)
	}

	if result.Items[1].Kind != "session" {
		t.Errorf("Second item should be session, got %s", result.Items[1].Kind)
	}
}

func TestAggregator_EmptyInput(t *testing.T) {
	agg := NewAggregator(nil, nil)
	result := agg.Aggregate(nil, nil)

	if result.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0", result.TotalCount)
	}

	if len(result.Items) != 0 {
		t.Errorf("Items = %d, want 0", len(result.Items))
	}
}

func TestAggregator_AllMuted(t *testing.T) {
	sessions := []protocol.Session{
		{
			ID:         "sess-1",
			Label:      "muted",
			State:      protocol.SessionStateWaitingInput,
			StateSince: protocol.TimestampNow().String(),
			Muted:      true,
		},
	}

	prs := []protocol.PR{
		{
			ID:          "github.com:owner/repo#1",
			Title:       "muted",
			State:       protocol.PRStateWaiting,
			Repo:        "owner/repo",
			LastUpdated: protocol.TimestampNow().String(),
			Muted:       true,
		},
	}

	agg := NewAggregator(nil, nil)
	result := agg.Aggregate(sessions, prs)

	if result.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0 (all muted)", result.TotalCount)
	}
}

func TestResult_FilterByKind(t *testing.T) {
	result := Result{
		Items: []Item{
			{ID: "sess-1", Kind: "session"},
			{ID: "pr-1", Kind: "pr"},
			{ID: "sess-2", Kind: "session"},
			{ID: "pr-2", Kind: "pr"},
		},
		SessionCount: 2,
		PRCount:      2,
		TotalCount:   4,
	}

	sessions := result.Sessions()
	if len(sessions) != 2 {
		t.Errorf("Sessions() = %d items, want 2", len(sessions))
	}

	prs := result.PRs()
	if len(prs) != 2 {
		t.Errorf("PRs() = %d items, want 2", len(prs))
	}
}

func TestSessionAdapter(t *testing.T) {
	session := &protocol.Session{
		ID:         "test-session",
		Label:      "Test",
		State:      protocol.SessionStateWaitingInput,
		StateSince: protocol.TimestampNow().String(),
		Muted:      false,
	}

	adapter := SessionAdapter{Session: session}

	if adapter.AttentionID() != "test-session" {
		t.Errorf("AttentionID() = %q, want %q", adapter.AttentionID(), "test-session")
	}

	if adapter.AttentionKind() != "session" {
		t.Errorf("AttentionKind() = %q, want %q", adapter.AttentionKind(), "session")
	}

	if !adapter.NeedsAttention() {
		t.Error("NeedsAttention() = false, want true")
	}

	if adapter.AttentionReason() != "waiting_input" {
		t.Errorf("AttentionReason() = %q, want %q", adapter.AttentionReason(), "waiting_input")
	}

	// Test working state doesn't need attention
	session.State = protocol.SessionStateWorking
	if adapter.NeedsAttention() {
		t.Error("Working session should not need attention")
	}
}

func TestPRAdapter(t *testing.T) {
	pr := &protocol.PR{
		ID:          "github.com:owner/repo#123",
		Title:       "Test PR",
		State:       protocol.PRStateWaiting,
		Reason:      protocol.PRReasonReviewNeeded,
		Repo:        "owner/repo",
		LastUpdated: protocol.TimestampNow().String(),
		Muted:       false,
	}

	adapter := PRAdapter{PR: pr, RepoMuted: false}

	if adapter.AttentionID() != "github.com:owner/repo#123" {
		t.Errorf("AttentionID() = %q, want %q", adapter.AttentionID(), "github.com:owner/repo#123")
	}

	if adapter.AttentionKind() != "pr" {
		t.Errorf("AttentionKind() = %q, want %q", adapter.AttentionKind(), "pr")
	}

	if !adapter.NeedsAttention() {
		t.Error("NeedsAttention() = false, want true")
	}

	if adapter.AttentionReason() != protocol.PRReasonReviewNeeded {
		t.Errorf("AttentionReason() = %q, want %q", adapter.AttentionReason(), protocol.PRReasonReviewNeeded)
	}

	// Test repo muted
	adapter.RepoMuted = true
	if adapter.NeedsAttention() {
		t.Error("PR in muted repo should not need attention")
	}
	if !adapter.AttentionMuted() {
		t.Error("AttentionMuted() should be true when repo is muted")
	}
}
