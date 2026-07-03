package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tasks"
)

// taskToProtocol converts one durable task-runner record into the
// user-facing protocol type. Timestamps are emitted as RFC3339 (UTC); LastError
// becomes a pointer only when non-empty.
//
// SECURITY: Task.Meta carries internal inputs (e.g. transcript filesystem paths)
// and Task.CommitGuard is a live run latch — neither has a field on
// protocol.Task, so neither can leak to a client. Do not add them.
func taskToProtocol(t *tasks.Task) protocol.Task {
	pt := protocol.Task{
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

// tasksToProtocol converts a slice of runner records, skipping nil
// entries.
func tasksToProtocol(ts []*tasks.Task) []protocol.Task {
	out := make([]protocol.Task, 0, len(ts))
	for _, t := range ts {
		if t == nil {
			continue
		}
		out = append(out, taskToProtocol(t))
	}
	return out
}

// sendTaskListWSResult lists the durable runner's records and replies to
// a websocket client with a task_list_result event correlated by
// requestID. A nil runner (disabled / not yet built) is a successful empty list,
// not an error. This WS path is the only task-list path; the former unix-socket
// CLI task-list command was removed.
func (d *Daemon) sendTaskListWSResult(client *wsClient, requestID string) {
	runner := d.compactRunnerRef()
	if runner == nil {
		d.sendToClient(client, protocol.TaskListResultMessage{
			Event:     protocol.EventTaskListResult,
			RequestID: requestID,
			Success:   true,
		})
		return
	}
	list, err := runner.List()
	msg := protocol.TaskListResultMessage{
		Event:     protocol.EventTaskListResult,
		RequestID: requestID,
		Success:   err == nil,
		Tasks:     tasksToProtocol(list),
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendTaskRetryWSResult forces a failed/dead task back to queued and
// replies with a task_retry_result event correlated by requestID. The
// runner's OnChange callback fires broadcastTasksChanged automatically on
// a successful retry transition, so this handler does NOT broadcast itself.
func (d *Daemon) sendTaskRetryWSResult(client *wsClient, requestID, taskID string) {
	runner := d.compactRunnerRef()
	if runner == nil {
		d.sendToClient(client, protocol.TaskRetryResultMessage{
			Event:     protocol.EventTaskRetryResult,
			RequestID: requestID,
			Success:   false,
			Error:     protocol.Ptr("task runner unavailable"),
		})
		return
	}
	task, err := runner.Retry(taskID)
	msg := protocol.TaskRetryResultMessage{
		Event:     protocol.EventTaskRetryResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	} else if task != nil {
		pt := taskToProtocol(task)
		msg.Task = &pt
	}
	d.sendToClient(client, msg)
}

// broadcastTasksChanged announces that a task lifecycle transition
// occurred so an open task panel re-lists. It is wired to the runner's OnChange
// callback (see startCompactRunner). It builds a fresh message and does a
// non-blocking broadcastMessage -> wsHub.BroadcastValue (a full broadcast channel
// drops the message), holding no shared state, so it is safe to invoke
// CONCURRENTLY from the runner's dispatch goroutine and its in-flight runs.
func (d *Daemon) broadcastTasksChanged() {
	d.broadcastMessage(protocol.TasksChangedMessage{
		Event: protocol.EventTasksChanged,
	})
}
