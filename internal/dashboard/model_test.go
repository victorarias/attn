package dashboard

import (
	"testing"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestModel_Init(t *testing.T) {
	m := NewModel(nil)
	if m.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", m.cursor)
	}
}

func TestModel_MoveCursor(t *testing.T) {
	m := NewModel(nil)
	m.sessions = []*protocol.Session{
		{ID: "1"},
		{ID: "2"},
		{ID: "3"},
	}

	// Move down
	m.moveCursor(1)
	if m.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.cursor)
	}

	// Move down again
	m.moveCursor(1)
	if m.cursor != 2 {
		t.Errorf("cursor after second down = %d, want 2", m.cursor)
	}

	// Move down at bottom (should stay)
	m.moveCursor(1)
	if m.cursor != 2 {
		t.Errorf("cursor at bottom = %d, want 2", m.cursor)
	}

	// Move up
	m.moveCursor(-1)
	if m.cursor != 1 {
		t.Errorf("cursor after up = %d, want 1", m.cursor)
	}
}

func TestModel_SelectedSession(t *testing.T) {
	m := NewModel(nil)
	m.sessions = []*protocol.Session{
		{ID: "1", Label: "one"},
		{ID: "2", Label: "two"},
	}

	m.cursor = 1
	selected := m.SelectedSession()
	if selected == nil {
		t.Fatal("expected selected session")
	}
	if selected.Label != "two" {
		t.Errorf("selected label = %q, want %q", selected.Label, "two")
	}
}

