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

**Tool allowlist (read-only + comment):**
- `read_file(path)` - read file contents
- `get_diff(path?)` - get diff for file or full branch
- `get_file_list()` - list changed files
- `add_comment(filepath, line_start, line_end, content)` - add review comment
- `list_comments()` - see existing comments with resolved status

**Explicitly excluded:**
- bash / shell
- write_file / edit
- Any MCP servers

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
│ feature-branch → main              [Ask Claude] [Create PR] │
├─────────────────┬────────────────────────────────────────────┤
│ Files           │ Diff viewer (Monaco)                       │
│ ────────────────│                                            │
│ ✓ src/foo.ts    │  @@ -10,3 +10,5 @@                        │
│   src/bar.ts    │  - old line                                │
│ ⊘ pnpm-lock.yaml│  + new line                                │
│                 │  + another new line                        │
│                 │                                            │
│                 │  💬 comment popover                        │
│                 │  ┌────────────────────┐                    │
│                 │  │ check null here    │                    │
│                 │  │ [Send to Session]  │                    │
│                 │  └────────────────────┘                    │
├─────────────────┴────────────────────────────────────────────┤
│ Claude Review (streaming)                                    │
│ "Reviewing 5 files... Found potential null pointer at..."   │
└──────────────────────────────────────────────────────────────┘
```

**File list:**
- Groups: "Needs review" / "Auto-skip"
- Auto-skip patterns: `pnpm-lock.yaml`, `go.sum`, `*.generated.*`, etc. (configurable)
- Icons: ✓ = viewed, ⊘ = auto-skipped
- Shows +/- line counts

**Diff viewer:**
- Monaco-based (existing, consider CodeMirror 6 migration later)
- Default: diff hunks only
- Expand on demand: `e` for context around hunk, `E` for full file
- Click line number or select text → comment popover

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
- Yellow gutter markers on lines with comments
- Hover or click to expand
- "Send to Session" button → opens main session with context
- Resolve/unresolve toggle (manual task tracking)

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

### Phase 1: Core Review UI
- Evolve DiffOverlay to multi-file review panel
- File list with navigation
- Keyboard shortcuts
- Auto-skip patterns

### Phase 2: Comments
- Comment storage (SQLite)
- Add/view/resolve comments in UI
- "Send to Session" integration

### Phase 3: Reviewer Agent
- Integrate claude-agent-sdk-go
- Implement read-only tool set
- Streaming response in review panel

### Phase 4: Contextual Intelligence
- Store reviewer transcripts
- Inject previous context on subsequent reviews
- Track what changed between reviews

## Open Questions

1. **CodeMirror migration** - Monaco works but is heavy. Migrate before or after this feature?
2. **Auto-skip configuration** - Where to configure patterns? Settings file? UI?
3. **Branch selector** - How to pick which branch to review? Dropdown in panel header?
4. **PR creation from review** - "Create PR" button exists in mockup. What info to pre-fill?

## Non-Goals (for now)

- Publishing comments to GitHub
- Threaded comment discussions
- Reviewing PRs from other people (focus is personal workflow)
- Automatic review triggers (manual only)
