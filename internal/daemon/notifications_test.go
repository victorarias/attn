package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// TestNotifyTaskTerminalFailurePersistsNotification closes the producer→store
// leg: the queue's terminal-failure sink turns a dead job into a durable,
// unread notification with the fields the detail dialog + Retry need.
func TestNotifyTaskTerminalFailurePersistsNotification(t *testing.T) {
	d := &Daemon{store: store.New()} // nil wsHub: broadcast is a guarded no-op

	d.notifyTaskTerminalFailure(&jobs.Job{
		ID:        "job-1",
		Kind:      compactContextKind,
		UniqueKey: "ws-1",
		State:     jobs.StateDead,
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
	if n.SourceKind != "task" || n.SourceID != "job-1" {
		t.Fatalf("source = %s/%s, want task/job-1", n.SourceKind, n.SourceID)
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
	d.notifyTaskTerminalFailure(&jobs.Job{Kind: reconcileKind, State: jobs.StateDead})
	// no panic == pass
}

func TestRenderTaskFailureNotification(t *testing.T) {
	// Known kind → friendly title; singular attempt wording.
	got := renderTaskFailureNotification(&jobs.Job{
		ID: "job-9", Kind: reconcileKind, UniqueKey: "t-9", Attempts: 1, LastError: "nope",
	})
	if got.Title != "Ticket reconciliation failed" {
		t.Fatalf("title = %q", got.Title)
	}
	if want := "attn retried 1 attempt and gave up. Retry to run it again."; got.Body != want {
		t.Fatalf("singular body = %q, want %q", got.Body, want)
	}
	// Unknown kind → generic title carrying the raw kind; plural wording.
	other := renderTaskFailureNotification(&jobs.Job{ID: "job-x", Kind: "mystery", Attempts: 2})
	if other.Title != "Background task failed: mystery" {
		t.Fatalf("unknown-kind title = %q", other.Title)
	}
	if want := "attn retried 2 attempts and gave up. Retry to run it again."; other.Body != want {
		t.Fatalf("plural body = %q, want %q", other.Body, want)
	}
}

// A dead background job is a warning: it stays failed until the user retries it,
// but nothing keeps degrading while it waits. It is deliberately not critical —
// the ambient surface is reserved for what breaks silently and stays broken.
func TestTaskFailureNotificationIsWarning(t *testing.T) {
	if got := renderTaskFailureNotification(&jobs.Job{
		ID: "job-9", Kind: reconcileKind, Attempts: 1,
	}).Severity; got != store.NotificationWarning {
		t.Fatalf("severity = %q, want warning", got)
	}

	d := &Daemon{store: store.New()}
	d.notifyTaskTerminalFailure(&jobs.Job{
		ID: "job-1", Kind: compactContextKind, State: jobs.StateDead, Attempts: 3,
	})
	list, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Severity != store.NotificationWarning {
		t.Fatalf("persisted severity = %+v, want one warning row", list)
	}

	// A warning does not light the ambient critical surface.
	n, title, err := d.store.UnreadCriticalNotifications()
	if err != nil {
		t.Fatalf("unread critical: %v", err)
	}
	if n != 0 || title != "" {
		t.Fatalf("a dead job lit the critical surface: (%d, %q)", n, title)
	}
}

// The wire shape carries the stored severity, and normalizes on the way out so a
// row holding something unrecognized reaches the app as info rather than as a
// severity it has no styling for.
func TestNotificationToProtocolCarriesSeverity(t *testing.T) {
	for _, tc := range []struct {
		stored store.NotificationSeverity
		want   protocol.NotificationSeverity
	}{
		{store.NotificationInfo, protocol.NotificationSeverityInfo},
		{store.NotificationWarning, protocol.NotificationSeverityWarning},
		{store.NotificationCritical, protocol.NotificationSeverityCritical},
		{"", protocol.NotificationSeverityInfo},
		{"nonsense", protocol.NotificationSeverityInfo},
	} {
		got := notificationToProtocol(store.NotificationRecord{Severity: tc.stored})
		if got.Severity != tc.want {
			t.Fatalf("stored %q → wire %q, want %q", tc.stored, got.Severity, tc.want)
		}
	}
}
