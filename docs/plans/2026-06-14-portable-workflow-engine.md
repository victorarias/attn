# Portable Workflow Engine — Design + Experiment Campaign

**Status:** design v2 (post adversarial-review). Pre-spike, no code yet. v1 changelog at the bottom.
**Scope:** Re-implement Claude Code's *dynamic Workflow engine* inside attn so it is usable by harnesses with **no native workflow support** (primary target: **Codex**). Claude has it natively and does not need this.
**Parity stance:** v1 targets **maximum native fidelity, including in the skeleton.** The *only* deliberate de-scopes are **`budget`** (token accounting) and **Copilot**. Writable execution, subagent MCP-tool access, the hard safety caps, cancellation, and completion notification are all **in scope** — decided 2026-06-14.
**Build style:** NOT small PRs. A sequence of **experiments** that retire the biggest unknowns, converging on a **running end-to-end skeleton + test harness**, then harden.

---

## 1. Goal + thesis

**Goal.** An attn-managed agent (Codex, or any future harness) authors a workflow script, triggers it through attn, watches it run, and gets structured results back — while attn persists the run durably and shows it live as a **read-only** progress tree. attn is the durable store and the UI; **the agent runs the engine.**

**Thesis (corrected after review).** attn supplies the *spawn + sandbox + side-channel mechanism shape*; the engine, the result/validation layer, and writable execution are **net-new**. Precisely:

- attn **has the spawn half**: `agent.HeadlessTaskProvider.RunHeadlessTask` (`internal/agent/driver.go`) spawns one locked-down headless agent across harnesses (Codex + Claude implement it). It does **not** return a value — it captures stdout only to classify failure (`internal/agent/headless.go:31-54`) and returns `Diagnostics` on error, empty on success.
- attn **has the side-channel *shape***: a forced-ish MCP tool writes a payload to a file the parent reads (`internal/contextjanitor/toolserver.go`). But that toolserver does **zero schema validation**, is hardwired to two tools, and validation happens *post-exit in the daemon* with a single mismatch hard-aborting — **no retry**.
- **Net-new** (this project builds it): the goja engine + journal + structural-ordinal resume; a **generic result return** (text and schema object); **in-turn** schema validation + retry; **detect-missing-call** (Codex `required=true` forces server *presence*, not tool *invocation* — see §5); the **error→`null`** contract; **writable execution**; **subagent MCP-server attach**; the **agent-facing CLI contract**.

