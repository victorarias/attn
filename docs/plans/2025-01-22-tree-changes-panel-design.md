# Tree-structured Changes Panel

## Overview

Replace the flat file list in the Changes panel with a tree structure that groups files by directory. Deep paths get abbreviated shell-style (first N-3 folders become single chars).

**Example:**
```
Staged (3)
▼ s/c/forms/inputs/
    A TextField.tsx     +45
    A SelectField.tsx   +32
▼ internal/daemon/
    M gitstatus.go      +12 -3

Changes (1)
▼ app/src/
    M App.tsx           +5 -2

Untracked (2)
▼ docs/plans/
    ? design.md
    ? notes.md
```

## Backend Changes

**File:** `internal/daemon/gitstatus.go`

1. **Expand untracked directories** - When path ends with `/`, walk directory to get individual files
2. **Respect .gitignore** - Use `git check-ignore` to skip ignored files
3. **No protocol changes** - Still send flat `[]GitFileChange`, tree built client-side

## Frontend Changes

**File:** `app/src/components/ChangesPanel.tsx`

1. **Build tree structure** - Group files by directory path
2. **Abbreviate deep paths** - Paths >3 levels: abbreviate first N-3 to single chars
   - `src/components/forms/inputs` → `s/c/forms/inputs`
3. **Render tree recursively** - Directories show abbreviated path, files indented beneath
4. **Click behavior** - Directories: no action. Files: show diff.
5. **Always expanded** - No collapse/expand, all directories open

## Edge Cases

- Single file in root: show flat, no directory wrapper
- Multiple files same dir: group under one directory node
- Mixed depths: each unique directory path gets own node
- Empty directories: won't appear (git doesn't track them)
- Renamed files: show in both locations if paths differ
