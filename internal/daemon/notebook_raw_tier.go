package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
)

// The raw tier is the deterministic capture floor for the notebook narration
// pipeline. It holds machine inputs the narrate pass later consumes, under
// <notebook.root>/.attn/raw/ — physically unreachable through the user-facing
// notebook APIs (CleanPath rejects dotdir segments) and skipped by the watcher,
// so raw writes emit no external-edit broadcast. Two deterministic daemon writes
// land here and SHARE this one atomic writer + the one neutralizeJournalMarkers
// step so they are built, reviewed, and tested together:
//
//   - context-snapshots/<wsID>.md — the synchronous context.md snapshot taken at
//     every workspace-removal site, BEFORE store.RemoveWorkspace runs its
//     DELETE FROM workspace_contexts (an async writer cannot win that race).
//   - dispatches/<dispatchID>.md  — the redirected dispatch outcome capture (see
//     notebook_dispatch_journal.go).
//
// They cannot collide: distinct per-item files under distinct subdirs, distinct
// exactly-once ledgers (file existence + a source footer / marker), and both
// bodies pass through neutralizeJournalMarkers so no free-text field can forge a
// journal marker.

// rawTierFilename turns a raw-tier item id (a workspace id or dispatch id) into a
// single safe "<id>.md" filename, or errors if the id cannot be a single path
// segment. This is the load-bearing guard that keeps a CLIENT-CONTROLLED id (the
// workspace id from register_workspace is accepted verbatim over the socket, with
// no UUID/segment validation) from escaping its raw-tier subdir: filepath.Join
// cleans ".." segments, so an unvalidated id like "../../../journal/2026-06-15"
// would resolve OUT of context-snapshots/ and overwrite the curated journal (or,
// with enough "..", any .md file the daemon can write outside the notebook root).
// The raw tier is supposed to be physically unreachable through the user-facing
// notebook APIs, and the daemon must never write the curated journal; an id that
// is not a plain filename violates both, so we reject it rather than join it.
//
// Dispatch ids are server UUIDs and never trip this, but routing both writers
// through the same guard keeps the invariant in one place instead of resting on a
// per-call-site property a future caller could quietly break.
func rawTierFilename(id string) (string, error) {
	return rawTierName(id, ".md")
}

// rawTierSegment validates a raw-tier id as a single safe path *segment* (no
// extension), for callers that need a directory name rather than a "<id>.md"
// file — e.g. partitioning the per-session digest dir by workspace id
// (RawSessionsDir/<wsID>/). It applies the same containment guard as
// rawTierFilename so a crafted id (workspace id from register_workspace is
// accepted verbatim over the socket) can never climb out of the raw tier.
func rawTierSegment(id string) (string, error) {
	return rawTierName(id, "")
}

// rawTierName is the shared single-safe-segment guard. It trims, rejects path
// separators / dotdir / control characters, optionally appends an extension, and
// asserts the result round-trips through filepath.Base/Clean so an escaping id
// cannot slip through. suffix is "" for a bare directory segment or ".md" for a
// raw-tier file.
func rawTierName(id, suffix string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("raw-tier id is empty")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." || strings.HasPrefix(id, ".") {
		return "", fmt.Errorf("raw-tier id %q is not a single safe path segment", id)
	}
	// Reject control characters (newlines especially): an id is interpolated into
	// the file's plaintext "source:" footer, so a newline would let it inject a
	// forged grounding line, and a control char has no place in a filename anyway.
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("raw-tier id %q contains a control character", id)
		}
	}
	name := id + suffix
	// Belt-and-suspenders: a Clean of the bare name must be the name itself (no
	// separator, no dotdir reintroduced). filepath.Base of an escaping id would not
	// round-trip to it.
	if filepath.Base(name) != name || name != filepath.Clean(name) {
		return "", fmt.Errorf("raw-tier id %q does not produce a safe filename", id)
	}
	return name, nil
}

// writeRawAtomic writes content to a raw-tier file built from dir + a validated
// "<id>.md" filename, via a temp+rename so a reader never observes a half-written
// file, MkdirAll'ing the parent first so a write never fails on a missing raw-tier
// subdir. It mirrors notebook.writeAtomic / tasks.writeAtomic (no fsync, matching
// the repo idiom). This is the one writer both deterministic raw-tier writes use,
// and the single chokepoint that validates the id: callers pass the raw id and the
// intended subdir, and the path is only ever dir/<safe-name> — never a join that
// ".." could climb out of. A second containment assertion verifies the final path
// stays under dir even if rawTierFilename is ever weakened.
func writeRawAtomic(dir, id string, content []byte) error {
	name, err := rawTierFilename(id)
	if err != nil {
		return err
	}
	absPath := filepath.Join(dir, name)
	cleanDir := filepath.Clean(dir)
	if filepath.Dir(absPath) != cleanDir {
		return fmt.Errorf("raw-tier write for %q escapes %q", id, dir)
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
// context.md overlay into the raw tier before the workspace row (and its
// context.md) is deleted. It is the deterministic data-safety floor: context.md
// is erased by store.RemoveWorkspace's DELETE FROM workspace_contexts, which an
// async writer cannot win, so this MUST run at every removal site AFTER the
// keeper compaction cancel/forget (commit-fence: no in-flight keeper compaction
// write is still racing the context row) and BEFORE store.RemoveWorkspace.
//
// Best-effort: it never returns an error and never blocks or fails a teardown.
// Every failure — read error, unresolvable/unconfigured notebook root, write
// error — is logged and swallowed. An empty or revision-0 context (a workspace
// that never had an overlay) is a silent no-op.
//
// The file is keyed 1:1 on the workspace id, so a replayed removal is a harmless
// identical overwrite; existence of <wsID>.md plus the
// "source: workspace-context:<id>@<revision>" footer is the exactly-once ledger
// (no HTML-comment dedup marker is needed). title is used only for logging — the
// file is keyed on id, so a blank title never breaks the write or the ledger.
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

	// Neutralize the verbatim overlay BEFORE appending the genuine source footer,
	// so no free-text field in the body can forge a journal marker while the real
	// footer stays intact (same pattern renderDispatchJournalEntry uses).
	var doc strings.Builder
	doc.WriteString(neutralizeJournalMarkers(canonical.Content))
	fmt.Fprintf(&doc, "\nsource: workspace-context:%s@%d\n", id, canonical.Revision)

	dir := notebook.RawContextSnapshotsDir(root)
	if err := writeRawAtomic(dir, id, []byte(doc.String())); err != nil {
		d.logf("context snapshot %s (%s): write under %s: %v", id, title, dir, err)
		return
	}
}
