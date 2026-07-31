# Plan: attn extension primitives

## Goal

Give agents inside attn a small set of **primitives** they can combine to build
interactions attn does not have. Not a feature with a platform around it — a
vocabulary.

The test of the vocabulary is not "can it build the delegation gate." It is
that things attn *already has* fall out of it as compositions, and that the
next twenty ideas are compositions too rather than core PRs.

## The primitives

| Primitive | What it does |
| --- | --- |
| `subscribe(event)` | attn emits named events; an extension listens |
| `block` | a subscriber makes attn wait for its answer before continuing |
| `ask(ui)` | render React, get a structured answer back |
| `render(ui)` | mount React somewhere persistent, not tied to a question |
| `state` | durable per-extension storage |
| `act` | call attn's own commands — delegate, ticket, comment, session |
| `think` | invoke an agent and use its answer |

**`block` and `ask` are orthogonal.** An earlier draft fused them into an
`observe | decide | gate` taxonomy; that was a list of use cases, not
primitives, and it kept collapsing the design back onto one feature. Blocking
without asking is a code-only decision. Asking without blocking is a
notification that wants an answer. Both together is a gate.

### The set validated against what already exists

```text
automations                 = subscribe + think
present                     = render + ask + state
worktree provider plugins   = subscribe + block
delegation prompt approval  = subscribe + block + ask
a custom queue view         = render + state
nightly digest of idle work = subscribe + think + state
```

Automations and the worktree plugins are **working features** — this vocabulary
describes them, it does not replace them. Where an existing mechanism is a
composition of these primitives, converging later is a rename, not a rewrite.

## Where extension code runs

**Handlers are supervised bun processes on the existing plugin chassis.**

attn already made this call: `internal/daemon/daemon.go:342` — *"The engine runs
in a separate process (the `attn workflow run` CLI)."* The goja engine in
`internal/workflow` is deliberately out-of-daemon, and an earlier draft of this
plan reversed that without noticing.

The deciding argument is the same one that killed the YAML view-model: **a
restricted bespoke runtime is another thing agents don't know.** goja is an
ES2017-era realm with no `fetch`, no `console.log`, no `setTimeout`, no npm and
no debugger — every API has to be hand-built by attn and hand-learned by the
agent. bun is the environment agents already write for, and attn already runs
plugins in it (`internal/daemon/plugin_supervisor.go`).

Reused rather than rebuilt: `plugin_rpc.go` (JSON-RPC, handshake, generation
tokens, priority-ordered registry), `plugin_supervisor.go` (crash restart with
bounded backoff — this *is* hot reload), `sdk/plugin` (the authoring SDK).

## What already exists for each primitive

| Primitive | Today | Gap |
| --- | --- | --- |
| `subscribe` | ~100 daemon broadcasts; 4 closed plugin surfaces; 3 closed automation triggers | no open, named catalog |
| `block` | `dispatchWorktreeCreateProvider` blocks a core operation on an out-of-process verdict, with timeouts, fallback and chain | worktrees only |
| `ask` | `attn present --wait` returns verdicts | diffs only, CLI only |
| `render` | tiles (`markdown`, `browser`), `PresentRoot` window | no extension-authored UI |
| `state` | — | nothing |
| `act` | the whole `attn` CLI over the socket | not reachable from a handler |
| `think` | delegation, automations spawn agents | not reachable from a handler |

`block` is the one people assume is hardest and is in fact nearly done —
`plugin_worktree.go:93` already has the shape, including a 2-minute
provider timeout and a declared fallback.

## Verdicts carry data

`allow`/`deny` is too narrow. `worktree.create` already returns `(path, branch)`
— a result that *replaces* what the daemon would have done. And the delegation
case wants **edit the prompt and approve**, not just approve or reject; without
it a one-word fix costs a full agent round-trip.

So a blocking subscriber returns `continue` | `continue_with(data)` | `stop(data)`.

## Boundaries

- One event catalog serving every subscriber, whoever they are. The catalog is
  the opened form of `validatePluginSurfaces`, not a parallel mechanism.
- Which events can be blocked is a property the **daemon author** sets per
  event, with a documented contract: where it is emitted, what state exists at
  that point, what `stop` unwinds, what happens on timeout. Opening a new
  blockable event is core work; the win is that it is one seam instead of a
  vertical feature.
- No bespoke UI data model, ever. If React plus the provided components can't
  express something, the answer is another component.
- Registration is agent-driven: files plus one command, no clicking, no restart.

## Open questions (real ones)

1. **How does agent-authored React load into the packaged app?** Bundled ESM
   through the asset protocol, with `react` externalized so there is one React
   instance. This is genuinely unproven — `build:browser-runtime` is a
   first-party bundle built at app build time, which is not the same thing.
   **Spike this before writing PR3.**
2. **Daemon restart while something is blocked.** The waiting operation does not
   survive the restart, so the honest semantics are: fail to the declared
   default and say so. `make install-daemon-dev` restarts the daemon, so this
   happens routinely and should be boring, not clever.
3. **Where in `delegateOperation` the pause sits.** That path is a compensation
   saga (`delegationRollback`) with a strict acquisition order. Before any
   resource is reserved is safe; later is not.

## Implementation Steps

One primitive per PR, so each lands with a real consumer and nothing is built
on speculation.

- [ ] **PR1 — `subscribe` + `act`.** Event catalog (opened
      `validatePluginSurfaces`), bun handlers on the existing plugin chassis,
      handler access to attn's commands, `attn ext register|list|enable|disable
      |logs`, hot reload, invocation log. *Proves: an extension reacts to
      something and does something.*
- [ ] **PR2 — `state`.** Durable per-extension storage, reachable from the
      handler. Serialized read-modify-write. *Proves: an extension remembers.*
- [ ] **PR3 — `render`.** The React host, after the spike: bundling,
      loading in the packaged app, one React instance, error boundary, hot
      reload, the provided component set (start tiny, grow from real use).
      *Proves: an extension shows something.*
- [ ] **PR4 — `ask`.** Structured answers back from a rendered surface.
      *Proves: an extension has a conversation.*
- [ ] **PR5 — `block`.** Blockable events, generalized from
      `dispatchWorktreeCreateProvider`; verdicts carrying data; timeout to
      declared default; kill switch. First blockable event:
      `delegation.before_start`. *Proves: an extension changes what attn does.*
- [ ] **PR6 — `think`.** Agent invocation from a handler.

The delegation prompt approval is the composition that falls out after PR5. It
is the proof, not the plan.

## Testing

`attn ext invoke <ext> --event <name> --payload <file>` runs a handler against a
fixture without firing a real event. Without it, an agent developing against
`delegation.before_start` debugs by spawning real delegations with real
worktrees. This is PR1 material.

## Verification

Touches daemon lifecycle, protocol, persisted state and UI, so live verification
in a running non-production app per AGENTS.md from PR1 on.
