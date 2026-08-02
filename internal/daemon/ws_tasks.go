package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

// taskToProtocol converts one durable job record into the user-facing protocol
// type. Timestamps are emitted as RFC3339 (UTC); LastError becomes a pointer
// only when non-empty. Subject is the job's coalescing key, which for every kind
// the daemon queues is the entity the run acts on (see jobSubject).
//
// SECURITY: Job.Payload and Job.Result carry internal inputs and outputs (e.g.
// transcript filesystem paths) and Job.CommitGuard is a live run latch — none
// has a field on protocol.Task, so none can leak to a client. Do not add them.
func taskToProtocol(t *jobs.Job) protocol.Task {
	pt := protocol.Task{
		ID:            t.ID,
		Kind:          t.Kind,
		Subject:       jobSubject(t),
		State:         string(t.State),
		Attempts:      t.Attempts,
		NextAttemptAt: t.ScheduledAt.UTC().Format(time.RFC3339),
		CreatedAt:     t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.LastError != "" {
		pt.LastError = protocol.Ptr(t.LastError)
	}
	return pt
}

// tasksToProtocol converts a slice of queue records, skipping nil entries.
func tasksToProtocol(ts []*jobs.Job) []protocol.Task {
	out := make([]protocol.Task, 0, len(ts))
	for _, t := range ts {
		if t == nil {
			continue
		}
		out = append(out, taskToProtocol(t))
	}
	return out
}

// sendTaskListWSResult lists the durable queue's records and replies to
// a websocket client with a task_list_result event correlated by
// requestID. A nil runner (disabled / not yet built) is a successful empty list,
// not an error. This WS path is the only task-list path; the former unix-socket
// CLI task-list command was removed.
func (d *Daemon) sendTaskListWSResult(client *wsClient, requestID string) {
	runner := d.jobQueueRef()
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
	runner := d.jobQueueRef()
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

// projectTasksChanged re-pushes the "something in the task queue moved" ping an
// open task panel re-lists on. It runs from the runner's OnChange callback,
// which may fire CONCURRENTLY from the dispatch goroutine and from each
// in-flight run; the push itself holds no shared state and drops on a full
// broadcast channel, so it can never stall a run.
func (d *Daemon) projectTasksChanged() {
	d.projectSnapshot(snapshotTasks, func() {
		d.broadcastMessage(protocol.TasksChangedMessage{
			Event: protocol.EventTasksChanged,
		})
	})
}
