package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tasks"
)

// sqlTaskStore satisfies the tasks.Store seam.
var _ tasks.Store = (*sqlTaskStore)(nil)

// sqlTaskStore adapts the profile SQLite store to the tasks.Store seam so the
// durable task runner persists its records in ~/.attn[-profile]/attn.db instead of
// one JSON file per task under the notebook root. It lives in the daemon (which
// imports both internal/tasks and internal/store) so neither of those packages
// depends on the other. See docs/plans/2026-07-02-bg-task-notifications.md.
//
// The single-instance lock stays a file lock — relocated from the notebook tasks
// dir to the profile data dir (one runner per profile, matching one daemon per
// profile) — reusing the shared dir-lock helpers.
type sqlTaskStore struct {
	store   *store.Store
	lockDir string
	log     tasks.LogFunc
}

// newSQLTaskStore builds the adapter over the daemon's store, locking under the
// profile data dir.
func (d *Daemon) newSQLTaskStore() *sqlTaskStore {
	return &sqlTaskStore{store: d.store, lockDir: config.DataDir(), log: d.logf}
}

// Init is a no-op: migration {61} creates the tasks table when the DB opens.
func (a *sqlTaskStore) Init() error { return nil }

func (a *sqlTaskStore) AcquireLock() (string, error) { return tasks.AcquireDirLock(a.lockDir, a.log) }
func (a *sqlTaskStore) ReleaseLock(token string)     { tasks.ReleaseDirLock(token, a.log) }

func (a *sqlTaskStore) RecoverOrphans(now time.Time) (int, error) {
	return a.store.RecoverRunningTasks(now)
}

func (a *sqlTaskStore) Load(id string) (*tasks.Task, error) {
	rec, ok, err := a.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return recordToTask(*rec), nil
}

func (a *sqlTaskStore) Save(t *tasks.Task) error {
	return a.store.UpsertTask(taskToRecord(t))
}

func (a *sqlTaskStore) Delete(id string) error { return a.store.DeleteTask(id) }

func (a *sqlTaskStore) List() ([]*tasks.Task, error) {
	recs, err := a.store.ListTasks()
	if err != nil {
		return nil, err
	}
	out := make([]*tasks.Task, 0, len(recs))
	for _, rec := range recs {
		out = append(out, recordToTask(rec))
	}
	return out, nil
}

// taskToRecord maps a runner task to its store row. Meta is carried opaquely as a
// JSON blob; the store never interprets it. CommitGuard is per-run and never
// persisted.
func taskToRecord(t *tasks.Task) store.TaskRecord {
	meta := ""
	if len(t.Meta) > 0 {
		if b, err := json.Marshal(t.Meta); err == nil {
			meta = string(b)
		}
	}
	return store.TaskRecord{
		ID:            t.ID,
		Kind:          t.Kind,
		Subject:       t.Subject,
		State:         string(t.State),
		Attempts:      t.Attempts,
		NextAttemptAt: t.NextAttemptAt,
		LastError:     t.LastError,
		MetaJSON:      meta,
		Requeued:      t.Requeued,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
	}
}

func recordToTask(rec store.TaskRecord) *tasks.Task {
	var meta map[string]string
	if strings.TrimSpace(rec.MetaJSON) != "" {
		_ = json.Unmarshal([]byte(rec.MetaJSON), &meta)
	}
	return &tasks.Task{
		ID:            rec.ID,
		Kind:          rec.Kind,
		Subject:       rec.Subject,
		State:         tasks.State(rec.State),
		Attempts:      rec.Attempts,
		NextAttemptAt: rec.NextAttemptAt,
		LastError:     rec.LastError,
		Meta:          meta,
		Requeued:      rec.Requeued,
		CreatedAt:     rec.CreatedAt,
		UpdatedAt:     rec.UpdatedAt,
	}
}

// migrateLegacyTasksToSQLite imports any pre-existing on-disk JSON task records
// (the old <root>/.attn/tasks/*.json format) into the SQLite tasks table exactly
// once, then retires the directory so it never re-imports. Unparseable records are
// dropped — the file store's List already skips and logs them. Best-effort: a
// failure never blocks the runner from starting, and Save is an idempotent upsert
// so a partial run re-converges on the next boot.
func (d *Daemon) migrateLegacyTasksToSQLite(root string) {
	dir := filepath.Join(root, ".attn", "tasks")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return // nothing to migrate
	}
	legacy, err := tasks.NewFileStore(root, d.logf).List()
	if err != nil {
		d.logf("tasks migrate: list legacy records: %v", err)
		return
	}
	adapter := d.newSQLTaskStore()
	migrated := 0
	for _, t := range legacy {
		if err := adapter.Save(t); err != nil {
			d.logf("tasks migrate: import %s: %v", t.ID, err)
			continue
		}
		migrated++
	}
	// Retire the dir so we never re-import. Clear any stale prior .migrated first so
	// the rename can't fail on a leftover.
	retired := dir + ".migrated"
	_ = os.RemoveAll(retired)
	if err := os.Rename(dir, retired); err != nil {
		d.logf("tasks migrate: retire legacy dir: %v", err)
	}
	if migrated > 0 {
		d.logf("tasks migrate: imported %d legacy task(s) into sqlite", migrated)
	}
}
