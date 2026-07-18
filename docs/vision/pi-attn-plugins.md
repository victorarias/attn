# Vision: pi as the daily-driver harness — the attn plugin suite

> Grounded in pi's actual extension surface ([earendil-works/pi](https://github.com/earendil-works/pi),
> `packages/coding-agent`) and in openclaw's history of embedding pi
> ([openclaw/openclaw](https://github.com/openclaw/openclaw)). Builds on attn's
> plugin system ([docs/plans/2026-04-16-plugin-system.md](../plans/2026-04-16-plugin-system.md)),
> the earlier pi plans ([2026-04-16-pi-plugin.md](../plans/2026-04-16-pi-plugin.md),
> [2026-04-07-pi-integration.md](../plans/2026-04-07-pi-integration.md) — still
> behavior-authoritative), and the shipped `plugins/attn-opencode` driver.

## End state (the why)

Victor opens a session and it's pi — but it doesn't feel like a downgrade from
claude code, it feels like *his* harness. The features that earn their keep are
there: auto mode with a real permission gate, subagents fanning out, skills and
slash commands, compaction that respects his conventions, a Monitor that wakes
the agent when the build goes red, background commands that ring the doorbell.
Under it, any model — Claude next to GPT next to whatever ships next month —
switchable mid-session, one muscle memory across all of them.

And it's a *native attn citizen*, not a guest observed through hooks and
transcript scraping: the agent reports its own state through structured events,
takes ticket nudges and doorbell wakes as in-band steering, links its session to
attn's on birth. The claude-code integration is attn adapting to a closed
harness from the outside; pi is the harness attn gets to shape from the inside.

Why it matters: attn's model is many agents, one Victor. Today the harness
underneath is someone else's product — its roadmap, its model lock-in, its
opaque state. pi is small, open, and extensible at every seam that matters.
Owning the harness layer as *plugins* — not a fork — buys the leverage without
the maintenance treadmill.

## The central decision: plugin suite, not a fork

**Decision: one main attn package for pi, composing focused extensions.** pi
packages declare `pi.extensions/skills/prompts/themes` in `package.json` and
install via `pi install git:...` / local path ([docs/packages.md](https://github.com/earendil-works/pi/blob/main/docs/coding-agent/packages.md)).
The suite is that one package; each capability inside it is a separate
extension file that stands alone.

Every feature on the wishlist maps to a documented extension point (table
below). The extension API is deep enough that fork-territory is genuinely
narrow: events fire at every seam (`tool_call` can block/mutate,
`context` edits the message array pre-call, `before_agent_start` swaps the
system prompt, `before_provider_request` rewrites the raw payload), extensions
can inject turns (`pi.sendMessage({triggerTurn, deliverAs: "steer"|"followUp"|"nextTurn"})`),
register tools/commands/shortcuts/providers, own UI widgets and overlays, and
override compaction (`session_before_compact`). See
`packages/coding-agent/src/core/extensions/types.ts` for the full surface.

**Rejected: forking pi.** openclaw is the cautionary tale, not the template.
It never used pi's RPC mode — it embedded pi in-process via exact-pinned npm
packages, spent months fighting nested retry loops, event-name churn, and
opaque error sentinels, and finally vendored the whole runtime into its tree
(`@openclaw/agent-core`, `@openclaw/ai`; only `pi-tui` survives as a real
dependency). But openclaw forked because it needed to *own* compaction policy,
retries, failover, and persistence — it bet its product on being the runtime.
attn needs none of that ownership: it needs lifecycle signals, steering, and
feature extensions — exactly what the extension API sells. If a future need
crosses into loop-ownership territory, that is a new decision to make then,
with openclaw's scars as the price list; it is not part of this vision.

**Also rejected (for now): RPC-first embedding.** `pi --mode rpc` (JSONL over
stdio, [docs/rpc.md](https://github.com/earendil-works/pi/blob/main/docs/coding-agent/rpc.md))
would make attn render the conversation itself — a much larger build, and RPC
hosts cannot register tools or event handlers over the wire anyway (extensions
must ship on disk regardless). PTY + extension gets the value at a fraction of
the surface. RPC embedding stays available if a Present-style native rendering
of pi sessions ever wants it.

## The shape: two plugin systems meet in the middle

Both sides are plugins; neither harness gets forked or patched:

- **attn side — `plugins/attn-pi`**, an agent-driver plugin per attn's own
  plugin system (Bun subprocess, JSON-RPC on `attn.sock`, `driver.register` /
  `driver.spawn` → argv; attn owns the PTY). The opencode plugin is the
  precedent; the 2026-04-16 pi-plugin plan is the sketch. It stages the pi-side
  package and launches `pi` with it.
- **pi side — the attn suite**, one pi package composing the extensions below.
  Loaded per-launch (`-e` / staged package), it phones the daemon over the unix
  socket: session linking on `session_start`, state from agent lifecycle
  events, and inbound steering (doorbell, ticket nudges) via `pi.sendMessage`.

State reporting flips from inference to declaration: today attn *deduces*
claude's state from hooks, PTY heuristics, and a stop-time classifier; the pi
extension *reports* it from `agent_start/end/settled`, `tool_execution_*`, and
the permission gate — same authority model as the hook-authoritative states,
with fewer guessing layers.

## Per-feature map (wishlist → pi extension point)

All paths under `packages/coding-agent` unless noted; examples under
[`examples/extensions/`](https://github.com/earendil-works/pi/tree/main/packages/coding-agent/examples/extensions).

| Feature | pi extension point | Status in pi |
| --- | --- | --- |
| Auto mode / permission modes | `tool_call` event blocks/mutates pre-execution + `ctx.ui.confirm`; `permission-gate.ts`, `plan-mode/` examples | Deliberately absent natively; extension-buildable, worked examples |
| Subagent orchestration | `registerTool` spawning child `pi --mode json` processes; `subagent/` example (parallel/chain modes, `agents/*.md`) | Absent natively; worked example |
| Skills / commands | Native: agentskills.io spec, `.pi/skills`, `/skill:name`, prompt templates `/name`, `registerCommand` | Native |
| Compaction | Native auto-compaction + `/compact`; `session_before_compact` full override (`custom-compaction.ts`) | Native + overridable |
| Monitor / background wake | Any async callback → `pi.sendMessage({triggerTurn: true, deliverAs})`; `file-trigger.ts` is the pattern | Extension-buildable, first-class steering API |
| Workflows / orchestration | SDK (`createAgentSession`) for deterministic pipelines; subagent chain mode; `packages/orchestrator` exists | Absent in coding-agent; buildable |
| Multi-model | Native: ~40 providers (`packages/ai/src/providers/`), mid-session `/model` + `set_model`, `registerProvider` for custom ones | Native |
| attn state / presence | `agent_start/end/settled`, `turn_*`, `tool_execution_*` events → daemon socket | Extension-buildable |
| Doorbell / ticket nudges | Unix-socket listener in extension → `sendUserMessage(..., {deliverAs: "steer"})` | Extension-buildable |
| Session linking / resume | `session_start` event, `appendEntry` for persisted attn metadata; native session tree, `-r`/`--session` | Native + extension glue |
| Custom system prompt | `.pi/APPEND_SYSTEM.md` / `SYSTEM.md`; per-turn via `before_agent_start` | Native |
| MCP (if ever needed) | Deliberately absent; bridgeable via `registerTool` | Absent by design |

**Fork-pressure points, honestly:** the core agent loop's turn structure
(`packages/agent`), the transcript renderer layout (individual message types
re-renderable, screen structure fixed), and built-in tool internals. Nothing
on the wishlist needs any of them today.

## North-star principles

- **Plugins on both sides, forks on neither.** attn integration ships as an
  attn driver plugin; pi capability ships as a pi package. No in-tree Go
  driver, no vendored pi. If a feature can't be expressed, that's a named
  escalation, not a quiet patch.
- **Declared state beats inferred state.** The extension reports what the
  agent *is doing*; scraping and stop-time classification are fallbacks, not
  the design.
- **One package, composable inside.** A single `pi install` gets the suite;
  each extension inside works alone and earns its place alone. No monolith
  where disabling auto mode breaks the doorbell.
- **Parity by value, not by checklist.** Daily-driver is the destination, but
  each feature ships when it's the next most valuable thing, judged against
  actually living in pi — not to tick a claude-code comparison box.
- **Pin pi like a protocol.** openclaw's event-name churn is the warning:
  pin the pi version, treat the extension API surface as versioned, and gate
  launches on a compat check — the same reflex as attn's `ProtocolVersion`.
- **The agent stays a guest in attn's house.** attn owns the PTY, the session
  lifecycle, and the outer harness (tickets, delegation, notebook, presence).
  pi owns the loop, the models, the context. The seam is the socket.

## Scope & non-goals

**In scope:** the `plugins/attn-pi` driver plugin; the pi-side attn suite
(state/link/steering extension, permission gate + auto mode, subagents,
monitor/background wake, workflow tooling, compaction tuning, skills/prompts
curation); multi-model daily use; living in it.

**Non-goals:** forking or vendoring pi; RPC-mode embedding and attn-rendered
conversations (revisit only if a concrete surface like Present demands it);
building MCP support before something needs it; migrating claude/codex/copilot
integrations to this pattern (zero-pressure, per the plugin-system plan);
feature-parity for claude-code features Victor doesn't actually use.

## Big rocks (the arc)

- [ ] **attn-pi driver plugin** — spawn/resume/state plumbing per the
      2026-04-16 pi-plugin plan, revalidated against today's plugin API and
      the opencode precedent.
- [ ] **attn-link extension** — session tie, declared state, doorbell/nudge
      steering. The moment pi becomes a first-class attn citizen.
- [ ] **Permission gate + auto mode** — the safety/velocity dial; unlocks
      `pending_approval` as a real reported state.
- [ ] **Subagents** — orchestration parity for the fan-out workflows.
- [ ] **Monitor + background wake** — background commands that re-prompt the
      main agent.
- [ ] **Skills/prompts curation** — port the skills that matter; decide reuse
      vs. rewrite per skill.
- [ ] **Daily-driver trial** — live in it for real work; the feedback loop
      that reorders everything above.
- [ ] **Workflows / deterministic orchestration** — likely last; needs the
      most design.

## Open questions

- **Skill reuse:** pi implements the agentskills.io spec and reads
  `.agents/skills/`. How much of the existing `~/.claude/skills` corpus works
  unmodified, and is a shared skills dir the right move vs. curation?
- **Classifier's fate:** with declared state from the extension, does the
  stop-time WAITING/DONE classifier still run for pi sessions, or does the
  extension's `agent_settled` + gate state make it redundant? (Old pi plan
  kept a classifier-with-hints; revisit with the richer event set.)
- **Suite packaging details:** one package with many extensions vs. a small
  family (`attn-core` + optional `attn-auto`, `attn-subagents`, ...) under one
  install. Decide when the second extension lands.
- **Auto-mode semantics:** map claude's permission-mode vocabulary onto the
  gate, or design pi-native modes (plan/ask/auto/yolo) fresh?
- **Blindspots (ground before first chunk):** pi's extension runtime under
  long-lived real sessions (jiti loading, error isolation, `session_shutdown`
  cleanup discipline); pi's release cadence and API stability in practice;
  how pi's TUI behaves under attn's PTY geometry rules (resize authority,
  replay) compared to claude/codex.
