package notebook

import "path/filepath"

// Raw tier machine paths.
//
// The narration pipeline feeds the curated journal from a separate "raw tier"
// of machine inputs under <root>/.attn/raw/. Like the dreaming state dir, the raw
// tier lives in the .attn dotdir: it is skipped by List, by the watcher, and by
// any dotfile-aware external sync scanner, and CleanPath rejects dotdir segments
// so Store.Write/Read/AppendJournalEntryOnce cannot address it. Daemon and agent
// code therefore writes the raw tier with direct filesystem I/O, never through
// notebook.Store. These helpers keep the ".attn"/"raw"/... literals in one place
// (mirroring DreamsStateDir) so callers do not hardcode the layout.
const (
	rawDir                 = "raw"
	rawContextSnapshotsDir = "context-snapshots"
	rawDispatchesDir       = "dispatches"
)

// RawDir returns the absolute .attn/raw directory for a notebook root.
func RawDir(root string) string {
	return filepath.Join(root, machineDir, rawDir)
}

// RawContextSnapshotsDir returns the absolute directory holding per-workspace
// context.md removal snapshots (<wsID>.md).
func RawContextSnapshotsDir(root string) string {
	return filepath.Join(RawDir(root), rawContextSnapshotsDir)
}

// RawDispatchesDir returns the absolute directory holding per-dispatch outcome
// files (<dispatchID>.md).
func RawDispatchesDir(root string) string {
	return filepath.Join(RawDir(root), rawDispatchesDir)
}
