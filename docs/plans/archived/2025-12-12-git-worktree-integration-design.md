# Git Worktree Integration Design

## Overview

Integrate git worktrees into the session management workflow, enabling seamless parallel feature work and multi-PR management without manual git gymnastics.

**Goals:**
- Enable parallel work on multiple branches simultaneously
- Make worktree creation invisible/seamless
- Support multi-PR review workflows
- Full keyboard navigation

## Core Concepts

### Session Grouping by Directory

Sessions are grouped by their working directory (`cwd`), regardless of whether it's a main repo or worktree:

- Multiple sessions in the same directory are grouped together
- Applies to both sidebar and dashboard
- Worktrees are just directories with branch metadata

### Branch Display

Every session shows its current git branch:
- Format: `label · branch-name`
- Worktree sessions get a `⎇` indicator
- Branch updated via daemon polling

### Worktree Location Strategy

Worktrees are created as sibling directories with clear naming:
```
~/projects/
  claude-manager/                          ← main repo
  claude-manager--pr-142/                  ← PR worktree
  claude-manager--feature-add-worktrees/   ← feature worktree
```

Double-dash separator makes worktrees obvious and easy to clean up.

## Entry Points

### 1. PR Card Action (Zero Friction)

Add "Open" button to PR row actions:

```
┌─────────────────────────────────────────────────────┐
│ ● fix: dashboard loading state        #142          │
│   victor.arias • 2 commits • CI passing             │
│                              [Open] [✓] [⇋] [⊘]     │
└─────────────────────────────────────────────────────┘
```

**Behavior:**
1. Check if worktree exists for PR branch → reuse it
2. If not → create worktree: `repo--pr-{number}-{short-description}`
3. Spawn terminal session in worktree
4. Focus new session

No modal. No questions. One click.

### 2. New Session Flow (Keyboard-First)

Extend LocationPicker with worktree awareness:

```
┌─────────────────────────────────────────────────────┐
│  NEW SESSION                                   ⌘⇧N  │
│                                                     │
│  [~/projects/trackour                          ]    │
│                                                     │
│  ──────────────────────────────────────────────     │
│  1  main branch                                     │
│  ──────────────────────────────────────────────     │
│  ⎇ WORKTREES                                        │
│  2  feature/auth           ● 1 session              │
│  3  fix/login-bug                                   │
│  4  refactor/api-client    ● 2 sessions             │
│  ──────────────────────────────────────────────     │
│  n  New branch...                                   │
│                                                     │
│  ↑↓ navigate  ⏎ select  n new branch  esc cancel   │
└─────────────────────────────────────────────────────┘
```

**Keyboard bindings:**

| Key | Action |
|-----|--------|
| `↑` `↓` | Navigate options |
| `1-9` | Quick-select by number |
| `⏎` | Open session in selected |
| `n` | Jump to "new branch" input |
| `⎋` | Cancel |

**Features:**
- Shows existing worktrees when selecting a git repo
- Session count is informational (multiple sessions per worktree allowed)
- Number shortcuts for muscle memory
- "New branch..." always available via `n` key

### 3. Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `⌘N` | New session (existing flow) |
| `⌘⇧N` | New session with branch (worktree flow) |

## UI Components

### Sidebar (Expanded)

```
┌─ SESSIONS ──────────────────────────────────────┐
│ ● claude-manager · main                    ⌘1   │
│                                                 │
│ trackour                                        │
│   ● main work · main                       ⌘2   │
│   ○ tests · main                           ⌘3   │
│                                                 │
│ ○ trackour · fix/login-bug             ⎇   ⌘4   │
│ ● trackour · feature/auth              ⎇   ⌘5   │
└─────────────────────────────────────────────────┘
```

**Display rules:**
- Group sessions by directory
- Show `repo-name · branch` format
- Worktree indicator `⎇` on the right
- Single-session directories: no grouping indent
- Multi-session directories: group header + indented sessions

### Sidebar (Collapsed)

```
┌──────┐
│  ⌂   │  ← home
│──────│
│  ●2  │  ← claude-manager (2 sessions, one needs attention)
│  ○   │  ← trackour (grouped)
│  ●   │  ← trackour ⎇ fix/login
│──────│
│  +   │  ← new session
└──────┘
```

- Badge shows session count when > 1
- State indicator shows "worst" state in group

### Dashboard (Home)

```
┌─ WAITING FOR INPUT ─────────────────────────────┐
│                                                 │
│ claude-manager · main                      ● 2  │
│ ┌─────────────────────────────────────────────┐ │
│ │ session 1         "needs approval"      ⌘1  │ │
│ │ session 2         "waiting for input"   ⌘2  │ │
│ └─────────────────────────────────────────────┘ │
│                                                 │
│ trackour · fix/login                   ⎇   ● 1  │
│ ┌─────────────────────────────────────────────┐ │
│ │ fixing bug        "question about..."   ⌘7  │ │
│ └─────────────────────────────────────────────┘ │
│                                                 │
└─────────────────────────────────────────────────┘
```

**Grouping order:**
1. First by state (Waiting → Working → Idle)
2. Within state, by directory
3. Directory header with session count
4. Individual sessions with labels/status

## Session Lifecycle

### Worktree Cleanup Prompt

When closing a worktree session (and no other sessions in that worktree):

```
┌────────────────────────────────────┐
│  Session closed                    │
│                                    │
│  Keep worktree for later?          │
│                                    │
│  [Keep]  [Delete]  [Always keep]   │
└────────────────────────────────────┘
```

- **Keep**: Default, safe for WIP
- **Delete**: Removes worktree + branch if merged
- **Always keep**: Remember preference, stop asking

