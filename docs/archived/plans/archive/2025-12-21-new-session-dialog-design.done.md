# New Session Dialog Redesign

## Problems with Current Implementation

1. **Branch visibility** - "main" label shows repo name but not actual checked-out branch
2. **New worktree starting point** - no visibility into base branch for new worktrees
3. **Tab completion** - not fish-style; doesn't complete inline progressively
4. **Complete vs Open ambiguity** - unclear when navigating deeper vs opening session
5. **Recent directories** - not prominently shown at top during filtering

## Design

### Filesystem Navigation

**Empty state (just opened Cmd+N):**
```
New Session

[                                        ]

RECENT
  attn         ~/projects/victor/attn
  claude-code  ~/projects/oss/claude-code
  dotfiles     ~/dotfiles

↑↓ navigate  Tab complete  Enter select  Esc cancel
```

**After typing `~/pro` and Tab:**
```
New Session

[~/projects/]                              ← Tab completed with /

RECENT (matching)
  attn         ~/projects/victor/attn
  claude-code  ~/projects/oss/claude-code

DIRECTORIES
  victor/
  oss/
  archive/
```

**Key behaviors:**

| Input | Action |
|-------|--------|
| Tab | Complete common prefix including `/` (fish-style) |
| Tab (ambiguous) | Complete as far as possible, show options below |
| Enter | Open session (non-git) OR show repository options (git) |
| Type after Tab | Continue navigating deeper |
| ↑↓ | Navigate suggestion list |
| Esc | Close dialog |

**Recent directories:**
- Always shown at top of list
- Filtered by typed input but remain above filesystem matches
- Stored in daemon, persisted across sessions

### Repository Options Screen

When Enter is pressed on a git repository directory:

```
~/projects/attn

MAIN REPOSITORY
  ● develop @ 3f2a1b9 (2h ago)
    [Enter] Open session

WORKTREES (checked out)
  ◎ feature/hooks → ~/projects/attn-wt/hooks
  ◎ fix/pty-bug → ~/projects/attn-wt/pty-bug

BRANCHES                                    [R] refresh
  + New worktree: [_______________]
    from: ● feature/hooks  ○ origin/main

  ○ main @ 8a2f3c1 (1d ago)
  ○ feature/old-thing @ 2b4c6d8 (3d ago)
  ○ origin/experiment @ 9e1f2a3 (5d ago)

↑↓ navigate  Enter select  R refresh  d delete  Esc back
```

**Sections:**

1. **Main Repository**
   - Shows actual checked-out branch (not hardcoded "main")
   - Shows commit hash and relative time
   - Enter opens session in main repo directory

2. **Worktrees (checked out)**
   - Lists existing worktrees with branch name and path
   - Enter opens session in that worktree
   - `d` to delete worktree

3. **Branches**
   - `+ New worktree` at top with name input
   - Radio toggle for starting point: current session's branch OR origin/[default]
   - Available branches (not checked out) with commit info
   - Enter on branch creates worktree from that branch
   - `d` to delete branch

**Key behaviors:**

| Input | Action |
|-------|--------|
| Enter on main repo | Open session in main repository |
| Enter on worktree | Open session in that worktree |
| Enter on branch | Create worktree from that branch |
| R | Refresh branches (git fetch) |
| d | Delete selected worktree or branch |
| Esc | Back to filesystem navigation |
| n | Focus new worktree input |

### New Worktree Starting Point

The "from:" radio toggle defaults intelligently:

1. If current session is in the same repository → default to current session's branch
2. Otherwise → default to origin/[default branch]

The default branch is auto-detected (main, master, develop, etc.).

### Git Fetch & Caching

- When repository options screen is shown, trigger `git fetch` in background
- Cache fetch results in daemon for 30 minutes
- `R` key forces refresh regardless of cache
- Show loading indicator during fetch

## Implementation Notes

### Approach: Rewrite, Not New Component

This replaces `LocationPicker.tsx` entirely. The current implementation is ~1000 lines with 16 useState hooks and complex mode handling. We'll rewrite it with:
- Cleaner state management (single mode enum, consolidated state)
- Split into focused sub-components
- New visual design (terminal-native aesthetic)

Files to modify/replace:
- `app/src/components/LocationPicker.tsx` → rewrite
- `app/src/components/LocationPicker.css` → rewrite
- `app/src/hooks/useFilesystemSuggestions.ts` → keep, possibly extend

### State Simplification

Current implementation has 16 useState hooks. Consolidate into:
- `mode`: 'browse' | 'repo-options' | 'branch-action'
- `path`: current input path
- `selectedIndex`: for keyboard navigation
- `repoState`: { branches, worktrees, currentBranch, fetching }

### Component Structure

Consider splitting into sub-components:
- `LocationPicker` (main orchestrator)
- `PathInput` (fish-style autocomplete)
- `RepoOptions` (branch/worktree selection)
- `BranchList` (with commit info)

### Daemon Protocol

New/modified commands:
- `get_repo_info`: returns current branch, worktrees, branches with commit info
- `git_fetch`: triggers fetch, updates cache
- `create_worktree`: now accepts `startingPoint` parameter

Cache structure in daemon:
```go
type RepoCache struct {
    Branches    []BranchInfo
    FetchedAt   time.Time
}
```
