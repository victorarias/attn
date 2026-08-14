// Running one handler: load the version's bundle, find the handler the daemon
// named, and call it with a context scoped to the app.
//
// Two rules shape this file.
//
// The daemon decides which handler runs. It holds the frozen declaration and the
// same pattern-matching the bus filter uses, so resolving an event name to a
// subscription here would be a second implementation of a rule that already
// exists — free to drift, and drifting silently.
//
// A version is a path. Versions are content-addressed, so every distinct build
// lives at its own absolute path and `import()` caches by path: a new version is
// a fresh module by construction, and an in-flight dispatch keeps running the one
// it started on. Nothing unloads, nothing is invalidated, and a stale bundle
// cannot be served — the failure mode a reload-by-name cache exists to fight
// simply has no way to occur here.

import { pathToFileURL } from "node:url"
import { RpcConnection } from "./rpc.ts"

/** What the daemon sends to run one handler. */
export interface DispatchParams {
  /** The daemon's id for this dispatch. Every callback carries it back. */
  dispatch: string
  app: string
  version_id: number
  /** Absolute path to this version's bundle. */
  artifact: string
  /** The subscription pattern to invoke — a key of the bundle's default export. */
  handler: string
  /** The collections this version declared, by name. */
  collections: string[]
  event: {
    name: string
    subject: string
    seq: number
    payload: unknown
    published_at: string
  }
}

/** What the daemon gets back. A handler that threw is a result, not an RPC error. */
export interface DispatchResult {
  ok: boolean
  error?: string
}

/** What the daemon sends to run one command a view invoked. */
export interface CommandParams {
  dispatch: string
  app: string
  version_id: number
  artifact: string
  /** The `command:<name>` key to invoke — a key of the bundle's default export. */
  handler: string
  collections: string[]
  /** The caller's argument, already parsed. Absent when the command takes none. */
  payload?: unknown
}

/** What the daemon gets back from a command, plus whatever the handler returned. */
export interface CommandResult {
  ok: boolean
  error?: string
  payload?: unknown
}

type Handler = (event: DispatchParams["event"], ctx: unknown) => unknown

/**
 * Bundles already imported, by absolute path. It only ever grows, which is
 * correct for the lifetime of one runtime: a version that ran is a version that
 * can run again on a rollback, and the entries are small. A runtime restart is
 * what empties it, and the supervisor provides those.
 */
const modules = new Map<string, Promise<Record<string, Handler>>>()

/**
 * Which app each loaded bundle belongs to. It is how an error that escaped every
 * handler is traced back to whose code it came from: a rejection can surface long
 * after the dispatch that started it returned, so "which app is running" names an
 * innocent, while the stack still carries the content-addressed bundle path.
 */
const appByArtifact = new Map<string, string>()

/**
 * The app whose bundle appears in this stack, if any.
 *
 * Empty when nothing matches — a rejection from the host's own code, or a reason
 * that is not an Error and carries no stack at all. Nothing is charged then;
 * guessing is worse than not knowing.
 */
export function appForStack(stack: string): string {
  for (const [artifact, app] of appByArtifact) {
    if (stack.includes(artifact)) return app
  }
  return ""
}

async function loadHandlers(artifact: string): Promise<Record<string, Handler>> {
  let pending = modules.get(artifact)
  if (!pending) {
    pending = importHandlers(artifact)
    modules.set(artifact, pending)
    // A bundle that fails to import must not be remembered as broken forever: the
    // artifact may be mid-write, or the disk may have had a bad moment, and the
    // bus is going to retry this delivery.
    pending.catch(() => modules.delete(artifact))
  }
  return pending
}

async function importHandlers(artifact: string): Promise<Record<string, Handler>> {
  const module = (await import(pathToFileURL(artifact).href)) as { default?: unknown }
  const handlers = module.default
  if (!handlers || typeof handlers !== "object") {
    throw new Error(
      `${artifact} has no default export; an app's entrypoint default-exports its handlers, as \`export default { "session.state.changed": onChange } satisfies Handlers\``,
    )
  }
  return handlers as Record<string, Handler>
}

