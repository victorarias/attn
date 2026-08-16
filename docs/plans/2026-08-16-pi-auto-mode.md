# Plan: pi auto mode — the safety envelope's autonomy dial

Rock 4 of [the pi vision](../vision/pi-attn-plugins.md). Superseding the
[superdraft](2026-08-16-pi-auto-mode-superdraft.md), which holds the design
conversation; this doc is what gets built. Grounding: pi 0.84.2 extension
surface (attn pins 0.83.0; the `tool_call`/dialog/`completeSimple` seam is
unchanged between them), Claude Code's documented auto-mode behavior, and the
receipt below.

## What it is

pi has no permission system at all — YOLO by design. Auto mode is a pi
extension that adds one without adding ceremony: a static **safety envelope**
(work inside the session's working directory runs free), a **classifier**
(a cheap model judges everything that reaches further, against the
conversation's intent), and **conversational approval** (a denied agent asks
in its reply; the user's answer in the transcript is the grant). No
click-to-approve dialogs, ever — that is the vision's stated non-goal.

## Receipt: classifier latency/cost/quality

`plugins/attn-pi/spike-harness/s7-classifier-receipt.js`, 16-case corpus
(safe builds, intent-backed pushes, destructive git, exfil curl, out-of-cwd
writes, a stated boundary, genuine ambiguity), run 2026-08-16 on this
machine's pi auth (opencode-go + openai-codex):

| model | p50 | p90 | $/1000 calls | wrong verdicts |
| --- | --- | --- | --- | --- |
| opencode-go/glm-5.3 | 1.7s | 4.0s | $0.61 | 0 of 48 |
| openai-codex/gpt-5.6-luna | 2.9s | 3.9s | $0.14 | 3 of 48 (same case, over-deny) |
| opencode-go/qwen3.8-max | 4.1s | 7.4s | $1.88 | 0 of 16 |
| opencode-go/minimax-m3 | 2.8s | 4.6s | $0.38 | 1 of 16 |
| opencode-go/kimi-k3 | 5.7s | 10.0s | $2.79 | 0 of 16 |
| opencode-go/deepseek-v4-flash | 5–6s | 14s | $0.07 | 1 of 16 |

Conclusions the design leans on:

- **Cost is a non-issue** (hundreds of classified calls/day ≈ under $1).
- **Latency is the axis.** Nothing beats ~1.7s p50, so the envelope and the
  verdict cache carrying the hot path are load-bearing, not optimizations.
- Defaults (user-overridable settings, never pins): classifier
  `opencode-go/glm-5.3`, layer-2b escalation `opencode-go/qwen3.8-max`.
  The seats pick the models: 2a runs on every classified call, so the
  fastest accurate model wins it (deepseek is cheapest but always burns
  reasoning tokens — p90 14s — and the price gap is pennies/day). 2b must
  be a different, stronger judge than 2a or escalation adds latency
  without information; among the stronger candidates qwen beat kimi on
  both latency and price. (glm, kimi, and qwen all had zero wrong
  verdicts; the only spread was one judgment-call case.)

## Critical UX (the moments that must feel right)

1. **Envelope invisibility.** Normal work — reads, in-cwd edits, boring bash
   — adds zero latency and zero classifier calls. If a quiet session shows
   classifier traffic, that is a defect.
2. **The denial.** The run does not stop. The agent gets the reason as its
   tool result, adapts or asks; the user sees what was blocked and why
   without being interrupted (TUI widget; attn notification + session
   activity).
3. **The grant.** A plain reply ("go ahead, push it") is the approval. No
   dialog, no rule editing, nothing to click. It works because the
   classifier reads the transcript and explicit user intent outranks soft
   policy.
4. **The breaker.** Repeated denials of the same intent surface one human
   question — `ctx.ui.confirm` in TUI, a normal waiting-on-you attention
   signal in attn (yellow, not flashing yellow). One answer resumes auto.
5. **Latency honesty.** A classified call shows "checking…"-grade feedback
   (TUI status / working message) so ~2s never reads as a hang.
6. **Trust at a glance.** The status line always shows auto on/off; recent
   denials are inspectable (`attn automode denials`, session activity).

## Public interface

In the session (pi side):

- `/auto` toggles; `--auto` / `--no-auto` flags (registerFlag) force it at
  launch; status indicator shows the mode. Default: on for attn sessions,
  off for bare pi.
- Denial tool-result contract (the model-facing API): names auto mode, the
  blocked action, the reason, and states that the user's explicit approval
  in conversation permits a retry. A system-prompt addendum (via
  `before_agent_start`) says auto mode is active and how grants work.
- Custom/unknown tools outside the envelope are denied with a reason naming
  the limit (no silent budget).

attn CLI (agent- and human-reachable; every verb has `--json`):

- `attn automode show` — effective config + mode defaults.
- `attn automode env [set|add|remove]` — environment prose entries.
- `attn automode allow <pattern>` / `deny <pattern>` — record a **proposal**
  only, and say so: promotion happens in the app. Broad patterns (`*`) are
  refused at submission.
- `attn automode model <classifier|escalation> <provider/id>` — also a
  proposal.
- `attn automode denials` — recent denials with reasons.

The CLI proposes; only the app promotes. Human input in the app is the trust
boundary no CLI caller can fake — this is what keeps an agent from writing
its own leash. Belt and suspenders: automode's storage and config paths are
born hard-denied to sessions.

attn app:

- Settings: a minimal **proposed rules** section (list, promote, discard)
  ships with v1. The full harness/model panel comes later on the same
  storage.
- Session launch surface: an auto on/off parameter, defaulting from config.
- Denials appear as notifications + session activity.

Config schema (daemon-owned storage, global scope in v1, versioned/migrated
from day one — a shipped CLI writer means user state exists):

- `enabled_default` (bool, attn sessions), `environment` (prose entries),
  `allow` (narrow patterns), `hard_deny` (patterns), `classifier_model`,
  `escalation_model`.

Bare pi fallback: the standalone extension reads the same schema from a file
under the pi config dir; no attn required.

## Decision path (per tool call, in order)

1. Hard denies → block.
2. Allow list → run.
3. Read-only tools (`read`, `grep`, `find`, `ls`) → run.
4. `write`/`edit` resolving inside cwd, outside protected paths (`.git`,
   `.pi`, agent/shell config) → run.
5. `bash` matching the static read-only set → run. Network never rides the
   envelope: `curl` and friends always classify, even a plain GET.
6. Everything else (bash, out-of-cwd writes; v1's classified surface) →
   classifier. Unknown custom tools → deny with a named limit.

Classifier (layer 2a → 2b):

- Sees recent transcript + pending call + environment prose. Never tool
  results (injection surface).
- Internal precedence mirrors Claude's documented order: config hard denies
  are unconditional; explicit user intent naming the exact action clears
  everything softer; then policy judgment.
- Verdicts: allow / deny / uncertain. Uncertain escalates to the named
  escalation model for the final verdict (2b), never implicitly the
  session's main model.
- Allow verdicts cache per normalized intent for the session; deny verdicts
  drop on new user input (the next message may be the grant).
- Circuit breaker: 3 consecutive (or 20 total) denials → one human
  question; any approval resets. No-UI contexts (`-p`, json) fail closed.
- Both layers pass `ctx.signal` and report usage into session totals.

## Architecture

- `plugins/attn-pi/automode/` — self-contained extension module, pure pi
  API, no relay dependency. Factory takes optional hooks (`onDenial`,
  config source). `automode/standalone.ts` default-exports it for bare pi.
- `suite/` composes the same factory for attn sessions and adds attn-side
  reporting (denials over the relay: new `suite.report_denial` method in
  `src/relay-protocol.ts`, delegate wiring in `src/index.ts`).
- Daemon: automode config storage + migrations in `internal/store`; config
  injection rides the existing launch path (driver spawn params, like
  launch instructions); denial reports become a bus fact
  (`automode.denied`) with a `wireProjections` entry; proposals + settings
  reads/writes are new protocol commands/events (TypeSpec →
  `make generate-types` → `ProtocolVersion` bump).
- Staging: `build-bundled-plugins.sh` bundles `automode/` (same rules as
  `suite.js`: `@earendil-works/pi-coding-agent` external, version triple
  check).

## Slices (each a PR; merge to main as they land)

1. **Policy core.** `automode/` decision tree: envelope, path resolution,
   read-only bash matcher, allow/hard-deny lists, verdict types, denial
   text contract. Classifier stubbed behind an interface. Ships dark.
2. **Classifier.** Prompt, 2a/2b model calls via the registry, verdict
   cache, circuit breaker, system-prompt addendum, usage accounting.
3. **pi UX.** `/auto`, flags, status indicator, TUI denial widget +
   checking feedback, fail-closed no-UI behavior, standalone bundle for
   bare pi.
4. **attn config + CLI.** Storage + migrations, `attn automode` verbs with
   propose semantics, daemon injection into spawn, protocol additions.
5. **attn visibility + promotion.** Denial fact → notification + session
   activity; proposed-rules settings section (promote/discard); launch
   parameter toggle.
6. **Live verification pass.** Harness scenario, daily-driver soak,
   evidence recordings; defaults flipped on for attn sessions.

## How to verify

- **Unit (bun, per slice).** The decision tree gets exhaustive table tests;
  the s7 corpus doubles as classifier-prompt fixtures with a mocked model.
  A rapid property beside the path/envelope logic (resolved path inside cwd
  never classifies; nothing outside the envelope ever silently runs).
- **Go tests** for storage, CLI verbs (propose-only semantics), protocol,
  and the bus projection (the projection tables enforce their own fixtures).
- **Receipt gate.** s7 stays the model-quality gate; rerun it on pi pin
  bumps like the other spike scenarios.
- **Deterministic harness scenario.** `scenario-pi-automode`: a packaged-app
  pi session attempts an out-of-envelope call against a **local stub
  provider** (pi's `registerProvider` pointed at a loopback HTTP server) so
  verdicts are deterministic; assert the denial reaches the app, the
  conversational grant unblocks the retry, and the breaker escalates after
  3 denials. No live-model flake in CI.
- **Live tier.** Slices 3–6 change protocol, launch, and UI surfaces: full
  `make install PROFILE=<name>` verification with preflight, an evidence
  recording on the PR, and `attn profile clean` after. Slice 6 includes a
  quiet-session check: auto mode on, session idle → zero classifier calls,
  zero recurring work.
- **Live classifier smoke** (not CI): one real run of the harness scenario
  with glm-5.3 before flipping the attn default on.

## Non-goals

Ask rules and a precedence engine; per-workspace scope; MCP-specific
handling; classifying reads; the full harness/model settings panel (own
work, same storage); porting auto mode to claude/codex sessions.
