# GitHub Enterprise Support - Implementation Tasks

## Overview

Breaking down the SPEC.md into implementable tasks. Tasks are ordered by dependency - earlier tasks unblock later ones.

---

## Phase 1: Foundation (No Breaking Changes)

These tasks add infrastructure without changing existing behavior.

### [x] Task 1.1: Add gh CLI Version Check

**Files**: `internal/github/version.go` (new)

**Work**:
- Add `CheckGHVersion() (version string, error)` - parse `gh --version` output
- Add `RequireGHVersion(minVersion string) error` - compare versions, return clear error
- Minimum version: 2.81.0

**Tests**: Version parsing, comparison logic

**Acceptance**: Daemon exits with clear error if gh < 2.81.0

---

### [x] Task 1.2: Add Host Discovery

**Files**: `internal/github/discovery.go` (new)

**Work**:
- Add `DiscoverHosts() ([]HostInfo, error)` - runs `gh auth status --json hosts`
- Parse JSON response, extract hosts with `state: "success"`
- Add `GetTokenForHost(host string) (string, error)` - runs `gh auth token -h <host>`
- Map host to API URL: `github.com` → `api.github.com`, others → `<host>/api/v3`

**Structs**:
```go
type HostInfo struct {
    Host     string  // "github.com" or "ghe.corp.com"
    APIURL   string  // "https://api.github.com" or "https://ghe.corp.com/api/v3"
    Login    string  // username
    Active   bool
}
```

**Tests**: Parse sample JSON, token retrieval (mock exec)

**Acceptance**: `DiscoverHosts()` returns all authenticated hosts

---

### [x] Task 1.3: Create Client Registry

**Files**: `internal/github/registry.go` (new), `internal/github/interface.go` (update)

**Work**:
- Add `ClientRegistry` struct with `map[string]*Client`
- Add methods: `Get(host)`, `Hosts()`, `Register(host, client)`, `Remove(host)`
- Add `NewClientRegistry() *ClientRegistry`
- Add `NewClientForHost(host, apiURL, token string) (*Client, error)` factory function
  - **Important**: Uses explicit tokens to avoid cross-contamination between hosts

**Tests**: Registry CRUD operations

**Acceptance**: Can create/retrieve clients by host

---

### [x] Task 1.4: Integrate Registry into Daemon (Behind Flag)

**Files**: `internal/daemon/daemon.go`

**Work**:
- Add `ghRegistry *github.ClientRegistry` field alongside existing `ghClient`
- On startup, call `DiscoverHosts()`, create client for each, register
- Keep existing `ghClient` for backward compatibility (set to github.com client)
- Multi-host is the default behavior (no flag)

**Tests**: Daemon startup with mock discovery

**Acceptance**: Daemon initializes registry, existing behavior unchanged when flag off

---

## Phase 2: Data Model Changes

These tasks change the PR data model to support multiple hosts.

### [x] Task 2.1: Update Protocol Schema

**Files**: `internal/protocol/schema/main.tsp`

**Work**:
- Add `host: string` field to `PR` model
- Document new ID format in comments: `host:owner/repo#number`
- **Change action messages to use `id` instead of `repo`+`number`**:
  - `ApprovePRMessage`: replace `repo`+`number` with `id: string`
  - `MergePRMessage`: replace `repo`+`number` with `id: string`
  - `FetchPRDetailsMessage`: replace `repo` with `id: string`
- Update `PRActionResultMessage` to use `id` instead of `repo`+`number`

**Run**: `make generate-types` to update Go and TypeScript types

**Acceptance**: Generated types include `host` field, action messages use `id`

---

### [x] Task 2.2: Database Migration

**Files**: `internal/store/migrations.go` (new or extend), `internal/store/sqlite.go`

