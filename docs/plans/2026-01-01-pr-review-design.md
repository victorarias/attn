# PR Review in attn

## Overview

Integrate PR review workflow into attn to eliminate context-switching to GitHub UI. This is an evolution of the existing DiffOverlay into a full review experience.

**Use cases:**
1. Review agent-written code before/after creating a PR
2. Have Claude review PRs on-demand (replaces GitHub Action)
3. Leave comments locally, send to main session for fixes

## Design Decisions

- **Local-first comments** - Comments stored in SQLite, not published to GitHub (for now)
- **Review = continuous process** - Starts on branch, PR creation just adds metadata
- **Manual Claude review trigger** - User decides when to ask Claude, not automatic
- **Reviewer is a Go agent** - Uses claude-agent-sdk-go, not Claude Code (faster, safer, cheaper)
- **Modal UI** - Evolved DiffOverlay, can evolve to replace terminal area later

## Data Model

```sql
CREATE TABLE reviews (
    id TEXT PRIMARY KEY,
    branch TEXT NOT NULL,
    pr_number INTEGER,           -- nullable, filled when PR created
    repo_path TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE review_comments (
    id TEXT PRIMARY KEY,
    review_id TEXT NOT NULL REFERENCES reviews(id),
    filepath TEXT NOT NULL,
    line_start INTEGER NOT NULL,
    line_end INTEGER NOT NULL,
    content TEXT NOT NULL,
    author TEXT NOT NULL,        -- "user" or "agent"
    resolved INTEGER DEFAULT 0,  -- boolean: 0 = unresolved, 1 = resolved
    created_at TEXT NOT NULL
);

CREATE TABLE reviewer_sessions (
    id TEXT PRIMARY KEY,
    review_id TEXT NOT NULL REFERENCES reviews(id),
    head_sha TEXT NOT NULL,      -- commit SHA when review was triggered
    transcript TEXT NOT NULL,    -- JSON: full conversation transcript
    created_at TEXT NOT NULL
);
```

**Lifecycle:**
1. First review on branch → creates `reviews` row
2. Comments added → `review_comments` rows
3. Claude review triggered → `reviewer_sessions` row with transcript
4. PR created → updates `reviews.pr_number`
5. Branch deleted (merged/closed) → archive or cleanup

## Reviewer Agent

**Implementation:** Go agent using `victorarias/claude-agent-sdk-go`

**Built-in tools (from claude-agent-sdk-go):**
- `Read` - read any file in the repo
- `Grep` - ripgrep search across codebase
- `Glob` - list files by pattern

**Excluded built-in tools:**
- `Write`, `Edit`, `bash`, `WebSearch`

**Custom tools (MCP server hosted by attn daemon):**
- `get_diff(path?)` - get diff for file or full branch
- `get_changed_files()` - list files in the branch diff
- `add_comment(filepath, line_start, line_end, content)` - add review comment
- `list_comments()` - see existing comments with resolved status

**Contextual intelligence:**
On subsequent review triggers, the agent receives:
- Previous review transcript
- Diff of new commits since last review
- Current unresolved comments
- Resolved comments (for context)

## UI: Review Panel (Evolved DiffOverlay)

**Entry points:**
- CHANGES panel → "Review" button next to title
- Branch/PR in sidebar → click opens review

**Layout (modal, full-screen):**
```
┌──────────────────────────────────────────────────────────────┐
│ review: feature-branch → origin/main          2/8 files [×] │
├─────────────────┬────────────────────────────────────────────┤
│ NEEDS REVIEW    │  @@ -10,3 +10,5 @@           [▲ 10][▼ 10] │
│ ✓ src/foo.ts    │  - old line                                │
│ 💬 bar.tsx   2  │  + new line                                │
│   src/baz.ts    │                                            │
│                 │  💬 comment popover                        │
│ AUTO-SKIP       │  ┌────────────────────────────────────┐    │
│ ⊘ pnpm-lock     │  │ check null here                    │    │
│                 │  │ [Cancel] [Save] [Resolve] [Send CC]│    │
│                 │  └────────────────────────────────────┘    │
├─────────────────┴────────────────────────────────────────────┤
│ ▾ Claude Review (3 unresolved)                               │
│ ─────────────────────────────────────────────────────────────│
│ ## Authentication Implementation Review                      │
│                                                              │
│ This PR introduces a complete auth flow...                   │
│ The token validation in `src/login.tsx:19` uses...           │
└──────────────────────────────────────────────────────────────┘
```

**File list:**
- Groups: "Needs review" / "Auto-skip"
- Auto-skip: hardcoded defaults + `.gitattributes` `linguist-generated` detection
- Icons: ✓ = viewed, 💬 = has comments, ⊘ = auto-skipped
- Unresolved comment badge (blue number) next to filename
- Shows +/- line counts
- Path abbreviation for deep paths (`.../api/routes.go`)

