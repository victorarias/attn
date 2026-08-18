# Vision: pi as the daily-driver harness — the attn plugin suite

> The what and the why. Each capability below gets its own implementation-level
> document when its turn comes; none of that detail lives here. Grounding for
> the decisions: pi's extension surface ([earendil-works/pi](https://github.com/earendil-works/pi)),
> openclaw's history of embedding pi ([openclaw/openclaw](https://github.com/openclaw/openclaw)),
> and attn's plugin system ([docs/plans/2026-04-16-plugin-system.md](../plans/2026-04-16-plugin-system.md),
> with the earlier pi plan [2026-04-07-pi-integration.md](../plans/2026-04-07-pi-integration.md)).

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

The conversation itself is attn's own surface: it reads with the density of a
terminal transcript, but it's built from real UI — tool calls that expand,
diffs that render, images inline, scroll that behaves. The terminal remains
for harnesses that live there; pi's sessions are drawn by the app itself.

Why it matters: attn's model is many agents, one Victor. Today the harness
underneath is someone else's product — its roadmap, its model lock-in, its
opaque state. pi is small, open, and extensible at every seam that matters.
Owning the harness layer as *plugins* — not a fork — buys the leverage without
the maintenance treadmill.

## The central decision: a plugin suite

**One main attn package for pi, composing focused extensions** — installed
with a single `pi install`, each capability a piece that stands alone.
Everything this vision wants maps onto pi's supported extension surface; the
verified feature-by-feature mapping informs each capability's own doc.

Plugins are the whole bet: attn wants signals, steering, capabilities, and its
own rendering — never ownership of the agent loop. openclaw's history of
vendoring pi is the price list for crossing that line.

## The shape: two plugin systems meet in the middle

Both sides are plugins; neither harness gets forked or patched:

- **attn side** — the driver plugin provides a headless host entrypoint
  (Bun, in-repo) that runs pi via `createAgentSession`; the daemon spawns it
  per session and owns its process group and lifecycle.
- **pi side** — the attn suite, loaded into the host's pi runtime, carrying
  the in-loop capabilities.

The PTY/terminal path stays for claude, codex, and shell sessions.

State flips from inference to declaration: today attn *deduces* claude's state
from hooks, heuristics, and a stop-time classifier; the pi extension *reports*
it — declared state is authoritative, with no classifier fallback. What
survives of classification is a service, not a guess: when the agent stops,
the plugin can ask attn to read the last turn and enrich the stop — done, or
waiting on a reply — so the user understands *why* the session went quiet.

Two streams come out of the host: a small typed semantic stream in attn's own
vocabulary — session linked, run started/settled, state, tool started/
finished, message committed — that the daemon understands and every attn
feature integrates on; and a render stream — streaming deltas, tool detail —
that the daemon routes without parsing, typed in TypeScript shared between
host and app.

## The capabilities (each gets its own doc)

- **Autonomy with a safety envelope.** A simple policy declares what's
  inherently safe: the worktree pi has open is the agent's to read and write,
  no ceremony. Everything outside the envelope — bash, anything that reaches
  further — rides auto mode. Easy, safe defaults; pressure off.
- **Subagent orchestration.** Fan-out and delegation inside the session.
  Prior art exists in the pi ecosystem — we adapt, not invent.
- **Skills and commands.** pi speaks the same skills spec; the existing
  corpus carries over as-is for the trial, curated from real use rather than
  ported upfront.
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
- **The agent stays a guest in attn's house.** attn owns the process, the
  session lifecycle, and the outer harness. pi owns the loop, the models, the
  context. The seam is the socket.
- **attn integrates on declarations, never on renderings.** The daemon's
  picture of a session must be complete without ever reading a render event;
  deltas exist only so the app can paint.

## Scope & non-goals

**In scope:** the attn-side driver plugin; the pi-side attn suite carrying the
capabilities above; multi-model daily use; attn-rendered conversations via the
pi SDK; living in it.

**Non-goals:** forking or vendoring pi; a click-to-approve permissioning
system; MCP support before something needs it; migrating claude/codex/copilot
integrations to this pattern; parity for claude-code features Victor doesn't
actually use.

## Big rocks (the arc)

Each rock opens with its own alignment + implementation doc.

- [x] **Driver plugin** — pi launches, resumes, and lives as an attn session.
- [x] **attn citizenship** — linking, declared state, doorbell/nudge steering.
- [ ] **Headless host + rendered conversation** — the SDK host process and the
      React conversation surface, built in vertical slices (own plan doc).
- [x] **Safety envelope + auto mode** — the autonomy dial.
- [ ] **Subagents** — adapted from ecosystem prior art.
- [ ] **Background eyes** — monitors that wake the agent.
- [ ] **Skills** — trial the existing corpus as-is; curate from real use.
- [ ] **Daily-driver trial** — live in it; the feedback loop that reorders
      everything above.
- [ ] **Compaction tuning and workflows** — likely last; needs the most
      design.
