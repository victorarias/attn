package notebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
//	state.json       — run bookkeeping: the schedule anchor + last-dispatch time
//	runs/            — reserved for the promote phase's dated run reports
//
// All writes reuse the package's atomic temp+rename writer so a crash mid-write
// never leaves a half-written state file.

const (
	dreamsDir            = "dreams"
	dreamsCandidatesFile = "candidates.json"
	dreamsStateFile      = "state.json"

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
// candidate list and the tiny run metadata are written independently. The cron
// enqueuer (enqueueDueDreamHarvest) is its SOLE writer: the harvest executor that
// runs async on the durable runner does NOT touch this file, so there is no
// two-writer race on state.json.
type DreamRunState struct {
	Version int `json:"version"`
	// ScheduledFrom is the anchor the next run is computed from (RFC3339, UTC):
	// set to "now" on the first enable (so the first run lands at the next
	// scheduled slot, not at daemon startup) and advanced to the dispatch time
	// after each due fire. A fire is due when schedule.Next(ScheduledFrom) has
	// passed; because the anchor jumps forward to the dispatch time, slots missed
	// while the daemon was down collapse into a single catch-up enqueue. See
	// enqueueDueDreamHarvest for the full catch-up semantics (and its two
	// deliberate wall-clock nuances).
	ScheduledFrom string `json:"scheduled_from,omitempty"`
	// LastRunAt is when the daily harvest was last DISPATCHED by the cron enqueuer
	// (the harvest itself runs async on the durable runner moments later) —
	// RFC3339, UTC; display.
	LastRunAt string `json:"last_run_at,omitempty"`
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
