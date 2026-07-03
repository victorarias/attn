package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tasks"
)

// TestNotifyTaskTerminalFailurePersistsNotification closes the producer→store
// leg: the runner's terminal-failure sink turns a dead task into a durable,
// unread notification with the fields the detail dialog + Retry need.
func TestNotifyTaskTerminalFailurePersistsNotification(t *testing.T) {
	d := &Daemon{store: store.New()} // nil wsHub: broadcast is a guarded no-op

	d.notifyTaskTerminalFailure(&tasks.Task{
		ID:        "compact_context:ws-1",
		Kind:      compactContextKind,
		Subject:   "ws-1",
		State:     tasks.StateDead,
		Attempts:  3,
		LastError: "boom: context deadline exceeded",
	})

	list, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(list))
	}
	n := list[0]
	if n.Kind != notificationKindTaskFailed {
		t.Fatalf("kind = %q, want %q", n.Kind, notificationKindTaskFailed)
	}
	if n.Title != "Context compaction failed" {
		t.Fatalf("title = %q", n.Title)
	}
	if n.Detail != "boom: context deadline exceeded" {
		t.Fatalf("detail = %q", n.Detail)
	}
	if n.SourceKind != "task" || n.SourceID != "compact_context:ws-1" {
		t.Fatalf("source = %s/%s, want task/compact_context:ws-1", n.SourceKind, n.SourceID)
	}
	if !n.ReadAt.IsZero() {
		t.Fatalf("expected unread notification")
	}
	if unread, _ := d.store.UnreadNotificationCount(); unread != 1 {
		t.Fatalf("unread = %d, want 1", unread)
	}
}

// A nil store (the runner-disabled mode) drops the notification without panicking.
func TestNotifyTaskTerminalFailureNilStoreIsNoop(t *testing.T) {
	d := &Daemon{}
	d.notifyTaskTerminalFailure(&tasks.Task{Kind: reconcileKind, State: tasks.StateDead})
	// no panic == pass
}

func TestRenderTaskFailureNotification(t *testing.T) {
	// Known kind → friendly title; singular attempt wording.
	got := renderTaskFailureNotification(&tasks.Task{
		ID: "reconcile:t-9", Kind: reconcileKind, Subject: "t-9", Attempts: 1, LastError: "nope",
	})
	if got.Title != "Ticket reconciliation failed" {
		t.Fatalf("title = %q", got.Title)
	}
	if want := "attn retried 1 attempt and gave up. Retry to run it again."; got.Body != want {
		t.Fatalf("singular body = %q, want %q", got.Body, want)
	}
	// Unknown kind → generic title carrying the raw kind; plural wording.
	other := renderTaskFailureNotification(&tasks.Task{ID: "mystery:x", Kind: "mystery", Attempts: 2})
	if other.Title != "Background task failed: mystery" {
		t.Fatalf("unknown-kind title = %q", other.Title)
	}
	if want := "attn retried 2 attempts and gave up. Retry to run it again."; other.Body != want {
		t.Fatalf("plural body = %q, want %q", other.Body, want)
	}
}