**Diff viewer:**
- CodeMirror 6 based (new implementation, more control over UX)
- Default: diff hunks only
- Hunk controls: `▲ 10` / `▼ 10` buttons to expand N lines up/down incrementally
- `e` / `E` for quick expand around cursor / full file
- Click line number or select text → comment popover

**Claude review panel (collapsible bottom):**
- Shows markdown-formatted review brief (not structured list)
- File references are clickable (jump to file:line)
- Badge shows unresolved comment count
- Expands to ~400px when visible

**Keyboard navigation:**
| Key | Action |
|-----|--------|
| `j/k` | Move in file list |
| `n/p` | Next/prev file (sequential) |
| `]` | Next file needing review (skips viewed + auto-skip) |
| `e` | Expand context around current hunk |
| `E` | Show full file |
| `c` | Add comment at cursor |
| `r` | Trigger Claude review |
| `Esc` | Close panel |

**Comments:**
- 💬 gutter markers on lines with comments
- Click to expand popover
- Saved comments show author badge: "Claude" (amber) or "You" (blue) + timestamp
- New comment actions: Cancel, Save, Send to Claude Code
- Saved comment actions: Cancel, Save, **Resolve**, Send to Claude Code
- Resolve button only appears after comment is saved (on right side)
- Resolved comments are tracked but visually dimmed

## Integration Points

**With existing attn features:**

1. **CHANGES panel** - Add "Review" button, reuses git status data
2. **Sessions** - "Send to Session" routes to the session that created the branch
3. **PRs sidebar** - PRs link to their review (if exists)
4. **Worktrees** - Review can work on any worktree's changes

**With Claude Code (main session):**

- "Send to Session" extends existing DiffOverlay pattern (already has "send to Claude Code")
- Pastes context: filepath, line range, comment text
- User chats with main session to fix issues
- Main session is Claude Code (full power), reviewer is read-only agent

## Implementation Phases

### Phase 1.0: Basic Review Panel
- New modal with file list + CodeMirror 6 diff viewer
- Keyboard navigation (j/k/n/p/]/Esc)
- Auto-skip detection (hardcoded defaults + gitattributes)
- Diff hunks with expand on demand (e/E)
- Entry point: button next to CHANGES
- Auto-select first file (no empty state)

**Verify:** Can navigate files, view diffs, skip lockfiles

### Phase 1.1: Viewed Tracking (Persisted)
- Track which files have been viewed per review
- Checkmark in file list (✓)
- `]` skips viewed files
- Persist in SQLite (reviews table or separate)

**Verify:** Close and reopen review, viewed state preserved

### Phase 2: Comments
- SQLite: `review_comments` table
- Add, view, resolve/unresolve comments
- Comment markers in gutter (💬)
- "Send to Claude Code" on comments

**Verify:** Comments survive restart, can send to main session

### Phase 3: Reviewer Agent
- Integrate claude-agent-sdk-go
- Built-in tools: `Read`, `Grep`, `Glob` (read-only)
- Custom MCP tools: `get_diff`, `get_changed_files`, `add_comment`, `list_comments`
- Streaming response in collapsible bottom panel
- Findings auto-create comments
- Click finding → jump to file/line

**Verify:** Claude can explore codebase, review, and leave comments

### Phase 4: Contextual Intelligence
- Store reviewer transcripts (`reviewer_sessions` table)
- Inject previous review context on re-trigger
- Show diff of changes since last review

**Verify:** Second review knows about first review's findings

## Resolved Questions

1. **CodeMirror** - Use CodeMirror 6 for the review panel (not Monaco). More control over UX, and this is a new component anyway.

2. **Auto-skip patterns** - Hardcoded defaults + gitattributes detection:
   - Defaults: `pnpm-lock.yaml`, `go.sum`, `package-lock.json`, `yarn.lock`, `Cargo.lock`
   - Check `.gitattributes` for `linguist-generated` or `diff=generated` markers
   - No config UI for now

3. **Branch selection** - Implicit, no selector:
   - Always reviews current session's branch vs `origin/main` (or default branch)
   - Branch is snapshotted when review starts
   - If branch changes mid-session, we don't handle it (edge case, defer)

4. **PR creation** - No dedicated button. Instead:
   - "Create PR" action sends `commit and create pr` to main Claude Code session
   - Keeps PR creation in Claude Code where it has full context

## Non-Goals (for now)

- Publishing comments to GitHub
- Threaded comment discussions
- Reviewing PRs from other people (focus is personal workflow)
- Automatic review triggers (manual only)
