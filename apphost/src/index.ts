// attn's shared app runtime.
//
// One supervised Bun process runs the handlers of every installed app. It is not
// a sandbox and does not pretend to be one: isolation between apps is failure
// attribution, not an OS boundary. What it owes the daemon is an honest answer
// per dispatch — this app's handler threw, or it did not — because a whole-process
// death cannot name a culprit and must never be charged to whichever app happened
// to be running.
//
// It ships as a `bun build --compile` standalone binary. The Bun runtime is inside
// the executable, so a daemon launched by the macOS app needs no PATH resolution
// and a user's machine needs no toolchain to run apps. There is deliberately no
// PATH-resolution fallback: one mechanism, one failure class.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.

import { AsyncLocalStorage } from "node:async_hooks"
import { RpcConnection, RPC_METHOD_NOT_FOUND, describe, type RpcRequest } from "./rpc.ts"
import { runDispatch, type DispatchParams } from "./dispatch.ts"

/**
 * The runtime contract this host speaks. The daemon refuses a host that does not
 * match, because a version skew between the daemon and a binary inside an old app
 * bundle is exactly the case a silent mismatch would turn into wrong behavior.
 */
const APP_RUNTIME_API_VERSION = 1

/**
 * Which app a line of output came from.
 *
 * Handler code writes to the same stdout as everything else in this process, and
 * a shared log with no attribution cannot answer `attn app logs <name>`. The
 * storage is async-scoped, so a log written after an `await` inside a handler is
 * still tagged with the app that wrote it.
 */
const currentApp = new AsyncLocalStorage<string>()

/** The tag `attn app logs <name>` filters on. Kept in step with appRuntimeLogTag in Go. */
function tag(app: string | undefined): string {
  return app ? `[app ${app}] ` : "[runtime] "
}

/**
 * Routes everything this process prints through the app tag.
 *
 * It patches console rather than asking apps to use a logger: an app author
 * writes console.log, and output that vanishes because it did not go through the
 * approved channel is worse than no capture at all.
 */
function captureConsole(): void {
  const write = (stream: NodeJS.WriteStream, args: unknown[]) => {
    const prefix = tag(currentApp.getStore())
    const text = args.map(render).join(" ")
    for (const line of text.split("\n")) stream.write(`${prefix}${line}\n`)
  }
  console.log = (...args: unknown[]) => write(process.stdout, args)
  console.info = (...args: unknown[]) => write(process.stdout, args)
  console.debug = (...args: unknown[]) => write(process.stdout, args)
  console.warn = (...args: unknown[]) => write(process.stderr, args)
  console.error = (...args: unknown[]) => write(process.stderr, args)
}

function render(value: unknown): string {
  if (typeof value === "string") return value
  if (value instanceof Error) return value.stack?.trim() || `${value.name}: ${value.message}`
  try {
    return JSON.stringify(value) ?? String(value)
  } catch {
    return String(value)
  }
}

/** Says why the process cannot start, then stops. The supervisor restarts it. */
function fatal(message: string): never {
  process.stderr.write(`${tag(undefined)}${message}\n`)
  process.exit(1)
}

function requiredEnv(name: string): string {
  const value = process.env[name]?.trim()
  if (!value) {
    fatal(
      `${name} is not set. The app runtime is launched by the attn daemon, which injects it; running this binary by hand is not a supported way to start it.`,
    )
  }
  return value
}

async function main(): Promise<void> {
  captureConsole()

  const socketPath = requiredEnv("ATTN_SOCKET_PATH")
  const generation = Number(requiredEnv("ATTN_APP_RUNTIME_GENERATION"))
  if (!Number.isSafeInteger(generation) || generation <= 0) {
    fatal(
      `ATTN_APP_RUNTIME_GENERATION is ${process.env.ATTN_APP_RUNTIME_GENERATION}, which is not a positive integer. The supervisor fences stale processes by generation, so a host that cannot present its own is refused.`,
    )
  }

  // Declared before the connection so the read loop can reach it: a dispatch can
  // arrive on the same tick the hello result does.
  let connection: RpcConnection
  const serve = async (request: RpcRequest): Promise<unknown> => {
    switch (request.method) {
      case "app.dispatch": {
        const params = request.params as DispatchParams
        // The tag follows the handler through every await it makes.
        return currentApp.run(params.app, () => runDispatch(connection, params))
      }
      case "app.runtime.ping":
        // A liveness answer the daemon can ask for without running app code.
        return { ok: true, api_version: APP_RUNTIME_API_VERSION }
      default:
        throw Object.assign(new Error(`unknown method ${request.method}`), {
          code: RPC_METHOD_NOT_FOUND,
        })
    }
  }

  connection = new RpcConnection(socketPath, serve)
  try {
    await connection.ready()
  } catch (err) {
    fatal(`cannot reach the attn daemon at ${socketPath}: ${describe(err)}`)
  }

  try {
    await connection.call("app_runtime.hello", {
      generation,
      api_version: APP_RUNTIME_API_VERSION,
      pid: process.pid,
    })
  } catch (err) {
    fatal(`the daemon refused this app runtime: ${describe(err)}`)
  }

  console.log(`app runtime ready (generation ${generation}, pid ${process.pid})`)

  // The process lives as long as its connection. A dropped socket is the
  // supervisor's business, not something to paper over with a reconnect loop
  // here: the supervisor already owns backoff, generation fencing and parking,
  // and a second reconnect policy underneath it would fight the first.
  const ended = await connection.done()
  process.stderr.write(`${tag(undefined)}connection to the daemon ended: ${ended.message}\n`)
  process.exit(1)
}

void main()
