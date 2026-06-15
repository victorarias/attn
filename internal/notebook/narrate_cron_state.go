package notebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Daily-narrate cron machine state.
//
// The daily per-workspace narrate pass persists its schedule anchor under
// <root>/.attn/narrate/ as plain JSON — self-describing and git-diffable. This is
// MACHINE state, not a notebook document: it lives in the .attn dotdir (skipped by
// List and by any dotfile-aware external sync scanner) and never goes through
// Store.Write, whose CleanPath rejects dotdir and non-.md paths.
//
// It is deliberately SEPARATE from DreamRunState (.attn/dreams/state.json) so the
// daily narrate's schedule anchor never couples to the dreaming-enabled gate or the
// harvest's run bookkeeping. The two share only the nightly cadence/timezone
// SETTINGS (a deliberate "notebook-maintenance slot"), not a state file.
//
// The write reuses the package's atomic temp+rename writer so a crash mid-write
// never leaves a half-written state file.

const (
	narrateDir       = "narrate"
	narrateStateFile = "state.json"

	// narrateCronStateVersion tags persisted state so a future format change can be
	// detected and migrated rather than silently mis-read.
	narrateCronStateVersion = 1
)

// NarrateCronStateDir returns the absolute .attn/narrate directory for a notebook
// root.
func NarrateCronStateDir(root string) string {
	return filepath.Join(root, machineDir, narrateDir)
}

// NarrateCronState is the persisted schedule anchor for the daily per-workspace
// narrate cron. It is deliberately tiny and separate from DreamRunState: the cron
// enqueuer (enqueueDueDailyNarrates) is its SOLE writer, so there is no two-writer
// race on state.json.
type NarrateCronState struct {
	Version int `json:"version"`
	// ScheduledFrom is the anchor the next daily-narrate pass is computed from
	// (RFC3339, UTC): set to "now" on the first observation (so the first pass
	// lands at the next scheduled slot, not at daemon startup) and advanced to the
	// dispatch time after each due fire. A fire is due when
	// schedule.Next(ScheduledFrom) has passed; because the anchor jumps forward to
	// the dispatch time, slots missed while the daemon was down collapse into a
	// single catch-up. See enqueueDueDailyNarrates for the full semantics.
	ScheduledFrom string `json:"scheduled_from,omitempty"`
}

// LoadNarrateCronState reads the persisted schedule anchor. A missing file yields
// the zero value (no pass has been anchored yet), not an error.
func LoadNarrateCronState(root string) (NarrateCronState, error) {
	path := filepath.Join(NarrateCronStateDir(root), narrateStateFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NarrateCronState{}, nil
	}
	if err != nil {
		return NarrateCronState{}, err
	}
	var state NarrateCronState
	if err := json.Unmarshal(data, &state); err != nil {
		return NarrateCronState{}, fmt.Errorf("notebook: parse %s: %w", narrateStateFile, err)
	}
	return state, nil
}

// SaveNarrateCronState writes the schedule anchor atomically, stamping the current
// format version.
func SaveNarrateCronState(root string, state NarrateCronState) error {
	state.Version = narrateCronStateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(NarrateCronStateDir(root), narrateStateFile), data)
}
