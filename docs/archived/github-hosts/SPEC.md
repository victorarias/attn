# GitHub Enterprise Support Specification

## Overview

Add support for multiple GitHub hosts (github.com + GitHub Enterprise instances) with automatic discovery via the `gh` CLI.

## Goals

1. **Zero configuration**: Auto-discover authenticated hosts from `gh auth status --json hosts`
2. **Multi-host support**: Poll PRs from all authenticated hosts simultaneously
3. **Seamless routing**: Actions (approve, merge) route to the correct host automatically
4. **No collisions**: PRs from different hosts with same repo name are distinguishable

## Requirements

- **gh CLI v2.81.0+** required (for `gh auth status --json hosts`)
- Clear error message if version is too old

## Architecture Changes

### 1. PR Identification

**Current**: `owner/repo#number` (e.g., `acme/widget#42`)

**New**: `host:owner/repo#number` (e.g., `github.com:acme/widget#42`, `ghe.corp.com:acme/widget#42`)

This prevents collisions when the same org/repo name exists on multiple hosts.

**Shorthand**: For display purposes, only display the host name if the repository supports more than one host. Otherwise, the host is omitted, but the full ID is always used internally.

### 2. GitHub Client Registry

Replace single `ghClient` with a registry that manages multiple clients:

```go
type ClientRegistry struct {
    clients map[string]*Client  // host -> client
    mu      sync.RWMutex
}

func (r *ClientRegistry) Get(host string) (*Client, bool)
func (r *ClientRegistry) Hosts() []string
func (r *ClientRegistry) FetchAllPRs() ([]*protocol.PR, error)  // aggregates all hosts
```

**Client creation per host**:
- API URL: `github.com` → `https://api.github.com`, others → `https://<host>/api/v3`
- Token: `gh auth token -h <host>`

**API URL assumption**: The `gh` CLI does not expose API URLs programmatically. We assume the standard GHES pattern `https://<host>/api/v3`. Custom ports or non-standard schemes are **not supported**.

**New client factory**: Create `NewClientForHost(host, token string)` that bypasses env var logic to avoid token cross-contamination between hosts.

### 3. Host Discovery

On daemon startup:

```bash
gh auth status --json hosts
```

Returns:
```json
{
  "hosts": {
    "github.com": [{"state": "success", "active": true, ...}],
    "ghe.corp.com": [{"state": "success", "active": true, ...}]
  }
}
```

For each host with `state: "success"`:
1. Get token via `gh auth token -h <host>`
2. Create client with appropriate API URL
3. Register in ClientRegistry

**Refresh**: Re-discover hosts periodically (every 5 minutes) or on demand.

### 4. Database Schema

Add `host` column to `prs` table and migrate related tables:

```sql
-- Migration for prs table
ALTER TABLE prs ADD COLUMN host TEXT NOT NULL DEFAULT 'github.com';

-- Update primary key concept (SQLite doesn't support PK changes, so use unique index)
CREATE UNIQUE INDEX idx_prs_host_repo_number ON prs(host, repo, number);

-- Update ID format in existing rows
UPDATE prs SET id = 'github.com:' || id WHERE id NOT LIKE '%:%';

-- Migration for pr_interactions table (preserve visit/approval history)
UPDATE pr_interactions SET pr_id = 'github.com:' || pr_id WHERE pr_id NOT LIKE '%:%';
```

**Note**: The `id` column remains the primary key but now includes the host prefix.

**Repo state table (`repos`) stays unchanged**: Mute/collapse operations are intentionally **global** across hosts. If you mute `acme/widget`, it's muted whether the PR is from github.com or GHE. This matches user expectation - same repo name = same project.

### 5. Protocol Changes

**PR struct** (TypeSpec):
```typespec
model PR {
  id: string;      // Now "host:owner/repo#number"
  host: string;    // New field: "github.com" or "ghe.corp.com"
  repo: string;    // Still "owner/repo"
  number: int32;
  // ... rest unchanged
}
```

**WebSocket commands** - change to use PR `id` instead of `repo`+`number`:

**Before** (current):
```typespec
model ApprovePRMessage {
  cmd: "approve_pr";
  repo: string;
  number: int32;
}
```

**After** (new):
```typespec
model ApprovePRMessage {
  cmd: "approve_pr";
  id: string;  // "host:owner/repo#number" - contains routing info
}

model MergePRMessage {
  cmd: "merge_pr";
  id: string;  // "host:owner/repo#number"
  method: string;
}

model FetchPRDetailsMessage {
  cmd: "fetch_pr_details";
  id: string;  // "host:owner/repo#number" - route to correct host
}
```

This is a **breaking protocol change** - bump `ProtocolVersion`.

### 6. PR Polling

**Current flow**:
```
pollPRs() → ghClient.FetchAll() → store.SetPRs(prs)
```

**New flow**:
```
pollPRs() → for each host in registry:
              client.FetchAll() → tag PRs with host
            → merge all PRs
            → store.SetPRs(prs)
```

Each PR gets its `host` and `id` fields set based on which client fetched it.

**Rate limiting**: Track per-host. If one host is rate-limited, continue polling others.

### 7. PR Actions Routing

When handling `approve_pr` or `merge_pr`:

1. Parse host from PR ID (e.g., `ghe.corp.com:acme/widget#42` → host=`ghe.corp.com`)
2. Get client from registry: `registry.Get(host)`
3. Call action on that client

```go
func (d *Daemon) handleApprovePR(id string) error {
    host, repo, number := parsePRID(id)
    client, ok := d.ghRegistry.Get(host)
    if !ok {
        return fmt.Errorf("no client for host %s", host)
    }
    return client.ApprovePR(repo, number)
}
```

### 8. Frontend Changes

**Minimal changes required**:
- PR ID format change is transparent (IDs come from daemon)
- PR URLs already come from daemon (no hardcoded github.com)
- Actions use PR ID which now routes correctly

**Optional UI enhancements**:
- Show host badge/icon when the same repo appears on multiple hosts
- Group PRs by host in the list (optional)
- Settings page showing connected hosts (derived from PRs if no explicit host list is available)

### 9. Error Handling

**Version check**: On daemon startup, run `gh --version`, parse output, fail if < 2.81.0:
```
GitHub Enterprise support requires gh CLI v2.81.0 or later.
Current version: 2.75.0
Please upgrade: brew upgrade gh
```

**Partial failures**: If one host fails auth, log warning and continue with others. Don't fail entire PR feature because one host is down.

**No hosts authenticated**: If `gh auth status` returns empty hosts, log info and disable PR features (current behavior).

## Migration Path

1. **Database migration**: Add `host` column, update existing IDs to include `github.com:` prefix
2. **Protocol version bump**: Increment `ProtocolVersion` since PR ID format changes
3. **Backward compatibility**: None needed - this is a breaking change to PR IDs

## What Stays the Same

- Git operations (branches, worktrees, stash) - purely local, no GitHub dependency
- Session management - no GitHub dependency
- Review comments storage - stored locally, not on GitHub
- CLI wrapper - no changes needed

## Testing

1. **Unit tests**: Mock `gh auth status` output with multiple hosts
2. **Integration tests**: Mock GitHub API server per host
3. **Manual testing**: Need access to a GHE instance (or mock one)

## Configuration

**None required** - discovery is automatic via `gh` CLI.
