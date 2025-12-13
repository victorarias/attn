# PR Detail Refresh Strategy

## Overview

Smart refresh strategy for PR details (CI status, head SHA, mergeable state) that balances freshness with API quota efficiency.

**Principle:** PRs you interact with or that change should refresh more frequently. Muted PRs are completely ignored.

---

## PR Heat State Model

Three heat states tracked per PR:

| State | Refresh Interval | Transitions To |
|-------|------------------|----------------|
| HOT | 30 seconds | WARM after 3 min of no activity |
| WARM | 2 minutes | COLD after 10 min of no activity |
| COLD | 10 minutes | Stays cold unless triggered |

### What Triggers HOT

- User clicks PR link
- User approves PR
- User unmutes PR (individual or repo)
- Comment count changes (detected during list poll)
- Head SHA changes (detected during detail fetch)

### Timer Logic

On each detail refresh, check `last_heat_activity_at`:
- If < 3 min ago → stay HOT
- If 3-10 min ago → transition to WARM
- If > 10 min ago → transition to COLD

### Muted PRs

Completely skipped:
- No heat tracking
- No detail fetching
- No change detection
- Not shown in UI

---

## Two-Phase Polling

### Phase 1: List Poll (every 90s)

Calls `FetchAll()` - 3 GitHub search API calls:
- `is:pr is:open author:@me`
- `is:pr is:open review-requested:@me`
- `is:pr is:open reviewed-by:@me`

Returns all PRs with basic info including `comment_count`.

### Phase 2: Detail Fetch (variable interval)

For each visible PR (not muted, repo not muted), check if refresh needed:

```
time_since_last_detail = now - pr.details_fetched_at

if pr.heat == HOT and time_since_last_detail > 30s:
    fetch details
elif pr.heat == WARM and time_since_last_detail > 2min:
    fetch details
elif pr.heat == COLD and time_since_last_detail > 10min:
    fetch details
elif pr.comment_count != pr.last_seen_comment_count:
    fetch details
    set HOT
```

### Immediate Fetch Triggers

Outside the poll cycle:
- User clicks PR → fetch details now, set HOT
- User approves PR → fetch details now, set HOT
- User unmutes PR/repo → fetch details for affected PRs, set HOT

### API Calls Per Detail Fetch

2 calls per PR:
1. `/repos/{owner}/{repo}/pulls/{number}` - mergeable state, head SHA
2. `/repos/{owner}/{repo}/commits/{sha}/check-runs` - CI status

Skip reviews endpoint (already have `approved_by_me` from search API).

---

## Change Detection & "Updated" Badge

### What Counts as a Change

| Change Type | Detection Method | Badge For |
|-------------|------------------|-----------|
| New commits | `head_sha` differs from `last_seen_sha` | All PRs |
| New comments | `comment_count` increased | All PRs |
| CI finished | `ci_status` changed `pending` → `success`/`failure` | Authored + Approved PRs only |

### Clearing the Badge

User clicks PR link:
- `last_seen_sha = head_sha`
- `last_seen_comment_count = comment_count`
- `last_seen_ci_status = ci_status`
- Badge clears

---

## Database Schema Changes

### Add to `prs` table

```sql
ALTER TABLE prs ADD COLUMN heat_state TEXT NOT NULL DEFAULT 'cold';
ALTER TABLE prs ADD COLUMN last_heat_activity_at TEXT;
```

### Add to `pr_interactions` table

```sql
ALTER TABLE pr_interactions ADD COLUMN last_seen_ci_status TEXT;
```

Heat state values: `'hot'`, `'warm'`, `'cold'`

---

## Code Changes

| File | Changes |
|------|---------|
| `internal/store/store.go` | Add `UpdatePRHeat()`, `GetPRsNeedingDetailRefresh()`, heat decay logic |
| `internal/store/sqlite.go` | Schema migration for new columns |
| `internal/daemon/daemon.go` | New `doDetailRefresh()` called after list poll, immediate fetch on interaction |
| `internal/daemon/websocket.go` | Trigger immediate detail fetch + set HOT on click/approve/unmute |
| `internal/protocol/types.go` | Add `HeatState` field to PR struct (optional, for debugging) |

---

## Rate Limit Estimates

GitHub API limit: 5000 requests/hour

### Worst Case

- 15 visible PRs, all HOT
- Every 90s: 3 (list) + 30 (details) = 33 calls
- Per hour: ~1320 calls

### Typical Case

- 2-3 HOT, 5 WARM, 7 COLD
- Per poll: 3 (list) + 6 (HOT) + ~3 (WARM) + ~1 (COLD) = ~13 calls
- Per hour: ~520 calls

---

## Mute Behavior

| Phase | Muted PRs | Muted Repos |
|-------|-----------|-------------|
| List poll (GitHub API) | Fetched (can't filter) | Fetched (can't filter) |
| Store in DB | Yes (need to track mute state) | Yes |
| Detail fetch | **Skip** | **Skip** |
| Heat tracking | **Skip** | **Skip** |
| Change detection | **Skip** | **Skip** |
| Show in UI | No | No |

When unmuted: PR appears immediately (already in DB), starts HOT, fetches details immediately.
