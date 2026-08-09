package jobs

import (
	"sort"
	"sync"
	"time"
)

// memStore is an in-memory Store for this package's tests. It exists only so the
// runner's behavior can be exercised without a database; the daemon injects the
// SQLite-backed store in production. It holds its own mutex because a test may
// read it from the test goroutine while the dispatch loop writes.
type memStore struct {
	mu     sync.Mutex
	jobs   map[string]*Job
	locked bool

	// saveErr, when set, makes the next Save fail. Tests use it to drive the
	// claim-rollback path.
	saveErr error
}

func newMemStore() *memStore { return &memStore{jobs: make(map[string]*Job)} }

func (m *memStore) Init() error { return nil }

func (m *memStore) AcquireLock() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locked {
		return "", ErrAlreadyRunning
	}
	m.locked = true
	return "mem", nil
}

func (m *memStore) ReleaseLock(string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locked = false
}

func (m *memStore) RecoverOrphans(now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	recovered := 0
	for _, j := range m.jobs {
		if j.State != StateRunning {
			continue
		}
		j.State = StateQueued
		j.ScheduledAt = now
		j.UpdatedAt = now
		recovered++
	}
	return recovered, nil
}

func (m *memStore) Load(id string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id].clone(), nil
}

func (m *memStore) LoadByKey(kind, uniqueKey string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Kind == kind && j.UniqueKey == uniqueKey {
			return j.clone(), nil
		}
	}
	return nil, nil
}

func (m *memStore) Save(j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		err := m.saveErr
		m.saveErr = nil
		return err
	}
	if j.Requeued && j.State == StateRunning {
		// The flag is only set on a record whose run is still in flight; seeing it
		// persisted that way is the collision itself, not its aftermath.
		sawTriggerLandOnARunningJob.Reached()
	}
	m.jobs[j.ID] = j.clone()
	return nil
}

func (m *memStore) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, id)
	return nil
}

func (m *memStore) List() ([]*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j.clone())
	}
	return out, nil
}

func (m *memStore) Eligible(now time.Time, limit int) ([]*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.State != StateQueued && j.State != StateFailed {
			continue
		}
		if now.Before(j.ScheduledAt) {
			// The record is in a runnable state and dispatch is asking; the only
			// reason it is not going out is the clock.
			sawJobWithheldByItsSchedule.Reached()
			continue
		}
		out = append(out, j.clone())
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Priority != out[b].Priority {
			return out[a].Priority > out[b].Priority
		}
		if !out[a].ScheduledAt.Equal(out[b].ScheduledAt) {
			return out[a].ScheduledAt.Before(out[b].ScheduledAt)
		}
		return out[a].CreatedAt.Before(out[b].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memStore) TrimDone(cutoff time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	trimmed := 0
	for id, j := range m.jobs {
		if j.State == StateDone && j.UpdatedAt.Before(cutoff) {
			delete(m.jobs, id)
			trimmed++
		}
	}
	return trimmed, nil
}

// count returns how many records the store holds.
func (m *memStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.jobs)
}

// failNextSave arms a one-shot Save failure.
func (m *memStore) failNextSave(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveErr = err
}

// fakeClock is a manually advanced clock so backoff and debounce windows are
// tested by moving time rather than by sleeping through it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
