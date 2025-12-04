package github

import (
	"testing"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestParsePRList_Author(t *testing.T) {
	ghOutput := `[{
		"number": 123,
		"title": "Fix bug",
		"url": "https://github.com/owner/repo/pull/123",
		"isDraft": false,
		"repository": {"nameWithOwner": "owner/repo"}
	}]`

	prs, err := parsePRList([]byte(ghOutput), protocol.PRRoleAuthor)
	if err != nil {
		t.Fatalf("parsePRList error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}

	pr := prs[0]
	if pr.ID != "owner/repo#123" {
		t.Errorf("ID = %q, want %q", pr.ID, "owner/repo#123")
	}
	if pr.State != protocol.StateWaiting {
		t.Errorf("State = %q, want %q", pr.State, protocol.StateWaiting)
	}
	if pr.Role != protocol.PRRoleAuthor {
		t.Errorf("Role = %q, want %q", pr.Role, protocol.PRRoleAuthor)
	}
}

func TestParsePRList_Reviewer(t *testing.T) {
	ghOutput := `[{
		"number": 456,
		"title": "New feature",
		"url": "https://github.com/other/repo/pull/456",
		"isDraft": false,
		"repository": {"nameWithOwner": "other/repo"}
	}]`

	prs, err := parsePRList([]byte(ghOutput), protocol.PRRoleReviewer)
	if err != nil {
		t.Fatalf("parsePRList error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}

	pr := prs[0]
	if pr.Role != protocol.PRRoleReviewer {
		t.Errorf("Role = %q, want %q", pr.Role, protocol.PRRoleReviewer)
	}
	if pr.Reason != protocol.PRReasonReviewNeeded {
		t.Errorf("Reason = %q, want %q", pr.Reason, protocol.PRReasonReviewNeeded)
	}
}

func TestParsePRList_SkipsDrafts(t *testing.T) {
	ghOutput := `[
		{"number": 1, "title": "Draft PR", "url": "https://github.com/o/r/pull/1", "isDraft": true, "repository": {"nameWithOwner": "o/r"}},
		{"number": 2, "title": "Ready PR", "url": "https://github.com/o/r/pull/2", "isDraft": false, "repository": {"nameWithOwner": "o/r"}}
	]`

	prs, err := parsePRList([]byte(ghOutput), protocol.PRRoleAuthor)
	if err != nil {
		t.Fatalf("parsePRList error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1 (draft should be skipped)", len(prs))
	}

	if prs[0].Number != 2 {
		t.Errorf("Expected PR #2, got #%d", prs[0].Number)
	}
}

func TestConvertPR(t *testing.T) {
	gh := ghSearchPR{
		Number: 789,
		Title:  "Test PR",
		URL:    "https://github.com/test/repo/pull/789",
		Repository: struct {
			NameWithOwner string `json:"nameWithOwner"`
		}{NameWithOwner: "test/repo"},
	}

	pr := convertPR(gh, protocol.PRRoleAuthor)

	if pr.ID != "test/repo#789" {
		t.Errorf("ID = %q, want %q", pr.ID, "test/repo#789")
	}
	if pr.Repo != "test/repo" {
		t.Errorf("Repo = %q, want %q", pr.Repo, "test/repo")
	}
	if pr.Number != 789 {
		t.Errorf("Number = %d, want %d", pr.Number, 789)
	}
}
