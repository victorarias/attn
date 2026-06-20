// Package notebook implements the Notebook on-disk format and a
// filesystem-canonical store: a durable, profile-wide markdown layer (dated
// journals + a PARA-organized knowledge base) that outlives any single
// workspace.
//
// The package is deliberately free of daemon dependencies so the format and the
// store can be unit-tested in isolation. The daemon wraps a Store, resolves the
// notebook root from settings, serializes attn-originated writes, and broadcasts
// change events; this package only knows about bytes on disk.
//
// Design notes:
//   - Filesystem-canonical: the .md files under the root ARE the source of
//     truth (unlike workspace context.md, which is SQLite-canonical). Edits use
//     hash-CAS on file bytes, not a revision table.
//   - Plain files, paths-as-identity, root-absolute markdown links (no
//     wikilinks), machine state under a .attn/ dotdir — so an external markdown
//     sync tool or Obsidian can point at the root without attn precluding it.
//   - Permissive reader: never reject a file on unknown type, extra frontmatter
//     keys, broken links, or a missing index; preserve unknown keys on
//     round-trip so fields written by other tools survive.
package notebook

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// TypeJournal is the OKF `type` value attn writes onto dated journal notes. It
// is the one type attn authors itself; every other note's `type` is author- or
// chief-chosen (OKF leaves the vocabulary open), so the store does not enforce a
// closed set — it stays a permissive reader/writer.
const TypeJournal = "journal"

// MaxFileSize bounds a single note so the store stays sync-friendly; journals
// rotate daily to stay well under it.
const MaxFileSize = 2 << 20 // 2 MiB

// Hash returns the canonical content hash used for hash-CAS edits.
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Entry is a single note as surfaced by List: enough to render a tree without
// reading every file's full body.
type Entry struct {
	Path    string // notebook-relative, slash-separated, e.g. "knowledge/areas/foo.md"
	Type    string // OKF `type` ("" when the file declares none; permissive)
	Title   string
	Summary string
	Updated string // frontmatter `updated`, else the file mtime (RFC3339)
	Size    int64
}

// Conflict reports that a hash-CAS write did not apply because the on-disk
// content no longer matches the base the caller read. CurrentHash is the hash
// now on disk ("" when the target file is gone).
type Conflict struct {
	CurrentHash string
}

// NotFoundError is returned by Read when the requested note does not exist.
type NotFoundError struct{ Path string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("notebook: %s not found", e.Path)
}

// IsNotFound reports whether err is a NotFoundError.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}
