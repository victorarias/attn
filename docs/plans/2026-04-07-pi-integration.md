# Pi Coding Agent Integration

**Status**: SUPERSEDED on 2026-04-16 by `docs/plans/2026-04-16-plugin-system.md` and `docs/plans/2026-04-16-pi-plugin.md`.

Pi is no longer an in-tree Go driver. It becomes the first consumer of attn's new plugin system — a TypeScript/Bun plugin living in its own repo, connecting over JSON-RPC on the unix socket.

**The behavioral design in this document is still authoritative** and referenced by the new pi plugin plan:

- State event → attn state mapping (§State flow table)
- Classifier hint pattern (who runs the classifier, with which model)
- Session ID tie via `session_start` + `attn.link` custom entry
- Edge cases: `--no-session`, resume/fork, ephemeral crashes
- Explicit non-goals: no `pending_approval`, no yolo, no permission gate, no transcript watcher, no PTY state detector
- extension.ts sketch (§F)

**What changes (obsolete in this document):**

- `internal/agent/pi.go`, `internal/agent/pi_ext/`, `internal/classifier/pi.go` — moved to the plugin repo in TypeScript, do not create in attn core.
- Pi-specific protocol commands (`pi_session_linked`) — replaced by generic `agent_session_linked` / `session.report_metadata` in the plugin system plan.
- Pi-specific SQLite columns (`pi_session_file`, `pi_session_id`) — replaced by generic `agent_metadata TEXT` blob.
- Staging to `~/.attn/pi-ext.ts` via `ensureAttnPiExtensionStaged` — plugin handles its own staging.
- Go `ClassifierProvider` / `ClassifierProviderWithHint` plumbing — plugin may own classifier end-to-end in TypeScript (open question in new plan).

---

**Original status**: Proposed
**Date**: 2026-04-07
**Owner**: daemon/frontend

## Summary

