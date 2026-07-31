# Brainstorm + spike: attn as a self-extensible platform

> Status: **brainstorm and spike complete; design forks open.** Not a plan yet.
> The spike lives in `spike-ext/` and is deleted when the real thing lands.

## The question

Can an agent *inside* attn extend attn — not by shipping a plugin directory and
restarting a supervised process, but dynamically: subscribe to something that
happens, put up a purpose-built UI, take the human's decision, and act on it?

The motivating case (a delegation whose prompt gets reviewed before the
delegate starts) is deliberately **not** the point. It is the first tenant. The
point is that the next twenty ideas like it should not each be a core PR.

## What attn already has

The surprising finding of this pass: attn is most of the way to a platform
already. Every piece exists — each one built vertically for a single feature,
and none of them open.

| Capability | Where it lives | Open to extensions? |
| --- | --- | --- |
| Typed event stream (~100 `*_changed` / `*_updated` broadcasts) | `internal/daemon`, `internal/protocol` | No — hardcoded to the app |
| Supervised out-of-process extensions (JSON-RPC, health, generation tokens, priority) | `internal/daemon/plugin_rpc.go`, `sdk/plugin` | No — closed 4-surface allowlist |
| Chain-of-responsibility dispatch (`handled` / `decline` / `error`) | `plugin_worktree.go` | Exists, worktrees only |
| A hardened, sandboxed JS runtime (goja: event loop, watchdog, panic mapping, realm hygiene) | `internal/workflow` | No — workflow-only |
| Declarative trigger → agent launch | `internal/automation` | No — closed 3-value trigger enum |
| Agent-authored UI + structured human feedback + blocking wait | `internal/present`, `attn present --wait` | No — `kind: changes` (git diffs) only |
| Durable async human↔agent messaging (comments, subscribers, doorbells) | tickets | Yes, in practice |
| Non-terminal UI surfaces mounted in workspace splits (`tile_kind` + `tile_params`) | `app/src/components/SessionTerminalWorkspace` | No — kind-switched in React |
| An embedded browser tile with a CLI automation surface | `app/src/browser`, `attn browser` | Yes, in practice |
| A second OS window with its own WS client and root component | `PresentRoot` | No — presentations only |

`present --wait` deserves special mention: it is already a complete
agent→human→agent structured-interaction loop with verdicts
(`approved` / `feedback`), pinned content, rounds, and a blocking CLI. It is the
shape the platform wants, frozen at one content type.

## The three gaps

Everything missing reduces to three things, and attn has zero of each in open
form:

**A — no open event catalog.** Events exist but are addressed to the app.
Nothing can subscribe by name. Automations' trigger enum is
`manual | scheduled | github_review_requested` and closed.

**B — no interception seam.** Every mechanism today is either *reactive*
(fires after the fact — automations, doorbells) or *voluntary* (the agent
chooses to call `attn present`). Nothing can **block** a daemon operation
pending an external decision. The delegation-gate idea needs exactly this, which
is why it felt like it had nowhere to live. The plugin `handled`/`decline` chain
is the only existing instance of this shape — and it is the right thing to
generalize.

**C — no extension-authored UI.** Present renders diffs; tiles are
kind-switched in React. Nothing lets an extension define a surface.

## The spike

`spike-ext/` is a throwaway host — attn's own event loop and watchdog from
`internal/workflow/loop.go`, stripped of the journal, resume, determinism bans
and `agent()`, with extension host fns installed instead. It runs a real
agent-authored extension:

```js
on("delegation.before_start", async (ev) => {
  if (ev.brief.length < 120) return allow();          // cheap policy, no UI

  const answer = await ask({
    title: "Approve delegation to " + ev.agent + "?",
    view: [
      { kind: "markdown", text: "**Ticket:** " + ev.ticket_id },
      { kind: "code", lang: "text", text: ev.brief },
      { kind: "textarea", id: "feedback", label: "What should change?" },
    ],
    actions: [
      { id: "approve", label: "Approve", primary: true },
      { id: "reject",  label: "Send back" },
    ],
  });

  if (answer.action === "approve") return allow();
  return deny({ feedback: answer.fields.feedback });
});
```

Run it with `go run ./spike-ext`. All five questions pass:

1. **Event → handler → verdict works.** The daemon dispatches a named event with
   a typed payload; the handler runs and returns a structured decision.
2. **A handler can block a core operation on a human and resume with
   structured feedback.** The dispatch took 1.5s — exactly as long as the
   simulated human — and `deny({feedback})` crossed back into Go intact, as did
   the view tree on the way out.
3. **Blocking does not trip the watchdog.** A handler with a *50ms* JS budget
   survived a 1.2s human pause. This is the load-bearing discovery, and it falls
   straight out of the existing loop design: while parked on `await ask(...)`
   the loop goroutine is blocked on a Go channel receive, not inside goja, so
   `vm.Interrupt` cannot reach it. attn's own comment in `loop.go` already
   documents this for `agent()`.