**Work**:
- Add migration to add `host` column: `ALTER TABLE prs ADD COLUMN host TEXT NOT NULL DEFAULT 'github.com'`
- Update existing IDs: `UPDATE prs SET id = 'github.com:' || id WHERE id NOT LIKE '%:%'`
- **Migrate `pr_interactions` table**: `UPDATE pr_interactions SET pr_id = 'github.com:' || pr_id WHERE pr_id NOT LIKE '%:%'`
- Add unique index on `(host, repo, number)`
- Update all SQL queries to include `host` column

**Note**: The `repos` table (mute/collapse state) is **intentionally NOT migrated** - mute/collapse is global across hosts with same repo name.

**Tests**: Migration runs cleanly, existing data preserved, pr_interactions history intact

**Acceptance**: Database has `host` column, existing PRs and interactions migrated

---

### [x] Task 2.3: PR ID Parsing Helpers

**Files**: `internal/protocol/helpers.go` (extend)

**Work**:
- Add `ParsePRID(id string) (host, repo string, number int, error)`
- Add `FormatPRID(host, repo string, number int) string`
- Handle both old format (`owner/repo#42`) and new format (`host:owner/repo#42`)
- Old format assumes `github.com` host

**Tests**: Parse/format round-trip, backward compat with old IDs

**Acceptance**: Can parse and format PR IDs in both formats

---

### [x] Task 2.4: Update GitHub Client to Tag PRs with Host

**Files**: `internal/github/client.go`

**Work**:
- Add `host` field to `Client` struct (set during creation)
- In `SearchAuthoredPRs`, `SearchReviewRequestedPRs`, `SearchReviewedByMePRs`:
  - Set `pr.Host = c.host`
  - Set `pr.ID = FormatPRID(c.host, repo, number)`

**Tests**: PRs returned have correct host and ID format

**Acceptance**: All PR search methods return PRs with host info

---

## Phase 3: Multi-Host Polling

### [x] Task 3.1: Aggregate PR Fetching

**Files**: `internal/github/registry.go`

**Work**:
- Add `FetchAllPRs() ([]*protocol.PR, error)` to `ClientRegistry`
- Iterate all clients, call `FetchAll()` on each
- Merge results, handle per-host errors gracefully (log, continue)
- Track which hosts succeeded/failed

**Tests**: Multiple mock clients, partial failure scenarios

**Acceptance**: Returns PRs from all hosts, doesn't fail if one host errors

---

### [x] Task 3.2: Per-Host Rate Limiting

**Files**: `internal/github/client.go`, `internal/github/registry.go`

**Work**:
- Rate limits already tracked per-client (existing code)
- Add `IsAnyHostRateLimited() bool` to registry
- Add `GetRateLimitedHosts() []string` for reporting

**Tests**: Rate limit tracking across multiple clients

**Acceptance**: Rate limiting works independently per host

---

### [x] Task 3.3: Update Daemon PR Polling

**Files**: `internal/daemon/daemon.go`

**Work**:
- Use `ghRegistry.FetchAllPRs()` instead of `ghClient.FetchAll()`
- Broadcast rate limit events per-host (or aggregate)
- Update detail refresh to route to correct client per PR

**Tests**: Daemon polls multiple hosts, stores PRs correctly

**Acceptance**: PRs from multiple hosts appear in store and are broadcast

---

## Phase 4: Action Routing

### [x] Task 4.1: Route Approve/Merge to Correct Host

**Files**: `internal/daemon/websocket.go`

**Work**:
- In `handleApprovePR`: parse host from PR ID, get client from registry, call approve
- In `handleMergePR`: same pattern
- In `handleFetchPRDetails`: same pattern
- Fall back to `ghClient` if host parsing fails (backward compat)

**Tests**: Actions route to correct mock client based on PR ID

**Acceptance**: Can approve/merge PRs from any authenticated host

---

### [x] Task 4.2: Update PR Details Fetch

**Files**: `internal/daemon/daemon.go`

**Work**:
- In `doDetailRefresh`, for each PR:
  - Parse host from PR ID
  - Get client from registry
  - Call `FetchPRDetails` on that client
