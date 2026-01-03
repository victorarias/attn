# Phase 3: Reviewer Agent Implementation Plan

## Approach: Walking Skeleton

Start with a fully connected skeleton (UI → daemon → fake agent → UI) where everything is mocked/hardcoded. Then progressively un-mock each layer. Each phase has a manual verification step.

---

## Reviewer Prompt

```
Review the changes on this branch against {base_branch}.

Provide feedback on:
- Code quality and best practices
- Potential bugs or issues
- Performance considerations
- Security concerns
- Test coverage

Use the repository's CLAUDE.md for guidance on style and conventions.
Be constructive and helpful.

Workflow:
1. Call get_changed_files() to see what changed
2. Call list_comments() to see existing feedback (yours and user's)
3. Review each file - use get_diff() and explore with Read/Grep as needed
4. Use add_comment() for new issues at specific locations
5. Use resolve_comment() if a previous issue was fixed

{{#if is_rereview}}
This is a follow-up review. Focus on changes since commit {last_review_sha}.
{{/if}}
```

---

## Phase 3.0: Walking Skeleton

**Goal:** Click "Review" button → see fake streaming output → fake comments appear in diff

### Backend (daemon)

1. Add WebSocket command `start_review` handler in daemon
2. Handler sends hardcoded streaming events:
   - `review_started`
   - `review_chunk` (3-4 fake text chunks with delays)
   - `review_finding` (2 fake findings with file:line)
   - `review_complete`
3. Findings create real comments in SQLite via existing store

### Frontend

4. Add "Review" button to ReviewPanel header (next to existing controls)
5. Add collapsible ReviewerOutput panel at bottom
6. Wire button → `sendStartReview()` → daemon
7. Handle streaming events → append to panel, show progress
8. On `review_finding` → call existing `addComment` with author="agent"

### Protocol

9. Define events in TypeSpec: `review_started`, `review_chunk`, `review_finding`, `review_complete`
10. Generate types, increment protocol version

### Tests (skeleton-level)

11. Go: Test that `start_review` command triggers event sequence
12. TS: Test that ReviewerOutput renders streaming chunks

**Verify:** Click Review → see fake text streaming in → 2 comments appear in gutter

---

## Phase 3.1: MCP Tools (Real)

**Goal:** Replace hardcoded diff/files with real git data via MCP tools

### Schema Changes

Update `ReviewComment` to track who resolved:

```go
type ReviewComment struct {
    // ... existing fields
    Resolved   bool
    ResolvedBy string     // "user" or "agent" (empty if not resolved)
    ResolvedAt *time.Time // when resolved (nil if not resolved)
}
```

1. Add `resolved_by TEXT`, `resolved_at TEXT` columns to SQLite
2. Update `ResolveComment(id, resolvedBy string)` store method
3. Update TypeSpec types, regenerate

### MCP Tools

4. Create `internal/reviewer/mcp/` package
5. Implement `get_changed_files()` - calls git, returns file list with status (added/modified/deleted)
6. Implement `get_diff(paths)` - calls git, returns diffs map (empty paths = all files)
7. Implement `list_comments()` - reads from store, includes resolved status and resolvedBy
8. Implement `add_comment(filepath, line_start, line_end, content)` - writes to store with author="agent"
9. Implement `resolve_comment(id)` - marks resolved with resolvedBy="agent"

### Tests

10. MCP tool unit tests with real temp git repo
11. MCP tool unit tests with in-memory SQLite
12. Test resolve_comment sets resolvedBy correctly

**Verify:** Write a simple Go test that calls each tool and prints output - visually confirm git data is correct

---

## Phase 3.2: Mock Transport

**Goal:** Build the test infrastructure for agent testing

### Backend

1. Create `internal/reviewer/transport/mock.go` - implements SDK's Transport interface
2. Create `internal/reviewer/transport/fixtures.go` - scripted response sequences
3. MockTransport supports:
   - Scripted message sequences
   - Configurable delays (simulate streaming)
   - Error injection at message N
   - Recording what agent sent (for assertions)

### Tests

4. Test MockTransport itself - verify it returns scripted responses
5. Test fixture builder helpers

**Verify:** Run mock transport tests, see they pass and output makes sense