/** Builds `ctx.collections`, one object per collection the version declared. */
function collectionsFor(
  conn: RpcConnection,
  dispatch: string,
  declared: string[],
): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const name of declared) {
    // Every call carries the dispatch id and the collection, and no namespace:
    // the daemon resolves which app is asking from its own record of the dispatch
    // in flight, so an app cannot name a namespace at all, let alone another's.
    const call = (op: string, params: Record<string, unknown>) =>
      conn.call(`app.collection.${op}`, { dispatch, collection: name, ...params })
    out[name] = {
      get: (id: string) => call("get", { id }),
      put: (id: string, body: unknown, options?: { ifRev?: number }) =>
        call("put", { id, body, if_rev: options?.ifRev }),
      delete: (id: string) => call("delete", { id }),
      query: (options?: unknown) => call("query", { query: options ?? {} }),
      count: (options?: unknown) => call("count", { query: options ?? {} }),
    }
  }
  return out
}

/**
 * Runs one dispatch and describes what happened.
 *
 * A handler that throws produces `{ok: false, error}` — a normal answer, not an
 * RPC failure. The distinction is what lets the daemon tell an app's fault from
 * the runtime's: only a transport error or a dead process reaches the daemon as
 * an RPC failure, and only those are the runtime's to answer for.
 */
export async function runDispatch(
  conn: RpcConnection,
  params: DispatchParams,
): Promise<DispatchResult> {
  appByArtifact.set(params.artifact, params.app)

  let handlers: Record<string, Handler>
  try {
    handlers = await loadHandlers(params.artifact)
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  }

  const handler = handlers[params.handler]
  if (typeof handler !== "function") {
    return { ok: false, error: missingHandler(handlers, params.app, params.version_id, params.handler) }
  }

  const ctx = {
    app: params.app,
    version: params.version_id,
    collections: collectionsFor(conn, params.dispatch, params.collections),
  }
  // Announced before the call, because a handler that never yields would keep any
  // later announcement from ever being written. This is the daemon's only witness
  // of which handler is on the event loop, and it needs it exactly when this
  // process has stopped answering everything else.
  //
  // Both halves matter. Without the second, an entry outlives the handler and
  // names it for a freeze it is no longer part of; the daemon would have to guess
  // when a handler left, and the only signal it could guess from — the loop still
  // turning — says nothing about a handler that yielded and never settled.
  const scope = { dispatch: params.dispatch, app: params.app }
  conn.notify("app_runtime.entered", scope)
  try {
    await handler(params.event, ctx)
    return { ok: true }
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  } finally {
    conn.notify("app_runtime.left", scope)
  }
}

/**
 * Runs one command a view invoked, and describes what happened.
 *
 * It is runDispatch with a different argument and an answer that carries a
 * value. Everything that makes a handler run — the module cache keyed by the
 * content-addressed artifact, the app-scoped collections, the entered/left
 * announcements that let the daemon name whoever froze the shared loop — is the
 * same code, because a command is one more key of the same default export.
 */
export async function runCommand(
  conn: RpcConnection,
  params: CommandParams,
): Promise<CommandResult> {
  appByArtifact.set(params.artifact, params.app)

  let handlers: Record<string, Handler>
  try {
    handlers = await loadHandlers(params.artifact)
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  }

  const handler = handlers[params.handler] as
    | ((payload: unknown, ctx: unknown) => unknown)
    | undefined
  if (typeof handler !== "function") {
    return { ok: false, error: missingHandler(handlers, params.app, params.version_id, params.handler) }
  }

  const ctx = {
    app: params.app,
    version: params.version_id,
    collections: collectionsFor(conn, params.dispatch, params.collections),
  }
  const scope = { dispatch: params.dispatch, app: params.app }
  conn.notify("app_runtime.entered", scope)
  try {
    const payload = await handler(params.payload, ctx)
    // undefined is "returned nothing", and JSON has no word for it: leaving the
    // field off is what tells the caller that apart from a handler that
    // deliberately returned null.
    return payload === undefined ? { ok: true } : { ok: true, payload }
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  } finally {
    conn.notify("app_runtime.left", scope)
  }
}

/**
 * What to say when the manifest declared something the bundle does not export.
 *
 * The generated Handlers type makes this a compile error at apply time, so
 * reaching it means the bundle is out of step with the declaration it was
 * stored beside — worth naming exactly, because nothing else in the system can.
 */
function missingHandler(
  handlers: Record<string, unknown>,
  app: string,
  version: number,
  key: string,
): string {
  const declared = Object.keys(handlers)
  return (
    `app ${app} version ${version} declares ${key} but its default export has no handler under that key. ` +
    (declared.length > 0
      ? `It exports: ${declared.join(", ")}.`
      : "It exports no handlers at all.") +
    " The generated Handlers type makes this a compile error — the bundle is out of step with its manifest."
  )
}

function describeFailure(err: unknown): string {
  if (err instanceof Error) return err.stack?.trim() || `${err.name}: ${err.message}`
  return String(err)
}