- Handle missing client gracefully (PR from host that's no longer authenticated)

**Tests**: Detail refresh works across multiple hosts

**Acceptance**: PR details update correctly for all hosts

---

## Phase 5: Protocol Version & Cleanup

### [x] Task 5.1: Bump Protocol Version

**Files**: `internal/protocol/constants.go`

**Work**:
- Increment `ProtocolVersion`
- Document breaking change: PR ID format changed

**Acceptance**: App shows version mismatch if daemon is old

---

### [x] Task 5.2: Remove Feature Flag

**Files**: `internal/daemon/daemon.go`

**Work**:
- Remove feature flag gating
- Make multi-host the default behavior
- Remove old single-client code path

**Acceptance**: Multi-host works out of the box

---

### [x] Task 5.3: Update Documentation

**Files**: `docs/CONFIGURATION.md`, `CLAUDE.md`, `README.md`

**Work**:
- Document gh CLI version requirement (2.81.0+)
- Document multi-host support
- Update troubleshooting for GHE issues
- Remove references to `GITHUB_API_URL`/`GITHUB_TOKEN` (gh discovery only)
- Document test-only mock GitHub env vars for E2E (`ATTN_MOCK_GH_*`)

**Acceptance**: Docs reflect new behavior

---

## Phase 6: Frontend Polish (Optional)

### [x] Task 6.1: Host Badge in PR List

**Files**: `app/src/components/PRList.tsx` (or similar)

**Work**:
- For PRs where `host !== 'github.com'`, show small host badge
- Consider grouping PRs by host

**Acceptance**: GHE PRs visually distinguishable

---

### [x] Task 6.2: Connected Hosts Display

**Files**: `app/src/components/Settings.tsx` (or new)

**Work**:
- Show list of authenticated GitHub hosts
- Show auth status per host
- Link to `gh auth login` instructions for adding hosts

**Acceptance**: User can see which hosts are connected

---

## Refactoring Assessment

### Not Needed
- Git operations - already host-agnostic
- Session management - no GitHub dependency
- WebSocket infrastructure - just needs routing logic
- Frontend architecture - minimal changes

### Recommended
- **PR ID handling**: Centralize in `protocol/helpers.go` (Task 2.3)
- **Client creation**: Factory pattern in registry (Task 1.3)

### Breaking Changes
- PR ID format: `owner/repo#42` → `github.com:owner/repo#42`
- Action messages: `approve_pr`/`merge_pr`/`fetch_pr_details` now use `id` instead of `repo`+`number`
- Database migration required (both `prs` and `pr_interactions` tables)
- Protocol version bump required

---

## Implementation Order

```
Phase 1 (Foundation)     Phase 2 (Data Model)     Phase 3 (Polling)
    1.1 ──┐                  2.1 ──┐                  3.1 ──┐
    1.2 ──┼─→ 1.4            2.2 ──┼─→ 2.4            3.2 ──┼─→ 3.3
    1.3 ──┘                  2.3 ──┘                  (deps on 2.4)

Phase 4 (Actions)        Phase 5 (Cleanup)        Phase 6 (Polish)
    4.1 ──┐                  5.1                      6.1
    4.2 ──┘                  5.2                      6.2
    (deps on 3.3)            5.3
```

**Suggested milestone splits**:
1. **Milestone A** (Phases 1-2): Infrastructure + data model - can be merged without behavior change
2. **Milestone B** (Phases 3-4): Enable multi-host polling and actions
3. **Milestone C** (Phases 5-6): Cleanup and polish

---

## Testing Strategy

1. **Unit tests**: Each task includes unit tests for new code
2. **Integration tests**: Mock multiple GitHub API servers
3. **E2E tests**: Hard without real GHE - consider mock server that simulates GHE responses
4. **Manual testing**: Request access to a GHE instance or set up local mock

## Risk Areas

1. **Database migration**: Test on copy of real data before deploying
2. **ID format change**: Ensure all code paths handle new format
3. **Rate limiting**: Don't let one slow host block others
4. **Token refresh**: `gh auth token -h` should handle token refresh, but verify
