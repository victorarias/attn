package notebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Dreaming machine state.
//
// The dreaming pass persists its working state under <root>/.attn/dreams/ as
// plain JSON — self-describing and git-diffable. This is MACHINE state, not a
// notebook document: it lives in the .attn dotdir (skipped by List and by any
// dotfile-aware external sync scanner) and never goes through Store.Write, whose
// CleanPath rejects dotdir and non-.md paths. The state files are:
//
//	candidates.json  — the accumulated harvest candidate set (grows monotonically;
//	                   harvest only ever merges, never prunes — compaction is a
//	                   separate, deferred op)
//	state.json       — run bookkeeping: the schedule anchor + last-run summary
//	locks/dream.lock — single-writer lock held for the duration of a run
//	runs/            — reserved for the promote phase's dated run reports
//
// All writes reuse the package's atomic temp+rename writer so a crash mid-write
// never leaves a half-written state file.

const (
	dreamsDir            = "dreams"
	dreamsCandidatesFile = "candidates.json"
	dreamsStateFile      = "state.json"
	dreamsLocksDir       = "locks"
	dreamsLockFile       = "dream.lock"

	// dreamStateVersion tags persisted state so a future format change can be
	// detected and migrated rather than silently mis-read.
	dreamStateVersion = 1
)

// DreamsStateDir returns the absolute .attn/dreams directory for a notebook root.
func DreamsStateDir(root string) string {
	return filepath.Join(root, machineDir, dreamsDir)
}

// DreamRunState is the persisted run bookkeeping for the dreaming pass. It is
// deliberately small and separate from candidates.json so the (potentially large)
// candidate list and the tiny run metadata are written independently.
type DreamRunState struct {
	Version int `json:"version"`
	// ScheduledFrom is the anchor the next run is computed from (RFC3339, UTC):
	// set to "now" on the first enable (so the first run lands at the next
	// scheduled slot, not at daemon startup) and advanced to the run time after
	// each completed run. A run is due when schedule.Next(ScheduledFrom) has passed;
	// because the anchor jumps forward to the run time, slots missed while the
	// daemon was down collapse into a single catch-up run. See dreamSchedulerTick
	// for the full catch-up semantics (and its two deliberate wall-clock nuances).
	ScheduledFrom string `json:"scheduled_from,omitempty"`
	// LastRunAt is when the last harvest run completed (RFC3339, UTC; display).
	LastRunAt string `json:"last_run_at,omitempty"`
	// LastRunNewCandidates is how many candidates that run added that were not
	// already persisted.
	LastRunNewCandidates int `json:"last_run_new_candidates"`
	// LastRunCandidateCount is the total persisted candidate count after that run.
	LastRunCandidateCount int `json:"last_run_candidate_count"`
}

// LoadDreamCandidates reads the persisted candidate set. A missing file is not an
// error — it means no run has persisted candidates yet — and yields an empty set.
func LoadDreamCandidates(root string) ([]DreamCandidate, error) {
	path := filepath.Join(DreamsStateDir(root), dreamsCandidatesFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cands []DreamCandidate
	if err := json.Unmarshal(data, &cands); err != nil {
		return nil, fmt.Errorf("notebook: parse %s: %w", dreamsCandidatesFile, err)
	}
	return cands, nil
}

// SaveDreamCandidates writes the candidate set atomically.
func SaveDreamCandidates(root string, cands []DreamCandidate) error {
	if cands == nil {
		cands = []DreamCandidate{}
	}
	data, err := json.MarshalIndent(cands, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(DreamsStateDir(root), dreamsCandidatesFile), data)
}

// LoadDreamRunState reads the run bookkeeping. A missing file yields the zero
// value (no run has happened yet), not an error.
func LoadDreamRunState(root string) (DreamRunState, error) {
	path := filepath.Join(DreamsStateDir(root), dreamsStateFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DreamRunState{}, nil
	}
	if err != nil {
		return DreamRunState{}, err
	}
	var state DreamRunState
	if err := json.Unmarshal(data, &state); err != nil {
		return DreamRunState{}, fmt.Errorf("notebook: parse %s: %w", dreamsStateFile, err)
	}
	return state, nil
}

// SaveDreamRunState writes the run bookkeeping atomically, stamping the current
// format version.
func SaveDreamRunState(root string, state DreamRunState) error {
	state.Version = dreamStateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(DreamsStateDir(root), dreamsStateFile), data)
}

// dreamLockInfo records who holds the dream lock, for crash auditability.
type dreamLockInfo struct {
	PID      int    `json:"pid"`
	Acquired string `json:"acquired"`
}

// AcquireDreamLock takes the single-writer dream lock by creating
// locks/dream.lock exclusively. It returns a release func that removes the lock;
// release is idempotent. If the lock already exists the call fails — the caller
// must treat that as "a run is already in progress" (startup orphan recovery
// clears a lock left behind by a crashed run).
func AcquireDreamLock(root string) (release func() error, err error) {
	dir := filepath.Join(DreamsStateDir(root), dreamsLocksDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, dreamsLockFile)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("notebook: dream lock already held at %s", lockPath)
		}
		return nil, err
	}
	info, _ := json.Marshal(dreamLockInfo{PID: os.Getpid(), Acquired: time.Now().UTC().Format(time.RFC3339)})
	_, _ = f.Write(info)
	_ = f.Close()

	released := false
	return func() error {
		if released {
			return nil
		}
		released = true
		return os.Remove(lockPath)
	}, nil
}

// ClearOrphanDreamLocks removes any lock files left under locks/. For every
// supported configuration this is safe: the daemon is a process singleton (the PID
// lock kills any prior daemon on startup) and the notebook root is per-profile (the
// default root is profile-namespaced), so a lock present at startup is necessarily
// orphaned by a crashed run. The one way to defeat this is unsupported — pointing
// two profiles' daemons at one EXPLICIT notebook.root, where startup recovery could
// clear the other daemon's live lock. The lock records its holder PID for auditing
// if that ever needs hardening into a liveness check. Returns how many lock files
// were removed; a missing locks dir is not an error.
func ClearOrphanDreamLocks(root string) (int, error) {
	dir := filepath.Join(DreamsStateDir(root), dreamsLocksDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cleared := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			cleared++
		}
	}
	return cleared, nil
}
