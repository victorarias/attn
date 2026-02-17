package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestStore_AddAndGet(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "abc123",
		Label:      "drumstick",
		Directory:  "/home/user/project",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	}

	s.Add(session)

	got := s.Get("abc123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Label != "drumstick" {
		t.Errorf("Label = %q, want %q", got.Label, "drumstick")
	}
	if got.Agent != "codex" {
		t.Errorf("Agent = %q, want %q", got.Agent, "codex")
	}
}

func TestStore_AddAndGet_PreservesAgent(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "agent123",
		Label:      "session-with-agent",
		Agent:      "claude",
		Directory:  "/home/user/project",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	}

	s.Add(session)

	got := s.Get("agent123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", got.Agent, "claude")
	}
}

func TestStore_Remove(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:    "abc123",
		Label: "drumstick",
	}
	s.Add(session)

	s.Remove("abc123")

	if got := s.Get("abc123"); got != nil {
		t.Errorf("expected nil after remove, got %+v", got)
	}
}

func TestStore_List(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{ID: "1", Label: "one", State: protocol.SessionStateWorking})
	s.Add(&protocol.Session{ID: "2", Label: "two", State: protocol.SessionStateWaitingInput})
	s.Add(&protocol.Session{ID: "3", Label: "three", State: protocol.SessionStateWaitingInput})

	all := s.List("")
	if len(all) != 3 {
		t.Errorf("List() returned %d sessions, want 3", len(all))
	}

	waiting := s.List(string(protocol.SessionStateWaitingInput))
	if len(waiting) != 2 {
		t.Errorf("List(waiting_input) returned %d sessions, want 2", len(waiting))
	}

	working := s.List(string(protocol.SessionStateWorking))
	if len(working) != 1 {
		t.Errorf("List(working) returned %d sessions, want 1", len(working))
	}
}

func TestStore_List_StableOrderForDuplicateLabels(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{ID: "b-id", Label: "dup", State: protocol.SessionStateWorking})
	s.Add(&protocol.Session{ID: "a-id", Label: "dup", State: protocol.SessionStateWorking})
	s.Add(&protocol.Session{ID: "c-id", Label: "zzz", State: protocol.SessionStateWorking})

	all := s.List("")
	if len(all) != 3 {
		t.Fatalf("List() returned %d sessions, want 3", len(all))
	}

	if all[0].ID != "a-id" || all[1].ID != "b-id" || all[2].ID != "c-id" {
		t.Fatalf("unexpected order: got [%s %s %s], want [a-id b-id c-id]", all[0].ID, all[1].ID, all[2].ID)
	}

	// Re-read to ensure deterministic ordering across calls.
	all2 := s.List("")
	if all2[0].ID != "a-id" || all2[1].ID != "b-id" || all2[2].ID != "c-id" {
		t.Fatalf("order changed across calls: got [%s %s %s], want [a-id b-id c-id]", all2[0].ID, all2[1].ID, all2[2].ID)
	}
}

func TestStore_UpdateState(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{
		ID:         "abc123",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.NewTimestamp(time.Now().Add(-5 * time.Minute)).String(),
	})

	before := protocol.Timestamp(s.Get("abc123").StateSince).Time()

	s.UpdateState("abc123", string(protocol.SessionStateWaitingInput))

	got := s.Get("abc123")
	if got.State != protocol.SessionStateWaitingInput {
		t.Errorf("State = %q, want %q", got.State, protocol.SessionStateWaitingInput)
	}
	if !protocol.Timestamp(got.StateSince).Time().After(before) {
		t.Error("StateSince should be updated")
	}
}

func TestStore_UpdateTodos(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{
		ID:    "abc123",
		Label: "test",
		Todos: []string{},
	})

	todos := []string{"task 1", "task 2"}
	s.UpdateTodos("abc123", todos)

	got := s.Get("abc123")
	if len(got.Todos) != 2 {
		t.Errorf("Todos length = %d, want 2", len(got.Todos))
	}
	if got.Todos[0] != "task 1" {
		t.Errorf("Todos[0] = %q, want %q", got.Todos[0], "task 1")
	}
}

