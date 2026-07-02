package tasks

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This is the FILE-backed implementation of the Store seam (see FileStore below):
// one atomic-JSON file per task under <root>/.attn/tasks/, with the "queue" being
// os.ReadDir filtered by State. Production now injects a SQLite-backed Store from
// the daemon instead (docs/plans/2026-07-02-bg-task-notifications.md); this file
// impl backs the package's own tests and the daemon's one-time JSON->SQLite import
// of any pre-existing on-disk records. The single worker serializes all writes, so
// no per-task lock is needed; the only durability guarantee this store owns is
// atomic temp+rename so a crash mid-write never leaves a half-written record.

const (
	machineDir = ".attn"
	tasksDir   = "tasks"
)

// stateDir returns the absolute .attn/tasks directory for a notebook root.
func stateDir(root string) string {
	return filepath.Join(root, machineDir, tasksDir)
}

// trimRoot normalizes a configured notebook root; an empty/whitespace root means
// "no notebook configured" and disables the runner.
func trimRoot(root string) string {
	return strings.TrimSpace(root)
}

// taskPath returns the absolute JSON path for a task id.
func taskPath(root, id string) string {
	return filepath.Join(stateDir(root), taskFilename(id)+".json")
}

// taskFilename encodes a task id as a single, collision-free filename component.
// The id is hex-encoded rather than character-replaced: the old "replace every
// unsafe rune with _" scheme was LOSSY, so distinct ids that differed only in a
// replaced rune (e.g. "k:a/b" vs "k:a_b", or "k:a:b" vs "k:a..b") collapsed onto
// the same file and silently clobbered each other's record. Hex is injective and
// emits only [0-9a-f], so it can never contain a path separator or ".." and is
// safe on macOS's case-insensitive default filesystem (no two ids can produce
// names that differ only by case). The name is opaque, but nothing reads the id
// back out of it — the human-readable id lives in the JSON body.
func taskFilename(id string) string {
	return hex.EncodeToString([]byte(id))
}

// store is the file-backed persistence layer for tasks. It holds no in-memory
// state of its own; the runner is the single owner of liveness, so the store is a
// pure read/write/list surface over the tasks dir.
type store struct {
	root string
	log  LogFunc
}

func newStore(root string) *store {
	return &store{root: root, log: func(string, ...interface{}) {}}
}

// init creates the tasks dir (MkdirAll-on-init).
func (s *store) init() error {
	return os.MkdirAll(stateDir(s.root), 0o755)
}

// save writes a task atomically (temp+rename). Same helper shape as
// notebook.writeAtomic.
func (s *store) save(t *Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(taskPath(s.root, t.ID), data)
}

// load reads a single task by id. A missing file yields (nil, nil) — a coalesced
// re-enqueue must distinguish "no record yet" from a read error.
func (s *store) load(id string) (*Task, error) {
	t, err := s.loadPath(taskPath(s.root, id))
	if err != nil {
		return nil, err
	}
	if t != nil && t.ID != id {
		// Defense in depth: taskFilename is an injective encoding of the id, so a
		// record whose stored ID differs from the one requested should be
		// impossible. If it ever happens (a hand-edited file, a future encoding
		// change, a filesystem that folds the name), treat it as "no record"
		// rather than return the wrong task — a wrong-task return would corrupt
		// the coalescing/retry state keyed on that id.
		s.log("tasks: ignoring record %s: stored id %q != requested %q",
			filepath.Base(taskPath(s.root, id)), t.ID, id)
		return nil, nil
	}
	return t, nil
}

func (s *store) loadPath(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("tasks: parse %s: %w", filepath.Base(path), err)
	}
	return &t, nil
}

