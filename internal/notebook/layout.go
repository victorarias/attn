package notebook

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Reserved layout (OKF-derived). index.md and log.md carry no frontmatter, like
// OKF's bundle root. The .attn/ dotdir holds machine state (dreaming candidates,
// locks, run reports) and is never surfaced by List nor touched by a
// dotfile-skipping external sync scanner.
const (
	FileIndex = "index.md"
	FileLog   = "log.md"

	DirJournal = "journal"
	DirMemory  = "memory"

	machineDir = ".attn"
)

// memorySubdirs are organizational groupings under memory/ (not kinds).
var memorySubdirs = []string{"decisions", "gotchas", "domain"}

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

// CleanPath validates and normalizes a notebook path. The input may be
// root-absolute ("/memory/foo.md", matching the link convention) or relative
// ("memory/foo.md"); the result is always a clean, slash-separated relative
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
	dirs := []string{DirJournal, DirMemory}
	for _, sub := range memorySubdirs {
		dirs = append(dirs, path.Join(DirMemory, sub))
	}
	return dirs
}

func scaffoldFiles() []scaffoldFile {
	return []scaffoldFile{
		{FileIndex, indexTemplate},
		{FileLog, logTemplate},
		{path.Join(DirMemory, FileIndex), memoryIndexTemplate},
	}
}

const indexTemplate = `# Notebook

Durable, profile-wide markdown memory — written by attn on behalf of agents, a
nightly consolidation pass, and you. It outlives any single workspace.

- ` + "`journal/`" + ` — dated, append-only entries (the raw system of record).
- ` + "`memory/`" + ` — durable distilled notes: decisions, gotchas, domain knowledge.
- ` + "`log.md`" + ` — global change history, newest first.

Kinds: ` + "`journal`" + `, ` + "`memory`" + `. Links are root-absolute markdown, e.g.
[a decision](/memory/decisions/example.md) — not wikilinks.

<!-- okf_version: 0.1 -->
`

const logTemplate = `# Log

Change history, newest first.
`

const memoryIndexTemplate = `# Memory

Durable distilled notes. Every note carries resolvable ` + "`sources:`" + ` — there is
no ungrounded memory.

- ` + "`decisions/`" + ` — cross-workspace decisions that outlived a single PR.
- ` + "`gotchas/`" + ` — repeated surprises worth remembering.
- ` + "`domain/`" + ` — glossary and business rules.
`