func TestStore_Touch(t *testing.T) {
	s := New()

	now := time.Now()
	s.Add(&protocol.Session{
		ID:       "abc123",
		LastSeen: protocol.NewTimestamp(now.Add(-5 * time.Minute)).String(),
	})

	before := protocol.Timestamp(s.Get("abc123").LastSeen).Time()

	time.Sleep(10 * time.Millisecond) // Ensure time passes
	s.Touch("abc123")

	got := s.Get("abc123")
	if !protocol.Timestamp(got.LastSeen).Time().After(before) {
		t.Error("LastSeen should be updated after Touch")
	}
}

func TestStore_ToggleMute(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{
		ID:    "abc123",
		Muted: false,
	})

	// First toggle: false -> true
	s.ToggleMute("abc123")
	if !s.Get("abc123").Muted {
		t.Error("expected Muted=true after first toggle")
	}

	// Second toggle: true -> false
	s.ToggleMute("abc123")
	if s.Get("abc123").Muted {
		t.Error("expected Muted=false after second toggle")
	}
}

func TestStore_ToggleMute_NonExistent(t *testing.T) {
	s := New()

	// Should not panic on non-existent session
	s.ToggleMute("nonexistent")
}

func TestStore_SetAndListPRs(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting, Muted: false},
		{ID: "github.com:owner/repo#2", State: protocol.StateWorking, Muted: false},
	}

	s.SetPRs(prs)

	all := s.ListPRs("")
	if len(all) != 2 {
		t.Errorf("ListPRs('') returned %d PRs, want 2", len(all))
	}

	waiting := s.ListPRs(protocol.PRStateWaiting)
	if len(waiting) != 1 {
		t.Errorf("ListPRs(waiting) returned %d PRs, want 1", len(waiting))
	}
}

func TestStore_SetPRs_PreservesMuted(t *testing.T) {
	s := New()

	// Initial PRs
	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	// Mute it
	s.ToggleMutePR("github.com:owner/repo#1")

	// Set PRs again (simulating poll)
	prs2 := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.StateWorking, Muted: false},
	}
	s.SetPRs(prs2)

	// Should still be muted
	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should still be muted after SetPRs")
	}
}

func TestStore_SetPRs_PreservesApprovedByMe(t *testing.T) {
	s := New()

	// Initial PR
	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting},
	}
	s.SetPRs(prs)

	// Mark as approved
	s.MarkPRApproved("github.com:owner/repo#1")

	// Verify it's approved
	pr := s.GetPR("github.com:owner/repo#1")
	if !pr.ApprovedByMe {
		t.Fatal("PR should be marked as approved")
	}

	// Set PRs again (simulating poll after approval action)
	prs2 := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting}, // ApprovedByMe not set in incoming data
	}
	s.SetPRs(prs2)

	// Should still be approved
	pr = s.GetPR("github.com:owner/repo#1")
	if !pr.ApprovedByMe {
		t.Error("PR should still be approved after SetPRs")
	}
}

