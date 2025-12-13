package status

import (
	"testing"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestFormat_NoSessions(t *testing.T) {
	result := Format(nil)
	if result != "✓ all clear" {
		t.Errorf("expected '✓ all clear' for no sessions, got %q", result)
	}
}

func TestFormat_NoWaiting(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "one", State: protocol.SessionStateWorking},
		{Label: "two", State: protocol.SessionStateWorking},
	}
	result := Format(sessions)
	if result != "✓ all clear" {
		t.Errorf("expected '✓ all clear' for no waiting, got %q", result)
	}
}

func TestFormat_OneWaiting(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "drumstick", State: protocol.SessionStateWaitingInput},
	}
	result := Format(sessions)
	expected := "1 waiting: drumstick"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_TwoWaiting(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "drumstick", State: protocol.SessionStateWaitingInput, StateSince: protocol.TimestampNow().String()},
		{Label: "hurdy", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(time.Now().Add(-time.Minute)).String()},
	}
	result := Format(sessions)
	expected := "2 waiting: hurdy, drumstick"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_ManyWaiting_Truncates(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "one", State: protocol.SessionStateWaitingInput, StateSince: protocol.TimestampNow().String()},
		{Label: "two", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(time.Now().Add(-time.Second)).String()},
		{Label: "three", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(time.Now().Add(-2 * time.Second)).String()},
		{Label: "four", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(time.Now().Add(-3 * time.Second)).String()},
		{Label: "five", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(time.Now().Add(-4 * time.Second)).String()},
	}
	result := Format(sessions)
	// Should truncate and show count (maxLabels=3 now)
	if result != "5 waiting: five, four, three, ..." {
		t.Errorf("got %q, want truncated format", result)
	}
}

func TestFormat_MixedStates(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "working1", State: protocol.SessionStateWorking},
		{Label: "waiting1", State: protocol.SessionStateWaitingInput},
		{Label: "working2", State: protocol.SessionStateWorking},
	}
	result := Format(sessions)
	expected := "1 waiting: waiting1"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_SortsOldestFirst(t *testing.T) {
	now := time.Now()
	sessions := []protocol.Session{
		{Label: "newest", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(now).String()},
		{Label: "oldest", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(now.Add(-10 * time.Minute)).String()},
		{Label: "middle", State: protocol.SessionStateWaitingInput, StateSince: protocol.NewTimestamp(now.Add(-5 * time.Minute)).String()},
	}
	result := Format(sessions)
	expected := "3 waiting: oldest, middle, newest"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_MutedSessionsExcluded(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "active", State: protocol.SessionStateWaitingInput, Muted: false},
		{Label: "muted", State: protocol.SessionStateWaitingInput, Muted: true},
	}
	result := Format(sessions)
	expected := "1 waiting: active"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_AllMuted(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "muted1", State: protocol.SessionStateWaitingInput, Muted: true},
		{Label: "muted2", State: protocol.SessionStateWaitingInput, Muted: true},
	}
	result := Format(sessions)
	if result != "✓ all clear" {
		t.Errorf("expected '✓ all clear' when all waiting are muted, got %q", result)
	}
}

func TestFormat_WithPRs(t *testing.T) {
	sessions := []protocol.Session{}
	prs := []protocol.PR{
		{ID: "owner/repo#123", State: protocol.PRStateWaiting, Reason: protocol.PRReasonReadyToMerge, Repo: "owner/repo", Number: 123, Muted: false},
	}

	result := FormatWithPRs(sessions, prs)
	if result != "1 PR: #123" {
		t.Errorf("Expected '1 PR: #123', got: %s", result)
	}
}

func TestFormat_SessionsAndPRs(t *testing.T) {
	sessions := []protocol.Session{
		{Label: "foo", State: protocol.SessionStateWaitingInput, Muted: false},
	}
	prs := []protocol.PR{
		{ID: "owner/repo#123", State: protocol.PRStateWaiting, Repo: "owner/repo", Number: 123, Muted: false},
	}

	result := FormatWithPRs(sessions, prs)
	if result != "1 waiting: foo | 1 PR: #123" {
		t.Errorf("Expected '1 waiting: foo | 1 PR: #123', got: %s", result)
	}
}

func TestFormat_AllClear(t *testing.T) {
	sessions := []protocol.Session{}
	prs := []protocol.PR{}

	result := FormatWithPRs(sessions, prs)
	if result != "✓ all clear" {
		t.Errorf("Expected '✓ all clear', got: %s", result)
	}
}

func TestFormatWithPRs_RepoGrouping(t *testing.T) {
	tests := []struct {
		name     string
		sessions []protocol.Session
		prs      []protocol.PR
		repos    []protocol.RepoState
		want     string
	}{
		{
			name: "1 repo PRs only",
			prs: []protocol.PR{
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting},
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting},
			},
			want: "● repo-a(2)",
		},
		{
			name: "2 repos PRs only",
			prs: []protocol.PR{
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting},
				{Repo: "owner/repo-b", State: protocol.PRStateWaiting},
			},
			want: "● repo-a(1) repo-b(1)",
		},
		{
			name: "3+ repos shows counts",
			prs: []protocol.PR{
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting},
				{Repo: "owner/repo-b", State: protocol.PRStateWaiting},
				{Repo: "owner/repo-c", State: protocol.PRStateWaiting},
			},
			want: "● 3 PRs in 3 repos",
		},
		{
			name: "sessions and PRs separate",
			sessions: []protocol.Session{
				{Label: "foo", State: protocol.SessionStateWaitingInput},
				{Label: "bar", State: protocol.SessionStateWaitingInput},
			},
			prs: []protocol.PR{
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting},
			},
			want: "● #[fg=red,bold]2 sessions#[default] | repo-a(1)",
		},
		{
			name: "muted repo excluded",
			prs: []protocol.PR{
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting},
				{Repo: "owner/muted", State: protocol.PRStateWaiting},
			},
			repos: []protocol.RepoState{
				{Repo: "owner/muted", Muted: true},
			},
			want: "● repo-a(1)",
		},
		{
			name: "muted PR excluded",
			prs: []protocol.PR{
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting, Muted: false},
				{Repo: "owner/repo-a", State: protocol.PRStateWaiting, Muted: true},
			},
			want: "● repo-a(1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatWithPRsAndRepos(tt.sessions, tt.prs, tt.repos)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
