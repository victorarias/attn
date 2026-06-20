package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tasks"
)

// notebookTaskToProtocol converts one durable task-runner record into the
// user-facing protocol type. Timestamps are emitted as RFC3339 (UTC); LastError
// becomes a pointer only when non-empty.
//
// SECURITY: Task.Meta carries internal inputs (e.g. transcript filesystem paths)
// and Task.CommitGuard is a live run latch — neither has a field on
// protocol.NotebookTask, so neither can leak to a client. Do not add them.
func notebookTaskToProtocol(t *tasks.Task) protocol.NotebookTask {
	pt := protocol.NotebookTask{
		ID:            t.ID,
		Kind:          t.Kind,
		Subject:       t.Subject,
		State:         string(t.State),
		Attempts:      t.Attempts,
		NextAttemptAt: t.NextAttemptAt.UTC().Format(time.RFC3339),
		CreatedAt:     t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.LastError != "" {
		pt.LastError = protocol.Ptr(t.LastError)
	}
	return pt
}

// notebookTasksToProtocol converts a slice of runner records, skipping nil
// entries.
func notebookTasksToProtocol(ts []*tasks.Task) []protocol.NotebookTask {
	out := make([]protocol.NotebookTask, 0, len(ts))
	for _, t := range ts {
		if t == nil {
			continue
		}
		out = append(out, notebookTaskToProtocol(t))
	}
	return out
}

// sendNotebookTaskListWSResult lists the durable runner's records and replies to
// a websocket client with a notebook_task_list_result event correlated by
// requestID. A nil runner (disabled / not yet built) is a successful empty list,
// not an error. This WS path is the only task-list path; the former unix-socket
// CLI task-list command was removed.
func (d *Daemon) sendNotebookTaskListWSResult(client *wsClient, requestID string) {
	runner := d.compactRunnerRef()
	if runner == nil {
		d.sendToClient(client, protocol.NotebookTaskListResultMessage{
			Event:     protocol.EventNotebookTaskListResult,
			RequestID: requestID,
			Success:   true,
		})
		return
	}
	list, err := runner.List()
	msg := protocol.NotebookTaskListResultMessage{
		Event:     protocol.EventNotebookTaskListResult,
		RequestID: requestID,
		Success:   err == nil,
		Tasks:     notebookTasksToProtocol(list),
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendNotebookTaskRetryWSResult forces a failed/dead task back to queued and
// replies with a notebook_task_retry_result event correlated by requestID. The
// runner's OnChange callback fires broadcastNotebookTasksChanged automatically on
// a successful retry transition, so this handler does NOT broadcast itself.
func (d *Daemon) sendNotebookTaskRetryWSResult(client *wsClient, requestID, taskID string) {
	runner := d.compactRunnerRef()
	if runner == nil {
		d.sendToClient(client, protocol.NotebookTaskRetryResultMessage{
			Event:     protocol.EventNotebookTaskRetryResult,
			RequestID: requestID,
			Success:   false,
			Error:     protocol.Ptr("task runner unavailable"),
		})
		return
	}
	task, err := runner.Retry(taskID)
	msg := protocol.NotebookTaskRetryResultMessage{
		Event:     protocol.EventNotebookTaskRetryResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	} else if task != nil {
		pt := notebookTaskToProtocol(task)
		msg.Task = &pt
	}
	d.sendToClient(client, msg)
}

// broadcastNotebookTasksChanged announces that a task lifecycle transition
// occurred so an open task panel re-lists. It is wired to the runner's OnChange
// callback (see startCompactRunner). broadcastMessage -> wsHub.BroadcastValue is
// non-blocking (a full broadcast channel drops the message), so this is safe to
// invoke synchronously from the runner's single worker goroutine.
func (d *Daemon) broadcastNotebookTasksChanged() {
	d.broadcastMessage(protocol.NotebookTasksChangedMessage{
		Event: protocol.EventNotebookTasksChanged,
	})
}
