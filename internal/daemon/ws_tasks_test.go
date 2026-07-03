package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tasks"
)

// testTaskKind is a fake executor kind tests enqueue against. Its behavior (succeed
// or fail) is controlled per-runner by the closure installInstrumentedTaskRunner
// hands back, so a single kind covers both the list and retry scenarios.
const testTaskKind = "test_task"

// installInstrumentedTaskRunner wires a started durable runner onto the daemon with
// one fake executor whose outcome the returned *atomic.Bool toggles (true = fail).
// MaxAttempts is 1 with a huge backoff so a single failure lands the task in dead
// and never auto-requeues, making the retry assertions deterministic. The runner's
// own tasks dir is a throwaway temp dir, and the OnChange broadcast wiring is
// attached exactly as startCompactRunner does so the wiring is under test too.
func installInstrumentedTaskRunner(t *testing.T, d *Daemon) (*tasks.Runner, *atomic.Bool) {
	t.Helper()
	shouldFail := &atomic.Bool{}
	runner := tasks.New(tasks.Options{
		Root:         filepath.Join(t.TempDir(), "tasks"),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
		MaxAttempts:  1,
		BackoffBase:  time.Hour,
		BackoffCap:   time.Hour,
	})
	if err := runner.Register(testTaskKind, func(context.Context, *tasks.Task) error {
		if shouldFail.Load() {
			return context.DeadlineExceeded
		}
		return nil
	}); err != nil {
		t.Fatalf("register %s: %v", testTaskKind, err)
	}
	runner.OnChange(func() { d.broadcastTasksChanged() })
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.compactRunner = runner
	return runner, shouldFail
}

// TestTaskToProtocolMapsFieldsAndOmitsMeta verifies the field mapping and,
// critically, that the internal Meta bag (transcript filesystem paths and other
// kind-specific inputs) never reaches the user-facing protocol type.
func TestTaskToProtocolMapsFieldsAndOmitsMeta(t *testing.T) {
	next := time.Date(2026, 6, 14, 9, 30, 0, 0, time.UTC)
	created := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 6, 14, 9, 15, 0, 0, time.UTC)
	secret := "/Users/victor/.claude/transcripts/SUPER-SECRET-PATH.jsonl"
	task := &tasks.Task{
		ID:            "test_task:ws-1",
		Kind:          testTaskKind,
		Subject:       "ws-1",
		State:         tasks.StateFailed,
		Attempts:      3,
		NextAttemptAt: next,
		LastError:     "boom",
		CreatedAt:     created,
		UpdatedAt:     updated,
		Meta:          map[string]string{"transcript_path": secret},
	}

	pt := taskToProtocol(task)

	if pt.ID != task.ID || pt.Kind != task.Kind || pt.Subject != task.Subject {
		t.Fatalf("identity fields = %+v, want id/kind/subject from %+v", pt, task)
	}
	if pt.State != string(tasks.StateFailed) {
		t.Fatalf("state = %q, want %q", pt.State, tasks.StateFailed)
	}
	if pt.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", pt.Attempts)
	}
	if pt.NextAttemptAt != next.Format(time.RFC3339) ||
		pt.CreatedAt != created.Format(time.RFC3339) ||
		pt.UpdatedAt != updated.Format(time.RFC3339) {
		t.Fatalf("timestamps = next=%q created=%q updated=%q, want RFC3339 of inputs",
			pt.NextAttemptAt, pt.CreatedAt, pt.UpdatedAt)
	}
	if pt.LastError == nil || *pt.LastError != "boom" {
		t.Fatalf("last_error = %v, want ptr to %q", pt.LastError, "boom")
	}

	// The protocol struct has no Meta field, so the only way it could leak is via
	// JSON. Serialize and assert the secret path is nowhere in the wire form.
	raw, err := json.Marshal(pt)
	if err != nil {
		t.Fatalf("marshal protocol task: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("protocol task JSON leaked Meta secret: %s", raw)
	}
	if strings.Contains(string(raw), "meta") {
		t.Fatalf("protocol task JSON contains a meta key: %s", raw)
	}

	// An empty LastError stays a nil pointer (omitted on the wire).
	task.LastError = ""
	if got := taskToProtocol(task); got.LastError != nil {
		t.Fatalf("empty last_error = %v, want nil pointer", got.LastError)
	}
}

