package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// readContextSnapshot returns the raw-tier context snapshot for a workspace
// (.attn/raw/context-snapshots/<wsID>.md), or "" if it does not exist.
func readContextSnapshot(t *testing.T, d *Daemon, wsID string) string {
	t.Helper()
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatalf("notebook root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(notebook.RawContextSnapshotsDir(root), wsID+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read context snapshot %s: %v", wsID, err)
	}
	return string(data)
}

// seedWorkspaceContext registers a workspace+session and seeds a revision-1
// shared context with body, returning the daemon ready for a removal-site call.
func seedWorkspaceContext(t *testing.T, d *Daemon, sessionID, workspaceID, body string) {
	t.Helper()
	setupWorkspaceContextSession(t, d, sessionID, workspaceID)
	if _, _, err := d.store.UpdateWorkspaceContext(workspaceID, body, sessionID, 0); err != nil {
		t.Fatalf("seed workspace context: %v", err)
	}
}

// The snapshot must land at every removal site, BEFORE the workspace_contexts row
// is deleted, with the source footer keyed on id@revision.
func TestSnapshotWorkspaceContextAtRemovalSites(t *testing.T) {
	cases := []struct {
		name   string
		wsID   string
		remove func(d *Daemon, sessionID, workspaceID string)
	}{
		{
			name: "handleUnregisterWorkspace",
			wsID: "ws-unreg",
			remove: func(d *Daemon, _, workspaceID string) {
				d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: workspaceID})
			},
		},
		{
			name: "dissociateSessionFromWorkspace",
			wsID: "ws-dissoc",
			remove: func(d *Daemon, sessionID, _ string) {
				d.dissociateSessionFromWorkspace(sessionID)
			},
		},
		{
			name: "unregisterWorkspaceIfEmptyAfterMove",
			wsID: "ws-move",
			remove: func(d *Daemon, sessionID, workspaceID string) {
				// The session must be gone for the workspace to be considered empty.
				d.workspaces.dissociateSession(sessionID)
				d.store.Remove(sessionID)
				d.unregisterWorkspaceIfEmptyAfterMove(workspaceID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newNotebookDaemon(t)
			seedWorkspaceContext(t, d, "session-1", tc.wsID, "# Decisions\nchose hash-CAS")

			tc.remove(d, "session-1", tc.wsID)

			// The row is gone (the removal completed) ...
			if d.store.HasWorkspaceContext(tc.wsID) {
				t.Fatal("workspace context row survived removal")
			}
			// ... yet the snapshot captured the body before the delete.
			snap := readContextSnapshot(t, d, tc.wsID)
			if !strings.Contains(snap, "chose hash-CAS") {
				t.Fatalf("snapshot missing context body:\n%s", snap)
			}
			if !strings.Contains(snap, "source: workspace-context:"+tc.wsID+"@1") {
				t.Fatalf("snapshot missing/incorrect source footer:\n%s", snap)
			}
		})
	}
}

// The startup-reconciliation reap (loadWorkspacesFromStore) is the fourth removal
// site. It runs before the compaction runner exists (compactRunner nil), and the
// snapshot must still land — it does not touch the runner.
func TestSnapshotWorkspaceContextAtLoadTimeReap(t *testing.T) {
	d := newNotebookDaemon(t)
	// Mimic production: an orphaned workspace row + context with no live session,
	// reaped during Start() before startCompactRunner runs.
	d.compactRunner = nil
	d.store.AddWorkspace(&protocol.Workspace{ID: "ws-orphan", Title: "orphan", Directory: "/repo/orphan"})
	if _, _, err := d.store.UpdateWorkspaceContext("ws-orphan", "# Old context\nstale but durable", "s-orphan", 0); err != nil {
		t.Fatalf("seed orphan context: %v", err)
	}

	d.workspaces = newWorkspaceRegistry()
	d.loadWorkspacesFromStore() // must not panic with a nil runner

	if d.store.GetWorkspace("ws-orphan") != nil {
		t.Fatal("orphan workspace survived load reap")
	}
	snap := readContextSnapshot(t, d, "ws-orphan")
	if !strings.Contains(snap, "stale but durable") {
		t.Fatalf("load-reap snapshot missing context body:\n%s", snap)
	}
	if !strings.Contains(snap, "source: workspace-context:ws-orphan@1") {
		t.Fatalf("load-reap snapshot missing source footer:\n%s", snap)
	}
}