4. **A runaway handler is still caught.** `while(true){}` is interrupted at the
   budget rather than wedging the daemon.
5. **An unanswered gate degrades safely.** Nobody answers → bounded timeout →
   the daemon falls back to policy.
6. **The cheap path costs the human nothing.** A low-stakes brief auto-allowed
   with zero UI, zero interruptions, in ~0ms.

### What the spike changes about the design

The workflow engine cannot be reused as-is: its determinism bans (`Date.now()`,
`Math.random()`) and journal exist to make expensive `agent()` calls
resume-cacheable, which an event handler neither needs nor wants.

But it does not need to be. **A handler invocation is one-shot**: event arrives
→ run to completion → discard the VM. That maps onto the engine's execution
model almost exactly. What changes is the host fn set and the timeout policy;
what is dropped is the journal and the bans. Long-lived extension state belongs
in the daemon (a small per-extension KV), never in a resident VM — which
sidesteps the "long-lived handler" problem entirely.

So the extension runtime is a **second realm profile over known-good code**
(~300 lines of loop/watchdog/panic-mapping that already exist and are already
hardened), not a research project.

## Design forks

### Fork 1 — Where does extension code run?

- **(a) In-daemon goja.** An agent writes one `.js` file. No install, no
  process, no build, no restart. Spiked and working.
- **(b) Out-of-process plugin.** Exists today. Full power (fs, network, npm),
  but a directory, a manifest, an install and a supervised process — heavy for
  an agent to author, and slow to iterate.

**Recommendation: both, one catalog — but ship (a) first.** goja is the paved
road for agent-authored extensions and is the direct answer to "more dynamic
than a plugin". Plugins stay for anything needing the filesystem, the network,
or real dependencies. Crucially they share one event catalog and one set of
surface kinds, so an extension can graduate from script to plugin without the
platform changing shape.

### Fork 2 — How does an extension render UI?

- **(a) View tree over a registered component catalog.** The extension emits
  JSON from a schema-validated vocabulary (`markdown`, `code`, `diff`,
  `textarea`, `radio`, actions); attn renders it with native React. Safe,
  themed, keyboard-first, testable, works in a tile *and* the present window.
  Cost: every genuinely new widget is a core PR — the exact trap worth escaping.
- **(b) Sandboxed HTML in the existing browser tile.** Unlimited expressiveness,
  zero core PRs forever, and the webview tile plus `attn browser` automation
  already exist. Cost: theming and keyboard drift, a real security surface (CSP
  plus a capability-scoped bridge), harder to test.

**Recommendation: (a) as the paved road, (b) as a declared escape hatch** — an
`html` entry in the same catalog. Grow the vocabulary from evidence: whatever
people keep reaching into the escape hatch for is the next native component.

On **tambo**: the useful half is the contract — *a registry of components with
schemas, filled and validated at the boundary*. That is fork (a), and it is
worth copying. The other half — an LLM choosing components at runtime, hosted
thread state, streaming props — attn does not need: attn's agents author the
tree deliberately, and attn already owns its state and transport. Take the
schema discipline; skip the generative middleman.

### Fork 3 — What may an extension intercept?

Three interaction kinds, all of which the plugin layer already implements in
some form:

- `observe` — fire-and-forget notification.
- `decide` — chain of responsibility (`handled` / `decline`), as
  `worktree.create` works today.
- `gate` — the daemon awaits `allow` / `deny(+data)`, with a mandatory timeout
  and a declared default.

**The highest-leverage single change in the whole platform is turning
`validatePluginSurfaces`'s closed switch into an open, versioned catalog** of
`(event name, payload schema, interaction kind)`. That one move converts an
existing, tested dispatch mechanism into an extension point.

### Fork 4 — Which events ship first?

Not a mechanical mapping of the ~100 broadcasts. Start with a handful of real
ones behind real tenants — `delegation.before_start` is the obvious first, and
it is the one that has no home today.

## Tensions and risks worth naming now

**This collides with a stated principle.** `docs/vision/pi-attn-plugins.md`
says: *"Autonomy over approval. The answer to risk is a safety envelope with
easy defaults, never click-to-approve ceremony."* A gate platform is, mechanically,
a click-to-approve machine. I do not think that kills the idea — that principle
is about *tool permissioning inside an agent's loop*, and this is about a human
reviewing work he is about to spend an hour of agent time on. But the platform
should be built so the principle survives: **policy first, UI only on the
expensive path**. The spike's cheap path (zero asks, zero milliseconds, on a
low-stakes brief) is that principle expressed in code, and it should be the
documented default shape of every gate — not an optimization.

**Three extension mechanisms is one too many.** Plugins, automations, and now
extensions. Automations are already `(closed event set) → (spawn an agent)` —
a strict subset of what the platform does. I am not proposing to rewrite
automations; I am flagging that the platform must have a consolidation story on
day one, or attn grows a third half-overlapping way to do the same thing.

