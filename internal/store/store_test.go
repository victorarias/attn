package store

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestStore_AddAndGet(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "abc123",
		Label:      "drumstick",
		Directory:  "/home/user/project",
		State:      protocol.StateWorking,
		StateSince: time.Now(),
		LastSeen:   time.Now(),
	}

	s.Add(session)

	got := s.Get("abc123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Label != "drumstick" {
		t.Errorf("Label = %q, want %q", got.Label, "drumstick")
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

	s.Add(&protocol.Session{ID: "1", Label: "one", State: protocol.StateWorking})
	s.Add(&protocol.Session{ID: "2", Label: "two", State: protocol.StateWaiting})
	s.Add(&protocol.Session{ID: "3", Label: "three", State: protocol.StateWaiting})

	all := s.List("")
	if len(all) != 3 {
		t.Errorf("List() returned %d sessions, want 3", len(all))
	}

	waiting := s.List(protocol.StateWaiting)
	if len(waiting) != 2 {
		t.Errorf("List(waiting) returned %d sessions, want 2", len(waiting))
	}

	working := s.List(protocol.StateWorking)
	if len(working) != 1 {
		t.Errorf("List(working) returned %d sessions, want 1", len(working))
	}
}

func TestStore_UpdateState(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{
		ID:         "abc123",
		State:      protocol.StateWorking,
		StateSince: time.Now().Add(-5 * time.Minute),
	})

	before := s.Get("abc123").StateSince

	s.UpdateState("abc123", protocol.StateWaiting)

	got := s.Get("abc123")
	if got.State != protocol.StateWaiting {
		t.Errorf("State = %q, want %q", got.State, protocol.StateWaiting)
	}
	if !got.StateSince.After(before) {
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
		LastSeen: now.Add(-5 * time.Minute),
	})

	before := s.Get("abc123").LastSeen

	time.Sleep(10 * time.Millisecond) // Ensure time passes
	s.Touch("abc123")

	got := s.Get("abc123")
	if !got.LastSeen.After(before) {
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
		{ID: "owner/repo#1", State: protocol.StateWaiting, Muted: false},
		{ID: "owner/repo#2", State: protocol.StateWorking, Muted: false},
	}

	s.SetPRs(prs)

	all := s.ListPRs("")
	if len(all) != 2 {
		t.Errorf("ListPRs('') returned %d PRs, want 2", len(all))
	}

	waiting := s.ListPRs(protocol.StateWaiting)
	if len(waiting) != 1 {
		t.Errorf("ListPRs(waiting) returned %d PRs, want 1", len(waiting))
	}
}

func TestStore_SetPRs_PreservesMuted(t *testing.T) {
	s := New()

	// Initial PRs
	prs := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	// Mute it
	s.ToggleMutePR("owner/repo#1")

	// Set PRs again (simulating poll)
	prs2 := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWorking, Muted: false},
	}
	s.SetPRs(prs2)

	// Should still be muted
	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should still be muted after SetPRs")
	}
}

func TestStore_ToggleMutePR(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "owner/repo#1", State: protocol.StateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	s.ToggleMutePR("owner/repo#1")

	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should be muted after toggle")
	}
}

func TestStore_DirtyFlag(t *testing.T) {
	s := New()

	if s.IsDirty() {
		t.Error("new store should not be dirty")
	}

	s.Add(&protocol.Session{ID: "test", Label: "test"})

	if !s.IsDirty() {
		t.Error("store should be dirty after Add")
	}

	s.ClearDirty()

	if s.IsDirty() {
		t.Error("store should not be dirty after ClearDirty")
	}
}

func TestStore_BackgroundPersistence(t *testing.T) {
	// Create temp file for state
	tmpFile, err := os.CreateTemp("", "store-test-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	s := NewWithPersistence(tmpFile.Name())
	done := make(chan struct{})

	// Start background persistence with short interval
	go s.StartPersistence(50*time.Millisecond, done)

	// Add a session (marks dirty)
	s.Add(&protocol.Session{ID: "bg-test", Label: "bg-test"})

	// Wait for persistence to run
	time.Sleep(100 * time.Millisecond)

	// Stop persistence
	close(done)

	// Verify file was written
	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	if !strings.Contains(string(data), "bg-test") {
		t.Error("state file should contain bg-test session")
	}

	// Verify dirty flag was cleared
	if s.IsDirty() {
		t.Error("dirty flag should be cleared after save")
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
