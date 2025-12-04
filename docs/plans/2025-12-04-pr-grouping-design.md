# PR Grouping and Repo Muting Design

## Overview

Enhance the PR dashboard with repository grouping, repo-level muting, and PR title display.

## Features

### 1. Repository Grouping

PRs are grouped by repository with collapsible sections.

**Collapsed view:**
```
▶ repo-name (3)
```
- Shows repo name and total PR count
- No title, no state breakdown - just the count

**Expanded view:**
```
▼ repo-name (3)
  ⬡ #123 review
    Fix authentication bug in login flow
  ⬡ #456 merge
    Add user profile endpoint
  ⬡ #789 open
    Refactor database connection pooling for
    better performance under load
```
- Each PR shows number + state on first line
- Title wraps to multiple lines as needed
- Enter on repo line toggles expand/collapse

### 2. Repo Muting

Mute entire repositories to hide all their PRs.

**Behavior:**
- Muted repo hides all PRs from that repo
- Individual PR mute state is preserved but ineffective while repo is muted
- Future: detect literal @mentions to bypass repo mute (deferred)

**UI:**
- 'm' key acts on selected item:
  - On repo line (collapsed or expanded header) → mutes the repo
  - On PR line → mutes that PR
- 'V' toggles visibility of muted repos
- 'M' toggles visibility of muted PRs (only within visible repos)
- Hierarchy: muted repo hides its PRs even if 'M' is on; need 'V' first

### 3. PR Titles

Shown only in expanded repo view, wrapped to fit pane width.

### 4. Navigation

- Up/Down (j/k): Move between items (repos and PRs when expanded)
- Enter: On repo → toggle expand/collapse; On PR → open in browser
- Tab: Switch between Sessions and PRs panes
- m: Mute selected item (repo or PR)
- M: Toggle show muted PRs
- V: Toggle show muted repos

## Data Model Changes

### Protocol

Add to `protocol/types.go`:

```go
// RepoState tracks per-repo UI state
type RepoState struct {
    Repo      string `json:"repo"`
    Muted     bool   `json:"muted"`
    Collapsed bool   `json:"collapsed"`
}
```

Add new commands:
- `mute_repo` - toggle repo muted state
- `collapse_repo` - set repo collapsed state

### Store

Add to store state:
```go
type Store struct {
    // ... existing fields
    repos map[string]*RepoState  // keyed by repo name (e.g., "owner/repo")
}
```

New methods:
- `SetRepoCollapsed(repo string, collapsed bool)`
- `ToggleMuteRepo(repo string)`
- `GetRepoState(repo string) *RepoState`
- `ListRepoStates() []*RepoState`

### Persistence Changes

**Current problem:** `save()` called on every state change, potential conflicts with async PR polling.

**New approach:** Async persistence with dirty flag

```go
type Store struct {
    // ... existing fields
    dirty     bool
    saveMu    sync.Mutex  // separate mutex for save operations
}

// MarkDirty sets the dirty flag (called by state-changing methods)
func (s *Store) markDirty() {
    s.dirty = true
}

// StartPersistence runs a goroutine that periodically saves if dirty
func (s *Store) StartPersistence(interval time.Duration, done <-chan struct{})

// Flush forces an immediate save (call on shutdown)
func (s *Store) Flush()
```

State-changing methods (Add, Remove, UpdateState, SetPRs, etc.) call `markDirty()` instead of `save()`.

Background goroutine:
1. Wake every N seconds (e.g., 3 seconds)
2. If dirty flag set, acquire saveMu, write to disk, clear flag
3. On daemon shutdown, call Flush() for final write

### Persisted State Format

```json
{
  "sessions": [...],
  "prs": [...],
  "repos": [
    {"repo": "owner/repo", "muted": false, "collapsed": true},
    {"repo": "other/repo", "muted": true, "collapsed": false}
  ]
}
```

## Dashboard Changes

### Model

```go
type Model struct {
    // ... existing fields
    repos         map[string]*repoGroup  // grouped PRs
    repoOrder     []string               // sorted repo names
    expandedRepos map[string]bool        // local UI state (synced to daemon)
    showMutedPRs  bool
    showMutedRepos bool
}

type repoGroup struct {
    name  string
    prs   []*protocol.PR
    muted bool
}
```

### Rendering

Build repo groups from PR list, render based on collapsed/expanded state.

### Cursor Navigation

Cursor moves through a flattened list:
- Repo headers (always visible unless muted)
- PR rows (only when repo expanded)

Track cursor as index into this flattened view.

## Tmux Status Bar

Update `FormatWithPRs` to show repo names when few repos, counts when many.

**1-2 repos with PRs:**
```
● 2 waiting | repo-a(2) repo-b(1)
```

**3+ repos:**
```
● 5 waiting | 5 PRs in 4 repos
```

Respects muted state - muted repos/PRs don't count toward "waiting".

## Implementation Order

1. Async persistence (dirty flag + background save)
2. RepoState in protocol and store
3. Repo grouping in dashboard (display only)
4. Expand/collapse with persistence
5. Repo muting with 'm' key
6. 'V' key for showing muted repos
7. PR titles in expanded view

## Testing

- Store: test dirty flag behavior, concurrent access
- Dashboard: test cursor navigation with mixed expanded/collapsed
- Integration: test mute/collapse persists across daemon restart