**Design principle — faithful re-implementation.** The engine must behave *identically* to native. It may differ **only** where the foreign harness forces a different *mechanism* (e.g. extracting structured output from Codex via an MCP tool instead of Claude's native `StructuredOutput` tool — §5), or where we have *explicitly* de-scoped (`budget`, Copilot). **Anywhere else, a difference is a bug.** In particular default execution context, MCP access, worktree semantics, caps, and resume behavior match native — not attn's existing *janitor* configuration (the janitor is read-only and single-tool because its *task* is; those are not engine properties).

The engine is a **goja** (pure-Go ECMAScript) interpreter embedded in the attn binary, exposing the exact native script API, spawning subagents through a generalized `RunHeadlessTask`, and journaling every call to the daemon over the unix socket.

---

## 2. What we are replicating (native engine, compressed)

Sandboxed plain-JS interpreter; **no inference itself**. Deterministic control flow in JS; model work **only** in `agent()`.

- `agent(prompt, {schema, label, phase, model, isolation, agentType})` — no `schema` → final **text**; with `schema` → forced `StructuredOutput`, validated + retried, returns **object**. Resolves **`null`** (never rejects) on user-skip/terminal-error → `.filter(Boolean)`. Default agent shares the **writable** working tree; `isolation:'worktree'` = fresh worktree (auto-removed if unchanged) for parallel mutation. Subagents may reach **session-connected MCP tools**.
- `parallel(thunks)` — barrier; never rejects; throwing thunk → `null` slot. `pipeline(items, ...stages)` — no barrier; each item flows all stages; stage cb gets `(prevResult, originalItem, index)`; throwing stage → that item `null`. ≤4096 items/call.
- `phase(title)` / `log(msg)` — progress annotations. `args` — verbatim input (resume identity). `workflow(name, args)` — nested **one level**, shares cap/counter/abort/budget. `budget` — *de-scoped here*.
- Background: returns `runId` immediately, **notifies** on completion. Caps: concurrency `min(16, cores-2)`; **1000-agent lifetime**; 4096 items/call.
- Resume: `resumeFromRunId` replays the longest **unchanged prefix** of `agent()` calls from journal; first divergence + everything after runs live. Same script + same `args` = 100% hit.
- Determinism bans: `Date.now()`, `Math.random()`, argless `new Date()` **throw**.

Irreducible core: *deterministic JS + journaled `agent()` + structural-ordinal resume.* The native resume-invalidation semantics must be **transcribed into this repo as a testable spec before E1** (they currently live only in the reverse-engineering run — see §6, R-spec).

---

## 3. Architecture Map

```text
 Driver harness  (Codex / others — NO native workflow support)
   │  authors workflow.js, then triggers (defaults to ATTN_SESSION_ID):
   │     attn workflow run workflow.js --args-file in.json [--wait]
   ▼
 attn workflow engine            (goja interpreter, INSIDE the attn binary, spawned BY the agent)
   │  • deterministic JS in a DENY-BY-DEFAULT realm (no fs/net/env/crypto/performance/argless-time/random)
   │  • interrupt watchdog (goja vm.Interrupt) — an infinite loop is killable
   │  • assigns each agent() a STRUCTURAL ordinal (call-site id + iteration path), not invocation order
   │  • each agent() → RunHeadlessTask(generalized) → one headless subagent
   │  • journals every call (ordinal, prompt_hash, schema_hash, result|null) + emits coalesced progress
   │        │ IPC: one frame / connect→write→close (≤64KB), like every attn subcommand
   │        ▼                                        ▲ reads <run-tmp>/<ordinal>.json
 attn daemon  ── persists ──► SQLite                 │ (return_result tool, schema-validated in-turn)
   │  workflow_runs + workflow_agent_calls = journal │
   │  relays workflow_run_cancel ──────────────────┐ │
   │                                               ▼ │
   │                                       headless subagents
   │                                       codex exec / claude -p
   │                                       (writable working tree by default; session MCP servers attached)
   └─► websocket (COALESCED ~100-250ms) ─► attn frontend = READ-ONLY progress tree (attached to the run's session)
```

**Who runs what.** The agent spawns `attn workflow run`; that process *is* the engine. It talks to the daemon as a plain IPC client (one command per connection, ≤64KB/frame — `internal/daemon/daemon.go:1685-1808`); the daemon is the **single SQLite writer** and reuses its persist→hydrate→**coalesced-**broadcast loop. The daemon does **not** host the engine but **does** relay a cancel command to it. If the engine/agent dies, the daemon marks the run stale-but-resumable; `attn workflow run --resume <runId>` replays the journaled prefix. The watchdog (not the socket-drop) is what catches a *live* infinite loop.

---

## 4. What attn has (mechanism shape) vs. net-new

**Have — reuse directly:**

- `agent.Driver` registry + `RunHeadlessTask` *spawn* + locked sandbox + env allowlist + failure classification (`internal/agent/{driver,headless,codex,claude}.go`).
- Forced-MCP-tool → side-channel-file *mechanism* (`internal/contextjanitor/toolserver.go`) — the *shape*, not validation/retry.
- Durable parent+child *persistence shape* and the async-runner→persist→hydrate→broadcast loop + request/result pattern (`internal/store/review_loop_runs.go`, `internal/daemon/review_loop.go`, `sendReviewLoopResult`). **NB:** this is a status-row template only — it has **no** call-ordinal cache / prefix-replay; the journal's defining behavior is new.
- Read-only nested projections (`SessionReviewLoopBar`, `Dashboard.renderDispatch`).
- Migration system + ProtocolVersion handshake; full worktree machinery (`internal/git/worktree.go` et al.); per-session MCP wiring to copy for subagent attach.

**Net-new — must build:**

- **goja engine**: interpreter, static `meta` parse, deny-by-default realm + determinism bans, interrupt watchdog, host functions, **structural-ordinal** journaling, resume/prefix-replay with the §6 match predicate.
- **Generic result return**: capture child final **text** (Codex `agent_message` events via `--json` — `internal/transcript/parser_test.go:150`; Claude `.result` via `--output-format json`) for the no-schema path, plus the schema-object path. Add `Result` to `HeadlessTaskResult`; add `Schema`, `ToolName`, `Label/Phase/CallID`, **`ResultPath`**, and writable/MCP fields to `HeadlessTaskRequest`.
- **Schema validation + in-turn retry + detect-missing-call + error→null** (the §5 reliability layer) — none exist today.
- **Writable execution mode** (§7) — every current headless path is read-only + feature-stripped and runs in a throwaway temp dir.
- **Subagent MCP-server attach** (§7) — parity.
- **Hard caps + watchdog + cancel + completion-signal** (§8).
- **Agent-facing contract**: authoring reference + `attn workflow run/result/show/list` (§9).
- **Store/protocol/frontend wiring**: `workflow_runs` (+ `session_id`/`workspace_id`) + `workflow_agent_calls`; `workflow_*` commands/events; coalesced broadcast; `WorkflowRun` types; ProtocolVersion bump; read-only `WorkflowRunView`.

---

## 5. Structured output + result extraction (rewritten)

**Reject Codex native `--output-schema`** (validated, high confidence): it is **silently dropped when an MCP server is attached** (codex #15451 — exactly our config), and forces *intermediate* messages into the schema too (#19816); strict schemas also require top-level `additionalProperties:false` + all fields `required`. So native schema is at most an accelerator on a tool-free final turn — never load-bearing.

**Use a generalized `return_result(schema)` MCP tool — but be honest about the mechanism:**

- **It is not truly "forced."** Codex `mcp_servers.<name>.required=true` (`internal/agent/codex.go:139`) requires the *server* present at startup, **not** that the model *invokes* the tool. There is no `tool_choice` forcing. So the real mechanism is: **instruct** the agent to finish by calling `return_result` + **require** the server + **detect a missing call** + **retry**.
- **Validation + retry live in-turn, in the toolserver** (resolves R2): the generalized toolserver validates the payload against `request.Schema` *inside* `tools/call` and returns `isError:true` with the validator message, so the model self-corrects **in the same turn** — closest to native. Engine-level re-spawn is the outer fallback after the turn ends with no/invalid result.
- **Double-call = last-write-wins, no error** (drop the janitor's once-only + read-before-write preconditions — no native analog; re-emitting is common LLM behavior). Schema validity — not call count — is the only gate.
- **Terminal failure resolves `null`, never throws** (parity): non-zero exit, never-called, or retries-exhausted → journal `result=null`, `status=skipped|errored`, diagnostics in the call row's `error` column; `agent()` **resolves null**. Today every failure path returns a Go `error` (`headless.go:46-50`, `workspace_context_janitor.go:426-430`) — an explicit error→null adapter at the engine boundary is required.
- **No-schema path**: capture the child's final text (above) and resolve it as a string.

This reproduces native's force-StructuredOutput + retry + null semantics; the only forced *mechanism* difference is "MCP tool + detect-missing-call" vs Claude's native tool — which the harness forces, so it is allowed.

---

## 6. Resume + determinism (rewritten — the hardest correctness property)

**Structural ordinals, not temporal.** The prior draft argued single-goroutine goja makes invocation-order ordinals stable. **Wrong** (review `FID-resume-2`): for `pipeline()` downstream stages and any `agent()` after an `await`, *which* journaled result a continuation observes and *when* it issues further calls depends on **promise-resolution order**, which varies with real subagent timing. Fix: assign each `agent()` an ordinal from its **structural position** — a stable call-site identifier + iteration/slot path (e.g. `phase·parallelSlot·pipelineItem·stage·callsite`) — so the *same logical call* gets the *same* ordinal on every re-run regardless of timing.

**Match predicate (resolves the §10 ambiguity).** A journaled call at ordinal *N* is a **cache hit iff** `ordinal==N` **AND** `prompt_hash` matches **AND** `schema_hash` matches (the columns already exist; the prior §7 narrative never used them). Scan in structural order; the first ordinal that is missing or mismatches is the divergence boundary — it and everything after run live. A script edit that changes an upstream call's prompt invalidates that call and all downstream; a downstream-only edit keeps the upstream prefix cached.

**Per-call result files.** The current single `candidatePath` would collide under `parallel()`. Each call writes `<run-tmp>/<ordinal>.json`; `HeadlessTaskRequest.ResultPath` is set per call; the invariant is **one result file per ordinal, never shared**.

**Side-effect / replay contract** (kept half of `FID-resume-5`). A resumed prefix replays journaled *results* but does **not** re-apply the *file mutations* those agents produced. State the contract explicitly: for read-only and worktree-isolated agents this is safe (no shared mutable state, or the worktree is discarded); for **writable working-tree** agents (§7), resume after a partial mutation is **not** transactional — document that resuming a writable workflow assumes the working tree is in the state the journal left it, and recommend `isolation:'worktree'` for any agent whose mutations a later stage depends on.

**Deny-by-default realm** (`FID-resume-3`). The 3 banned APIs are necessary but not sufficient. The goja realm exposes **only** `args`, `log`, `phase`, `agent`, `parallel`, `pipeline`, `workflow` — no `fs`, `net`, `env`, `child_process`, `crypto`, `performance.now`, `process.hrtime`, locale-sensitive surprises, argless time, or `Math.random`. Determinism is a **security + correctness boundary**, not just three throws. Resolve **R1**: `new Date(<explicit-arg>)` is allowed (deterministic); argless throws.

**R-spec (do before E1).** Transcribe the native resume-invalidation matrix into the repo as a testable spec: {args change · upstream edit · downstream-only edit · prompt change · schema change} × {hit | live}, plus the native ordinal rule under `parallel`/`pipeline`. This is the fidelity target for the hardest property and is currently unpinned.

---

## 7. Execution context — writable default, worktree isolation, MCP attach (parity)

All three are **in the skeleton** (decided 2026-06-14).

- **Writable default.** Native's default `agent()` shares a **writable** working tree (that is *why* `isolation:'worktree'` exists — read-only agents never conflict). Every attn headless path is currently hardcoded read-only + feature-stripped (Codex `--sandbox read-only` + 16 `features.*=false`, `internal/agent/codex.go:111-141`; Claude `--permission-mode dontAsk` + empty tool allowlist) and runs in a throwaway `os.MkdirTemp` (`workspace_context_janitor.go:409`), *not* the working tree. Net-new: a **writable headless mode** — extend `HeadlessTaskRequest` with sandbox/AllowedTools/CWD; design the writable Codex (`--sandbox workspace-write`, re-enabled edit/shell tools) and Claude arg sets; set CWD to the run's working tree. **Security review required** — this re-enables exactly what the janitor locked down.
- **Worktree isolation** (`isolation:'worktree'`). Opt-in for parallel mutation; hand the subagent a fresh worktree as CWD (auto-removed if unchanged) over attn's existing worktree infra. The structured result still returns via `return_result`; mutations are the consumed side effect. Pairs with the §6 side-effect contract.
- **Subagent MCP attach** (parity, decided). Native subagents reach session-connected MCP tools. Attach the workflow session's MCP servers to the spawned subagent **in addition to** `return_result` (not instead of). Generalize `HeadlessTaskRequest` beyond its single `MCPServer*` triple to a list, and thread real tool names through each driver's argv (`enabled_tools` for Codex, `--allowedTools` for Claude) instead of the hardcoded `read_context,replace_context`.

### E3 security posture (implemented)

The writable headless mode landed in E3 (`internal/agent/{driver,codex,claude}.go`, `internal/workflow/driveragent.go`). The approved security boundary:

- **Opt-in + fail-closed.** `HeadlessTaskRequest.Sandbox` is `""` (read-only) by default; only `"workspace-write"` is writable, and any unrecognized value falls back to read-only. The workspace-context janitor sets none of the new fields, so it is **byte-identically** read-only — guarded by `TestJanitorShapedRequestStaysReadOnly`.
- **Codex = OS sandbox is the boundary.** `workspace-write` emits `--sandbox workspace-write` + `features.shell_tool=true`. On macOS the seatbelt confines writes to the process cwd + `TMPDIR` with **network disabled by default**. `approval_policy="never"` stays because the OS sandbox — not an interactive approval prompt — is the enforcement boundary (no human is in the loop; a prompt would only deadlock). We **never** emit `--dangerously-bypass-approvals-and-sandbox` and **never** use `danger-full-access`. Every other `features.*` stays `false` exactly as on the read-only path; only the sandbox mode and the shell tool change.
- **Claude = the allowlist is the boundary.** Claude headless has no OS seatbelt, so the tool allowlist itself is the boundary. `workspace-write` adds only `Edit`, `Write`, `MultiEdit`, `Bash` alongside the prefixed MCP tools, keeping `--permission-mode dontAsk` (which in `--print` mode auto-approves edits **and** bash without prompting; `acceptEdits` would not cover bash). No `--dangerously-skip-permissions`; no features beyond the attached MCP servers.
- **MCP attach is additive.** `ExtraMCPServers` are wired in addition to the primary server (the result sink / janitor tools), mirroring its emission exactly (`required=true`, `default_tools_approval_mode="approve"`, prefixed tool names). Empty list = no change.
- **Residual gap (not fixed in E3 — follow-up).** Codex `workspace-write` disables network by default, so a writable subagent that runs `go mod download`, `npm install`, `pip install`, etc. will fail offline. v1 expects cached/vendored dependencies; lifting this (a scoped network allowance or a pre-fetch step) is a deliberate follow-up, tracked here, not addressed in E3.

---

## 8. Safety + lifecycle (in scope)

The engine runs **foreign-authored JS spawning real paid subagents**. Guards decided 2026-06-14:

- **Hard caps** (kept even though `budget` is de-scoped): concurrency `min(16, cores-2)`; **1000-agent lifetime cap** (the true backstop against `while(true) await agent()` — `agent()` throws at the cap); 4096 items per `parallel`/`pipeline` call. Enforcement points named in the engine; "throws at the lifetime cap" is an **E1/E5 kill criterion**.
- **Interrupt watchdog**: goja `vm.Interrupt()` timeout so a CPU-bound/infinite script loop is killable (the socket-drop/stale-resumable path does **not** catch a live hang). **E1 kill criterion.** Also prove worker-goroutine results marshal safely back onto the single runtime goroutine (goja is not thread-safe).
- **Cancellation**: a `workflow_run_cancel` IPC the daemon relays to the engine process (control frame on the socket, or signal), which cancels in-flight subagent `context`s and cleans up orphaned children. Lets the user (watching the read-only UI) or a moved-on agent halt a run. Engine death → orphan reaping defined.
- **Completion notification** (NOT de-scoped). attn's `internal/attention` is a derived read-model (sessions/PRs implement a `Source`; the aggregator derives a needs-attention view — no push/OS notification today). Add a **workflow `Source` adapter** so finished / needs-input runs surface in the attention aggregator and the UI, parity with native's completion signal. `--wait`/poll remain available for the synchronous agent path.

---

## 9. Agent-facing contract (NEW — the core goal: "usable by other agents")

A Codex agent has none of Claude's native Workflow tool description. The full loop must be a **delivered, frozen contract**, not hand-waved.

- **Discover + author.** Ship a workflow reference at `internal/agent/attn_skill/references/workflow.md`, registered in the Capability Index, documenting every host fn + signature, the `meta` pure-literal-first-statement rule, the `{schema, opts}` shapes, and the **determinism bans with their deterministic substitutes** (so a foreign author doesn't reflexively reach for `Date.now()`/`Math.random()` and hit a runtime throw). The engine's throw on a banned API emits a **clear, agent-actionable** message (names the API + the allowed substitute).
- **Trigger.** `attn workflow run <script.js> [--args-file <json> | --args <json>] [--wait]`. `--args` takes a JSON document (`--args-file` for large/escaped payloads — avoids shell-quoting traps for programmatic callers); it becomes the script's `args`. Defaults to attaching the run to `ATTN_SESSION_ID` so it appears in that session's UI.
- **Observe.** Returns a `runId` immediately (or blocks with `--wait`). The run is attached to the triggering session/workspace (`workflow_runs.session_id/workspace_id`) so the user sees it live in attn.
- **Retrieve (frozen, resolves R4).** `attn workflow result <runId>` emits JSON to stdout: `{ status: enum, result: <value|null>, error?, phase, calls_total, calls_done }`. Exit code 0 on completed, non-zero on errored. `--wait` streams to completion then emits the same shape.
- **Inspect + resume.** `attn workflow show <runId>` (full run + per-call status/error) and `attn workflow list [--session]` so **any** agent — including a fresh one after the original died — can enumerate in-flight/stale runs, read `last_error`, see a `resumable` flag, and `attn workflow run --resume <runId>` (args rehydrate from the journal; the agent never re-serializes them).

---

## 10. Experiment campaign (restructured) + e2e harness

Throwaway-or-keep spikes, each gated by a kill criterion; we don't advance without the prior green. E1–E4 stand up the parity skeleton; E5–E6 complete fan-out + isolation. Run on the **dev profile** (`make dev`) — never prod.

| # | Experiment | Proves | Kill criterion |
|---|---|---|---|
| **E1** | **Engine core + structural-ordinal resume**, *fake* `agent()`. goja realm (deny-by-default + bans), interrupt watchdog, structural ordinals, journal, kill@k + resume with the ordinal+prompt_hash+schema_hash predicate, 1000-agent cap throws. | The spine + safety: deterministic replay under fake parallel/pipeline; infinite loop killable; caps enforced. | Resume can't be made deterministic under concurrency, or the watchdog can't kill a hang. |
| **E2** | **Real `agent()` with + without schema.** Generalized `RunHeadlessTask`: generic text capture, per-call `ResultPath`, `return_result` with in-turn validation + retry (`isError`), detect-missing-call, error→null. Real Codex. Confirm `--output-schema` traps. | The reliability layer: schema object out, in-turn retry corrects a mismatch, missing-call→null (not throw), free-text path returns text. | No reliable schema-valid output from real Codex via the tool. |
| **E3** | **Writable execution + MCP attach** (parity). Writable working-tree Codex/Claude run that mutates files AND returns a schema-valid `return_result`; a session MCP server attached and callable by the subagent. | Native default + MCP parity. Security boundary of writable mode understood. | Writable + structured-return can't coexist, or MCP attach breaks the result channel. |
| **E4** | **End-to-end skeleton through attn.** Engine spawned by an attn agent → daemon IPC journal → **coalesced** `workflow_run_updated` → read-only UI (attached to session) → result via `attn workflow result` → `workflow_run_cancel` halts a run → completion surfaces in the attention aggregator. The §10 harness loop, automated. | Integration + the agent contract + cancel + notify. | The agent→engine→attn→agent loop or cancel can't be made to work cleanly. |
| **E5** | **Parallel fan-out + caps.** `parallel`/`pipeline` never-reject + null-slot-on-throw + stage signature; concurrency `min(16,cores-2)`; ordinals stable under real concurrent dispatch; 4096-item guard. | Fan-out without breaking resume or error isolation. | Concurrency destabilizes structural ordinals. |
| **E6** | **Worktree isolation.** `isolation:'worktree'` over attn worktree infra; the §6 side-effect/replay contract holds. | Opt-in isolated mutation. | (low — infra exists) |

**Skeleton "done"** = this runs green, automated, on dev: an attn agent triggers `attn workflow run` → ≥1 real subagent returns a schema-valid object **and** a writable agent mutates the tree → journal rows persist → a coalesced broadcast reaches a websocket client → the UI shows the run under its session → the agent gets the result via `attn workflow result` → cancel halts a run → kill + `--resume` replays the prefix.

**Harness composition** (single-tenant for packaged-app per `AGENTS.md`): Go integration tests for E1–E3 + E5 (engine/journal/resume/caps + real `codex exec`); a real-app scenario (`pnpm --dir app run real-app:scenario-workflow-*`, honoring `ATTN_PROFILE`) for E4's UI + result + cancel; fixture scripts (linear, schema'd, writable, parallel, infinite-loop, never-calls-tool).

---

## 11. Data model / interfaces (sketch — refined during the spikes)

```text
// store (new migration; verify live MAX(version) in real ~/.attn + ~/.attn-dev DBs first — burned-version caveat)
workflow_runs(
  run_id PK, script_path, script_hash, args_json,
  session_id, workspace_id,                 -- run→UI attachment (default ATTN_SESSION_ID)
  status TEXT, phase TEXT, harness TEXT,
  result_json, last_error, resumable INT,
  created_at, updated_at, completed_at)
workflow_agent_calls(
  id PK, run_id FK ON DELETE CASCADE,
  ordinal TEXT,             -- STRUCTURAL path (phase·slot·item·stage·callsite); UNIQUE(run_id, ordinal)
  label, phase, prompt_hash, schema_hash,    -- match predicate: ordinal AND prompt_hash AND schema_hash
  resolved_model, resolved_harness, agent_type,  -- journaled so model is part of replay identity (R6)
  result_json, status TEXT, error,           -- status∈{running,ok,skipped,errored}; null result on skip/errored
  result_path,                               -- <run-tmp>/<ordinal>.json, one file per call
  started_at, completed_at)

// generalized agent-layer (internal/agent/driver.go)
HeadlessTaskRequest += {
  Schema json.RawMessage, ToolName string, ResultPath string,
  Label, Phase, CallID string,
  Sandbox enum{readonly,workspace-write}, AllowedTools []string, CWD string, Isolation enum{none,worktree},
  MCPServers []MCPServerSpec }              // a LIST now (session servers + return_result), not one triple
HeadlessTaskResult += { Result json.RawMessage, Text string }   // generic value + free-text path

// daemon IPC (engine → daemon, one frame/conn ≤64KB; daemon → engine = cancel control frame)
workflow_run_upsert {run} · workflow_call_upsert {run_id, call}   // persist + COALESCED broadcast
workflow_run_get / list                                          // resume + `attn workflow list/show`
workflow_run_cancel {run_id}                                     // daemon relays to engine process

// protocol (main.tsp → make generate-types → bump ProtocolVersion + PROTOCOL_VERSION)
WorkflowRun + WorkflowRunStatus (mirror ReviewLoopRun*) ; WorkflowRunUpdatedMessage ; WorkflowActionResultMessage

// model resolution (R6): opts.model → meta.model → fixed engine default  (NEVER "triggering agent's harness")
```

---

## 12. Resolved decisions + remaining open questions

**Resolved (2026-06-14):** R1 — `new Date(arg)` allowed, argless throws. R2 — validation + retry **in-turn** in the toolserver (`isError`), engine re-spawn as outer fallback. R3 — MCP attach is **parity now**. R4 — result contract **frozen** in §9. R6 — model precedence `opts.model → meta.model → engine default`, journaled. Writable execution, caps, watchdog, cancel, notification all **in scope**.

**Open (decide during spikes):** R5 — broadcast coalescing window (~100–250ms or call-level deltas keyed by ordinal) + the forced-disconnect risk (a slow client is dropped with `StatusPolicyViolation "client too slow"` after `maxSlowCount=3`, `websocket.go:391-413`) — prove a high-fan-out workflow doesn't disconnect the UI in E4. R-spec — transcribe the native resume-invalidation matrix before E1. Security review of writable mode (E3). Exact structural-ordinal encoding (E1).

---

## 13. v1 → v2 changelog (what the adversarial review changed)

35 verified findings folded in. Material reversals from v1:

- **"`agent()` primitive already exists" → spawn half only.** `RunHeadlessTask` returns no value; toolserver does zero validation; "forced" is server-presence, not tool-invocation. Result/validation/retry/null/text are net-new (§1, §4, §5).
- **Resume ordinals: invocation-order → structural.** The v1 "single-goroutine goja makes ordinals stable" reasoning was wrong for pipeline/post-await calls; match predicate now uses prompt_hash+schema_hash; per-call result files (§6).
- **Writable default exposed as net-new** and promoted into the skeleton (§7, E3) — every current path is read-only.
- **Parity additions** (Victor, 2026-06-14): subagent MCP attach (§7), hard caps + watchdog + cancel + completion notification (§8).
- **Agent-facing contract** promoted from open question to a frozen §9 — the core goal was previously hand-waved.
- **Accuracy fixes:** IPC is one-frame/≤64KB request-response (not streaming); broadcast overflow disconnects the UI; `review_loop` is a persistence-shape template only; model resolution precedence (R6); error→null contract; double-call last-write-wins; deny-by-default realm.
```