func TestStore_SetPRs_PreservesDetailFields(t *testing.T) {
	s := New()

	// Initial PR
	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting},
	}
	s.SetPRs(prs)

	// Set detail fields (simulating fetchPRDetails)
	mergeable := true
	s.UpdatePRDetails("github.com:owner/repo#1", &mergeable, "clean", "success", "approved", "abc123", "feature-branch")

	// Verify details are set
	pr := s.GetPR("github.com:owner/repo#1")
	if protocol.Deref(pr.CIStatus) != "success" {
		t.Fatalf("CIStatus should be 'success', got '%s'", protocol.Deref(pr.CIStatus))
	}

	// Set PRs again (simulating poll after an action like approve)
	// The incoming PR has a NEWER LastUpdated than DetailsFetchedAt
	// This is what happens in real scenario - GitHub returns updated timestamp
	prs2 := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.StateWorking, LastUpdated: protocol.NewTimestamp(time.Now().Add(time.Hour)).String()}, // No detail fields, but newer timestamp
	}
	s.SetPRs(prs2)

	// Details should be preserved
	pr = s.GetPR("github.com:owner/repo#1")
	if protocol.Deref(pr.CIStatus) != "success" {
		t.Errorf("CIStatus should still be 'success' after SetPRs, got '%s'", protocol.Deref(pr.CIStatus))
	}
	if protocol.Deref(pr.ReviewStatus) != "approved" {
		t.Errorf("ReviewStatus should still be 'approved' after SetPRs, got '%s'", protocol.Deref(pr.ReviewStatus))
	}
	if protocol.Deref(pr.MergeableState) != "clean" {
		t.Errorf("MergeableState should still be 'clean' after SetPRs, got '%s'", protocol.Deref(pr.MergeableState))
	}
	if protocol.Deref(pr.HeadSHA) != "abc123" {
		t.Errorf("HeadSHA should still be 'abc123' after SetPRs, got '%s'", protocol.Deref(pr.HeadSHA))
	}
}

func TestStore_ToggleMutePR(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	s.ToggleMutePR("github.com:owner/repo#1")

	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should be muted after toggle")
	}
}

func TestStore_SQLitePersistence(t *testing.T) {
	// Create temp directory for SQLite DB
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	// Create store with SQLite
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}

	// Add a session (persisted immediately with SQLite)
	s.Add(&protocol.Session{ID: "sqlite-test", Label: "sqlite-test"})

	// Close and reopen to verify persistence
	s.Close()

	s2, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB reopen error: %v", err)
	}
	defer s2.Close()

	// Verify session persisted
	got := s2.Get("sqlite-test")
	if got == nil {
		t.Error("session should persist across store reopens")
	}
	if got != nil && got.Label != "sqlite-test" {
		t.Errorf("Label = %q, want sqlite-test", got.Label)
	}
}

func TestStore_RepoState(t *testing.T) {
	s := New()

	// Initially no repo state
	state := s.GetRepoState("owner/repo")
	if state != nil {
		t.Error("expected nil for unknown repo")
	}

	// Toggle mute creates state
	s.ToggleMuteRepo("owner/repo")
	state = s.GetRepoState("owner/repo")
	if state == nil {
		t.Fatal("expected repo state after toggle")
	}
	if !state.Muted {
		t.Error("repo should be muted")
	}

	// Toggle again unmutes
	s.ToggleMuteRepo("owner/repo")
	state = s.GetRepoState("owner/repo")
	if state.Muted {
		t.Error("repo should be unmuted")
	}

	// Set collapsed
	s.SetRepoCollapsed("owner/repo", true)
	state = s.GetRepoState("owner/repo")
	if !state.Collapsed {
		t.Error("repo should be collapsed")
	}
}

func TestStore_ListRepoStates(t *testing.T) {
	s := New()

	s.ToggleMuteRepo("repo-a")
	s.SetRepoCollapsed("repo-b", true)

	states := s.ListRepoStates()
	if len(states) != 2 {
		t.Errorf("expected 2 repo states, got %d", len(states))
	}
}

func TestStore_AuthorState(t *testing.T) {
	s := New()

	// Initially no author states
	states := s.ListAuthorStates()
	if len(states) != 0 {
		t.Errorf("expected 0 author states, got %d", len(states))
	}

	// Toggle mute creates state
	s.ToggleMuteAuthor("dependabot")
	states = s.ListAuthorStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 author state, got %d", len(states))
	}
	if !states[0].Muted {
		t.Error("author should be muted")
	}

	// Toggle again unmutes
	s.ToggleMuteAuthor("dependabot")
	states = s.ListAuthorStates()
	if states[0].Muted {
		t.Error("author should be unmuted")
	}
}

func TestStore_ListAuthorStates(t *testing.T) {
	s := New()

	s.ToggleMuteAuthor("dependabot")
	s.ToggleMuteAuthor("renovate")

	states := s.ListAuthorStates()
	if len(states) != 2 {
		t.Errorf("expected 2 author states, got %d", len(states))
	}
}