// delete removes a task's record file. A missing file is not an error (the
// record is already gone). Used by Runner.Remove to forget a task whose subject
// no longer exists (e.g. a removed workspace), so its record does not leak.
func (s *store) delete(id string) error {
	err := os.Remove(taskPath(s.root, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// list returns every persisted task, newest-updated first. A missing tasks dir is
// not an error — it means nothing has been enqueued yet.
func (s *store) list() ([]*Task, error) {
	dir := stateDir(s.root)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t, err := s.loadPath(filepath.Join(dir, e.Name()))
		if err != nil {
			// A single corrupt/undecodable record must not blind the whole queue: a
			// power-loss partial file, a hand-edit, an incompatible schema, or any
			// stray non-task .json would otherwise wedge EVERY task forever (runNext
			// propagates the error and the worker never selects any eligible task).
			// One-file-per-task exists precisely to isolate a bad file, so skip it and
			// keep going. The store logs the skip so the bad record still surfaces.
			s.log("tasks: skipping undecodable record %s: %v", e.Name(), err)
			continue
		}
		if t == nil {
			continue
		}
		// Defense symmetric with load(): the store addresses a record ONLY by its
		// canonical taskFilename(id) name, so a task file named anything else is
		// unreachable — save/delete/load by id all target taskFilename(id), never
		// this file. The dangerous case is a leftover from the old lossy
		// rune-replacement naming (pre-hex): list() would return it, runNext would
		// claim it and persist the claim to the CANONICAL file, leaving the
		// orphan's own file untouched and still queued — so it is re-selected every
		// cycle. Because its NextAttemptAt is in the past it wins selection forever,
		// a hot loop that also starves the real, canonically-named tasks. Skip it.
		if e.Name() != taskFilename(t.ID)+".json" {
			s.log("tasks: ignoring non-canonical record %s (stored id %q)", e.Name(), t.ID)
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// recoverOrphans resets any task left in StateRunning back to StateQueued: a
// running record at startup means a crash interrupted that task mid-run, so it is
// re-eligible (reset, don't drop). NextAttemptAt is pulled forward to now so
// recovery is immediate.
func (s *store) recoverOrphans(now time.Time) (int, error) {
	all, err := s.list()
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, t := range all {
		if t.State != StateRunning {
			continue
		}
		t.State = StateQueued
		t.NextAttemptAt = now
		t.UpdatedAt = now
		if err := s.save(t); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

// Store is the persistence + single-instance-lock seam the Runner sits on. The
// file-backed FileStore below is the default and backs this package's own tests;
// the daemon injects a SQLite-backed implementation via Options.Store so tasks
// persist in the profile DB (not under the notebook root). Keeping the seam here
// (not importing internal/store) preserves this package as a leaf.
type Store interface {
	// Init prepares the store (the file store creates its tasks dir; a DB store is
	// a no-op — migrations create the table).
	Init() error
	// AcquireLock takes exclusive single-instance ownership, returning an opaque
	// token for ReleaseLock. Returns ErrAlreadyRunning if another live process
	// already holds it.
	AcquireLock() (string, error)
	// ReleaseLock releases a token from AcquireLock. Best-effort; never blocks Stop.
	ReleaseLock(token string)
	// RecoverOrphans resets any task left in StateRunning back to StateQueued
	// (NextAttemptAt = now) and returns how many were recovered.
	RecoverOrphans(now time.Time) (int, error)
	// Load returns the record for id, or (nil, nil) when there is none.
	Load(id string) (*Task, error)
	// Save persists a record (create or overwrite by id).
	Save(t *Task) error
	// Delete removes a record by id; a missing record is not an error.
	Delete(id string) error
	// List returns every persisted record.
	List() ([]*Task, error)
}

// FileStore is the file-backed Store: one atomic-JSON file per task under
// <root>/.attn/tasks/. It wraps this package's internal file store so both the
// Runner and the daemon's one-time JSON->SQLite import reach it through the Store
// interface.
type FileStore struct{ s *store }

// NewFileStore builds a file-backed Store rooted at root. A nil log is a no-op.
func NewFileStore(root string, log LogFunc) *FileStore {
	s := newStore(root)
	if log != nil {
		s.log = log
	}
	return &FileStore{s: s}
}

func (f *FileStore) Init() error                  { return f.s.init() }
func (f *FileStore) AcquireLock() (string, error) { return f.s.acquireLock() }
func (f *FileStore) ReleaseLock(token string)     { f.s.releaseLock(token) }
func (f *FileStore) RecoverOrphans(now time.Time) (int, error) {
	return f.s.recoverOrphans(now)
}
func (f *FileStore) Load(id string) (*Task, error) { return f.s.load(id) }
func (f *FileStore) Save(t *Task) error            { return f.s.save(t) }
func (f *FileStore) Delete(id string) error        { return f.s.delete(id) }
func (f *FileStore) List() ([]*Task, error)        { return f.s.list() }

// writeAtomic mirrors notebook.writeAtomic: write to a unique temp file, then
// rename over the target so a reader never observes a half-written record.
func writeAtomic(absPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", absPath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, absPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
