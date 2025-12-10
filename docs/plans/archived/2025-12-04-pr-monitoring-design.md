# PR Monitoring for claude-manager

## Overview

Extend claude-manager to monitor GitHub PRs alongside Claude sessions. PRs that need your attention appear in the dashboard and tmux status bar, using the same "waiting/working" mental model.

## What Gets Tracked

**Your authored PRs** - waiting when:
- CI passed + approved → ready to merge
- CI failed → needs fixing
- Changes requested → needs fixing

**PRs requesting your review** - waiting when:
- Review not yet submitted

## Data Model

```go
type PR struct {
    ID          string    // "owner/repo#number"
    Repo        string    // "owner/repo"
    Number      int
    Title       string
    URL         string
    Role        string    // "author" or "reviewer"
    State       string    // "waiting" or "working"
    Reason      string    // "ready_to_merge", "ci_failed", "changes_requested", "review_needed"
    LastUpdated time.Time
    LastPolled  time.Time
    Muted       bool
}
```

## GitHub Polling

New `internal/github` package using `gh` CLI:

- Poll every 90 seconds for active PRs
- Poll muted PRs once per day (skip if polled within 24 hours)
- Uses `gh pr list --json` to fetch data (handles auth automatically)
- Fetches: authored PRs (`--author @me`) and review requests (`--search "review-requested:@me"`)

### State Determination

For each PR, determine state from:
- `statusCheckRollup` - CI status
- `reviewDecision` - approved/changes_requested/review_required
- `mergeable` - can be merged

## Store Changes

```go
type Store struct {
    sessions map[string]*protocol.Session
    prs      map[string]*protocol.PR
}

func (s *Store) SetPRs(prs []*protocol.PR)    // Replace all, preserve muted state
func (s *Store) ListPRs(filter string) []*PR
func (s *Store) ToggleMutePR(id string)
```

PRs are fully replaced each poll cycle. Muted state preserved by checking existing map before overwrite.

## Protocol Changes

New commands:
- `query_prs` - list PRs with optional state filter
- `mute_pr` - toggle mute on a PR

## Dashboard UI

Horizontal split layout:

```
┌─ Sessions ─────────────────────┬─ Pull Requests (2) ────────────────────┐
│ ● foo-project   working        │ ⬡ my-feature    merge   owner/repo#123 │
│ ● bar-api       waiting  2m    │ ⬡ fix-bug       review  other/repo#456 │
└────────────────────────────────┴────────────────────────────────────────┘
```

### Keybindings

- `Tab` or `h/l` - switch between panes
- `j/k` - navigate within pane
- `m` - mute/unmute selected item
- `M` - toggle showing muted PRs (hidden by default)
- `Enter` on PR - open in browser (`gh pr view --web`)

### PR State Indicators

- `merge` (green) - ready to merge
- `fix` (red/yellow) - CI failed or changes requested
- `review` (yellow) - needs your review
- `wait` (dim) - waiting on others

## Status Bar

Combined format:

```
2 waiting: foo, bar | 1 PR: my-feature
```

If nothing needs attention:
```
✓ all clear
```

## Daemon Logging

Log to `~/.claude-manager/daemon.log`:
- Startup/shutdown
- PR poll results (count, state changes)
- Errors and retries
- Rate limit events

Control verbosity with `CM_DEBUG` env var (existing mechanism).
Rotate/truncate at 10MB.

## Error Handling

**`gh` CLI not available:**
- PR features gracefully disabled
- Dashboard shows "PRs: gh CLI not configured"
- Check once at startup, warn once

**API failures:**
- Retry with exponential backoff
- Show stale data with indicator
- Log errors, don't crash

**Rate limiting:**
- Back off to 5-minute intervals temporarily
- Parse `gh` error messages to detect
