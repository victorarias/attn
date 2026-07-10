package notebook

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Reserved layout (OKF-derived). index.md and log.md carry no frontmatter, like
// OKF's bundle root. The .attn/ dotdir holds machine state (the durable task
// runner, raw tier, narrate-cron anchor) and is never surfaced by List nor touched
// by a dotfile-skipping external sync scanner.
const (
	FileIndex = "index.md"
	FileLog   = "log.md"
	// FileInbox is the reserved note where "send to chief" messages accumulate.
	// It is created on the first send (not scaffolded) and lives at the root so
	// it groups under "Notebook" in the browser, distinct from journal/knowledge.
	FileInbox = "inbox.md"

	DirJournal   = "journal"
	DirKnowledge = "knowledge"

	machineDir = ".attn"
)

// paraSubdirs are the PARA-method groupings under knowledge/ (the directory
// axis). They are organizational, orthogonal to a note's OKF `type`. Knowledge
// is nested under projects/ and areas/; resources/ and archive/ hold reference
// and inactive material respectively.
var paraSubdirs = []string{"projects", "areas", "resources", "archive"}

// DefaultRoot returns the default notebook root for a profile, derived from the
// user's home directory. The default profile ("" or "default") maps to
// ~/attn-notebook; a named profile "foo" maps to ~/attn-notebook-foo, so a
// dev/test profile never writes the real notebook. The root lives OUTSIDE
// ~/.attn[-profile]/ so it is a plain, externally-syncable directory.
func DefaultRoot(home, profile string) string {
	base := filepath.Join(home, "attn-notebook")
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "default" {
		return base
	}
	return base + "-" + p
}

// TicketsDir returns the absolute .attn/tickets directory for a notebook root —
// the store for ticket attachment files. Like the raw tier it lives under .attn/,
// which CleanPath rejects and a dotfile-skipping external sync scanner ignores, so
// it is written with direct filesystem I/O, not the notebook.Store APIs.
func TicketsDir(root string) string {
	return filepath.Join(root, machineDir, "tickets")
}

// TicketAttachmentsDir returns the absolute directory holding one ticket's copied
// attachment files (.attn/tickets/<id>/).
func TicketAttachmentsDir(root, ticketID string) string {
	return filepath.Join(TicketsDir(root), ticketID)
}

// TicketArtifactsDir returns the visible Notebook directory whose direct
// Markdown children are the current artifacts for one ticket.
func TicketArtifactsDir(root, ticketID string) string {
	return filepath.Join(root, "tickets", ticketID)
}

// CleanPath validates and normalizes a notebook path. The input may be
// root-absolute ("/knowledge/areas/foo.md", matching the link convention) or
// relative ("knowledge/areas/foo.md"); the result is always a clean, slash-separated relative
// path. It rejects empty paths, the root itself, parent-directory escapes,
// dotfile/dotdir segments, and any extension other than .md.
func CleanPath(p string) (string, error) {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return "", fmt.Errorf("notebook: empty path")
	}
	// Anchor at "/" and Clean so any ".." is neutralized to within the root,
	// then strip the leading slash to get a relative path.
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(trimmed, "/")), "/")
	if rel == "" || rel == "." {
		return "", fmt.Errorf("notebook: %q is the root, not a file", p)
	}
	if !strings.HasSuffix(rel, ".md") {
		return "", fmt.Errorf("notebook: %q must be a .md file", p)
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" {
			return "", fmt.Errorf("notebook: %q has an empty path segment", p)
		}
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("notebook: %q has a dotfile/dotdir segment", p)
		}
	}
	return rel, nil
}

type scaffoldFile struct {
	relPath string
	content string
}

func scaffoldDirs() []string {
	dirs := []string{DirJournal, DirKnowledge}
	for _, sub := range paraSubdirs {
		dirs = append(dirs, path.Join(DirKnowledge, sub))
	}
	return dirs
}

func scaffoldFiles() []scaffoldFile {
	files := []scaffoldFile{
		{FileIndex, indexTemplate},
		{FileLog, logTemplate},
		{path.Join(DirKnowledge, FileIndex), knowledgeIndexTemplate},
	}
	for _, sub := range paraSubdirs {
		files = append(files, scaffoldFile{
			relPath: path.Join(DirKnowledge, sub, FileIndex),
			content: paraIndexTemplates[sub],
		})
	}
	return files
}

// ScaffoldPaths returns the notebook-relative paths of the reserved files that
// EnsureScaffold creates, for callers that need to announce a fresh init.
func ScaffoldPaths() []string {
	files := scaffoldFiles()
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.relPath
	}
	return out
}

const indexTemplate = `# Notebook

A durable, profile-wide markdown bundle — the journal attn writes on your behalf
and the knowledge base the chief of staff maintains. It outlives any single
workspace and is yours to read, edit, and sync.

- ` + "`journal/`" + ` — dated narrative of what was done, newest entries appended per day.
- ` + "`knowledge/`" + ` — the PARA-organized knowledge base (projects, areas, resources, archive).
- ` + "`log.md`" + ` — global change history, newest first.

This is one OKF bundle (Open Knowledge Format): every note carries a ` + "`type`" + `
frontmatter field, and links are root-absolute markdown, e.g.
[an area note](/knowledge/areas/example.md) — not wikilinks.

<!-- okf_version: 0.1 -->
`

const logTemplate = `# Log

Change history, newest first.
`

// inboxTemplate seeds inbox.md on the first "send to chief". It has no type: it
// is an append-only delivery log, not durable knowledge.
const inboxTemplate = `# Chief inbox

Selections sent to the chief of staff from the Notebook, oldest first.
`

const knowledgeIndexTemplate = `# Knowledge base

The chief of staff's durable knowledge, organized by the PARA method (the
directory axis) with an OKF ` + "`type`" + ` on every note (the frontmatter axis).

- ` + "`projects/`" + ` — bounded efforts with an end (one folder per project/epic).
- ` + "`areas/`" + ` — ongoing responsibilities and subsystems, with no end.
- ` + "`resources/`" + ` — reference material worth keeping.
- ` + "`archive/`" + ` — finished or inactive items, moved here when a project closes.

Every note is grounded — carry resolvable ` + "`sources:`" + ` (journal anchors
or URLs), never paraphrase alone.
`

// paraIndexTemplates seeds each PARA directory's reserved index.md.
var paraIndexTemplates = map[string]string{
	"projects":  "# Projects\n\nBounded efforts with an end. One folder per project or epic; a\nproject's `index.md` links the workspace that produced it with\n`resource: attn:workspace/<id>`.\n",
	"areas":     "# Areas\n\nOngoing responsibilities and subsystems, with no end. Durable knowledge\npromoted out of finished projects lands here.\n",
	"resources": "# Resources\n\nReference material worth keeping across projects and areas.\n",
	"archive":   "# Archive\n\nFinished or inactive items. A project folder is moved here when its\nworkspace closes.\n",
}
