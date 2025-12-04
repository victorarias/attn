package github

import (
	"testing"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestParsePRFromGH(t *testing.T) {
	ghOutput := `{
		"number": 123,
		"title": "Fix bug",
		"url": "https://github.com/owner/repo/pull/123",
		"headRepository": {"nameWithOwner": "owner/repo"},
		"statusCheckRollup": {"state": "SUCCESS"},
		"reviewDecision": "APPROVED",
		"mergeable": "MERGEABLE"
	}`

	pr, err := parsePR([]byte(ghOutput), protocol.PRRoleAuthor)
	if err != nil {
		t.Fatalf("parsePR error: %v", err)
	}

	if pr.ID != "owner/repo#123" {
		t.Errorf("ID = %q, want %q", pr.ID, "owner/repo#123")
	}
	if pr.State != protocol.StateWaiting {
		t.Errorf("State = %q, want %q (ready to merge)", pr.State, protocol.StateWaiting)
	}
	if pr.Reason != protocol.PRReasonReadyToMerge {
		t.Errorf("Reason = %q, want %q", pr.Reason, protocol.PRReasonReadyToMerge)
	}
}

func TestDetermineState_CIFailed(t *testing.T) {
	state, reason := determineState("FAILURE", "", "", protocol.PRRoleAuthor)
	if state != protocol.StateWaiting {
		t.Errorf("State = %q, want waiting", state)
	}
	if reason != protocol.PRReasonCIFailed {
		t.Errorf("Reason = %q, want ci_failed", reason)
	}
}

func TestDetermineState_ChangesRequested(t *testing.T) {
	state, reason := determineState("SUCCESS", "CHANGES_REQUESTED", "", protocol.PRRoleAuthor)
	if state != protocol.StateWaiting {
		t.Errorf("State = %q, want waiting", state)
	}
	if reason != protocol.PRReasonChangesRequested {
		t.Errorf("Reason = %q, want changes_requested", reason)
	}
}

func TestDetermineState_ReviewNeeded(t *testing.T) {
	state, reason := determineState("SUCCESS", "REVIEW_REQUIRED", "", protocol.PRRoleReviewer)
	if state != protocol.StateWaiting {
		t.Errorf("State = %q, want waiting", state)
	}
	if reason != protocol.PRReasonReviewNeeded {
		t.Errorf("Reason = %q, want review_needed", reason)
	}
}

func TestDetermineState_WaitingOnOthers(t *testing.T) {
	state, reason := determineState("PENDING", "", "", protocol.PRRoleAuthor)
	if state != protocol.StateWorking {
		t.Errorf("State = %q, want working (CI pending)", state)
	}
	if reason != "" {
		t.Errorf("Reason = %q, want empty", reason)
	}
}