### Stale Worktree Cleanup

Surface in settings or as notification:

```
┌────────────────────────────────────────────────────┐
│  🧹 STALE WORKTREES                                │
│                                                    │
│  These worktrees have merged branches:             │
│                                                    │
│  □ trackour--pr-42 (fix/login-bug) - merged 3d ago │
│  □ trackour--pr-38 (docs/readme) - merged 1w ago   │
│                                                    │
│                    [Keep All]  [Delete Selected]   │
└────────────────────────────────────────────────────┘
```

## Backend Design

### Data Model

```go
type Session struct {
    ID         string
    Label      string
    Directory  string    // groups sessions together
    Branch     string    // current git branch
    IsWorktree bool      // true if in a git worktree
    MainRepo   string    // if worktree, path to main repo
    State      string
    StateSince time.Time
    Todos      []string
    LastSeen   time.Time
    Muted      bool
}

type Worktree struct {
    Path      string    // ~/projects/repo--feature-xyz
    Branch    string    // feature/xyz
    MainRepo  string    // ~/projects/repo
    CreatedAt time.Time
}
```

### Git Monitor

New component: `internal/git/monitor.go`

```go
type BranchInfo struct {
    Branch     string
    IsWorktree bool
    MainRepo   string
}

func GetBranchInfo(dir string) (*BranchInfo, error) {
    // git rev-parse --abbrev-ref HEAD → branch name
    // git rev-parse --show-toplevel → repo root
    // git worktree list → detect worktrees
}
```

### Daemon Branch Polling

Poll all sessions every 5 seconds:

```go
func (d *Daemon) startBranchMonitor() {
    ticker := time.NewTicker(5 * time.Second)
    for range ticker.C {
        for _, session := range d.store.AllSessions() {
            info, err := git.GetBranchInfo(session.Directory)
            if err != nil {
                continue
            }
            if info.Branch != session.Branch || info.IsWorktree != session.IsWorktree {
                session.Branch = info.Branch
                session.IsWorktree = info.IsWorktree
                session.MainRepo = info.MainRepo
                d.store.Update(session)
                d.wsHub.Broadcast(&protocol.BranchChangedEvent{
                    SessionID: session.ID,
                    Branch:    info.Branch,
                    IsWorktree: info.IsWorktree,
                })
            }
        }
    }
}
```

### Worktree Registry

Daemon maintains awareness of all worktrees:

```go
type WorktreeRegistry struct {
    mu        sync.RWMutex
    worktrees map[string]*Worktree  // path → worktree
}

func (r *WorktreeRegistry) ForRepo(mainRepo string) []*Worktree
func (r *WorktreeRegistry) Add(wt *Worktree)
func (r *WorktreeRegistry) Remove(path string)
func (r *WorktreeRegistry) ScanRepo(mainRepo string) []*Worktree
```

**Update triggers:**
- Session registered → detect if worktree, add to registry
- "Open" PR action → create worktree, add to registry
- LocationPicker opens → scan selected repo's worktrees
- Session close → check for orphaned worktrees

### Protocol Updates

```go
// New event types
const (
    EventBranchChanged = "branch_changed"
    EventWorktreeCreated = "worktree_created"
    EventWorktreeDeleted = "worktree_deleted"
)

type BranchChangedEvent struct {
    SessionID  string `json:"session_id"`
    Branch     string `json:"branch"`
    IsWorktree bool   `json:"is_worktree"`
}

type WorktreeCreatedEvent struct {
    Path     string `json:"path"`
    Branch   string `json:"branch"`
    MainRepo string `json:"main_repo"`
}

// New commands
const (
    CmdCreateWorktree = "create_worktree"
    CmdDeleteWorktree = "delete_worktree"
    CmdListWorktrees  = "list_worktrees"
)

type CreateWorktreeMessage struct {
    Cmd      string `json:"cmd"`
    MainRepo string `json:"main_repo"`
    Branch   string `json:"branch"`
    Path     string `json:"path"`  // optional, auto-generated if empty
}
```

### Git Operations

New component: `internal/git/worktree.go`

```go
func CreateWorktree(mainRepo, branch, path string) error {
    // git worktree add <path> <branch>
    // or: git worktree add -b <branch> <path> for new branches
}

func DeleteWorktree(path string) error {
    // git worktree remove <path>
}

func ListWorktrees(mainRepo string) ([]*Worktree, error) {
    // git worktree list --porcelain
}

func IsBranchMerged(mainRepo, branch string) (bool, error) {
    // git branch --merged main | grep <branch>
}
```

## Edge Cases

| Situation | Behavior |
|-----------|----------|
| Not a git repo | Branch shows as "—", no worktree options |
| Detached HEAD | Branch shows short SHA: `a3b2c1d` |
| Branch switch mid-session | Daemon detects, broadcasts update |
| Repo deleted while session active | Branch shows "—", session continues |
| Worktree path already exists | Increment suffix: `repo--branch-2` |
| Branch already has worktree | Reuse existing worktree |
| Multiple sessions close same worktree | Only prompt cleanup for last session |

## Implementation Order

1. **Git monitor** - Branch polling for all sessions
2. **Protocol updates** - New events and commands
3. **Session display** - Branch + worktree indicator in UI
4. **Session grouping** - Sidebar and dashboard grouping by directory
5. **Worktree registry** - Daemon tracking of worktrees
6. **LocationPicker updates** - Worktree selection UI
7. **PR "Open" action** - One-click worktree creation
8. **Keyboard shortcuts** - `⌘⇧N` for worktree flow
9. **Cleanup prompts** - Session close + stale worktree detection