**Versioning.** Extensions bind to an event catalog. That catalog needs the
same fail-explicitly discipline as `ProtocolVersion`, or a daemon upgrade
silently changes what a gate sees.

**Blast radius.** Agent-authored code that can block core operations and spawn
agents needs capability grants, a per-extension kill switch, and observability —
a wedged gate must be diagnosable in one look, not inferred from a stalled
delegation.

**What the platform must not become.** A general-purpose app-scripting layer.
The test for every proposed primitive: does it serve *agents extending attn for
Victor*, or is it a plugin API for a hypothetical third party?

## Settled (2026-07-31)

1. **Runtime — goja-first, plugins kept.** Agent-authored extensions are a
   single `.js` file run in-daemon. Out-of-process plugins stay for anything
   needing filesystem, network, or real dependencies, sharing one event catalog
   so an extension can graduate without the platform changing shape.
2. **UI — component catalog as the paved road, `html` as a declared escape
   hatch.** Grow the vocabulary from what people keep reaching into the hatch
   for.
3. **Placement — the extension declares it.** The view tree names its own
   surface (tile / window / drawer) and attn honors it.

## Present is not a competing tenant — it is the same project

Aligned 2026-07-31: aim the platform at the prompt-approval gate **and** at
Present.

This looked like a scope grab and is closer to the opposite. Present's own
unbuilt rocks already contain both of the platform's key primitives:

| Present rock (unbuilt) | Platform primitive |
| --- | --- |
| Gates — presentations that block; rounds that require approval | the `gate` interaction kind |
| HTML artifacts — sandboxed rendering + annotation | the `html` escape hatch |
| Document reader; Rendering palette | the component catalog |
| Feedback vocabulary | the interaction's structured response model |

And Present's own open question — *"Gate ↔ `pending_approval` interplay: is a
gated presentation a session state, a ticket state, or its own thing?"* — is
precisely the question the platform's `gate` kind exists to answer. Its
principle *"One spine, many uses. A new use is a new manifest shape, not a new
subsystem"* is the platform's thesis, stated a month early and scoped to one
content type.

The structural fact: **a Present manifest is already a view tree with a single
component kind (`changes`), a declared placement (its own window), and a
verdict-carrying response.** The platform is that manifest generalized — a
catalog instead of one kind, an event catalog instead of only agent-initiated
opens, and `gate` instead of only advisory `--wait`.

So the two tenants stress the platform along different axes, which is exactly
what a first release wants:

- **`delegation.before_start`** proves the *event and interception* half: a
  named event, a blocking gate on a core daemon operation, a structured verdict
  flowing back, and a cheap policy path that never interrupts the human.
- **Present** proves the *expressiveness* half: if the catalog can host attn's
  most demanding review UI — diffs, inline annotation anchoring, keyboard-first
  traversal, rounds and drift — it is a platform rather than a form renderer.

Present is therefore the platform's first-party surface, not an extension
competing with it.

### Migration posture: free hand

Aligned 2026-07-31: Present is **not yet adopted** — the change reader is not
working well enough for daily use, and Victor is explicit that we can do
whatever we want with it.

That retires the constraint behind the cautious option. "Generalize in place"
was recommended to avoid a daily-review regression window; there is no daily
review to regress. So the platform does **not** inherit Present's manifest
schema as its contract. That schema was designed for exactly one content type,
and treating it as the platform's foundation would bake a single-tenant shape
into a multi-tenant catalog.

Instead: design the catalog from first principles, and treat Present's *use
cases* — change review, document reading, teaching — as its first tenants.
What carries over is the hard-won machinery, not the schema:

- the second-window shell (`PresentRoot`, Tauri multi-window lifecycle, WS
  client, shortcut routing) — a solved blindspot, reused as a placement
- diff rendering and inline commenting — the heaviest component in the catalog
- rounds, pinned SHAs and drift — a genuine concept, re-expressed as
  interaction state
- the doorbell/`--wait` agent loop — becomes the generic `gate` response path

The shipped change reader stays working until its replacement is better; it is
simply no longer a constraint on the design.

**Open risk to carry into the plan:** Present not reaching adoption is
unexplained. Rebuilding it on the platform without knowing *why* it stalled
would port the failure into the foundation. The plan must name the adoption
gap before it commits to a first-party review surface.

## Settled, round two (2026-07-31)

4. **Interaction kinds — open all three at once** (`observe` / `decide` /
   `gate`). The catalog is complete from day one and the existing worktree
   surfaces migrate onto the same mechanism rather than remaining a private
   fourth path.
5. **Present — free hand.** Not adopted, so not a constraint. Catalog designed
   from first principles; Present's machinery reused, its schema not inherited.
6. **Automations — commit now, migrate later.** The vision states that
   automations become a platform tenant, and the event catalog is designed so a
   scheduled trigger and a `github_review_requested` trigger are expressible as
   events. The working v2 automations code is not touched in this effort.

## Still open

1. Why Present has not reached adoption — the answer shapes what the
   first-party review surface must actually do.