---

## Phase 3.3: Agent Orchestrator (Mock LLM)

**Goal:** Real agent code, but using mock transport instead of real Claude

### Backend

1. Create `internal/reviewer/reviewer.go` - agent orchestrator
2. Integrate claude-agent-sdk-go with `WithCustomTransport()`
3. Register MCP tools from Phase 3.1
4. Agent receives: repo path, branch, base branch, existing comments
5. Agent streams events back via callback
6. Wire into daemon: `start_review` → creates agent → runs with mock transport

### Tests

7. Integration test: full flow with mock transport → comments in store
8. Integration test: cancel mid-stream → clean state
9. Integration test: error handling → error event sent

**Verify:** Click Review in UI → see mock agent's scripted responses streaming → comments appear (now through real agent code path)

---

## Phase 3.4: Playwright E2E (Mock Transport)

**Goal:** Validate the full user flow works with automated tests

### Frontend Polish (required for E2E)

1. Jump-to-file: click finding in ReviewerOutput → navigates to file:line in diff viewer
2. Cancel button: stop review mid-stream
3. Error state: show error message, retry button
4. Loading states: spinner while waiting for first chunk

### Playwright Tests

5. Happy path: start review → findings stream in → comments in gutter
6. Cancel mid-stream: click cancel → streaming stops cleanly
7. Jump-to-file: click finding → diff viewer navigates to file:line

**Verify:** `pnpm e2e --grep reviewer` passes - all 3 scenarios green

---

## Phase 3.5: Re-review Context

**Goal:** Second review knows about first review (tested with mock transport)

### Backend

1. Add `reviewer_sessions` table (per design doc)
2. Store transcript after each review
3. On re-review: load previous transcript + unresolved comments
4. Inject context into agent prompt

### Tests

5. Integration test: second review receives previous context
6. Integration test: resolved comments excluded from context
7. Playwright: re-review scenario

**Verify:** Run review, add comment, resolve it, run review again - mock agent receives previous context in prompt

---

## Phase 3.6: Real LLM

**Goal:** Replace mock transport with real Claude SDK

### Backend

1. Add build-tagged `TestRealAPI_*` tests in separate file
2. Add `make test-realapi` target
3. Switch daemon to use real transport (no mock) by default
4. Keep mock transport available for tests

### Tests

4. Real API test: basic review of small diff
5. Real API test: agent calls MCP tools correctly

**Verify:** `make test-realapi` succeeds (requires API key). Then test in real app - click Review on actual branch with changes, see Claude analyze the code.

---

## Test Summary by Phase

| Phase | Go Unit | Go Integration | TS Unit | Playwright |
|-------|---------|----------------|---------|------------|
| 3.0 Skeleton | Event sequence | - | Streaming render | - |
| 3.1 MCP Tools | Tool handlers | - | - | - |
| 3.2 Mock Transport | Transport behavior | - | - | - |
| 3.3 Agent Orchestrator | - | Full flow (mock) | - | - |
| 3.4 Playwright E2E | - | - | Interactions | 3 scenarios |
| 3.5 Re-review Context | - | Context injection | - | 1 scenario |
| 3.6 Real LLM | - | Real API (manual) | - | - |

---

## File Structure (Final)

```
internal/
  reviewer/
    reviewer.go              # agent orchestrator
    reviewer_test.go         # integration tests (mock transport)
    realapi_test.go          # build tag: realapi

    mcp/
      tools.go               # MCP tool handlers
      tools_test.go          # unit tests

    transport/
      mock.go                # MockTransport
      fixtures.go            # scripted responses

app/
  src/
    components/
      ReviewerPanel.tsx      # (existing, add Review button)
      ReviewerOutput.tsx     # streaming output panel
      ReviewerOutput.test.tsx

    hooks/
      useReviewer.ts         # reviewer state machine
      useReviewer.test.ts

  e2e/
    reviewer.spec.ts         # 4 Playwright scenarios
    mocks/
      reviewerSocket.ts
```

---

## Makefile Additions

```makefile
test-realapi:    ## Run real API tests (requires ANTHROPIC_API_KEY)
	go test ./internal/reviewer -tags=realapi -v
```
