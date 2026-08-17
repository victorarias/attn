// The apps the reconcile exit proof installs.
//
// Every one of them starts as `attn app new` output and is then rewritten from
// here, so the scaffold is on the critical path of the proof rather than beside
// it: a scaffold that stops applying cleanly fails the scenario at its first
// step.
//
// The caller names them, because an app's documents and version history outlive
// `attn app remove`: a scenario that reused one fixed name would read the last
// run's documents and turn its own first install into a version move.
//
// They subscribe to `ticket.*` because tickets are the one domain a scenario can
// drive deterministically from the CLI without an agent in the loop, and because
// `ctx.current.snapshot().tickets` is the current-state read a reconcile is
// supposed to rebuild from.

// steward derives one document per ticket. Version 1 stores the title; version 2
// derives a second field from the same facts, which is the version-change
// trigger's whole reason to exist: the documents version 1 wrote have no
// `status`, and nothing but a rebuild will ever give them one.
export function stewardManifest({ name = 'steward', reconcile = true } = {}) {
  return `name = "${name}"
description = "Derives one document per ticket. The reconcile exit proof's converging app."

attn_app_api = 1
entrypoint = "src/index.ts"
${reconcile ? 'reconcile = true\n' : ''}
[[subscribe]]
events = ["ticket.*"]

[[collections]]
name = "tickets"
fields = ["state"]

[[commands]]
name = "refresh"
description = "Ask the app to say what it holds."
`;
}

// derive is the one function the two versions differ in, and the only difference
// between them: same subscription, same collection, same reconcile shape.
export const STEWARD_V1_DERIVE = `function derive(ticket: TicketLike): Record<string, unknown> {
  return { state: "seen", title: ticket.title }
}`;

export const STEWARD_V2_DERIVE = `function derive(ticket: TicketLike): Record<string, unknown> {
  // Version 2 derives a field version 1 never wrote. Every document already in
  // the collection is wrong until reconcile rebuilds it.
  return { state: "seen", title: ticket.title, status: ticket.status }
}`;

// blockGuard makes a reconcile that does not finish until a ticket appears,
// which is how a rebuild is caught mid-flight by a daemon that goes away
// underneath it. The signal is a ticket rather than a file because a handler
// typechecks against attn's own types and has no runtime globals to reach a
// filesystem with — and current truth is a read the rebuild already does.
// It yields while it waits: a non-yielding loop would be the dispatch timeout's
// case, which is pinned in Go, not here.
function blockGuard(releaseTicketId) {
  const id = JSON.stringify(releaseTicketId);
  return `  for (;;) {
    const gate = await ctx.current.snapshot()
    if (gate.tickets.some((row) => row.id === ${id})) break
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
`;
}

export function stewardEntrypoint(derive, { blockUntilTicket = null } = {}) {
  return `import type { Ctx, Handlers } from "./generated"
import type { AppEvent, ReconcileReason } from "@victorarias/attn-app"

interface TicketLike {
  readonly id: string
  readonly title: string
  readonly status: string
}

${derive}

async function onTicket(event: AppEvent, ctx: Ctx): Promise<void> {
  const current = await ctx.current.snapshot()
  const ticket = current.tickets.find((row) => row.id === event.subject)
  if (!ticket) {
    // The ticket is gone in current truth, so the fact was its removal.
    await ctx.collections.tickets.delete(event.subject)
    return
  }
  await ctx.collections.tickets.put(ticket.id, derive(ticket))
}

async function refresh(_payload: unknown, ctx: Ctx): Promise<{ held: number }> {
  return { held: (await ctx.collections.tickets.query({ limit: 1000 })).length }
}

async function reconcile(reason: ReconcileReason, ctx: Ctx): Promise<void> {
${blockUntilTicket ? blockGuard(blockUntilTicket) : ''}  const current = await ctx.current.snapshot()
  const live = new Map(current.tickets.map((row) => [row.id, row]))
  // Deleting what current truth no longer has is half the job. A rebuild that
  // only upserts leaves rows nothing will ever remove.
  for (const doc of await ctx.collections.tickets.query({ limit: 1000 })) {
    if (!live.has(doc.id)) {
      await ctx.collections.tickets.delete(doc.id)
    }
  }
  for (const ticket of live.values()) {
    await ctx.collections.tickets.put(ticket.id, derive(ticket))
  }
  console.log("steward: rebuilt " + live.size + " through seq " + reason.throughSeq)
}

export default {
  subscriptions: { "ticket.*": onTicket },
  commands: { refresh },
  reconcile,
} satisfies Handlers
`;
}

// historian subscribes and declares no reconcile. It is the app the loud paths
// are about: a version move it cannot survive is refused before the pointer
// moves, and a gap it cannot heal disables it without moving its cursor.
export function historianManifest({
  name = 'historian',
  description = 'Accumulates what it is told. Declares no reconcile on purpose.',
} = {}) {
  return `name = "${name}"
description = "${description}"

attn_app_api = 1
entrypoint = "src/index.ts"

[[subscribe]]
events = ["ticket.*"]

[[collections]]
name = "seen"
fields = ["state"]
`;
}

export function historianEntrypoint() {
  return `import type { Ctx, Handlers } from "./generated"
import type { AppEvent } from "@victorarias/attn-app"

// A history app: what it holds is what it was told, in the order it was told.
// No snapshot rebuilds this, which is exactly why attn refuses to move it across
// a trigger it cannot survive.
async function onTicket(event: AppEvent, ctx: Ctx): Promise<void> {
  await ctx.collections.seen.put(String(event.seq), {
    state: "seen",
    subject: event.subject,
    name: event.name,
  })
}

export default {
  subscriptions: { "ticket.*": onTicket },
} satisfies Handlers
`;
}
