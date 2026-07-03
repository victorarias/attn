package daemon

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tasks"
)

// notificationToProtocol converts one durable notification row into the
// user-facing protocol type. read_at is emitted as RFC3339 (UTC) when read and
// "" while unread, matching the schema's documented convention.
func notificationToProtocol(rec store.NotificationRecord) protocol.Notification {
	pn := protocol.Notification{
		ID:         rec.ID,
		Kind:       rec.Kind,
		Title:      rec.Title,
		Body:       rec.Body,
		Detail:     rec.Detail,
		SourceKind: rec.SourceKind,
		SourceID:   rec.SourceID,
		CreatedAt:  rec.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !rec.ReadAt.IsZero() {
		pn.ReadAt = rec.ReadAt.UTC().Format(time.RFC3339)
	}
	return pn
}

func notificationsToProtocol(recs []store.NotificationRecord) []protocol.Notification {
	out := make([]protocol.Notification, 0, len(recs))
	for _, r := range recs {
		out = append(out, notificationToProtocol(r))
	}
	return out
}

// sendNotificationListWSResult lists the whole notification feed (newest first)
// with the current unread count and replies to a websocket client with a
// notification_list_result correlated by requestID. A nil store is a successful
// empty list, not an error.
func (d *Daemon) sendNotificationListWSResult(client *wsClient, requestID string) {
	if d.store == nil {
		d.sendToClient(client, protocol.NotificationListResultMessage{
			Event:     protocol.EventNotificationListResult,
			RequestID: requestID,
			Success:   true,
		})
		return
	}
	list, err := d.store.ListNotifications()
	unread, unreadErr := d.store.UnreadNotificationCount()
	if err == nil {
		err = unreadErr
	}
	msg := protocol.NotificationListResultMessage{
		Event:         protocol.EventNotificationListResult,
		RequestID:     requestID,
		Success:       err == nil,
		Notifications: notificationsToProtocol(list),
		UnreadCount:   unread,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendNotificationMarkReadWSResult marks one notification read (when
// notificationID is set) or all unread ones (when nil), replies with a
// notification_mark_read_result carrying the post-mark unread count, and — on a
// successful mark — broadcasts notifications_updated so every client updates its
// badge and any open panel re-lists.
func (d *Daemon) sendNotificationMarkReadWSResult(client *wsClient, requestID string, notificationID *string) {
	fail := func(m string) {
		d.sendToClient(client, protocol.NotificationMarkReadResultMessage{
			Event:     protocol.EventNotificationMarkReadResult,
			RequestID: requestID,
			Success:   false,
			Error:     protocol.Ptr(m),
		})
	}
	if d.store == nil {
		fail("notification store unavailable")
		return
	}
	var markErr error
	if notificationID != nil && *notificationID != "" {
		markErr = d.store.MarkNotificationRead(*notificationID, time.Now())
	} else {
		_, markErr = d.store.MarkAllNotificationsRead(time.Now())
	}
	if markErr != nil {
		fail(markErr.Error())
		return
	}
	unread, err := d.store.UnreadNotificationCount()
	if err != nil {
		fail(err.Error())
		return
	}
	d.sendToClient(client, protocol.NotificationMarkReadResultMessage{
		Event:       protocol.EventNotificationMarkReadResult,
		RequestID:   requestID,
		Success:     true,
		UnreadCount: unread,
	})
	d.broadcastNotificationsUpdated()
}

// notificationKindTaskFailed marks a notification produced by a background task
// that exhausted its retries.
const notificationKindTaskFailed = "task_failed"

// taskFailureTitles maps a task kind to a human-facing notification title. An
// unknown kind falls back to a generic title carrying the raw kind string.
var taskFailureTitles = map[string]string{
	compactContextKind:           "Context compaction failed",
	notebookSummarizeSessionKind: "Session summary failed",
	notebookNarrateWorkspaceKind: "Workspace narration failed",
	reconcileKind:                "Ticket reconciliation failed",
}

// notifyTaskTerminalFailure is the task runner's OnTerminalFailure sink: it turns
// a task that exhausted its retries (reached the terminal dead state) into a
// durable notification and broadcasts the new unread count. It is wired to the
// runner in startCompactRunner and runs on the runner's goroutine, so it must not
// block or panic. A nil store drops the notification — the same mode in which the
// runner does not persist tasks at all.
func (d *Daemon) notifyTaskTerminalFailure(t *tasks.Task) {
	if t == nil || d.store == nil {
		return
	}
	if _, err := d.store.AddNotification(renderTaskFailureNotification(t), time.Now()); err != nil {
		d.logf("notifications: add task-failure notification for %s: %v", t.ID, err)
		return
	}
	d.broadcastNotificationsUpdated()
}

// renderTaskFailureNotification builds the durable notification for a dead task.
// SourceKind/SourceID point back at the task so the detail dialog's Retry can
// re-queue it; Detail carries the raw last error for diagnosis.
func renderTaskFailureNotification(t *tasks.Task) store.NotificationRecord {
	title := taskFailureTitles[t.Kind]
	if title == "" {
		title = fmt.Sprintf("Background task failed: %s", t.Kind)
	}
	attemptWord := "attempt"
	if t.Attempts != 1 {
		attemptWord = "attempts"
	}
	return store.NotificationRecord{
		Kind:       notificationKindTaskFailed,
		Title:      title,
		Body:       fmt.Sprintf("attn retried %d %s and gave up. Retry to run it again.", t.Attempts, attemptWord),
		Detail:     t.LastError,
		SourceKind: "task",
		SourceID:   t.ID,
	}
}

// broadcastNotificationsUpdated announces that the notification feed changed (a
// new notification was added, or one/all were marked read) so every client
// updates its unread badge and any open panel re-lists. It reads the current
// unread count and does a non-blocking broadcast, holding no shared state, so it
// is safe to invoke concurrently — including from the task runner's terminal-
// failure callback. A nil store broadcasts an unread count of 0.
func (d *Daemon) broadcastNotificationsUpdated() {
	unread := 0
	if d.store != nil {
		if n, err := d.store.UnreadNotificationCount(); err == nil {
			unread = n
		}
	}
	d.broadcastMessage(protocol.NotificationsUpdatedMessage{
		Event:       protocol.EventNotificationsUpdated,
		UnreadCount: unread,
	})
}
