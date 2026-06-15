package notebook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNarrateCronStateRoundTrip(t *testing.T) {
	root := t.TempDir()

	// Missing file reads as the zero value, not an error.
	zero, err := LoadNarrateCronState(root)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if zero.ScheduledFrom != "" {
		t.Fatalf("expected zero state, got %+v", zero)
	}

	if err := SaveNarrateCronState(root, NarrateCronState{
		ScheduledFrom: "2026-06-14T03:00:00Z",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadNarrateCronState(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Version != narrateCronStateVersion {
		t.Fatalf("expected version stamped to %d, got %d", narrateCronStateVersion, got.Version)
	}
	if got.ScheduledFrom != "2026-06-14T03:00:00Z" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// The state lives under the .attn narrate dir, separate from the dreams dir.
	if _, err := os.Stat(filepath.Join(NarrateCronStateDir(root), narrateStateFile)); err != nil {
		t.Fatalf("narrate state file not written: %v", err)
	}
}
