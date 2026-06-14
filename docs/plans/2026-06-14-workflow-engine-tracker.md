# Portable Workflow Engine — Execution Tracker (autonomous goal)

**Branch:** `feat/workflow-engine` · **Worktree:** `/Users/victor/projects/victor/attn--feat-workflow-engine`
**Design:** [2026-06-14-portable-workflow-engine.md](2026-06-14-portable-workflow-engine.md) (v2)
**Driver:** self-paced `/loop`. Each step = one ultracode workflow (spec→build→verify→fix). Advance on workflow-completion notifications; long fallback heartbeat in case a workflow hangs.

## Rails (non-negotiable)

1. All work on `feat/workflow-engine` in the worktree. **Never** touch `main` or prod; **never** `make install` / packaged-app / app builds autonomously (needs keychain/prod).
2. Commit a step **only** when independently verified green: `cd <worktree> && go build ./...` + that step's `go test`. Report honestly; do not claim green if not.
3. One step → one ultracode workflow, scoped to that step's files only (sequential builder, parallel read-only verify, conditional fixer — no parallel editors).
4. **PAUSE at HUMAN-GATE steps** — implement + unit/integration-test on the branch, then STOP the loop and surface to Victor with exactly what's needed. Do not auto-proceed past a gate.
5. Migration numbering: verify live `MAX(version)` in real `~/.attn` + `~/.attn-dev` DBs first (burned-version caveat). ProtocolVersion bumps are gates (need coordinated daemon/app + real-daemon check).
6. Keep this tracker current each step (status + commit sha). Trust `go build` over gopls (multi-worktree false diagnostics).

## Steps

| # | Step | Scope | Verify | Gate | Status |
|---|------|-------|--------|------|--------|
| E1 | Engine spine (fake `agent()`) | `internal/workflow`: goja realm + determinism bans + watchdog + **structural ordinals** + journal iface + kill/resume + caps | `go test ./internal/workflow/... -race` | auto | **✅ done** |
| E2 | Real headless `agent()` | generalize `RunHeadlessTask` (generic result+text capture, per-call `ResultPath`); `return_result` MCP tool w/ **in-turn** schema validation + retry + detect-missing-call + error→null; no-schema text path | `go test` + a real `codex exec` round-trip | auto¹ | **✅ done** |
| E3 | Writable exec + MCP attach | writable working-tree sandbox (re-enable edit/shell) that mutates tree **and** returns schema-valid result; attach session MCP servers to subagent | `go test` + writable round-trip | **GATE** (security review of re-enabling write/shell in headless) | pending |
| S-store | Durable journal | `workflow_runs` (+`session_id`/`workspace_id`) + `workflow_agent_calls` migration + store methods; swap E1 in-memory `Journal` for the durable impl | `go test ./internal/store/...` | auto (migration-number check) | **✅ done** |
| S-proto | Protocol | TypeSpec `WorkflowRun*` models + `make generate-types` + `constants.go` cmds/events + **ProtocolVersion bump** | `go build` + `tsc` | **GATE** (version bump → coordinated daemon/app) | pending |
| S-daemon | Daemon IPC + runner | `workflow_run_upsert/call_upsert/get/list/cancel`; coalesced broadcast; async runner; cancel relay; completion `Source` adapter | `go test ./internal/daemon/... -run Workflow` | auto² | pending |
| S-cli | Agent-facing contract | `attn workflow run/result/show/list` + `--args`/`--args-file` + runId→`ATTN_SESSION_ID` attach + authoring reference (`internal/agent/attn_skill/references/workflow.md`) | `go test` + CLI smoke | auto | pending |
| S-fe | Read-only UI | `WorkflowRunView` (clone `SessionReviewLoopBar`, strip controls) + `useDaemonSocket` wiring + store slice | `pnpm test` (unit) | **GATE** (packaged-app verify needs `make`) | pending |
| E4 | E2E skeleton harness | dev-profile loop: agent triggers → engine → journal → coalesced broadcast → UI → `attn workflow result` → cancel → resume | real-app scenario on dev profile | **GATE** (needs `make dev` / real daemon) | pending |
| E5 | Fan-out + caps | `parallel`/`pipeline` never-reject + null-slot + stage sig; concurrency `min(16,cores-2)`; ordinals stable under real concurrency; 4096 guard | `go test ./internal/workflow/... -race` | auto | pending |
| E6 | Worktree isolation | `isolation:'worktree'` over attn worktree infra; §6 side-effect/replay contract | `go test` + worktree round-trip | auto | pending |

¹ E2 needs working `codex` headless auth (OPENAI creds) for the live round-trip; if absent, implement + unit-test and flag the live check.
² real-daemon E2E for S-daemon is folded into E4 (a gate); S-daemon itself is unit/integration-tested.

## Order & dependencies

Spine first: **E1 → E2 → E3(gate)**. Then persistence/transport: **S-store → S-proto(gate) → S-daemon → S-cli**. Then surface + proof: **S-fe(gate) → E4(gate)**. Then completeness: **E5 → E6**. The loop runs each `auto` step to green-and-commit, and stops at the first `GATE` it reaches with a report. Auto steps that have no gate dependency ahead of a gate may be reordered earlier to maximize unattended progress (e.g. S-store, S-cli, E5 are gate-free Go work).

## Log

- **E1 — engine spine: DONE.** 32 tests green under `-race` (3201 LOC). goja realm + deny-by-default + determinism bans (incl. the `Date()`-as-function bypass), `vm.Interrupt` watchdog, **structural ordinals** (incl. the post-await parallel/pipeline temporal-leak fix the review caught), in-memory `Journal` + resume (R1–R5 + kill@k), lifetime/concurrency/item caps. R-spec (`2026-06-14-resume-invalidation-spec.md`) + this tracker committed alongside. The adversarial review found 3 real must-fix defects (post-await temporal ordinals breaking R1, `Date()` bypass); the fix phase resolved them with regression tests that fail pre-fix / pass post-fix.
- **E2 — real headless `agent()`: DONE.** Generalized `RunHeadlessTask` (`Schema`/`ToolName`/`ResultPath` + `Result`/`Text` capture); new `internal/workflowresult` schema-validating `return_result` sink (jsonschema v6, in-turn `isError` self-correction, last-write-wins) + `attn _workflow-result-mcp` subcommand; `driverAgent` wires `agent()` to real spawns with detect-missing + outer re-spawn retry + error→null + schema/text paths. **Live `codex exec` round-trip returned a schema-valid `{"answer":"PONG"}`.** Janitor untouched; all affected packages green under `-race`; review found 0 must-fix. `driverAgent` is built+tested but not yet wired into a production caller (that's E4's `attn workflow run`).
- **S-store — durable journal: DONE.** Migration 50 (`workflow_runs` + `workflow_agent_calls`, declarative `CREATE … IF NOT EXISTS`, idempotent on reopen); store CRUD in `workflow_runs.go` (store-local row structs, modeled on `review_loop_runs.go`; manual child-delete since FK CASCADE is documentary store-wide — `PRAGMA foreign_keys` is never enabled); `DurableJournal` adapter (`workflow` imports `store`, no cycle) with a write-through mem mirror seeded from SQLite so a fresh adapter resumes from the DB alone. **Journal-parity test runs E1's R1–R5 + kill@k over both `[mem, durable]`**, durable arm rebuilt from SQLite. Green under `-race`; review 0 must-fix. (Adapter writes the 6 `JournalEntry` fields; the richer columns — label/phase/resolved_model/result_path/timestamps — are populated by the S-daemon write path.)
