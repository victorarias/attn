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
	sessions := []*protocol.Session{
		{Label: "one", State: protocol.StateWorking},
		{Label: "two", State: protocol.StateWorking},
	}
	result := Format(sessions)
	if result != "✓ all clear" {
		t.Errorf("expected '✓ all clear' for no waiting, got %q", result)
	}
}

func TestFormat_OneWaiting(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "drumstick", State: protocol.StateWaiting},
	}
	result := Format(sessions)
	expected := "1 waiting: drumstick"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_TwoWaiting(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "drumstick", State: protocol.StateWaiting, StateSince: time.Now()},
		{Label: "hurdy", State: protocol.StateWaiting, StateSince: time.Now().Add(-time.Minute)},
	}
	result := Format(sessions)
	expected := "2 waiting: hurdy, drumstick"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_ManyWaiting_Truncates(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "one", State: protocol.StateWaiting, StateSince: time.Now()},
		{Label: "two", State: protocol.StateWaiting, StateSince: time.Now().Add(-time.Second)},
		{Label: "three", State: protocol.StateWaiting, StateSince: time.Now().Add(-2 * time.Second)},
		{Label: "four", State: protocol.StateWaiting, StateSince: time.Now().Add(-3 * time.Second)},
		{Label: "five", State: protocol.StateWaiting, StateSince: time.Now().Add(-4 * time.Second)},
	}
	result := Format(sessions)
	// Should truncate and show count (maxLabels=3 now)
	if result != "5 waiting: five, four, three, ..." {
		t.Errorf("got %q, want truncated format", result)
	}
}

func TestFormat_MixedStates(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "working1", State: protocol.StateWorking},
		{Label: "waiting1", State: protocol.StateWaiting},
		{Label: "working2", State: protocol.StateWorking},
	}
	result := Format(sessions)
	expected := "1 waiting: waiting1"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_SortsOldestFirst(t *testing.T) {
	now := time.Now()
	sessions := []*protocol.Session{
		{Label: "newest", State: protocol.StateWaiting, StateSince: now},
		{Label: "oldest", State: protocol.StateWaiting, StateSince: now.Add(-10 * time.Minute)},
		{Label: "middle", State: protocol.StateWaiting, StateSince: now.Add(-5 * time.Minute)},
	}
	result := Format(sessions)
	expected := "3 waiting: oldest, middle, newest"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_MutedSessionsExcluded(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "active", State: protocol.StateWaiting, Muted: false},
		{Label: "muted", State: protocol.StateWaiting, Muted: true},
	}
	result := Format(sessions)
	expected := "1 waiting: active"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_AllMuted(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "muted1", State: protocol.StateWaiting, Muted: true},
		{Label: "muted2", State: protocol.StateWaiting, Muted: true},
	}
	result := Format(sessions)
	if result != "✓ all clear" {
		t.Errorf("expected '✓ all clear' when all waiting are muted, got %q", result)
	}
}

func TestFormat_WithPRs(t *testing.T) {
	sessions := []*protocol.Session{}
	prs := []*protocol.PR{
		{ID: "owner/repo#123", State: protocol.StateWaiting, Reason: protocol.PRReasonReadyToMerge, Repo: "owner/repo", Number: 123, Muted: false},
	}

	result := FormatWithPRs(sessions, prs)
	if result != "1 PR: #123" {
		t.Errorf("Expected '1 PR: #123', got: %s", result)
	}
}

func TestFormat_SessionsAndPRs(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "foo", State: protocol.StateWaiting, Muted: false},
	}
	prs := []*protocol.PR{
		{ID: "owner/repo#123", State: protocol.StateWaiting, Repo: "owner/repo", Number: 123, Muted: false},
	}

	result := FormatWithPRs(sessions, prs)
	if result != "1 waiting: foo | 1 PR: #123" {
		t.Errorf("Expected '1 waiting: foo | 1 PR: #123', got: %s", result)
	}
}

func TestFormat_AllClear(t *testing.T) {
	sessions := []*protocol.Session{}
	prs := []*protocol.PR{}

	result := FormatWithPRs(sessions, prs)
	if result != "✓ all clear" {
		t.Errorf("Expected '✓ all clear', got: %s", result)
	}
}
