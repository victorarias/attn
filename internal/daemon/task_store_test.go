package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tasks"
)

// newTestSQLTaskStore builds the adapter over an in-memory store with a hermetic
// lock dir (t.TempDir) so tests never touch the real profile data dir.
func newTestSQLTaskStore(t *testing.T) (*store.Store, *sqlTaskStore) {
	t.Helper()
	st := store.New()
	return st, &sqlTaskStore{store: st, lockDir: t.TempDir(), log: func(string, ...interface{}) {}}
}

// TestSQLTaskStore_RunnerEndToEnd drives a real Runner backed by the SQLite
// adapter: a registered executor runs the enqueued task to completion and the
// record is durably reflected in the DB.
func TestSQLTaskStore_RunnerEndToEnd(t *testing.T) {
	st, adapter := newTestSQLTaskStore(t)
	runner := tasks.New(tasks.Options{
		Store:        adapter,
		PollInterval: 2 * time.Millisecond,
		Log:          func(string, ...interface{}) {},
	})
	ran := make(chan string, 1)
	if err := runner.Register("compact_context", func(_ context.Context, task *tasks.Task) error {
		ran <- task.Subject
		return nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer runner.Stop()

	if _, err := runner.Enqueue("compact_context", "ws-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case got := <-ran:
		if got != "ws-1" {
			t.Fatalf("executor ran for %q, want ws-1", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("executor never ran")
	}

	// The terminal record is persisted in the DB, addressable by its runner id.
	id := tasks.TaskID("compact_context", "ws-1")
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, ok, err := st.GetTask(id)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if ok && rec.State == string(tasks.StateDone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not reach done in the DB (ok=%v rec=%+v)", ok, rec)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestSQLTaskStore_MetaRoundTrip checks the Task<->TaskRecord mapping preserves
// the kind-specific Meta bag through a Save/Load cycle.
func TestSQLTaskStore_MetaRoundTrip(t *testing.T) {
	_, adapter := newTestSQLTaskStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	orig := &tasks.Task{
		ID:            "summarize_session:s-1",
		Kind:          "summarize_session",
		Subject:       "s-1",
		State:         tasks.StateFailed,
		Attempts:      2,
		NextAttemptAt: now.Add(time.Minute),
		LastError:     "agent run failed",
		Meta:          map[string]string{"transcript": "/tmp/x.jsonl", "workspace": "ws-9"},
		Requeued:      true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := adapter.Save(orig); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := adapter.Load(orig.ID)
	if err != nil || got == nil {
		t.Fatalf("load: got=%v err=%v", got, err)
	}
	if got.Kind != orig.Kind || got.State != orig.State || got.Attempts != orig.Attempts ||
		got.LastError != orig.LastError || !got.Requeued {
		t.Fatalf("scalar mismatch: %+v", got)
	}
	if len(got.Meta) != 2 || got.Meta["transcript"] != "/tmp/x.jsonl" || got.Meta["workspace"] != "ws-9" {
		t.Fatalf("meta not preserved: %+v", got.Meta)
	}
	if !got.NextAttemptAt.Equal(orig.NextAttemptAt) {
		t.Fatalf("next_attempt_at: got %v want %v", got.NextAttemptAt, orig.NextAttemptAt)
	}

	// Load of a missing id is (nil, nil), not an error — the coalesced re-enqueue
	// contract.
	miss, err := adapter.Load("nope:x")
	if err != nil || miss != nil {
		t.Fatalf("load miss: got=%v err=%v", miss, err)
	}
}

// TestMigrateLegacyTasksToSQLite imports the pre-SQLite on-disk JSON records into
// the DB exactly once, drops an unparseable file, and retires the directory.
func TestMigrateLegacyTasksToSQLite(t *testing.T) {
	st := store.New()
	d := &Daemon{store: st}
	root := t.TempDir()

	// Seed two legacy records via the file store (the real on-disk format).
	fs := tasks.NewFileStore(root, nil)
	for _, task := range []*tasks.Task{
		{ID: "compact_context:ws-a", Kind: "compact_context", Subject: "ws-a", State: tasks.StateQueued},
		{ID: "narrate_workspace:ws-b", Kind: "narrate_workspace", Subject: "ws-b", State: tasks.StateDead, LastError: "boom"},
	} {
		if err := fs.Save(task); err != nil {
			t.Fatalf("seed legacy %s: %v", task.ID, err)
		}
	}
	// An unparseable file must be dropped, not fatal.
	legacyDir := filepath.Join(root, ".attn", "tasks")
	if err := os.WriteFile(filepath.Join(legacyDir, "corrupt.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.migrateLegacyTasksToSQLite(root)

	all, err := st.ListTasks()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 imported tasks, got %d (%+v)", len(all), all)
	}
	if _, ok, _ := st.GetTask("narrate_workspace:ws-b"); !ok {
		t.Fatalf("dead task not imported")
	}
	// The legacy dir is retired so a re-run does not re-import.
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy dir not retired (stat err=%v)", err)
	}
	if _, err := os.Stat(legacyDir + ".migrated"); err != nil {
		t.Fatalf("expected .migrated dir: %v", err)
	}

	// Idempotent: a second run with no dir is a no-op and does not error/duplicate.
	d.migrateLegacyTasksToSQLite(root)
	all2, _ := st.ListTasks()
	if len(all2) != 2 {
		t.Fatalf("re-run changed task count: %d", len(all2))
	}
}
