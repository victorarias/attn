package store

import (
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
		TmuxTarget: "main:1.%42",
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
