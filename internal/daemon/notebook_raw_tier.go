package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
)

// The raw tier holds machine inputs for the narration pipeline under
// <notebook.root>/.attn/raw/ — unreachable through the user-facing notebook APIs
// (CleanPath rejects dotdir segments) and skipped by the watcher, so raw writes
// emit no external-edit broadcast.

// rawTierFilename turns a raw-tier item id into a single safe "<id>.md"
// filename. Load-bearing guard: ids are CLIENT-CONTROLLED (register_workspace
// accepts them verbatim), and a ".." id joined into a path would climb out of
// the raw tier and overwrite the curated journal.
func rawTierFilename(id string) (string, error) {
	return rawTierName(id, ".md")
}

// rawTierSegment validates a raw-tier id as a single safe directory segment
// (no extension), with the same containment guard as rawTierFilename.
func rawTierSegment(id string) (string, error) {
	return rawTierName(id, "")
}

// rawTierName is the shared single-safe-segment guard. suffix is "" for a bare
// directory segment or ".md" for a raw-tier file.
func rawTierName(id, suffix string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("raw-tier id is empty")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." || strings.HasPrefix(id, ".") {
		return "", fmt.Errorf("raw-tier id %q is not a single safe path segment", id)
	}
	// Control chars rejected: an id is interpolated into the plaintext "source:"
	// footer, so a newline could inject a forged grounding line.
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("raw-tier id %q contains a control character", id)
		}
	}
	name := id + suffix
	// Belt-and-suspenders: an escaping id would not round-trip Base/Clean.
	if filepath.Base(name) != name || name != filepath.Clean(name) {
		return "", fmt.Errorf("raw-tier id %q does not produce a safe filename", id)
	}
	return name, nil
}

// writeRawAtomic writes a raw-tier file via temp+rename (no fsync, matching the
// repo idiom). It is the single chokepoint that validates the id, and a second
// containment assertion keeps the final path under dir even if rawTierFilename
// is ever weakened.
func writeRawAtomic(root, dir, id string, content []byte) error {
	name, err := rawTierFilename(id)
	if err != nil {
		return err
	}
	absPath := filepath.Join(dir, name)
	cleanDir := filepath.Clean(dir)
	if filepath.Dir(absPath) != cleanDir {
		return fmt.Errorf("raw-tier write for %q escapes %q", id, dir)
	}
	// Lexical checks only prove the string join. The root is externally syncable,
	// so a raw-tier ancestor could be a symlink pointing outside it; resolve the
	// deepest existing ancestor first — the same guard Store.Read/Write/List apply.
	if err := notebook.EnsureWithinResolvedRoot(root, absPath); err != nil {
		return err
	}
	if err := os.MkdirAll(cleanDir, 0o755); err != nil {
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

// snapshotWorkspaceContextOnRemove synchronously captures a workspace's
// context.md overlay into the raw tier. It MUST run at every removal site AFTER
// the keeper compaction cancel/forget and BEFORE store.RemoveWorkspace, whose
// DELETE FROM workspace_contexts an async writer cannot win. Best-effort: every
// failure is logged and swallowed, never failing a teardown. Keyed 1:1 on the
// workspace id, so a replayed removal is a harmless identical overwrite.
func (d *Daemon) snapshotWorkspaceContextOnRemove(id, title string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}

	canonical, err := d.store.GetWorkspaceContext(id)
	if err != nil {
		d.logf("context snapshot %s (%s): read context: %v", id, title, err)
		return
	}
	if strings.TrimSpace(canonical.Content) == "" || canonical.Revision == 0 {
		return // no overlay to preserve — silent no-op
	}

	root, err := d.notebookRoot()
	if err != nil {
		d.logf("context snapshot %s (%s): notebook root unavailable: %v", id, title, err)
		return
	}
	if strings.TrimSpace(root) == "" {
		return // notebook disabled — silent no-op
	}

	// Neutralize BEFORE appending the genuine footer, so free text cannot forge a
	// journal marker while the real footer stays intact.
	var doc strings.Builder
	doc.WriteString(neutralizeJournalMarkers(canonical.Content))
	fmt.Fprintf(&doc, "\nsource: workspace-context:%s@%d\n", id, canonical.Revision)

	dir := notebook.RawContextSnapshotsDir(root)
	if err := writeRawAtomic(root, dir, id, []byte(doc.String())); err != nil {
		d.logf("context snapshot %s (%s): write under %s: %v", id, title, dir, err)
		return
	}
}

// neutralizeJournalMarkers breaks any HTML-comment opener ("<!--") in a rendered
// body so a free-text field can never forge a raw-tier source marker. The real
// footer the writer appends afterward stays the only authentic marker.
func neutralizeJournalMarkers(s string) string {
	return strings.ReplaceAll(s, "<!--", "<! --")
}