// An empty or never-written context is a silent no-op: no file is created.
func TestSnapshotWorkspaceContextEmptyIsNoOp(t *testing.T) {
	d := newNotebookDaemon(t)

	// A workspace that never had a context overlay (revision 0).
	d.snapshotWorkspaceContextOnRemove("ws-empty", "Empty workspace")
	if snap := readContextSnapshot(t, d, "ws-empty"); snap != "" {
		t.Fatalf("absent context should not write a snapshot:\n%s", snap)
	}

	// A workspace whose context is whitespace-only is also a no-op (its revision is
	// >0, so this exercises the content-trim gate, not just the revision gate).
	setupWorkspaceContextSession(t, d, "session-ws", "ws-blank")
	if _, _, err := d.store.UpdateWorkspaceContext("ws-blank", "   \n\t", "session-ws", 0); err != nil {
		t.Fatalf("seed blank context: %v", err)
	}
	d.snapshotWorkspaceContextOnRemove("ws-blank", "Blank")
	if snap := readContextSnapshot(t, d, "ws-blank"); snap != "" {
		t.Fatalf("whitespace-only context should not write a snapshot:\n%s", snap)
	}
}

// A write failure must be swallowed: the helper returns normally and the teardown
// is never blocked. We force failure by pointing the notebook root under a regular
// file, so MkdirAll inside the atomic writer fails.
func TestSnapshotWorkspaceContextSwallowsWriteFailure(t *testing.T) {
	d := newNotebookDaemon(t)
	setupWorkspaceContextSession(t, d, "session-1", "ws-fail")
	if _, _, err := d.store.UpdateWorkspaceContext("ws-fail", "real content", "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	d.store.SetSetting(SettingNotebookRoot, filepath.Join(blocker, "notebook"))

	// Must not panic and must complete; the full removal path still tears down.
	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: "ws-fail"})

	if d.store.GetWorkspace("ws-fail") != nil {
		t.Fatal("workspace must still be removed when the snapshot write fails")
	}
}

