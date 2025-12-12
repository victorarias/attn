# GitHub Rate Limiting & Single-Instance Daemon

## Problem

1. **Multiple daemons accumulate** - No single-instance protection means restarting during development leaves orphan daemons running. Each polls GitHub, quickly exhausting rate limits.

2. **No rate limit handling** - When GitHub returns 403 rate limit errors, the daemon logs the error but provides no user feedback or recovery.

## Solution

### Part 1: Single-Instance Daemon (PID File)

**Mechanism:**
- On startup, daemon writes PID to file alongside socket (e.g., `~/.attn.pid`)
- Before writing, check if file exists and if that PID is still running
- If old daemon is running, send SIGTERM and wait up to 2s for graceful shutdown
- If it doesn't die, SIGKILL
- On clean shutdown, remove the PID file

**PID file location:**
- Derived from socket path: same directory, named `attn.pid`
- Production: `~/.attn.pid` (alongside `~/.attn.sock`)
- E2E tests: `$TEMP_DIR/attn.pid` (alongside test socket)

### Part 2: Rate Limit Handling

**GitHub rate limits:**
- REST API (`core`): 5,000 requests/hour
- Search API (`search`): 30 requests/minute

**Response headers:**
- `X-RateLimit-Remaining`: Requests left in window
- `X-RateLimit-Reset`: Unix timestamp when quota resets
- `X-RateLimit-Resource`: Which limit applies (`core` or `search`)

**Client behavior:**
- Read headers from every response, update internal state
- Before requests, check if remaining < 5 for that resource
- If rate limited, return `ErrRateLimited` with reset time
- Caller decides whether to skip or wait

**Daemon behavior:**
- When poll skipped due to rate limit, broadcast `rate_limited` event
- Event includes resource type and reset timestamp
- Continue polling loop - next tick will check again

**UI behavior:**
- Show banner: "GitHub rate limited, resuming in Xm Ys"
- Auto-dismiss when rate limit resets
- Manual refresh button disabled while rate limited

## Implementation

### Files to modify

**`internal/daemon/daemon.go`**
- Add `acquirePIDLock()` called at start of `Run()`
- Add `releasePIDLock()` in shutdown handler
- Update `doPRPoll()` to handle rate limit errors
- Broadcast `rate_limited` event when poll skipped

**`internal/github/client.go`**
- Add rate limit state tracking:
  ```go
  type rateLimitState struct {
      Remaining int
      Reset     time.Time
  }
  rateLimits map[string]*rateLimitState
  mu         sync.RWMutex
  ```
- Update `doRequest()` to parse rate limit headers
- Add `IsRateLimited(resource string) (limited bool, resetAt time.Time)`
- Search calls check `search` resource, others check `core`

**`internal/protocol/types.go`**
- Add `EventRateLimited = "rate_limited"`
- Add message struct:
  ```go
  type RateLimitedMessage struct {
      Event    string    `json:"event"`
      Resource string    `json:"resource"` // "core" or "search"
      ResetAt  time.Time `json:"reset_at"`
  }
  ```

**`app/src/hooks/useDaemonSocket.ts`**
- Handle `rate_limited` event
- Store `{ resource, resetAt }` in state
- Clear when current time > resetAt

**`app/src/components/Dashboard.tsx`**
- Show rate limit banner when state is set
- Display countdown to reset
- Disable refresh button while limited

### Test considerations

- E2E tests use isolated temp directories, so PID files won't conflict
- Mock GitHub server can return rate limit headers to test handling
- Unit tests for `IsRateLimited()` logic
