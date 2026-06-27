package notebook

import "path/filepath"

// Raw tier machine paths.
//
// The narration pipeline feeds the curated journal from a separate "raw tier"
// of machine inputs under <root>/.attn/raw/. The raw tier lives in the .attn
// dotdir: it is skipped by List, by the watcher, and by any dotfile-aware external
// sync scanner, and CleanPath rejects dotdir segments so Store.Write/Read cannot
// address it. Daemon and agent code therefore writes the raw tier with direct
// filesystem I/O, never through notebook.Store. These helpers keep the
// ".attn"/"raw"/... literals in one place so callers do not hardcode the layout.
const (
	rawDir                 = "raw"
	rawContextSnapshotsDir = "context-snapshots"
	rawSessionsDir         = "sessions"
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

// RawSessionsDir returns the absolute directory holding per-session digest files
// (<sessionID>.md), written natively by the summarize_session narration agent and
// later read by the narrate_workspace agent. Like the other raw-tier subdirs it
// lives under .attn/raw/ — unreachable through the user-facing notebook APIs.
func RawSessionsDir(root string) string {
	return filepath.Join(RawDir(root), rawSessionsDir)
}
