# PR State Tracking - Two-Phase Plan

## Overview

**Principle**: A PR needs attention if it changed since you last interacted with it. Approved PRs without changes don't need attention but should remain visible until merged.

---

# Plan 1: SQLite Migration

## Goal
Replace JSON file persistence with SQLite for reliability. Make all paths configurable. Fix E2E test isolation.

## Configuration

**Priority** (highest to lowest):
1. Environment variables
2. Config file (`~/.attn/config.json`)
3. Defaults

| Path | Env Var | Config Key | Default |
|------|---------|------------|---------|
| Database | `ATTN_DB_PATH` | `db_path` | `~/.attn/attn.db` |
| Socket | `ATTN_SOCKET_PATH` | `socket_path` | `~/.attn/attn.sock` |
| Config | `ATTN_CONFIG_PATH` | n/a | `~/.attn/config.json` |

## Schema

```sql
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  directory TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'idle',
  state_since TEXT NOT NULL,
  state_updated_at TEXT NOT NULL,
  todos TEXT,
  last_seen TEXT NOT NULL,
  muted INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE prs (
  id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  number INTEGER NOT NULL,
  title TEXT NOT NULL,
  url TEXT NOT NULL,
  role TEXT NOT NULL,
  state TEXT NOT NULL,
  reason TEXT,
  last_updated TEXT NOT NULL,
  last_polled TEXT NOT NULL,
  muted INTEGER NOT NULL DEFAULT 0,
  details_fetched INTEGER NOT NULL DEFAULT 0,
  details_fetched_at TEXT,
  mergeable INTEGER,
  mergeable_state TEXT,
  ci_status TEXT,
  review_status TEXT
);

CREATE TABLE repos (
  repo TEXT PRIMARY KEY,
  muted INTEGER NOT NULL DEFAULT 0,
  collapsed INTEGER NOT NULL DEFAULT 0
);
```

## Files to Modify

| File | Changes |
|------|---------|
| `internal/config/config.go` | Add config file loading, env var support |
| `internal/store/sqlite.go` | **NEW** - SQLite connection, schema, queries |
| `internal/store/store.go` | Replace JSON/maps with SQLite operations |
| `internal/store/store_test.go` | Update tests to use temp DB |
| `internal/daemon/daemon.go` | Remove `StartPersistence()`, delete old JSON on startup |
| `app/e2e/fixtures.ts` | Add `ATTN_DB_PATH`, `ATTN_SOCKET_PATH` env vars |

---

# Plan 2: PR Interaction Tracking

## Goal
Track user interactions with PRs. Show "updated" badge when PR changes since last interaction. Keep approved PRs visible but dimmed.

## New SQLite Tables (added in Plan 2)

```sql
CREATE TABLE pr_interactions (
  pr_id TEXT PRIMARY KEY,
  last_visited_at TEXT,
  last_approved_at TEXT,
  last_seen_sha TEXT,
  last_seen_comment_count INTEGER
);
```

## GitHub Client Changes

**File: `internal/github/client.go`**

1. Add `SearchReviewedByMePRs()`:
   - Query: `is:pr is:open reviewed-by:@me`
   - Catches PRs you've approved (no longer in `review-requested:@me`)

2. Modify `FetchAll()`:
   - Call three searches: authored, review-requested, reviewed-by-me
   - Deduplicate by PR ID
   - For reviewed-by PRs not in review-requested: mark `ApprovedByMe: true`

3. Fetch per PR:
   - `head.sha` → for change detection
   - Comment count → for change detection
   - CI status → for display

## Protocol Changes

**File: `internal/protocol/types.go`**

Add to PR struct:
```go
ApprovedByMe    bool   `json:"approved_by_me"`
HasNewChanges   bool   `json:"has_new_changes"`
HeadSHA         string `json:"head_sha"`
CommentCount    int    `json:"comment_count"`
CIStatusIcon    string `json:"ci_status_icon"`
```

Add command:
```go
CmdPRVisited = "pr_visited"
```

Bump `ProtocolVersion` to "3"

## Store Changes

**File: `internal/store/store.go`**

1. Add methods:
   - `MarkPRVisited(prID)` - updates interaction, clears `HasNewChanges`
   - `MarkPRApproved(prID)` - updates interaction

2. In `SetPRs()`:
   - Compute `HasNewChanges`: `sha != last_seen_sha || comments > last_seen_comments`

## Frontend Changes

**File: `app/src/hooks/useDaemonSocket.ts`**
- Update `DaemonPR` interface with new fields
- Add `sendPRVisited(prID)` function
- Bump `PROTOCOL_VERSION` to "3"

**File: `app/src/components/Dashboard.tsx`**
- Capture PR link clicks → call `sendPRVisited()`
- Show badges: `✓` (approved), `UPDATED` (has changes), CI dot
- Dim approved PR rows (opacity: 0.5)
- Attention count excludes approved PRs without changes

**File: `app/src/components/Dashboard.css`**
- `.badge-approved` - green checkmark
- `.badge-changes` - blue "UPDATED" label (see prototype)
- `.ci-status.success/failure/pending` - colored dots
- `.pr-row.approved` - dimmed opacity
- Gap fix: animate `max-height` on fade-out

## Files to Modify

| File | Changes |
|------|---------|
| `internal/protocol/types.go` | Add PR fields, `CmdPRVisited`, bump version |
| `internal/github/client.go` | Add `SearchReviewedByMePRs()`, fetch CI/SHA/comments |
| `internal/github/interface.go` | Add new method |
| `internal/store/store.go` | Add interaction tracking, `HasNewChanges` computation |
| `internal/daemon/websocket.go` | Handle `pr_visited` command |
| `app/src/hooks/useDaemonSocket.ts` | Update types, add `sendPRVisited()` |
| `app/src/components/Dashboard.tsx` | Badges, click tracking, dimmed rows |
| `app/src/components/Dashboard.css` | Badge styles, gap fix animation |

## Prototype

See: `docs/prototypes/pr-states-prototype.html`

## Testing

1. Approve PR → checkmark badge, row dimmed, stays visible
2. New commit on approved PR → "UPDATED" badge appears
3. Click PR link → "UPDATED" badge clears
4. Merged PR → smooth fade-out (no gap)
5. Restart daemon → interaction state preserved (SQLite)