// A replayed removal is a harmless identical overwrite — the 1:1 <wsID>.md keying
// means a second snapshot of the same revision reproduces byte-identical content.
func TestSnapshotWorkspaceContextReplayIsIdenticalOverwrite(t *testing.T) {
	d := newNotebookDaemon(t)
	seedWorkspaceContext(t, d, "session-1", "ws-replay", "# Decisions\nlocked the design")

	d.snapshotWorkspaceContextOnRemove("ws-replay", "Replay")
	first := readContextSnapshot(t, d, "ws-replay")
	if first == "" {
		t.Fatal("first snapshot did not write")
	}

	// The row still exists (we called the helper directly, not the full removal), so
	// a replay reads the same revision-1 content and overwrites identically.
	d.snapshotWorkspaceContextOnRemove("ws-replay", "Replay")
	second := readContextSnapshot(t, d, "ws-replay")
	if second != first {
		t.Fatalf("replay was not an identical overwrite:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// A context body that embeds a literal journal marker must be neutralized so no
// free-text overlay content can forge a marker in the raw tier.
func TestSnapshotWorkspaceContextNeutralizesForgedMarker(t *testing.T) {
	d := newNotebookDaemon(t)
	forged := "Notes\n" + journalDispatchMarker("dsp-victim") + "\nmore notes"
	seedWorkspaceContext(t, d, "session-1", "ws-forge", forged)

	d.snapshotWorkspaceContextOnRemove("ws-forge", "Forge")

	snap := readContextSnapshot(t, d, "ws-forge")
	if strings.Contains(snap, journalDispatchMarker("dsp-victim")) {
		t.Fatalf("forged marker survived neutralization:\n%s", snap)
	}
	if !strings.Contains(snap, "<! -- attn:dispatch:dsp-victim -->") {
		t.Fatalf("forged marker should be neutralized to a non-opener:\n%s", snap)
	}
}

// A crafted workspace id with ".." segments must NOT let the snapshot writer escape
// the context-snapshots subdir. register_workspace accepts the id verbatim over the
// socket, so an id like "../../../journal/<date>" would otherwise resolve out of the
// raw tier and overwrite the curated journal (or, with more "..", any .md file the
// daemon can write outside the notebook root). The write must be refused: no file is
// created at the escape target, and the workspace context row is irrelevant because
// the body never reaches disk.
func TestSnapshotWorkspaceContextRejectsPathTraversal(t *testing.T) {
	cases := []struct {
		name      string
		craftedID string
		// target is the absolute escape path the unguarded join would have written,
		// expressed relative to the notebook root or its parent.
		target func(root string) string
	}{
		{
			name:      "into the curated journal",
			craftedID: "../../../journal/2026-06-15",
			target:    func(root string) string { return filepath.Join(root, "journal", "2026-06-15.md") },
		},
		{
			name:      "outside the notebook root",
			craftedID: "../../../../victim",
			target:    func(root string) string { return filepath.Join(filepath.Dir(root), "victim.md") },
		},
		{
			name:      "absolute path escape",
			craftedID: "/etc/attn-victim",
			target:    func(root string) string { return filepath.Join(root, "etc", "attn-victim.md") },
		},
		{
			name:      "nested subdir escape",
			craftedID: "../dispatches/dsp-victim",
			target: func(root string) string {
				return filepath.Join(notebook.RawDispatchesDir(root), "dsp-victim.md")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newNotebookDaemon(t)
			root, err := d.notebookRoot()
			if err != nil {
				t.Fatalf("notebook root: %v", err)
			}

			// Plant a sentinel at the escape target so a successful escape would be an
			// observable overwrite, not just a fresh write.
			victim := tc.target(root)
			if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
				t.Fatalf("mkdir victim dir: %v", err)
			}
			if err := os.WriteFile(victim, []byte("SENTINEL — must survive"), 0o644); err != nil {
				t.Fatalf("seed sentinel: %v", err)
			}

			seedWorkspaceContext(t, d, "session-evil", tc.craftedID, "ATTACKER CONTROLLED CONTENT")
			d.snapshotWorkspaceContextOnRemove(tc.craftedID, "evil")

			data, err := os.ReadFile(victim)
			if err != nil {
				t.Fatalf("read sentinel: %v", err)
			}
			if string(data) != "SENTINEL — must survive" {
				t.Fatalf("path traversal overwrote %s:\n%s", victim, string(data))
			}
		})
	}
}

// The shared raw-tier writer is the single chokepoint and must reject an unsafe id
// directly, independent of the snapshot call site, so a future caller cannot
// reintroduce the escape.
func TestWriteRawAtomicRejectsUnsafeID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "raw", "context-snapshots")

	for _, id := range []string{
		"",
		".",
		"..",
		"../escape",
		"../../journal/2026-06-15",
		"/etc/passwd",
		"a/b",
		`a\b`,
		".hidden",
		// Control chars: a newline in the id would otherwise inject a forged
		// "source:" footer line into the file body (the id is interpolated there).
		"ws\nsource: workspace-context:victim@9",
		"ws\x00null",
		"ws\x7fdel",
	} {
		if err := writeRawAtomic(root, dir, id, []byte("x")); err == nil {
			t.Fatalf("writeRawAtomic accepted unsafe id %q", id)
		}
	}

	// A normal id still writes to exactly dir/<id>.md and nowhere else.
	if err := writeRawAtomic(root, dir, "ws-ok", []byte("ok")); err != nil {
		t.Fatalf("writeRawAtomic rejected a safe id: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ws-ok.md"))
	if err != nil {
		t.Fatalf("safe id did not write to dir/<id>.md: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("safe write produced wrong content: %q", string(data))
	}
}

// The raw tier lives under the externally-syncable notebook root, so a user/sync
// client could turn a raw-tier subdir (e.g. .attn/raw/dispatches) into a symlink
// pointing outside the root. The lexical id/parent checks cannot catch that;
// writeRawAtomic must resolve ancestors and refuse to write through the symlink.
func TestWriteRawAtomicRejectsSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	rawParent := filepath.Join(root, "raw")
	if err := os.MkdirAll(rawParent, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	dir := filepath.Join(rawParent, "dispatches")
	if err := os.Symlink(outside, dir); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	if err := writeRawAtomic(root, dir, "evil", []byte("x")); err == nil {
		t.Fatalf("writeRawAtomic wrote through a symlinked ancestor pointing outside the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("a raw-tier file leaked outside the notebook root: stat err=%v", err)
	}
}