// TestTasksToProtocolSkipsNil guards the nil-skip in the slice converter so
// a sparse runner list can never index-panic or emit a zero-value record.
func TestTasksToProtocolSkipsNil(t *testing.T) {
	in := []*tasks.Task{
		{ID: "a", Kind: testTaskKind, Subject: "a", State: tasks.StateQueued},
		nil,
		{ID: "b", Kind: testTaskKind, Subject: "b", State: tasks.StateDone},
	}
	out := tasksToProtocol(in)
	if len(out) != 2 || out[0].ID != "a" || out[1].ID != "b" {
		t.Fatalf("converted = %+v, want [a b] skipping nil", out)
	}
}

// TestSendTaskListWSResult exercises the websocket list path (the only
// list path after the unix-socket CLI was removed): a started runner's records
// come back as a task_list_result correlated by request, with each
// record's id/kind/state mapped through.
func TestSendTaskListWSResult(t *testing.T) {
	d := newNotebookDaemon(t)
	runner, _ := installInstrumentedTaskRunner(t, d)
	if _, err := runner.Enqueue(testTaskKind, "ws-a", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue ws-a: %v", err)
	}
	if _, err := runner.Enqueue(testTaskKind, "ws-b", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue ws-b: %v", err)
	}
	// Let the worker run both to a terminal state so the records are stable.
	waitForTaskState(t, d, testTaskKind, "ws-a", tasks.StateDone)
	waitForTaskState(t, d, testTaskKind, "ws-b", tasks.StateDone)

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendTaskListWSResult(client, "list-1")

	var msg protocol.TaskListResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	if msg.Event != protocol.EventTaskListResult || msg.RequestID != "list-1" || !msg.Success {
		t.Fatalf("list result = %+v, want success task_list_result for list-1", msg)
	}
	got := map[string]protocol.Task{}
	for _, task := range msg.Tasks {
		got[task.Subject] = task
	}
	if len(got) != 2 {
		t.Fatalf("listed %d tasks, want 2: %+v", len(msg.Tasks), msg.Tasks)
	}
	for _, subject := range []string{"ws-a", "ws-b"} {
		task, ok := got[subject]
		if !ok {
			t.Fatalf("subject %q missing from list: %+v", subject, msg.Tasks)
		}
		if task.Kind != testTaskKind || task.State != string(tasks.StateDone) {
			t.Fatalf("task %q = %+v, want kind=%s state=done", subject, task, testTaskKind)
		}
		if task.ID != tasks.TaskID(testTaskKind, subject) {
			t.Fatalf("task %q id = %q, want %q", subject, task.ID, tasks.TaskID(testTaskKind, subject))
		}
	}
}

// TestSendTaskListWSResultNilRunner confirms a nil runner is a successful
// empty WS result, not a transport error.
func TestSendTaskListWSResultNilRunner(t *testing.T) {
	d := newNotebookDaemon(t)
	d.compactRunner = nil

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendTaskListWSResult(client, "list-nil")

	var msg protocol.TaskListResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	if !msg.Success || msg.RequestID != "list-nil" || len(msg.Tasks) != 0 || msg.Error != nil {
		t.Fatalf("nil-runner list result = %+v, want success empty list", msg)
	}
}