Integrate [`pi`](https://github.com/badlogic/pi-mono) (`npm install -g @mariozechner/pi-coding-agent`) as a first-class agent in attn, alongside claude/codex/copilot. The current `internal/agent/pi.go` is a minimal stub that only spawns the binary and passes args through; everything downstream (protocol enum, settings keys, `LocationPicker`, pty manager, hub manager) is already scaffolded for pi but no state detection or lifecycle features are wired up.

Goal: codex-parity baseline (resume, fork, classifier, session metadata) using a custom attn-owned **pi extension** as the primary state source instead of transcript watching or PTY heuristics. Pi's extension API is richer than Claude Code hooks — it surfaces `session_start`, `input`, `before_agent_start`, `agent_start`/`end`, `turn_start`/`end`, `tool_call` (blockable), `tool_result`, `session_shutdown` — which makes it the cleanest place to hang reliable state detection.

## Why an extension, not transcripts or PTY heuristics

- Pi exposes a full TypeScript extension API auto-loadable via `pi -e <path>`. Extensions run in the pi node process, have access to `ctx.sessionManager`, can `appendCustomEntry` to persist state into the session JSONL, and can use `node:net` to talk directly to attn's unix socket.
- The event model covers every state transition attn cares about, with no race conditions inherent to tailing a JSONL file or parsing a TUI.
- If the extension fails to load, pi still runs; attn just falls through to `unknown` for that session. No brittle fallback machinery to maintain.

## Architecture

```
┌─────────────────┐         ┌───────────────────────┐
│  attn daemon    │         │  pi process           │
│                 │         │  (spawned by attn)    │
│  ~/.attn/       │◄────────┤   loads -e <staged>   │
│  attn.sock      │ unix    │                       │
│                 │ socket  │   attn pi extension   │
│                 │         │   (TypeScript)        │
└─────────────────┘         └───────────────────────┘
       │                              │
       │                              │
       │                              ▼
       │                    ~/.pi/agent/sessions/
       │                    --<cwd>--/<ts>_<uuid>.jsonl
       │                    (pi-owned, extension appends
       │                     `attn.link` custom entry for
       │                     session tie recovery)
       │
       ▼
  When pi stops, daemon runs
  `pi -p --no-tools --no-extensions --model <hint>`
  as a classifier subprocess.
```

**State flow:**

| Pi event                | Extension sends                             | Resulting attn state                             |
|-------------------------|---------------------------------------------|--------------------------------------------------|
| `session_start`         | `pi_session_linked` (ties IDs)              | (bookkeeping only; state stays `launching`)      |
| `input` (user-typed)    | `update_state`, state=`working`             | `working`                                        |
| `before_agent_start`    | `update_state`, state=`working`             | `working`                                        |
| `turn_start` / `_end`   | (ignored; noise)                            | —                                                |
| `agent_end`             | `stop` + last assistant text + `classifier_hint` | classifier runs, resolves to `idle` / `waiting_input` |
| `session_shutdown`      | `update_state`, state=`idle`                | `idle`                                           |
| Extension fails to load | (nothing)                                   | `launching` → `unknown` until PTY exits          |

**Classifier path:** the extension does NOT call an LLM itself. It just tells the daemon *which* model to use via `classifier_hint: { provider, model }` on the stop payload. The Go daemon implements `ClassifierProvider.Classify` on the pi driver and shells out to `pi -p --no-tools --no-extensions --no-session --model <hint.model> '<prompt>'`, matching the Codex/Copilot pattern. This keeps classifier orchestration unified across agents and uses pi's own auth/model config.

## Session ID tie

Attn generates `ATTN_SESSION_ID` as today and passes it via env var. The extension's `session_start` handler is the first synchronization point:

```typescript
pi.on("session_start", async (_event, ctx) => {
  const attnId = process.env.ATTN_SESSION_ID;
  if (!attnId) return; // running outside attn — no-op

  const piSessionFile = ctx.sessionManager.getSessionFile();  // path or undefined (--no-session)
  const piSessionId   = ctx.sessionManager.getSessionId();    // pi's own uuid

  // (a) tell the daemon — first message on the socket
  send({
    cmd: "pi_session_linked",
    id: attnId,
    pi_session_id: piSessionId,
    pi_session_file: piSessionFile ?? null,
  });

  // (b) persist the tie inside pi's own session file
  if (piSessionFile) {
    ctx.sessionManager.appendCustomEntry("attn.link", { attnSessionId: attnId });
  }
});
```

The daemon stores `pi_session_file` + `pi_session_id` on the attn session record. The persisted `attn.link` custom entry lets attn rebuild the mapping by scanning `~/.pi/agent/sessions/` after a daemon restart — every attn-owned pi session is self-identifying.

Edge cases:
- `--no-session`: `getSessionFile()` is `undefined`; daemon knows there's no disk file to read.
- `/resume` or `/fork`: `session_start` fires again with `reason: "resume" | "fork"` and a new session file; the link is re-sent.
- Ephemeral / crash before link: attn side just has no `pi_session_file` until (or unless) the extension ever connects.

## Wire protocol

All messages are single-line JSON over `~/.attn/attn.sock`, same envelope the Claude hooks use today (exact field shape cross-checked against `internal/hooks` and `internal/daemon` socket handler as step 0 of Phase 2):

```
{"cmd":"pi_session_linked","id":"<attn>","pi_session_id":"<pi-uuid>","pi_session_file":"<path>|null"}
{"cmd":"update_state","id":"<attn>","state":"working","source":"pi_ext"}
{"cmd":"stop","id":"<attn>","text":"<last assistant text>","classifier_hint":{"provider":"google","model":"gemini-2.5-flash"}}
{"cmd":"update_state","id":"<attn>","state":"idle","source":"pi_ext"}
```

New commands on the daemon socket: `pi_session_linked`. Reusing existing: `update_state`, `stop`. The optional `classifier_hint` field on the stop payload may or may not require a struct change — will be determined in Phase 2 step 0.

## Deferred / explicit non-goals

The following are intentionally out of scope and should not be added speculatively:

- **`pending_approval` state for pi.** Pi has no native permission prompt. Building one inside the extension would be speculative UX without a concrete problem.
- **Permission-gate hook** (`rm -rf` / `sudo` / destructive-write interception). Same reason.
- **Yolo mode.** If there's nothing to gate, there's nothing to bypass. `HasYolo` stays `false`.
- **Transcript watcher / real-time JSONL tailing.** The extension is the real-time source; tailing would duplicate work.
- **PTY state detector.** Explicit non-goal: when the extension fails, we report `unknown` rather than introduce fragile TUI parsing.
- **Global install of the extension to `~/.pi/agent/extensions/`.** The extension is staged per-daemon into `$TMPDIR` and loaded via `-e <path>`; bare `pi` runs outside attn are unaffected.

If any of these becomes desirable later, each is a standalone follow-up with its own design, not part of the initial integration.

## Implementation Phases

### Phase 1 — Launch plumbing (no state yet)

**Goal:** pi sessions launched by attn gain resume, fork, and `--continue` pass-through. Attn still shows `launching → unknown`, but the session is usable with resume semantics matching codex/copilot.

**Changes:**

- `internal/agent/pi.go`
  - `BuildCommand`: translate `SpawnOpts.{ResumeSessionID, ResumePicker, ForkSession}` into pi's native flags:
    - `ResumeSessionID != ""` → `--session <path>` (if we have a full file path) or `--fork <partial-uuid>` for fork-from-id. Verify which is right per pi's semantics before committing.
    - `ResumePicker` → `--resume`
    - `ForkSession && ResumeSessionID != ""` → `--fork <path-or-uuid>`
    - Pass `--continue` for a "continue most recent" UX if attn has an equivalent.
  - `Capabilities`: `HasResume: true`, `HasFork: true`. Everything else stays `false`.
- `internal/agent/driver_test.go`
  - Replace `TestBuiltInPiDriver_MinimalCapabilities` with a resume/fork-aware assertion.
  - Add `TestPiDriver_BuildCommand_Resume`, `TestPiDriver_BuildCommand_Fork`, `TestPiDriver_BuildCommand_Passthrough`.

**Deliverable:** `attn -s foo` + resume picker + fork work end-to-end. No regressions in attn UI state behavior.

### Phase 2 — Attn pi extension + state reporting

**Goal:** attn knows when pi is working vs done via the extension alone.

**Step 0 — verify hook wire protocol.** Before writing any TypeScript, read `internal/hooks/*.go` and `internal/daemon/` socket handler to lock the exact `cmd` / `state` / `source` field shapes. Confirm that adding a new `pi_session_linked` command and an optional `classifier_hint` field on the stop payload does not require a protocol version bump on the WebSocket side.

**New files:**

- `internal/agent/pi_ext/extension.ts` — the extension source.
  - Single TypeScript file, no npm deps. Imports `@mariozechner/pi-coding-agent` types (resolved at runtime by pi's bundled jiti). Uses `node:net` for the socket, `process.env` for config.
  - No-ops cleanly if `ATTN_SESSION_ID` or `ATTN_SOCKET_PATH` is missing, so the file is safe even if accidentally copied to `~/.pi/agent/extensions/`.
  - Subscribes to: `session_start`, `input`, `before_agent_start`, `agent_start`, `agent_end`, `session_shutdown`. (Skip `turn_start`/`turn_end` — noise, `agent_start`/`agent_end` are sufficient.)
  - On `agent_end`: walks `ctx.sessionManager.getBranch()` for the last assistant text, reads `ctx.model` / current model info, sends `stop` with `classifier_hint`.
  - Fire-and-forget writes. Reconnects the socket on disconnect with bounded backoff; drops messages if the socket is unavailable.
- `internal/agent/pi_ext/embed.go` — `//go:embed extension.ts` + `StageExtension(dir string) (path string, err error)` helper.
- `internal/agent/pi_ext/extension_test.go` — unit tests for the Go staging helper (idempotent writes, permissions, path stable across daemon lifetime).

**Modified files:**

- `internal/agent/pi.go`
  - `Capabilities`: no new flags flip yet; the extension mechanism is orthogonal to `HasHooks`.
  - `BuildEnv`: inject `ATTN_SESSION_ID=<opts.SessionID>` and `ATTN_SOCKET_PATH=<opts.SocketPath>` if the existing env plumbing doesn't already provide them (verify against `internal/pty/manager.go` during Phase 2 step 0).
  - `BuildCommand`: prepend `-e <staged-extension-path>` to the arg list.
  - Staging: the daemon writes the extension once on startup to `$TMPDIR/attn-pi-ext-<daemon-pid>.ts` and passes the path to spawn options. The pi driver doesn't need to re-stage per spawn.
- `internal/daemon/` (socket handler / state update dispatch)
  - Accept and route the new `pi_session_linked` command.
  - Store `pi_session_file` + `pi_session_id` on the session record (new optional fields on the in-memory session struct and SQLite schema; no frontend surface yet).
- `internal/daemon/daemon.go` startup
  - Call `pi_ext.StageExtension(d.tmpDir)` once at init, stash the path, cleanup on shutdown.

**Tests:**

- Go: `BuildCommand` includes `-e <staged>`, env contains `ATTN_SESSION_ID` + `ATTN_SOCKET_PATH`, staging creates a readable file.
- Go: daemon handles `pi_session_linked` → session record has `pi_session_file` + `pi_session_id` populated.
- Smoke e2e: spawn `pi --help` (fast, no LLM call) with the extension loaded via `-e`, assert the extension connects to a test socket and sends a `pi_session_linked` message. This is the highest-value test because it exercises the full jiti load.

**Deliverable:** pi sessions in attn transition through `launching → working → idle` based on real pi lifecycle events.

### Phase 3 — Classifier via pi subprocess

**Goal:** WAITING vs DONE classification at stop time.

**New files:**

- `internal/classifier/pi.go` — `ClassifyWithPi(text, executable, model, timeout)` that shells out to `pi -p --no-tools --no-extensions --no-session --mode text --model <model> '<classification prompt>'`. Cribbed from `ClassifyWithCodexExecutableInDir`. Reuse Codex's classifier system prompt verbatim; iterate only if we observe false positives.
- `internal/classifier/pi_test.go` — parity with existing classifier tests; fake `pi` executable returning different verdicts.

**Modified files:**

- `internal/agent/pi.go`
  - Implement `ClassifierProvider.Classify(text, timeout)` delegating to `classifier.ClassifyWithPi`.
  - Add `ExecutableClassifierProvider.ClassifyWithExecutable` if the daemon's classifier dispatch needs it (check how Codex wires this).
  - `Capabilities`: `HasClassifier: true`.
- `internal/daemon/` stop-event handler
  - Extract `classifier_hint.provider` + `classifier_hint.model` from the stop payload and route them into the classifier call. If the existing struct can't carry extra fields, add a new field and classify via a hint-aware variant.

**Tests:**

- Unit: `Pi.Classify()` with a fake pi stub executable.
- Extension contract: stop payload includes `classifier_hint`.
- Daemon: stop message with `classifier_hint` → classifier called with the hinted model.

**Deliverable:** pi sessions land on `idle` vs `waiting_input` after stop, parity with claude/codex.

### Phase 4 — Polish

- `app/src/components/SettingsModal.tsx` / `app/src/utils/agentAvailability.ts`: confirm pi row renders correctly, `pi_executable` override works. Most scaffolding already exists; this is verification + small fixes.
- `CHANGELOG.md`: one entry per landed phase.
- `AGENTS.md`: one sentence noting pi support and the extension-based state tracking.

Explicitly not in Phase 4: yolo mode, approval state, permission gates, transcript watcher, PTY state detector. See "Deferred / explicit non-goals" above.

## Open questions (to resolve during implementation)

1. **`ATTN_SOCKET_PATH` plumbing.** Is it already injected for non-claude drivers, or does `Pi.BuildEnv` need to add it? Claude hooks rely on the settings.json rather than env, so it may not be there for codex/copilot either. Resolve by reading `internal/pty/manager.go` env setup at the start of Phase 2.
2. **Existing stop-event struct.** Can it carry `classifier_hint` as an optional field without a protocol version bump? Resolve by reading `internal/protocol` + `internal/daemon` stop handling in Phase 2 step 0.
3. **Classifier prompt.** Start with Codex's prompt verbatim. If classification quality suffers, revisit with a pi-specific prompt in a follow-up.
4. **Resume vs fork argument mapping.** Verify exactly how attn's `SpawnOpts.ResumeSessionID` values look today (full path vs uuid vs partial uuid) and map them onto pi's `--session` / `--fork` / `--resume` semantics. Settle this before writing `BuildCommand` in Phase 1.

## Suggested kick-off order

1. **Phase 1** as a standalone PR. Small, low-risk, immediately unlocks pi usage.
2. **Phase 2 step 0** — investigate hook protocol before writing any TypeScript. (Partially done already — see the *Wire protocol — concrete mapping* section below.)
3. **Phase 2 extension + staging** — ship state transitions, verify via smoke e2e.
4. **Phase 3** as a separate PR.
5. **Phase 4** polish merged alongside Phase 3 or as a tiny follow-up.

---

## Expanded implementation details

The following sections expand load-bearing details that were too hand-wavy in the phase sketches above. Read these before writing code for the relevant phase.

### A. Attn's spawn architecture — why staging and env live in the wrapper, not the daemon

This affects staging (§B), env injection (§D), and failure modes.

There are **two processes** involved in spawning a pi session:

1. **Daemon (`internal/daemon`)** — runs in the background. When a session is requested, it uses `internal/pty/manager.go` to spawn a login shell that runs `attn -s <label> [--resume ...] [--yolo]` and injects `ATTN_DAEMON_MANAGED=1`, `ATTN_SESSION_ID=<id>`, `ATTN_AGENT=pi`, etc. See `internal/pty/manager.go:378` (`buildSpawnCommand`) and `:417` (`buildSpawnEnv`). The daemon does **not** call `Driver.BuildCommand` or `Driver.PrepareLaunch` directly.
2. **Wrapper (`cmd/attn/main.go`)** — the `attn` binary re-executed by the daemon as the login-shell child. It parses flags, registers with the daemon (no-op when `ATTN_DAEMON_MANAGED=1`), constructs `agentdriver.SpawnOpts{SocketPath: config.SocketPath(), ...}`, calls `preparer.PrepareLaunch(opts)` at line 546, calls `driver.BuildCommand(opts)` at line 575, and `driver.BuildEnv(opts)` at line 580. See `cmd/attn/main.go:520-600`.

**Implications:**
- `Pi.PrepareLaunch` runs in the **wrapper** process, per spawn, with access to `opts.SessionID`, `opts.SocketPath`, `opts.CWD`.
- `Pi.BuildCommand` runs in the wrapper too, same access.
- `Pi.BuildEnv` output is merged into the child process env via `mergeEnv(os.Environ(), driver.BuildEnv(opts))` at `cmd/attn/main.go:580`.
- `ATTN_SESSION_ID` is set at `internal/pty/manager.go:447` during the daemon-side spawn *and* re-read by the wrapper at `cmd/attn/main.go:521`. We do not need to add it in `Pi.BuildEnv`; it's already in the wrapper's env by the time `BuildEnv` runs, and Go's child process inherits it via `os.Environ()` in `mergeEnv`.
- `ATTN_SOCKET_PATH` is **not** currently set in the child env by anyone. Claude hooks don't need it (they bake the path into settings.json via `hooks.Generate`). For pi, `Pi.BuildEnv` must add `ATTN_SOCKET_PATH=` + `opts.SocketPath` explicitly.

### B. Staging strategy — use the claude_skill.go pattern

Previous plan said "stage once per daemon lifetime to `$TMPDIR`." Revised: **stage persistently to `~/.attn/pi-ext.ts` with a content-hash check, called from `Pi.PrepareLaunch` in the wrapper.** Mirrors `internal/agent/claude_skill.go:155` (`ensureAttnClaudeSkillInstalled`).

Why the revision: the daemon doesn't run `PrepareLaunch`; the wrapper does. "Per daemon lifetime" would require adding a new daemon-side staging hook with its own config plumbing. The claude_skill pattern is already established in the codebase, is per-spawn but idempotent via content comparison, and requires zero daemon changes.

**Code sketch (new file `internal/agent/pi_ext.go`):**

```go
package agent

import (
    "fmt"
    "os"
    "path/filepath"
)

// attnPiExtensionContent is the TypeScript extension source that attn ships
// and loads into pi via `pi -e <path>`. See extension.ts in the tree for the
// canonical source and docs/plans/2026-04-07-pi-integration.md for shape.
const attnPiExtensionContent = `<full extension.ts source as a Go raw string>`

func ensureAttnPiExtensionStaged() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil {
        return "", fmt.Errorf("resolve home for pi extension: %w", err)
    }
    extDir := filepath.Join(home, ".attn")
    extPath := filepath.Join(extDir, "pi-ext.ts")
    if current, err := os.ReadFile(extPath); err == nil {
        if string(current) == attnPiExtensionContent {
            return extPath, nil
        }
    } else if !os.IsNotExist(err) {
        return "", fmt.Errorf("read pi extension: %w", err)
    }
    if err := os.MkdirAll(extDir, 0o755); err != nil {
        return "", fmt.Errorf("create ~/.attn dir: %w", err)
    }
    if err := os.WriteFile(extPath, []byte(attnPiExtensionContent), 0o644); err != nil {
        return "", fmt.Errorf("write pi extension: %w", err)
    }
    return extPath, nil
}
```

**Wiring in `internal/agent/pi.go`:**

```go
var _ LaunchPreparer = (*Pi)(nil)

func (p *Pi) PrepareLaunch(opts SpawnOpts) error {
    _, err := ensureAttnPiExtensionStaged()
    return err
}

func (p *Pi) BuildCommand(opts SpawnOpts) *exec.Cmd {
    args := []string{}
    if path, err := ensureAttnPiExtensionStaged(); err == nil {
        args = append(args, "-e", path)
    }
    // ...resume/fork args (see §C)...
    args = append(args, opts.AgentArgs...)
    return exec.Command(opts.Executable, args...)
}

func (p *Pi) BuildEnv(opts SpawnOpts) []string {
    env := []string{
        "ATTN_SOCKET_PATH=" + opts.SocketPath,
    }
    if opts.Executable != "" && opts.Executable != p.DefaultExecutable() {
        env = append(env, p.ExecutableEnvVar()+"="+opts.Executable)
    }
    return env
}
```

**Why `BuildCommand` also calls `ensureAttnPiExtensionStaged`**: `PrepareLaunch` is optional (the interface is only invoked if implemented), and `BuildCommand` needs the path. Calling it twice is idempotent (content-hash short-circuit). If staging fails in `BuildCommand` we just omit the `-e` flag — pi still runs, attn sees `unknown`, matches the "report unknown on failure" decision.

**No SpawnOpts changes, no daemon changes, no singleton.**

### C. Resume/fork flag mapping — semantic mismatch and Phase 1 scope

Pi's native session flags are fundamentally different from codex/copilot:

| attn intent                   | claude                   | codex                | copilot              | pi                                   |
|-------------------------------|--------------------------|----------------------|----------------------|--------------------------------------|
| New session                   | `--session-id <id>`      | (none)               | (none)               | (none)                               |
| Resume picker (interactive)   | `-r`                     | `resume`             | `--resume`           | `--resume`                           |
| Resume specific by ID         | `-r <id>`                | `resume <id>`        | `--resume <id>`      | **`--session <path>`** (path, not id)|
| Fork specific by ID           | `-r <id> --fork-session` | (not supported)      | (not supported)      | `--fork <path-or-partial-uuid>`      |

**The mismatch:** attn's `SpawnOpts.ResumeSessionID` carries whatever the downstream agent's native session identifier is — for codex/copilot/claude that's a UUID. Pi's `--session` wants a filesystem path, not a UUID. Pi's `--fork` accepts either a path or a partial UUID, which is one convenient escape hatch.

Looking at how the wrapper populates `ResumeSessionID`: `cmd/attn/main.go:535` sets it from `parsed.resumeID` which comes from CLI arg parsing (`--resume <id>`). For attn-internal recovery flows, the daemon's `SetResumeSessionID` / `GetResumeSessionID` store methods persist whatever id the driver asked to keep.

**Phase 1 scope (honest):**

| Flow                   | Phase 1 wires up?                                              |
|------------------------|----------------------------------------------------------------|
| New pi session         | Yes — no resume flags, default pi behavior                     |
| `--resume` picker      | Yes — `ResumePicker → --resume`                                |
| Fork-by-id             | Yes — `ForkSession && ResumeSessionID != "" → --fork <id>` (partial UUID works per pi docs) |
| Resume-by-id in place  | **No** — requires a file path we don't yet have. Defer to Phase 2b. |

Phase 1's `BuildCommand`:

```go
func (p *Pi) BuildCommand(opts SpawnOpts) *exec.Cmd {
    args := []string{}
    // (extension staging handled in §B)

    switch {
    case opts.ForkSession && opts.ResumeSessionID != "":
        // Partial UUID works per pi docs; full path also works.
        args = append(args, "--fork", opts.ResumeSessionID)
    case opts.ResumePicker:
        args = append(args, "--resume")
    case opts.ResumeSessionID != "":
        // Phase 1: warn and ignore. Phase 2b lifts this limitation.
        // (Log via driver warning path; return cmd without --session.)
    }

    if opts.YoloMode {
        // Explicit non-goal: HasYolo=false, this branch never fires.
    }

    args = append(args, opts.AgentArgs...)
    return exec.Command(opts.Executable, args...)
}
```

**Capabilities for Phase 1:** `HasResume: true` (picker works), `HasFork: true` (via partial UUID). Leaving `HasResume: true` is slightly dishonest because resume-by-id doesn't work in place — but the picker is resume UX and it *does* work. If this is uncomfortable, gate the capability on `HasFork` alone and leave `HasResume: false` until Phase 2b.

**Phase 2b — resume-by-id lift (added after Phase 2):**

Once `pi_session_linked` is wired up, the daemon stores `pi_session_file` alongside the session. For resume-by-id, the wrapper needs to resolve an incoming ID to a path. Two layers of resolution:

1. **Store lookup** — if attn has ever seen this session before, `store.GetPiSessionLink(id)` returns the path.
2. **Filesystem glob fallback** — if not in the store (e.g., first time attn is asked to resume a pi session it didn't originally own), glob `~/.pi/agent/sessions/**/*_<partial-uuid>*.jsonl` across all cwd-dirs. Return the first match (or ambiguous → fail).

New helper in `internal/agent/pi.go`:

```go
func (p *Pi) resolveSessionFile(cwd, idOrPath string) string {
    // If idOrPath is already a .jsonl path that exists, use it.
    if strings.HasSuffix(idOrPath, ".jsonl") {
        if _, err := os.Stat(idOrPath); err == nil {
            return idOrPath
        }
    }
    // Otherwise treat as a (partial) UUID and glob.
    // (Implementation: filepath.Glob across ~/.pi/agent/sessions/**/<ts>_*<id>*.jsonl)
    return ""
}
```

Phase 2b threads a store lookup through the wrapper before calling `BuildCommand`, or exposes a `ResumePolicyProvider`-style hook on the driver so it can self-resolve. TBD at phase 2b kickoff — simplest answer is probably to add a `SpawnOpts.ResumeSessionPath` field set by the wrapper after a store lookup, parallel to the existing `ResumeSessionID`.

### D. Wire protocol — concrete mapping

The daemon reads one JSON message per unix socket connection. See `internal/daemon/daemon.go:1406` (`handleConnection`) — 65536-byte buffer, single `protocol.ParseMessage` call, switch on command, dispatch to handler, close conn. **Each message is one connect/write/close cycle** (matches how Claude hooks use `nc` via `hooks.Generate` in `internal/hooks/hooks.go`).

The pi extension must match this: open a new `node:net` socket per message, `write(JSON + '\n')`, `end()`, handle errors by dropping. Do **not** try to keep a persistent connection — the existing `handleConnection` only reads one message and closes.

**New command: `pi_session_linked`.**

TypeSpec (`internal/protocol/schema/main.tsp`) — add alongside existing messages around line 264:

```tsp
model PiSessionLinkedMessage {
  cmd: "pi_session_linked";
  id: string;              // attn session id
  pi_session_id: string;   // pi's own UUID (empty if ephemeral)
  pi_session_file?: string; // null/omitted for --no-session
}
```

After `make generate-types`, update `internal/protocol/constants.go`:

```go
const CmdPiSessionLinked = "pi_session_linked"

// In ParseMessage switch:
case CmdPiSessionLinked:
    var msg PiSessionLinkedMessage
    // ... standard parse ...
```

Daemon handler in `internal/daemon/daemon.go` near `handleStop`:

```go
func (d *Daemon) handlePiSessionLinked(conn net.Conn, msg *protocol.PiSessionLinkedMessage) {
    d.logf("handlePiSessionLinked: session=%s pi_id=%s file=%s",
        msg.ID, msg.PiSessionID, protocol.Deref(msg.PiSessionFile))
    d.store.SetPiSessionLink(msg.ID, protocol.Deref(msg.PiSessionFile), msg.PiSessionID)
    d.sendOK(conn)
}
```

Add the case to `handleConnection` switch at `internal/daemon/daemon.go:1422`:

```go
case protocol.CmdPiSessionLinked:
    d.handlePiSessionLinked(conn, msg.(*protocol.PiSessionLinkedMessage))
```

**Reusing existing commands for state / stop.**

| Extension event         | Wire command         | Shape                                                                       |
|-------------------------|----------------------|-----------------------------------------------------------------------------|
| `session_start`         | `pi_session_linked`  | above                                                                       |
| `input` / `before_agent_start` / `agent_start` | `state` | `{cmd:"state", id, state:"working"}` — matches existing `StateMessage`      |
| `session_shutdown`      | `state`              | `{cmd:"state", id, state:"idle"}`                                           |
| `agent_end`             | `stop`               | `{cmd:"stop", id, transcript_path, text?, classifier_hint?}` — see below    |

**Extending `StopMessage` with classifier hint and pre-extracted text.**

TypeSpec change to `internal/protocol/schema/main.tsp:270`:

```tsp
model ClassifierHint {
  provider: string;
  model: string;
}

model StopMessage {
  cmd: "stop";
  id: string;
  transcript_path: string;
  text?: string;                    // new: pre-extracted last assistant text
  classifier_hint?: ClassifierHint; // new: model to use for classification
}
```

Both new fields are optional → this is an additive change → no protocol version bump required for existing Claude-hook clients.

**Consumer side:** `classifySessionState` in `internal/daemon/daemon.go:1669` currently pulls text via `driver.ExtractLastAssistantForClassification(transcriptPath, ...)`. For pi we want to short-circuit: if `msg.Text != nil && *msg.Text != ""`, skip extraction and use it directly. This is a small branch in `classifyOrDeferAfterStop` / `classifySessionState`.

**Classifier hint plumbing:** the existing `ClassifierProvider.Classify(text, timeout)` has no room for a hint. Options:
1. Add a new optional interface `ClassifierProviderWithHint` that pi implements; daemon prefers it when the stop message carries a hint.
2. Add a thread-local / per-call context parameter to Classify (more invasive).

Pick option 1 — narrower blast radius:

```go
// internal/agent/driver.go
type ClassifierHint struct {
    Provider string
    Model    string
}

type ClassifierProviderWithHint interface {
    ClassifyWithHint(text string, hint ClassifierHint, timeout time.Duration) (string, error)
}

func GetClassifierWithHint(d Driver) (ClassifierProviderWithHint, bool) {
    if d == nil || !EffectiveCapabilities(d).HasClassifier {
        return nil, false
    }
    cp, ok := d.(ClassifierProviderWithHint)
    return cp, ok
}
```

Daemon dispatch (in the classification path): try `ClassifierProviderWithHint` if a hint is present, fall back to `ClassifierProvider.Classify`.

### E. Session record schema + migration

Decision: mirror the `resume_session_id` pattern. New columns live in SQLite and are accessed via explicit store methods, **not** added to the TypeSpec `Session` model (`internal/protocol/schema/main.tsp:91`) or broadcast to the frontend.

Why: matches existing convention (`resume_session_id` is on the sessions table but not in the protocol), keeps internal metadata out of the frontend bundle, avoids a protocol version bump.

**New SQLite migration in `internal/store/sqlite.go` (next migration number after current max — check existing list before writing, current max was 24+ at time of plan):**

```go
{N,   "add pi_session_file to sessions", "ALTER TABLE sessions ADD COLUMN pi_session_file TEXT NOT NULL DEFAULT ''"},
{N+1, "add pi_session_id to sessions",   "ALTER TABLE sessions ADD COLUMN pi_session_id TEXT NOT NULL DEFAULT ''"},
```

Also update the `columnExists` fallback block at `internal/store/sqlite.go:461` to add idempotent ALTERs for these two columns (mirrors how `resume_session_id` is handled at line 461-470).

**New store methods in `internal/store/store.go` (mirror `SetResumeSessionID` / `GetResumeSessionID` at lines 594-625):**

```go
func (s *Store) SetPiSessionLink(id, piSessionFile, piSessionID string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.db == nil {
        return
    }
    _, err := s.db.Exec(
        "UPDATE sessions SET pi_session_file = ?, pi_session_id = ? WHERE id = ?",
        strings.TrimSpace(piSessionFile), strings.TrimSpace(piSessionID), id,
    )
    if err != nil {
        log.Printf("[store] SetPiSessionLink: failed for session %s: %v", id, err)
    }
}

func (s *Store) GetPiSessionLink(id string) (piSessionFile, piSessionID string) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    if s.db == nil {
        return "", ""
    }
    var f, pid string
    if err := s.db.QueryRow(
        "SELECT pi_session_file, pi_session_id FROM sessions WHERE id = ?", id,
    ).Scan(&f, &pid); err != nil {
        return "", ""
    }
    return strings.TrimSpace(f), strings.TrimSpace(pid)
}
```

**Files touched for the schema/link work:**

| File                                        | Change                                                    |
|---------------------------------------------|-----------------------------------------------------------|
| `internal/store/sqlite.go`                  | 2 new migrations + columnExists fallback branches         |
| `internal/store/store.go`                   | `SetPiSessionLink` / `GetPiSessionLink`                   |
| `internal/store/store_test.go`              | Tests for the new methods (mirror resume_session_id tests)|
| `internal/protocol/schema/main.tsp`         | `PiSessionLinkedMessage` + `StopMessage` field additions + `ClassifierHint` |
| `internal/protocol/generated.go`            | Regenerated via `make generate-types`                     |
| `app/src/types/generated.ts`                | Regenerated via `make generate-types`                     |
| `internal/protocol/constants.go`            | `CmdPiSessionLinked` constant + `ParseMessage` case       |
| `internal/daemon/daemon.go`                 | `handlePiSessionLinked`, dispatch case, stop-path short-circuit when `text` is set, classifier dispatch to `ClassifierProviderWithHint` |
| `internal/agent/driver.go`                  | `ClassifierHint` struct + `ClassifierProviderWithHint` interface + `GetClassifierWithHint` helper |

**Not touched:** `app/src/**` (no frontend surface for pi_session_file/id). `Session` model in TypeSpec (unchanged).

### F. Extension.ts — full file sketch

File path (pre-staging): `internal/agent/pi_ext_source.ts` in the attn repo, not shipped directly. Its content lives as a Go raw string in `internal/agent/pi_ext.go` (see §B). Keeping the `.ts` file in the tree makes editor tooling work; a `go generate` step (or a tiny Go test) reads it and asserts the Go constant matches.

**Full sketch:**

```typescript
/**
 * Attn pi extension
 *
 * Loaded via `pi -e <path>` when attn spawns pi. Reports session lifecycle
 * events to attn's unix socket so attn can track state without PTY heuristics
 * or transcript tailing.
 *
 * No-ops cleanly if ATTN_SESSION_ID or ATTN_SOCKET_PATH is missing, so the
 * file is safe even if accidentally copied to ~/.pi/agent/extensions/.
 *
 * Protocol: one JSON message per socket connection (connect / write / end),
 * matching the daemon's handleConnection loop.
 */

import type { ExtensionAPI, ExtensionContext } from "@mariozechner/pi-coding-agent";
import { createConnection } from "node:net";
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

type AttnMessage =
  | { cmd: "pi_session_linked"; id: string; pi_session_id: string; pi_session_file: string | null }
  | { cmd: "state"; id: string; state: "working" | "idle" }
  | { cmd: "stop"; id: string; transcript_path: string; text?: string;
      classifier_hint?: { provider: string; model: string } };

const ATTN_ID = process.env.ATTN_SESSION_ID;
const SOCKET_PATH = process.env.ATTN_SOCKET_PATH;
const LOG_PATH = join(homedir(), ".attn", "pi-ext.log");

function log(line: string): void {
  try {
    appendFileSync(LOG_PATH, `[${new Date().toISOString()}] [${ATTN_ID ?? "-"}] ${line}\n`);
  } catch {
    // swallow: logging must never crash the extension
  }
}

function send(msg: AttnMessage): void {
  if (!SOCKET_PATH) return;
  try {
    const sock = createConnection(SOCKET_PATH);
    sock.on("error", (err) => log(`socket error: ${err.message}`));
    sock.on("connect", () => {
      sock.write(JSON.stringify(msg));
      sock.end();
    });
  } catch (err) {
    log(`send failed: ${(err as Error).message}`);
  }
}

function lastAssistantText(ctx: ExtensionContext): string {
  try {
    const branch = ctx.sessionManager.getBranch();
    for (let i = branch.length - 1; i >= 0; i--) {
      const entry = branch[i];
      if (entry.type !== "message") continue;
      if (entry.message.role !== "assistant") continue;
      const content = entry.message.content;
      if (typeof content === "string") return content;
      if (!Array.isArray(content)) continue;
      const text = content
        .filter((c): c is { type: "text"; text: string } => c.type === "text")
        .map((c) => c.text)
        .join("\n")
        .trim();
      if (text) return text;
    }
  } catch (err) {
    log(`lastAssistantText error: ${(err as Error).message}`);
  }
  return "";
}

function classifierHint(ctx: ExtensionContext): { provider: string; model: string } | undefined {
  try {
    // ctx.model is the currently-active model for this session.
    const m = ctx.model;
    if (!m || !m.provider || !m.id) return undefined;
    return { provider: m.provider, model: m.id };
  } catch (err) {
    log(`classifierHint error: ${(err as Error).message}`);
    return undefined;
  }
}

export default function (pi: ExtensionAPI): void {
  if (!ATTN_ID || !SOCKET_PATH) {
    // Running outside attn. Do nothing.
    return;
  }

  log(`extension loaded: attn_id=${ATTN_ID} socket=${SOCKET_PATH}`);

  pi.on("session_start", async (_event, ctx) => {
    try {
      const piSessionFile = ctx.sessionManager.getSessionFile() ?? null;
      const piSessionId = ctx.sessionManager.getSessionId() ?? "";
      send({
        cmd: "pi_session_linked",
        id: ATTN_ID!,
        pi_session_id: piSessionId,
        pi_session_file: piSessionFile,
      });
      if (piSessionFile) {
        try {
          ctx.sessionManager.appendCustomEntry("attn.link", { attnSessionId: ATTN_ID });
        } catch (err) {
          log(`appendCustomEntry failed: ${(err as Error).message}`);
        }
      }
    } catch (err) {
      log(`session_start handler threw: ${(err as Error).message}`);
    }
  });

  const markWorking = async () => {
    try {
      send({ cmd: "state", id: ATTN_ID!, state: "working" });
    } catch (err) {
      log(`markWorking threw: ${(err as Error).message}`);
    }
  };

  pi.on("input", markWorking);
  pi.on("before_agent_start", markWorking);
  pi.on("agent_start", markWorking);

  pi.on("agent_end", async (_event, ctx) => {
    try {
      const text = lastAssistantText(ctx);
      const hint = classifierHint(ctx);
      send({
        cmd: "stop",
        id: ATTN_ID!,
        transcript_path: ctx.sessionManager.getSessionFile() ?? "",
        text: text || undefined,
        classifier_hint: hint,
      });
    } catch (err) {
      log(`agent_end handler threw: ${(err as Error).message}`);
    }
  });

  pi.on("session_shutdown", async (_event) => {
    try {
      send({ cmd: "state", id: ATTN_ID!, state: "idle" });
    } catch (err) {
      log(`session_shutdown handler threw: ${(err as Error).message}`);
    }
  });
}
```

**Design notes on the sketch:**

- **Fire-and-forget**. `send()` does not await; if a write fails (socket gone, file missing) the error is logged and execution continues. State messages are idempotent — missing one doesn't desync pi's actual state from attn.
- **Per-handler try/catch**. A throw inside any pi event handler could abort pi's lifecycle. Every top-level handler wraps its body in try/catch with logging.
- **Logging to `~/.attn/pi-ext.log`, not stdout**. Pi owns the TUI; any stdout write corrupts it. Append-only file with best-effort error swallowing.
- **Connection per message**. Matches the daemon's `handleConnection` which reads exactly one message per conn. No persistent connection, no framing concerns, no reconnect logic. Cost: a Unix socket connect is ~microseconds; we'll fire maybe 10 messages per session.
- **Text and hint extraction are defensive**. Either can return empty; daemon must handle missing `text` by falling through to transcript extraction (which for pi will likely return empty too, causing classifier to skip and state to land on `unknown`). That's acceptable.
- **`appendCustomEntry` is best-effort**. If pi's persistence fails (ephemeral session, disk full), we still sent the link over the socket — attn still has the tie for this session's lifetime; recovery after daemon restart would just fail.
- **No npm dependencies**. Only `node:net`, `node:fs`, `node:path`, `node:os`, plus type-only imports from `@mariozechner/pi-coding-agent`. Type imports are erased at runtime by jiti's TS loader; no runtime require path needed.
- **Type assertions on `ExtensionContext` / `ExtensionAPI`**. These come from the user's installed pi; jiti resolves them at extension load. If pi's type surface changes version-to-version, the extension breaks at load (not runtime) and attn falls through to `unknown`. That's the correct failure mode.

**What's NOT in the sketch but will need to be sorted during implementation:**

- Exact shape of `ctx.model` at `agent_end` time — docs hint it exists but don't nail the property names. Verify against `@mariozechner/pi-coding-agent/dist/` type definitions when writing the Go test that asserts the extension compiles.
- Whether `ctx.sessionManager.getBranch()` returns only the current branch or the full path from leaf to root — if it's the full path, the walk is correct; if it's ambiguous, check `getBranch(fromId)` overloads.
- Log rotation — `~/.attn/pi-ext.log` grows unbounded. Acceptable for v1; add rotation if it becomes a problem.

### G. Testing strategy (brief)

- **Go unit tests**: `Pi.BuildCommand` / `BuildEnv` / `PrepareLaunch` — standard table-driven tests in `internal/agent/pi_test.go`. Staging idempotence test.
- **Store tests**: `SetPiSessionLink` / `GetPiSessionLink` round-trip in `internal/store/store_test.go`.
- **Protocol test**: `PiSessionLinkedMessage` round-trip parse in `internal/protocol` tests.
- **Daemon test**: stop message with `text` and `classifier_hint` bypasses transcript extraction and hits `ClassifierProviderWithHint`. Use a fake Pi driver that records hint/text seen.
- **Extension integration test (deferred)**: spawn a real `pi --print --no-session --no-tools --model <stub> 'hi'` with `-e <staged>` and a test unix socket server. Assert the expected sequence of JSON messages lands on the server. Skip if pi isn't installed in CI; run locally.

