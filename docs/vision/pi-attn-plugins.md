# Vision: pi as the daily-driver harness — the attn plugin suite

> The what and the why. Each capability below gets its own implementation-level
> document when its turn comes; none of that detail lives here. Grounding for
> the decisions: pi's extension surface ([earendil-works/pi](https://github.com/earendil-works/pi)),
> openclaw's history of embedding pi ([openclaw/openclaw](https://github.com/openclaw/openclaw)),
> and attn's plugin system ([docs/plans/2026-04-16-plugin-system.md](../plans/2026-04-16-plugin-system.md),
> with the earlier pi plans [2026-04-16-pi-plugin.md](../plans/2026-04-16-pi-plugin.md)
> and [2026-04-07-pi-integration.md](../plans/2026-04-07-pi-integration.md)).

## End state (the why)

Victor opens a session and it's pi — but it doesn't feel like a downgrade from
claude code, it feels like *his* harness. The capabilities that earn their keep
are there: real autonomy without babysitting, subagents fanning out, skills and
slash commands, compaction that respects his conventions, background eyes that
wake the agent when something changes. Under it, any model — Claude next to GPT
next to whatever ships next month — switchable mid-session, one muscle memory
across all of them.

And it's a *native attn citizen*, not a guest observed through hooks and
transcript scraping: the agent reports its own state, takes ticket nudges and
doorbell wakes as in-band steering, links its session to attn's on birth. The
claude-code integration is attn adapting to a closed harness from the outside;
pi is the harness attn gets to shape from the inside.

Why it matters: attn's model is many agents, one Victor. Today the harness
underneath is someone else's product — its roadmap, its model lock-in, its
opaque state. pi is small, open, and extensible at every seam that matters.
Owning the harness layer as *plugins* — not a fork — buys the leverage without
the maintenance treadmill.

## The central decision: plugin suite, not a fork

**Decision: one main attn package for pi, composing focused extensions** —
installed with a single `pi install`, each capability a piece that stands
alone. Everything this vision wants maps onto pi's supported extension surface;
the verified feature-by-feature mapping informs each capability's own doc.

**Rejected: forking pi.** openclaw is the cautionary tale, not the template:
it embedded pi in-process, fought version churn and nested control loops, and
ended up vendoring the entire runtime. But it forked because it bet its product
on *owning* the loop — compaction policy, retries, failover, persistence. attn
needs none of that ownership; it needs signals, steering, and capabilities,
which is exactly what extensions provide. If a future need ever crosses into
loop-ownership territory, that's a new decision made then, with openclaw's
scars as the price list.

**Also rejected (for now): RPC-first embedding.** pi can run headless with a
host rendering the conversation; that's a much larger build than attn needs,
and it doesn't remove the need for on-disk extensions anyway. It stays
available if a Present-style native rendering of pi sessions ever wants it.

## The shape: two plugin systems meet in the middle

Both sides are plugins; neither harness gets forked or patched:

- **attn side** — an agent-driver plugin per attn's own plugin system, like
  the opencode plugin before it: it launches pi into an attn-owned PTY and
  carries the session's lifecycle.
- **pi side** — the attn suite, a pi package the driver stages: it links the
  session, declares state, delivers doorbells and nudges as steering, and
  carries the harness capabilities.

State flips from inference to declaration: today attn *deduces* claude's state
from hooks, heuristics, and a stop-time classifier; the pi extension *reports*
it. Fewer guessing layers, same authority model.

## The capabilities (each gets its own doc)

- **Autonomy with a safety envelope.** Not a permissioning system — no
  approving things one by one. A simple policy declares what's inherently
  safe: the worktree pi has open is the agent's to read and write, no
  ceremony. Everything outside the envelope — bash, anything that reaches
  further — rides auto mode. Easy, safe defaults; pressure off.
- **Subagent orchestration.** Fan-out and delegation inside the session.
  Prior art exists in the pi ecosystem — we adapt, not invent.
- **Skills and commands.** pi speaks the same skills spec; the existing
  corpus should largely carry over, curated rather than rewritten.
- **Compaction, tuned.** Native in pi and fully overridable; shape it to
  Victor's conventions.
- **Background eyes.** Monitors and background commands that wake the main
  agent when the world changes — pi's steering API makes this first-class.
- **Multi-model, one place.** Native to pi: dozens of providers, mid-session
  switching.
- **attn citizenship.** Session linking, declared state, doorbell/ticket
  steering — the piece that makes all of the above legible to the outer
  harness.

## North-star principles

- **Plugins on both sides, forks on neither.** If a capability can't be
  expressed as a plugin, that's a named escalation, not a quiet patch.
- **Declared state beats inferred state.** Scraping and stop-time
  classification are fallbacks, not the design.
- **One install, composable inside.** A single `pi install` gets the suite;
  each piece works alone and earns its place alone.
- **Autonomy over approval.** The answer to risk is a safety envelope with
  easy defaults, never click-to-approve ceremony.
- **Parity by value, not by checklist.** Daily-driver is the destination, but
  each capability ships when it's the next most valuable thing — judged by
  actually living in pi, not by a claude-code comparison table.
- **Pin pi like a protocol.** Version the seam, gate on compat — the same
  reflex as attn's `ProtocolVersion`.
- **The agent stays a guest in attn's house.** attn owns the PTY, the session
  lifecycle, and the outer harness. pi owns the loop, the models, the context.
  The seam is the socket.

## Scope & non-goals

**In scope:** the attn-side driver plugin; the pi-side attn suite carrying the
capabilities above; multi-model daily use; living in it.

**Non-goals:** forking or vendoring pi; RPC-mode embedding and attn-rendered
conversations; a click-to-approve permissioning system; MCP support before
something needs it; migrating claude/codex/copilot integrations to this
pattern; parity for claude-code features Victor doesn't actually use.

## Big rocks (the arc)

Each rock opens with its own alignment + implementation doc.

- [ ] **Driver plugin** — pi launches, resumes, and lives as an attn session.
- [ ] **attn citizenship** — linking, declared state, doorbell/nudge steering.
- [ ] **Safety envelope + auto mode** — the autonomy dial.
- [ ] **Subagents** — adapted from ecosystem prior art.
- [ ] **Background eyes** — monitors that wake the agent.
- [ ] **Skills curation** — carry over what matters.
- [ ] **Daily-driver trial** — live in it; the feedback loop that reorders
      everything above.
- [ ] **Compaction tuning and workflows** — likely last; needs the most
      design.

## Open questions

- How much of the existing skills corpus carries over unmodified, and is a
  shared skills directory the right move?
- Does the stop-time classifier still run for pi sessions once state is
  declared, or does it become redundant?
- Packaging granularity: one package with many extensions, or a small family
  under one install? Decide when the second capability lands.
- The safety envelope's exact policy vocabulary — designed fresh in its own
  doc, not inherited from claude's permission modes.
- **Blindspots (ground before the first chunk):** pi's extension runtime under
  long-lived real sessions; pi's release cadence and API stability in
  practice; how pi's TUI behaves under attn's PTY geometry rules compared to
  claude/codex.