// TestSendTaskRetryWSResultRequeuesDeadTask drives a real task to dead
// (MaxAttempts=1 + a failing executor) then retries it over the WS path: the result
// must carry the task flipped back to queued with NextAttemptAt advanced to ~now.
func TestSendTaskRetryWSResultRequeuesDeadTask(t *testing.T) {
	d := newNotebookDaemon(t)
	runner, shouldFail := installInstrumentedTaskRunner(t, d)
	shouldFail.Store(true)

	if _, err := runner.Enqueue(testTaskKind, "ws-fail", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue ws-fail: %v", err)
	}
	dead := waitForTaskState(t, d, testTaskKind, "ws-fail", tasks.StateDead)
	if dead.LastError == "" {
		t.Fatalf("dead task has no last_error: %+v", dead)
	}

	before := time.Now()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendTaskRetryWSResult(client, "retry-1", tasks.TaskID(testTaskKind, "ws-fail"))

	var msg protocol.TaskRetryResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	if msg.Event != protocol.EventTaskRetryResult || msg.RequestID != "retry-1" || !msg.Success {
		t.Fatalf("retry result = %+v, want success task_retry_result for retry-1", msg)
	}
	if msg.Task == nil || msg.Task.State != string(tasks.StateQueued) {
		t.Fatalf("retry result task = %+v, want state queued", msg.Task)
	}
	if msg.Task.Attempts != 0 {
		t.Fatalf("retry result attempts = %d, want 0 (reset)", msg.Task.Attempts)
	}
	nextAt, err := time.Parse(time.RFC3339, msg.Task.NextAttemptAt)
	if err != nil {
		t.Fatalf("parse next_attempt_at %q: %v", msg.Task.NextAttemptAt, err)
	}
	// Retry sets NextAttemptAt = now; allow a small skew on either side of the call.
	if nextAt.Before(before.Add(-2*time.Second)) || nextAt.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("next_attempt_at = %s, want ~now (between %s and %s)", nextAt, before, time.Now())
	}
}

// TestSendTaskRetryWSResultNilRunner confirms the disabled-runner retry path
// is a clear, non-panicking failure result rather than a silent success.
func TestSendTaskRetryWSResultNilRunner(t *testing.T) {
	d := newNotebookDaemon(t)
	d.compactRunner = nil

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendTaskRetryWSResult(client, "retry-nil", "test_task:gone")

	var msg protocol.TaskRetryResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	if msg.Success || msg.Error == nil || *msg.Error != "task runner unavailable" {
		t.Fatalf("nil-runner retry result = %+v, want failure 'task runner unavailable'", msg)
	}
}

// TestTasksChangedBroadcastReachesClient drives a REAL task transition
// through the daemon-wired runner and asserts the live tasks_changed
// broadcast actually lands on a subscribed websocket client with the correct event
// name. This covers the end-to-end path startCompactRunner relies on (runner.OnChange
// -> d.broadcastTasksChanged -> wsHub), which the per-handler result tests
// cannot see: a renamed event or a broken broadcast message would slip past them but
// is caught here. (That the runner invokes OnChange at all is a tasks-package property
// already covered by internal/tasks; this test owns the daemon's wiring of it.)
func TestTasksChangedBroadcastReachesClient(t *testing.T) {
	d := newNotebookDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	// installInstrumentedTaskRunner wires runner.OnChange -> broadcastTasksChanged
	// exactly as startCompactRunner does, so a real transition exercises the live path.
	runner, _ := installInstrumentedTaskRunner(t, d)
	if _, err := runner.Enqueue(testTaskKind, "ws-bcast", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The enqueue and the worker's queued->running->done each fire the broadcast.
	// Assert at least one tasks_changed reaches the client.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case message := <-client.send:
			var event protocol.TasksChangedMessage
			if err := json.Unmarshal(message.payload, &event); err != nil {
				t.Fatalf("decode broadcast: %v", err)
			}
			if event.Event != protocol.EventTasksChanged {
				t.Fatalf("broadcast event = %q, want tasks_changed", event.Event)
			}
			return
		case <-deadline:
			t.Fatal("tasks_changed was not broadcast on a task transition")
		}
	}
}
