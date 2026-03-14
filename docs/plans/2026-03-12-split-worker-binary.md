# Split Worker into Separate Binary

**Status**: Proposed
**Date**: 2026-03-12

## Problem

CrowdStrike's ML model flags the `attn` binary because `claude-agent-sdk-go` contains C2-like patterns (subprocess spawning, WebSocket comms, JSON command parsing). The binary gets quarantined on write, so the daemon (surviving in memory) can't spawn new worker processes.

## Key Insight

The worker process (`attn pty-worker`) doesn't need the agent SDK. The transitive dependency chain is:

```
ptyworker -> internal/pty -> internal/agent -> internal/classifier -> claude-agent-sdk-go
```

`internal/pty/manager.go` imports `internal/agent` for only three things:
1. Check if state detection is enabled for an agent
2. Validate agent names
3. Look up agent executable env vars

All three can be dependency-injected.

## Solution

Split into two binaries:
- **`attn`** — full binary (daemon, wrapper, CLI). Contains agent SDK. Gets flagged but only runs once.
- **`attn-worker`** — minimal binary (PTY management only). No agent SDK. CrowdStrike ignores it.

## Implementation Phases

### Phase 1: Decouple `internal/pty` from `internal/agent`

Remove the `agentdriver` import from `internal/pty/manager.go` by injecting the three dependencies:

1. **State detector enablement** (line ~220): Add `DisableStateDetector bool` to `SpawnOptions`. Daemon sets this based on agent capabilities at spawn time.

2. **Agent name validation** (line ~369): Add `SetAgentValidator(func(string) bool)` method on `Manager`. Daemon initializes from the agent registry. Worker accepts any agent name (daemon already validated).

3. **Executable env var resolution** (line ~427): Add `AgentEnvConfig` struct to `SpawnOptions`:
   ```go
   type AgentEnvConfig struct {
       EnvVar            string
       DefaultExecutable string
   }
   ```
   Daemon populates from agent registry. When nil, fall back to hardcoded switch.

**Result**: `internal/pty` has zero imports from `internal/agent`.

### Phase 2: Create `cmd/attn-worker/main.go`

Move the `runPTYWorker()` logic from `cmd/attn/main.go` into a new entry point. Imports only `internal/ptyworker` → `internal/pty` (clean).

Verify: `go list -deps ./cmd/attn-worker | grep claude-agent-sdk` returns nothing.

### Phase 3: Update daemon to spawn `attn-worker`

In `internal/ptybackend/worker.go`:
- Add `WorkerBinaryPath` to config, resolved from `ATTN_WORKER_PATH` env → `~/.local/bin/attn-worker` → cache → app bundle → LookPath
- Change spawn from `exec attn pty-worker ...args` to `exec attn-worker ...args`
- Set `ATTN_WRAPPER_PATH` to main binary path (still needed for `resolveAttnPath()` inside PTY)
- Cache `attn-worker` instead of `attn`
- Fallback: if `attn-worker` not found, use `attn pty-worker` (backwards compat)

### Phase 4: Update build system

**Makefile**:
```makefile
build:
	go build -ldflags "-s -w" -o attn ./cmd/attn
	go build -ldflags "-s -w" -o attn-worker ./cmd/attn-worker

install: build
	# Install both binaries to ~/.local/bin
```

**Tauri** (`app/src-tauri/src/lib.rs`): Pass `ATTN_WORKER_PATH` pointing to bundled worker binary.

**Tauri config**: Add `attn-worker` to `externalBin`.

### Phase 5: Backwards compatibility

During transition:
- Keep `pty-worker` subcommand in `cmd/attn/main.go`
- Worker resolution tries `attn-worker` first, falls back to `attn pty-worker`
- Remove after one release cycle

## Dependency Graph After Split

```
attn (flagged, lives in memory):
  cmd/attn -> internal/daemon -> internal/agent -> claude-agent-sdk-go
           -> internal/wrapper -> internal/hooks
           -> internal/ptybackend (spawns attn-worker)

attn-worker (clean, NOT flagged):
  cmd/attn-worker -> internal/ptyworker -> internal/pty
                                        (NO agent SDK in transitive closure)
```

## Risk Areas

1. **`resolveAttnPath()` in `internal/pty/manager.go`** still resolves the main `attn` binary for the shell command inside the PTY (`exec attn -s <label>`). This is separate from the worker binary path. No change needed.

2. **Binary caching**: Cache `attn-worker` (needed for spawning). Main binary doesn't need caching (stays in memory), but IS needed on disk for `resolveAttnPath()` — the PR #86 resilience fixes cover that.

3. **Embedded backend**: Still calls `pty.Manager.Spawn()` directly. Must set `AgentValidator` on the manager. No subprocess involved.

4. **Tests**: Worker integration tests need to build `attn-worker`. Manager tests need updating for the new DI interface.

5. **Protocol version**: No WebSocket or worker RPC protocol changes. No version bump needed.

## Validation

After building, confirm on CrowdStrike machine:
- `attn-worker` binary is NOT quarantined
- `attn` binary may still be quarantined, but daemon survives in memory
- New sessions spawn successfully using `attn-worker`
